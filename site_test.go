package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSite 在临时目录里铺出一个站点，返回页面目录。
func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("创建目录 %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("写入 %s: %v", full, err)
		}
	}
	return dir
}

// loadTestSite 铺站点并加载，测试里统一使用 templates 作为局部模板目录、dist 作为产物目录。
func loadTestSite(t *testing.T, files map[string]string) *Site {
	t.Helper()
	site, err := LoadSite(SiteOptions{
		Dir:         writeSite(t, files),
		TemplateDir: "templates",
		ExcludeDirs: []string{"dist"},
	})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	return site
}

// testSiteFiles 是测试用的站点骨架，覆盖页面、局部模板、数据文件、被忽略目录与静态资源。
func testSiteFiles() map[string]string {
	return map[string]string{
		"index.html":              "<html>INDEX</html>",
		"about.html":              `<html>{{template "header" .}}<main>ABOUT</main></html>`,
		"help/index.html":         "<html>HELP-INDEX</html>",
		"help/74.html":            "<html>HELP-74</html>",
		"templates/header.html":   `{{define "header"}}<header>SITE-HEADER {{.site.title}}</header>{{end}}`,
		"templates/data.json":     `{"site":{"title":"GOWEB-SITE"},"year":2026}`,
		"templates/note.txt":      "只有 html 参与模板解析，data.json 参与数据加载",
		"node_modules/pkg/a.html": "<html>DEP</html>",
		".hidden/secret.html":     "<html>SECRET</html>",
		"dist/old.html":           "<html>STALE</html>",
		"static/css/a.css":        "body{color:red}",
		"robots.txt":              "User-agent: *",
		"UPPER.HTML":              "<html>UPPER</html>",
	}
}

func TestLoadSitePages(t *testing.T) {
	site := loadTestSite(t, testSiteFiles())

	want := []string{"UPPER", "about", "help/74", "help/index", "index"}
	got := site.Pages()
	if len(got) != len(want) {
		t.Fatalf("页面清单 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("页面清单 = %v，期望 %v", got, want)
		}
	}

	// 局部模板不产出页面，但仍注册在共享命名空间里。
	if site.hasPage("templates/header") {
		t.Fatal("局部模板不应出现在页面清单里")
	}
	if _, err := site.Render("templates/header"); err != nil {
		t.Fatalf("局部模板应可渲染: %v", err)
	}
}

func TestResolve(t *testing.T) {
	site := loadTestSite(t, testSiteFiles())
	root := site.Root()

	tests := []struct {
		path       string
		wantKind   RouteKind
		wantTarget string // RouteRedirect 的目标 URL 或 RoutePage 的模板名
	}{
		{path: "/", wantKind: RoutePage, wantTarget: "index"},
		{path: "/about/", wantKind: RoutePage, wantTarget: "about"},
		{path: "/about", wantKind: RouteRedirect, wantTarget: "/about/"},
		{path: "/about.html", wantKind: RouteRedirect, wantTarget: "/about/"},
		{path: "/index.html", wantKind: RouteRedirect, wantTarget: "/"},
		{path: "/help/", wantKind: RoutePage, wantTarget: "help/index"},
		{path: "/help", wantKind: RouteRedirect, wantTarget: "/help/"},
		{path: "/help/74/", wantKind: RoutePage, wantTarget: "help/74"},
		{path: "/help/74", wantKind: RouteRedirect, wantTarget: "/help/74/"},
		{path: "/help/74.html", wantKind: RouteRedirect, wantTarget: "/help/74/"},
		{path: "/help/index.html", wantKind: RouteRedirect, wantTarget: "/help/"},
		{path: "/UPPER/", wantKind: RoutePage, wantTarget: "UPPER"},
		{path: "/UPPER.HTML", wantKind: RouteRedirect, wantTarget: "/UPPER/"},
		{path: "//about//", wantKind: RouteRedirect, wantTarget: "/about/"},
		{path: "/static/css/a.css", wantKind: RouteFile},
		{path: "/robots.txt", wantKind: RouteFile},
		{path: "/templates/header.html", wantKind: RouteNotFound},
		{path: "/templates/header", wantKind: RouteNotFound},
		{path: "/templates/data.json", wantKind: RouteNotFound},
		{path: "/templates/note.txt", wantKind: RouteNotFound},
		{path: "/static/", wantKind: RouteNotFound},
		{path: "/missing", wantKind: RouteNotFound},
		{path: "/missing/", wantKind: RouteNotFound},
		{path: "/dist/old", wantKind: RouteNotFound},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			route := site.Resolve(test.path)
			if route.Kind != test.wantKind {
				t.Fatalf("Resolve(%q).Kind = %v，期望 %v", test.path, route.Kind, test.wantKind)
			}
			switch route.Kind {
			case RouteRedirect:
				if route.Location != test.wantTarget {
					t.Fatalf("重定向目标 = %q，期望 %q", route.Location, test.wantTarget)
				}
			case RoutePage:
				if route.Name != test.wantTarget {
					t.Fatalf("页面模板名 = %q，期望 %q", route.Name, test.wantTarget)
				}
			case RouteFile:
				if !strings.HasPrefix(route.File, root+string(filepath.Separator)) {
					t.Fatalf("静态文件 %q 越出页面目录 %q", route.File, root)
				}
			}
		})
	}
}

