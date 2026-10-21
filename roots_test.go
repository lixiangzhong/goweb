package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThreeRoots 覆盖用户要的布局：页面在 pages/、模板在 templates/、资源在 static/，
// 三个根彼此独立，--dir 不影响模板加载。
func TestThreeRoots(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":       `<html><body>{{template "header" .}}<img src="/static/logo.png"></body></html>`,
		"pages/about/index.html": `<html><body><h1>ABOUT</h1></body></html>`,
		"templates/header.html":  `{{define "header"}}<header>{{.site.title}}</header>{{end}}`,
		"templates/data.json":    `{"site":{"title":"THREE-ROOTS"}}`,
		"static/logo.png":        "PNG",
		"static/app.js":          "console.log(1)",
	})

	site, err := LoadSite(SiteOptions{
		ProjectRoot: dir,
		Dir:         "pages",
		TemplateDir: "templates",
		AssetsRoot:  ".",
		Fingerprint: true,
	})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	// 页面名相对页面根。
	if got := site.Pages(); !equalStrings(got, []string{"about/index", "index"}) {
		t.Fatalf("页面清单 = %v，期望 [about/index index]", got)
	}

	// 模板与数据来自模板根，与页面根无关。
	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染 index: %v", err)
	}
	if !strings.Contains(string(body), "THREE-ROOTS") {
		t.Fatalf("局部模板未取到模板根里的 data.json: %s", body)
	}

	// URL 解析：页面根空间 + 资源根空间。
	tests := map[string]RouteKind{
		"/":                 RoutePage,
		"/about/":           RoutePage,
		"/static/logo.png":  RouteFile,
		"/static/app.js":    RouteFile,
		"/pages/index.html": RouteNotFound, // 页面根空间里没有 pages/ 这一层
		"/templates/header": RouteNotFound, // 模板根不可访问
	}
	for urlPath, wantKind := range tests {
		if got := site.Resolve(urlPath).Kind; got != wantKind {
			t.Errorf("Resolve(%q).Kind = %v，期望 %v", urlPath, got, wantKind)
		}
	}

	// 产物：页面按页面根落盘，资源按资源根落盘。
	out := filepath.Join(t.TempDir(), "dist")
	result, err := Generate(site, GenOptions{Out: out, Clean: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Pages != 2 || result.Assets != 2 {
		t.Fatalf("页面数/资源数 = %d/%d，期望 2/2", result.Pages, result.Assets)
	}
	want := []string{"about/index.html", "index.html", "static/app.js", "static/logo.png"}
	if got := listFiles(t, out); !equalStrings(got, want) {
		t.Fatalf("产物清单 = %v，期望 %v", got, want)
	}
	if index := readFile(t, filepath.Join(out, "index.html")); !strings.Contains(index, "/static/logo.png?v=") {
		t.Fatalf("产物里的资源引用应带版本号: %s", index)
	}
}

// TestIgnorePatternsOnlyAffectGen 覆盖「忽略规则只作用于 gen，serve 仍可见」。
func TestIgnorePatternsOnlyAffectGen(t *testing.T) {
	files := map[string]string{
		"pages/index.html": `<html><body>INDEX</body></html>`,
		"pages/draft.html": `<html><body>DRAFT</body></html>`,
		"static/keep.css":  "body{}",
		"static/skip.tmp":  "临时文件",
		".gitignore":       "*.tmp\npages/draft.html\n",
	}
	dir := writeSite(t, files)

	serve, err := LoadSite(SiteOptions{ProjectRoot: dir, Dir: "pages", AssetsRoot: ".", UseGitignore: true})
	if err != nil {
		t.Fatalf("serve 侧 LoadSite: %v", err)
	}
	// serve：草稿页面仍被发现、仍可访问；被忽略的静态文件也仍可访问。
	if got := serve.Pages(); !equalStrings(got, []string{"draft", "index"}) {
		t.Fatalf("serve 侧页面清单 = %v，期望包含草稿", got)
	}
	if got := serve.Resolve("/draft").Kind; got != RoutePage {
		t.Fatalf("serve 侧 /draft 应为页面，实际 %v", got)
	}
	if got := serve.Resolve("/static/skip.tmp").Kind; got != RouteFile {
		t.Fatalf("serve 侧被忽略的静态文件仍应可访问，实际 %v", got)
	}

	gen, err := LoadSite(SiteOptions{ProjectRoot: dir, Dir: "pages", AssetsRoot: ".", UseGitignore: true, Publishing: true})
	if err != nil {
		t.Fatalf("gen 侧 LoadSite: %v", err)
	}
	out := filepath.Join(t.TempDir(), "dist")
	result, err := Generate(gen, GenOptions{Out: out, Clean: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Pages != 1 {
		t.Fatalf("gen 侧页面数 = %d，期望 1（草稿被忽略）", result.Pages)
	}
	want := []string{"index.html", "static/keep.css"}
	if got := listFiles(t, out); !equalStrings(got, want) {
		t.Fatalf("gen 产物 = %v，期望 %v（被忽略的文件不进产物）", got, want)
	}
}

// TestIgnoredAssetReferencedByPageFailsGen 覆盖：gen 时页面引用了被忽略的资源 → 报错。
func TestIgnoredAssetReferencedByPageFailsGen(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":    `<html><body><img src="/static/dev-only.png"></body></html>`,
		"static/dev-only.png": "PNG",
		".gitignore":          "*.png\n",
	})

	gen, err := LoadSite(SiteOptions{
		ProjectRoot: dir, Dir: "pages", AssetsRoot: ".",
		UseGitignore: true, Publishing: true, Fingerprint: true,
	})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	if _, err := Generate(gen, GenOptions{Out: filepath.Join(t.TempDir(), "dist"), Clean: true}); err == nil {
		t.Fatal("引用被忽略资源时 gen 应当报错")
	} else if !strings.Contains(err.Error(), "忽略") {
		t.Fatalf("错误信息应说明资源被忽略: %v", err)
	}

	// serve 侧同样引用不报错（本地可见）。
	serve, err := LoadSite(SiteOptions{
		ProjectRoot: dir, Dir: "pages", AssetsRoot: ".",
		UseGitignore: true, Publishing: false, Fingerprint: true,
	})
	if err != nil {
		t.Fatalf("serve 侧 LoadSite: %v", err)
	}
	if _, err := serve.Render("index"); err != nil {
		t.Fatalf("serve 侧不应因被忽略的资源报错: %v", err)
	}
}

// TestArtifactPathConflict 覆盖页面产物与资源产物撞同一路径 → 报错并列出两个来源。
func TestArtifactPathConflict(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/static/app.js": `<html><body>PAGE</body></html>`, // 页面根内 → dist/static/app.js
		"static/app.js":       "console.log(1)",                 // 资源根 → dist/static/app.js
	})
	site, err := LoadSite(SiteOptions{ProjectRoot: dir, Dir: "pages", AssetsRoot: "."})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	_, err = Generate(site, GenOptions{Out: filepath.Join(t.TempDir(), "dist"), Clean: true})
	if err == nil {
		t.Fatal("产物路径冲突应当报错")
	}
	if !strings.Contains(err.Error(), "产物路径冲突") {
		t.Fatalf("错误信息应说明冲突: %v", err)
	}
	for _, want := range []string{"pages/static/app.js", "static/app.js"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息应列出两个来源（缺少 %s）: %v", want, err)
		}
	}
}

