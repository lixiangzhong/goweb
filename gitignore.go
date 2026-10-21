package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// gitignoreFileName 是项目根下读取的忽略文件。
const gitignoreFileName = ".gitignore"

// ignoreMatcher 实现 .gitignore 的一个明确子集：
// 支持 `*` `?` `**` `[...]`、`!` 反选、前导 `/` 锚定、结尾 `/` 目录限定、
// `#` 注释与空行。
// 不支持：嵌套 .gitignore、core.excludesFile、反斜杠转义、行尾空格转义（README 里写明）。
type ignoreMatcher struct {
	patterns []ignorePattern
}

// ignorePattern 是一条编译后的忽略规则。
type ignorePattern struct {
	// pattern 是 doublestar 模式（已去掉前导 `/` 与结尾 `/`）。
	pattern string
	// negate 表示 `!` 反选（重新包含）。
	negate bool
	// dirOnly 表示只匹配目录（结尾 `/`）。
	dirOnly bool
	// anchored 表示模式相对项目根锚定（含 `/` 或以 `/` 开头）；否则按任意层级的名字匹配。
	anchored bool
}

// loadIgnoreMatcher 读取项目根的 .gitignore 与配置里的额外模式。
// useGitignore 为 false 时只使用 extra。
func loadIgnoreMatcher(projectRoot string, useGitignore bool, extra []string) (*ignoreMatcher, error) {
	var lines []string

	if useGitignore {
		gitignorePath := filepath.Join(projectRoot, gitignoreFileName)
		body, err := os.ReadFile(gitignorePath)
		switch {
		case err == nil:
			lines = append(lines, strings.Split(string(body), "\n")...)
		case errors.Is(err, fs.ErrNotExist):
			// 没有 .gitignore 属于正常情况。
		default:
			return nil, fmt.Errorf("读取 %s: %w", gitignorePath, err)
		}
	}
	lines = append(lines, extra...)

	return parseIgnorePatterns(lines)
}

// parseIgnorePatterns 把若干行模式编译成匹配器。
func parseIgnorePatterns(lines []string) (*ignoreMatcher, error) {
	matcher := &ignoreMatcher{}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		pattern := ignorePattern{}
		if strings.HasPrefix(line, "!") {
			pattern.negate = true
			line = strings.TrimPrefix(line, "!")
		}
		if strings.HasPrefix(line, "/") {
			pattern.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if strings.HasSuffix(line, "/") {
			pattern.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if line == "" {
			continue
		}
		if strings.Contains(line, "/") {
			pattern.anchored = true
		}
		if !doublestar.ValidatePattern(line) {
			return nil, fmt.Errorf("无法识别的忽略模式 %q", raw)
		}

		pattern.pattern = line
		matcher.patterns = append(matcher.patterns, pattern)
	}
	return matcher, nil
}

// Match 判断项目根相对路径（斜杠分隔）是否被忽略。
//
// 按 git 语义逐级判断：祖先目录被排除时，其下内容不能再被 `!` 重新包含。
func (m *ignoreMatcher) Match(rel string, isDir bool) bool {
	if m == nil || len(m.patterns) == 0 {
		return false
	}
	cleaned := path.Clean(strings.TrimPrefix(filepath.ToSlash(rel), "/"))
	if cleaned == "" || cleaned == "." {
		return false
	}

	segments := strings.Split(cleaned, "/")

	// 祖先目录（不含自身）被忽略 → 直接忽略，且不允许反选救回。
	for i := 0; i < len(segments)-1; i++ {
		if verdict, matched := m.matchOne(strings.Join(segments[:i+1], "/"), true); matched && verdict {
			return true
		}
	}

	verdict, matched := m.matchOne(cleaned, isDir)
	return matched && verdict
}

// matchOne 判断单个路径自身的判定结果：最后一个命中的模式说了算。
func (m *ignoreMatcher) matchOne(rel string, isDir bool) (verdict bool, matched bool) {
	for _, pattern := range m.patterns {
		if pattern.dirOnly && !isDir {
			continue
		}
		if !pattern.matches(rel) {
			continue
		}
		verdict, matched = !pattern.negate, true
	}
	return verdict, matched
}

// matches 判断模式是否命中某个路径。
func (p ignorePattern) matches(rel string) bool {
	candidate := rel
	if !p.anchored {
		candidate = path.Base(rel)
	}
	if matched, err := doublestar.Match(p.pattern, candidate); err == nil && matched {
		return true
	}
	// `**/foo` 这类模式在 git 里也能命中根下的 foo（`**` 可匹配零层）。
	if strings.HasPrefix(p.pattern, "**/") {
		if matched, err := doublestar.Match(strings.TrimPrefix(p.pattern, "**/"), rel); err == nil && matched {
			return true
		}
	}
	return false
}
