package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
)

// GenOptions 描述静态产物的输出位置与清理策略。
type GenOptions struct {
	// Out 是产物目录，相对路径按当前工作目录解析。
	Out string
	// Clean 表示生成前清空产物目录（校验模式不做任何清理）。
	Clean bool
	// Minify 表示压缩页面与可压缩的资源（HTML/CSS/JS/JSON/SVG/XML）；其余文件逐字节拷贝。
	Minify bool
	// Check 为真时进入校验模式：不写、不清空任何产物，并尽量收集全部问题
	// （页面渲染失败、产物路径冲突、护栏错误）而不是遇到第一个就返回。check 子命令用它。
	Check bool
	// OnPage 在每一页渲染（含压缩）完成后调用，用于检查渲染结果（例如站内链接）。
	// 返回的错误按当前模式处理：校验模式收集、生成模式立即失败。可为 nil。
	OnPage func(name string, body []byte) error
	// Logger 用于记录跳过项；为空时静默。
	Logger *slog.Logger
}

// GenResult 汇总一次生成/校验的产物数量。
type GenResult struct {
	// Out 是产物目录的绝对路径。
	Out    string
	Pages  int
	Assets int
}

// Generate 把站点渲染为纯静态产物：页面按目录式落盘，其余文件原样拷贝。
// 开启 Minify 时页面与可压缩资源会被压缩（只去空白与注释，不改 JS 变量名）。
// 默认任一步失败即整体失败，不做静默跳过，避免半成品产物被当成成功发布；
// Check 为真时改为「不落盘 + 收集全部问题」。
func Generate(site *Site, opts GenOptions) (GenResult, error) {
	var result GenResult

	out, err := filepath.Abs(opts.Out)
	if err != nil {
		return result, fmt.Errorf("解析产物目录 %q: %w", opts.Out, err)
	}
	result.Out = out

	run := &generateRun{
		site:      site,
		out:       out,
		logger:    opts.logger(),
		check:     opts.Check,
		onPage:    opts.OnPage,
		artifacts: make(map[string]string),
	}
	// 压缩器按需构造一次：关闭压缩时为 nil，代码路径只有「原样拷贝」。
	if opts.Minify {
		run.minifier = newMinifier()
	}

	if err := guardOutDir(site, out); err != nil {
		if stop := run.record(err); stop != nil {
			return result, stop
		}
	}
	// 产物目录落在源码根内部时要被清空：默认 dist 是约定俗成的位置，不打扰；
	// 其它位置（例如误写 --out static）很可能落在源码上，清空前给一条明确的警告。
	if !opts.Check && opts.Clean && out != filepath.Join(site.Root(), defaultOutDir) {
		if withinDir(site.PagesRoot(), out) || withinDir(site.AssetsRoot(), out) {
			run.logger.Warn("产物目录位于页面根/资源根内，清空会删除其中原有的非产物文件",
				"out", out)
		}
	}
	if !opts.Check {
		if opts.Clean {
			if err := cleanDir(out); err != nil {
				return result, err
			}
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			return result, fmt.Errorf("创建产物目录 %q: %w", out, err)
		}
	}

	run.writePages()
	if run.fatal != nil {
		return result, run.fatal
	}
	run.copyAssets()
	if run.fatal != nil {
		return result, run.fatal
	}

	result.Pages, result.Assets = run.pages, run.assets
	if len(run.problems) > 0 {
		return result, errors.Join(run.problems...)
	}
	return result, nil
}

// generateRun 持有一次生成/校验的共享状态，让「写页面」与「拷资源」两条路径共用同一套
// 冲突登记与问题收集逻辑，只在真正落盘的那几行分叉。
type generateRun struct {
	site      *Site
	out       string
	minifier  *minify.M
	logger    *slog.Logger
	check     bool
	onPage    func(name string, body []byte) error
	artifacts map[string]string

	problems []error
	// fatal 是生成模式下的第一个错误：记下来让调用方尽快退出。
	fatal  error
	pages  int
	assets int
}

// record 登记一个问题。校验模式收集后继续（返回 nil），生成模式记下致命错误并返回它，
// 让调用方立即停止——生成不能产出半成品，校验则要一次把问题都列出来。
func (r *generateRun) record(err error) error {
	r.problems = append(r.problems, err)
	if r.check {
		return nil
	}
	r.fatal = err
	return err
}

// writePages 渲染全部页面：生成模式落盘，校验模式只渲染并交给 OnPage 检查。
func (r *generateRun) writePages() {
	for _, name := range r.site.Pages() {
		source := r.site.pageFile(name)
		// 忽略规则只作用于产出：被忽略的页面本地可见（serve 照常），但不进产物。
		if source != "" && r.site.ignoredForOutput(source, false) {
			r.logger.Info("跳过被忽略的页面", "page", name, "source", source)
			continue
		}
		body, err := r.renderPage(name)
		if err != nil {
			if stop := r.record(err); stop != nil {
				return
			}
			continue
		}
		// 页面产物跟随页面根：about.html -> dist/about.html。
		rel := filepath.FromSlash(name + pageExt)
		if err := recordArtifact(r.artifacts, rel, source); err != nil {
			if stop := r.record(err); stop != nil {
				return
			}
			continue
		}
		if !r.check {
			if err := writePageFile(r.out, rel, body); err != nil {
				if stop := r.record(fmt.Errorf("页面 %s: %w", name, err)); stop != nil {
					return
				}
				continue
			}
		}
		r.pages++
	}
}

