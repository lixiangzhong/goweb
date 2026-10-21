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
	// Clean 表示生成前清空产物目录。
	Clean bool
	// Minify 表示压缩页面与可压缩的资源（HTML/CSS/JS/JSON/SVG/XML）；其余文件逐字节拷贝。
	Minify bool
	// Logger 用于记录跳过项；为空时静默。
	Logger *slog.Logger
}

// GenResult 汇总一次生成的产物数量。
type GenResult struct {
	// Out 是产物目录的绝对路径。
	Out    string
	Pages  int
	Assets int
}

// Generate 把站点渲染为纯静态产物：页面按目录式落盘，其余文件原样拷贝。
// 开启 Minify 时页面与可压缩资源会被压缩（只去空白与注释，不改 JS 变量名）。
// 任一步失败即整体失败，不做静默跳过，避免半成品产物被当成成功发布。
func Generate(site *Site, opts GenOptions) (GenResult, error) {
	var result GenResult

	// 压缩器按需构造一次：关闭压缩时为 nil，代码路径只有「原样拷贝」。
	var minifier *minify.M
	if opts.Minify {
		minifier = newMinifier()
	}

	out, err := filepath.Abs(opts.Out)
	if err != nil {
		return result, fmt.Errorf("解析产物目录 %q: %w", opts.Out, err)
	}
	if err := guardOutDir(site, out); err != nil {
		return result, err
	}
	result.Out = out

	if opts.Clean {
		if err := cleanDir(out); err != nil {
			return result, err
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return result, fmt.Errorf("创建产物目录 %q: %w", out, err)
	}

	logger := opts.logger()

	// artifacts 记录产物路径 -> 来源，用于发现页面与资源撞同一路径。
	artifacts := make(map[string]string)

	for _, name := range site.Pages() {
		source := site.pageFile(name)
		// 忽略规则只作用于 gen：被忽略的页面本地可见（serve 照常），但不进产物。
		if source != "" && site.ignoredForOutput(source, false) {
			logger.Info("跳过被忽略的页面", "page", name, "source", source)
			continue
		}
		body, err := site.Render(name)
		if err != nil {
			return result, fmt.Errorf("渲染页面 %s: %w", name, err)
		}
		// 压缩在渲染与资源版本号改写之后进行：先保证 URL 已经定稿，再去空白与注释。
		if minifier != nil {
			if body, err = minifyBytes(minifier, mediaTypeHTML, body); err != nil {
				return result, fmt.Errorf("压缩页面 %s: %w", name, err)
			}
		}
		// 页面产物跟随页面根：about.html -> dist/about.html。
		rel := filepath.FromSlash(name + pageExt)
		target := filepath.Join(out, rel)
		if err := recordArtifact(artifacts, rel, source); err != nil {
			return result, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return result, fmt.Errorf("创建页面目录 %q: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return result, fmt.Errorf("写入页面 %q: %w", target, err)
		}
		result.Pages++
	}

	assets, err := copyAssets(site, out, artifacts, minifier, logger)
	if err != nil {
		return result, err
	}
	result.Assets = assets
	return result, nil
}

// recordArtifact 登记一个产物路径，撞车时直接报错（列出两个来源）。
func recordArtifact(artifacts map[string]string, rel, source string) error {
	normalized := filepath.ToSlash(filepath.Clean(rel))
	if previous, ok := artifacts[normalized]; ok {
		return fmt.Errorf("产物路径冲突：%s\n  来源一：%s\n  来源二：%s\n"+
			"请调整页面根/资源根，或用 .goweb.yml 的 ignore 排除其中一个", normalized, previous, source)
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

// guardOutDir 拦截会破坏源码的产物目录：项目根、三个根本身或它们的父目录，
// 以及用户主目录。
func guardOutDir(site *Site, out string) error {
	switch {
	case out == site.Root():
		return fmt.Errorf("产物目录不能是项目根：%s", out)
	case isAncestor(out, site.Root()):
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
		if out == root.path {
			return fmt.Errorf("产物目录不能是%s：%s", root.name, out)
		}
		if isAncestor(out, root.path) {
			return fmt.Errorf("产物目录不能是%s的父目录：%s", root.name, out)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && out == filepath.Clean(home) {
		return fmt.Errorf("拒绝把用户主目录当作产物目录：%s", out)
	}
	return nil
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
func cleanDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取产物目录 %q: %w", dir, err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("清理产物 %q: %w", entry.Name(), err)
		}
	}
	return nil
}

// copyAssets 把资源根下、页面与模板之外的文件拷进产物。
//
// 产物路径按「所属根」决定：位于页面根内的文件按页面根相对路径
// （这样页面里的相对引用才对得上），其余按资源根相对路径。
// 被忽略规则排除的文件不进产物，并记录日志。
// minifier 非空时，按扩展名可压缩的文件（CSS/JS/JSON/SVG/XML）会压缩后写出。
func copyAssets(site *Site, out string, artifacts map[string]string, minifier *minify.M, logger *slog.Logger) (int, error) {
	assetsRoot := site.AssetsRoot()
	outInAssets := withinDir(assetsRoot, out) || out == assetsRoot
	count := 0

	err := filepath.WalkDir(assetsRoot, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == assetsRoot {
			return nil
		}
		if site.skipPath(filePath, entry.IsDir()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// 产物目录本身不参与拷贝（它在资源根里时）。
		if outInAssets && withinDir(out, filePath) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// 模板根的内容不进产物。
		if site.WithinTemplateDir(filePath) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(assetsRoot, filePath)
		if err != nil {
			return fmt.Errorf("计算相对路径 %s: %w", filePath, err)
		}

		if entry.IsDir() {
			return nil
		}

		// 页面根里的 html 已由模板渲染产出，不再当资源拷贝；
		// 页面根外的 html 是普通文件，照原样拷贝。
		if withinDir(site.PagesRoot(), filePath) {
			if strings.EqualFold(filepath.Ext(filePath), pageExt) {
				return nil
			}
			// 页面根内的非 html 文件按页面根映射，保证与页面 URL 一致。
			if pageRel, err := filepath.Rel(site.PagesRoot(), filePath); err == nil {
				rel = pageRel
			}
		}

		if site.ignoredForOutput(filePath, false) {
			logger.Info("跳过被忽略的文件", "file", filepath.ToSlash(rel))
			return nil
		}

		if entry.Type()&fs.ModeSymlink != 0 {
			isDir, err := symlinkToDir(filePath)
			if err != nil {
				return err
			}
			if isDir {
				logger.Warn("跳过指向目录的符号链接", "path", filepath.ToSlash(rel))
				return nil
			}
		}

		if err := recordArtifact(artifacts, rel, filePath); err != nil {
			return err
		}
		if err := copyFile(filePath, filepath.Join(out, rel), minifier, minifyMediaType(filePath)); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return count, err
	}
	return count, nil
}

// symlinkToDir 判断符号链接是否指向目录。
func symlinkToDir(filePath string) (bool, error) {
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return false, fmt.Errorf("解析符号链接 %s: %w", filePath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return false, fmt.Errorf("读取符号链接目标 %s: %w", resolved, err)
	}
	return info.IsDir(), nil
}

// copyFile 拷贝单个文件到目标路径。
// minifier 非空且 mediaType 非空时改为「流式压缩写入」，其余情况逐字节原样拷贝。
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

	if minifier != nil && mediaType != "" {
		if err := minifier.Minify(mediaType, out, in); err != nil {
			out.Close()
			return fmt.Errorf("压缩 %s: %w", src, err)
		}
	} else if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("写入 %s: %w", dst, err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("关闭 %s: %w", dst, err)
	}
	return nil
}
