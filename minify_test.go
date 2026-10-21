package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestMinifyMediaType 覆盖扩展名到压缩类型的映射；不在名单里的必须返回空（原样拷贝）。
func TestMinifyMediaType(t *testing.T) {
	tests := map[string]string{
		"a.html":      mediaTypeHTML,
		"a.htm":       mediaTypeHTML,
		"A.HTML":      mediaTypeHTML,
		"a.css":       mediaTypeCSS,
		"a.js":        mediaTypeJS,
		"a.mjs":       mediaTypeJS,
		"a.json":      mediaTypeJSON,
		"a.svg":       mediaTypeSVG,
		"a.xml":       mediaTypeXML,
		"a.png":       "",
		"a.txt":       "",
		"a.woff2":     "",
		"noextension": "",
	}
	for name, want := range tests {
		if got := minifyMediaType(name); got != want {
			t.Errorf("minifyMediaType(%q) = %q，期望 %q", name, got, want)
		}
	}
}

// TestMinifyKeepsSemantics 锁定压缩不改变语义的几条硬保证。
func TestMinifyKeepsSemantics(t *testing.T) {
	minifier := newMinifier()

	t.Run("HTML 注释被删、结构与空白语义保留", func(t *testing.T) {
		source := "<!-- 注释 --><div>\n  <p>第一段</p>\n  <span>a</span> <span>b</span>\n</div>"
		out, err := minifyBytes(minifier, mediaTypeHTML, []byte(source))
		if err != nil {
			t.Fatalf("压缩 HTML: %v", err)
		}
		minified := string(out)
		if strings.Contains(minified, "注释") {
			t.Fatalf("普通注释应被删除: %s", minified)
		}
		if len(minified) >= len(source) {
			t.Fatalf("压缩后不应更长：%d -> %d", len(source), len(minified))
		}
		// 内联元素之间的空格必须保留（丢了就会把「a b」变成「ab」）。
		if !strings.Contains(minified, "<span>a</span> <span>b</span>") {
			t.Fatalf("内联元素之间的空格被压掉了: %s", minified)
		}
		if text := htmlText(t, minified); !strings.Contains(text, "第一段") {
			t.Fatalf("正文内容丢失: %s", text)
		}
	})

	t.Run("pre 与 textarea 内的空白逐字节保留", func(t *testing.T) {
		source := "<pre>\n  func main() {\n      println(\"hi\")\n  }\n</pre><textarea>  两个  空格  </textarea>"
		out, err := minifyBytes(minifier, mediaTypeHTML, []byte(source))
		if err != nil {
			t.Fatalf("压缩 HTML: %v", err)
		}
		for _, want := range []string{
			"<pre>\n  func main() {\n      println(\"hi\")\n  }\n</pre>",
			"<textarea>  两个  空格  </textarea>",
		} {
			if !strings.Contains(string(out), want) {
				t.Fatalf("预格式化内容被改动，缺少 %q：\n%s", want, out)
			}
		}
	})

	t.Run("CSS 压缩但保留 /*! 许可声明 */", func(t *testing.T) {
		source := "/*! 版权 (c) 2026 */\n/* 普通注释 */\n.a {\n  color : red ;\n  margin : 0 auto ;\n}\n"
		out, err := minifyBytes(minifier, mediaTypeCSS, []byte(source))
		if err != nil {
			t.Fatalf("压缩 CSS: %v", err)
		}
		minified := string(out)
		// 许可声明的文字必须留下（注释内部空白会被压掉，这是压缩的正常行为）。
		if !strings.Contains(minified, "/*!") || !strings.Contains(minified, "版权 (c) 2026") {
			t.Fatalf("许可声明应保留: %s", minified)
		}
		if strings.Contains(minified, "普通注释") {
			t.Fatalf("普通注释应删除: %s", minified)
		}
		if !strings.Contains(minified, ".a{color:red;margin:0 auto}") {
			t.Fatalf("CSS 压缩结果不符合预期: %s", minified)
		}
	})

	t.Run("JS 只去空白与注释，不改变量名", func(t *testing.T) {
		source := "/*! 版权 (c) 2026 */\n// 注释\nfunction add(first, second) {\n  return first + second;\n}\n"
		out, err := minifyBytes(minifier, mediaTypeJS, []byte(source))
		if err != nil {
			t.Fatalf("压缩 JS: %v", err)
		}
		minified := string(out)
		// KeepVarNames：函数参数名必须原样保留（minify 不是 uglify）。
		if !strings.Contains(minified, "function add(first,second)") {
			t.Fatalf("JS 变量名不应被改动: %s", minified)
		}
		if !strings.Contains(minified, "/*!") || !strings.Contains(minified, "版权 (c) 2026") {
			t.Fatalf("许可声明应保留: %s", minified)
		}
		if strings.Contains(minified, "// 注释") {
			t.Fatalf("普通注释应删除: %s", minified)
		}
	})

	t.Run("压缩器或类型为空时原样返回", func(t *testing.T) {
		body := []byte("原样")
		if out, err := minifyBytes(nil, mediaTypeHTML, body); err != nil || string(out) != "原样" {
			t.Fatalf("nil 压缩器应原样返回: %q err=%v", out, err)
		}
		if out, err := minifyBytes(minifier, "", body); err != nil || string(out) != "原样" {
			t.Fatalf("空类型应原样返回: %q err=%v", out, err)
		}
	})
}

