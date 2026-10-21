package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// 端到端测试在真实进程与真实 HTTP 上验证完整链路：编译出真正的 goweb 二进制，
// 像用户一样执行 gen/serve，再请求 URL。
// 需要编译与端口，因此 go test -short 会跳过。
//
//	go test -run TestE2E -v ./...

const (
	// e2eStartupTimeout 是等待服务端打印监听地址的上限。
	e2eStartupTimeout = 20 * time.Second
	// e2eReloadTimeout 是等待热重载生效的上限。
	e2eReloadTimeout = 5 * time.Second
	// e2eHTTPTimeout 是单次请求超时。
	e2eHTTPTimeout = 5 * time.Second
)

// e2eAddrRe 匹配服务端启动日志里的真实监听地址（含 IPv6 通配写法如 [::]:8081）。
var e2eAddrRe = regexp.MustCompile(`addr=(\S+)`)

// buildGoweb 编译出真实二进制，供 e2e 像用户一样调用。
func buildGoweb(t *testing.T) string {
	t.Helper()
	name := "goweb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 goweb 失败: %v\n%s", err, out)
	}
	return binary
}

func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e 需要编译二进制并使用端口，-short 下跳过")
	}

	binary := buildGoweb(t)
	siteDir := writeE2ESite(t)

	t.Run("gen 产出与源码同路径的扁平产物", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "dist")
		code, _, stderr := runBinaryIn(t, siteDir, binary, "gen", "--out", out)
		if code != 0 {
			t.Fatalf("gen 退出码 = %d，期望 0（stderr: %s）", code, stderr)
		}
		if !strings.Contains(stderr, "静态站已生成") {
			t.Fatalf("缺少生成日志: %s", stderr)
		}

		want := []string{
			"about.html",
			"help/74.html",
			"help/index.html",
			"index.html",
			"robots.txt",
			"static/css/site.css",
			"static/js/site.js",
		}
		if got := listFiles(t, out); !equalStrings(got, want) {
			t.Fatalf("产物清单 = %v，期望 %v", got, want)
		}

		about := readFile(t, filepath.Join(out, "about.html"))
		if !strings.Contains(about, "E2E-SITE") || !strings.Contains(about, "ABOUT") {
			t.Fatalf("产物未渲染模板或数据: %s", about)
		}

		// gen 默认是 prod 模式；普通路径的 /static/css/site.css 应被自动补上真实内容哈希。
		index := readFile(t, filepath.Join(out, "index.html"))
		if !strings.Contains(index, ">PROD<") {
			t.Fatalf("gen 产物应处于 prod 模式: %s", index)
		}
		cssSum := sha256.Sum256([]byte(".a{color:red}"))
		wantAsset := "/static/css/site.css?v=" + hex.EncodeToString(cssSum[:])[:assetHashLength]
		if !strings.Contains(index, wantAsset) {
			t.Fatalf("页面里的普通资源引用应被自动补版本号 %s，实际: %s", wantAsset, index)
		}
		if !strings.Contains(index, ">E2E-SITE<") {
			t.Fatalf("局部模板里的 default 兜底未生效: %s", index)
		}
		// asset 函数是显式出口，产物里同样生效。
		if about := readFile(t, filepath.Join(out, "about.html")); !strings.Contains(about, wantAsset) {
			t.Fatalf("asset 函数应产出带哈希的 URL %s，实际: %s", wantAsset, about)
		}
		// 热重载脚本只注入 serve 的响应，产物必须干净。
		for _, name := range []string{"index.html", "about.html", "help/index.html"} {
			if body := readFile(t, filepath.Join(out, filepath.FromSlash(name))); strings.Contains(body, liveReloadMarker) {
				t.Fatalf("产物 %s 不应包含热重载脚本: %s", name, body)
			}
		}
	})

	t.Run("gen 重复执行结果一致", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "dist")
		if code, _, stderr := runBinaryIn(t, siteDir, binary, "gen", "--out", out); code != 0 {
			t.Fatalf("首次 gen 失败: %s", stderr)
		}
		first := readFile(t, filepath.Join(out, "index.html"))

		if code, _, stderr := runBinaryIn(t, siteDir, binary, "gen", "--out", out); code != 0 {
			t.Fatalf("二次 gen 失败: %s", stderr)
		}
		if second := readFile(t, filepath.Join(out, "index.html")); first != second {
			t.Fatalf("重复生成结果不一致:\n%s\n---\n%s", first, second)
		}
	})

	t.Run("gen --clean=false 保留遗留文件", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "dist")
		if code, _, stderr := runBinaryIn(t, siteDir, binary, "gen", "--out", out); code != 0 {
			t.Fatalf("gen 失败: %s", stderr)
		}
		stale := filepath.Join(out, "stale.html")
		if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
			t.Fatalf("写入遗留文件: %v", err)
		}

		if code, _, stderr := runBinaryIn(t, siteDir, binary, "gen", "--out", out, "--clean=false"); code != 0 {
			t.Fatalf("--clean=false 生成失败: %s", stderr)
		}
		if _, err := os.Stat(stale); err != nil {
			t.Fatalf("--clean=false 不应删除遗留文件: %v", err)
		}

		if code, _, stderr := runBinaryIn(t, siteDir, binary, "gen", "--out", out); code != 0 {
			t.Fatalf("gen 失败: %s", stderr)
		}
		if _, err := os.Stat(stale); err == nil {
			t.Fatal("默认 --clean 应清空遗留文件")
		}
	})

	t.Run("gen 护栏拒绝危险产物目录", func(t *testing.T) {
		// 在站点目录里跑，--out . 即把项目根当产物目录，护栏应当拦下。
		code, _, stderr := runBinaryIn(t, siteDir, binary, "gen", "--out", ".")
		if code == 0 {
			t.Fatal("把页面目录当产物目录应当失败")
		}
		if !strings.Contains(stderr, "产物目录") {
			t.Fatalf("错误信息缺少产物目录提示: %s", stderr)
		}
		if _, err := os.Stat(filepath.Join(siteDir, "index.html")); err != nil {
			t.Fatalf("源码被破坏: %v", err)
		}
	})

	t.Run("build 别名与 gen 等价", func(t *testing.T) {
		siteDir := writeE2ESite(t)
		out := filepath.Join(t.TempDir(), "built")
		code, _, stderr := runBinaryIn(t, siteDir, binary, "build", "--out", out)
		if code != 0 {
			t.Fatalf("build 退出码 = %d，期望 0（stderr: %s）", code, stderr)
		}
		if !strings.Contains(stderr, "静态站已生成") || !strings.Contains(stderr, "mode=prod") {
			t.Fatalf("build 应与 gen 一样产出并默认 prod 模式: %s", stderr)
		}
		want := []string{"about.html", "help/74.html", "help/index.html", "index.html", "robots.txt", "static/css/site.css", "static/js/site.js"}
		if got := listFiles(t, out); !equalStrings(got, want) {
			t.Fatalf("build 产物清单 = %v，期望 %v", got, want)
		}
		// 别名要出现在帮助里，用户才知道它存在。
		if code, stdout, _ := runBinary(t, binary, "gen", "--help"); code != 0 || !strings.Contains(stdout, "build") {
			t.Fatalf("gen --help 应提示别名 build: code=%d stdout=%q", code, stdout)
		}
	})

	t.Run("build 默认压缩页面与资源，serve 保持可读", func(t *testing.T) {
		// 用三根布局：页面在 pages/，static/partial.html 因此是「页面根外的 html 资源」，
		// 不参与模板渲染，可以用它验证 HTML 注释是压缩删掉的。
		// 注意：模板里的 HTML 注释本来就会被 html/template 去掉，所以页面的压缩特征用换行判断。
		siteDir := writeSite(t, map[string]string{
			"pages/index.html":    "<html>\n  <body>\n    <pre>  保留  </pre>\n    <p>{{.site.title}}</p>\n  </body>\n</html>\n",
			"templates/data.json": `{"site":{"title":"E2E-MINIFY"}}`,
			"static/site.css":     "/* 注释 */\n.a {\n  color : red ;\n}\n",
			"static/site.js":      "// 注释\nfunction add(first, second) {\n  return first + second;\n}\n",
			"static/partial.html": "<!-- 片段注释 --><section>\n  <p>片段</p>\n</section>\n",
			"static/raw.txt":      "  原样文本  \n",
		})
		out := filepath.Join(t.TempDir(), "dist")
		code, _, stderr := runBinaryIn(t, siteDir, binary, "build", "--dir", "pages", "--out", out)
		if code != 0 {
			t.Fatalf("build 退出码 = %d，期望 0（stderr: %s）", code, stderr)
		}
		page := readFile(t, filepath.Join(out, "index.html"))
		if strings.Contains(page, "\n") {
			t.Fatalf("产物页面应被压缩（不应有换行）: %q", page)
		}
		if !strings.Contains(page, "<pre>  保留  </pre>") || !strings.Contains(page, "E2E-MINIFY") {
			t.Fatalf("压缩不应改动预格式化内容或丢内容: %s", page)
		}
		css := readFile(t, filepath.Join(out, "static", "site.css"))
		if strings.Contains(css, "注释") || !strings.Contains(css, ".a{color:red}") {
			t.Fatalf("CSS 应被压缩: %s", css)
		}
		// JS 只去空白与注释，变量名保持原样。
		if js := readFile(t, filepath.Join(out, "static", "site.js")); !strings.Contains(js, "function add(first,second)") {
			t.Fatalf("JS 变量名不应被改动: %s", js)
		}
		if partial := readFile(t, filepath.Join(out, "static", "partial.html")); strings.Contains(partial, "片段注释") {
			t.Fatalf("HTML 资源里的注释应被压缩删除: %s", partial)
		}
		if raw := readFile(t, filepath.Join(out, "static", "raw.txt")); raw != "  原样文本  \n" {
			t.Fatalf("不可压缩的文件应逐字节保留: %q", raw)
		}

		// serve 不压缩：开发时看到的是可读的原始渲染结果。
		server := startServe(t, binary, siteDir, "--dir", "pages")
		client := &http.Client{Timeout: e2eHTTPTimeout}
		status, served := e2eGet(t, client, server.url+"/")
		if status != 200 || !strings.Contains(served, "\n") || !strings.Contains(served, "E2E-MINIFY") {
			t.Fatalf("serve 应保持可读（不压缩）: status=%d body=%q", status, served)
		}

		// --minify=false 的产物与 serve 渲染结果一样保留换行。
		plain := filepath.Join(t.TempDir(), "plain")
		if code, _, stderr := runBinaryIn(t, siteDir, binary, "build", "--dir", "pages", "--out", plain, "--minify=false"); code != 0 {
			t.Fatalf("build --minify=false 退出码 = %d（stderr: %s）", code, stderr)
		}
		if plainPage := readFile(t, filepath.Join(plain, "index.html")); !strings.Contains(plainPage, "\n") {
			t.Fatalf("--minify=false 时不应压缩（应保留换行）: %q", plainPage)
		}
	})

	t.Run("默认端口被占用时自动递增端口", func(t *testing.T) {
		// 占住通配 :8080（实测：只占 127.0.0.1:8080 时，通配绑定在 macOS 上仍会成功）；
		// 占不上（本机 8080 已被别人用）就跳过，递增逻辑本身由 listen_test.go 覆盖。
		blocker, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Skipf("本机 8080 不可用（%v），跳过", err)
		}
		defer blocker.Close()

		server := startServeDefaultAddr(t, binary, writeE2ESite(t))
		logs := server.logs()
		if !strings.Contains(logs, "端口被占用") {
			t.Fatalf("应记录端口被占用并改用其它端口: %s", logs)
		}

		addr := strings.TrimPrefix(server.url, "http://")
		if portOf(t, addr) == 8080 {
			t.Skip("本平台允许同时绑定通配与 127.0.0.1 的同一端口，跳过断言")
		}
		// 用测试过的地址推导逻辑把通配地址变成可访问的 URL，再确认服务真的起来了。
		url := strings.TrimSuffix(browserURL(fakeAddr(addr)), "/")
		client := &http.Client{Timeout: e2eHTTPTimeout}
		if status, body := e2eGet(t, client, url+"/about.html"); status != 200 || !strings.Contains(body, "E2E-SITE") {
			t.Fatalf("递增端口后的服务应可访问: status=%d body=%s url=%s", status, body, url)
		}
	})

	t.Run("日志写在站点目录内不会自我触发重载", func(t *testing.T) {
		siteDir := writeE2ESite(t)
		// 复现用户最容易踩的写法：`goweb serve > site/build.log`。
		// 日志文件在站点里，它自己的每次写入都会产生文件事件——修好之前会形成重载死循环
		// （实测 3 秒 19 次重载），现在应当一次都不触发。
		logPath := filepath.Join(siteDir, "build.log")
		logFile, err := os.Create(logPath)
		if err != nil {
			t.Fatalf("创建日志文件: %v", err)
		}
		defer logFile.Close()

		cmd := exec.Command(binary, "serve", "--open=false", "--addr", "127.0.0.1:0")
		cmd.Dir = siteDir
		cmd.Stdout, cmd.Stderr = logFile, logFile
		if err := cmd.Start(); err != nil {
			t.Fatalf("启动 serve: %v", err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		})

		// 等服务启动（地址写在同一个日志文件里），再观察一段时间是否有重载。
		waitFor(t, e2eStartupTimeout, func() bool {
			body, err := os.ReadFile(logPath)
			return err == nil && e2eAddrRe.Match(body)
		}, "应把启动日志写进站点内的日志文件")

		time.Sleep(2 * time.Second)
		body, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("读取日志: %v", err)
		}
		if count := strings.Count(string(body), "站点已重载"); count != 0 {
			t.Fatalf("日志文件自身不应触发重载，实际触发 %d 次:\n%s", count, body)
		}
	})

	t.Run("dev 别名等价于 serve", func(t *testing.T) {
		siteDir := writeE2ESite(t)
		server := startServeProcess(t, binary, siteDir, nil,
			[]string{"dev", "--open=false", "--addr", "127.0.0.1:0"})
		client := &http.Client{Timeout: e2eHTTPTimeout}
		status, body := e2eGet(t, client, server.url+"/about.html")
		if status != 200 || !strings.Contains(body, "E2E-SITE") {
			t.Fatalf("dev 应像 serve 一样提供页面: status=%d body=%s", status, body)
		}
		if logs := server.logs(); !strings.Contains(logs, "mode=dev") {
			t.Fatalf("dev 应与 serve 一样默认 dev 模式: %s", logs)
		}
		// 别名要出现在帮助里，用户才知道它存在。
		if code, stdout, _ := runBinary(t, binary, "serve", "--help"); code != 0 || !strings.Contains(stdout, "dev") {
			t.Fatalf("serve --help 应提示别名 dev: code=%d stdout=%q", code, stdout)
		}
	})

	t.Run("init 生成配置且不覆盖已有文件", func(t *testing.T) {
		siteDir := writeE2ESite(t)

		code, stdout, stderr := runBinaryIn(t, siteDir, binary, "init")
		if code != 0 {
			t.Fatalf("init 退出码 = %d（stderr: %s）", code, stderr)
		}
		if !strings.Contains(stdout, ".goweb.yml") || !strings.Contains(stdout, "goweb dev") {
			t.Fatalf("init 应打印生成路径与下一步: %s", stdout)
		}
		configPath := filepath.Join(siteDir, ".goweb.yml")
		before := readFile(t, configPath)
		if !strings.Contains(before, "minify: true") {
			t.Fatalf("生成的配置应含默认值: %s", before)
		}

		// 第二次：报错退出（非 0），且不碰已有文件。
		if code, _, stderr := runBinaryIn(t, siteDir, binary, "init"); code == 0 || !strings.Contains(stderr, "已存在") {
			t.Fatalf("已存在时应报错退出: code=%d stderr=%s", code, stderr)
		}
		if after := readFile(t, configPath); after != before {
			t.Fatal("报错时不应改动已有配置")
		}

		// --force 后，用生成的配置跑通 build。
		if code, _, stderr := runBinaryIn(t, siteDir, binary, "init", "--force"); code != 0 {
			t.Fatalf("init --force 退出码 = %d（stderr: %s）", code, stderr)
		}
		out := filepath.Join(t.TempDir(), "dist")
		if code, _, stderr := runBinaryIn(t, siteDir, binary, "build", "--out", out); code != 0 {
			t.Fatalf("用生成的配置跑 build 失败: code=%d stderr=%s", code, stderr)
		}
		if index := readFile(t, filepath.Join(out, "index.html")); !strings.Contains(index, "E2E-SITE") {
			t.Fatalf("产物内容不正确: %s", index)
		}
	})

	t.Run("check 只校验不产出", func(t *testing.T) {
		siteDir := writeE2ESite(t)

		// 正常站点：通过，且不产生任何产物。
		code, _, stderr := runBinaryIn(t, siteDir, binary, "check")
		if code != 0 {
			t.Fatalf("check 退出码 = %d，期望 0（stderr: %s）", code, stderr)
		}
		if !strings.Contains(stderr, "检查通过") {
			t.Fatalf("check 应报告通过: %s", stderr)
		}
		if _, err := os.Stat(filepath.Join(siteDir, "dist")); !os.IsNotExist(err) {
			t.Fatalf("check 不应创建产物目录: %v", err)
		}

		// 故意加一个坏链接与一个渲染失败：问题要一次全部列出。
		writeFile(t, filepath.Join(siteDir, "index.html"),
			`<html><body><a href="/pages/about.html">坏链接</a><a href="/static">目录</a></body></html>`)
		writeFile(t, filepath.Join(siteDir, "help", "bad.html"), `<html><body>{{fail "故意失败"}}</body></html>`)
		code, _, stderr = runBinaryIn(t, siteDir, binary, "check")
		if code == 0 {
			t.Fatalf("有问题时 check 应非零退出: %s", stderr)
		}
		for _, want := range []string{"故意失败", "链接在产物里不存在：/pages/about.html", "链接在产物里不存在：/static（"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("check 输出缺少 %q:\n%s", want, stderr)
			}
		}

		// --links=false 时链接不再检查，渲染失败照旧报。
		code, _, stderr = runBinaryIn(t, siteDir, binary, "check", "--links=false")
		if code == 0 {
			t.Fatal("渲染失败时仍应非零退出")
		}
		if strings.Contains(stderr, "链接在产物里不存在") {
			t.Fatalf("--links=false 时不应检查链接: %s", stderr)
		}
		if !strings.Contains(stderr, "故意失败") {
			t.Fatalf("渲染失败仍应报出: %s", stderr)
		}
	})

	t.Run("gen 遇到坏数据直接失败", func(t *testing.T) {
		broken := writeE2ESite(t)
		writeFile(t, filepath.Join(broken, "templates", "data.json"), `{"site": }`)

		out := filepath.Join(t.TempDir(), "dist")
		code, _, stderr := runBinaryIn(t, broken, binary, "gen", "--out", out)
		if code == 0 {
			t.Fatal("坏 data.json 应当让 gen 失败")
		}
		if !strings.Contains(stderr, "数据文件") {
			t.Fatalf("错误信息缺少数据文件提示: %s", stderr)
		}
	})

	t.Run("版本 帮助 补全可用", func(t *testing.T) {
		if code, stdout, stderr := runBinary(t, binary, "--version"); code != 0 || !strings.Contains(stdout, version) {
			t.Fatalf("--version 异常: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if code, stdout, _ := runBinary(t, binary, "help"); code != 0 ||
			!strings.Contains(stdout, "serve") || !strings.Contains(stdout, "gen") {
			t.Fatalf("help 输出异常: code=%d stdout=%q", code, stdout)
		}
		if code, stdout, stderr := runBinary(t, binary, "completion", "bash"); code != 0 || stdout == "" {
			t.Fatalf("completion 异常: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("serve 的 URL 语义与热重载", func(t *testing.T) {
		server := startServe(t, binary, siteDir)
		client := &http.Client{Timeout: e2eHTTPTimeout}

		cases := []struct {
			path         string
			wantStatus   int
			wantContains string
		}{
			{path: "/", wantStatus: 200, wantContains: "INDEX"},
			{path: "/index.html", wantStatus: 200, wantContains: "INDEX"},
			{path: "/about.html", wantStatus: 200, wantContains: "ABOUT"},
			{path: "/about", wantStatus: 200, wantContains: "ABOUT"},
			{path: "/about/", wantStatus: 404},
			{path: "/about/index.html", wantStatus: 404},
			{path: "/help", wantStatus: 200, wantContains: "HELP-INDEX"},
			{path: "/help/", wantStatus: 200, wantContains: "HELP-INDEX"},
			{path: "/help/74", wantStatus: 200, wantContains: "HELP-74"},
			{path: "/help/74.html", wantStatus: 200, wantContains: "HELP-74"},
			{path: "//about//", wantStatus: 404},
			{path: "/a/../about", wantStatus: 200, wantContains: "ABOUT"},
			{path: "/about/.", wantStatus: 404},
			{path: "/static/css/site.css", wantStatus: 200, wantContains: "color:red"},
			{path: "/robots.txt", wantStatus: 200, wantContains: "User-agent"},
			{path: "/templates/header.html", wantStatus: 404},
			{path: "/templates/data.json", wantStatus: 404},
			{path: "/missing", wantStatus: 404},
		}
		for _, test := range cases {
			t.Run(test.path, func(t *testing.T) {
				status, body := e2eGet(t, client, server.url+test.path)
				if status != test.wantStatus {
					t.Fatalf("GET %s 状态码 = %d，期望 %d（body: %s）", test.path, status, test.wantStatus, body)
				}
				if test.wantContains != "" && !strings.Contains(body, test.wantContains) {
					t.Fatalf("GET %s 响应缺少 %q：%s", test.path, test.wantContains, body)
				}
			})
		}

		t.Run("页面带 no-store 缓存头", func(t *testing.T) {
			resp := e2eRequest(t, client, http.MethodGet, server.url+"/about")
			defer resp.Body.Close()
			if got := resp.Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q，期望 no-store", got)
			}
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Fatalf("Content-Type = %q，期望 text/html", got)
			}
		})

		t.Run("非 GET/HEAD 返回 405", func(t *testing.T) {
			resp := e2eRequest(t, client, http.MethodPost, server.url+"/about")
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("POST 状态码 = %d，期望 405", resp.StatusCode)
			}
			if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
				t.Fatalf("Allow = %q，期望 GET, HEAD", got)
			}
		})

		t.Run("HEAD 可用", func(t *testing.T) {
			resp := e2eRequest(t, client, http.MethodHead, server.url+"/about")
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("HEAD 状态码 = %d，期望 200", resp.StatusCode)
			}
		})

		t.Run("改动页面后自动重载", func(t *testing.T) {
			writeFile(t, filepath.Join(siteDir, "about.html"), "<html><body><h1>ABOUT-EDITED</h1></body></html>")
			waitForBody(t, client, server.url+"/about", "ABOUT-EDITED")
		})

		t.Run("改动 data.json 后自动重载", func(t *testing.T) {
			writeFile(t, filepath.Join(siteDir, "templates", "data.json"), `{"site":{"title":"E2E-EDITED"}}`)
			waitForBody(t, client, server.url+"/index.html", "E2E-EDITED")
		})

		t.Run("页面注入了热重载脚本", func(t *testing.T) {
			status, body := e2eGet(t, client, server.url+"/about")
			if status != 200 || !strings.Contains(body, liveReloadMarker) {
				t.Fatalf("页面未注入热重载脚本: status=%d body=%s", status, body)
			}
		})

		t.Run("serve 默认是 dev 模式且 asset 同样生效", func(t *testing.T) {
			status, body := e2eGet(t, client, server.url+"/index.html")
			if status != 200 {
				t.Fatalf("状态码 = %d，期望 200", status)
			}
			if !strings.Contains(body, ">DEV<") {
				t.Fatalf("serve 默认应为 dev 模式: %s", body)
			}
			cssSum := sha256.Sum256([]byte(".a{color:red}"))
			if want := "/static/css/site.css?v=" + hex.EncodeToString(cssSum[:])[:assetHashLength]; !strings.Contains(body, want) {
				t.Fatalf("serve 下的资源链接应带哈希 %s: %s", want, body)
			}
		})

		t.Run("改动文件后通过 SSE 推送刷新事件", func(t *testing.T) {
			// SSE 是长连接，这里不能用带 Timeout 的客户端。
			streamClient := &http.Client{}
			ctx, cancel := context.WithTimeout(context.Background(), e2eReloadTimeout+5*time.Second)
			defer cancel()

			request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.url+liveReloadPath, nil)
			if err != nil {
				t.Fatalf("构造 SSE 请求: %v", err)
			}
			response, err := streamClient.Do(request)
			if err != nil {
				t.Fatalf("连接 SSE: %v", err)
			}
			defer response.Body.Close()

			if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
				t.Fatalf("Content-Type = %q，期望 text/event-stream", got)
			}

			reader := bufio.NewReader(response.Body)
			if line, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(line, "retry:") {
				t.Fatalf("SSE 首包异常: %q err=%v", line, err)
			}

			// 订阅已建立，改文件触发重载。
			writeFile(t, filepath.Join(siteDir, "about.html"), "<html><body><h1>ABOUT-SSE</h1></body></html>")

			for {
				line, err := reader.ReadString('\n')
				if strings.TrimSpace(line) == "data: reload" {
					return
				}
				if err != nil {
					t.Fatalf("未收到 reload 事件: %v", err)
				}
			}
		})

		t.Run("坏模板不拖垮服务", func(t *testing.T) {
			// 记录当前页面内容，坏模板后应当仍然返回同一份内容（与前面的子测试无关）。
			status, expected := e2eGet(t, client, server.url+"/about")
			if status != 200 {
				t.Fatalf("改坏模板前 GET /about 状态码 = %d，期望 200", status)
			}

			writeFile(t, filepath.Join(siteDir, "about.html"), "<html>{{template ")
			// 等防抖窗口 + 一次失败的重载
			time.Sleep(500 * time.Millisecond)

			status, body := e2eGet(t, client, server.url+"/about")
			if status != 200 || body != expected {
				t.Fatalf("坏模板下应继续提供上一份可用页面，实际 status=%d body=%s（期望 %s）", status, body, expected)
			}
			if log := server.logs(); !strings.Contains(log, "站点重载失败") {
				t.Fatalf("日志中缺少重载失败记录: %s", log)
			}
		})
	})

	t.Run("关闭热重载后页面与产物一致", func(t *testing.T) {
		// 用独立站点，避免受前面子测试改坏模板的影响。
		// 同时验证 --mode prod：serve 也能预览生产模式的产物。
		server := startServe(t, binary, writeE2ESite(t), "--livereload=false", "--mode", "prod")
		client := &http.Client{Timeout: e2eHTTPTimeout}

		status, body := e2eGet(t, client, server.url+"/about")
		if status != 200 {
			t.Fatalf("GET /about 状态码 = %d，期望 200（body: %s）", status, body)
		}
		if strings.Contains(body, liveReloadMarker) {
			t.Fatalf("关闭热重载后不应注入脚本: %s", body)
		}
		if status, _ := e2eGet(t, client, server.url+liveReloadPath); status != http.StatusNotFound {
			t.Fatalf("关闭热重载后 SSE 端点应 404，实际 %d", status)
		}

		status, index := e2eGet(t, client, server.url+"/index.html")
		if status != 200 || !strings.Contains(index, ">PROD<") {
			t.Fatalf("--mode prod 下服务端应渲染 prod 分支: status=%d body=%s", status, index)
		}
	})

	t.Run("--fingerprint=false 关闭自动版本号", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "dist")
		code, _, stderr := runBinaryIn(t, writeE2ESite(t), binary, "gen", "--out", out, "--fingerprint=false")
		if code != 0 {
			t.Fatalf("gen 退出码 = %d，期望 0（stderr: %s）", code, stderr)
		}
		index := readFile(t, filepath.Join(out, "index.html"))
		if strings.Contains(index, "site.css?v=") {
			t.Fatalf("关闭自动改写后普通引用不应带版本号: %s", index)
		}
		// asset 是显式出口，即使关闭自动改写也照常生效。
		if about := readFile(t, filepath.Join(out, "about.html")); !strings.Contains(about, "site.css?v=") {
			t.Fatalf("asset 函数不受 --fingerprint 影响，应仍带版本号: %s", about)
		}
	})

	t.Run("--build-time 可固定构建时间", func(t *testing.T) {
		// 用独立站点：共享站点在服务端子测试里被改坏过模板。
		out := filepath.Join(t.TempDir(), "dist")
		code, _, stderr := runBinaryIn(t, writeE2ESite(t), binary, "gen", "--out", out,
			"--build-time", "2026-03-05T10:30:00Z")
		if code != 0 {
			t.Fatalf("gen 退出码 = %d，期望 0（stderr: %s）", code, stderr)
		}
		if index := readFile(t, filepath.Join(out, "index.html")); !strings.Contains(index, "2026-03-05") {
			t.Fatalf("产物未使用固定的构建时间: %s", index)
		}
	})

	t.Run("serve 自动打开浏览器（默认开、--open=false 关）", func(t *testing.T) {
		recorder := buildRecorder(t)
		siteDir := writeE2ESite(t)

		// 默认开启：serve 应调用我们指定的打开命令，并把真实监听地址传给它。
		opened := filepath.Join(t.TempDir(), "opened.txt")
		server := startServeEnv(t, binary, siteDir,
			[]string{openCommandEnv + "=" + recorder, "RECORDER_OUT=" + opened})
		waitFor(t, e2eStartupTimeout, func() bool {
			body, err := os.ReadFile(opened)
			return err == nil && strings.TrimSpace(string(body)) == server.url+"/"
		}, "应把真实地址 "+server.url+"/ 交给打开命令")
		if logs := server.logs(); !strings.Contains(logs, "open=true") {
			t.Fatalf("启动日志应标明 open=true: %s", logs)
		}

		// --open=false：不调用打开命令（等一会，确保不是慢半拍）。
		silent := filepath.Join(t.TempDir(), "silent.txt")
		startServeEnv(t, binary, siteDir,
			[]string{openCommandEnv + "=" + recorder, "RECORDER_OUT=" + silent}, "--open=false")
		time.Sleep(500 * time.Millisecond)
		if body, err := os.ReadFile(silent); err == nil {
			t.Fatalf("--open=false 时不应打开浏览器，实际记录了 %q", strings.TrimSpace(string(body)))
		}
	})

	t.Run("三根布局 pages/ + templates/ + static/", func(t *testing.T) {
		siteDir := writeSite(t, map[string]string{
			"pages/index.html":       `<html><body>{{template "header" .}}<link rel="stylesheet" href="/static/xxx.css"></body></html>`,
			"pages/about/index.html": `<html><body><h1>ABOUT</h1></body></html>`,
			"templates/header.html":  `{{define "header"}}<header>THREE-ROOTS-HEADER</header>{{end}}`,
			"templates/data.json":    `{"site":{"title":"THREE"}}`,
			"static/xxx.css":         "body{}",
			"static/skip.tmp":        "临时文件",
			".gitignore":             "*.tmp\n",
			".goweb.yml":             "dir: pages\n",
		})

		// gen：由配置文件指定页面根，产物落在项目根的 dist/。
		code, _, stderr := runBinaryIn(t, siteDir, binary, "gen")
		if code != 0 {
			t.Fatalf("gen 退出码 = %d，期望 0（stderr: %s）", code, stderr)
		}
		out := filepath.Join(siteDir, "dist")
		want := []string{"about/index.html", "index.html", "static/xxx.css"}
		if got := listFiles(t, out); !equalStrings(got, want) {
			t.Fatalf("产物清单 = %v，期望 %v（.gitignore 里的 *.tmp 不应进产物）", got, want)
		}
		index := readFile(t, filepath.Join(out, "index.html"))
		if !strings.Contains(index, "THREE-ROOTS-HEADER") {
			t.Fatalf("模板根未被加载: %s", index)
		}
		if !strings.Contains(index, "/static/xxx.css?v=") {
			t.Fatalf("资源引用应带内容哈希: %s", index)
		}

		// serve：同样由配置文件驱动；被忽略的文件本地仍可访问。
		server := startServe(t, binary, siteDir)
		client := &http.Client{Timeout: e2eHTTPTimeout}

		cases := []struct {
			path         string
			wantStatus   int
			wantContains string
		}{
			{path: "/", wantStatus: 200, wantContains: "THREE-ROOTS-HEADER"},
			{path: "/about/", wantStatus: 200, wantContains: "ABOUT"},
			{path: "/static/xxx.css", wantStatus: 200, wantContains: "body{}"},
			{path: "/static/skip.tmp", wantStatus: 200, wantContains: "临时文件"},
		}
		for _, test := range cases {
			status, body := e2eGet(t, client, server.url+test.path)
			if status != test.wantStatus || !strings.Contains(body, test.wantContains) {
				t.Fatalf("GET %s = %d/%q，期望 %d 且包含 %q",
					test.path, status, body, test.wantStatus, test.wantContains)
			}
		}
	})
}

