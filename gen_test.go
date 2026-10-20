package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generateTestSite 铺站点并生成到 dist，返回页面目录、站点与生成结果。
func generateTestSite(t *testing.T, files map[string]string, clean bool) (string, *Site, GenResult) {
	t.Helper()
	dir := writeSite(t, files)
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "template", ExcludeDirs: []string{"dist"}})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	result, err := Generate(site, GenOptions{Out: filepath.Join(dir, "dist"), Clean: clean})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return dir, site, result
}

func TestGenerateOutputTree(t *testing.T) {
	dir, _, result := generateTestSite(t, testSiteFiles(), true)
	out := filepath.Join(dir, "dist")

	wantFiles := []string{
		"index.html",
		"about/index.html",
		"help/index.html",
		"help/74/index.html",
		"UPPER/index.html",
		"static/css/a.css",
		"robots.txt",
	}
	for _, name := range wantFiles {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Fatalf("产物缺少 %s: %v", name, err)
		}
	}

	// 页面源文件、局部模板、被忽略目录都不应进入产物。
	unwanted := []string{
		"about.html",
		"template/header.html",
		"template/note.txt",
		"node_modules/pkg/a.html",
		".hidden/secret.html",
		"dist/old.html",
	}
	for _, name := range unwanted {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err == nil {
			t.Fatalf("产物不应包含 %s", name)
		}
	}

	if result.Pages != 5 {
		t.Fatalf("页面数 = %d，期望 5", result.Pages)
	}
	if result.Assets != 2 {
		t.Fatalf("资源数 = %d，期望 2", result.Assets)
	}

	body, err := os.ReadFile(filepath.Join(out, "about", "index.html"))
	if err != nil {
		t.Fatalf("读取产物页面: %v", err)
	}
	if !strings.Contains(string(body), "SITE-HEADER") || !strings.Contains(string(body), "ABOUT") {
		t.Fatalf("产物页面内容不正确: %s", body)
	}
}

func TestGenerateCleanBehavior(t *testing.T) {
	dir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "template", ExcludeDirs: []string{"dist"}})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	out := filepath.Join(dir, "dist")
	stale := filepath.Join(out, "stale.html")

	if _, err := Generate(site, GenOptions{Out: out, Clean: true}); err != nil {
		t.Fatalf("首次生成: %v", err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatalf("写入遗留文件: %v", err)
	}

	if _, err := Generate(site, GenOptions{Out: out, Clean: false}); err != nil {
		t.Fatalf("增量生成: %v", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("clean=false 时不应删除遗留文件: %v", err)
	}

	if _, err := Generate(site, GenOptions{Out: out, Clean: true}); err != nil {
		t.Fatalf("清空生成: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("clean=true 时应清空遗留文件")
	}
}

// TestGenerateGuardsOutDir 确保危险产物目录在任何删除动作之前就被拦截。
func TestGenerateGuardsOutDir(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":           "<html>INDEX</html>",
		"template/header.html": `{{define "header"}}<header>SITE-HEADER</header>{{end}}`,
	})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "template"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	guarded := []string{
		site.Root(),
		filepath.Dir(site.Root()),
		filepath.Join(site.Root(), "template"),
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		guarded = append(guarded, home)
	}

	for _, out := range guarded {
		if _, err := Generate(site, GenOptions{Out: out, Clean: true}); err == nil {
			t.Fatalf("产物目录 %q 应当被拦截", out)
		}
	}

	// 源码必须在拦截后仍然完好。
	if _, err := os.Stat(filepath.Join(site.Root(), "index.html")); err != nil {
		t.Fatalf("页面源文件被破坏: %v", err)
	}
	if _, err := os.Stat(filepath.Join(site.Root(), "template")); err != nil {
		t.Fatalf("局部模板目录被破坏: %v", err)
	}
}

// TestGenerateFailsFastOnBrokenPage 覆盖渲染期报错：必须整体失败，不产出半成品页面。
// 站点没有数据源，页面里的 {{.Field}} 就是典型的渲染期错误。
func TestGenerateFailsFastOnBrokenPage(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html": "<html>INDEX</html>",
		"bad.html":   "<html>{{.Field}}</html>",
	})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "template", ExcludeDirs: []string{"dist"}})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	out := filepath.Join(dir, "dist")
	if _, err := Generate(site, GenOptions{Out: out, Clean: true}); err == nil {
		t.Fatal("渲染失败的页面应当让 gen 报错")
	} else if !strings.Contains(err.Error(), "bad") {
		t.Fatalf("错误信息应指出出错页面: %v", err)
	}

	if _, err := os.Stat(filepath.Join(out, "bad", "index.html")); err == nil {
		t.Fatal("渲染失败的页面不应留下产物")
	}
}