// TestGenerateMinifiesOutput 覆盖 gen 的压缩：页面与可压缩资源被压，其他文件逐字节不变。
//
// 注意：模板里的 HTML 注释会被 html/template 去掉（与压缩无关），因此页面的「压缩特征」
// 用换行与缩进判断；HTML 注释的删除改用页面根外被当资源拷贝的 .html 来验证。
func TestGenerateMinifiesOutput(t *testing.T) {
	files := map[string]string{
		// 用三根布局：页面根是 pages/，因此 static/partial.html 属于「页面根外的 html 资源」，
		// 不参与模板渲染，注释只有压缩才会消失。
		"pages/index.html":    "<html>\n  <body>\n    <pre>  保留   </pre>\n    <p>{{.site.title}}</p>\n  </body>\n</html>\n",
		"templates/data.json": `{"site":{"title":"压缩站点"}}`,
		"static/site.css":     "/* 注释 */\n.a {\n  color : red ;\n}\n",
		"static/site.js":      "// 注释\nfunction add(first, second) {\n  return first + second;\n}\n",
		"static/data.json":    "{\n  \"a\": 1,\n  \"b\": [1, 2]\n}\n",
		"static/logo.svg":     "<svg xmlns=\"http://www.w3.org/2000/svg\">\n  <rect width=\"10\" height=\"10\" />\n</svg>\n",
		"static/partial.html": "<!-- 片段注释 --><section>\n  <p>片段</p>\n</section>\n",
		"static/raw.txt":      "  原样文本  \n",
		"static/img/logo.png": "\x89PNG\r\n\x1a\n二进制内容",
	}
	dirMinified := writeSite(t, files)
	dirPlain := writeSite(t, files)

	siteOptions := func(root string) SiteOptions {
		return SiteOptions{ProjectRoot: root, Dir: "pages", TemplateDir: "templates", AssetsRoot: "."}
	}

	minifiedSite, err := LoadSite(siteOptions(dirMinified))
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	outMinified := filepath.Join(dirMinified, "dist")
	if _, err := Generate(minifiedSite, GenOptions{Out: outMinified, Clean: true, Minify: true}); err != nil {
		t.Fatalf("Generate(Minify: true): %v", err)
	}

	plainSite, err := LoadSite(siteOptions(dirPlain))
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	outPlain := filepath.Join(dirPlain, "dist")
	if _, err := Generate(plainSite, GenOptions{Out: outPlain, Clean: true, Minify: false}); err != nil {
		t.Fatalf("Generate(Minify: false): %v", err)
	}

	// 页面：换行与缩进被压掉、长度变短，但预格式化内容与正文都还在。
	minifiedPage := readFile(t, filepath.Join(outMinified, "index.html"))
	plainPage := readFile(t, filepath.Join(outPlain, "index.html"))
	if strings.Contains(minifiedPage, "\n") {
		t.Fatalf("压缩后的页面不应有换行: %q", minifiedPage)
	}
	if !strings.Contains(plainPage, "\n") {
		t.Fatalf("未压缩的页面应保留模板原样的换行: %q", plainPage)
	}
	if len(minifiedPage) >= len(plainPage) {
		t.Fatalf("压缩后页面不应更长：%d -> %d", len(plainPage), len(minifiedPage))
	}
	if !strings.Contains(minifiedPage, "<pre>  保留   </pre>") {
		t.Fatalf("pre 内容被改动: %s", minifiedPage)
	}
	for _, want := range []string{"压缩站点", "保留"} {
		if !strings.Contains(minifiedPage, want) {
			t.Fatalf("页面内容丢失 %q：%s", want, minifiedPage)
		}
	}
	if minifiedPage == plainPage {
		t.Fatal("开启与关闭压缩的页面不应完全相同（否则压缩没生效）")
	}

	// 可压缩资源：变短。
	for _, rel := range []string{"static/site.css", "static/site.js", "static/data.json", "static/logo.svg", "static/partial.html"} {
		minifiedAsset := readFile(t, filepath.Join(outMinified, filepath.FromSlash(rel)))
		plainAsset := readFile(t, filepath.Join(outPlain, filepath.FromSlash(rel)))
		if len(minifiedAsset) >= len(plainAsset) {
			t.Errorf("%s 压缩后不应更长：%d -> %d", rel, len(plainAsset), len(minifiedAsset))
		}
	}
	if got := readFile(t, filepath.Join(outMinified, "static", "site.js")); !strings.Contains(got, "function add(first,second)") {
		t.Errorf("JS 变量名不应被改动: %s", got)
	}
	// 页面根外的 .html 资源：注释是压缩删掉的（模板渲染不会碰它）。
	if got := readFile(t, filepath.Join(outMinified, "static", "partial.html")); strings.Contains(got, "片段注释") {
		t.Errorf("HTML 资源里的注释应被压缩删除: %s", got)
	}
	if got := readFile(t, filepath.Join(outPlain, "static", "partial.html")); !strings.Contains(got, "片段注释") {
		t.Errorf("关闭压缩时 HTML 资源的注释应保留: %s", got)
	}

	// 不可压缩资源：逐字节一致。
	for _, rel := range []string{"static/raw.txt", "static/img/logo.png"} {
		minifiedAsset := readFile(t, filepath.Join(outMinified, filepath.FromSlash(rel)))
		plainAsset := readFile(t, filepath.Join(outPlain, filepath.FromSlash(rel)))
		if minifiedAsset != plainAsset {
			t.Errorf("%s 不应被压缩：%q vs %q", rel, minifiedAsset, plainAsset)
		}
	}
}

// htmlText 抽取 HTML 里的文本内容并折叠空白，用于比较压缩前后语义是否一致。
func htmlText(t *testing.T, source string) string {
	t.Helper()
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatalf("解析 HTML: %v", err)
	}
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return strings.Join(strings.Fields(builder.String()), " ")
}

// TestMinifyRejectsUnwritableTarget 覆盖压缩写入失败时的错误路径（目标被目录占用）。
func TestMinifyRejectsUnwritableTarget(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":      "<html>INDEX</html>",
		"static/site.css": ".a{color:red}",
	})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	out := filepath.Join(dir, "dist")
	if err := os.MkdirAll(filepath.Join(out, "static", "site.css"), 0o755); err != nil {
		t.Fatalf("预置同名目录: %v", err)
	}
	if _, err := Generate(site, GenOptions{Out: out, Clean: false, Minify: true}); err == nil {
		t.Fatal("目标被目录占用时应当报错")
	}
}