// e2eServer 是运行中的 goweb serve 进程。
type e2eServer struct {
	url string

	cmd    *exec.Cmd
	mu     sync.Mutex
	stderr bytes.Buffer
}

// logs 返回服务端已输出的日志。
func (s *e2eServer) logs() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stderr.String()
}

// startServe 用编译后的二进制起一个开发服务器，等待它打印真实监听地址后返回。
// 用 --addr 127.0.0.1:0 让内核分配端口，再从日志里取回，避免端口冲突。
// 常规测试一律关掉自动开浏览器，免得在开发机或 CI 上真的弹窗。
func startServe(t *testing.T, binary, siteDir string, extraArgs ...string) *e2eServer {
	t.Helper()
	return startServeEnv(t, binary, siteDir, nil, append([]string{"--open=false"}, extraArgs...)...)
}

// startServeEnv 与 startServe 相同，但可以追加环境变量（例如替换打开浏览器的命令）。
func startServeEnv(t *testing.T, binary, siteDir string, env []string, extraArgs ...string) *e2eServer {
	t.Helper()
	args := append([]string{"serve", "--addr", "127.0.0.1:0"}, extraArgs...)
	return startServeProcess(t, binary, siteDir, env, args)
}

// startServeDefaultAddr 不带 --addr 启动，用的是内置默认端口 :8080，
// 用于验证「没有显式指定端口时被占用会自动递增」。
func startServeDefaultAddr(t *testing.T, binary, siteDir string) *e2eServer {
	t.Helper()
	return startServeProcess(t, binary, siteDir, nil,
		[]string{"serve", "--open=false", "--livereload=false"})
}

