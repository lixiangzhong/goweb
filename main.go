package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

const (
	// defaultAddr 是开发服务器默认监听地址。
	defaultAddr = ":8080"
	// defaultPageDir 是默认页面目录，即运行目录。
	defaultPageDir = "."
	// defaultTemplateDir 是默认局部模板目录（同时是 data.json 所在目录），相对页面目录。
	defaultTemplateDir = "templates"
	// defaultOutDir 是 gen 的默认产物目录。
	defaultOutDir = "dist"
)

// version 是版本号，构建时可通过 -ldflags "-X main.version=v1.0.0" 注入。
var version = "dev"

// globalOptions 是根命令与各子命令共用的参数（persistent flag）。
type globalOptions struct {
	dir         string
	templateDir string
}

func main() {
	// 错误信息与 usage 由 cobra 打印（前缀 goweb:），这里只负责退出码。
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

// newRootCommand 组装整个 CLI。不带子命令时等价于 serve，因此根命令自带 --addr。
func newRootCommand() *cobra.Command {
	globals := &globalOptions{}
	var addr string
	var liveReload bool

	root := &cobra.Command{
		Use:     "goweb",
		Short:   "面向前端开发的静态站服务器与静态化工具",
		Long:    rootLongDescription,
		Example: rootExample,
		Version: version,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 参数已解析完成，运行期错误不再刷 usage。
			cmd.SilenceUsage = true
			return runServe(cmd, serveOptions{addr: addr, liveReload: liveReload, globals: globals})
		},
	}
	// 统一错误前缀（形如 goweb: ...），并与 usage 的输出顺序保持一致。
	root.SetErrPrefix("goweb:")
	addGlobalFlags(root, globals)
	root.Flags().StringVarP(&addr, "addr", "a", defaultAddr, "监听地址（根命令等价于 serve）")
	addLiveReloadFlag(root, &liveReload)

	root.AddCommand(newServeCommand(globals), newGenCommand(globals))
	return root
}

// newServeCommand 构造 serve 子命令：行为与不带子命令的根命令一致。
func newServeCommand(globals *globalOptions) *cobra.Command {
	var addr string
	var liveReload bool

	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "启动开发服务器",
		Long:    "启动开发服务器：目录式路由 + 模板渲染 + 静态文件直出；站点文件变更后自动重载，并通过 SSE 让浏览器自动刷新。",
		Example: serveExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return runServe(cmd, serveOptions{addr: addr, liveReload: liveReload, globals: globals})
		},
	}
	cmd.Flags().StringVarP(&addr, "addr", "a", defaultAddr, "监听地址")
	addLiveReloadFlag(cmd, &liveReload)
	return cmd
}

