package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	// defaultAddr 是开发服务器默认监听地址：只监听本机回环口，
	// 不把「整个项目目录 + 模板渲染」默认暴露到局域网。
	defaultAddr = "127.0.0.1:8080"
	// defaultPageDir 是默认页面目录，即运行目录。
	defaultPageDir = "."
	// defaultTemplateDir 是默认模板根（全局模板与 data.json 所在目录），相对项目根。
	defaultTemplateDir = "templates"
	// defaultAssetsRoot 是默认资源根：项目根，即除页面与模板之外的文件都进产物。
	defaultAssetsRoot = "."
	// defaultOutDir 是 gen 的默认产物目录。
	defaultOutDir = "dist"
	// modeAuto 表示按子命令自动选择渲染模式：serve=dev、gen=prod。
	modeAuto = "auto"
)

// version 是版本号，构建时可通过 -ldflags "-X main.version=v1.0.0" 注入（发布包就是这么做的）。
var version = "dev"

// effectiveVersion 给出要展示的版本号：
//  1. -ldflags 注入的值（发布包走这条）；
//  2. 否则回退到构建信息里的模块版本——`go install github.com/lixiangzhong/goweb@v0.2.0`
//     不会注入 ldflags，但会把模块版本写进二进制，取出来才对得上用户装的东西；
//  3. 本地 `go build` 时模块版本是 "(devel)"，此时依旧是 "dev"。
func effectiveVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return info.Main.Version
}

// globalOptions 是根命令与各子命令共用的参数（persistent flag）。
type globalOptions struct {
	// dir 是页面根（相对项目根）：只负责页面发现与页面路径。
	dir string
	// templateDir 是模板根（相对项目根）：局部模板与 data.json，不受 dir 影响。
	templateDir string
	// assetsRoot 是资源根（相对项目根）：其余文件按它映射到产物。
	assetsRoot string
	// config 是配置文件路径；留空表示从运行目录向上查找 .goweb.yml。
	config string
	// mode 是渲染模式：auto（serve=dev、gen=prod）、dev、prod。
	mode string
	// buildTime 是模板里的构建时间（RFC3339 或 2006-01-02）；留空则取站点内容最新的文件修改时间。
	buildTime string
	// fingerprint 表示是否给页面里的站内资源引用自动补 ?v=<内容哈希>。
	fingerprint bool
	// gitignore 表示是否读取项目根的 .gitignore 作为忽略规则（只作用于 gen/check）。
	gitignore bool
	// ignore 是命令行追加的忽略模式（与 .gitignore 同语法），会接在配置 ignore 之后。
	ignore []string
}

// main 只负责退出码：命令树、错误信息与 usage 都由 cobra 处理。
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
	var open bool

	root := &cobra.Command{
		Use:     "goweb",
		Short:   "面向前端开发的静态站服务器与静态化工具",
		Long:    rootLongDescription,
		Example: rootExample,
		Version: effectiveVersion(),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 参数已解析完成，运行期错误不再刷 usage。
			cmd.SilenceUsage = true
			return runServe(cmd, serveOptions{
				addr: addr, liveReload: liveReload, open: open, globals: globals,
			})
		},
	}
	// 统一错误前缀（形如 goweb: ...），并与 usage 的输出顺序保持一致。
	root.SetErrPrefix("goweb:")
	addGlobalFlags(root, globals)
	root.Flags().StringVarP(&addr, "addr", "a", defaultAddr, "监听地址（根命令等价于 serve）")
	addLiveReloadFlag(root, &liveReload)
	addOpenFlag(root, &open)

	root.AddCommand(newServeCommand(globals), newGenCommand(globals), newCheckCommand(globals), newInitCommand(globals))
	return root
}

