package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// fingerprintSite 铺一个开了资源版本号改写的站点。
func fingerprintSite(t *testing.T, files map[string]string) *Site {
	t.Helper()
	site, err := LoadSite(SiteOptions{
		Dir:         writeSite(t, files),
		TemplateDir: "templates",
		Fingerprint: true,
	})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	return site
}

// assetHashOf 独立算出期望哈希（不从被测代码里取）。
func assetHashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:assetHashLength]
}

func TestFingerprintRewritesSiteAssets(t *testing.T) {
	const css = ".a{color:red}"
	site := fingerprintSite(t, map[string]string{
		"index.html": `<html><head>` +
			`<link rel="stylesheet" href="/static/css/site.css">` +
			`<script src="/static/js/site.js"></script>` +
			`</head><body>` +
			`<img src="/static/img/logo.png" alt="logo">` +
			`<a href="/static/file/report.pdf">报告</a>` +
			`</body></html>`,
		"static/css/site.css":    css,
		"static/js/site.js":      "console.log(1)",
		"static/img/logo.png":    "PNG",
		"static/file/report.pdf": "%PDF",
	})

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	html := string(body)

	for path, content := range map[string]string{
		"/static/css/site.css":    css,
		"/static/js/site.js":      "console.log(1)",
		"/static/img/logo.png":    "PNG",
		"/static/file/report.pdf": "%PDF",
	} {
		want := path + "?v=" + assetHashOf(content)
		if !strings.Contains(html, want) {
			t.Fatalf("资源引用未被改写，缺少 %q：\n%s", want, html)
		}
	}
}

// TestFingerprintLeavesOtherReferencesAlone 覆盖「不该动的东西一律不动」。
func TestFingerprintLeavesOtherReferencesAlone(t *testing.T) {
	site := fingerprintSite(t, map[string]string{
		"index.html": `<html><body>` +
			`<a href="https://example.com/a.css">外链</a>` +
			`<a href="//cdn.example.com/b.css">协议相对</a>` +
			`<a href="#section">锚点</a>` +
			`<a href="mailto:hi@example.com">邮件</a>` +
			`<img src="data:image/gif;base64,R0lGOD">` +
			`<a href="/about.html">站内页面</a>` +
			`<img src="/static/missing.png">` +
			`<img src="/static/already.css?v=custom">` +
			`<a href="/templates/header.html">局部模板</a>` +
			`</body></html>`,
		"about.html":         "<html><body>ABOUT</body></html>",
		"static/already.css": "body{}",
	})

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	html := string(body)

	// 页面链接、外链、模板、data:、锚点、已带查询串都应保持原样。
	for _, want := range []string{
		`href="https://example.com/a.css"`,
		`href="//cdn.example.com/b.css"`,
		`href="#section"`,
		`href="mailto:hi@example.com"`,
		`src="data:image/gif;base64,R0lGOD"`,
		`href="/about.html"`,
		`src="/static/missing.png"`,
		`src="/static/already.css?v=custom"`,
		`href="/templates/header.html"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("不应被改写但被改了：缺少 %q\n%s", want, html)
		}
	}
	if strings.Contains(html, "already.css?v=custom?v=") {
		t.Fatalf("不应重复追加版本号：%s", html)
	}
}

// TestFingerprintKeepsOutputByteIdentical 覆盖没有可改写资源时逐字节不动。
func TestFingerprintKeepsOutputByteIdentical(t *testing.T) {
	source := "<html><b>内容</b><img src=\"/static/missing.png\"></html>"
	plain, err := LoadSite(SiteOptions{Dir: writeSite(t, map[string]string{"index.html": source})})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	fingerprinted := fingerprintSite(t, map[string]string{"index.html": source})

	want, err := plain.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	got, err := fingerprinted.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("没有可改写资源时输出应逐字节一致:\n带改写: %s\n原样  : %s", got, want)
	}
}

// TestFingerprintRelativePaths 覆盖相对路径按页面所在目录解析。
func TestFingerprintRelativePaths(t *testing.T) {
	const css = "body{margin:0}"
	site := fingerprintSite(t, map[string]string{
		"index.html":          `<html><body><link href="static/css/site.css"></body></html>`,
		"help/74.html":        `<html><body><img src="../static/css/site.css"></body></html>`,
		"static/css/site.css": css,
	})

	rootPage, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染根页面: %v", err)
	}
	if want := "static/css/site.css?v=" + assetHashOf(css); !strings.Contains(string(rootPage), want) {
		t.Fatalf("根页面的相对路径未改写，期望 %q：%s", want, rootPage)
	}

	subPage, err := site.Render("help/74")
	if err != nil {
		t.Fatalf("渲染子页面: %v", err)
	}
	if want := "../static/css/site.css?v=" + assetHashOf(css); !strings.Contains(string(subPage), want) {
		t.Fatalf("子页面的相对路径未按页面目录解析，期望 %q：%s", want, subPage)
	}
}

// TestFingerprintSrcset 覆盖 srcset 的多个候选与描述符。
func TestFingerprintSrcset(t *testing.T) {
	const small, large = "SMALL", "LARGE"
	site := fingerprintSite(t, map[string]string{
		"index.html": `<html><body>` +
			`<img srcset="/static/a.png 1x, /static/b.png 2x" src="/static/a.png">` +
			`<img srcset="data:image/gif;base64,R0lGOD, /static/a.png 1x">` +
			`</body></html>`,
		"static/a.png": small,
		"static/b.png": large,
	})

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	html := string(body)

	for _, want := range []string{
		"/static/a.png?v=" + assetHashOf(small) + " 1x",
		"/static/b.png?v=" + assetHashOf(large) + " 2x",
		// data: 的 srcset 整条跳过，保持原样。
		`srcset="data:image/gif;base64,R0lGOD, /static/a.png 1x"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("srcset 处理不符合预期，缺少 %q：\n%s", want, html)
		}
	}
}

