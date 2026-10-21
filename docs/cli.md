# 命令与参数

这一篇讲命令行用法、`.goweb.yml` 配置文件与全部参数的默认值和适用范围。

```bash
goweb init                                     # 生成默认配置文件 .goweb.yml
goweb check                                    # 只校验不产出（跑 gen 的检查 + 站内链接）
goweb [serve] [--addr :8080] [--dir pages] [--template templates] [--assets .]
goweb dev                                      # serve 的别名，完全等价
goweb gen     [--dir pages] [--out dist] [--clean=true]
goweb build                                    # gen 的别名，完全等价
goweb completion bash|zsh|fish|powershell      # 生成 shell 补全脚本
goweb help [command]
goweb --version
```

`goweb` 不带子命令时等价于 `goweb serve`；`goweb dev` 等价于 `goweb serve`、`goweb build` 等价于 `goweb gen`（都是别名，参数与行为完全一致，`build` 在 `--mode auto` 下同样是 `prod`）；`--dir` / `--template` / `--assets` / `--config` / `--mode` / `--fingerprint` / `--build-time` 是公共参数，写在子命令前后都可以（`goweb --dir pages gen` 与 `goweb gen --dir pages` 等价）。

## 校验：`goweb check`

```bash
goweb check                  # 跑完整校验
goweb check --links=false    # 不查站内链接
```

- 跑与 `gen` **完全相同**的检查：模板语法与 `data.json`、每页渲染（含压缩）、忽略规则、产物路径冲突、产物目录护栏；另外默认检查渲染结果里的**站内链接**。
- **不写、不清空任何产物**：产物目录既不创建也不删除（`--out` 只用于「哪些目录被排除」和护栏判断）。
- **问题一次列全**再以非零码退出（不是遇到第一个就停），方便一轮改完。
- 链接检查按 `serve` 完全相同的 `try_files` 规则试算，并额外要求**目标会进产物且 URL 与产物路径一致**，因此能抓出「本地能开、线上没有」的链接：多写了页面根那一层（`/pages/x.html`）、指向目录（`/static`）、指向被忽略的文件、指向不存在的文件。外链、`data:`、`mailto:`、锚点不检查。
- 同一个坏 URL 在多个页面出现时合并成一条（附页面清单），避免共享模板的问题刷屏。

## 生成配置文件：`goweb init`

```bash
goweb init                              # 在当前目录生成 .goweb.yml
goweb init --force                      # 覆盖已存在的配置
goweb init --config conf/goweb.yml      # 生成到别处
```

- 生成的内容是**全部 13 个键的当前默认值**，每个键上方一行注释；出现这个文件**不会改变任何默认行为**（可对照下文「配置文件」小节逐项核对）。
- 已存在时报错退出、不碰你的文件；要覆盖必须显式 `--force`。
- 从子目录运行且项目根已有配置时报错（避免生成第二份嵌套配置，出现两个真相源）。
- **唯一的例外**：`addr` 只以注释形式列出。显式写 `addr`（哪怕值就是 `:8080`）会被视为「我就要这个端口」，从而关掉「默认端口被占用时自动递增」——生成的配置不该悄悄改变这个行为。

## 配置文件 `.goweb.yml`

放在项目根（也可用 `--config` 指向别处）；键名与 flag 同名，**优先级 flag > 配置 > 默认**，**未知字段直接报错**（避免拼错后静默无效）：

