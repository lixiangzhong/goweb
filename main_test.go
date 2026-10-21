package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// executeCLI 用给定参数跑一次完整的 CLI（每次都新建命令树，避免状态串味），
// 返回标准输出、标准错误与错误。
func executeCLI(args ...string) (string, string, error) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestRootCommandFlags(t *testing.T) {
	root := newRootCommand()

	// 根命令自带 serve 参数：不带子命令时等价于 serve。
	if root.Flags().Lookup("addr") == nil {
		t.Fatal("根命令缺少 --addr")
	}
	// --dir / --template 是 persistent flag，子命令共享（解析时才合并进各自的 Flags()）。
	for _, name := range []string{"dir", "template"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("根命令缺少持久参数 --%s", name)
		}
	}
}

func TestSubcommandFlags(t *testing.T) {
	root := newRootCommand()

	serveCmd, _, err := root.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("查找 serve 子命令: %v", err)
	}
	if serveCmd.Flags().Lookup("addr") == nil {
		t.Fatal("serve 子命令缺少 --addr")
	}

	genCmd, _, err := root.Find([]string{"gen"})
	if err != nil {
		t.Fatalf("查找 gen 子命令: %v", err)
	}
	for _, name := range []string{"out", "clean"} {
		if genCmd.Flags().Lookup(name) == nil {
			t.Fatalf("gen 子命令缺少 --%s", name)
		}
	}
	// 只有 serve 用的参数不应出现在 gen 上。
	if genCmd.Flags().Lookup("addr") != nil {
		t.Fatal("gen 子命令不应接受 --addr")
	}
	// gen 通过 persistent flag 继承公共参数。
	for _, name := range []string{"dir", "template"} {
		if genCmd.InheritedFlags().Lookup(name) == nil {
			t.Fatalf("gen 子命令未继承 --%s", name)
		}
	}
}

func TestHelpAndVersion(t *testing.T) {
	stdout, _, err := executeCLI("--version")
	if err != nil {
		t.Fatalf("--version 出错: %v", err)
	}
	if !strings.Contains(stdout, "dev") {
		t.Fatalf("--version 输出异常: %s", stdout)
	}

	stdout, _, err = executeCLI("--help")
	if err != nil {
		t.Fatalf("--help 出错: %v", err)
	}
	for _, want := range []string{"serve", "gen", "completion", "--dir", "--addr"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("帮助信息缺少 %q：%s", want, stdout)
		}
	}
}

// TestGenCommandGenerates 走完整的 CLI 链路验证 gen：参数解析 → 渲染 → 产物落盘。
func TestGenCommandGenerates(t *testing.T) {
	siteDir := writeSite(t, map[string]string{
		"index.html":          `<html>INDEX {{.site.title}}</html>`,
		"templates/data.json": `{"site":{"title":"CLI-SITE"}}`,
		"static/a.css":        "body{}",
	})
	out := filepath.Join(t.TempDir(), "out")

	_, stderr, err := executeCLIInSite(t, siteDir, "gen", "--out", out)
	if err != nil {
		t.Fatalf("gen 出错: %v（stderr: %s）", err, stderr)
	}
	if !strings.Contains(stderr, "静态站已生成") {
		t.Fatalf("缺少生成日志: %s", stderr)
	}

	body, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatalf("读取产物: %v", err)
	}
	if !strings.Contains(string(body), "CLI-SITE") {
		t.Fatalf("产物内容不正确: %s", body)
	}
	if _, err := os.Stat(filepath.Join(out, "static", "a.css")); err != nil {
		t.Fatalf("资源未拷贝: %v", err)
	}
}