// TestResolveNeverEscapesRoot 确保带 .. 的路径不会落到页面目录之外的磁盘文件。
func TestResolveNeverEscapesRoot(t *testing.T) {
	site := loadTestSite(t, testSiteFiles())
	root := site.Root()

	for _, path := range []string{"/../etc/passwd", "/static/../../etc/passwd", "/%2e%2e/etc/passwd"} {
		route := site.Resolve(path)
		if route.Kind == RouteFile && !strings.HasPrefix(route.File, root+string(filepath.Separator)) {
			t.Fatalf("Resolve(%q) 逃出页面目录: %s", path, route.File)
		}
	}
}

func TestRenderInlinesPartial(t *testing.T) {
	site := loadTestSite(t, testSiteFiles())

	body, err := site.Render("about")
	if err != nil {
		t.Fatalf("渲染 about: %v", err)
	}
	html := string(body)
	if !strings.Contains(html, "SITE-HEADER") {
		t.Fatalf("局部模板未展开: %s", html)
	}
	if !strings.Contains(html, "ABOUT") {
		t.Fatalf("页面正文丢失: %s", html)
	}
	// data.json 对局部模板同样可用。
	if !strings.Contains(html, "GOWEB-SITE") {
		t.Fatalf("局部模板未拿到模板数据: %s", html)
	}
}

func TestRenderUnknownTemplate(t *testing.T) {
	site := loadTestSite(t, testSiteFiles())
	if _, err := site.Render("nope"); err == nil {
		t.Fatal("渲染不存在的模板应当报错")
	}
}

// TestRenderWithDataJSON 覆盖模板数据的取值方式：嵌套对象、数字与 range 循环。
func TestRenderWithDataJSON(t *testing.T) {
	site := loadTestSite(t, map[string]string{
		"index.html": `<p>{{.site.title}}|{{.year}}|{{range .items}}{{.name}},{{end}}|{{.missing}}</p>`,
		"templates/data.json": `{
			"site": {"title": "我的站点"},
			"year": 2026,
			"items": [{"name": "a"}, {"name": "b"}]
		}`,
	})

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染 index: %v", err)
	}
	want := "<p>我的站点|2026|a,b,|</p>"
	if string(body) != want {
		t.Fatalf("渲染结果 = %q，期望 %q", body, want)
	}
}

// TestDataFileMissingMeansNoData 覆盖没有 data.json 的站点：不报错，缺值渲染为空。
func TestDataFileMissingMeansNoData(t *testing.T) {
	site := loadTestSite(t, map[string]string{"index.html": `<p>[{{.site.title}}]</p>`})

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染 index: %v", err)
	}
	if string(body) != "<p>[]</p>" {
		t.Fatalf("渲染结果 = %q，期望 %q", body, "<p>[]</p>")
	}
}

// TestDataFileInvalid 覆盖数据文件本身出错：JSON 语法错误或顶层不是对象都必须报错。
func TestDataFileInvalid(t *testing.T) {
	tests := map[string]string{
		"JSON 语法错误": `{"site": }`,
		"顶层不是对象":    `[1, 2, 3]`,
		"顶层是字符串":    `"just a string"`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			dir := writeSite(t, map[string]string{
				"index.html":          "<html>INDEX</html>",
				"templates/data.json": data,
			})
			if _, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"}); err == nil {
				t.Fatalf("数据文件 %s 应当让加载失败", data)
			}
		})
	}
}

