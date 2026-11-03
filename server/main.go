// 性格色彩测试 · 局域网服务
// 运行后在本机启动 HTTP 服务，自动探测局域网地址，
// 生成二维码供同一局域网内的手机等设备扫码访问。
package main

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

//go:embed web
var webFS embed.FS

// 默认管理密码：仅存于服务端，首次运行时写入 config.json，
// 之后以配置文件为准，并可在管理后台修改（不写入网页 JS）。
const defaultAdminPwd = "12345678"
const defaultPort = 8322

/* ------------------------- 记录存储 ------------------------- */

type record struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Time      int64          `json:"time"`
	Scores    map[string]int `json:"scores"`
	Primary   string         `json:"primary"`
	Secondary string         `json:"secondary"`
	Answers   []string       `json:"answers"`
	Key       string         `json:"key"`
}

// 报告密码字符表：去掉 0/O/1/I/L 等易混淆字符
const keyChars = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

func genKey() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> uint(i*8))
		}
	}
	out := make([]byte, 6)
	for i := range out {
		out[i] = keyChars[int(b[i])%len(keyChars)]
	}
	return string(out)
}

func (s *store) hasKey(k string) bool {
	for _, r := range s.records {
		if r.Key == k {
			return true
		}
	}
	return false
}

func (s *store) findByKey(k string) (record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k = strings.ToUpper(strings.TrimSpace(k))
	for _, r := range s.records {
		if strings.ToUpper(r.Key) == k {
			return r, true
		}
	}
	return record{}, false
}

type store struct {
	mu      sync.Mutex
	path    string
	records []record
}

func (s *store) load() {
	b, err := os.ReadFile(s.path)
	if err == nil {
		var arr []record
		if json.Unmarshal(b, &arr) == nil && arr != nil {
			s.records = arr
		}
	}
}

func (s *store) saveLocked() {
	b, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

func (s *store) add(r record) record {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 幂等：同一 ID 的记录只保存一次（设备重复提交 / 离线补传时防重复）
	for _, ex := range s.records {
		if ex.ID == r.ID {
			return ex
		}
	}
	// 报告密码：为空则生成；与已有记录重复则重新生成，保证唯一
	if strings.TrimSpace(r.Key) == "" {
		r.Key = genKey()
	}
	for s.hasKey(r.Key) {
		r.Key = genKey()
	}
	s.records = append([]record{r}, s.records...)
	s.saveLocked()
	return r
}

func (s *store) all() []record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]record, len(s.records))
	copy(out, s.records)
	return out
}

func (s *store) del(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.records {
		if r.ID == id {
			s.records = append(s.records[:i], s.records[i+1:]...)
			s.saveLocked()
			return true
		}
	}
	return false
}

func (s *store) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = nil
	s.saveLocked()
}

/* ------------------------- 校验 ------------------------- */

var validColors = map[string]bool{"red": true, "blue": true, "yellow": true, "green": true}

func sanitize(r record) (record, error) {
	if len(r.Name) > 60 {
		return r, errors.New("名字过长")
	}
	if strings.TrimSpace(r.Name) == "" {
		r.Name = "匿名"
	}
	if r.Time <= 0 {
		r.Time = time.Now().UnixMilli()
	}
	if len(r.Answers) > 30 {
		return r, errors.New("answers 非法")
	}
	for _, a := range r.Answers {
		if a != "A" && a != "B" && a != "C" && a != "D" {
			return r, errors.New("answers 非法")
		}
	}
	sum := 0
	for k, v := range r.Scores {
		if !validColors[k] || v < 0 || v > 30 {
			return r, errors.New("scores 非法")
		}
		sum += v
	}
	if sum > 30 {
		return r, errors.New("scores 总分非法")
	}
	if !validColors[r.Primary] || !validColors[r.Secondary] {
		return r, errors.New("primary/secondary 非法")
	}
	if r.ID == "" || len(r.ID) > 40 {
		r.ID = "r" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	// 报告密码：仅保留合法字符集内的 6 位码，非法则交由 add() 重新生成
	r.Key = strings.ToUpper(strings.TrimSpace(r.Key))
	if len(r.Key) != 6 {
		r.Key = ""
	} else {
		for _, c := range r.Key {
			if !strings.ContainsRune(keyChars, c) {
				r.Key = ""
				break
			}
		}
	}
	return r, nil
}

/* ------------------------- 网络工具 ------------------------- */

// lanIPs 返回本机所有非环回 IPv4 地址，并按常用内网段优先排序。
func lanIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if v4 := ipnet.IP.To4(); v4 != nil {
				s := v4.String()
				if !seen[s] {
					seen[s] = true
					out = append(out, s)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return ipPref(out[i]) < ipPref(out[j]) })
	return out
}

