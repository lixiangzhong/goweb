package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

// syncBuffer 是并发安全的日志缓冲：监听协程与测试主协程会同时读写它。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newHandlerWithLog 构造处理器，并把日志收进返回的缓冲，便于断言。
func newHandlerWithLog(site *Site) (http.Handler, *syncBuffer) {
	logs := new(syncBuffer)
	return newHandler(site, slog.New(slog.NewTextHandler(logs, nil)), nil), logs
}

// executeCLIInSite 先把进程工作目录切到站点目录，再执行 CLI。
// 等价于用户在项目根运行 goweb：项目根 = 站点目录，三个根的默认值都在它下面。
func executeCLIInSite(t *testing.T, siteDir string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(siteDir)
	return executeCLI(args...)
}

// writeFile 写入文件并创建父目录。
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录 %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入 %s: %v", path, err)
	}
}

// readFile 读取文件内容。
func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return string(body)
}

// listFiles 返回目录下所有文件的相对路径（排序、斜杠分隔）。
func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

// equalStrings 比较两个字符串切片。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// waitFor 轮询等待条件成立，超时即失败。
func waitFor(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待超时（%s）：%s", timeout, message)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
