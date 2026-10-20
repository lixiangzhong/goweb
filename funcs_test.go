package main

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// renderTemplate 用站点渲染一段模板源码，返回输出（便于逐函数验证）。
func renderTemplate(t *testing.T, site *Site, source string) (string, error) {
	t.Helper()
	ctx := &renderContext{site: site, mode: site.Mode(), buildTime: site.BuildTime()}
	tmpl, err := template.New("probe").Funcs(templateFuncs(ctx)).Parse(source)
	if err != nil {
		t.Fatalf("解析模板失败: %v", err)
	}
	var builder strings.Builder
	if err := tmpl.Execute(&builder, site.data); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func TestTextFuncs(t *testing.T) {
	site := loadTestSite(t, map[string]string{"index.html": "<html>INDEX</html>"})

	tests := []struct {
		source string
		want   string
	}{
		{source: `{{upper "abc"}}`, want: "ABC"},
		{source: `{{lower "ABC"}}`, want: "abc"},
		{source: `{{trimPrefix "#" "#anchor"}}`, want: "anchor"},
		{source: `{{trimSuffix ".html" "about.html"}}`, want: "about"},
		{source: `{{replace " " "-" "a b c"}}`, want: "a-b-c"},
		{source: `{{join "," (list "a" "b")}}`, want: "a,b"},
		{source: `{{join "/" (list 1 2)}}`, want: "1/2"},
		{source: `{{charCount "你好世界"}}`, want: "4"},
		// 内置 len/slice 是字节语义，truncate/charCount 是按字符的替代品。
		{source: `{{len "你好世界"}}`, want: "12"},
		{source: `{{truncate 3 "你好世界"}}`, want: "你好世…"},
		{source: `{{truncate 8 "你好世界"}}`, want: "你好世界"},
		{source: `{{truncate 0 "abc"}}`, want: ""},
		{source: `{{"你好世界" | truncate 2}}`, want: "你好…"},
	}

	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			got, err := renderTemplate(t, site, test.source)
			if err != nil {
				t.Fatalf("渲染 %s 出错: %v", test.source, err)
			}
			if got != test.want {
				t.Fatalf("%s = %q，期望 %q", test.source, got, test.want)
			}
		})
	}
}

func TestDefaultFunc(t *testing.T) {
	site := loadTestSite(t, map[string]string{"index.html": "<html>INDEX</html>"})

	tests := []struct {
		source string
		want   string
	}{
		{source: `{{default "兜底" .missing}}`, want: "兜底"},
		{source: `{{default "兜底" ""}}`, want: "兜底"},
		{source: `{{default "兜底" 0}}`, want: "兜底"},
		{source: `{{default "兜底" false}}`, want: "兜底"},
		{source: `{{default "兜底" (list)}}`, want: "兜底"},
		{source: `{{default "兜底" "值"}}`, want: "值"},
		{source: `{{default "兜底" 42}}`, want: "42"},
		{source: `{{.missing | default "管道兜底"}}`, want: "管道兜底"},
	}

	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			got, err := renderTemplate(t, site, test.source)
			if err != nil {
				t.Fatalf("渲染 %s 出错: %v", test.source, err)
			}
			if got != test.want {
				t.Fatalf("%s = %q，期望 %q", test.source, got, test.want)
			}
		})
	}
}

func TestFailFuncStopsRendering(t *testing.T) {
	site := loadTestSite(t, map[string]string{"index.html": "<html>INDEX</html>"})

	_, err := renderTemplate(t, site, `{{if not .site.title}}{{fail "data.json 缺少 site.title"}}{{end}}`)
	if err == nil {
		t.Fatal("fail 应当让渲染失败")
	}
	if !strings.Contains(err.Error(), "缺少 site.title") {
		t.Fatalf("错误信息应带上 fail 的消息: %v", err)
	}
}

