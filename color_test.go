package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestColorizeStatus 覆盖按状态码上色：2xx 不动，3xx/4xx/5xx 分别青/黄/红。
func TestColorizeStatus(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "2xx 不上色",
			line: `time="2026-10-21 15:00:00" level=INFO msg=处理请求 method=GET path=/ status=200` + "\n",
			want: `time="2026-10-21 15:00:00" level=INFO msg=处理请求 method=GET path=/ status=200` + "\n",
		},
		{
			name: "3xx 青色",
			line: "msg=处理请求 status=301\n",
			want: "msg=处理请求 status=" + ansiCyan + "301" + ansiReset + "\n",
		},
		{
			name: "4xx 黄色",
			line: "msg=处理请求 status=404\n",
			want: "msg=处理请求 status=" + ansiYellow + "404" + ansiReset + "\n",
		},
		{
			name: "5xx 红色",
			line: "msg=渲染页面失败 status=500\n",
			want: "msg=渲染页面失败 status=" + ansiRed + "500" + ansiReset + "\n",
		},
		{
			name: "没有 status 字段的行原样保留",
			line: "msg=站点已重载 pages=3\n",
			want: "msg=站点已重载 pages=3\n",
		},
		{
			name: "只替换 status 值，不影响同一行的其它数字",
			line: "status=404 pages=12 port=8080\n",
			want: "status=" + ansiYellow + "404" + ansiReset + " pages=12 port=8080\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := colorizeStatus(test.line); got != test.want {
				t.Fatalf("上色结果 = %q，期望 %q", got, test.want)
			}
		})
	}
}

// TestColorWriterBuffersPartialLines 覆盖分多次写出的同一行：必须整行处理后再输出。
func TestColorWriterBuffersPartialLines(t *testing.T) {
	var out bytes.Buffer
	writer := newColorWriter(&out)

	if _, err := writer.Write([]byte(`msg=处理请求 status=5`)); err != nil {
		t.Fatalf("写入: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("没有换行时不应输出，实际: %q", out.String())
	}
	if _, err := writer.Write([]byte("03\n")); err != nil {
		t.Fatalf("写入: %v", err)
	}
	want := "msg=处理请求 status=" + ansiRed + "503" + ansiReset + "\n"
	if out.String() != want {
		t.Fatalf("输出 = %q，期望 %q", out.String(), want)
	}
}

// TestLoggerDoesNotColorNonTTY 覆盖默认行为：写进缓冲区（不是终端）时日志里不应有转义序列。
func TestLoggerDoesNotColorNonTTY(t *testing.T) {
	var buffer syncBuffer
	logger := newLogger(&buffer)
	logger.Info("处理请求", "method", "GET", "path", "/missing", "status", 404)

	line := buffer.String()
	if strings.Contains(line, "\x1b[") {
		t.Fatalf("非终端输出不应含 ANSI 转义: %q", line)
	}
	if !strings.Contains(line, "status=404") {
		t.Fatalf("应记录状态码: %q", line)
	}
	if strings.Contains(line, "duration") {
		t.Fatalf("访问日志不应再有 duration: %q", line)
	}
}

// TestWantColorLogsRespectsNoColor 覆盖 NO_COLOR 约定（只在设置了非空值时关闭颜色）。
func TestWantColorLogsRespectsNoColor(t *testing.T) {
	// 缓冲区不是终端 → 不上色。
	if wantColorLogs(&bytes.Buffer{}) {
		t.Fatal("非终端输出不应上色")
	}
	t.Setenv("NO_COLOR", "1")
	if wantColorLogs(os.Stderr) {
		t.Fatal("设置了 NO_COLOR 时不应上色")
	}
}

// TestAccessLogFields 锁定访问日志的字段集合：method/path/status，不含 duration。
func TestAccessLogFields(t *testing.T) {
	handler, logs := newHandlerWithLog(loadTestSite(t, testSiteFiles()))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing", nil))

	line := logs.String()
	if !strings.Contains(line, "msg=处理请求") || !strings.Contains(line, "status=404") {
		t.Fatalf("访问日志内容不符: %q", line)
	}
	if strings.Contains(line, "duration=") {
		t.Fatalf("访问日志不应包含 duration: %q", line)
	}
	if strings.Contains(line, "\x1b[") {
		t.Fatalf("非终端输出不应含 ANSI 转义: %q", line)
	}
}
