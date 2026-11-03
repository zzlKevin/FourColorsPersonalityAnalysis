//go:build windows

// Windows 专用：控制台 UTF-8 与打开浏览器

package main

import (
	"log"
	"os/exec"
	"syscall"
)

// setupWindowsConsole 把控制台代码页切到 UTF-8，避免中文乱码。
func setupWindowsConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("SetConsoleOutputCP")
	_, _, _ = proc.Call(65001)
}

func openBrowser(url string) {
	cmd := exec.Command("cmd", "/c", "start", "", url)
	if err := cmd.Start(); err != nil {
		log.Printf("打开浏览器失败: %v（请手动访问上方地址）", err)
	}
}
