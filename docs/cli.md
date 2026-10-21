# 命令与参数

这一篇讲命令行用法、`.goweb.yml` 配置文件与全部参数的默认值和适用范围。

```bash
goweb [serve] [--addr :8080] [--dir pages] [--template templates] [--assets .]
goweb gen     [--dir pages] [--out dist] [--clean=true]
goweb build                                    # gen 的别名，完全等价
goweb completion bash|zsh|fish|powershell      # 生成 shell 补全脚本
goweb help [command]
goweb --version
```

`goweb` 不带子命令时等价于 `goweb serve`；`goweb build` 等价于 `goweb gen`（别名，参数与行为完全一致，`--mode auto` 下同样是 `prod`）；`--dir` / `--template` / `--assets` / `--config` / `--mode` / `--fingerprint` / `--build-time` 是公共参数，写在子命令前后都可以（`goweb --dir pages gen` 与 `goweb gen --dir pages` 等价）。

## 配置文件 `.goweb.yml`

放在项目根（也可用 `--config` 指向别处）；键名与 flag 同名，**优先级 flag > 配置 > 默认**，**未知字段直接报错**（避免拼错后静默无效）：

```yaml
dir: pages
template: templates
assets: .
out: dist
mode: auto            # auto | dev | prod
build-time: ""        # RFC3339 或 2006-01-02
fingerprint: true     # 自动补 ?v=<内容哈希>
livereload: true
open: true            # 启动后自动打开浏览器
addr: ":8080"
gitignore: true       # 读项目根的 .gitignore
ignore:               # 额外忽略模式（与 .gitignore 同语法，只作用于 gen）
  - "**/*.map"
  - "drafts/"
```

| 参数 | 短选项 | 适用 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `--addr` | `-a` | serve（含不带子命令） | `:8080` | 监听地址 |
| `--dir` | `-d` | 全部 | `.` | 页面根（相对项目根）：页面发现与页面路径的基准 |
| `--template` | `-t` | 全部 | `templates` | 模板根（相对项目根）：局部模板与 `data.json`，不受 `--dir` 影响；置空表示没有 |
| `--assets` | — | 全部 | `.` | 资源根（相对项目根）：其余文件按它映射到产物 |
| `--config` | — | 全部 | 空（向上查找 `.goweb.yml`） | 配置文件路径；其所在目录即项目根 |
| `--out` | `-o` | gen | `dist` | 产物目录（相对项目根） |
| `--clean` | `-c` | gen | `true` | 生成前清空产物目录 |
| `--livereload` | `-l` | serve（含不带子命令） | `true` | 注入热重载脚本并开放 SSE 端点；`--livereload=false` 关闭 |
| `--open` | — | serve（含不带子命令） | `true` | 监听成功后自动用系统默认浏览器打开站点；`--open=false` 关闭 |
| `--fingerprint` | — | 全部 | `true` | 自动给页面里的站内资源引用补 `?v=<内容哈希>`；`--fingerprint=false` 关闭 |
| `--mode` | — | 全部 | `auto` | 渲染模式：`auto`（serve=dev、gen=prod）、`dev`、`prod` |
| `--build-time` | — | 全部 | 空（取站点最新修改时间） | 模板里的构建时间，RFC3339 或 `2006-01-02` |
| `--version` | `-v` | 根命令（`goweb --version`） | — | 打印版本号（`dev`，除非构建时用 `-ldflags` 注入） |
| `--help` | `-h` | 全部 | — | 查看帮助，如 `goweb gen --help` |

### 自动打开浏览器

`serve` 监听成功后才打开（所以 `--addr :0` 打开的是内核分配的真实端口）：

- 地址推导：`:8080`、`0.0.0.0:8080`、`[::]:8080` → `http://localhost:8080/`；指定 IP 原样使用，IPv6 补方括号。
- 平台命令：macOS `open`、Windows `rundll32 url.dll,FileProtocolHandler`、Linux/BSD `xdg-open`。
- **打不开只记一条日志**（CI、SSH、没有桌面环境），绝不影响服务器；`--open=false` 完全不尝试。
- 自定义命令（如 WSL 的 `wslview`、指定浏览器）：环境变量 `GOWEB_OPEN_COMMAND`，URL 作为最后一个参数传入。

启用 shell 补全（以 zsh 为例）：

```bash
goweb completion zsh > "${fpath[1]}/_goweb"
```

---

[← 返回 README](../README.md)