// newGenCommand 构造 gen 子命令：渲染全部页面并产出纯静态站。
func newGenCommand(globals *globalOptions) *cobra.Command {
	var out string
	var clean bool

	cmd := &cobra.Command{
		Use:     "gen",
		Short:   "渲染模板并产出纯静态站",
		Long:    "渲染模板并产出纯静态站：页面按源码路径落盘（about.html -> dist/about.html），其余文件原样拷贝；生成前清空产物目录，任一页面渲染失败即整体失败。",
		Example: genExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return runGen(cmd, genOptions{globals: globals, out: out, clean: clean})
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", defaultOutDir, "产物目录")
	cmd.Flags().BoolVarP(&clean, "clean", "c", true, "生成前清空产物目录")
	return cmd
}

// addLiveReloadFlag 挂载浏览器热重载开关；根命令与 serve 子命令共用同一套语义。
func addLiveReloadFlag(cmd *cobra.Command, liveReload *bool) {
	cmd.Flags().BoolVarP(liveReload, "livereload", "l", true,
		"注入热重载脚本并通过 SSE 让浏览器自动刷新（关闭后页面与线上产物完全一致）")
}

// addGlobalFlags 挂载根命令与子命令共用的参数。
// 用 persistent flag 是为了让 `goweb --dir site gen` 这类写法同样生效，
// 而不是被父命令静默吞掉。
func addGlobalFlags(cmd *cobra.Command, globals *globalOptions) {
	flags := cmd.PersistentFlags()
	flags.StringVarP(&globals.dir, "dir", "d", defaultPageDir, "页面目录")
	flags.StringVarP(&globals.templateDir, "template", "t", defaultTemplateDir,
		"局部模板目录（相对页面目录），data.json 也在其中；置空表示没有")
}

// serveOptions 是 serve 的参数。
type serveOptions struct {
	globals    *globalOptions
	addr       string
	liveReload bool
}

// genOptions 是 gen 的参数。
type genOptions struct {
	globals *globalOptions
	out     string
	clean   bool
}

// runServe 启动开发服务器。
func runServe(cmd *cobra.Command, opts serveOptions) error {
	logger := newLogger(cmd.ErrOrStderr())
	site, err := LoadSite(SiteOptions{
		Dir:         opts.globals.dir,
		TemplateDir: opts.globals.templateDir,
		ExcludeDirs: []string{defaultOutDir},
	})
	if err != nil {
		return err
	}

	// 热重载：站点重载成功后广播给所有已连接的浏览器页面。
	var live *liveReloadHub
	if opts.liveReload {
		live = newLiveReloadHub()
	}

	stopWatch, err := watchSite(site, logger, func() {
		if live != nil {
			live.broadcast()
		}
	})
	if err != nil {
		return err
	}
	defer stopWatch()

	// 先监听再打印日志：--addr 写 :0 时日志给出的是内核分配的真实地址，
	// 脚本与端到端测试可以直接读它。
	listener, err := net.Listen("tcp", opts.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           newHandler(site, logger, live),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	logger.Info("goweb 开发服务器已启动",
		"addr", listener.Addr().String(),
		"dir", site.Root(),
		"pages", len(site.Pages()),
		"data", site.DataFile(),
		"livereload", opts.liveReload,
	)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// runGen 渲染全部页面并产出纯静态站。
func runGen(cmd *cobra.Command, opts genOptions) error {
	logger := newLogger(cmd.ErrOrStderr())
	site, err := LoadSite(SiteOptions{
		Dir:         opts.globals.dir,
		TemplateDir: opts.globals.templateDir,
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
		"data", site.DataFile(),
	)
	return nil
}

// newLogger 构造开发期日志器，输出到给定 writer（默认标准错误）。
func newLogger(w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

const rootLongDescription = `goweb 用两个子命令覆盖「本地写页面 → 产出静态站」的完整流程：

  serve   起开发服务器，页面 URL 与静态托管严格一致，改完自动重载并让浏览器自动刷新
  gen     渲染模板产出纯静态站，可直接部署

不带子命令时等价于 serve，直接在当前目录起开发服务器。

浏览器热重载（--livereload，默认开启）：
  serve 会在 html 响应末尾注入一段脚本，通过 SSE 端点 ` + liveReloadPath + ` 接收刷新信号；
  改页面、模板、data.json 或静态资源都会整页刷新。这是 serve 独有的注入，
  gen 产物不含任何脚本；用 --livereload=false 可关闭，让页面与线上产物完全一致。

目录约定：
  页面目录（--dir，默认 .）中递归出现的 *.html 都是页面：
    index.html -> /      about.html -> /about/      help/74.html -> /help/74/
  局部模板目录（--template，默认 templates）里的 html 只参与模板解析、不产出页面，
  也不能被直接访问；templates/data.json 的顶层对象是所有页面共用的模板数据。

URL 规则（等效 nginx 的 try_files $uri $uri.html $uri/index.html，零重定向）：
  about.html       -> /about.html 命中真实文件
  about            -> /about      由 $uri.html 命中
  about/           -> 404         扁平产物里没有 about/ 目录
  help             -> /help       由 $uri/index.html 命中
  help/            -> /help/      目录存在，由 index 提供
  非规范路径静默归一化后再解析（//about// -> /about/ -> 404，/a/../about -> 命中）；
  局部模板目录、产物目录与忽略项一律视为不存在；其余返回纯文本 404。`

const rootExample = `  goweb                        # 等价于 goweb serve，访问 http://localhost:8080/
  goweb --addr :3000           # 换端口
  goweb --livereload=false     # 关掉浏览器自动刷新
  goweb --dir site gen         # 公共参数写在哪一层都生效
  goweb gen --out public       # 产出到 public/`

const serveExample = `  goweb serve
  goweb serve --addr :3000
  goweb serve --livereload=false
  goweb serve --dir site --template templates`

const genExample = `  goweb gen
  goweb gen --out public
  goweb gen --clean=false`