// TestConfigFileDrivesCLI 覆盖 .goweb.yml 生效、flag 覆盖配置、以及未知字段报错。
func TestConfigFileDrivesCLI(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":    "<html>INDEX</html>",
		"templates/data.json": `{"site":{"title":"CFG"}}`,
		"static/a.css":        "body{}",
		".goweb.yml":          "dir: pages\nout: public\n",
	})

	t.Run("配置生效", func(t *testing.T) {
		if _, stderr, err := executeCLIInSite(t, dir, "gen"); err != nil {
			t.Fatalf("gen 出错: %v（stderr: %s）", err, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "public", "index.html")); err != nil {
			t.Fatalf("产物应在配置指定的 public/ 下: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "public", "static", "a.css")); err != nil {
			t.Fatalf("资源应进产物: %v", err)
		}
	})

	t.Run("flag 覆盖配置", func(t *testing.T) {
		if _, stderr, err := executeCLIInSite(t, dir, "gen", "--out", "out2"); err != nil {
			t.Fatalf("gen 出错: %v（stderr: %s）", err, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "out2", "index.html")); err != nil {
			t.Fatalf("flag 应覆盖配置里的 out: %v", err)
		}
	})

	t.Run("未知字段报错", func(t *testing.T) {
		broken := writeSite(t, map[string]string{
			"index.html": "<html>INDEX</html>",
			".goweb.yml": "dirr: pages\n",
		})
		_, _, err := executeCLIInSite(t, broken, "gen")
		if err == nil {
			t.Fatal("未知配置字段应当报错，避免拼错后静默无效")
		}
		// 报错要能照着直接改：指出拼错的字段并列出可用字段，且不暴露内部 Go 类型名。
		for _, want := range []string{"dirr", "可用字段", "template", "gitignore"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("错误信息应包含 %q，实际: %v", want, err)
			}
		}
		if strings.Contains(err.Error(), "main.fileConfig") {
			t.Fatalf("不应把内部 Go 类型名暴露给用户: %v", err)
		}
	})
}