func TestDictAndListFuncs(t *testing.T) {
	site := loadTestSite(t, map[string]string{"index.html": "<html>INDEX</html>"})

	got, err := renderTemplate(t, site, `{{$d := dict "title" "标题" "n" 3}}{{$d.title}}/{{$d.n}}`)
	if err != nil {
		t.Fatalf("渲染出错: %v", err)
	}
	if got != "标题/3" {
		t.Fatalf("dict 渲染 = %q，期望 %q", got, "标题/3")
	}

	if _, err := renderTemplate(t, site, `{{dict "only-key"}}`); err == nil {
		t.Fatal("dict 参数个数为奇数时应当报错")
	}
	if _, err := renderTemplate(t, site, `{{dict 1 "value"}}`); err == nil {
		t.Fatal("dict 的键不是字符串时应当报错")
	}
	if _, err := renderTemplate(t, site, `{{join "," "not-a-slice"}}`); err == nil {
		t.Fatal("join 收到非切片时应当报错")
	}

	// 带格式化参数的 fail：{{fail "缺少 %s" "site.title"}}
	if _, err := renderTemplate(t, site, `{{fail "缺少 %s" "site.title"}}`); err == nil ||
		!strings.Contains(err.Error(), "缺少 site.title") {
		t.Fatalf("fail 应支持格式化参数，实际: %v", err)
	}
}

// TestIsEmptyValue 覆盖 default 判定空值时的各类取值。
func TestIsEmptyValue(t *testing.T) {
	type sample struct{ A int }
	var nilPointer *sample

	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{name: "nil", value: nil, want: true},
		{name: "空字符串", value: "", want: true},
		{name: "非空字符串", value: "x", want: false},
		{name: "零值整数", value: 0, want: true},
		{name: "非零整数", value: -1, want: false},
		{name: "零值浮点", value: 0.0, want: true},
		{name: "非零浮点", value: 1.5, want: false},
		{name: "零值无符号", value: uint(0), want: true},
		{name: "false", value: false, want: true},
		{name: "true", value: true, want: false},
		{name: "空切片", value: []string{}, want: true},
		{name: "非空切片", value: []string{"a"}, want: false},
		{name: "空映射", value: map[string]any{}, want: true},
		{name: "非空映射", value: map[string]any{"a": 1}, want: false},
		{name: "nil 指针", value: nilPointer, want: true},
		{name: "结构体不做零值判断", value: sample{}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isEmptyValue(test.value); got != test.want {
				t.Fatalf("isEmptyValue(%#v) = %v，期望 %v", test.value, got, test.want)
			}
		})
	}
}

func TestModeFuncs(t *testing.T) {
	source := `{{if prod}}PROD{{else}}DEV{{end}}`

	devSite := loadTestSite(t, map[string]string{"index.html": source})
	if got, err := devSite.Render("index"); err != nil || string(got) != "DEV" {
		t.Fatalf("默认（dev）渲染 = %q（err=%v），期望 DEV", got, err)
	}

	prodDir := writeSite(t, map[string]string{"index.html": source})
	prodSite, err := LoadSite(SiteOptions{Dir: prodDir, Mode: ModeProd})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	if got, err := prodSite.Render("index"); err != nil || string(got) != "PROD" {
		t.Fatalf("prod 模式渲染 = %q（err=%v），期望 PROD", got, err)
	}
}

