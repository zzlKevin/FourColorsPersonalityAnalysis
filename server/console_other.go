//go:build !windows

// 非 Windows 平台：控制台无需设置，浏览器用系统默认命令

package main

import (
	"log"
	"os/exec"
	"runtime"
)

func setupWindowsConsole() {}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("打开浏览器失败: %v（请手动访问上方地址）", err)
	}
}