// TestDataFileWithoutTemplateDir 覆盖未配置模板目录时没有数据来源。
func TestDataFileWithoutTemplateDir(t *testing.T) {
	site := loadTestSite(t, testSiteFiles())
	if site.DataFile() == "" {
		t.Fatal("配置了模板目录时数据文件路径不应为空")
	}

	plain, err := LoadSite(SiteOptions{Dir: writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	if plain.DataFile() != "" {
		t.Fatalf("未配置模板目录时数据文件路径应为空，实际 %q", plain.DataFile())
	}
}

func TestTemplateFuncs(t *testing.T) {
	site := loadTestSite(t, map[string]string{
		"index.html": `<p>{{"<b>raw</b>"|HTML}}</p><script>const data = {{JS "x"}};</script>`,
	})

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染 index: %v", err)
	}
	html := string(body)
	if !strings.Contains(html, "<b>raw</b>") {
		t.Fatalf("HTML 函数未原样输出: %s", html)
	}
	if !strings.Contains(html, `const data = "x";`) {
		t.Fatalf("JS 函数未输出 JSON 字面量: %s", html)
	}
}

func TestReloadKeepsPreviousTemplatesOnError(t *testing.T) {
	dir := writeSite(t, map[string]string{"about.html": "<html>OLD</html>"})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	// 模板语法错误时保留上一份可用模板。
	if err := os.WriteFile(filepath.Join(dir, "about.html"), []byte("<html>{{template "), 0o644); err != nil {
		t.Fatalf("写入坏模板: %v", err)
	}
	if err := site.Reload(); err == nil {
		t.Fatal("坏模板应当让 Reload 报错")
	}
	body, err := site.Render("about")
	if err != nil {
		t.Fatalf("渲染应沿用旧模板: %v", err)
	}
	if !strings.Contains(string(body), "OLD") {
		t.Fatalf("旧模板内容丢失: %s", body)
	}

	// 修好后重新生效。
	if err := os.WriteFile(filepath.Join(dir, "about.html"), []byte("<html>NEW</html>"), 0o644); err != nil {
		t.Fatalf("写入新模板: %v", err)
	}
	if err := site.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	body, err = site.Render("about")
	if err != nil {
		t.Fatalf("渲染 about: %v", err)
	}
	if !strings.Contains(string(body), "NEW") {
		t.Fatalf("新模板未生效: %s", body)
	}
}

func TestIgnored(t *testing.T) {
	site := loadTestSite(t, testSiteFiles())
	root := site.Root()

	tests := map[string]bool{
		filepath.Join(root, "about.html"):               false,
		filepath.Join(root, ".git", "config"):           true,
		filepath.Join(root, "node_modules", "a.js"):     true,
		filepath.Join(root, "dist", "index.html"):       true,
		filepath.Join(filepath.Dir(root), "other.html"): true,
	}
	for path, want := range tests {
		if got := site.Ignored(path); got != want {
			t.Fatalf("Ignored(%q) = %v，期望 %v", path, got, want)
		}
	}
}

func TestCanonicalPath(t *testing.T) {
	tests := map[string]string{
		"":          "/",
		"/":         "/",
		"about":     "/about",
		"/about/":   "/about/",
		"//about//": "/about/",
		"/a/../b":   "/b",
		"/a/./b/":   "/a/b/",
		"/../etc":   "/etc",
	}
	for input, want := range tests {
		if got := canonicalPath(input); got != want {
			t.Fatalf("canonicalPath(%q) = %q，期望 %q", input, got, want)
		}
	}
}

func TestPageOutput(t *testing.T) {
	tests := map[string]string{
		"index":      "index.html",
		"about":      "about/index.html",
		"help/index": "help/index.html",
		"help/74":    "help/74/index.html",
	}
	for name, want := range tests {
		if got := pageOutput(name); got != want {
			t.Fatalf("pageOutput(%q) = %q，期望 %q", name, got, want)
		}
	}
}
