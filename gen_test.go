package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// generateTestSite 铺站点并生成到 dist，返回页面目录、站点与生成结果。
func generateTestSite(t *testing.T, files map[string]string, clean bool) (string, *Site, GenResult) {
	t.Helper()
	dir := writeSite(t, files)
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates", ExcludeDirs: []string{"dist"}})
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
		"about.html",
		"help/index.html",
		"help/74.html",
		"UPPER.html",
		"static/css/a.css",
		"robots.txt",
	}
	for _, name := range wantFiles {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Fatalf("产物缺少 %s: %v", name, err)
		}
	}

	// 页面产物跟随源码路径，不再产出 <页面>/index.html 这类目录式布局；
	// 局部模板与被忽略目录也不应出现在产物中。
	unwanted := []string{
		"about/index.html",
		"help/74/index.html",
		"templates/header.html",
		"templates/data.json",
		"templates/note.txt",
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

	body, err := os.ReadFile(filepath.Join(out, "about.html"))
	if err != nil {
		t.Fatalf("读取产物页面: %v", err)
	}
	if !strings.Contains(string(body), "SITE-HEADER") || !strings.Contains(string(body), "ABOUT") {
		t.Fatalf("产物页面内容不正确: %s", body)
	}
	// 模板数据在产物里同样生效，且 data.json 本身不进入产物。
	if !strings.Contains(string(body), "GOWEB-SITE") {
		t.Fatalf("产物页面缺少模板数据: %s", body)
	}
}

func TestGenerateCleanBehavior(t *testing.T) {
	dir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates", ExcludeDirs: []string{"dist"}})
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
		"index.html":            "<html>INDEX</html>",
		"templates/header.html": `{{define "header"}}<header>SITE-HEADER</header>{{end}}`,
	})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	guarded := []string{
		site.Root(),
		filepath.Dir(site.Root()),
		filepath.Join(site.Root(), "templates"),
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
	if _, err := os.Stat(filepath.Join(site.Root(), "templates")); err != nil {
		t.Fatalf("局部模板目录被破坏: %v", err)
	}
}

// TestGenerateFailsFastOnBrokenPage 覆盖渲染期报错：必须整体失败，不产出半成品页面。
// 引用一个不存在的模板名就是典型的渲染期错误（缺键只会渲染成空，不算错误）。
func TestGenerateFailsFastOnBrokenPage(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html": "<html>INDEX</html>",
		"bad.html":   `<html>{{template "missing" .}}</html>`,
	})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates", ExcludeDirs: []string{"dist"}})
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

// TestGenerateAssets 覆盖资源拷贝的边界：嵌套目录、符号链接、忽略项。
func TestGenerateAssets(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":           "<html>INDEX</html>",
		"static/css/site.css":  "body{}",
		"static/deep/a/b.txt":  "deep",
		"assets/logo.svg":      "<svg/>",
		"node_modules/x/a.css": "dep",
		".hidden/secret.css":   "hidden",
		"templates/data.json":  `{"k":"v"}`,
		"templates/note.txt":   "note",
	})

	// 指向文件的符号链接按内容拷贝；指向目录的符号链接跳过（避免逃出站点或成环）。
	if err := os.Symlink(filepath.Join(dir, "assets", "logo.svg"), filepath.Join(dir, "static", "logo-link.svg")); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "assets"), filepath.Join(dir, "static", "assets-link")); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}

	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates", ExcludeDirs: []string{"dist"}})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	logs := new(syncBuffer)
	result, err := Generate(site, GenOptions{
		Out:    filepath.Join(dir, "dist"),
		Clean:  true,
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	want := []string{
		"assets/logo.svg",
		"index.html",
		"static/css/site.css",
		"static/deep/a/b.txt",
		"static/logo-link.svg",
	}
	if got := listFiles(t, filepath.Join(dir, "dist")); !equalStrings(got, want) {
		t.Fatalf("产物清单 = %v，期望 %v", got, want)
	}
	if result.Assets != 4 {
		t.Fatalf("资源数 = %d，期望 4", result.Assets)
	}
	if !strings.Contains(logs.String(), "跳过指向目录的符号链接") {
		t.Fatalf("日志中缺少跳过符号链接目录的记录: %s", logs.String())
	}
}

// TestGenerateCleanMissingOutDir 覆盖产物目录不存在时 clean 不报错。
func TestGenerateCleanMissingOutDir(t *testing.T) {
	dir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	out := filepath.Join(dir, "dist")
	if _, err := Generate(site, GenOptions{Out: out, Clean: true}); err != nil {
		t.Fatalf("产物目录不存在时 clean 应直接跳过: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "index.html")); err != nil {
		t.Fatalf("产物未生成: %v", err)
	}
}

// TestGenerateFailsWhenOutIsFile 覆盖产物路径被普通文件占用的错误路径。
func TestGenerateFailsWhenOutIsFile(t *testing.T) {
	dir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("写入占位文件: %v", err)
	}
	if _, err := Generate(site, GenOptions{Out: blocked, Clean: true}); err == nil {
		t.Fatal("产物目录被文件占用时应当报错")
	}
}

// TestGenerateFailsWhenTargetIsDirectory 覆盖拷贝目标被目录占用的错误路径。
func TestGenerateFailsWhenTargetIsDirectory(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":   "<html>INDEX</html>",
		"static/a.css": "body{}",
	})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	out := filepath.Join(dir, "dist")
	if err := os.MkdirAll(filepath.Join(out, "static", "a.css"), 0o755); err != nil {
		t.Fatalf("预置同名目录: %v", err)
	}
	if _, err := Generate(site, GenOptions{Out: out, Clean: false}); err == nil {
		t.Fatal("拷贝目标被目录占用时应当报错")
	}
}

// TestGenCommandOutDirBoundary 从 CLI 层锁定产物目录排除的边界（用户明确选择的方案 A）：
//   - 「当前 --out 指定的产物目录」会被自动排除，连跑两次也不会自我嵌套；
//   - 换过产物目录后，旧产物目录仍属于资源根，会被当普通资源拷进新产物。
//
// 因此约定是把产物目录写进 .gitignore / 配置里的 ignore；若哪天改成自动排除旧产物目录，
// 要同步改这条测试与文档。
func TestGenCommandOutDirBoundary(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":   "<html>INDEX</html>",
		"static/a.css": "body{}",
	})

	// 连跑两次默认 gen：第二次 dist/ 里已有内容，仍然不能把自己拷进自己。
	for attempt := 1; attempt <= 2; attempt++ {
		if _, stderr, err := executeCLIInSite(t, dir, "gen"); err != nil {
			t.Fatalf("第 %d 次 gen: %v（stderr: %s）", attempt, err, stderr)
		}
	}
	out := filepath.Join(dir, "dist")
	want := []string{"index.html", "static/a.css"}
	if got := listFiles(t, out); !equalStrings(got, want) {
		t.Fatalf("产物清单 = %v，期望 %v（不应出现自我嵌套）", got, want)
	}

	// 换产物目录：dist/ 不再是「当前 --out」，按资源被拷进 d2。
	if _, stderr, err := executeCLIInSite(t, dir, "gen", "--out", "d2"); err != nil {
		t.Fatalf("gen --out d2: %v（stderr: %s）", err, stderr)
	}
	if got := listFiles(t, filepath.Join(dir, "d2")); !slices.Contains(got, "dist/index.html") {
		t.Fatalf("方案 A 下旧产物目录应被当资源拷贝，实际 = %v", got)
	}
}