// startServeProcess 启动 serve 进程并等待它打印真实监听地址。
func startServeProcess(t *testing.T, binary, siteDir string, env, args []string) *e2eServer {
	t.Helper()

	// 在站点目录里启动，等价于用户在项目根运行 goweb：
	// 项目根 = 站点目录，templates/ 与静态资源都在它下面。
	cmd := exec.Command(binary, args...)
	cmd.Dir = siteDir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("获取 stderr: %v", err)
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 serve: %v", err)
	}

	server := &e2eServer{cmd: cmd}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	addrCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		sent := false
		for scanner.Scan() {
			line := scanner.Text()
			server.mu.Lock()
			server.stderr.WriteString(line + "\n")
			server.mu.Unlock()

			if sent {
				continue
			}
			if match := e2eAddrRe.FindStringSubmatch(line); match != nil {
				addrCh <- match[1]
				sent = true
			}
		}
	}()

	select {
	case addr := <-addrCh:
		server.url = "http://" + addr
	case <-time.After(e2eStartupTimeout):
		t.Fatalf("等待监听地址超时，日志: %s", server.logs())
	}
	return server
}

// runBinary 运行二进制并返回退出码与输出。
func runBinary(t *testing.T, binary string, args ...string) (int, string, string) {
	t.Helper()
	return runBinaryIn(t, "", binary, args...)
}

