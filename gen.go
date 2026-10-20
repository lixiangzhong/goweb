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
)

// GenOptions 描述静态产物的输出位置与清理策略。
type GenOptions struct {
	// Out 是产物目录，相对路径按当前工作目录解析。
	Out string
	// Clean 表示生成前清空产物目录。
	Clean bool
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
// 任一步失败即整体失败，不做静默跳过，避免半成品产物被当成成功发布。
func Generate(site *Site, opts GenOptions) (GenResult, error) {
	var result GenResult

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

	for _, name := range site.Pages() {
		body, err := site.Render(name)
		if err != nil {
			return result, fmt.Errorf("渲染页面 %s: %w", name, err)
		}
		// 产物严格跟随源码路径：about.html -> dist/about.html，
		// help/74.html -> dist/help/74.html。
		target := filepath.Join(out, filepath.FromSlash(name+pageExt))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return result, fmt.Errorf("创建页面目录 %q: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return result, fmt.Errorf("写入页面 %q: %w", target, err)
		}
		result.Pages++
	}

	assets, err := copyAssets(site, out, logger)
	if err != nil {
		return result, err
	}
	result.Assets = assets
	return result, nil
}

// logger 返回可用的日志器，未配置时丢弃日志。
func (o GenOptions) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// guardOutDir 拦截会破坏源码的产物目录：页面目录本身、页面目录的祖先、
// 局部模板目录，以及文件系统根目录与用户主目录。
func guardOutDir(site *Site, out string) error {
	root := site.Root()
	switch {
	case out == root:
		return fmt.Errorf("产物目录不能是页面目录本身：%s", out)
	case isAncestor(out, root):
		return fmt.Errorf("产物目录不能是页面目录的父目录：%s", out)
	case site.WithinTemplateDir(out):
		return fmt.Errorf("产物目录不能是局部模板目录：%s", out)
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

// copyAssets 把页面与局部模板之外的文件原样拷贝到产物目录，保持相对路径不变。
// 页面 html 由模板渲染产出，因此不参与拷贝。
func copyAssets(site *Site, out string, logger *slog.Logger) (int, error) {
	root := site.Root()
	outInRoot := out == root || strings.HasPrefix(out, root+string(filepath.Separator))
	count := 0

	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if site.skipPath(filePath, entry.IsDir()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if outInRoot && (filePath == out || strings.HasPrefix(filePath, out+string(filepath.Separator))) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return fmt.Errorf("计算相对路径 %s: %w", filePath, err)
		}
		if site.isTemplateFile(rel) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(filePath), pageExt) {
			return nil
		}

		target := filepath.Join(out, rel)
		if entry.Type()&fs.ModeSymlink != 0 {
			isDir, err := symlinkToDir(filePath)
			if err != nil {
				return err
			}
			if isDir {
				logger.Warn("跳过指向目录的符号链接", "path", rel)
				return nil
			}
		}
		if err := copyFile(filePath, target); err != nil {
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
func copyFile(src, dst string) error {
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
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("写入 %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("关闭 %s: %w", dst, err)
	}
	return nil
}
