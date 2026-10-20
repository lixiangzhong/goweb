# goweb

> 面向前端开发的极简静态站工具：写完 HTML 就能预览，一条命令产出可直接部署的纯静态站。

[![CI](https://github.com/lixiangzhong/goweb/actions/workflows/ci.yml/badge.svg)](https://github.com/lixiangzhong/goweb/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/lixiangzhong/goweb)](https://github.com/lixiangzhong/goweb/blob/HEAD/go.mod)
[![License](https://img.shields.io/github/license/lixiangzhong/goweb)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](#贡献)

`goweb` 是一个 Go 写的单二进制静态站工具，只有两个子命令：

- **`goweb serve`** —— 在站点目录起开发服务器。页面 URL 与最终静态托管（nginx / GitHub Pages）**严格一致**，改完模板或数据自动重新加载。
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
- **URL 与线上严格一致**：页面 URL 一律目录式（`/about/` ↔ `dist/about/index.html`），本地通过、线上 404 这类问题从根上避免。
- **复用页头页脚**：直接用 Go 的 `html/template`，`{{define}}` + `{{template}}` 抽公共片段，不需要学新语法。
- **站点级数据**：`templates/data.json` 提供站名、备案号、导航等共享变量，页面与局部模板都能取。
- **保存即可见**：模板、页面或数据文件变更后自动重新加载，浏览器刷新即可；改到一半的坏模板不会让服务挂掉。
- **一条命令静态化**：`goweb gen` 原地产出 `dist/`，页面目录式落盘，其余文件原样拷贝。
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
├── index.html              -> /
├── about.html              -> /about/
├── help/74.html            -> /help/74/
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
dist/about/index.html
dist/index.html
dist/static/css/site.css
```

## 目录约定

| 路径 | 作用 |
| --- | --- |
| 页面目录（`--dir`，默认当前目录） | 其中递归出现的 `*.html` 都是页面 |
| `--template`（默认 `templates/`） | 局部模板目录：内部 html 只参与模板解析，不产出页面、也不能被直接访问；`data.json` 也放在这里 |
| 其他任意文件/目录 | 视为静态资源，`gen` 时原样拷贝进产物、`serve` 时直接对外提供 |

扫描时固定忽略：

- 以 `.` 开头的文件与目录（`.git`、`.DS_Store` 等）
- `node_modules/`
- 产物目录（`serve` 固定忽略 `dist/`，`gen` 忽略 `--out` 指定的目录）

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

内置模板函数只有两个：

```html
{{"<b>加粗</b>"|HTML}}                          <!-- 原样输出 HTML，不做转义 -->
<script>const cfg = {{JS "hello"}};</script>    <!-- 值编码为 JSON 字面量 -->
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

`serve` 的路由与 `gen` 的产物布局严格一致，页面 URL 一定以 `/` 结尾：

| 请求 | 结果 |
| --- | --- |
| `/` | 渲染 `index.html` |
| `/about/` | 渲染 `about.html` |
| `/about`、`/about.html` | `301` 到 `/about/` |
| `/index.html` | `301` 到 `/` |
| `/help/` | 先找 `help/index.html`，回退 `help.html` |
| `/help/74/` | 渲染 `help/74.html` |
| `/static/css/site.css` | 磁盘文件直出 |
| `/templates/header.html`、`/templates/data.json` | `404`：局部模板目录整体不可访问 |
| 其他 | 纯文本 `404`（无自定义 404 页面，不做目录列表） |

非 GET/HEAD 请求返回 `405`；开发期响应带 `Cache-Control: no-store`，避免改完文件被浏览器旧副本挡住。

## 生成静态站

```bash
goweb gen                          # 产出到 dist/
goweb gen --out public             # 换产物目录（-o）
goweb gen --clean=false            # 不清空，只覆盖同名文件（-c=false）
goweb gen --dir site --template templates
```

产物布局与源码一一对应：

```
dist/
├── index.html
├── about/index.html
├── help/74/index.html
├── static/...                 （原样拷贝）
└── robots.txt                 （原样拷贝）
```

- 页面按目录式落盘：`about.html` → `dist/about/index.html`（`index` 页落在所在目录的 `index.html`）。
- 除页面、局部模板目录、忽略项之外的所有文件**原样拷贝**，相对路径不变。
- 生成前清空产物目录（`--clean=false` 关闭）。护栏：产物目录不能是页面目录本身、页面目录的父目录、局部模板目录，也不能是用户主目录；命中直接报错，不会删任何东西。
- 任一页面渲染失败即整体失败、非零退出，不做静默跳过。

## 部署

产物是纯静态文件，任何静态托管都能直接用，**不需要 rewrite 规则**：

```nginx
server {
    listen 80;
    root /var/www/mysite/dist;
    index index.html;
}
```

- **nginx / Apache / Caddy**：把 `dist/` 内容拷进去即可；目录式产物配合 `index index.html` 天然支持 `/about/` 这类 URL。
- **GitHub Pages / Cloudflare Pages / Netlify / Vercel**：构建命令填 `goweb gen`，发布目录填 `dist`。
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
| `--version` | `-v` | 根命令（`goweb --version`） | — | 打印版本号（`dev`，除非构建时用 `-ldflags` 注入） |
| `--help` | `-h` | 全部 | — | 查看帮助，如 `goweb gen --help` |

启用 shell 补全（以 zsh 为例）：

```bash
goweb completion zsh > "${fpath[1]}/_goweb"
```

## 设计取舍

- **目录式 URL 优先**：`/about` 与 `/about.html` 一律 301 到 `/about/`，与静态托管对 `<目录>/index.html` 的解析方式对齐，代价是地址栏多一个尾斜杠。
- **单一 `html/template` 命名空间**：所有页面与局部模板一起解析，`{{template "x" .}}` 全局可用；代价是不同文件里同名 `define` 会互相覆盖。
- **不做缺键校验**：JSON 直接解成 map 当模板数据，缺键渲染成空串而不是报错，实现最简且不会误伤 `range`/`with` 内部取值；代价是拼错键名会静默留白。
- **开发期不做浏览器热刷新**：只重载模板与数据，不注入任何脚本，`gen` 产物因此天然干净。
- **渲染失败即失败**：宁可让 `gen` 报错退出，也不产出一个缺页面的半成品静态站。
- **用 cobra 而不是标准库 `flag`**：换来统一的 `--help`/`--version`/shell 补全与更清晰的子命令结构，代价是多两个间接依赖（pflag、mousetrap）；公共参数（`--dir`/`--template`）做成 persistent flag，保证 `goweb --dir site gen` 不会被静默忽略。

## 目前不做

浏览器热刷新（SSE 注入）、CSS 热替换、自定义 404 页面、多语言（i18n）、资源内容哈希/指纹、每页独立数据（只提供站点级 `data.json`）。

模板与 `data.json` 变更后自动重载，浏览器手动刷新即可。

## 开发

```bash
go build ./...
go vet ./...
go test ./...          # 含真实 fsnotify 文件监听用例
go test -race ./...
```

源码结构（`package main`，扁平分层，不做过度抽象）：

| 文件 | 职责 |
| --- | --- |
| `main.go` | 子命令与参数分发 |
| `site.go` | 页面发现、模板命名空间、`data.json` 加载、URL 路由解析、渲染 |
| `serve.go` | HTTP 处理、访问日志、fsnotify 防抖重载 |
| `gen.go` | 静态产物落盘、资源拷贝、清空护栏 |
| `funcs.go` | 内置模板函数 `HTML`、`JS` |

CI 在 Ubuntu 与 macOS 上运行 `gofmt` 检查、`go mod tidy` 校验、`go vet` 与 `go test -race`。

## 贡献

欢迎 Issue 与 PR：

1. Fork 并新建分支。
2. 提交前确保 `gofmt -l .` 无输出、`go vet ./...` 与 `go test ./...` 全部通过。
3. 新增行为请补上对应的单元测试。
4. Commit message 沿用 `<type>(<scope>): <summary>`（如 `feat(gen): 支持 --clean=false`）。

## 许可证

[MIT](LICENSE) © 2026 lixiangzhong