func ipPref(ip string) int {
	switch {
	case strings.HasPrefix(ip, "192.168."):
		return 0
	case strings.HasPrefix(ip, "10."):
		return 1
	case isPrivate172(ip):
		return 2
	default:
		return 3
	}
}

func isPrivate172(ip string) bool {
	parts := strings.Split(ip, ".")
	if len(parts) < 2 || parts[0] != "172" {
		return false
	}
	n, err := strconv.Atoi(parts[1])
	return err == nil && n >= 16 && n <= 31
}

// listenOn 从 start 开始依次尝试端口，返回第一个可绑定的监听器。
func listenOn(start int) (net.Listener, int) {
	for p := start; p < start+20 && p < 65536; p++ {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err == nil {
			return ln, p
		}
	}
	return nil, start
}

/* ------------------------- 数据目录 ------------------------- */

func pickDataDir(flagDir string) string {
	if flagDir != "" {
		return flagDir
	}
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	for _, dir := range candidates {
		probe := filepath.Join(dir, ".write_test")
		if os.WriteFile(probe, []byte("1"), 0644) == nil {
			_ = os.Remove(probe)
			return dir
		}
	}
	if base, err := os.UserConfigDir(); err == nil {
		dir := filepath.Join(base, "性格色彩测试")
		_ = os.MkdirAll(dir, 0755)
		return dir
	}
	return "."
}

/* ------------------------- 配置（公网地址等） ------------------------- */

type config struct {
	mu        sync.Mutex
	path      string
	exists    bool   // config.json 是否已存在（不存在时首次运行自动生成）
	PublicURL string `json:"publicURL"`
	AdminPwd  string `json:"adminPwd"`
}

func loadConfig(path string) *config {
	c := &config{path: path}
	if b, err := os.ReadFile(path); err == nil {
		c.exists = true
		if err := json.Unmarshal(b, c); err == nil {
			c.AdminPwd = strings.TrimSpace(c.AdminPwd)
		}
	}
	return c
}

// ensureFile 首次运行时生成 config.json（写入默认管理密码）
func (c *config) ensureFile() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exists {
		return
	}
	if c.AdminPwd == "" {
		c.AdminPwd = defaultAdminPwd
	}
	c.saveLocked()
	c.exists = true
}

func (c *config) saveLocked() {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		_ = os.Rename(tmp, c.path)
	}
}

func (c *config) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.PublicURL
}

// adminPwd 返回当前管理密码（未配置时用默认值）
func (c *config) adminPwd() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.AdminPwd != "" {
		return c.AdminPwd
	}
	return defaultAdminPwd
}

// setAdminPwd 修改管理密码并持久化到 config.json
func (c *config) setAdminPwd(np string) {
	c.mu.Lock()
	c.AdminPwd = np
	c.saveLocked()
	c.mu.Unlock()
}

func (c *config) set(u string) {
	c.mu.Lock()
	c.PublicURL = u
	c.saveLocked()
	c.mu.Unlock()
}

// validPublicURL 校验公网地址：仅允许 URL 常见字符，防止注入
func validPublicURL(u string) (bool, string) {
	if u == "" {
		return true, ""
	}
	if len(u) > 200 {
		return false, "地址过长"
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return false, "地址需以 http:// 或 https:// 开头"
	}
	for _, c := range u {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			strings.ContainsRune(":-./?=&%_~#@", c)
		if !ok {
			return false, "地址包含不支持的字符"
		}
	}
	if _, err := url.Parse(u); err != nil {
		return false, "地址格式不正确"
	}
	return true, ""
}