func TestBuildTimeFuncs(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":          `<p>{{buildTime | date "2006-01-02"}}|{{year}}|{{date "2006" .since}}|{{.since | year}}</p>`,
		"templates/data.json": `{"since": "2020-03-04"}`,
	})

	t.Run("默认取站点内容里最新的修改时间", func(t *testing.T) {
		site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
		if err != nil {
			t.Fatalf("LoadSite: %v", err)
		}
		newest := newestModTime(t, dir)
		if got := site.BuildTime(); !got.Equal(newest.Truncate(time.Second)) {
			t.Fatalf("构建时间 = %s，期望站点最新修改时间 %s", got.Format(time.RFC3339), newest.Format(time.RFC3339))
		}

		body, err := site.Render("index")
		if err != nil {
			t.Fatalf("渲染: %v", err)
		}
		want := newest.Format("2006-01-02") + "|" + newest.Format("2006") + "|2020|2020"
		if string(body) != "<p>"+want+"</p>" {
			t.Fatalf("渲染 = %s，期望 <p>%s</p>", body, want)
		}
	})

	t.Run("--build-time 可显式固定", func(t *testing.T) {
		fixed := time.Date(2026, 3, 5, 10, 30, 0, 0, time.UTC)
		site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates", BuildTime: fixed})
		if err != nil {
			t.Fatalf("LoadSite: %v", err)
		}
		if got := site.BuildTime(); !got.Equal(fixed) {
			t.Fatalf("构建时间 = %s，期望 %s", got, fixed)
		}
		body, err := site.Render("index")
		if err != nil {
			t.Fatalf("渲染: %v", err)
		}
		if !strings.Contains(string(body), "2026-03-05|2026|") {
			t.Fatalf("渲染未使用固定构建时间: %s", body)
		}
	})

	t.Run("同样的输入产出同样的构建时间", func(t *testing.T) {
		first, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
		if err != nil {
			t.Fatalf("LoadSite: %v", err)
		}
		second, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
		if err != nil {
			t.Fatalf("LoadSite: %v", err)
		}
		if !first.BuildTime().Equal(second.BuildTime()) {
			t.Fatalf("两次加载的构建时间不同：%s vs %s", first.BuildTime(), second.BuildTime())
		}
	})

	t.Run("日期参数非法时报错", func(t *testing.T) {
		site := loadTestSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
		if _, err := renderTemplate(t, site, `{{date "2006" "不是时间"}}`); err == nil {
			t.Fatal("无法解析的时间字符串应当报错")
		}
		if _, err := renderTemplate(t, site, `{{date "2006" true}}`); err == nil {
			t.Fatal("不支持的时间类型应当报错")
		}
	})
}

// newestModTime 返回站点内文件最新的修改时间（截断到秒，与实现保持一致）。
func newestModTime(t *testing.T, root string) time.Time {
	t.Helper()
	var newest time.Time
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s: %v", root, err)
	}
	return newest.Truncate(time.Second)
}

func TestAssetFunc(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":          `<link href="{{asset "/static/css/site.css"}}">`,
		"static/css/site.css": "body{color:red}",
	})

	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	sum := sha256.Sum256([]byte("body{color:red}"))
	wantHash := hex.EncodeToString(sum[:])[:assetHashLength]

	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	want := `<link href="/static/css/site.css?v=` + wantHash + `">`
	if string(body) != want {
		t.Fatalf("渲染 = %s，期望 %s", body, want)
	}

	t.Run("内容变化后哈希变化", func(t *testing.T) {
		writeFile(t, filepath.Join(dir, "static", "css", "site.css"), "body{color:blue}")
		if err := site.Reload(); err != nil {
			t.Fatalf("Reload: %v", err)
		}
		body, err := site.Render("index")
		if err != nil {
			t.Fatalf("渲染: %v", err)
		}
		if strings.Contains(string(body), wantHash) {
			t.Fatalf("内容变了哈希却不变: %s", body)
		}
	})

	t.Run("拒绝不该接受的路径", func(t *testing.T) {
		cases := map[string]string{
			"不存在的资源":    `{{asset "/static/nope.css"}}`,
			"页面":        `{{asset "/index.html"}}`,
			"局部模板":      `{{asset "/templates/header.html"}}`,
			"带查询串":      `{{asset "/static/css/site.css?v=1"}}`,
			"路径越界":      `{{asset "/../secret.txt"}}`,
			"空路径":       `{{asset ""}}`,
			"忽略项(点号开头)": `{{asset "/.hidden/a.css"}}`,
		}
		for name, source := range cases {
			t.Run(name, func(t *testing.T) {
				if _, err := renderTemplate(t, site, source); err == nil {
					t.Fatalf("%s 应当报错：%s", name, source)
				}
			})
		}
	})
}
