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
		wantContains string
	}{
		{name: "根路径渲染 index", method: http.MethodGet, path: "/", wantStatus: http.StatusOK, wantContains: "INDEX"},
		{name: "页面真实路径", method: http.MethodGet, path: "/about.html", wantStatus: http.StatusOK, wantContains: "ABOUT"},
		{name: "无扩展名写法", method: http.MethodGet, path: "/about", wantStatus: http.StatusOK, wantContains: "ABOUT"},
		{name: "局部模板已展开", method: http.MethodGet, path: "/about", wantStatus: http.StatusOK, wantContains: "SITE-HEADER"},
		{name: "index.html 直接命中", method: http.MethodGet, path: "/index.html", wantStatus: http.StatusOK, wantContains: "INDEX"},
		{name: "目录 index 页面", method: http.MethodGet, path: "/help/", wantStatus: http.StatusOK, wantContains: "HELP-INDEX"},
		{name: "目录 index 页无斜杠写法", method: http.MethodGet, path: "/help", wantStatus: http.StatusOK, wantContains: "HELP-INDEX"},
		{name: "子目录页面", method: http.MethodGet, path: "/help/74", wantStatus: http.StatusOK, wantContains: "HELP-74"},
		{name: "扁平产物下没有 about 目录", method: http.MethodGet, path: "/about/", wantStatus: http.StatusNotFound},
		{name: "扁平产物下没有 about/index.html", method: http.MethodGet, path: "/about/index.html", wantStatus: http.StatusNotFound},
		{name: "归一化后命中页面", method: http.MethodGet, path: "/a/../about", wantStatus: http.StatusOK, wantContains: "ABOUT"},
		{name: "归一化后目录不存在", method: http.MethodGet, path: "//about//", wantStatus: http.StatusNotFound},
		{name: "静态文件直出", method: http.MethodGet, path: "/static/css/a.css", wantStatus: http.StatusOK, wantContains: "body{color:red}"},
		{name: "根目录静态文件直出", method: http.MethodGet, path: "/robots.txt", wantStatus: http.StatusOK, wantContains: "User-agent"},
		{name: "局部模板不可访问", method: http.MethodGet, path: "/templates/header.html", wantStatus: http.StatusNotFound},
		{name: "模板目录的数据文件不可访问", method: http.MethodGet, path: "/templates/data.json", wantStatus: http.StatusNotFound},
		{name: "模板目录的普通文件不可访问", method: http.MethodGet, path: "/templates/note.txt", wantStatus: http.StatusNotFound},
		{name: "产物目录不可访问", method: http.MethodGet, path: "/dist/old.html", wantStatus: http.StatusNotFound},
		{name: "未知路径 404", method: http.MethodGet, path: "/missing", wantStatus: http.StatusNotFound},
		{name: "未知目录 404", method: http.MethodGet, path: "/missing/", wantStatus: http.StatusNotFound},
		{name: "非 GET/HEAD 方法拒绝", method: http.MethodPost, path: "/about/", wantStatus: http.StatusMethodNotAllowed},
		{name: "HEAD 允许", method: http.MethodHead, path: "/about", wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("%s %s 状态码 = %d，期望 %d", test.method, test.path, recorder.Code, test.wantStatus)
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

// TestHandlerRenderError 覆盖渲染失败：返回 500，把原因写回浏览器，并记录错误日志。
func TestHandlerRenderError(t *testing.T) {
	handler, logs := newHandlerWithLog(loadTestSite(t, map[string]string{
		"bad.html": `<html>{{template "missing" .}}</html>`,
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/bad", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "渲染失败") {
		t.Fatalf("响应未说明渲染失败: %s", recorder.Body.String())
	}
	if !strings.Contains(logs.String(), "渲染页面失败") {
		t.Fatalf("日志中缺少渲染失败记录: %s", logs.String())
	}
}

// TestHandlerAccessLog 覆盖访问日志：方法、路径与状态码都要落到日志里。
func TestHandlerAccessLog(t *testing.T) {
	handler, logs := newHandlerWithLog(loadTestSite(t, testSiteFiles()))

	for _, path := range []string{"/about", "/missing"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	log := logs.String()
	for _, want := range []string{"method=GET", "path=/about", "status=200", "path=/missing", "status=404"} {
		if !strings.Contains(log, want) {
			t.Fatalf("访问日志缺少 %q：%s", want, log)
		}
	}
}

// TestHandlerHeadOnStaticFile 覆盖静态文件的 HEAD 请求：状态码与头正常、无响应体。
func TestHandlerHeadOnStaticFile(t *testing.T) {
	handler := newTestHandler(loadTestSite(t, testSiteFiles()))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/static/css/a.css", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", recorder.Code)
	}
	if body := recorder.Body.String(); body != "" {
		t.Fatalf("HEAD 不应返回响应体：%q", body)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q，期望 text/css", got)
	}
}

// TestWatchKeepsServingAfterBrokenTemplate 覆盖监听链路里的失败重载分支：
// 坏模板下继续提供上一份可用页面，并在日志里报错。
func TestWatchKeepsServingAfterBrokenTemplate(t *testing.T) {
	dir := writeSite(t, map[string]string{"about.html": "<html>OLD</html>"})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	logs := new(syncBuffer)
	stop, err := watchSite(site, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("watchSite: %v", err)
	}
	defer stop()

	writeFile(t, filepath.Join(dir, "about.html"), "<html>{{template ")
	waitFor(t, 5*time.Second, func() bool {
		return strings.Contains(logs.String(), "站点重载失败")
	}, "坏模板应在日志里报重载失败")

	body, err := site.Render("about")
	if err != nil {
		t.Fatalf("坏模板下应继续提供旧页面: %v", err)
	}
	if !strings.Contains(string(body), "OLD") {
		t.Fatalf("旧页面内容丢失: %s", body)
	}
}

// TestWatchNoticesNewFiles 覆盖新增文件也能被监听：新页面出现后重载即可渲染。
func TestWatchNoticesNewFiles(t *testing.T) {
	dir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	stop, err := watchSite(site, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("watchSite: %v", err)
	}
	defer stop()

	writeFile(t, filepath.Join(dir, "docs", "new.html"), "<html>NEW-PAGE</html>")
	waitFor(t, 5*time.Second, func() bool {
		return site.hasPage("docs/new")
	}, "新增页面应被重载发现")

	body, err := site.Render("docs/new")
	if err != nil {
		t.Fatalf("渲染新页面: %v", err)
	}
	if !strings.Contains(string(body), "NEW-PAGE") {
		t.Fatalf("新页面内容不正确: %s", body)
	}
}
