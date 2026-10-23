package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTemplateRootOverlapRejected 覆盖「模板根覆盖页面根/资源根」的配置错误。
//
// 模板根是只读、不产出、不可访问的子树；它一旦覆盖页面根或资源根，页面发现与资源
// 发布会被整体清空，产物是空的却照样生成成功。这类配置必须在加载期直接报错。
func TestTemplateRootOverlapRejected(t *testing.T) {
	newSite := func(t *testing.T) string {
		t.Helper()
		return writeSite(t, map[string]string{
			"pages/index.html":    "<html>INDEX</html>",
			"templates/data.json": `{"site":{"title":"X"}}`,
			"assets/static/x.css": "body{}",
		})
	}

	tests := []struct {
		name    string
		dir     string
		templ   string
		assets  string
		wantErr string
	}{
		{name: "模板根等于页面根", dir: ".", templ: ".", wantErr: "模板根不能是页面根"},
		{name: "模板根是页面根祖先", dir: "pages", templ: "."},
		{name: "模板根等于资源根", dir: "pages", templ: "assets", assets: "assets", wantErr: "资源根"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadSite(SiteOptions{
				ProjectRoot: newSite(t),
				Dir:         test.dir,
				TemplateDir: test.templ,
				AssetsRoot:  test.assets,
			})
			if err == nil {
				t.Fatal("模板根与页面根/资源根重叠时应当报错")
			}
			if test.wantErr != "" && !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("错误信息应包含 %q，实际: %v", test.wantErr, err)
			}
		})
	}

	// 默认布局（模板根在页面根/资源根之内）必须放行。
	if _, err := LoadSite(SiteOptions{ProjectRoot: newSite(t), Dir: "pages"}); err != nil {
		t.Fatalf("模板根位于页面根内是正常布局，不应报错: %v", err)
	}
}

// TestResolveInvariants 锁定 serve 与产物映射的不变式：URL 必须与文件在产物里的路径一致。
func TestResolveInvariants(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":       "<html>INDEX</html>",
		"pages/static/js/app.js": "console.log(1)",
		"static/raw.html":        "<html>RAW</html>",
		"static/a b.css":         "body{}",
	})
	site, err := LoadSite(SiteOptions{ProjectRoot: dir, Dir: "pages", AssetsRoot: "."})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}

	if route := site.Resolve("/static/js/app.js"); route.Kind != RouteFile {
		t.Fatalf("页面根内资源按页面根映射，/static/js/app.js 应为静态文件，实际 %+v", route)
	}
	// 这个 URL 多写了一层页面根目录：文件会进产物（产物路径是 static/js/app.js），
	// 但不会发布在 /pages/... 上，因此必须 404（本地能开、线上 404 是最坏情况）。
	if route := site.Resolve("/pages/static/js/app.js"); route.Kind != RouteNotFound {
		t.Fatalf("多写页面根目录的 URL 应当 404，实际 %+v", route)
	}
	// 页面根之外的 html 会原样进产物，按静态文件直出（与 nginx 一致）。
	if route := site.Resolve("/static/raw.html"); route.Kind != RouteFile {
		t.Fatalf("资源根里的 html 应按静态文件直出，实际 %+v", route)
	}
	// 含空格的静态文件：http 层已做百分号解码，Resolve 收到的是解码后的路径。
	if route := site.Resolve("/static/a b.css"); route.Kind != RouteFile {
		t.Fatalf("含空格的静态文件应可直出，实际 %+v", route)
	}
	assertPercentEncodedRequestServes(t, site)
}

// assertPercentEncodedRequestServes 验证浏览器发来的 /static/a%20b.css 能命中磁盘上的
// a b.css，且渲染时能给这个链接补上版本号。
func assertPercentEncodedRequestServes(t *testing.T, site *Site) {
	t.Helper()
	handler, _ := newHandlerWithLog(site)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/static/a%20b.css", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("百分号编码的静态文件请求状态码 = %d，期望 200", recorder.Code)
	}
}

// TestFingerprintPercentEncodedLink 覆盖百分号编码链接的指纹改写：必须解码后再查文件，
// 否则本地能开、线上也有文件，却拿不到 ?v= 版本号。
func TestFingerprintPercentEncodedLink(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":     `<html><body><link rel="stylesheet" href="/static/a%20b.css"></body></html>`,
		"static/a b.css": "body{}",
	})
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates", Fingerprint: true})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	body, err := site.Render("index")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(body), "/static/a%20b.css?v=") {
		t.Fatalf("百分号编码链接应补上版本号，实际: %s", body)
	}

	// check 也必须解码：既不能误报断链，也不能漏掉真实的产物路径。
	dir = writeSite(t, map[string]string{
		"index.html":     `<html><body><link rel="stylesheet" href="/static/a%20b.css"></body></html>`,
		"static/a b.css": "body{}",
	})
	if _, stderr, err := executeCLIInSite(t, dir, "check"); err != nil {
		t.Fatalf("含百分号编码链接的站点应通过 check: %v（stderr: %s）", err, stderr)
	}
}

