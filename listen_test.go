package main

import (
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// TestNextPort 覆盖端口递增：主机不变、非法端口不递增。
func TestNextPort(t *testing.T) {
	tests := []struct {
		addr string
		step int
		want string
		ok   bool
	}{
		{addr: ":8080", step: 1, want: ":8081", ok: true},
		{addr: "127.0.0.1:8080", step: 3, want: "127.0.0.1:8083", ok: true},
		{addr: "[::1]:8080", step: 1, want: "[::1]:8081", ok: true},
		{addr: ":8080", step: 0, want: ":8080", ok: true},
		// 下面这些不该递增：内核分配（:0）、非数字端口、没有端口、越界。
		{addr: ":0", step: 1},
		{addr: ":abc", step: 1},
		{addr: "localhost", step: 1},
		{addr: ":65535", step: 1},
		{addr: ":65530", step: 10},
	}
	for _, test := range tests {
		got, ok := nextPort(test.addr, test.step)
		if ok != test.ok || (ok && got != test.want) {
			t.Errorf("nextPort(%q, %d) = %q, %v；期望 %q, %v", test.addr, test.step, got, ok, test.want, test.ok)
		}
	}
}

// TestListenWithFallbackPicksNextFreePort 用真实 socket 验证：端口被占用时落到下一个端口。
func TestListenWithFallbackPicksNextFreePort(t *testing.T) {
	base, blocker := occupyConsecutivePorts(t, 1)
	defer blocker()

	var logged strings.Builder
	logger := slog.New(slog.NewTextHandler(&logged, nil))

	listener, err := listenWithFallback(base, true, logger)
	if err != nil {
		t.Fatalf("端口被占用时应递增重试: %v", err)
	}
	defer listener.Close()

	wantPort := portOf(t, base) + 1
	if got := portOf(t, listener.Addr().String()); got != wantPort {
		t.Fatalf("应落到 %d，实际 %d", wantPort, got)
	}
	if !strings.Contains(logged.String(), "端口被占用") || !strings.Contains(logged.String(), strconv.Itoa(wantPort)) {
		t.Fatalf("应记录一条改用端口的日志，实际: %s", logged.String())
	}
}

// TestListenWithFallbackFailsFastWhenExplicit 覆盖「显式指定端口」：占用时直接失败，不递增。
func TestListenWithFallbackFailsFastWhenExplicit(t *testing.T) {
	base, blocker := occupyConsecutivePorts(t, 1)
	defer blocker()

	listener, err := listenWithFallback(base, false, slog.New(slog.DiscardHandler))
	if err == nil {
		listener.Close()
		t.Fatal("显式指定端口被占用时应当直接失败")
	}
	if !isAddressInUse(err) {
		t.Fatalf("错误应当是「地址已被占用」: %v", err)
	}
}

// TestListenWithFallbackIgnoresNonInUseErrors 覆盖非占用类错误（端口非法）不被掩盖。
func TestListenWithFallbackIgnoresNonInUseErrors(t *testing.T) {
	if listener, err := listenWithFallback("127.0.0.1:99999", true, slog.New(slog.DiscardHandler)); err == nil {
		listener.Close()
		t.Fatal("非法端口应当直接报错")
	}
}

// TestListenWithFallbackExhaustsPorts 覆盖连续多个端口都被占用时的报错信息。
func TestListenWithFallbackExhaustsPorts(t *testing.T) {
	base, blocker := occupyConsecutivePorts(t, 3)
	defer blocker()

	_, err := listenWithFallbackN(base, true, 3, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("连续端口都被占用时应当报错")
	}
	message := err.Error()
	for _, want := range []string{"都被占用", base, ifErrorIsInUse(err)} {
		if !strings.Contains(message, want) {
			t.Fatalf("错误信息应包含 %q，实际: %s", want, message)
		}
	}
}

// ifErrorIsInUse 返回一个必定出现在错误信息里的标记；这里统一要求保留原始原因。
func ifErrorIsInUse(err error) string {
	if !isAddressInUse(err) {
		return "!! 原始错误被丢失 !!"
	}
	return "被占用"
}

// occupyConsecutivePorts 占用 count 个连续的 TCP 端口，返回基地址（如 127.0.0.1:34001）
// 与释放函数。内核分配一个空闲端口后再连着往后占，避免与其它进程抢端口。
func occupyConsecutivePorts(t *testing.T, count int) (string, func()) {
	t.Helper()

	for range 50 {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("探测空闲端口: %v", err)
		}
		basePort := probe.Addr().(*net.TCPAddr).Port
		probe.Close()

		addresses := make([]string, 0, count)
		listeners := make([]net.Listener, 0, count)
		fail := false
		for offset := range count {
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(basePort+offset))
			listener, err := net.Listen("tcp", addr)
			if err != nil {
				fail = true
				break
			}
			addresses = append(addresses, addr)
			listeners = append(listeners, listener)
		}
		if fail {
			for _, listener := range listeners {
				listener.Close()
			}
			continue
		}

		release := func() {
			for _, listener := range listeners {
				listener.Close()
			}
		}
		return addresses[0], release
	}
	t.Fatalf("找不到 %d 个连续空闲端口", count)
	return "", func() {}
}

// portOf 取出地址里的端口号。
func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("解析地址 %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("解析端口 %q: %v", portText, err)
	}
	return port
}

// TestIsAddressInUse 覆盖占用判断：Unix 的 EADDRINUSE、Windows 的真实 WSA 错误码都要认，
// 其它错误不能被当成端口占用（否则端口回退会掩盖真正的失败原因）。
func TestIsAddressInUse(t *testing.T) {
	if !isAddressInUse(&net.OpError{Err: &net.OpError{Err: syscall.EADDRINUSE}}) {
		t.Fatal("包装过的 EADDRINUSE 应被识别")
	}
	// Windows 上 net.Listen 返回的是真实 WSA 错误码，与 syscall.EADDRINUSE 不相等；
	// 这里在任意平台上都按数值走同一条分支，因此这条断言同时覆盖了 Windows 的路径。
	wsaErr := &os.SyscallError{Syscall: "bind", Err: syscall.Errno(wsaEADDRINUSE)}
	if !isAddressInUse(wsaErr) {
		t.Fatalf("Windows 的 WSA 错误码 %d 应被识别为端口占用", wsaEADDRINUSE)
	}
	if isAddressInUse(&net.OpError{Err: syscall.EACCES}) {
		t.Fatal("其它错误不应被当成端口占用")
	}
}