```yaml
dir: pages
template: templates
assets: .
out: dist
minify: true          # 产物压缩（只作用于 gen/build）
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
| `--addr` | `-a` | serve（含不带子命令） | `:8080` | 监听地址；**未显式指定**时端口被占用会自动递增（见下） |
| `--dir` | `-d` | 全部 | `.` | 页面根（相对项目根）：页面发现与页面路径的基准 |
| `--template` | `-t` | 全部 | `templates` | 模板根（相对项目根）：局部模板与 `data.json`，不受 `--dir` 影响；置空表示没有 |
| `--assets` | — | 全部 | `.` | 资源根（相对项目根）：其余文件按它映射到产物 |
| `--config` | — | 全部 | 空（向上查找 `.goweb.yml`） | 配置文件路径；其所在目录即项目根 |
| `--out` | `-o` | gen | `dist` | 产物目录（相对项目根） |
| `--clean` | `-c` | gen | `true` | 生成前清空产物目录 |
| `--minify` | — | gen / build | `true` | 压缩页面与 CSS/JS/JSON/SVG/XML 资源（只去空白与注释，不改 JS 变量名）；`--minify=false` 关闭；`serve` 始终不压缩 |
| `--livereload` | `-l` | serve（含不带子命令） | `true` | 注入热重载脚本并开放 SSE 端点；`--livereload=false` 关闭 |
| `--open` | — | serve（含不带子命令） | `true` | 监听成功后自动用系统默认浏览器打开站点；`--open=false` 关闭 |
| `--fingerprint` | — | 全部 | `true` | 自动给页面里的站内资源引用补 `?v=<内容哈希>`；`--fingerprint=false` 关闭 |
| `--mode` | — | 全部 | `auto` | 渲染模式：`auto`（serve=dev、gen=prod）、`dev`、`prod` |
| `--build-time` | — | 全部 | 空（取站点最新修改时间） | 模板里的构建时间，RFC3339 或 `2006-01-02` |
| `--force` | — | init | `false` | 覆盖已存在的配置文件 |
| `--links` | — | check | `true` | 检查渲染结果里的站内链接；`--links=false` 关闭 |
| `--version` | `-v` | 根命令（`goweb --version`） | — | 打印版本号（`dev`，除非构建时用 `-ldflags` 注入） |
| `--help` | `-h` | 全部 | — | 查看帮助，如 `goweb gen --help` |

### 日志格式

slog 文本格式、键值对，时间戳固定为 `time.DateTime`（`2006-01-02 15:04:05`，不含毫秒与时区偏移，含空格所以会被引号包起来）：

```console
time="2026-10-21 14:07:44" level=INFO msg="goweb 开发服务器已启动" addr=127.0.0.1:8080 mode=dev livereload=true open=true
time="2026-10-21 14:07:45" level=INFO msg=处理请求 method=GET path=/ status=200
time="2026-10-21 14:07:46" level=INFO msg=处理请求 method=GET path=/missing status=404
```

访问日志只留 `method`/`path`/`status`（耗时交给浏览器 devtools 或压测工具，不占终端宽度）。

**终端里给非 2xx 的状态码上色**（只影响显示，不改变输出内容）：

| 状态码 | 颜色 |
| --- | --- |
| 2xx | 不上色（正常请求占大多数，全上色反而看不清异常） |
| 3xx | 青 |
| 4xx | 黄 |
| 5xx | 红 |

- 只在**输出是终端**时上色：重定向到文件、管道给 `less`/`grep` 时不会混入转义序列（判断依据是 stdout/stderr 是否为字符设备）。
- 设置了 `NO_COLOR` 环境变量（约定见 no-color.org）时一律不上色。
- 想要纯文本日志又不想改环境变量时，直接重定向即可：`goweb dev > build.log 2>&1`。

> 注意：日志**字段**里的构建时间（`buildTime=`）仍是带时区的 RFC3339（它同时是 `--build-time` 的输入与产物里的时间值），与时间戳格式是两回事。

### 监听端口与自动递增

- 只有**没有显式指定端口**（即用内置默认 `:8080`）时才自动递增：`:8080` 被占用就依次试 `:8081`…`:8089`，成功后在日志里打印一行 `端口被占用，已自动改用其它端口`，启动行给出真实地址（`--open` 打开的也是它）。
- 显式写 `--addr`，或在 `.goweb.yml` 里写 `addr:`，都视为「我就要这个端口」→ 被占用时**直接报错退出**（想固定端口就这么写，它同时也是关掉递增的开关）。
- 只递增端口号，主机部分不变（`:8080` → `:8081`，`127.0.0.1:8080` → `127.0.0.1:8081`）；`--addr :0`（内核分配）与非数字端口不递增。
- 只对「地址已被占用」重试；端口非法、权限不足等错误直接失败，不会被回退掩盖。
- 10 个端口都被占用时退出，错误信息列出起始地址与最后尝试的端口。

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