// TestCheckIndexRelativeLinks 覆盖 index 页的相对链接：index 页既能用 /dir/ 访问，
// 也能用 /dir 访问，后者在浏览器里把 /dir 当文件，相对链接的基准是上一级目录。
// 只按目录这一种基准校验会放过「以 /dir 打开时断链」的链接。
func TestCheckIndexRelativeLinks(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":      `<html><body><a href="/">HOME</a></body></html>`,
		"pages/help/index.html": `<html><body><link rel="stylesheet" href="child.css"></body></html>`,
		"pages/help/child.css":  "body{}",
	})
	_, stderr, err := executeCLIInSite(t, dir, "check", "--dir", "pages", "--assets", ".")
	if err == nil {
		t.Fatal("以 /help 访问时 child.css 会落到 /child.css，check 应当失败")
	}
	for _, want := range []string{"child.css", "help/index"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("错误信息应包含 %q，实际: %s", want, stderr)
		}
	}
}

// TestGenCopiesPageRootAssetsWithDisjointRoots 覆盖「页面根与资源根互不包含」的布局：
// 页面根里的非 html 文件同样要按页面根映射进产物，否则 serve 能开、线上 404。
func TestGenCopiesPageRootAssetsWithDisjointRoots(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":    `<html><body><link rel="stylesheet" href="/static/x.css"></body></html>`,
		"pages/static/x.css":  "body{}",
		"templates/data.json": `{"site":{"title":"X"}}`,
	})
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("创建资源根: %v", err)
	}
	if _, stderr, err := executeCLIInSite(t, dir, "gen", "--dir", "pages", "--assets", "assets"); err != nil {
		t.Fatalf("gen: %v（stderr: %s）", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist", "static", "x.css")); err != nil {
		t.Fatalf("页面根里的资源应进产物: %v", err)
	}
	if _, _, err := executeCLIInSite(t, dir, "check", "--dir", "pages", "--assets", "assets"); err != nil {
		t.Fatalf("check 应与 gen 一致地认为该资源会进产物: %v", err)
	}
}

// TestGenRejectsSymlinkedOut 覆盖产物目录是符号链接：clean 会顺着链接清空目标目录，
// 可能删掉站点之外的用户文件，必须直接拒绝。
func TestGenRejectsSymlinkedOut(t *testing.T) {
	dir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	victim := t.TempDir()
	keep := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatalf("写入目标目录文件: %v", err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "dist")); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}

	if _, stderr, err := executeCLIInSite(t, dir, "gen"); err == nil {
		t.Fatal("产物目录是符号链接时应当报错")
	} else if !strings.Contains(stderr, "符号链接") {
		t.Fatalf("错误信息应说明符号链接风险，实际: %s", stderr)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("被指向目录里的文件不应被删除: %v", err)
	}
}

// TestGenSymlinkPolicy 覆盖 gen 的软链接策略：指向站点根之外或已失效的链接一律跳过
// 并告警，不让一个无关的坏链接导致整次生成失败，也不把站外内容拷进产物。
func TestGenSymlinkPolicy(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":    "<html>INDEX</html>",
		"static/ok.css": "body{}",
	})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("写入站外文件: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "static", "leak.txt")); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing.txt"), filepath.Join(dir, "static", "broken.txt")); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}

	_, stderr, err := executeCLIInSite(t, dir, "gen")
	if err != nil {
		t.Fatalf("坏链接不应让整次生成失败: %v（stderr: %s）", err, stderr)
	}
	for _, absent := range []string{"static/leak.txt", "static/broken.txt"} {
		if _, statErr := os.Stat(filepath.Join(dir, "dist", filepath.FromSlash(absent))); statErr == nil {
			t.Fatalf("不应把 %s 拷进产物", absent)
		}
	}
	for _, want := range []string{"站点根之外", "失效的符号链接"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("日志应包含 %q，实际: %s", want, stderr)
		}
	}
}

// TestCheckReportsConflictAsSingleProblem 覆盖「一条多行错误被按行拆成多条问题」的回归：
// 产物路径冲突应该只算一条，并保留两个来源。
func TestCheckReportsConflictAsSingleProblem(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"pages/index.html":  "<html>INDEX</html>",
		"pages/static/a.js": "console.log(1)",
		"static/a.js":       "console.log(2)",
	})
	_, stderr, err := executeCLIInSite(t, dir, "check", "--dir", "pages", "--assets", ".")
	if err == nil {
		t.Fatal("产物路径冲突时 check 应当失败")
	}
	if !strings.Contains(stderr, "共 1 个问题") {
		t.Fatalf("一条冲突只算一个问题，实际输出: %s", stderr)
	}
	for _, want := range []string{"产物路径冲突", "来源一", "来源二"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("错误信息应包含 %q，实际: %s", want, stderr)
		}
	}
}