// TestBuildAliasEqualsGen 覆盖 gen 的别名 build：CLI 结构上有别名，行为与 gen 完全一致。
func TestBuildAliasEqualsGen(t *testing.T) {
	root := newRootCommand()
	genCmd, _, err := root.Find([]string{"gen"})
	if err != nil {
		t.Fatalf("查找 gen: %v", err)
	}
	if !equalStrings(genCmd.Aliases, []string{"build"}) {
		t.Fatalf("gen 的别名 = %v，期望 [build]", genCmd.Aliases)
	}
	// 别名要能被解析到同一个命令（cobra 的 Find 会走别名匹配）。
	aliased, args, err := root.Find([]string{"build", "--out", "public"})
	if err != nil {
		t.Fatalf("解析 build: %v", err)
	}
	if aliased != genCmd {
		t.Fatalf("build 应解析到 gen 命令，实际 %s", aliased.Name())
	}
	if !equalStrings(args, []string{"--out", "public"}) {
		t.Fatalf("别名后的参数应原样保留，实际 %v", args)
	}

	t.Run("行为与 gen 一致", func(t *testing.T) {
		siteDir := writeSite(t, map[string]string{
			"index.html":          `<html>INDEX {{.site.title}}</html>`,
			"templates/data.json": `{"site":{"title":"ALIAS"}}`,
		})
		out := filepath.Join(t.TempDir(), "built")
		_, stderr, err := executeCLIInSite(t, siteDir, "build", "--out", out)
		if err != nil {
			t.Fatalf("build 出错: %v（stderr: %s）", err, stderr)
		}
		if !strings.Contains(stderr, "静态站已生成") {
			t.Fatalf("缺少生成日志: %s", stderr)
		}
		if body := readFile(t, filepath.Join(out, "index.html")); !strings.Contains(body, "ALIAS") {
			t.Fatalf("产物内容不正确: %s", body)
		}
		// 别名与 gen 一样是 prod 模式（--mode auto 下按产出方向取默认值）。
		if !strings.Contains(stderr, "mode=prod") {
			t.Fatalf("build 应与 gen 一样默认 prod 模式: %s", stderr)
		}
	})
}

// TestGlobalFlagsCanPrecedeSubcommand 覆盖 `goweb --dir . gen`：持久参数写在子命令之前也要生效。
func TestGlobalFlagsCanPrecedeSubcommand(t *testing.T) {
	siteDir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	out := filepath.Join(t.TempDir(), "out")

	_, stderr, err := executeCLIInSite(t, siteDir, "--dir", ".", "gen", "--out", out)
	if err != nil {
		t.Fatalf("gen 出错: %v（stderr: %s）", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "index.html")); err != nil {
		t.Fatalf("持久参数未生效，产物缺失: %v", err)
	}
}

func TestGenCommandCleanFlag(t *testing.T) {
	siteDir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	out := filepath.Join(t.TempDir(), "out")
	stale := filepath.Join(out, "stale.html")

	if _, _, err := executeCLIInSite(t, siteDir, "gen", "--out", out); err != nil {
		t.Fatalf("首次生成: %v", err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatalf("写入遗留文件: %v", err)
	}

	if _, _, err := executeCLIInSite(t, siteDir, "gen", "--out", out, "--clean=false"); err != nil {
		t.Fatalf("--clean=false 生成: %v", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("--clean=false 不应删除遗留文件: %v", err)
	}

	if _, _, err := executeCLIInSite(t, siteDir, "gen", "--out", out, "--clean"); err != nil {
		t.Fatalf("--clean 生成: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("--clean 应清空遗留文件")
	}
}

// TestCommandErrors 覆盖 CLI 层错误路径：未知子命令、未知 flag、多余参数、护栏错误。
func TestCommandErrors(t *testing.T) {
	siteDir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "未知子命令", args: []string{"bogus"}, want: "unknown command"},
		{name: "未知 flag", args: []string{"gen", "--bogus"}, want: "unknown flag"},
		{name: "多余位置参数", args: []string{"gen", "extra"}, want: "unknown command"},
		{name: "产物目录护栏", args: []string{"gen", "--out", "."}, want: "产物目录"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// 在站点目录里运行：项目根 = 站点目录，--out . 即把项目根当产物目录。
			_, stderr, err := executeCLIInSite(t, siteDir, test.args...)
			if err == nil {
				t.Fatalf("%v 应当报错", test.args)
			}
			if !strings.Contains(stderr, test.want) && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("错误信息缺少 %q（stderr: %s，err: %v）", test.want, stderr, err)
			}
		})
	}
}

