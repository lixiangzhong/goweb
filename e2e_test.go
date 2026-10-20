package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// 端到端测试在真实进程与真实 HTTP 上验证完整链路：编译出真正的 goweb 二进制，
// 像用户一样执行 gen/serve，再请求 URL。
// 需要编译与端口，因此 go test -short 会跳过。
//
//	go test -run TestE2E -v ./...

const (
	// e2eStartupTimeout 是等待服务端打印监听地址的上限。
	e2eStartupTimeout = 20 * time.Second
	// e2eReloadTimeout 是等待热重载生效的上限。
	e2eReloadTimeout = 5 * time.Second
	// e2eHTTPTimeout 是单次请求超时。
	e2eHTTPTimeout = 5 * time.Second
)

// e2eAddrRe 匹配服务端启动日志里的真实监听地址。
var e2eAddrRe = regexp.MustCompile(`addr=([0-9.]+:[0-9]+)`)

// buildGoweb 编译出真实二进制，供 e2e 像用户一样调用。
func buildGoweb(t *testing.T) string {
	t.Helper()
	name := "goweb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 goweb 失败: %v\n%s", err, out)
	}
	return binary
}

func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e 需要编译二进制并使用端口，-short 下跳过")
	}

	binary := buildGoweb(t)
	siteDir := writeE2ESite(t)

	t.Run("gen 产出与源码同路径的扁平产物", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "dist")
		code, _, stderr := runBinary(t, binary, "gen", "--dir", siteDir, "--out", out)
		if code != 0 {
			t.Fatalf("gen 退出码 = %d，期望 0（stderr: %s）", code, stderr)
		}
		if !strings.Contains(stderr, "静态站已生成") {
			t.Fatalf("缺少生成日志: %s", stderr)
		}

		want := []string{
			"about.html",
			"help/74.html",
			"help/index.html",
			"index.html",
			"robots.txt",
			"static/css/site.css",
			"static/js/site.js",
		}
		if got := listFiles(t, out); !equalStrings(got, want) {
			t.Fatalf("产物清单 = %v，期望 %v", got, want)
		}

		about := readFile(t, filepath.Join(out, "about.html"))
		if !strings.Contains(about, "E2E-SITE") || !strings.Contains(about, "ABOUT") {
			t.Fatalf("产物未渲染模板或数据: %s", about)
		}
	})

	t.Run("gen 重复执行结果一致", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "dist")
		if code, _, stderr := runBinary(t, binary, "gen", "--dir", siteDir, "--out", out); code != 0 {
			t.Fatalf("首次 gen 失败: %s", stderr)
		}
		first := readFile(t, filepath.Join(out, "index.html"))

		if code, _, stderr := runBinary(t, binary, "gen", "--dir", siteDir, "--out", out); code != 0 {
			t.Fatalf("二次 gen 失败: %s", stderr)
		}
		if second := readFile(t, filepath.Join(out, "index.html")); first != second {
			t.Fatalf("重复生成结果不一致:\n%s\n---\n%s", first, second)
		}
	})

	t.Run("gen --clean=false 保留遗留文件", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "dist")
		if code, _, stderr := runBinary(t, binary, "gen", "--dir", siteDir, "--out", out); code != 0 {
			t.Fatalf("gen 失败: %s", stderr)
		}
		stale := filepath.Join(out, "stale.html")
		if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
			t.Fatalf("写入遗留文件: %v", err)
		}

		if code, _, stderr := runBinary(t, binary, "gen", "--dir", siteDir, "--out", out, "--clean=false"); code != 0 {
			t.Fatalf("--clean=false 生成失败: %s", stderr)
		}
		if _, err := os.Stat(stale); err != nil {
			t.Fatalf("--clean=false 不应删除遗留文件: %v", err)
		}

		if code, _, stderr := runBinary(t, binary, "gen", "--dir", siteDir, "--out", out); code != 0 {
			t.Fatalf("gen 失败: %s", stderr)
		}
		if _, err := os.Stat(stale); err == nil {
			t.Fatal("默认 --clean 应清空遗留文件")
		}
	})

	t.Run("gen 护栏拒绝危险产物目录", func(t *testing.T) {
		code, _, stderr := runBinary(t, binary, "gen", "--dir", siteDir, "--out", siteDir)
		if code == 0 {
			t.Fatal("把页面目录当产物目录应当失败")
		}
		if !strings.Contains(stderr, "产物目录") {
			t.Fatalf("错误信息缺少产物目录提示: %s", stderr)
		}
		if _, err := os.Stat(filepath.Join(siteDir, "index.html")); err != nil {
			t.Fatalf("源码被破坏: %v", err)
		}
	})

	t.Run("gen 遇到坏数据直接失败", func(t *testing.T) {
		broken := writeE2ESite(t)
		writeFile(t, filepath.Join(broken, "templates", "data.json"), `{"site": }`)

		out := filepath.Join(t.TempDir(), "dist")
		code, _, stderr := runBinary(t, binary, "gen", "--dir", broken, "--out", out)
		if code == 0 {
			t.Fatal("坏 data.json 应当让 gen 失败")
		}
		if !strings.Contains(stderr, "数据文件") {
			t.Fatalf("错误信息缺少数据文件提示: %s", stderr)
		}
	})

	t.Run("版本 帮助 补全可用", func(t *testing.T) {
		if code, stdout, stderr := runBinary(t, binary, "--version"); code != 0 || !strings.Contains(stdout, version) {
			t.Fatalf("--version 异常: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if code, stdout, _ := runBinary(t, binary, "help"); code != 0 ||
			!strings.Contains(stdout, "serve") || !strings.Contains(stdout, "gen") {
			t.Fatalf("help 输出异常: code=%d stdout=%q", code, stdout)
		}
		if code, stdout, stderr := runBinary(t, binary, "completion", "bash"); code != 0 || stdout == "" {
			t.Fatalf("completion 异常: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("serve 的 URL 语义与热重载", func(t *testing.T) {
		server := startServe(t, binary, siteDir)
		client := &http.Client{Timeout: e2eHTTPTimeout}

		cases := []struct {
			path         string
			wantStatus   int
			wantContains string
		}{
			{path: "/", wantStatus: 200, wantContains: "INDEX"},
			{path: "/index.html", wantStatus: 200, wantContains: "INDEX"},
			{path: "/about.html", wantStatus: 200, wantContains: "ABOUT"},
			{path: "/about", wantStatus: 200, wantContains: "ABOUT"},
			{path: "/about/", wantStatus: 404},
			{path: "/about/index.html", wantStatus: 404},
			{path: "/help", wantStatus: 200, wantContains: "HELP-INDEX"},
			{path: "/help/", wantStatus: 200, wantContains: "HELP-INDEX"},
			{path: "/help/74", wantStatus: 200, wantContains: "HELP-74"},
			{path: "/help/74.html", wantStatus: 200, wantContains: "HELP-74"},
			{path: "//about//", wantStatus: 404},
			{path: "/a/../about", wantStatus: 200, wantContains: "ABOUT"},
			{path: "/about/.", wantStatus: 404},
			{path: "/static/css/site.css", wantStatus: 200, wantContains: "color:red"},
			{path: "/robots.txt", wantStatus: 200, wantContains: "User-agent"},
			{path: "/templates/header.html", wantStatus: 404},
			{path: "/templates/data.json", wantStatus: 404},
			{path: "/missing", wantStatus: 404},
		}
		for _, test := range cases {
			t.Run(test.path, func(t *testing.T) {
				status, body := e2eGet(t, client, server.url+test.path)
				if status != test.wantStatus {
					t.Fatalf("GET %s 状态码 = %d，期望 %d（body: %s）", test.path, status, test.wantStatus, body)
				}
				if test.wantContains != "" && !strings.Contains(body, test.wantContains) {
					t.Fatalf("GET %s 响应缺少 %q：%s", test.path, test.wantContains, body)
				}
			})
		}

		t.Run("页面带 no-store 缓存头", func(t *testing.T) {
			resp := e2eRequest(t, client, http.MethodGet, server.url+"/about")
			defer resp.Body.Close()
			if got := resp.Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q，期望 no-store", got)
			}
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Fatalf("Content-Type = %q，期望 text/html", got)
			}
		})

		t.Run("非 GET/HEAD 返回 405", func(t *testing.T) {
			resp := e2eRequest(t, client, http.MethodPost, server.url+"/about")
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("POST 状态码 = %d，期望 405", resp.StatusCode)
			}
			if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
				t.Fatalf("Allow = %q，期望 GET, HEAD", got)
			}
		})

		t.Run("HEAD 可用", func(t *testing.T) {
			resp := e2eRequest(t, client, http.MethodHead, server.url+"/about")
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("HEAD 状态码 = %d，期望 200", resp.StatusCode)
			}
		})

		t.Run("改动页面后自动重载", func(t *testing.T) {
			writeFile(t, filepath.Join(siteDir, "about.html"), "<html><body><h1>ABOUT-EDITED</h1></body></html>")
			waitForBody(t, client, server.url+"/about", "ABOUT-EDITED")
		})

		t.Run("改动 data.json 后自动重载", func(t *testing.T) {
			writeFile(t, filepath.Join(siteDir, "templates", "data.json"), `{"site":{"title":"E2E-EDITED"}}`)
			waitForBody(t, client, server.url+"/index.html", "E2E-EDITED")
		})

		t.Run("坏模板不拖垮服务", func(t *testing.T) {
			writeFile(t, filepath.Join(siteDir, "about.html"), "<html>{{template ")
			// 等防抖窗口 + 一次失败的重载
			time.Sleep(500 * time.Millisecond)

			status, body := e2eGet(t, client, server.url+"/about")
			if status != 200 || !strings.Contains(body, "ABOUT-EDITED") {
				t.Fatalf("坏模板下应继续提供上一份可用页面，实际 status=%d body=%s", status, body)
			}
			if log := server.logs(); !strings.Contains(log, "站点重载失败") {
				t.Fatalf("日志中缺少重载失败记录: %s", log)
			}
		})
	})
}

