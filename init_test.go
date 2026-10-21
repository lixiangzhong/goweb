package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestInitWritesDefaultConfig 覆盖 init 生成的配置：内容与模板一致、能被解析、
// 键的集合与 fileConfig 完全对齐（防止以后新增配置项时模板静默漏掉）。
func TestInitWritesDefaultConfig(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, err := executeCLIInSite(t, dir, "init")
	if err != nil {
		t.Fatalf("init 出错: %v（stderr: %s）", err, stderr)
	}
	if !strings.Contains(stdout, configFileName) || !strings.Contains(stdout, "goweb dev") {
		t.Fatalf("init 应告诉用户生成了哪个文件与下一步: stdout=%q", stdout)
	}

	path := filepath.Join(dir, configFileName)
	body := readFile(t, path)
	if body != defaultConfigTemplate {
		t.Fatal("生成的配置应与模板逐字一致")
	}
	if _, err := parseFileConfig(path); err != nil {
		t.Fatalf("生成的配置应能被解析: %v", err)
	}

	// 键集合：除刻意注释掉的 addr 外，必须与 fileConfig 一一对应。
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("解析生成的 YAML: %v", err)
	}
	for _, key := range configKeys() {
		_, active := parsed[key]
		comment := strings.Contains(body, "# "+key+":")
		if !active && !comment {
			t.Errorf("配置模板缺少键 %q（fileConfig 新增了配置项却忘了写进模板）", key)
		}
	}
	for key := range parsed {
		if !strings.Contains(strings.Join(configKeys(), " "), key) {
			t.Errorf("配置模板里有 fileConfig 不认识的键 %q", key)
		}
	}

	// addr 必须是注释：显式写 addr 会关掉「默认端口被占用时自动递增」。
	config, err := parseFileConfig(path)
	if err != nil {
		t.Fatalf("parseFileConfig: %v", err)
	}
	if config.Addr != nil {
		t.Fatalf("生成的配置不应显式写 addr（会关掉端口自动递增），实际 %q", *config.Addr)
	}
	if !strings.Contains(body, `# addr: ":8080"`) {
		t.Fatal("addr 应以注释形式列出，方便用户按需启用")
	}
}

// TestInitKeepsDefaultBehavior 覆盖「生成的配置不改变任何默认行为」：
// 用生成的配置跑一次，得到的设置应与完全没有配置时一致。
func TestInitKeepsDefaultBehavior(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := executeCLIInSite(t, dir, "init"); err != nil {
		t.Fatalf("init 出错: %v", err)
	}

	config, err := parseFileConfig(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatalf("parseFileConfig: %v", err)
	}
	defaults := map[string]struct {
		got  any
		want any
	}{
		"dir":         {deref(config.Dir), defaultPageDir},
		"template":    {deref(config.Template), defaultTemplateDir},
		"assets":      {deref(config.Assets), defaultAssetsRoot},
		"out":         {deref(config.Out), defaultOutDir},
		"mode":        {deref(config.Mode), modeAuto},
		"addr":        {deref(config.Addr), ""}, // 注释掉 → 未设置
		"build-time":  {deref(config.BuildTime), ""},
		"minify":      {derefBool(config.Minify), true},
		"fingerprint": {derefBool(config.Fingerprint), true},
		"livereload":  {derefBool(config.LiveReload), true},
		"open":        {derefBool(config.Open), true},
		"gitignore":   {derefBool(config.Gitignore), true},
	}
	for key, value := range defaults {
		if value.got != value.want {
			t.Errorf("生成的配置里 %s = %v，期望默认值 %v", key, value.got, value.want)
		}
	}
}

// TestInitRefusesToOverwrite 覆盖已存在时报错退出，并且不碰用户原有的文件。
func TestInitRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	const mine = "# 我自己调的配置\nout: public\n"
	writeFile(t, path, mine)

	_, _, err := executeCLIInSite(t, dir, "init")
	if err == nil {
		t.Fatal("配置文件已存在时 init 应当报错")
	}
	for _, want := range []string{"已存在", path, "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息应包含 %q，实际: %v", want, err)
		}
	}
	if got := readFile(t, path); got != mine {
		t.Fatalf("报错时不应改动用户文件，实际内容：%q", got)
	}
}

// TestInitForceOverwrites 覆盖 --force 覆盖。
func TestInitForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	writeFile(t, path, "out: public\n")

	if _, stderr, err := executeCLIInSite(t, dir, "init", "--force"); err != nil {
		t.Fatalf("init --force 出错: %v（stderr: %s）", err, stderr)
	}
	if got := readFile(t, path); got != defaultConfigTemplate {
		t.Fatal("--force 应覆盖为默认模板")
	}
}

// TestInitFindsConfigUpward 覆盖从子目录运行：项目根已有配置时不再生成第二份嵌套配置。
func TestInitFindsConfigUpward(t *testing.T) {
	root := t.TempDir()
	rootConfig := filepath.Join(root, configFileName)
	writeFile(t, rootConfig, "out: public\n")
	nested := filepath.Join(root, "some", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("建子目录: %v", err)
	}

	_, _, err := executeCLIInSite(t, nested, "init")
	if err == nil {
		t.Fatal("项目根已有配置时 init 应当报错，而不是生成嵌套的第二份")
	}
	if !strings.Contains(err.Error(), rootConfig) {
		t.Fatalf("错误信息应指出已有配置的路径 %s，实际: %v", rootConfig, err)
	}
	if _, statErr := os.Stat(filepath.Join(nested, configFileName)); statErr == nil {
		t.Fatal("不应在子目录里生成第二份配置")
	}
}

// TestInitConfigFlagSelectsPath 覆盖 --config 指定生成位置。
func TestInitConfigFlagSelectsPath(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "conf")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}

	if _, stderr, err := executeCLIInSite(t, dir, "init", "--config", filepath.Join("conf", "goweb.yml")); err != nil {
		t.Fatalf("init --config 出错: %v（stderr: %s）", err, stderr)
	}
	if got := readFile(t, filepath.Join(custom, "goweb.yml")); got != defaultConfigTemplate {
		t.Fatal("--config 指定的路径应生成默认模板")
	}
	if _, err := os.Stat(filepath.Join(dir, configFileName)); err == nil {
		t.Fatal("指定了 --config 就不该再往当前目录写一份")
	}
}

// TestInitThenBuild 覆盖「init 出来的配置能直接跑通后续命令」。
func TestInitThenBuild(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":          "<html>{{.site.title}}</html>",
		"templates/data.json": `{"site":{"title":"INIT-SITE"}}`,
	})
	if _, _, err := executeCLIInSite(t, dir, "init"); err != nil {
		t.Fatalf("init 出错: %v", err)
	}

	out := filepath.Join(t.TempDir(), "dist")
	if _, stderr, err := executeCLIInSite(t, dir, "build", "--out", out); err != nil {
		t.Fatalf("用生成的配置跑 build 出错: %v（stderr: %s）", err, stderr)
	}
	if body := readFile(t, filepath.Join(out, "index.html")); !strings.Contains(body, "INIT-SITE") {
		t.Fatalf("产物内容不正确: %s", body)
	}
}

// deref 取出字符串指针的值；nil 返回空字符串。
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// derefBool 取出布尔指针的值；nil 返回 false。
func derefBool(value *bool) bool {
	return value != nil && *value
}