// newServeCommand 构造 serve 子命令：行为与不带子命令的根命令一致。
func newServeCommand(globals *globalOptions) *cobra.Command {
	var addr string
	var liveReload bool
	var open bool

	cmd := &cobra.Command{
		Use:     "serve",
		Aliases: []string{"dev"},
		Short:   "启动开发服务器（别名：dev）",
		Long:    "启动开发服务器：目录式路由 + 模板渲染 + 静态文件直出；站点文件变更后自动重载，并通过 SSE 让浏览器自动刷新。别名 dev 与 serve 完全等价（不带子命令时同样等价于 serve）。",
		Example: serveExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return runServe(cmd, serveOptions{
				addr: addr, liveReload: liveReload, open: open, globals: globals,
			})
		},
	}
	cmd.Flags().StringVarP(&addr, "addr", "a", defaultAddr, "监听地址")
	addLiveReloadFlag(cmd, &liveReload)
	addOpenFlag(cmd, &open)
	return cmd
}

// newGenCommand 构造 gen 子命令：渲染全部页面并产出纯静态站。
// 别名 build：与 gen 完全等价，照顾「构建」这个更常见的说法。
func newGenCommand(globals *globalOptions) *cobra.Command {
	var out string
	var clean bool
	var minifyOutput bool

	cmd := &cobra.Command{
		Use:     "gen",
		Aliases: []string{"build"},
		Short:   "渲染模板并产出纯静态站（别名：build）",
		Long:    "渲染模板并产出纯静态站：页面按页面根落盘（pages/about.html -> dist/about.html），其余文件按所属根拷贝；生成前清空产物目录，任一页面渲染失败即整体失败。默认压缩页面与 CSS/JS/JSON/SVG/XML 资源（只去空白与注释，不改 JS 变量名），用 --minify=false 关闭。别名 build 与 gen 完全等价。",
		Example: genExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return runGen(cmd, genOptions{globals: globals, out: out, clean: clean, minify: minifyOutput})
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", defaultOutDir, "产物目录")
	cmd.Flags().BoolVarP(&clean, "clean", "c", true, "生成前清空产物目录")
	cmd.Flags().BoolVar(&minifyOutput, "minify", true,
		"压缩页面与 CSS/JS/JSON/SVG/XML 资源（只去空白与注释，不改 JS 变量名）；--minify=false 关闭")
	return cmd
}

// newCheckCommand 构造 check 子命令：跑与 gen 完全相同的校验，但不写任何产物。
func newCheckCommand(globals *globalOptions) *cobra.Command {
	var links bool

	cmd := &cobra.Command{
		Use:   "check",
		Short: "只校验不产出：跑 gen 的全套检查，外加站内链接检查",
		Long: "跑与 gen 完全相同的校验但不写任何产物：模板语法与 data.json、每页渲染（含压缩）、" +
			"忽略规则、产物路径冲突与产物目录护栏都会检查一遍，最后再把渲染结果里的站内链接" +
			"按 URL 规则试算一遍（外链、data:、锚点不查），报出会 404 的目标。" +
			"问题会一次全部列出再退出（非零退出码），不是遇到第一个就停。" +
			"用 --links=false 关掉链接检查。",
		Example: checkExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return runCheck(cmd, globals, checkOptions{links: links})
		},
	}
	cmd.Flags().BoolVar(&links, "links", true,
		"检查渲染结果里的站内链接是否都能在产物里找到；--links=false 关闭")
	return cmd
}

// newInitCommand 构造 init 子命令：生成一份带全部默认值（并逐行注释）的 .goweb.yml。
func newInitCommand(globals *globalOptions) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "init",
		Short:   "生成默认配置文件 .goweb.yml",
		Long:    "生成默认配置文件：键名与命令行参数同名，每个键都写出当前默认值并逐行注释；出现这个文件不会改变任何默认行为。已存在时报错退出，用 --force 覆盖；用 --config <路径> 生成到别处。",
		Example: initExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return runInit(cmd, globals, initOptions{force: force})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "覆盖已存在的配置文件")
	return cmd
}