// buildRecorder 编译一个极小的记录器：把收到的最后一个参数追加写入 RECORDER_OUT 指向的文件。
// 把它设成 GOWEB_OPEN_COMMAND，就能验证「serve 真的用真实监听地址拉起了打开命令」，
// 又不会真的弹出浏览器窗口；用 Go 程序而不是 shell 脚本，三个平台都能跑。
func buildRecorder(t *testing.T) string {
	t.Helper()

	const source = `package main

import "os"

func main() {
	file, err := os.OpenFile(os.Getenv("RECORDER_OUT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(1)
	}
	defer file.Close()
	if _, err := file.WriteString(os.Args[len(os.Args)-1] + "\n"); err != nil {
		os.Exit(1)
	}
}
`
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), source)
	writeFile(t, filepath.Join(dir, "go.mod"), "module opener-recorder\n\ngo 1.26\n")

	binary := filepath.Join(dir, "recorder")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("编译记录器失败: %v\n%s", err, out)
	}
	return binary
}

// runBinaryIn 在指定工作目录里运行二进制（空串表示沿用当前目录）。
// 站点类命令要在站点目录里跑：项目根 = 站点目录。
func runBinaryIn(t *testing.T, dir, binary string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("运行 %v 失败: %v", args, err)
	}
	return exitErr.ExitCode(), stdout.String(), stderr.String()
}

