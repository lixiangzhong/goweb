package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckPassesWithoutWriting 覆盖 check 的核心承诺：跑完整校验、退出码 0、不写任何产物。
func TestCheckPassesWithoutWriting(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html": `<html><body><a href="/about.html">关于</a>` +
			`<img src="/static/logo.svg"><a href="https://example.com/x">外链</a>` +
			`<a href="#top">锚点</a><img src="data:image/gif;base64,R0lGOD"></body></html>`,
		"pages/about.html":    `<html><body>ABOUT</body></html>`,
		"templates/data.json": `{"site":{"title":"CHECK"}}`,
		"static/logo.svg":     "<svg xmlns=\"http://www.w3.org/2000/svg\"/>",
		".goweb.yml":          "dir: pages\nassets: .\n",
	})
	out := filepath.Join(dir, "dist")

	_, stderr, err := executeCLIInSite(t, dir, "check")
	if err != nil {
		t.Fatalf("check 应当通过: %v（stderr: %s）", err, stderr)
	}
	if !strings.Contains(stderr, "检查通过") || !strings.Contains(stderr, "pages=2") {
		t.Fatalf("日志应报告校验结果: %s", stderr)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("check 不应创建产物目录: %v", statErr)
	}
	// 也不该清理已有产物：先 build 一次，再 check，产物必须原封不动。
	if _, _, err := executeCLIInSite(t, dir, "build"); err != nil {
		t.Fatalf("build 出错: %v", err)
	}
	before := listFiles(t, out)
	if _, _, err := executeCLIInSite(t, dir, "check"); err != nil {
		t.Fatalf("check 出错: %v", err)
	}
	if after := listFiles(t, out); !equalStrings(after, before) {
		t.Fatalf("check 不应改动已有产物：%v → %v", before, after)
	}
}

// TestCheckReportsAllProblems 覆盖「汇总所有问题再退出」：
// 页面渲染失败、产物路径冲突、站内链接 404 要一次性全部列出。
func TestCheckReportsAllProblems(t *testing.T) {
	dir := writeSite(t, map[string]string{
		// 1) 渲染失败
		"pages/broken.html": `<html><body>{{fail "故意失败"}}</body></html>`,
		// 2) 坏链接：页面根目录名（页面不在 URL 里）、指向目录、指向不存在的文件
		"pages/index.html": `<html><body>` +
			`<a href="/pages/about.html">多了页面根这一层</a>` +
			`<a href="/static">指向目录</a>` +
			`<img src="/static/missing.png">` +
			`<a href="/about.html">正常</a>` +
			`</body></html>`,
		"pages/about.html":    `<html><body>ABOUT</body></html>`,
		"templates/data.json": `{"site":{"title":"CHECK"}}`,
		// 3) 产物路径冲突：页面根内的 static/a.js 与资源根下的 static/a.js 撞同一路径
		"pages/static/app.js": `<html><body>PAGE</body></html>`,
		"static/app.js":       "console.log(1)",
		".goweb.yml":          "dir: pages\nassets: .\n",
	})

	_, _, err := executeCLIInSite(t, dir, "check")
	if err == nil {
		t.Fatal("有问题时 check 应当失败")
	}
	message := err.Error()
	for _, want := range []string{
		"故意失败",   // 渲染失败
		"产物路径冲突", // 冲突检测
		"链接在产物里不存在：/pages/about.html",   // 页面根那一层
		"链接在产物里不存在：/static（",            // 指向目录
		"链接在产物里不存在：/static/missing.png", // 不存在的文件
	} {
		if !strings.Contains(message, want) {
			t.Errorf("汇总信息缺少 %q，实际：\n%s", want, message)
		}
	}
	if strings.Contains(message, "链接在产物里不存在：/about.html") {
		t.Errorf("正常链接不应被报告：\n%s", message)
	}
}

// TestCheckLinksCanBeDisabled 覆盖 --links=false：链接问题不再报告，其它问题照旧。
func TestCheckLinksCanBeDisabled(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":    `<html><body><a href="/pages/about.html">坏链接</a></body></html>`,
		"pages/about.html":    `<html><body>ABOUT</body></html>`,
		"templates/data.json": `{"site":{"title":"CHECK"}}`,
		".goweb.yml":          "dir: pages\nassets: .\n",
	})

	if _, _, err := executeCLIInSite(t, dir, "check"); err == nil {
		t.Fatal("默认应检查链接并因此失败")
	}
	if _, stderr, err := executeCLIInSite(t, dir, "check", "--links=false"); err != nil {
		t.Fatalf("关闭链接检查后应当通过: %v（stderr: %s）", err, stderr)
	}
}

// TestCheckReportsLinksToIgnoredTargets 覆盖链接指向「不会进产物」的目标。
//
// 被忽略规则排除的页面在产物里不存在。这条比链接检查更早被抓住：fingerprint 的安全网
// 在渲染阶段就会报错（「资源被忽略规则排除，产物里不会有它」），check 汇总时照样列出它。
func TestCheckReportsLinksToIgnoredTargets(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":    `<html><body><a href="/draft.html">草稿</a></body></html>`,
		"pages/draft.html":    `<html><body>DRAFT</body></html>`,
		"templates/data.json": `{"site":{"title":"CHECK"}}`,
		".goweb.yml":          "dir: pages\nassets: .\n",
		".gitignore":          "draft.html\n",
	})

	_, _, err := executeCLIInSite(t, dir, "check")
	if err == nil {
		t.Fatal("链接指向被忽略的页面时应当失败（产物里没有它）")
	}
	for _, want := range []string{"/draft.html", "忽略", "index"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息应包含 %q，实际: %v", want, err)
		}
	}
}

// TestURLIsPublished 覆盖单个 URL 的产出判定，重点是「URL 必须与产物路径一致」。
func TestURLIsPublished(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":   `<html>INDEX</html>`,
		"pages/about.html":   `<html>ABOUT</html>`,
		"pages/draft.html":   `<html>DRAFT</html>`,
		"pages/img/logo.png": "PNG",
		"pages/data.json":    `{"a":1}`,
		"static/app.js":      "console.log(1)",
		".gitignore":         "draft.html\n",
	})
	site, err := LoadSite(SiteOptions{
		ProjectRoot: dir, Dir: "pages", TemplateDir: "templates", AssetsRoot: ".",
		Publishing: true, UseGitignore: true,
	})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	tests := map[string]bool{
		"/index.html":         true,  // 页面（try_files 的真实文件命中）
		"/index":              true,  // 页面（$uri.html）
		"/about.html":         true,  // 页面
		"/static/app.js":      true,  // 资源：源码 static/app.js → 产物 static/app.js → URL 一致
		"/img/logo.png":       true,  // 页面根内的资源：按页面根映射 → 产物 img/logo.png → URL 一致
		"/data.json":          true,  // 同上（页面根内的非 html 文件）
		"/draft.html":         false, // 被忽略的页面不进产物
		"/pages/about.html":   false, // 页面源文件不会作为静态文件发布（多写了页面根那一层）
		"/pages/img/logo.png": false, // 资源会发布，但发布在 /img/logo.png，不是这个 URL
		"/pages/data.json":    false, // 同上
		"/static/missing.png": false, // 不存在
		"/static":             false, // 目录，不提供列表
		"https://x.com/a.css": false, // 外链
	}
	for urlPath, want := range tests {
		if got := site.urlIsPublished(urlPath); got != want {
			t.Errorf("urlIsPublished(%s) = %v，期望 %v", urlPath, got, want)
		}
	}
}