// addLiveReloadFlag 挂载浏览器热重载开关；根命令与 serve 子命令共用同一套语义。
func addLiveReloadFlag(cmd *cobra.Command, liveReload *bool) {
	cmd.Flags().BoolVarP(liveReload, "livereload", "l", true,
		"注入热重载脚本并通过 SSE 让浏览器自动刷新（关闭后页面与线上产物完全一致）")
}

// addOpenFlag 挂载「启动后自动打开浏览器」开关；根命令与 serve 子命令共用。
func addOpenFlag(cmd *cobra.Command, open *bool) {
	cmd.Flags().BoolVar(open, "open", true,
		"启动后自动用系统默认浏览器打开站点；--open=false 关闭（打不开只记日志，不影响服务）")
}

// addGlobalFlags 挂载根命令与子命令共用的参数。
// 用 persistent flag 是为了让 `goweb --dir site gen` 这类写法同样生效，
// 而不是被父命令静默吞掉。
func addGlobalFlags(cmd *cobra.Command, globals *globalOptions) {
	flags := cmd.PersistentFlags()
	flags.StringVarP(&globals.dir, "dir", "d", defaultPageDir, "页面根：页面发现与页面路径的基准（相对项目根）")
	flags.StringVarP(&globals.templateDir, "template", "t", defaultTemplateDir,
		"模板根：局部模板与 data.json 所在目录（相对项目根），不受 --dir 影响；置空表示没有")
	flags.StringVar(&globals.assetsRoot, "assets", defaultAssetsRoot,
		"资源根：其余文件按它映射到产物（相对项目根）")
	flags.StringVar(&globals.config, "config", "",
		"配置文件路径；留空则从运行目录向上查找 .goweb.yml（其所在目录即项目根）")
	flags.StringVar(&globals.mode, "mode", modeAuto,
		"渲染模式：auto（serve=dev、gen=prod）、dev、prod；模板里用 {{if prod}} 判断")
	flags.StringVar(&globals.buildTime, "build-time", "",
		"模板里的构建时间（RFC3339 或 2006-01-02）；留空则取站点内容里最新的文件修改时间")
	flags.BoolVar(&globals.fingerprint, "fingerprint", true,
		"给页面里的站内静态资源引用自动补 ?v=<内容哈希>，用于缓存失效；关闭后输出不做任何改写")
	flags.BoolVar(&globals.gitignore, "gitignore", true,
		"读取项目根的 .gitignore 作为忽略规则（只作用于 gen/check；serve 仍可访问被忽略的文件）")
	flags.StringArrayVar(&globals.ignore, "ignore", nil,
		"额外忽略模式（与 .gitignore 同语法，可重复；只作用于 gen/check，追加在配置 ignore 之后）")
}

// resolveMode 把 --mode 的取值解析成实际模式：auto 时 serve 用 dev、gen 用 prod。
func resolveMode(raw string, devCommand bool) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", modeAuto:
		if devCommand {
			return ModeDev, nil
		}
		return ModeProd, nil
	case string(ModeDev):
		return ModeDev, nil
	case string(ModeProd):
		return ModeProd, nil
	default:
		return "", fmt.Errorf("--mode 只支持 %s、%s、%s，实际是 %q", modeAuto, ModeDev, ModeProd, raw)
	}
}

// parseBuildTime 解析 --build-time，留空表示由站点文件推导。
// 接受的格式与模板 date/year 参数共用同一份清单（acceptedTimeLayouts），避免两处分叉。
func parseBuildTime(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, nil
	}
	for _, layout := range acceptedTimeLayouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("--build-time 无法解析 %q（支持 %s）", raw, timeLayoutsHint)
}

// serveOptions 是 serve 的参数。
type serveOptions struct {
	globals    *globalOptions
	addr       string
	liveReload bool
	open       bool
}

// genOptions 是 gen 的参数。
type genOptions struct {
	globals *globalOptions
	out     string
	clean   bool
	minify  bool
}

