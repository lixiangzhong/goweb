package main

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ANSI 颜色码。只用于终端日志的可读性，不参与任何逻辑判断。
const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

// statusPattern 匹配日志里的 status 字段值，用于按状态码上色。
var statusPattern = regexp.MustCompile(`status=(\d{3})\b`)

// statusColor 给出状态码对应的颜色：
// 2xx 不上色（正常请求占大多数，全上色反而看不清异常），
// 3xx 青、4xx 黄、5xx 红——一眼能扫出「哪些不是正常响应」。
func statusColor(status int) string {
	switch {
	case status >= 500:
		return ansiRed
	case status >= 400:
		return ansiYellow
	case status >= 300:
		return ansiCyan
	default:
		return ""
	}
}

// colorWriter 给经过它的每一行日志里的 status 值上色。
//
// 之所以在「写完之后的整行文本」上做，而不是把颜色塞进 slog 的字段值：slog 的
// TextHandler 会把含控制字符的值转义成 `"\x1b[33m404\x1b[0m"`，反而更难读。
type colorWriter struct {
	out io.Writer
	// pending 缓存尚未遇到换行的内容：一次日志可能分多次写出。
	pending []byte
}

// newColorWriter 返回按状态码上色的 writer。
func newColorWriter(out io.Writer) *colorWriter {
	return &colorWriter{out: out}
}

func (c *colorWriter) Write(p []byte) (int, error) {
	c.pending = append(c.pending, p...)
	for {
		index := strings.IndexByte(string(c.pending), '\n')
		if index < 0 {
			break
		}
		line := c.pending[:index+1]
		c.pending = c.pending[index+1:]
		if _, err := c.out.Write([]byte(colorizeStatus(string(line)))); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

// colorizeStatus 把一行日志里的 status=404 变成 status=<颜色>404<重置>；2xx 原样返回。
func colorizeStatus(line string) string {
	return statusPattern.ReplaceAllStringFunc(line, func(match string) string {
		status, err := strconv.Atoi(strings.TrimPrefix(match, "status="))
		if err != nil {
			return match
		}
		color := statusColor(status)
		if color == "" {
			return match
		}
		return "status=" + color + strconv.Itoa(status) + ansiReset
	})
}

// wantColorLogs 判断是否该给日志上色：输出是终端（字符设备）且没有设置 NO_COLOR。
//
// 重定向到文件、管道给 less/grep 时不会是终端，因此不会混入转义序列；
// NO_COLOR 遵循 https://no-color.org 的约定（只要设置了非空值就关闭颜色）。
func wantColorLogs(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