func safePublicURL(u string) string {
	if ok, _ := validPublicURL(u); ok {
		return u
	}
	return ""
}

/* ------------------------- 二维码页面 ------------------------- */

func qrPageHandler(lanURLs []string, port int, cfg *config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		primary := lanURLs[0]
		png, err := qrcode.Encode(primary, qrcode.Medium, 480)
		if err != nil {
			http.Error(w, "QR generation failed", 500)
			return
		}
		dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

		var alt strings.Builder
		for i, u := range lanURLs {
			if i == 0 {
				continue
			}
			alt.WriteString(fmt.Sprintf(`<div class="url-alt">%s</div>`, u))
		}

		publicURL := safePublicURL(cfg.get())
		pubBlock := ""
		if publicURL != "" {
			pubBlock = `<div class="qrwrap on" id="pubWrap"><img id="pubQR" alt="公网访问二维码"></div>`
		} else {
			pubBlock = `<div class="qrwrap" id="pubWrap"><div class="qr-empty" id="pubEmpty">尚未配置公网地址</div><img id="pubQR" alt="公网访问二维码" style="display:none"></div>`
		}

		html := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>扫码访问 · 性格色彩测试</title>
<style>
  *{box-sizing:border-box;margin:0;padding:0}
  body{min-height:100vh;display:flex;align-items:center;justify-content:center;
    font-family:-apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
    background:#FAF7F2;color:#3F3A34;padding:24px}
  .card{background:#fff;border-radius:22px;box-shadow:0 18px 50px rgba(158,138,116,.16);
    max-width:460px;width:100%;padding:32px 30px;text-align:center}
  h1{font-size:22px;letter-spacing:1px}
  .sub{color:#9A9186;font-size:13px;margin-top:8px;line-height:1.7}
  .tabs{display:flex;gap:8px;justify-content:center;margin-top:16px}
  .tab{border:1px solid #E3D9CC;background:#FBF7F1;color:#6A625A;font-family:inherit;
    font-size:13px;padding:8px 18px;border-radius:999px;cursor:pointer}
  .tab.on{background:#4F4A44;color:#fff;border-color:#4F4A44}
  .qrwrap{margin:18px auto 12px}
  .qrwrap img{width:270px;height:270px;display:block;margin:0 auto;
    border:1px solid #EFE8DF;border-radius:16px;padding:10px;background:#fff}
  .qr-empty{width:270px;height:270px;margin:0 auto;display:flex;align-items:center;justify-content:center;
    border:1.5px dashed #E0D6C8;border-radius:16px;color:#B9AE9F;font-size:13px;line-height:2}
  .url-main{font-size:16.5px;font-weight:600;word-break:break-all}
  .url-alt{font-size:12.5px;color:#9A9186;margin-top:6px;word-break:break-all}
  .pane{display:none}
  .pane.on{display:block}
  .pubset{margin-top:14px;text-align:left}
  .pubset label{font-size:12.5px;color:#9A9186;display:block;margin-bottom:7px;line-height:1.7}
  .pubrow{display:flex;gap:8px}
  .pubrow input{flex:1;font-family:inherit;font-size:14px;padding:11px 13px;border-radius:12px;
    border:1.5px solid #E3D9CC;background:#FDFBF8;outline:none;color:#3F3A34}
  .pubrow input:focus{border-color:#CFC3B3}
  .pubrow button{border:none;background:#4F4A44;color:#fff;font-family:inherit;font-size:13.5px;
    padding:11px 18px;border-radius:12px;cursor:pointer;flex:0 0 auto}
  .pubmsg{font-size:12px;margin-top:8px;color:#7FB99A;min-height:18px}
  .pubmsg.err{color:#E8837A}
  .hint{margin-top:16px;font-size:12.5px;color:#9A9186;line-height:1.9;
    background:#FCF8F2;border-radius:12px;padding:12px 14px;text-align:left}
  .hint b{color:#6A625A}
  .foot{margin-top:14px;font-size:11px;color:#C4BBB0}
</style>
</head>
<body>
  <div class="card">
    <h1>性格色彩测试</h1>
    <p class="sub">手机扫码即可打开测试页面</p>
    <div class="tabs">
      <button class="tab on" id="tabLan" onclick="sw(0)">局域网扫码</button>
      <button class="tab" id="tabPub" onclick="sw(1)">公网扫码</button>
    </div>

    <div class="pane on" id="paneLan">
      <div class="qrwrap"><img alt="局域网访问二维码" src="` + dataURI + `"></div>
      <div class="url-main">` + primary + `</div>
` + alt.String() + `
      <div class="hint">
        <b>局域网模式</b><br>
        1. 手机需与本电脑连接<b>同一个 WiFi / 局域网</b>；<br>
        2. 首次运行如弹出 Windows 防火墙提示，请勾选<b>「专用网络」</b>并点「允许访问」。
      </div>
    </div>

    <div class="pane" id="panePub">
` + pubBlock + `
      <div class="url-main" id="pubURLTxt">` + publicURL + `</div>
      <div class="pubset">
        <label>用了内网穿透（如<b>蜻蜓映射</b>）？把获得的公网地址粘贴到这里，即可生成公网二维码，
        任何人任何网络都能扫码访问：</label>
        <div class="pubrow">
          <input id="pubInput" type="text" placeholder="例如 http://xxx.cn:12345 或 https://xxx.cn" spellcheck="false">
          <button onclick="savePub()">生成公网二维码</button>
        </div>
        <div class="pubmsg" id="pubMsg"></div>
      </div>
      <div class="hint">
        <b>蜻蜓映射配置方法</b><br>
        1. 在蜻蜓映射新建映射，协议选 <b>HTTP</b>；<br>
        2. 本地地址填 <b>127.0.0.1:` + strconv.Itoa(port) + `</b>（端口以本窗口显示为准）；<br>
        3. 保存后复制平台分配的公网地址，粘贴到上方输入框。
      </div>
    </div>

    <div class="foot">性格色彩测试 · 服务端口 ` + strconv.Itoa(port) + ` · 记录保存于本电脑</div>
  </div>
<script>
function sw(i){
  document.getElementById("paneLan").classList.toggle("on", i===0);
  document.getElementById("panePub").classList.toggle("on", i===1);
  document.getElementById("tabLan").classList.toggle("on", i===0);
  document.getElementById("tabPub").classList.toggle("on", i===1);
}
var initPub = "` + publicURL + `";
function setPubQR(u){
  var img = document.getElementById("pubQR");
  var empty = document.getElementById("pubEmpty");
  if(u){
    img.src = "/qr.png?text=" + encodeURIComponent(u) + "&t=" + Date.now();
    img.style.display = "block";
    if(empty) empty.style.display = "none";
  }else{
    img.removeAttribute("src");
    img.style.display = "none";
    if(empty) empty.style.display = "flex";
  }
}
if(initPub){ setPubQR(initPub); }
function savePub(){
  var inp = document.getElementById("pubInput");
  var msg = document.getElementById("pubMsg");
  var u = (inp.value||"").trim();
  msg.textContent = "正在保存…"; msg.className = "pubmsg";
  fetch("/api/config", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({publicURL: u})
  }).then(function(r){
    if(!r.ok) return r.json().then(function(j){ throw new Error(j.error||"保存失败"); });
    return r.json();
  }).then(function(j){
    msg.textContent = u ? "已保存，公网二维码已生成" : "已清除公网地址";
    document.getElementById("pubURLTxt").textContent = j.publicURL || "";
    setPubQR(j.publicURL || "");
  }).catch(function(e){
    msg.textContent = e.message || "保存失败";
    msg.className = "pubmsg err";
  });
}
</script>
</body>
</html>`
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, html)
	}
}

/* ------------------------- API ------------------------- */

type apiServer struct {
	st      *store
	cfg     *config
	lanURLs []string
	port    int
}

func (a *apiServer) routes() *http.ServeMux {
	mux := http.NewServeMux()

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	// 静态文件：首页禁止缓存，避免手机拿到旧版页面后探测逻辑过期
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.FileServer(http.FS(sub)).ServeHTTP(w, r)
	}))
	mux.HandleFunc("/qr", qrPageHandler(a.lanURLs, a.port, a.cfg))

	// 动态二维码图片：/qr.png?text=<要编码的内容>
	mux.HandleFunc("/qr.png", func(w http.ResponseWriter, r *http.Request) {
		text := strings.TrimSpace(r.URL.Query().Get("text"))
		if text == "" || len(text) > 256 ||
			(!strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://")) {
			http.Error(w, "invalid text", 400)
			return
		}
		png, err := qrcode.Encode(text, qrcode.Medium, 480)
		if err != nil {
			http.Error(w, "QR generation failed", 500)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(png)
	})

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"mode":    "server",
			"url":     a.lanURLs[0],
			"port":    a.port,
			"public":  a.cfg.get(),
			"records": len(a.st.all()),
		})
	})

	// 公网地址配置（蜻蜓映射等内网穿透生成的地址）
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]string{"publicURL": a.cfg.get()})
		case http.MethodPost:
			var body struct {
				PublicURL string `json:"publicURL"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, 400, map[string]string{"error": "bad json"})
				return
			}
			u := strings.TrimSpace(body.PublicURL)
			if ok, msg := validPublicURL(u); !ok {
				writeJSON(w, 400, map[string]string{"error": msg})
				return
			}
			// 去掉末尾斜杠，统一格式
			u = strings.TrimSuffix(u, "/")
			a.cfg.set(u)
			writeJSON(w, 200, map[string]string{"publicURL": a.cfg.get()})
		default:
			w.Header().Set("Allow", "GET, POST")
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !a.checkPwd(r) {
				writeJSON(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			writeJSON(w, 200, a.st.all())
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			var rec record
			dec := json.NewDecoder(r.Body)
			if err := dec.Decode(&rec); err != nil {
				writeJSON(w, 400, map[string]string{"error": "bad json"})
				return
			}
			rec, err := sanitize(rec)
			if err != nil {
				writeJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			added := a.st.add(rec)
			// 返回最终报告密码（可能与提交值不同：为空或重复时已重新生成）
			writeJSON(w, 200, map[string]any{"ok": true, "key": added.Key, "total": len(a.st.all())})
		case http.MethodDelete:
			if !a.checkPwd(r) {
				writeJSON(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			a.st.clear()
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		}
	})

	// 凭报告密码查单份报告（公开：报告密码本身即凭证）
	mux.HandleFunc("/api/records/bykey/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/api/records/bykey/")
		key = strings.ToUpper(strings.TrimSpace(key))
		if key == "" {
			writeJSON(w, 400, map[string]string{"error": "missing key"})
			return
		}
		if rec, ok := a.st.findByKey(key); ok {
			writeJSON(w, 200, rec)
			return
		}
		writeJSON(w, 404, map[string]string{"error": "not found"})
	})

	mux.HandleFunc("/api/records/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		if !a.checkPwd(r) {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/records/")
		if id == "" {
			writeJSON(w, 400, map[string]string{"error": "missing id"})
			return
		}
		if a.st.del(id) {
			writeJSON(w, 200, map[string]any{"ok": true})
		} else {
			writeJSON(w, 404, map[string]string{"error": "not found"})
		}
	})

	// 修改管理密码（需携带当前有效密码，双重新校验）
	mux.HandleFunc("/api/admin/password", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		if !a.checkPwd(r) {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var body struct {
			OldPwd string `json:"oldPwd"`
			NewPwd string `json:"newPwd"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad json"})
			return
		}
		cur := a.cfg.adminPwd()
		if !subtleEqual(strings.TrimSpace(body.OldPwd), cur) {
			writeJSON(w, 400, map[string]string{"error": "当前密码不正确"})
			return
		}
		np := strings.TrimSpace(body.NewPwd)
		if l := len([]rune(np)); l < 4 || l > 32 {
			writeJSON(w, 400, map[string]string{"error": "新密码长度需为 4-32 位"})
			return
		}
		for _, ch := range np {
			if ch < 0x20 || ch == 0x7f {
				writeJSON(w, 400, map[string]string{"error": "新密码包含非法字符"})
				return
			}
		}
		a.cfg.setAdminPwd(np)
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	return mux
}

func (a *apiServer) checkPwd(r *http.Request) bool {
	return subtleEqual(r.Header.Get("X-Admin-Pwd"), a.cfg.adminPwd())
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	v := 0
	for i := 0; i < len(a); i++ {
		v |= int(a[i] ^ b[i])
	}
	return v == 0
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

/* ------------------------- main ------------------------- */

func main() {
	setupWindowsConsole()

	portFlag := flag.Int("port", defaultPort, "服务端口（被占用时自动向后尝试）")
	dirFlag := flag.String("dir", "", "记录保存目录（默认与程序同目录）")
	pubFlag := flag.String("public", "", "公网访问地址（如蜻蜓映射生成的地址，会保存到配置）")
	noBrowser := flag.Bool("no-browser", false, "启动后不自动打开浏览器")
	flag.Parse()

	dataDir := pickDataDir(*dirFlag)
	st := &store{path: filepath.Join(dataDir, "records.json")}
	st.load()
	cfg := loadConfig(filepath.Join(dataDir, "config.json"))
	cfg.ensureFile() // 首次运行自动生成 config.json（含默认管理密码）
	cfg.PublicURL = safePublicURL(cfg.PublicURL)
	if p := strings.TrimSpace(*pubFlag); p != "" {
		if ok, msg := validPublicURL(p); ok {
			cfg.set(strings.TrimSuffix(p, "/"))
		} else {
			fmt.Println("警告：-public 参数无效（" + msg + "），已忽略。")
		}
	}

	ln, port := listenOn(*portFlag)
	if ln == nil {
		fmt.Println("错误：没有可用的端口。")
		fmt.Println("按回车键退出…")
		fmt.Scanln()
		os.Exit(1)
	}

	ips := lanIPs()
	if len(ips) == 0 {
		ips = []string{"127.0.0.1"}
	}
	lanURLs := make([]string, 0, len(ips))
	for _, ip := range ips {
		lanURLs = append(lanURLs, fmt.Sprintf("http://%s:%d/", ip, port))
	}

	api := &apiServer{st: st, cfg: cfg, lanURLs: lanURLs, port: port}
	srv := &http.Server{
		Handler:           api.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP 服务异常退出: %v", err)
			os.Exit(1)
		}
	}()

	localURL := fmt.Sprintf("http://localhost:%d/", port)
	qrURL := fmt.Sprintf("http://localhost:%d/qr", port)

	fmt.Println("================================================================")
	fmt.Println("  性格色彩测试 · 局域网服务已启动")
	fmt.Println("----------------------------------------------------------------")
	fmt.Println("  本机访问 : " + localURL)
	fmt.Println("  二维码页 : " + qrURL)
	fmt.Println("  手机访问 : " + strings.Join(lanURLs, "  或  "))
	if pub := cfg.get(); pub != "" {
		fmt.Println("  公网访问 : " + pub + "/（二维码页可切换公网码）")
	}
	fmt.Println("  管理密码 : 已保存于 config.json（首次运行自动生成，默认 12345678，可在管理后台修改）")
	fmt.Println("  数据文件 : " + st.path + "（现有 " + strconv.Itoa(len(st.all())) + " 条记录）")
	fmt.Println("----------------------------------------------------------------")
	fmt.Println("  浏览器已自动打开二维码页面，用手机扫码即可开始测试。")
	fmt.Println("  如需公网访问：可用蜻蜓映射等内网穿透，把 127.0.0.1:" + strconv.Itoa(port))
	fmt.Println("  映射出去，再把获得的公网地址粘贴到二维码页「公网扫码」标签内。")
	fmt.Println("  请保持本窗口开启；关闭窗口或按 Ctrl+C 即停止服务。")
	fmt.Println("================================================================")

	if !*noBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(qrURL)
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Println("\n正在停止服务…")
	_ = srv.Close()
	fmt.Println("已退出。")
}