// runServe 启动开发服务器。
func runServe(cmd *cobra.Command, opts serveOptions) error {
	logger := newLogger(cmd.ErrOrStderr())

	resolved, err := resolveSettings(cmd, opts.globals, localFlags{
		addr:       opts.addr,
		liveReload: opts.liveReload,
		open:       opts.open,
	}, true)
	if err != nil {
		return err
	}
	site, err := LoadSite(resolved.siteOptions([]string{resolved.out}, false))
	if err != nil {
		return err
	}
	if len(site.Pages()) == 0 {
		logger.Warn("没有发现任何页面，检查 --dir 是否指向页面根", "pagesRoot", site.PagesRoot())
	}

	// 热重载：站点重载成功后广播给所有已连接的浏览器页面。
	var live *liveReloadHub
	if resolved.liveReload {
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
	// 没有显式指定端口（用的内置默认 :8080）时，端口被占用会自动递增重试。
	listener, err := listenWithFallback(resolved.addr, !resolved.addrExplicit, logger)
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
		"projectRoot", site.Root(),
		"pagesRoot", site.PagesRoot(),
		"templateRoot", site.TemplateRoot(),
		"assetsRoot", site.AssetsRoot(),
		"pages", len(site.Pages()),
		"data", site.DataFile(),
		"mode", site.Mode(),
		"buildTime", site.BuildTime().Format(time.RFC3339),
		"fingerprint", resolved.fingerprint,
		"livereload", resolved.liveReload,
		"open", resolved.open,
	)

	// 监听成功后才能确定最终地址（--addr :0 由内核分配端口），因此在这里打开浏览器。
	// 打开动作只是开发体验的附属：失败只记日志，绝不拖垮服务器。
	if resolved.open {
		openInBrowser(logger, newBrowserOpener(), browserURL(listener.Addr()))
	}

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// checkOptions 是 check 的参数。
type checkOptions struct {
	// links 表示是否检查渲染结果里的站内链接。
	links bool
}

// runCheck 跑与 gen 完全相同的校验但不写任何产物，并把所有问题一次列全。
//
// 校验内容：模板语法与 data.json、每页渲染（含压缩）、忽略规则、产物路径冲突、
// 产物目录护栏，以及（默认开启的）渲染结果里的站内链接。
func runCheck(cmd *cobra.Command, globals *globalOptions, opts checkOptions) error {
	logger := newLogger(cmd.ErrOrStderr())

	// 与 gen 一样按产出方向取默认值：--mode auto 时为 prod，因此 prod 分支也会被校验。
	resolved, err := resolveSettings(cmd, globals, localFlags{}, false)
	if err != nil {
		return err
	}
	out, err := resolved.resolveOutput()
	if err != nil {
		return err
	}
	// Publishing 为真：忽略规则按 gen 的语义生效（被忽略的文件视为不会发布）。
	site, err := LoadSite(resolved.siteOptions([]string{out}, true))
	if err != nil {
		return err
	}

	var checker *linkChecker
	var onPage func(string, []byte) error
	if opts.links {
		checker = newLinkChecker(site)
		onPage = checker.checkPage
	}

	result, err := Generate(site, GenOptions{
		Out:    out,
		Minify: resolved.minify,
		Check:  true,
		OnPage: onPage,
		Logger: logger,
	})

	problems := renderProblems(err)
	if checker != nil {
		problems = append(problems, checker.issues()...)
	}
	if len(problems) > 0 {
		// 一次列全：修一轮就能改完，而不是修一个跑一次。
		return fmt.Errorf("检查未通过，共 %d 个问题：\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}

	checkedLinks := 0
	if checker != nil {
		checkedLinks = checker.checked
	}
	logger.Info("检查通过",
		"pages", result.Pages,
		"assets", result.Assets,
		"links", checkedLinks,
		"out", result.Out,
	)
	return nil
}

// renderProblems 把 Generate 返回的问题拆成「一条一行」的列表项：
// errors.Join 里的每个错误单独成条，错误内部的换行缩进成续行，
// 避免多行错误（例如产物冲突的两条来源说明）被拆成多条独立问题。
func renderProblems(err error) []string {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var lines []string
		for _, single := range joined.Unwrap() {
			lines = append(lines, formatProblem(single)...)
		}
		return lines
	}
	return formatProblem(err)
}

// formatProblem 把单个错误改成列表项：首行保持原样，续行缩进对齐。
func formatProblem(err error) []string {
	lines := strings.Split(err.Error(), "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = "    " + lines[i]
	}
	return lines
}

// runGen 渲染全部页面并产出纯静态站。
func runGen(cmd *cobra.Command, opts genOptions) error {
	logger := newLogger(cmd.ErrOrStderr())

	resolved, err := resolveSettings(cmd, opts.globals, localFlags{out: opts.out, minify: opts.minify}, false)
	if err != nil {
		return err
	}
	out, err := resolved.resolveOutput()
	if err != nil {
		return err
	}
	// gen 是产出产物：忽略规则生效（被忽略的页面与资源不进产物）。
	site, err := LoadSite(resolved.siteOptions([]string{out}, true))
	if err != nil {
		return err
	}
	if len(site.Pages()) == 0 {
		logger.Warn("没有发现任何页面，检查 --dir 是否指向页面根", "pagesRoot", site.PagesRoot())
	}

	result, err := Generate(site, GenOptions{
		Out: out, Clean: opts.clean, Minify: resolved.minify, Logger: logger,
	})
	if err != nil {
		return err
	}
	logger.Info("静态站已生成",
		"out", result.Out,
		"pages", result.Pages,
		"assets", result.Assets,
		"data", site.DataFile(),
		"mode", site.Mode(),
		"buildTime", site.BuildTime().Format(time.RFC3339),
		"fingerprint", resolved.fingerprint,
		"minify", resolved.minify,
	)
	return nil
}

// newLogger 构造开发期日志器，输出到给定 writer（默认标准错误）。
func newLogger(w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	// 终端里给非 2xx 的 status 上色；重定向到文件或管道时自动不上色，NO_COLOR 可关闭。
	if wantColorLogs(w) {
		w = newColorWriter(w)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		// 时间戳用 time.DateTime（2006-01-02 15:04:05）：日志按秒可读，
		// 不带时区与毫秒，终端里对齐更整齐。注意 slog 会自动给含空格的值加引号。
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.String(slog.TimeKey, attr.Value.Time().Format(time.DateTime))
			}
			return attr
		},
	}))
}