// e2eRequest 发一次请求。
func e2eRequest(t *testing.T, client *http.Client, method, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s 失败: %v", method, url, err)
	}
	return resp
}

// e2eGet 发一次 GET 并返回状态码与响应体。
func e2eGet(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	resp := e2eRequest(t, client, http.MethodGet, url)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	return resp.StatusCode, string(body)
}

// waitForBody 轮询等待 URL 返回期望内容。
func waitForBody(t *testing.T, client *http.Client, url, want string) {
	t.Helper()
	deadline := time.Now().Add(e2eReloadTimeout)
	var last string
	for {
		if status, body := e2eGet(t, client, url); status == http.StatusOK && strings.Contains(body, want) {
			return
		} else {
			last = fmt.Sprintf("status=%d body=%s", status, body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s 未在 %s 内出现 %q，最后响应: %s", url, e2eReloadTimeout, want, last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// writeE2ESite 铺一个尽量接近真实使用的站点，包含页面、局部模板、数据与静态资源。
func writeE2ESite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		// index 用普通路径引用资源（验证自动补版本号）；
		// about 用 asset 函数（验证显式出口在关闭自动改写时依然有效）。
		"index.html": `<html><body>{{template "header" .}}<h1>INDEX</h1>` +
			`<p id="mode">{{if prod}}PROD{{else}}DEV{{end}}</p>` +
			`<link rel="stylesheet" href="/static/css/site.css">` +
			`<p id="build">{{buildTime | date "2006-01-02"}}</p></body></html>`,
		"about.html": `<html><body>{{template "header" .}}<h1>ABOUT</h1>` +
			`<link rel="stylesheet" href="{{asset "/static/css/site.css"}}"></body></html>`,
		"help/index.html":             `<html><body><h1>HELP-INDEX</h1></body></html>`,
		"help/74.html":                `<html><body><h1>HELP-74</h1></body></html>`,
		"templates/header.html":       `{{define "header"}}<header>{{.site.title | default "无标题"}}</header>{{end}}`,
		"templates/data.json":         `{"site":{"title":"E2E-SITE"}}`,
		"templates/note.txt":          "局部模板目录里的非 html 文件",
		"static/css/site.css":         ".a{color:red}",
		"static/js/site.js":           "console.log(1)",
		"robots.txt":                  "User-agent: *",
		"node_modules/pkg/index.html": "<html>DEP</html>",
		".hidden/secret.html":         "<html>SECRET</html>",
	}
	for name, body := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), body)
	}
	return dir
}
