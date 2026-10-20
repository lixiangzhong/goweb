package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	_, stderr, err := executeCLI("gen", "--dir", siteDir, "--out", out)
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

// TestGlobalFlagsCanPrecedeSubcommand 覆盖 `goweb --dir X gen`：持久参数写在子命令之前也要生效。
func TestGlobalFlagsCanPrecedeSubcommand(t *testing.T) {
	siteDir := writeSite(t, map[string]string{"index.html": "<html>INDEX</html>"})
	out := filepath.Join(t.TempDir(), "out")

	_, stderr, err := executeCLI("--dir", siteDir, "gen", "--out", out)
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

	if _, _, err := executeCLI("gen", "--dir", siteDir, "--out", out); err != nil {
		t.Fatalf("首次生成: %v", err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatalf("写入遗留文件: %v", err)
	}

	if _, _, err := executeCLI("gen", "--dir", siteDir, "--out", out, "--clean=false"); err != nil {
		t.Fatalf("--clean=false 生成: %v", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("--clean=false 不应删除遗留文件: %v", err)
	}

	if _, _, err := executeCLI("gen", "--dir", siteDir, "--out", out, "--clean"); err != nil {
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
		{name: "产物目录护栏", args: []string{"gen", "--dir", siteDir, "--out", siteDir}, want: "产物目录"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, stderr, err := executeCLI(test.args...)
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

	_, stderr, err := executeCLI("gen", "--dir", siteDir)
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

	_, _, err := executeCLI("--dir", siteDir)
	if err == nil {
		t.Fatal("根命令应当执行 serve 并因坏数据失败")
	}
	if !strings.Contains(err.Error(), "数据文件") {
		t.Fatalf("错误不是来自 serve 的站点加载: %v", err)
	}
}