const rootLongDescription = `goweb 用四个子命令覆盖「初始化配置 → 校验 → 本地写页面 → 产出静态站」的完整流程：

  init    生成默认配置文件 .goweb.yml（键名与参数同名，全部写出默认值并带注释）
  check   只校验不产出：模板/渲染/忽略规则/产物路径冲突，外加站内链接检查
  serve   起开发服务器，页面 URL 与静态托管严格一致，改完自动重载并让浏览器自动刷新
          （别名 dev）
  gen     渲染模板产出纯静态站，可直接部署（别名 build）

不带子命令时等价于 serve，直接在当前目录起开发服务器。

监听端口（--addr，默认 127.0.0.1:8080，只监听本机）：
  没有显式指定端口时，8080 被占用会自动依次尝试 8081…8089，并在日志里说明；
  显式写 --addr 或在配置里写 addr 就表示「我就要这个端口」，被占用时直接报错退出。

浏览器热重载（--livereload，默认开启）：
  serve 会在 html 响应末尾注入一段脚本，通过 SSE 端点 ` + liveReloadPath + ` 接收刷新信号；
  改页面、模板、data.json 或静态资源都会整页刷新。这是 serve 独有的注入，
  gen 产物不含任何脚本；用 --livereload=false 可关闭，让页面与线上产物完全一致。

启动后自动打开浏览器（--open，默认开启）：
  监听成功后用系统默认浏览器打开站点（--addr :0 时用内核分配的真实端口）；
  --open=false 关闭。CI/SSH 里打不开只记一条日志，不影响服务器；
  需要自定义命令（例如 WSL 的 wslview）时设置 GOWEB_OPEN_COMMAND。

三个根（都相对项目根；项目根 = .goweb.yml 所在目录，没有配置文件时 = 运行目录）：
  --dir（默认 .）        页面根：递归出现的 *.html 都是页面
                         index.html -> /   about.html -> /about/   help/74.html -> /help/74/
  --template（默认 templates） 模板根：局部模板与 data.json，**不受 --dir 影响**
  --assets（默认 .）     资源根：其余文件按它映射到产物
  gen 时页面按页面根落盘、资源按所属根落盘，URL 与产物路径始终一致。

配置文件 .goweb.yml（可选，放在项目根；用 --config 指定其它位置）：
  dir / template / assets / out / minify / mode / build-time / fingerprint / livereload /
  addr / open / gitignore / ignore —— 键名与 flag 同名，优先级 flag > 配置 > 默认，
  未知字段直接报错。

产物压缩（--minify，默认开启；只作用于 gen/build）：
  压缩页面与 CSS/JS/JSON/SVG/XML 资源，只去空白与注释：不改 JS 变量名（minify 不是 uglify），
  <pre>/<textarea> 内的空白逐字节保留，CSS/JS 里的 /*! 许可声明 */ 保留；serve 始终不压缩。
  注意模板里的 HTML 注释本来就会被 html/template 丢掉；可选闭合标签与属性引号会被省略，
  依赖正则处理 HTML 的下游工具请用 --minify=false。

忽略规则（项目根的 .gitignore + 配置里的 ignore）**只作用于 gen**：
  被忽略的文件不进产物，但 serve 仍能看到，方便本地预览草稿。

模板函数（大小写之分即信任边界：大写输出不转义的安全类型）：
  HTML / JS                                        原样输出 HTML / 值编码为 JSON 字面量
  {{asset "/static/css/site.css"}}                 -> /static/css/site.css?v=<内容哈希>，文件不存在直接报错
  {{default "未命名" .site.title}}                 值为空时用兜底值（补「缺键渲染为空」的坑）
  {{fail "缺少 site.title"}}                       渲染直接失败：gen 退出非零、serve 回 500
  {{template "card" dict "title" .t "url" .u}}     给局部模板传多个参数；另有 list
  upper / lower / trimPrefix / trimSuffix / replace / join / truncate / charCount
                                                  文本工具，长度与截断一律按字符（中文不会被截半）
  dev / prod / buildTime / year / date             模式与时间

模式与构建时间：
  --mode auto（默认）时 serve 用 dev、gen 用 prod，也可显式写 --mode dev|prod；
  模板里 {{if prod}} 常用于「统计脚本只进线上产物」。
  构建时间默认取站点内容里最新的文件修改时间（同样输入 → 同样产出，可复现），
  用 --build-time 2006-01-02T15:04:05Z07:00 可显式固定。

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
  goweb gen --out public       # 产出到 public/
  goweb build                  # 与 goweb gen 完全等价（别名）
  goweb dev                    # 与 goweb serve 完全等价（别名）`

const serveExample = `  goweb serve
  goweb dev                    # 别名，与 serve 等价
  goweb serve --addr :3000
  goweb serve --livereload=false
  goweb serve --dir pages --template templates`

const checkExample = `  goweb check                  # 跑完整校验，问题一次列全
  goweb check --links=false    # 不查站内链接`

const initExample = `  goweb init                   # 生成 .goweb.yml
  goweb init --force           # 覆盖已存在的配置
  goweb init --config site/.goweb.yml`

const genExample = `  goweb gen
  goweb build                  # 别名，与 gen 等价
  goweb gen --out public
  goweb gen --clean=false`
