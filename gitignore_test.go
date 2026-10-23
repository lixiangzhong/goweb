package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIgnoreMatcherMatchesGit 用真实 git 做对拍：
// 同一份 .gitignore 与同一批路径，git check-ignore 的判定必须与我们的匹配器一致。
func TestIgnoreMatcherMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git，跳过错拍测试")
	}

	const gitignore = `# 注释与空行

*.log
!keep.log
build/
dist/**
**/node_modules
/root-only.txt
static/*.min.js
drafts/
drafts/!keep-draft.html
`
	// 覆盖各种模式形态：命中/未命中、目录与文件、反选、锚定、** 跨层。
	candidates := []string{
		"a.log",
		"keep.log",
		"sub/b.log",
		"sub/keep.log",
		"build",
		"build/out.txt",
		"dist/app.js",
		"src/dist/app.js",
		"node_modules/pkg/index.js",
		"sub/node_modules/pkg/index.js",
		"root-only.txt",
		"sub/root-only.txt",
		"static/app.min.js",
		"static/app.js",
		"sub/static/app.min.js",
		"drafts/note.html",
		"drafts/sub/note.html",
		"src/index.html",
		"README.md",
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatalf("写入 .gitignore: %v", err)
	}
	// 让候选路径真实存在（作为目录或文件），git check-ignore 才会给出正确判定。
	directories := map[string]bool{}
	for _, rel := range candidates {
		segments := strings.Split(rel, "/")
		for i := 1; i < len(segments); i++ {
			directories[strings.Join(segments[:i], "/")] = true
		}
	}
	for _, rel := range candidates {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if directories[rel] {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatalf("建目录 %s: %v", rel, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("建父目录 %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("写文件 %s: %v", rel, err)
		}
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	matcher, err := loadIgnoreMatcher(dir, true, nil)
	if err != nil {
		t.Fatalf("loadIgnoreMatcher: %v", err)
	}

	// 一次性问 git：给出所有被忽略的路径。
	gitIgnored := gitCheckIgnore(t, dir, candidates)

	for _, rel := range candidates {
		info, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		isDir := statErr == nil && info.IsDir()
		got := matcher.Match(rel, isDir)
		if want := gitIgnored[rel]; got != want {
			t.Errorf("路径 %s：我们判定 ignored=%v，git 判定 ignored=%v", rel, got, want)
		}
	}
}

// gitCheckIgnore 返回 git 认为被忽略的路径集合。
func gitCheckIgnore(t *testing.T, dir string, candidates []string) map[string]bool {
	t.Helper()

	cmd := exec.Command("git", "-C", dir, "check-ignore", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(candidates, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		// exit code 1 表示没有任何路径被忽略，这是正常结果。
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("git check-ignore: %v\n%s", err, out)
		}
	}

	ignored := make(map[string]bool, len(candidates))
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ignored[filepath.ToSlash(line)] = true
		}
	}
	return ignored
}

// TestIgnoreMatcherBasics 覆盖不依赖 git 的基本语义（含目录前缀继承）。
func TestIgnoreMatcherBasics(t *testing.T) {
	matcher, err := parseIgnorePatterns([]string{
		"*.log",
		"!keep.log",
		"build/",
		"/root.txt",
	})
	if err != nil {
		t.Fatalf("parseIgnorePatterns: %v", err)
	}

	tests := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{rel: "a.log", want: true},
		{rel: "keep.log", want: false},
		{rel: "sub/deep/a.log", want: true},
		{rel: "build", isDir: true, want: true},
		{rel: "build", want: false}, // 目录模式不匹配同名文件
		{rel: "build/out/x.txt", want: true},
		{rel: "sub/build/x.txt", want: true},
		{rel: "root.txt", want: true},
		{rel: "sub/root.txt", want: false},
	}
	for _, test := range tests {
		if got := matcher.Match(test.rel, test.isDir); got != test.want {
			t.Errorf("Match(%q, isDir=%v) = %v，期望 %v", test.rel, test.isDir, got, test.want)
		}
	}

	t.Run("非法模式报错", func(t *testing.T) {
		if _, err := parseIgnorePatterns([]string{"a["}); err == nil {
			t.Fatal("非法模式应当报错")
		}
	})
}

// TestIgnoreMatcherEmpty 覆盖没有规则时不忽略任何东西。
func TestIgnoreMatcherEmpty(t *testing.T) {
	var matcher *ignoreMatcher
	if matcher.Match("a.log", false) {
		t.Fatal("nil 匹配器不应忽略任何路径")
	}

	empty, err := parseIgnorePatterns([]string{"", "# 只有注释"})
	if err != nil {
		t.Fatalf("parseIgnorePatterns: %v", err)
	}
	if empty.Match("a.log", false) {
		t.Fatal("空规则集不应忽略任何路径")
	}
}