// TestGenLogReportsResolvedFingerprint 覆盖 gen 日志字段：必须报告真实生效的
// fingerprint 设置，而不是命令行 flag 的默认值。配置里关掉指纹时日志不能谎报 true，
// 产物里也不能出现 ?v=。
func TestGenLogReportsResolvedFingerprint(t *testing.T) {
	dir := writeSite(t, map[string]string{
		".goweb.yml":   "fingerprint: false\n",
		"index.html":   `<html><body><link rel="stylesheet" href="/static/a.css"></body></html>`,
		"static/a.css": "body{}",
	})
	_, stderr, err := executeCLIInSite(t, dir, "gen")
	if err != nil {
		t.Fatalf("gen: %v（stderr: %s）", err, stderr)
	}
	if !strings.Contains(stderr, "fingerprint=false") {
		t.Fatalf("日志应报告真实生效的 fingerprint=false，实际: %s", stderr)
	}
	body, err := os.ReadFile(filepath.Join(dir, "dist", "index.html"))
	if err != nil {
		t.Fatalf("读取产物: %v", err)
	}
	if strings.Contains(string(body), "?v=") {
		t.Fatalf("关闭指纹后产物里不应有 ?v=，实际: %s", body)
	}
}

// TestServeDefaultAddrIsLoopback 锁定默认监听地址：开发服务器默认只监听本机，
// 避免在局域网/咖啡厅网络里把整个项目目录暴露出去。
func TestServeDefaultAddrIsLoopback(t *testing.T) {
	dir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	stdout, stderr, err := executeCLIInSite(t, dir, "serve", "--help")
	if err != nil {
		t.Fatalf("serve --help: %v（stderr: %s）", err, stderr)
	}
	if !strings.Contains(stdout, "127.0.0.1:8080") {
		t.Fatalf("默认监听地址应为 127.0.0.1:8080，--help 实际输出: %s", stdout)
	}
}

// TestResolveSkipsSymlinkedDirs 锁定目录软链接策略：gen 的遍历不进入目录软链接，
// serve 也必须当作不存在，否则会出现「本地能开、产物里没有」的 URL。
func TestResolveSkipsSymlinkedDirs(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html": "<html><body>hi</body></html>",
		"real/f.txt": "static",
	})
	if err := os.Symlink("real", filepath.Join(dir, "linkdir")); err != nil {
		t.Skipf("当前平台无法创建符号链接：%v", err)
	}
	site, err := LoadSite(SiteOptions{Dir: dir, TemplateDir: "templates"})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	if c := site.Resolve("/linkdir/f.txt"); c.Kind != RouteNotFound {
		t.Fatalf("目录软链接不应被 serve 跟随，实际得到 %+v", c)
	}
	if c := site.Resolve("/real/f.txt"); c.Kind != RouteFile {
		t.Fatalf("普通目录下的文件应可访问，实际得到 %+v", c)
	}
}

// TestResolveWithRootsOutsideProjectRoot 覆盖「三个根可以独立于项目根」的契约。
//
// 页面根/资源根允许是绝对路径且位于项目根之外；此时相对项目根的路径必然出现 ".."
// 组件，它只是路径记号，不能被当作隐藏项，否则整站都会被判定为忽略而全部 404。
func TestResolveWithRootsOutsideProjectRoot(t *testing.T) {
	pages := writeSite(t, map[string]string{
		"index.html":   "<html>OUTER</html>",
		"static/a.css": "body{}",
		".hidden.html": "<html>HIDDEN</html>",
	})
	projectRoot := t.TempDir() // 与页面根互不包含
	site, err := LoadSite(SiteOptions{ProjectRoot: projectRoot, Dir: pages, AssetsRoot: pages})
	if err != nil {
		t.Fatalf("LoadSite: %v", err)
	}
	if route := site.Resolve("/"); route.Kind != RoutePage || route.Name != "index" {
		t.Fatalf("Resolve(/) = %+v，期望 index 页面", route)
	}
	if _, ok := site.resolveURL("static/a.css"); !ok {
		t.Fatal("页面根位于项目根之外时，根内资源不应被当作忽略项")
	}
	if !site.ignoredPath(filepath.Join(pages, ".hidden.html")) {
		t.Fatal("隐藏文件仍应被忽略")
	}
}