// TestRuntimeErrorKeepsErrorPrefixAndSkipsUsage 运行期错误应带 goweb: 前缀且不打印 usage。
func TestRuntimeErrorKeepsErrorPrefixAndSkipsUsage(t *testing.T) {
	siteDir := writeSite(t, map[string]string{
		"index.html":          "<html>INDEX</html>",
		"templates/data.json": `{"broken": }`,
	})

	_, stderr, err := executeCLIInSite(t, siteDir, "gen")
	if err == nil {
		t.Fatal("坏 data.json 应当报错")
	}
	if !strings.Contains(stderr, "goweb:") {
		t.Fatalf("错误缺少 goweb: 前缀: %s", stderr)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Fatalf("运行期错误不应打印 usage: %s", stderr)
	}
}

// TestRootWithoutSubcommandRunsServe 覆盖不带子命令时的默认行为：
// 这里用坏 data.json 让 serve 在真正监听端口之前就失败，从而只验证走到了 serve。
func TestRootWithoutSubcommandRunsServe(t *testing.T) {
	siteDir := writeSite(t, map[string]string{
		"index.html":          "<html>INDEX</html>",
		"templates/data.json": `{"broken": }`,
	})

	_, _, err := executeCLIInSite(t, siteDir)
	if err == nil {
		t.Fatal("根命令应当执行 serve 并因坏数据失败")
	}
	if !strings.Contains(err.Error(), "数据文件") {
		t.Fatalf("错误不是来自 serve 的站点加载: %v", err)
	}
}

// TestCommandFlagDefaults 锁定各子命令的默认参数值，避免无意改默认行为。
func TestCommandFlagDefaults(t *testing.T) {
	root := newRootCommand()

	if got, err := root.Flags().GetString("addr"); err != nil || got != defaultAddr {
		t.Fatalf("根命令 --addr 默认值 = %q（err=%v），期望 %q", got, err, defaultAddr)
	}
	for flag, want := range map[string]string{"dir": defaultPageDir, "template": defaultTemplateDir} {
		got, err := root.PersistentFlags().GetString(flag)
		if err != nil || got != want {
			t.Fatalf("持久参数 --%s 默认值 = %q（err=%v），期望 %q", flag, got, err, want)
		}
	}

	genCmd, _, err := root.Find([]string{"gen"})
	if err != nil {
		t.Fatalf("查找 gen: %v", err)
	}
	if got, err := genCmd.Flags().GetString("out"); err != nil || got != defaultOutDir {
		t.Fatalf("gen --out 默认值 = %q（err=%v），期望 %q", got, err, defaultOutDir)
	}
	if got, err := genCmd.Flags().GetBool("clean"); err != nil || !got {
		t.Fatalf("gen --clean 默认值 = %v（err=%v），期望 true", got, err)
	}
	if genCmd.Flags().Lookup("livereload") != nil {
		t.Fatal("gen 子命令不应接受 --livereload")
	}

	// 根命令与 serve 子命令都带热重载开关，且默认开启。
	for name, cmd := range map[string]*cobra.Command{"根命令": root, "serve": serveCmd(t, root)} {
		if got, err := cmd.Flags().GetBool("livereload"); err != nil || !got {
			t.Fatalf("%s --livereload 默认值 = %v（err=%v），期望 true", name, got, err)
		}
		// 自动打开浏览器同样默认开启，用 --open=false 关闭。
		if got, err := cmd.Flags().GetBool("open"); err != nil || !got {
			t.Fatalf("%s --open 默认值 = %v（err=%v），期望 true", name, got, err)
		}
	}

	// gen 不该有这两个开发期开关（避免被静默接受）。
	if flag := serveCmd(t, root).Flags().Lookup("open"); flag == nil {
		t.Fatal("serve 应提供 --open")
	}
	if genCmd := func() *cobra.Command { cmd, _, _ := root.Find([]string{"gen"}); return cmd }(); genCmd.Flags().Lookup("open") != nil {
		t.Fatal("gen 不应提供 --open")
	}
}