// e2eServer 是运行中的 goweb serve 进程。
type e2eServer struct {
	url string

	cmd    *exec.Cmd
	mu     sync.Mutex
	stderr bytes.Buffer
}

// logs 返回服务端已输出的日志。
func (s *e2eServer) logs() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stderr.String()
}

// startServe 编译后的二进制起一个开发服务器，等待它打印真实监听地址后返回。
// 用 --addr 127.0.0.1:0 让内核分配端口，再从日志里取回，避免端口冲突。
func startServe(t *testing.T, binary, siteDir string) *e2eServer {
	t.Helper()

	cmd := exec.Command(binary, "serve", "--dir", siteDir, "--addr", "127.0.0.1:0")
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("获取 stderr: %v", err)
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 serve: %v", err)
	}

	server := &e2eServer{cmd: cmd}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	addrCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		sent := false
		for scanner.Scan() {
			line := scanner.Text()
			server.mu.Lock()
			server.stderr.WriteString(line + "\n")
			server.mu.Unlock()

			if sent {
				continue
			}
			if match := e2eAddrRe.FindStringSubmatch(line); match != nil {
				addrCh <- match[1]
				sent = true
			}
		}
	}()

	select {
	case addr := <-addrCh:
		server.url = "http://" + addr
	case <-time.After(e2eStartupTimeout):
		t.Fatalf("等待监听地址超时，日志: %s", server.logs())
	}
	return server
}

