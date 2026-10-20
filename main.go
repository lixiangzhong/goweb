package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

const (
	// defaultAddr 是开发服务器默认监听地址。
	defaultAddr = ":8080"
	// defaultPageDir 是默认页面目录，即运行目录。
	defaultPageDir = "."
	// defaultTemplateDir 是默认局部模板目录，相对页面目录。
	defaultTemplateDir = "template"
	// defaultOutDir 是 gen 的默认产物目录。
	defaultOutDir = "dist"
)

// serveOptions 是 serve 子命令的参数。
type serveOptions struct {
	addr        string
	dir         string
	templateDir string
}

// genOptions 是 gen 子命令的参数。
type genOptions struct {
	dir         string
	templateDir string
	out         string
	clean       bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "goweb:", err)
		os.Exit(1)
	}
}

// run 解析子命令并分发；不带子命令时默认启动开发服务器。
func run(args []string) error {
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}

	switch command {
	case "serve":
		return runServe(args)
	case "gen":
		return runGen(args)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("未知子命令 %q，可用：serve、gen", command)
	}
}

// runServe 启动开发服务器：目录式路由 + 模板渲染 + 静态文件直出，并在模板变更后重编译。
func runServe(args []string) error {
	var opts serveOptions
	flags := serveFlagSet(&opts, os.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	logger := newLogger()
	site, err := LoadSite(SiteOptions{
		Dir:         opts.dir,
		TemplateDir: opts.templateDir,
		ExcludeDirs: []string{defaultOutDir},
	})
	if err != nil {
		return err
	}

	stopWatch, err := watchSite(site, logger)
	if err != nil {
		return err
	}
	defer stopWatch()

	server := &http.Server{
		Addr:              opts.addr,
		Handler:           newHandler(site, logger),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	logger.Info("goweb 开发服务器已启动",
		"addr", opts.addr,
		"dir", site.Root(),
		"pages", len(site.Pages()),
	)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// runGen 渲染全部页面并产出纯静态站。
func runGen(args []string) error {
	var opts genOptions
	flags := genFlagSet(&opts, os.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	logger := newLogger()
	site, err := LoadSite(SiteOptions{
		Dir:         opts.dir,
		TemplateDir: opts.templateDir,
		ExcludeDirs: []string{opts.out},
	})
	if err != nil {
		return err
	}

	result, err := Generate(site, GenOptions{Out: opts.out, Clean: opts.clean, Logger: logger})
	if err != nil {
		return err
	}
	logger.Info("静态站已生成",
		"out", result.Out,
		"pages", result.Pages,
		"assets", result.Assets,
	)
	return nil
}

// serveFlagSet 声明 serve 子命令的参数。
func serveFlagSet(opts *serveOptions, output io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.addr, "addr", defaultAddr, "监听地址")
	flags.StringVar(&opts.dir, "dir", defaultPageDir, "页面目录")
	flags.StringVar(&opts.templateDir, "template", defaultTemplateDir, "局部模板目录（相对页面目录，空表示没有）")
	return flags
}

// genFlagSet 声明 gen 子命令的参数。
func genFlagSet(opts *genOptions, output io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("gen", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.dir, "dir", defaultPageDir, "页面目录")
	flags.StringVar(&opts.templateDir, "template", defaultTemplateDir, "局部模板目录（相对页面目录，空表示没有）")
	flags.StringVar(&opts.out, "out", defaultOutDir, "产物目录")
	flags.BoolVar(&opts.clean, "clean", true, "生成前清空产物目录")
	return flags
}

// newLogger 构造开发期日志器，输出到标准错误。
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// printUsage 输出总览帮助，含两个子命令的参数说明。
func printUsage(w io.Writer) {
	fmt.Fprint(w, usageText)

	var serveOpts serveOptions
	fmt.Fprint(w, "\nserve 参数（goweb serve -h）：\n")
	serveFlagSet(&serveOpts, w).PrintDefaults()

	var genOpts genOptions
	fmt.Fprint(w, "\ngen 参数（goweb gen -h）：\n")
	genFlagSet(&genOpts, w).PrintDefaults()
}

const usageText = `goweb —— 面向前端开发的静态站服务器与静态化工具

用法：
  goweb [serve] [参数]    启动开发服务器（默认监听 :8080）
  goweb gen [参数]        渲染模板并产出纯静态站
  goweb help              显示本帮助

示例：
  cd 站点目录
  goweb                       # 浏览器打开 http://localhost:8080/
  goweb gen -out dist         # 产出可直接部署的静态站到 dist/

目录约定：
  页面目录（-dir，默认 .）中递归出现的 *.html 都是页面：
    index.html    -> /
    about.html    -> /about/
    help/74.html  -> /help/74/
  局部模板目录（-template，默认 template）里的 html 只参与模板解析，
  不产出页面、也不能被直接访问。

模板写法：
  页面与局部模板共用同一个 html/template 命名空间：
    局部模板 template/header.html：{{define "header"}}...{{end}}
    页面 about.html：{{template "header" .}}
  内置模板函数：
    {{"<b>x</b>"|HTML}}   原样输出为 HTML
    {{JS "x"}}            编码为 JSON 字面量
  扫描时忽略：点号开头的文件/目录、node_modules、产物目录（dist）。

URL 规则（与 gen 产物布局严格一致）：
  /、/about/、/help/74/ 是唯一 URL；/about、/about.html、/index.html
  一律 301 到目录 URL；未命中页面时按磁盘真实文件直出；
  其余返回纯文本 404（无自定义 404 约定、无目录列表）。
  模板变更后自动重编译，但不注入热刷新脚本，需要手动刷新浏览器。
`
