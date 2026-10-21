package main

import (
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
)

// TestBrowserURL 覆盖监听地址到浏览器地址的推导：
// 通配地址统一指向 localhost，具体地址原样使用，IPv6 补方括号。
func TestBrowserURL(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want string
	}{
		{
			name: "IPv4 通配地址指向 localhost",
			addr: &net.TCPAddr{IP: net.IPv4zero, Port: 8080},
			want: "http://localhost:8080/",
		},
		{
			name: "IPv6 通配地址也指向 localhost",
			addr: &net.TCPAddr{IP: net.IPv6zero, Port: 8080},
			want: "http://localhost:8080/",
		},
		{
			name: "具体 IPv4 原样使用",
			addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18123},
			want: "http://127.0.0.1:18123/",
		},
		{
			name: "IPv6 回环补方括号",
			addr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 8080},
			want: "http://[::1]:8080/",
		},
		{
			name: "无法解析的地址退化成原样拼接",
			addr: fakeAddr("not-a-host-port"),
			want: "http://not-a-host-port/",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := browserURL(test.addr); got != test.want {
				t.Fatalf("browserURL(%s) = %q，期望 %q", test.addr, got, test.want)
			}
		})
	}
}

// fakeAddr 是只提供 String() 的地址实现，用于覆盖解析失败的退化分支。
type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

// TestBrowserCommand 覆盖各平台使用的打开命令与自定义覆盖。
func TestBrowserCommand(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		override string
		wantName string
		wantArgs []string
		wantErr  bool
	}{
		{name: "macOS 用 open", goos: "darwin", wantName: "open", wantArgs: []string{"http://localhost:8080/"}},
		{
			name: "Windows 用 rundll32", goos: "windows",
			wantName: "rundll32", wantArgs: []string{"url.dll,FileProtocolHandler", "http://localhost:8080/"},
		},
		{name: "Linux 用 xdg-open", goos: "linux", wantName: "xdg-open", wantArgs: []string{"http://localhost:8080/"}},
		{
			name: "自定义命令优先（WSL 用 wslview 这类场景）", goos: "linux", override: "wslview",
			wantName: "wslview", wantArgs: []string{"http://localhost:8080/"},
		},
		{name: "未知平台报错", goos: "plan9", wantErr: true},
	}

	const url = "http://localhost:8080/"
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, args, err := browserCommand(test.goos, test.override, url)
			if test.wantErr {
				if err == nil {
					t.Fatalf("平台 %s 应当报错", test.goos)
				}
				return
			}
			if err != nil {
				t.Fatalf("browserCommand 出错: %v", err)
			}
			if name != test.wantName || !equalStrings(args, test.wantArgs) {
				t.Fatalf("browserCommand = %q %v，期望 %q %v", name, args, test.wantName, test.wantArgs)
			}
		})
	}
}

// TestOpenInBrowserReportsFailureWithoutFailing 覆盖「打不开只记日志」：
// 这是开发体验的附属动作，绝不能影响服务器本身。
func TestOpenInBrowserReportsFailureWithoutFailing(t *testing.T) {
	var logged strings.Builder
	logger := slog.New(slog.NewTextHandler(&logged, nil))

	openInBrowser(logger, func(string) error { return errors.New("没有可用的浏览器") }, "http://localhost:8080/")

	output := logged.String()
	if !strings.Contains(output, "无法自动打开浏览器") || !strings.Contains(output, "没有可用的浏览器") {
		t.Fatalf("失败时应记录警告，实际日志: %s", output)
	}
	if !strings.Contains(output, openCommandEnv) {
		t.Fatalf("日志里应提示可用 %s 自定义命令: %s", openCommandEnv, output)
	}

	// 打开成功时不产生任何日志噪音。
	logged.Reset()
	openInBrowser(logger, func(string) error { return nil }, "http://localhost:8080/")
	if logged.String() != "" {
		t.Fatalf("成功时不应记录日志，实际: %s", logged.String())
	}

	// nil 打开方式（例如平台不支持且未配置）不应 panic。
	openInBrowser(logger, nil, "http://localhost:8080/")
}
