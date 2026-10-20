package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestHandler 构造测试用处理器，日志丢弃。
func newTestHandler(site *Site) http.Handler {
	return newHandler(site, slog.New(slog.DiscardHandler))
}

func TestHandlerRoutes(t *testing.T) {
	handler := newTestHandler(loadTestSite(t, testSiteFiles()))

	tests := []struct {
		name         string
		method       string
		path         string
		wantStatus   int
		wantLocation string
		wantContains string
	}{
		{name: "根路径渲染 index", method: http.MethodGet, path: "/", wantStatus: http.StatusOK, wantContains: "INDEX"},
		{name: "目录页面", method: http.MethodGet, path: "/about/", wantStatus: http.StatusOK, wantContains: "ABOUT"},
		{name: "局部模板已展开", method: http.MethodGet, path: "/about/", wantStatus: http.StatusOK, wantContains: "SITE-HEADER"},
		{name: "无斜杠收敛", method: http.MethodGet, path: "/about", wantStatus: http.StatusMovedPermanently, wantLocation: "/about/"},
		{name: "带扩展名收敛", method: http.MethodGet, path: "/about.html", wantStatus: http.StatusMovedPermanently, wantLocation: "/about/"},
		{name: "index.html 收敛到根", method: http.MethodGet, path: "/index.html", wantStatus: http.StatusMovedPermanently, wantLocation: "/"},
		{name: "目录 index 页面", method: http.MethodGet, path: "/help/", wantStatus: http.StatusOK, wantContains: "HELP-INDEX"},
		{name: "目录无 index 回退页面", method: http.MethodGet, path: "/help/74/", wantStatus: http.StatusOK, wantContains: "HELP-74"},
		{name: "静态文件直出", method: http.MethodGet, path: "/static/css/a.css", wantStatus: http.StatusOK, wantContains: "body{color:red}"},
		{name: "根目录静态文件直出", method: http.MethodGet, path: "/robots.txt", wantStatus: http.StatusOK, wantContains: "User-agent"},
		{name: "局部模板不可访问", method: http.MethodGet, path: "/templates/header.html", wantStatus: http.StatusNotFound},
		{name: "模板目录的数据文件不可访问", method: http.MethodGet, path: "/templates/data.json", wantStatus: http.StatusNotFound},
		{name: "模板目录的普通文件不可访问", method: http.MethodGet, path: "/templates/note.txt", wantStatus: http.StatusNotFound},
		{name: "未知路径 404", method: http.MethodGet, path: "/missing", wantStatus: http.StatusNotFound},
		{name: "未知目录 404", method: http.MethodGet, path: "/missing/", wantStatus: http.StatusNotFound},
		{name: "非 GET/HEAD 方法拒绝", method: http.MethodPost, path: "/about/", wantStatus: http.StatusMethodNotAllowed},
		{name: "HEAD 允许", method: http.MethodHead, path: "/about/", wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("%s %s 状态码 = %d，期望 %d", test.method, test.path, recorder.Code, test.wantStatus)
			}
			if test.wantLocation != "" {
				if got := recorder.Header().Get("Location"); got != test.wantLocation {
					t.Fatalf("%s %s Location = %q，期望 %q", test.method, test.path, got, test.wantLocation)
				}
			}
			if test.wantContains != "" && !strings.Contains(recorder.Body.String(), test.wantContains) {
				t.Fatalf("%s %s 响应缺少 %q：%s", test.method, test.path, test.wantContains, recorder.Body.String())
			}
		})
	}
}

func TestHandlerStaticFileContentType(t *testing.T) {
	handler := newTestHandler(loadTestSite(t, testSiteFiles()))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/static/css/a.css", nil))

	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q，期望 text/css", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q，期望 no-store", got)
	}
}

// TestWatchReloadsTemplates 覆盖真实的文件监听链路：改动页面与 data.json 后应自动重编译。
func TestWatchReloadsTemplates(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"about.html":          "<html>OLD {{.title}}</html>",
		"templates/data.json": `{"title":"T1"}`,
	})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	stop, err := watchSite(site, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("watchSite: %v", err)
	}
	defer stop()

	// 改模板本身。
	if err := os.WriteFile(filepath.Join(dir, "about.html"), []byte("<html>NEW {{.title}}</html>"), 0o644); err != nil {
		t.Fatalf("写入新模板: %v", err)
	}
	waitForRender(t, site, "about", "NEW")

	// 改数据文件。
	if err := os.WriteFile(filepath.Join(dir, "templates", "data.json"), []byte(`{"title":"T2"}`), 0o644); err != nil {
		t.Fatalf("写入新数据: %v", err)
	}
	waitForRender(t, site, "about", "T2")
}

// waitForRender 轮询等待某个模板渲染出期望内容，超时即失败。
func waitForRender(t *testing.T, site *Site, name, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last []byte
	for {
		body, err := site.Render(name)
		last = body
		if err == nil && strings.Contains(string(body), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("模板 %s 未在 5s 内渲染出 %q，最后内容: %s", name, want, last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