// serveCmd 取出 serve 子命令。
func serveCmd(t *testing.T, root *cobra.Command) *cobra.Command {
	t.Helper()
	cmd, _, err := root.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("查找 serve: %v", err)
	}
	return cmd
}

// TestVersionFlagUsesInjectedVersion 覆盖构建时用 -ldflags 注入版本号后的 --version 输出。
func TestVersionFlagUsesInjectedVersion(t *testing.T) {
	original := version
	version = "v9.9.9-test"
	t.Cleanup(func() { version = original })

	stdout, _, err := executeCLI("--version")
	if err != nil {
		t.Fatalf("--version 出错: %v", err)
	}
	if !strings.Contains(stdout, "v9.9.9-test") {
		t.Fatalf("--version 未使用注入的版本号: %s", stdout)
	}
}

// TestGenWithoutTemplateDir 覆盖 --template 置空：没有模板目录时不加载数据、templates/ 变成普通目录。
func TestGenWithoutTemplateDir(t *testing.T) {
	siteDir := writeSite(t, map[string]string{
		"index.html":          "<html>{{.site.title}}</html>",
		"templates/data.json": `{"site":{"title":"IGNORED"}}`,
	})
	out := filepath.Join(t.TempDir(), "out")

	if _, stderr, err := executeCLIInSite(t, siteDir, "gen", "--template", "", "--out", out); err != nil {
		t.Fatalf("gen 出错: %v（stderr: %s）", err, stderr)
	}
	// 断言「数据没被加载」而不是逐字比对产物：产物默认会被压缩，
	// 例如空文档会被压成空文件，逐字断言会把压缩行为卷进来。
	if got := readFile(t, filepath.Join(out, "index.html")); strings.Contains(got, "IGNORED") {
		t.Fatalf("没有模板目录时不应加载 data.json，产物 = %q", got)
	}
	// templates/ 不再是模板目录，其中的文件按静态资源拷贝。
	if _, err := os.Stat(filepath.Join(out, "templates", "data.json")); err != nil {
		t.Fatalf("没有模板目录时 templates/ 应作为普通目录拷贝: %v", err)
	}
}

// TestErrorOutputUsesGowebPrefix 覆盖统一错误前缀与 usage 输出。
// 注意 cobra 的 usage 走 OutOrStderr：真实进程（未设置 Out）落在标准错误，
// 进程内测试设置了 Out，因此这里合并两路输出一起断言。
func TestErrorOutputUsesGowebPrefix(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "未知子命令", args: []string{"bogus"}, want: "unknown command"},
		{name: "未知参数", args: []string{"gen", "--bogus"}, want: "unknown flag"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := executeCLI(test.args...)
			if err == nil {
				t.Fatalf("%v 应当报错", test.args)
			}
			if !strings.Contains(stderr, "goweb:") {
				t.Fatalf("错误缺少 goweb: 前缀: %s", stderr)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("错误信息 = %q，期望包含 %q", err.Error(), test.want)
			}
			if combined := stdout + stderr; !strings.Contains(combined, "Usage:") {
				t.Fatalf("参数错误应打印 usage: %s", combined)
			}
		})
	}
}

// TestHelpGoesToStdout 覆盖帮助输出到标准输出（而不是标准错误）。
func TestHelpGoesToStdout(t *testing.T) {
	stdout, stderr, err := executeCLI("gen", "--help")
	if err != nil {
		t.Fatalf("--help 不应报错: %v", err)
	}
	for _, want := range []string{"--out", "--clean", "--dir", "Examples:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("帮助缺少 %q：%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("帮助不应写到标准错误: %s", stderr)
	}
}

// TestServeFailsOnInvalidAddr 覆盖 serve 在监听失败时的错误路径（站点加载与监听器都要先就绪）。
func TestServeFailsOnInvalidAddr(t *testing.T) {
	siteDir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})

	_, _, err := executeCLIInSite(t, siteDir, "serve", "--addr", "127.0.0.1:-1")
	if err == nil {
		t.Fatal("非法监听地址应当报错")
	}
	// 不同平台与 Go 版本的措辞可能不同，这里只要求错误确实指向地址/端口。
	message := err.Error()
	if !strings.Contains(message, "port") && !strings.Contains(message, "address") {
		t.Fatalf("错误信息不像监听失败: %v", err)
	}
}