// renderPage 渲染单页：执行模板 → 压缩（开启时）→ 交给 OnPage 检查渲染结果。
func (r *generateRun) renderPage(name string) ([]byte, error) {
	body, err := r.site.Render(name)
	if err != nil {
		return nil, fmt.Errorf("渲染页面 %s: %w", name, err)
	}
	// 压缩在渲染与资源版本号改写之后进行：先保证 URL 已经定稿，再去空白与注释。
	if r.minifier != nil {
		if body, err = minifyBytes(r.minifier, mediaTypeHTML, body); err != nil {
			return nil, fmt.Errorf("压缩页面 %s: %w", name, err)
		}
	}
	if r.onPage != nil {
		if err := r.onPage(name, body); err != nil {
			return nil, fmt.Errorf("检查页面 %s: %w", name, err)
		}
	}
	return body, nil
}

// writePageFile 把一个页面原子地写进产物目录：
// 先写同目录临时文件再 rename，避免写入失败或进程中断留下半个页面。
func writePageFile(out, rel string, body []byte) error {
	target := filepath.Join(out, rel)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建页面目录 %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".goweb-page-*")
	if err != nil {
		return fmt.Errorf("创建临时页面 %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if success {
			return
		}
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(body); err != nil {
		return fmt.Errorf("写入页面 %q: %w", target, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("设置页面权限 %q: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭页面 %q: %w", target, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("落盘页面 %q: %w", target, err)
	}
	success = true
	return nil
}

// recordArtifact 登记一个产物路径，撞车时直接报错（列出两个来源）。
// 错误保持单行：check 会按行列出全部问题，多行错误会被拆成多条。
func recordArtifact(artifacts map[string]string, rel, source string) error {
	normalized := filepath.ToSlash(filepath.Clean(rel))
	if previous, ok := artifacts[normalized]; ok {
		return fmt.Errorf("产物路径冲突：%s（来源一：%s；来源二：%s），请调整页面根/资源根，或用 ignore 排除其中一个",
			normalized, previous, source)
	}
	artifacts[normalized] = source
	return nil
}

// logger 返回可用的日志器，未配置时丢弃日志。
func (o GenOptions) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// guardOutDir 拦截会破坏源码的产物目录：项目根、三个根本身或它们的父目录、
// 模板根内部、符号链接，以及用户主目录。
//
// 比较前先解析路径里的符号链接：`dist -> ../somewhere` 这类写法很常见，
// 不解析的话「清空 dist」实际会删掉链接目标里的内容。
func guardOutDir(site *Site, out string) error {
	if info, err := os.Lstat(out); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("产物目录不能是符号链接：%s（请直接指定真实目录）", out)
	}

	outReal := canonicalPath(out)
	rootReal := canonicalPath(site.Root())
	switch {
	case outReal == rootReal:
		return fmt.Errorf("产物目录不能是项目根：%s", out)
	case isAncestor(outReal, rootReal):
		return fmt.Errorf("产物目录不能是项目根的父目录：%s", out)
	}
	for _, root := range []struct {
		name string
		path string
	}{
		{name: "页面根", path: site.PagesRoot()},
		{name: "模板根", path: site.TemplateRoot()},
		{name: "资源根", path: site.AssetsRoot()},
	} {
		if root.path == "" {
			continue
		}
		rootReal := canonicalPath(root.path)
		if outReal == rootReal {
			return fmt.Errorf("产物目录不能是%s：%s", root.name, out)
		}
		if isAncestor(outReal, rootReal) {
			return fmt.Errorf("产物目录不能是%s的父目录：%s", root.name, out)
		}
		if root.name == "模板根" && withinDir(rootReal, outReal) {
			return fmt.Errorf("产物目录不能位于模板根内（模板根只读、不产出）：%s", out)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && outReal == canonicalPath(home) {
		return fmt.Errorf("拒绝把用户主目录当作产物目录：%s", out)
	}
	return nil
}

// canonicalPath 解析路径中的符号链接；路径本身不存在时解析父目录再拼回文件名，
// 保证「刚创建前的产物目录」与已存在的项目根可以按同一口径比较。
func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolved, filepath.Base(path))
	}
	return filepath.Clean(path)
}

// isAncestor 判断 dir 是否为 target 的祖先目录。
func isAncestor(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cleanDir 清空目录内容但保留目录本身；目录不存在时直接返回。
// 符号链接一律拒绝：清空的应当是产物目录本身，而不是链接目标。
func cleanDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取产物目录 %q: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("产物目录是符号链接，拒绝清空：%s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("产物目录不是目录：%s", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读取产物目录 %q: %w", dir, err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("清理产物 %q: %w", entry.Name(), err)
		}
	}
	return nil
}

