# goweb

> 面向前端开发的极简静态站工具：写完 HTML 就能预览，一条命令产出可直接部署的纯静态站。

[![CI](https://github.com/lixiangzhong/goweb/actions/workflows/ci.yml/badge.svg)](https://github.com/lixiangzhong/goweb/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/lixiangzhong/goweb)](https://github.com/lixiangzhong/goweb/blob/HEAD/go.mod)
[![License](https://img.shields.io/github/license/lixiangzhong/goweb)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](#贡献)

`goweb` 是一个 Go 写的单二进制静态站工具，只有两个子命令：

- **`goweb serve`** —— 在站点目录起开发服务器。产物与源码一一对应，开发服务器不做任何尾斜杠/扩展名重定向，改完模板或数据自动重新加载。
- **`goweb gen`** —— 把页面渲染成纯静态站，产出 `dist/`，拷到任何静态托管即可上线。

不需要 npm、webpack、vite，没有配置文件，只依赖标准库 + [cobra](https://github.com/spf13/cobra) + [fsnotify](https://github.com/fsnotify/fsnotify)。

## 目录

- [特性](#特性)
- [安装](#安装)
- [快速开始](#快速开始)
- [目录约定](#目录约定)
- [模板](#模板)
- [模板数据](#模板数据)
- [URL 规则](#url-规则)
- [浏览器热重载](#浏览器热重载)
- [生成静态站](#生成静态站)
- [部署](#部署)
- [命令与参数](#命令与参数)
- [设计取舍](#设计取舍)
- [目前不做](#目前不做)
- [开发](#开发)
- [贡献](#贡献)
- [许可证](#许可证)

## 特性

- **零配置零构建**：站点就是一堆 `.html` + css/js，写完直接预览，不用装任何前端工具链。
- **产物跟随源码路径**：`about.html` → `dist/about.html`，`help/74.html` → `dist/help/74.html`，产物与源码一一对应，部署后 `file://` 也能直接打开。
- **开发服务器等效 `try_files`**：解析顺序就是 nginx 的 `try_files $uri $uri.html $uri/index.html`，零重定向；本地结果与用同一行配置部署的线上结果逐条一致（含 404）。
- **复用页头页脚**：直接用 Go 的 `html/template`，`{{define}}` + `{{template}}` 抽公共片段，不需要学新语法。
- **站点级数据**：`templates/data.json` 提供站名、备案号、导航等共享变量，页面与局部模板都能取。
- **保存即可见**：页面、模板、`data.json` 或静态资源变更后自动重载，并通过 SSE 让浏览器自动刷新；改到一半的坏模板不会让服务挂掉，也不会触发刷新。
- **一条命令静态化**：`goweb gen` 原地产出 `dist/`，其余文件原样拷贝。
- **危险操作有护栏**：清空产物目录前会拦截页面目录本身、其父目录、局部模板目录与用户主目录。
- **快速失败**：任一页面渲染失败即整体失败、非零退出，绝不把半成品产物当成成功发布。

## 安装

需要 Go 1.25 或更高版本。

```bash
go install github.com/lixiangzhong/goweb@latest
```

或者从源码构建：

```bash
git clone https://github.com/lixiangzhong/goweb.git
cd goweb
go build -o goweb .

# 带上版本号（goweb --version 会显示它）
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" -o goweb .
```

### 预编译二进制

[Releases](https://github.com/lixiangzhong/goweb/releases) 由 GoReleaser 在推 `v*` 标签时自动构建：

| 平台 | 架构 | 归档 |
| --- | --- | --- |
| macOS | Intel / Apple Silicon | `goweb_<版本>_darwin_{amd64,arm64}.tar.gz` |
| Linux | x86_64 / arm64 | `goweb_<版本>_linux_{amd64,arm64}.tar.gz` |
| Windows | x64 | `goweb_<版本>_windows_amd64.zip`（内含 `goweb.exe`） |

每个 Release 附带 `checksums.txt`。

Windows 说明：

- 只提供 **x64（amd64）**：Windows arm64 没有进 CI 测试矩阵，不发布未验证的产物。
- 二进制**未签名**，首次运行会被 SmartScreen 拦一下，需要手动选「仍要运行」——这是所有未签名开源二进制的通病。
- Windows 上的行为由 CI 持续验证（单元测试 + 真实 `.exe` 的端到端测试，覆盖热重载与 SSE），但还没有真实 Windows 用户环境的反馈。

## 快速开始

```bash
mkdir mysite && cd mysite
# 写你的页面、模板与静态资源……
goweb                # 浏览器打开 http://localhost:8080/
goweb gen            # 产出 dist/，直接部署
```

一个最小站点长这样：

```
mysite/
├── index.html              -> /          产物 dist/index.html
├── about.html              -> /about.html 产物 dist/about.html
├── help/74.html            -> /help/74.html
├── templates/              局部模板与数据，不产出页面、不可直接访问
│   ├── header.html
│   └── data.json
└── static/                 资源目录，原样拷贝进产物
    └── css/site.css
```

`templates/header.html`：

```html
{{define "header"}}
<header class="site-header">
  <a href="/">首页</a>
  <a href="/about/">关于</a>
  <span>{{.site.title}}</span>
</header>
{{end}}
```

`templates/data.json`：

```json
{
  "site": { "title": "我的站点", "beian": "京ICP备000000号" },
  "year": 2026
}
```

`about.html`：

```html
<!DOCTYPE html>
<html lang="zh">
  <head>
    <meta charset="utf-8" />
    <title>关于 - {{.site.title}}</title>
    <link rel="stylesheet" href="/static/css/site.css" />
  </head>
  <body>
    {{template "header" .}}
    <main>关于我们</main>
    <footer>© {{.year}} {{.site.title}} · {{.site.beian}}</footer>
  </body>
</html>
```

`goweb gen` 的输出：

```console
$ goweb gen
time=2026-10-20T15:15:33+08:00 level=INFO msg=静态站已生成 out=/Users/me/mysite/dist pages=2 assets=1 data=/Users/me/mysite/templates/data.json

$ find dist -type f
dist/about.html
dist/index.html
dist/static/css/site.css
```

产物路径严格跟随源码路径：源码里的 `about.html` 就产出 `dist/about.html`。

## 目录约定

| 路径 | 作用 |
| --- | --- |
| 页面目录（`--dir`，默认当前目录） | 其中递归出现的 `*.html` 都是页面，产物同名同路径 |
| `--template`（默认 `templates/`） | 局部模板目录：内部 html 只参与模板解析，不产出页面、也不能被直接访问；`data.json` 也放在这里 |
| 其他任意文件/目录 | 视为静态资源，`gen` 时原样拷贝进产物、`serve` 时直接对外提供 |

扫描时固定忽略：

- 以 `.` 开头的文件与目录（`.git`、`.DS_Store` 等）
- `node_modules/`
- 产物目录（`serve` 固定忽略 `dist/`，`gen` 忽略 `--out` 指定的目录）

局部模板目录与产物目录内的文件都不会被 `serve` 对外提供（直接访问返回 404）。

## 模板

所有页面与局部模板解析进**同一个 `html/template` 命名空间**，因此局部模板里 `define` 的名字可以被任意页面引用：

```html
<!-- templates/header.html -->
{{define "header"}}<header>SITE-HEADER</header>{{end}}
```

```html
<!-- about.html -->
{{template "header" .}}
<main>关于我们</main>
```

### 内置模板函数

命名规则：**大写输出「不转义的安全类型」**（信任边界），小写驼峰是普通工具。

| 函数 | 用法 | 说明 |
| --- | --- | --- |
| `HTML` | `{{"<b>粗</b>"\|HTML}}` | 原样输出 HTML，不再转义 |
| `JS` | `<script>const c = {{JS "x"}};</script>` | 值编码为 JSON 字面量 |
| `asset` | `{{asset "/static/css/site.css"}}` | → `/static/css/site.css?v=<内容哈希>`，缓存失效用；文件不存在、指向页面或落在模板目录/忽略项里都会**直接报错** |
| `default` | `{{.site.title \| default "未命名"}}` | 值为空（nil、`""`、0、false、空切片/映射）时用兜底值，补「缺键渲染为空」的坑 |
| `fail` | `{{if not .site.title}}{{fail "缺少 site.title"}}{{end}}` | 渲染直接失败：`gen` 非零退出、`serve` 回 500，消息带出来 |
| `dict` / `list` | `{{template "card" dict "title" .t "url" .u}}` | 构造 map / 切片，给局部模板传多个参数 |
| `upper` `lower` | `{{upper "abc"}}` | 大小写 |
| `trimPrefix` `trimSuffix` | `{{.anchor \| trimPrefix "#"}}` | 去前后缀 |
| `replace` | `{{.name \| replace " " "-"}}` | 全部替换 |
| `join` | `{{join "," (list "a" "b")}}` | 用分隔符连接切片 |
| `truncate` | `{{.title \| truncate 20}}` | 按**字符**截断并补 `…` |
| `charCount` | `{{charCount .title}}` | 按**字符**计数 |
| `dev` / `prod` | `{{if prod}}<script src="/stat.js"></script>{{end}}` | 当前渲染模式 |
| `buildTime` | `{{buildTime \| date "2006-01-02"}}` | 构建时间（见下） |
| `year` | `{{year}}` 或 `{{.since \| year}}` | 取年份 |
| `date` | `{{buildTime \| date "2006-01-02 15:04"}}` | 按 Go 时间布局格式化；接受 `time.Time`、Unix 秒、`2020-03-04` / RFC3339 字符串 |

> **中文站点注意**：Go 模板内置的 `len`、`slice`、`index` 对字符串是**字节**语义（`{{len "你好世界"}}` 得到 `12`，`{{slice "你好世界" 0 3}}` 会把字切碎）。要用 `charCount` / `truncate` 这类按字符的工具。

### 模式与构建时间

`--mode` 控制模板里的 `dev` / `prod`：

| 值 | 行为 |
| --- | --- |
| `auto`（默认） | `serve` 用 `dev`、`gen` 用 `prod` |
| `dev` / `prod` | 显式指定，两个子命令都接受 |

典型用途是「统计脚本只进线上产物」：`{{if prod}}<script src="/stat.js"></script>{{end}}`。想在本地预览生产产物：`goweb serve --livereload=false --mode prod`（关掉注入才是与 `gen` 逐字节一致的 HTML）。

构建时间默认取**站点内容里最新的文件修改时间**——同一份输入产出同一份结果，`gen` 可复现；需要在 CI 里彻底固定时显式指定：

```bash
goweb gen --build-time 2026-03-05T10:30:00Z
```

## 模板数据

`templates/data.json` 的**顶层对象就是模板的渲染数据**，页面与局部模板共用同一份（`{{template "header" .}}` 里也能直接取）：

```json
{
  "site": { "title": "我的站点", "beian": "京ICP备000000号" },
  "year": 2026,
  "nav": [
    { "name": "首页", "url": "/" },
    { "name": "关于", "url": "/about/" }
  ]
}
```

```html
<title>{{.site.title}}</title>
{{range .nav}}<a href="{{.url}}">{{.name}}</a>{{end}}
<footer>© {{.year}} {{.site.beian}}</footer>
```

行为约定：

- 文件不存在表示没有数据，不会报错。
- JSON 语法错误、或顶层不是对象（数组、字符串等）→ `serve` 启动失败、`gen` 非零退出，报错信息带文件路径。
- 数据文件参与热重载：改完 `data.json` 刷新页面即可看到新数据。
- **缺键不报错**：数据里没有的键（例如把 `site.title` 写成 `site.titl`）按 Go 模板原生语义渲染成空字符串，只会静默留白，需要自己核对键名。
- `data.json` 只是模板数据源，不会进入产物目录，也不能被直接访问。
- 数据是**站点级共享**的；页面之间的差异直接写在各自的 html 里。

## URL 规则

`serve` 的解析顺序**等效于 nginx 的 `try_files $uri $uri.html $uri/index.html`**，零重定向、URL 原样保留。
下表的线上两列是实测结果（同一份扁平产物）：只要部署端用这行 `try_files`，本地与线上逐条一致。

| 请求 | 命中方式 | 本地 serve | GH Pages / Netlify / Cloudflare | 裸 nginx | nginx + 该行 `try_files` |
| --- | --- | --- | --- | --- | --- |
| `/about.html` | `$uri`（真实文件） | 200 | 200 | 200 | 200 |
| `/about` | `$uri.html` | 200 | 200 | 404 | 200 |
| `/about/` | 目录不存在 | **404** | 404 | 404 | 404 |
| `/about/index.html` | 无此文件 | **404** | 404 | 404 | 404 |
| `/help` | `$uri/index.html` | 200 | 301 → `/help/` | 301 → `/help/` | 200 |
| `/help/` | 目录 + index | 200 | 200 | 200 | 200 |
| `/static/css/site.css` | `$uri`（静态文件） | 200 | 200 | 200 | 200 |
| `//about//` | 归一化成 `/about/` | 404 | 404 | 404 | 404 |
| `/a/../about` | 归一化成 `/about` | 200 | 200 | 200 | 200 |
| `/templates/*`、`/dist/*` | 忽略项视为不存在 | 404 | 404 | 404 | 404 |
| `/__goweb/livereload` | 热重载 SSE 端点（保留路径） | 200（`--livereload=false` 时为 404） | — | — | — |
| 其他 | 都不命中 | 纯文本 `404` | 404 | 404 | 404 |

要点：

- **`/about` 能开、`/about/` 打不开**，这不是 bug：扁平产物里没有 `about/` 目录，`$uri` 的目录检查必然失败——nginx 实测同样是 404。
- 非规范路径（`//about//`、`/a/../about`、`/about/.`）**静默归一化，不重定向**，与 nginx 的 `merge_slashes` + `remove_dot_segments` 行为一致。
- 局部模板目录、产物目录、隐藏项与 `node_modules` 一律视为不存在，与产物内容保持一致。
- 因此**站内链接写 `/about.html` 最稳**（任何托管都必然可用）；想让无扩展名写法在线上也可用，见[部署](#部署)里的那一行 `try_files`。
- `/__goweb/` 是 goweb 的保留前缀，站点里同名文件会让位给内置端点。

非 GET/HEAD 请求返回 `405`；开发期响应带 `Cache-Control: no-store`，避免改完文件被浏览器旧副本挡住。

## 浏览器热重载

`goweb serve` 默认开启浏览器热重载：站点文件一改，浏览器自己刷新，不用手动按 F5。

```
站点文件变更 ──fsnotify──▶ 防抖 150ms ──▶ 重载模板与 data.json
                                              │ 成功
                                              ▼
                        广播到 /__goweb/livereload ──SSE──▶ 页面 location.reload()
```

- **注入只在 serve**：页面 HTML 在 `</body>` 之前被插入一小段脚本（带 `data-goweb-livereload` 标记），`goweb gen` 的产物**不含任何脚本**——有测试保证。
- **端点**：`GET /__goweb/livereload`，`text/event-stream`，20 秒发一次保活注释；断线由浏览器自动重连。
- **触发范围**：页面、局部模板、`templates/data.json`、css/js/图片等任何被监听的站点文件；变更一律整页刷新（不做 CSS 热替换）。
- **只在成功时刷新**：模板改到一半（语法错误）时服务端保留上一份可用内容、终端打印错误，但**不**通知浏览器刷新——避免刷新后看到的还是旧内容而误以为没生效。
- **关掉它**：`goweb serve --livereload=false`（`-l=false`）。关闭后不注入脚本、端点返回 404，开发时看到的 HTML 与线上产物完全一致。

## 生成静态站

```bash
goweb gen                          # 产出到 dist/
goweb gen --out public             # 换产物目录（-o）
goweb gen --clean=false            # 不清空，只覆盖同名文件（-c=false）
goweb gen --dir site --template templates
```

产物路径与源码路径一一对应：

```
源码                          产物
index.html              →    dist/index.html
about.html              →    dist/about.html
help/index.html         →    dist/help/index.html
help/74.html            →    dist/help/74.html
static/css/site.css     →    dist/static/css/site.css
robots.txt              →    dist/robots.txt
templates/**            →    （不产出、不拷贝）
```

- 页面按源码路径落盘：`about.html` → `dist/about.html`，不再做 `<页面>/index.html` 的目录式转换。
- 除页面、局部模板目录、忽略项之外的所有文件**原样拷贝**，相对路径不变。
- 生成前清空产物目录（`--clean=false` 关闭）。护栏：产物目录不能是页面目录本身、页面目录的父目录、局部模板目录，也不能是用户主目录；命中直接报错，不会删任何东西。
- 任一页面渲染失败即整体失败、非零退出，不做静默跳过。

## 部署

产物是纯静态文件，任何静态托管都能直接用。

```nginx
server {
    listen 80;
    root /var/www/mysite/dist;
    index index.html;

    # 与 goweb serve 完全等效：无扩展名 URL（/about）也直接命中 about.html
    location / {
        try_files $uri $uri.html $uri/index.html =404;
    }
}
```

- **nginx**：把 `dist/` 拷进去即可，`/about.html` 与 `/help/` 开箱可用；加上面这行 `try_files` 后，本地 `goweb serve` 与线上**逐条一致**（含 `/about` 200、`/about/` 404）。
- **GitHub Pages / Cloudflare Pages / Netlify / Vercel**：构建命令填 `goweb gen`，发布目录填 `dist`；这些平台原生支持 `/about` 这类无扩展名 URL，与本地也一致。
- **Apache / Caddy**：默认配置下 `/about.html` 可用；要支持无扩展名 URL 需自己配 rewrite（与 `try_files` 等效的规则）。
- **对象存储（OSS/COS/S3）**：开启静态网站托管并设置索引文档为 `index.html`。

> 直接用 `file://` 双击打开 `dist/index.html` 时，`/static/...` 这类**以 `/` 开头的绝对路径会失效**（根变成了文件系统根）。本地预览请用 `goweb serve`，或把资源链接改成相对路径。

## 命令与参数

```bash
goweb [serve] [--addr :8080] [--dir .] [--template templates]
goweb gen     [--dir .] [--template templates] [--out dist] [--clean=true]
goweb completion bash|zsh|fish|powershell      # 生成 shell 补全脚本
goweb help [command]
goweb --version
```

`goweb` 不带子命令时等价于 `goweb serve`；`--dir` 与 `--template` 是公共参数，写在子命令前后都可以（`goweb --dir site gen` 与 `goweb gen --dir site` 等价）。

| 参数 | 短选项 | 适用 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `--addr` | `-a` | serve（含不带子命令） | `:8080` | 监听地址 |
| `--dir` | `-d` | 全部 | `.` | 页面目录 |
| `--template` | `-t` | 全部 | `templates` | 局部模板目录（相对页面目录），`data.json` 也在其中；置空表示没有 |
| `--out` | `-o` | gen | `dist` | 产物目录 |
| `--clean` | `-c` | gen | `true` | 生成前清空产物目录 |
| `--livereload` | `-l` | serve（含不带子命令） | `true` | 注入热重载脚本并开放 SSE 端点；`--livereload=false` 关闭 |
| `--mode` | — | 全部 | `auto` | 渲染模式：`auto`（serve=dev、gen=prod）、`dev`、`prod` |
| `--build-time` | — | 全部 | 空（取站点最新修改时间） | 模板里的构建时间，RFC3339 或 `2006-01-02` |
| `--version` | `-v` | 根命令（`goweb --version`） | — | 打印版本号（`dev`，除非构建时用 `-ldflags` 注入） |
| `--help` | `-h` | 全部 | — | 查看帮助，如 `goweb gen --help` |

启用 shell 补全（以 zsh 为例）：

```bash
goweb completion zsh > "${fpath[1]}/_goweb"
```

## 设计取舍

- **开发服务器等效 `try_files`**：解析顺序就是 `try_files $uri $uri.html $uri/index.html`，零重定向、不做 URL 重写；本地结果与用同一行配置部署的线上结果逐条一致（`/about` 能开、`/about/` 404 都一致），站内链接写 `/about.html` 最稳。
- **单一 `html/template` 命名空间**：所有页面与局部模板一起解析，`{{template "x" .}}` 全局可用；代价是不同文件里同名 `define` 会互相覆盖。
- **不做缺键校验**：JSON 直接解成 map 当模板数据，缺键渲染成空串而不是报错，实现最简且不会误伤 `range`/`with` 内部取值；代价是拼错键名会静默留白。
- **浏览器热重载是可选的注入**：`serve` 默认注入刷新脚本，`gen` 产物永远干净；用 `--livereload=false` 可让开发时看到的 HTML 与线上完全一致。
- **渲染失败即失败**：宁可让 `gen` 报错退出，也不产出一个缺页面的半成品静态站。
- **用 cobra 而不是标准库 `flag`**：换来统一的 `--help`/`--version`/shell 补全与更清晰的子命令结构，代价是多两个间接依赖（pflag、mousetrap）；公共参数（`--dir`/`--template`）做成 persistent flag，保证 `goweb --dir site gen` 不会被静默忽略。

## 目前不做

CSS 热替换（改样式仍整页刷新）、自定义 404 页面、多语言（i18n）、资源内容哈希/指纹、每页独立数据（只提供站点级 `data.json`）。

页面、模板、`data.json` 与静态资源变更后都会自动重载，并通过热重载让浏览器自动刷新。

## 开发

```bash
go build ./...
go vet ./...
go test -short ./...          # 单元测试（跳过 e2e，秒级）
go test ./...                 # 单元 + 端到端
go test -race -count=1 ./...  # 完整（CI 同款）
go test -run TestE2E -v ./... # 只看端到端
go test -short -cover ./...   # 覆盖率
```

源码结构（`package main`，扁平分层，不做过度抽象）：

| 文件 | 职责 |
| --- | --- |
| `main.go` | 子命令与参数分发 |
| `site.go` | 页面发现、模板命名空间、`data.json` 加载、URL 路由解析、渲染 |
| `serve.go` | HTTP 处理、访问日志、fsnotify 防抖重载 |
| `gen.go` | 静态产物落盘、资源拷贝、清空护栏 |
| `funcs.go` | 内置模板函数（`asset`/`default`/`fail`/`dict`/文本工具/模式与时间）与其加载上下文 |

### 测试分层

测试只打公开边界，不针对内部实现；每一层都有明确的观察面：

| 测试 | 边界 | 覆盖内容 |
| --- | --- | --- |
| `site_test.go` | `LoadSite` / `Reload` / `Render` / `Resolve` / `WatchPaths` | 页面发现与忽略规则、try_files 三候选顺序与路径归一化、data.json 语义、坏模板保留上一版、并发渲染与重载 |
| `gen_test.go` | `Generate` | 产物树、资源拷贝（嵌套目录/符号链接/忽略项）、清空护栏、快速失败、错误路径 |
| `serve_test.go` | `newHandler` / `watchSite` | 状态码与响应头、访问日志、渲染失败回 500、fsnotify 重载与失败韧性、新增文件被发现、热重载注入范围与广播时机 |
| `livereload_test.go` | `liveReloadHub` / `injectLiveReload` | 脚本注入位置（`</body>` 前/追加/大小写）、订阅与注销、慢客户端不阻塞广播 |
| `main_test.go` | cobra 命令树 | 参数默认值与短选项、公共参数前置、错误前缀与 usage、`--version` 注入 |
| `e2e_test.go` | **真实二进制 + 真实 HTTP** | 编译 goweb → 跑 `gen`（产物清单、幂等、护栏退出码、坏数据失败、产物不含热重载脚本）与 `serve`（全部 URL 语义、405/404、缓存头、SSE 推送刷新事件、`--livereload=false`、坏模板韧性、`--version`/`help`/`completion`） |

端到端测试用 `--addr 127.0.0.1:0` 让内核分配端口，再从启动日志里读回真实地址，因此不会与并行任务抢端口；`go test -short` 会跳过它（需要编译与监听）。

测试质量本身经过故障注入验证：分别破坏模板目录隔离、尾斜杠语义、产物目录护栏与解析错误处理，对应测试都会立刻变红。

CI 在 Ubuntu、macOS 与 Windows 上运行 `gofmt` 检查、`go mod tidy` 校验、`go vet`、单元测试（含覆盖率与竞态检测）与端到端测试。竞态检测需要 C 编译器，镜像里没有时该步骤会打印一条 notice 并跳过（不静默降级）。

### 发布

推一个 `v*` 标签即触发 [release.yml](.github/workflows/release.yml)：先在 Ubuntu 上跑完整测试，再由 GoReleaser 按 [.goreleaser.yaml](.goreleaser.yaml) 交叉编译 macOS/Linux 的 amd64 与 arm64、生成 `checksums.txt` 并创建 GitHub Release。

```bash
# 本地预演一次完整打包（产物落在 dist/，不会发布）
goreleaser release --snapshot --clean
```

## 贡献

欢迎 Issue 与 PR：

1. Fork 并新建分支。
2. 提交前确保 `gofmt -l .` 无输出、`go vet ./...` 与 `go test ./...` 全部通过。
3. 新增行为请补上对应的单元测试。
4. Commit message 沿用 `<type>(<scope>): <summary>`（如 `feat(gen): 支持 --clean=false`）。

## 许可证

[MIT](LICENSE) © 2026 lixiangzhong