// TestNewLoggerAcceptsNilWriter 覆盖未指定输出时的默认行为。
func TestNewLoggerAcceptsNilWriter(t *testing.T) {
	if logger := newLogger(nil); logger == nil {
		t.Fatal("newLogger(nil) 不应返回 nil")
	}
}

// TestResolveMode 覆盖 --mode 的解析：auto 按子命令取默认值，显式值原样生效。
func TestResolveMode(t *testing.T) {
	tests := []struct {
		raw        string
		devCommand bool
		want       Mode
	}{
		{raw: "", devCommand: true, want: ModeDev},
		{raw: "", devCommand: false, want: ModeProd},
		{raw: modeAuto, devCommand: true, want: ModeDev},
		{raw: modeAuto, devCommand: false, want: ModeProd},
		{raw: "dev", devCommand: false, want: ModeDev},
		{raw: "prod", devCommand: true, want: ModeProd},
		{raw: " PROD ", devCommand: true, want: ModeProd},
	}
	for _, test := range tests {
		got, err := resolveMode(test.raw, test.devCommand)
		if err != nil {
			t.Fatalf("resolveMode(%q, %v) 出错: %v", test.raw, test.devCommand, err)
		}
		if got != test.want {
			t.Fatalf("resolveMode(%q, %v) = %q，期望 %q", test.raw, test.devCommand, got, test.want)
		}
	}

	if _, err := resolveMode("stage", true); err == nil {
		t.Fatal("不支持的 --mode 取值应当报错")
	}
}

// TestParseBuildTime 覆盖 --build-time 的多种写法与非法输入。
func TestParseBuildTime(t *testing.T) {
	zero, err := parseBuildTime("")
	if err != nil || !zero.IsZero() {
		t.Fatalf("空值应表示由站点推导（零时间），实际 %v err=%v", zero, err)
	}

	tests := map[string]string{
		"2026-03-05T10:30:00Z":      "2026-03-05",
		"2026-03-05T18:30:00+08:00": "2026-03-05",
		"2026-03-05 10:30:00":       "2026-03-05",
		"2026-03-05":                "2026-03-05",
	}
	for raw, wantDate := range tests {
		parsed, err := parseBuildTime(raw)
		if err != nil {
			t.Fatalf("parseBuildTime(%q) 出错: %v", raw, err)
		}
		if got := parsed.Format("2006-01-02"); got != wantDate {
			t.Fatalf("parseBuildTime(%q) = %s，期望日期 %s", raw, got, wantDate)
		}
	}

	if _, err := parseBuildTime("昨天"); err == nil {
		t.Fatal("无法解析的 --build-time 应当报错")
	}
}

// TestModeAndBuildTimeFlags 覆盖两个新参数在各子命令上的默认值与生效路径。
func TestModeAndBuildTimeFlags(t *testing.T) {
	root := newRootCommand()
	if got, err := root.PersistentFlags().GetString("mode"); err != nil || got != modeAuto {
		t.Fatalf("--mode 默认值 = %q（err=%v），期望 %q", got, err, modeAuto)
	}
	if got, err := root.PersistentFlags().GetString("build-time"); err != nil || got != "" {
		t.Fatalf("--build-time 默认值 = %q（err=%v），期望空", got, err)
	}
	// 资源版本号默认开启：gen 默认就要做缓存失效。
	if got, err := root.PersistentFlags().GetBool("fingerprint"); err != nil || !got {
		t.Fatalf("--fingerprint 默认值 = %v（err=%v），期望 true", got, err)
	}

	// 非法取值要在真正干活之前就报错（用坏站点也应是参数错误先出现）。
	if _, _, err := executeCLI("gen", "--mode", "stage"); err == nil ||
		!strings.Contains(err.Error(), "--mode") {
		t.Fatalf("非法 --mode 应报参数错误，实际: %v", err)
	}
	if _, _, err := executeCLI("gen", "--build-time", "昨天"); err == nil ||
		!strings.Contains(err.Error(), "--build-time") {
		t.Fatalf("非法 --build-time 应报参数错误，实际: %v", err)
	}
}

