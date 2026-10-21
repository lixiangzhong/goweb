# goweb

> 面向前端开发的极简静态站工具：写完 HTML 就能预览，一条命令产出可直接部署的纯静态站。

[![CI](https://github.com/lixiangzhong/goweb/actions/workflows/ci.yml/badge.svg)](https://github.com/lixiangzhong/goweb/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/lixiangzhong/goweb)](https://github.com/lixiangzhong/goweb/blob/HEAD/go.mod)
[![License](https://img.shields.io/github/license/lixiangzhong/goweb)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](#贡献)

`goweb` 是一个 Go 写的单二进制静态站工具，只有四个子命令：

- **`goweb init`** —— 生成默认配置文件 `.goweb.yml`（每个键都写出默认值并带注释）。
- **`goweb check`** —— 只校验不产出：跑 `gen` 的全套检查（模板、渲染、忽略规则、产物路径冲突），外加渲染结果里的站内链接检查，问题一次列全。
- **`goweb serve`** —— 在站点目录起开发服务器（别名 `goweb dev`，两者完全等价）。产物与源码一一对应，开发服务器不做任何尾斜杠/扩展名重定向，改完模板或数据自动重新加载。
- **`goweb gen`** —— 把页面渲染成纯静态站，产出 `dist/`，拷到任何静态托管即可上线（别名 `goweb build`，两者完全等价）。

不需要 npm、webpack、vite，只依赖标准库 + [cobra](https://github.com/spf13/cobra) + [fsnotify](https://github.com/fsnotify/fsnotify) + [x/net/html](https://pkg.go.dev/golang.org/x/net/html) + [doublestar](https://github.com/bmatcuk/doublestar) + [yaml.v3](https://pkg.go.dev/go.yaml.in/yaml/v3)；配置文件可选，不写也能跑。

## 特性

- **零配置零构建**：站点就是一堆 `.html` + css/js，写完直接预览，不用装任何前端工具链。
- **产物跟随源码路径**：`about.html` → `dist/about.html`，`help/74.html` → `dist/help/74.html`，产物与源码一一对应，部署后 `file://` 也能直接打开。
- **开发服务器等效 `try_files`**：解析顺序就是 nginx 的 `try_files $uri $uri.html $uri/index.html`，零重定向；本地结果与用同一行配置部署的线上结果逐条一致（含 404）。
- **复用页头页脚**：直接用 Go 的 `html/template`，`{{define}}` + `{{template}}` 抽公共片段，不需要学新语法。
- **站点级数据**：`templates/data.json` 提供站名、备案号、导航等共享变量，页面与局部模板都能取。
- **保存即可见**：页面、模板、`data.json` 或静态资源变更后自动重载，并通过 SSE 让浏览器自动刷新；改到一半的坏模板不会让服务挂掉，也不会触发刷新。
- **端口自动避让**：没显式指定端口时，`:8080` 被占用会依次试 `:8081…:8089`，日志里说明实际地址；显式指定端口则保持快速失败。
- **终端里的日志看得清**：时间戳是 `2006-01-02 15:04:05`，访问日志只留 `method`/`path`/`status`，非 2xx 的状态码在终端里按 3xx 青／4xx 黄／5xx 红上色（重定向或管道时自动不上色，`NO_COLOR` 可关闭）。
- **启动即开浏览器**：`goweb serve` 监听成功后自动用系统默认浏览器打开站点（`--open=false` 关闭；CI/SSH 里打不开只记日志，不影响服务）。
- **缓存失效自动做**：页面里写普通的 `/static/css/site.css`，`gen` 与 `serve` 都会自动补上 `?v=<内容哈希>`；外链、`data:`、已带查询串的写法一律不动（`--fingerprint=false` 可关闭）。
- **交付前先自查**：`goweb check` 跑 `gen` 的全套检查（模板、渲染、忽略规则、产物路径冲突）外加站内链接检查，**不写任何产物**，问题一次列全。
- **一条命令静态化**：`goweb gen` 原地产出 `dist/`，其余文件按各自所属的根拷贝；默认压缩 HTML/CSS/JS/JSON/SVG/XML（只去空白与注释，不改 JS 变量名，`--minify=false` 关闭）。
- **危险操作有护栏**：清空产物目录前会拦截项目根、页面/模板/资源三个根本身及其父目录，也不会把用户主目录当产物目录。
- **快速失败**：任一页面渲染失败即整体失败、非零退出，绝不把半成品产物当成成功发布。

## 安装

需要 Go 1.26 或更高版本（`x/net` 的要求；预编译二进制不需要 Go 环境）。

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
goweb init           # 可选：生成带注释的 .goweb.yml（不生成也不影响任何功能）
# 写你的页面、模板与静态资源……
goweb                # 浏览器打开 http://localhost:8080/（自动开浏览器，--open=false 关闭）
goweb dev            # 与 goweb serve 等价（别名）
goweb check          # 只校验不产出：链接、模板、产物路径冲突一次查完
goweb gen            # 产出 dist/，直接部署（别名 goweb build 等价）
```

一个最小站点长这样（页面、模板、资源三个根彼此独立）：

```
mysite/                      ← 项目根：.goweb.yml 所在目录，没有配置文件时就是运行目录
├── pages/                   ← 页面根（--dir pages）：递归的 *.html 都是页面
│   ├── index.html           -> /            产物 dist/index.html
│   ├── about.html           -> /about.html  产物 dist/about.html
│   └── help/74.html         -> /help/74.html
├── templates/               ← 模板根（--template templates）：局部模板与数据，不产出页面、不可访问
│   ├── header.html
│   └── data.json
├── static/                  ← 资源（在资源根 --assets，默认项目根 之下）：按相对路径拷贝进产物
│   └── css/site.css
└── .goweb.yml               可选：把上面的约定写进配置，免得到处敲 flag
```

对应的 `.goweb.yml`：

```yaml
dir: pages            # 页面根
template: templates   # 模板根（不受 dir 影响）
assets: .             # 资源根
out: dist             # gen 产物目录
gitignore: true       # 读项目根的 .gitignore（默认就是 true）
ignore:               # 额外的忽略模式，与 .gitignore 叠加
  - "**/*.map"
```

`--dir` 只决定「页面在哪、URL 与产物怎么落」，**不会影响模板与资源的加载**——这是与早期版本最重要的差别（见[目录约定](docs/layout.md)）。

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
time="2026-10-21 10:06:47" level=INFO msg=静态站已生成 out=/Users/me/mysite/dist pages=2 assets=1 data=/Users/me/mysite/templates/data.json mode=prod buildTime=2026-10-21T10:06:46+08:00 fingerprint=true

$ find dist -type f
dist/about.html
dist/index.html
dist/static/css/site.css
```

页面产物相对**页面根**：`pages/about.html` 产出 `dist/about.html`，页面根那一层不会出现在 URL 与产物里；资源按所属根落盘，因此 URL 与产物路径始终一致（`/static/css/site.css` ↔ `dist/static/css/site.css`）。

## 文档

深入内容在这里，项目概览就是你正在读的这份 README：

- [文档索引](docs/README.md) —— 全部深入文档的入口与一句话导航。
- [目录约定](docs/layout.md) —— 项目根、页面/模板/资源三个根与产物映射。
- [模板](docs/templates.md) —— 单一模板命名空间、内置模板函数与 dev/prod 模式。
- [模板数据](docs/data.md) —— `templates/data.json` 的渲染语义与行为约定。
- [静态资源与缓存失效](docs/assets.md) —— 自动补 `?v=<内容哈希>` 的规则与关闭方式。
- [URL 规则](docs/routing.md) —— `serve` 的解析顺序与本地/线上逐条对照。
- [浏览器热重载](docs/livereload.md) —— 文件监听、SSE 广播与注入边界。
- [生成静态站](docs/generate.md) —— `goweb gen` 的产物映射与清空护栏。
- [部署](docs/deploy.md) —— nginx 配置与各静态托管平台差异。
- [命令与参数](docs/cli.md) —— 全部参数、`.goweb.yml` 与 shell 补全。
- [设计取舍与边界](docs/design.md) —— 关键设计取舍与目前不做的范围。
- [开发与贡献](docs/development.md) —— 本地开发、测试分层、发布与贡献约定。

## 贡献

欢迎 Issue 与 PR。提交前请确保 `gofmt -l .` 无输出、`go vet ./...` 与 `go test ./...` 全部通过，新增行为补上对应的单元测试；Commit message 沿用 `<type>(<scope>): <summary>`（如 `feat(gen): 支持 --clean=false`）。

开发命令、测试分层、发布流程与源码结构的详细说明见[开发与贡献](docs/development.md)。

## 许可证

[MIT](LICENSE) © 2026 lixiangzhong