// TestConfigUpwardDiscovery 覆盖从子目录运行时向上找到 .goweb.yml，项目根 = 它的目录。
func TestConfigUpwardDiscovery(t *testing.T) {
	root := writeSite(t, map[string]string{
		"pages/index.html": "<html>INDEX</html>",
		".goweb.yml":       "dir: pages\nout: dist\n",
	})
	nested := filepath.Join(root, "some", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("建子目录: %v", err)
	}

	// 从深层子目录运行，项目根应回退到 .goweb.yml 所在目录。
	if _, stderr, err := executeCLIInSite(t, nested, "gen"); err != nil {
		t.Fatalf("gen 出错: %v（stderr: %s）", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "dist", "index.html")); err != nil {
		t.Fatalf("产物应落在项目根下: %v", err)
	}
}

// TestResolveRoot 覆盖根解析：绝对路径原样、相对路径相对项目根、空值用 fallback。
func TestResolveRoot(t *testing.T) {
	projectRoot := filepath.FromSlash("/tmp/project")

	if got, err := resolveRoot(projectRoot, "pages", "."); err != nil || got != filepath.Join(projectRoot, "pages") {
		t.Fatalf("相对路径解析 = %q（err=%v），期望 %q", got, err, filepath.Join(projectRoot, "pages"))
	}
	if got, err := resolveRoot(projectRoot, filepath.FromSlash("/abs/pages"), "."); err != nil || got != filepath.FromSlash("/abs/pages") {
		t.Fatalf("绝对路径应原样使用，实际 %q（err=%v）", got, err)
	}
	if got, err := resolveRoot(projectRoot, "", "."); err != nil || got != projectRoot {
		t.Fatalf("空值应使用 fallback，实际 %q（err=%v）", got, err)
	}
	if got, err := resolveRoot(projectRoot, "", ""); err != nil || got != "" {
		t.Fatalf("空值与空 fallback 应得到空根，实际 %q（err=%v）", got, err)
	}
}

// TestConfigExplicitPath 覆盖 --config 显式指定（项目根 = 该文件所在目录）。
func TestConfigExplicitPath(t *testing.T) {
	root := writeSite(t, map[string]string{
		"pages/index.html": "<html>INDEX</html>",
		"conf/.goweb.yml":  "dir: ../pages\nout: built\n",
	})
	// 在 root 里运行，但配置文件在 root/conf 下：项目根应是 root/conf，
	// 配置里的相对路径（../pages、built）相对它解析。
	if _, stderr, err := executeCLIInSite(t, root, "gen", "--config", filepath.Join("conf", ".goweb.yml")); err != nil {
		t.Fatalf("gen 出错: %v（stderr: %s）", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "conf", "built", "index.html")); err != nil {
		t.Fatalf("产物应落在配置文件所在目录的 built/ 下: %v", err)
	}
}

// TestPickPrecedence 覆盖 flag > 配置 > 默认 的取值规则。
func TestPickPrecedence(t *testing.T) {
	fromConfig, fallback := "from-config", "from-default"

	if got := pickString("from-flag", true, &fromConfig, fallback); got != "from-flag" {
		t.Fatalf("flag 应优先，实际 %q", got)
	}
	if got := pickString("ignored", false, &fromConfig, fallback); got != fromConfig {
		t.Fatalf("没有 flag 时应用配置，实际 %q", got)
	}
	if got := pickString("ignored", false, nil, fallback); got != fallback {
		t.Fatalf("都没有时应用默认，实际 %q", got)
	}

	yes, no := true, false
	if got := pickBool(false, true, &yes, true); got {
		t.Fatal("flag 应优先于配置")
	}
	if got := pickBool(false, false, &yes, false); !got {
		t.Fatal("没有 flag 时应用配置")
	}
	if got := pickBool(false, false, nil, true); !got {
		t.Fatal("都没有时应用默认")
	}
	if got := pickBool(true, false, &no, true); got {
		t.Fatal("配置为 false 时应覆盖默认 true")
	}
}

// TestTemplateRootOutsideProject 覆盖模板根在项目根之外的情况。
func TestTemplateRootOutsideProject(t *testing.T) {
	shared := t.TempDir()
	if err := os.WriteFile(filepath.Join(shared, "header.html"),
		[]byte(`{{define "header"}}<header>SHARED</header>{{end}}`), 0o644); err != nil {
		t.Fatalf("写共享模板: %v", err)
	}

	project := writeSite(t, map[string]string{
		"pages/index.html": `<html><body>{{template "header" .}}</body></html>`,
	})
	site, err := LoadSite(SiteOptions{
		ProjectRoot: project,
		Dir:         "pages",
		TemplateDir: shared, // 绝对路径，位于项目根之外
	})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	if !strings.Contains(string(body), "SHARED") {
		t.Fatalf("项目根之外的模板根未被加载: %s", body)
	}
	// 模板名退回「相对模板根」的写法。
	if _, err := site.Render("header"); err != nil {
		t.Fatalf("模板名应为相对模板根（header）: %v", err)
	}
}
