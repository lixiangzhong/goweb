package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// defaultConfigTemplate 是 `goweb init` 生成的配置：每个键都写出当前默认值，并逐行注释。
//
// 键的集合必须与 fileConfig 的 yaml 标签一致（有测试锁定），否则以后新增配置项时
// 模板会静默漏掉，用户看到的「全部可配项」就变成了假话。
//
// 唯一的例外是 addr：它写成注释行。显式写 addr（哪怕值就是默认的 ":8080"）会被视为
// 「我就要这个端口」，从而关掉「默认端口被占用时自动递增」——生成的配置不该悄悄改变
// 这个行为，所以这里只把它作为可选项列出。
const defaultConfigTemplate = `# goweb 配置：不写任何命令行参数也能跑（goweb dev / goweb build）
# 键名与命令行参数同名；优先级：命令行参数 > 本文件 > 内置默认
# 未知字段会直接报错（避免拼错键名后静默无效）

# 页面根：页面发现与页面路径的基准（相对项目根）
dir: "."

# 模板根：局部模板与 data.json；不受 dir 影响，置空表示没有独立模板根
template: templates

# 资源根：其余文件按它映射到产物（相对项目根）
assets: "."

# gen/build 的产物目录
out: dist

# 产物压缩：只去空白与注释，不改 JS 变量名（serve/dev 始终不压缩）
minify: true

# 渲染模式：auto（serve/dev = dev，gen/build = prod）| dev | prod
mode: auto

# 模板里的构建时间；留空则取站点内容里最新的文件修改时间（同样输入 → 同样产出）
build-time: ""

# 给页面里的站内资源引用自动补 ?v=<内容哈希>
fingerprint: true

# serve/dev 注入热重载脚本（关闭后页面与产物完全一致）
livereload: true

# serve/dev 启动后自动打开浏览器（打不开只记日志，不影响服务）
open: true

# 读项目根的 .gitignore（忽略规则只作用于 gen/build，serve/dev 仍能看到被忽略的文件）
gitignore: true

# 额外的忽略模式，与 .gitignore 叠加，例如：
#   ignore:
#     - "**/*.map"
#     - "drafts/"
ignore: []

# 监听地址，默认 ":8080"。这里刻意写成注释：显式写 addr 会被视为「我就要这个端口」，
# 被占用时直接报错；留空则默认端口被占用会自动递增到 :8081…:8089。
# addr: ":8080"
`

// initOptions 是 init 的参数。
type initOptions struct {
	// force 为真时覆盖已存在的配置文件。
	force bool
}

// runInit 生成默认配置文件。
//
// 已存在时直接报错退出（不碰用户的文件），要覆盖得显式加 --force。
// 目标路径：默认是当前目录下的 .goweb.yml；若从子目录运行且项目根已有配置，
// 则报错而不是生成第二份嵌套配置（避免出现两个真相源）。
func runInit(cmd *cobra.Command, globals *globalOptions, opts initOptions) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取运行目录: %w", err)
	}

	target := strings.TrimSpace(globals.config)
	switch {
	case target != "":
		if !filepath.IsAbs(target) {
			target = filepath.Join(workingDir, target)
		}
	case true:
		existing, err := findConfigFile(workingDir)
		if err != nil {
			return err
		}
		if existing != "" {
			target = existing
		} else {
			target = filepath.Join(workingDir, configFileName)
		}
	}
	target = filepath.Clean(target)

	if _, err := os.Stat(target); err == nil && !opts.force {
		return fmt.Errorf("配置文件已存在：%s\n要覆盖请加 --force，或换个路径：goweb init --config <路径>", target)
	}
	if err := os.WriteFile(target, []byte(defaultConfigTemplate), 0o644); err != nil {
		return fmt.Errorf("写入配置文件 %s: %w", target, err)
	}

	newLogger(cmd.ErrOrStderr()).Info("已生成默认配置", "file", target, "force", opts.force)
	fmt.Fprintf(cmd.OutOrStdout(), "已生成 %s\n下一步：goweb dev（起开发服务器）或 goweb build（产出静态站）\n", target)
	return nil
}