// TestDevAliasEqualsServe 覆盖 serve 的别名 dev：CLI 结构上有别名，行为与 serve 一致。
func TestDevAliasEqualsServe(t *testing.T) {
	root := newRootCommand()
	serveCommand := serveCmd(t, root)
	if !equalStrings(serveCommand.Aliases, []string{"dev"}) {
		t.Fatalf("serve 的别名 = %v，期望 [dev]", serveCommand.Aliases)
	}
	aliased, args, err := root.Find([]string{"dev", "--addr", ":3000"})
	if err != nil {
		t.Fatalf("解析 dev: %v", err)
	}
	if aliased != serveCommand {
		t.Fatalf("dev 应解析到 serve 命令，实际 %s", aliased.Name())
	}
	if !equalStrings(args, []string{"--addr", ":3000"}) {
		t.Fatalf("别名后的参数应原样保留，实际 %v", args)
	}

	t.Run("行为与 serve 一致", func(t *testing.T) {
		siteDir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
		// 用非法端口让 serve 在监听阶段失败：既证明真的走进了 serve，又不会阻塞测试。
		// --open/--livereload 是 serve 专属参数，能解析就说明确实是同一条命令。
		_, stderr, err := executeCLIInSite(t, siteDir, "dev",
			"--open=false", "--livereload=false", "--addr", "127.0.0.1:-1")
		if err == nil {
			t.Fatal("非法端口应当报错")
		}
		if strings.Contains(stderr, "unknown flag") {
			t.Fatalf("dev 应支持 serve 的专属参数: %s", stderr)
		}
		if !strings.Contains(err.Error(), ":") && !strings.Contains(err.Error(), "port") {
			t.Fatalf("错误应来自监听阶段: %v", err)
		}
	})
}

// TestLoggerUsesReadableTimeFormat 覆盖日志时间戳格式：time.DateTime（2006-01-02 15:04:05）。
// 不带毫秒与时区偏移，终端里对齐更整齐；含空格所以 slog 会自动加引号。
func TestLoggerUsesReadableTimeFormat(t *testing.T) {
	var buffer syncBuffer
	logger := newLogger(&buffer)
	logger.Info("测试消息", "key", "value")

	line := strings.TrimSpace(buffer.String())
	if !strings.HasPrefix(line, `time="`) {
		t.Fatalf("时间戳应被引号包裹（含空格）: %q", line)
	}
	timestamp, rest, found := strings.Cut(strings.TrimPrefix(line, `time="`), `"`)
	if !found {
		t.Fatalf("时间戳缺少结束引号: %q", line)
	}
	if _, err := time.Parse(time.DateTime, timestamp); err != nil {
		t.Fatalf("时间戳应能用 time.DateTime 解析，实际 %q: %v", timestamp, err)
	}
	if len(timestamp) != len("2006-01-02 15:04:05") {
		t.Fatalf("时间戳长度应为 %d，实际 %q", len("2006-01-02 15:04:05"), timestamp)
	}
	if want := " level=INFO msg=测试消息 key=value"; rest != want {
		t.Fatalf("日志其余部分 = %q，期望 %q", rest, want)
	}
}

// TestEffectiveVersion 覆盖版本号来源：-ldflags 注入优先，没有注入时回退到构建信息里的模块版本，
// 本地 go build/go test 的模块版本是 "(devel)"，最终回退到 "dev"。
func TestEffectiveVersion(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "dev"
	if got := effectiveVersion(); got != "dev" {
		t.Fatalf("本地构建（模块版本为 devel）应回退 dev，实际 %q", got)
	}
	version = "v9.9.9-test"
	if got := effectiveVersion(); got != "v9.9.9-test" {
		t.Fatalf("-ldflags 注入的值应优先，实际 %q", got)
	}
}
