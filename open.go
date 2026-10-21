package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// openCommandEnv 可覆盖打开浏览器的命令（URL 作为最后一个参数），
// 用于 WSL（wslview）、自定义浏览器，也是端到端测试里「不真的开窗口、只记录 URL」的钩子。
const openCommandEnv = "GOWEB_OPEN_COMMAND"

// browserOpener 是「在浏览器里打开 URL」这个动作；抽成函数类型便于测试注入。
type browserOpener func(url string) error

// newBrowserOpener 按当前平台与 GOWEB_OPEN_COMMAND 构造打开方式。
func newBrowserOpener() browserOpener {
	return systemBrowserOpener(runtime.GOOS, strings.TrimSpace(os.Getenv(openCommandEnv)))
}

// systemBrowserOpener 返回使用平台自带命令的打开方式。
func systemBrowserOpener(goos, override string) browserOpener {
	return func(url string) error {
		name, args, err := browserCommand(goos, override, url)
		if err != nil {
			return err
		}
		command := exec.Command(name, args...)
		if err := command.Start(); err != nil {
			return fmt.Errorf("执行 %s: %w", name, err)
		}
		// 不等浏览器退出：开发服务器的生命周期不能被浏览器进程拖住。
		return command.Process.Release()
	}
}

// browserCommand 给出打开 URL 的命令；override 非空时优先使用（URL 追加在末尾）。
func browserCommand(goos, override, url string) (string, []string, error) {
	if override != "" {
		return override, []string{url}, nil
	}
	switch goos {
	case "darwin":
		return "open", []string{url}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{url}, nil
	default:
		return "", nil, fmt.Errorf("平台 %s 没有内置的打开方式，可用 %s 指定命令", goos, openCommandEnv)
	}
}

// browserURL 把监听地址转成浏览器地址：`:8080`、`0.0.0.0:8080`、`[::]:8080`
// 都指向 localhost；具体 IP 原样使用，IPv6 补方括号。
func browserURL(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String() + "/"
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%s/", host, port)
}

// openInBrowser 打开浏览器；失败只记一条警告——这是开发体验的附属动作，
// 打不开（CI、SSH、没有桌面环境）绝不能影响服务器本身。
func openInBrowser(logger *slog.Logger, opener browserOpener, url string) {
	if opener == nil {
		return
	}
	if err := opener(url); err != nil {
		logger.Warn("无法自动打开浏览器，请手动访问", "url", url, "err", err, "hint", openCommandEnv)
	}
}