// TestFingerprintSkipsStyleContent 覆盖 CSS 内容（内联样式/独立文件）不改写——
// 这是本轮明确不做的部分。
func TestFingerprintSkipsStyleContent(t *testing.T) {
	site := fingerprintSite(t, map[string]string{
		"index.html": `<html><head><style>.x{background:url(/static/bg.png)}</style></head>` +
			`<body><div style="background:url(/static/bg.png)"></div></body></html>`,
		"static/bg.png": "PNG",
	})

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	if count := strings.Count(string(body), "?v="); count != 0 {
		t.Fatalf("CSS 里的 url() 不应被改写，却出现 %d 处版本号：%s", count, body)
	}
}

// TestFingerprintDisabled 覆盖开关关闭时不做任何改写。
func TestFingerprintDisabled(t *testing.T) {
	files := map[string]string{
		"index.html":      `<html><body><link href="/static/site.css"></body></html>`,
		"static/site.css": "body{}",
	}
	site, err := LoadSite(SiteOptions{Dir: writeSite(t, files), TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	if strings.Contains(string(body), "?v=") {
		t.Fatalf("未开启改写时不应出现版本号：%s", body)
	}
}

// TestFingerprintMatchesAssetFunc 覆盖自动改写与 asset 函数给出同一个哈希。
func TestFingerprintMatchesAssetFunc(t *testing.T) {
	const css = ".a{color:red}"
	site := fingerprintSite(t, map[string]string{
		"index.html": `<html><body>` +
			`<link href="/static/site.css">` +
			`<link href="{{asset "/static/site.css"}}">` +
			`</body></html>`,
		"static/site.css": css,
	})

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	want := "/static/site.css?v=" + assetHashOf(css)
	if count := strings.Count(string(body), want); count != 2 {
		t.Fatalf("自动改写与 asset 应给出同一个 URL（期望 2 处），实际 %d 处：%s", count, body)
	}
}

// TestFingerprintRewrittenDocumentStillRenders 覆盖改写后仍是合法 HTML（解析器路线的规范化）。
func TestFingerprintRewrittenDocumentStillRenders(t *testing.T) {
	site := fingerprintSite(t, map[string]string{
		"sub/page.html": `<html><body><img SRC=/static/a.png alt=x><br/></body></html>`,
		"static/a.png":  "PNG",
	})

	body, err := site.Render("sub/page")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	html := string(body)
	// 规范化后的形态：标签小写、属性加引号、自闭合统一。
	for _, want := range []string{`<img src="/static/a.png?v=`, `alt="x"`, `<br/>`} {
		if !strings.Contains(html, want) {
			t.Fatalf("改写后的文档缺少 %q：\n%s", want, html)
		}
	}
}