// copyAssets 把页面根与资源根下、页面与模板之外的文件拷进产物。
//
// 遍历页面根 ∪ 资源根（去掉被包含的根，默认布局下整棵树只走一遍），产物路径按
// 「所属根」决定：位于页面根内的文件按页面根相对路径（这样页面里的相对引用才对得上），
// 其余按资源根相对路径。没有页面的纯资源根（例如 --assets 指向别处）也会被发布。
// 被忽略规则排除的文件不进产物，并记录日志。
// 生成模式下按扩展名可压缩的文件（CSS/JS/JSON/SVG/XML）会压缩后写出；校验模式只检查不写。
func (r *generateRun) copyAssets() {
	for _, root := range r.site.sourceRoots() {
		err := r.site.walkRoot(root, func(filePath string, entry fs.DirEntry) error {
			if skip, skipDir := r.skipAssetEntry(filePath, entry); skip {
				if skipDir {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			return r.copyAsset(filePath, entry)
		})
		if err != nil {
			// 遍历本身出错（权限、I/O）：生成模式立即失败，校验模式记下继续。
			_ = r.record(err)
		}
	}
}

// skipAssetEntry 判断资源遍历是否跳过该条目：产物目录本身与模板根。
// 隐藏项、node_modules 与排除目录已由 Site.walkRoot 统一处理。
// skipDir 表示连整个目录一起跳过。
func (r *generateRun) skipAssetEntry(filePath string, entry fs.DirEntry) (skip, skipDir bool) {
	// 产物目录本身不参与拷贝（它落在源码根里时，避免把上一轮产物当资源再拷一遍）。
	if withinDir(r.out, filePath) {
		return true, entry.IsDir()
	}
	// 模板根的内容不进产物。
	if r.site.withinTemplateRoot(filePath) {
		return true, entry.IsDir()
	}
	return false, false
}

// copyAsset 处理单个文件：算出产物路径 → 过滤（页面产物/忽略项/软链接）→ 计数，
// 生成模式再拷贝或压缩写出。
func (r *generateRun) copyAsset(filePath string, entry fs.DirEntry) error {
	rel, skipPageHTML, err := r.site.artifactRel(filePath)
	if err != nil {
		if stop := r.record(err); stop != nil {
			return stop
		}
		return nil
	}
	if skipPageHTML {
		return nil
	}
	if r.site.ignoredForOutput(filePath, false) {
		r.logger.Info("跳过被忽略的文件", "file", filepath.ToSlash(rel))
		return nil
	}
	if entry.Type()&fs.ModeSymlink != 0 && !r.allowSymlinkFile(filePath, rel) {
		return nil
	}
	if err := recordArtifact(r.artifacts, rel, filePath); err != nil {
		if stop := r.record(err); stop != nil {
			return stop
		}
		return nil
	}
	if !r.check {
		if err := copyFile(filePath, filepath.Join(r.out, rel), r.minifier, minifyMediaType(filePath)); err != nil {
			if stop := r.record(fmt.Errorf("资源 %s: %w", filepath.ToSlash(rel), err)); stop != nil {
				return stop
			}
			return nil
		}
	}
	r.assets++
	return nil
}

// allowSymlinkFile 决定符号链接文件能否进产物：只有指向三个根之内的普通文件才允许，
// 与 serve 的规则一致；指向目录、站点根之外或已失效的链接一律跳过并警告——
// 一个无关的坏链接不该让整次生成失败。
func (r *generateRun) allowSymlinkFile(filePath, rel string) bool {
	target, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		r.logger.Warn("跳过失效的符号链接", "path", filepath.ToSlash(rel))
		return false
	}
	info, err := os.Stat(target)
	if err != nil {
		r.logger.Warn("跳过无法读取的符号链接", "path", filepath.ToSlash(rel), "err", err)
		return false
	}
	switch {
	case info.IsDir():
		r.logger.Warn("跳过指向目录的符号链接", "path", filepath.ToSlash(rel))
		return false
	case !r.site.withinAnyRootResolved(target):
		r.logger.Warn("跳过指向站点根之外的符号链接", "path", filepath.ToSlash(rel), "target", target)
		return false
	}
	return true
}

// copyFile 拷贝单个文件到目标路径。
// minifier 非空且 mediaType 非空时改为「流式压缩写入」，其余情况逐字节原样拷贝。
// 任何一步失败都会删掉目标文件，避免半个文件留在产物里被当成正常产物。
func copyFile(src, dst string, minifier *minify.M, mediaType string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", src, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("创建目录 %s: %w", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("创建 %s: %w", dst, err)
	}
	success := false
	defer func() {
		if success {
			return
		}
		_ = out.Close()
		_ = os.Remove(dst)
	}()

	if minifier != nil && mediaType != "" {
		if err := minifier.Minify(mediaType, out, in); err != nil {
			return fmt.Errorf("压缩 %s: %w", src, err)
		}
	} else if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("写入 %s: %w", dst, err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("关闭 %s: %w", dst, err)
	}
	success = true
	return nil
}