// runBinary 运行二进制并返回退出码与输出。
func runBinary(t *testing.T, binary string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("运行 %v 失败: %v", args, err)
	}
	return exitErr.ExitCode(), stdout.String(), stderr.String()
}

// e2eRequest 发一次请求。
func e2eRequest(t *testing.T, client *http.Client, method, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s 失败: %v", method, url, err)
	}
	return resp
}

// e2eGet 发一次 GET 并返回状态码与响应体。
func e2eGet(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	resp := e2eRequest(t, client, http.MethodGet, url)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	return resp.StatusCode, string(body)
}

// waitForBody 轮询等待 URL 返回期望内容。
func waitForBody(t *testing.T, client *http.Client, url, want string) {
	t.Helper()
	deadline := time.Now().Add(e2eReloadTimeout)
	var last string
	for {
		if status, body := e2eGet(t, client, url); status == http.StatusOK && strings.Contains(body, want) {
			return
		} else {
			last = fmt.Sprintf("status=%d body=%s", status, body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s 未在 %s 内出现 %q，最后响应: %s", url, e2eReloadTimeout, want, last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// writeE2ESite 铺一个尽量接近真实使用的站点，包含页面、局部模板、数据与静态资源。
func writeE2ESite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":                  `<html><body>{{template "header" .}}<h1>INDEX</h1></body></html>`,
		"about.html":                  `<html><body>{{template "header" .}}<h1>ABOUT</h1></body></html>`,
		"help/index.html":             `<html><body><h1>HELP-INDEX</h1></body></html>`,
		"help/74.html":                `<html><body><h1>HELP-74</h1></body></html>`,
		"templates/header.html":       `{{define "header"}}<header>{{.site.title}}</header>{{end}}`,
		"templates/data.json":         `{"site":{"title":"E2E-SITE"}}`,
		"templates/note.txt":          "局部模板目录里的非 html 文件",
		"static/css/site.css":         ".a{color:red}",
		"static/js/site.js":           "console.log(1)",
		"robots.txt":                  "User-agent: *",
		"node_modules/pkg/index.html": "<html>DEP</html>",
		".hidden/secret.html":         "<html>SECRET</html>",
	}
	for name, body := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), body)
	}
	return dir
}
