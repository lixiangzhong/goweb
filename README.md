# goweb

面向前端开发的极简静态站工具：一条命令起本地服务器写 HTML/CSS/JS，一条命令把模板渲染成可直接部署的纯静态网页。

- `goweb serve`：启动开发服务器。页面 URL 与最终静态托管（nginx / GitHub Pages / `file://`）完全一致，改完模板自动重编译。
- `goweb gen`：把页面渲染成纯静态站，产出 `dist/`，随手拷到任何静态托管即可上线。

只依赖标准库 + `fsnotify`，单个二进制，无配置文件。

## 快速开始

```bash
go build -o goweb .

cd 你的站点目录
goweb                # 打开 http://localhost:8080/
goweb gen            # 产出 dist/
```

一个最小站点：

```
站点目录/
├── index.html              -> /
├── about.html              -> /about/
├── help/
│   ├── index.html          -> /help/
│   └── 74.html             -> /help/74/
├── templates/              局部模板目录，不产出页面
│   ├── header.html
│   ├── footer.html
│   └── data.json           模板数据，页面与局部模板共用
└── static/                 资源目录，原样拷贝进产物
    ├── css/site.css
    └── js/site.js
```

## 命令与参数

```bash
goweb [serve] [-addr :8080] [-dir .] [-template templates]
goweb gen     [-dir .] [-template templates] [-out dist] [-clean=true]
goweb help
```

| 参数 | 适用 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-addr` | serve | `:8080` | 监听地址 |
| `-dir` | 全部 | `.` | 页面目录 |
| `-template` | 全部 | `templates` | 局部模板目录（相对页面目录），数据文件也在其中；置空表示没有 |
| `-out` | gen | `dist` | 产物目录 |
| `-clean` | gen | `true` | 生成前清空产物目录 |

## 模板写法

所有页面与局部模板解析进同一个 `html/template` 命名空间，因此局部模板里 `define` 的名字可以被任意页面引用：

```html
<!-- templates/header.html -->
{{define "header"}}
<header class="site-header"><a href="/">首页</a></header>
{{end}}
```

```html
<!-- about.html -->
<!DOCTYPE html>
<html>
  <head><link rel="stylesheet" href="/static/css/site.css"></head>
  <body>
    {{template "header" .}}
    <main>关于我们</main>
  </body>
</html>
```

### 模板数据：templates/data.json

`templates/data.json` 的顶层对象就是模板的渲染数据，页面与局部模板共用同一份（`{{template "header" .}}` 里也能直接取）：

```json
{
  "site": { "title": "我的站点", "beian": "京ICP备xxxx号" },
  "year": 2026,
  "nav": [{ "name": "首页", "url": "/" }, { "name": "关于", "url": "/about/" }]
}
```

```html
<title>{{.site.title}}</title>
{{range .nav}}<a href="{{.url}}">{{.name}}</a>{{end}}
<footer>© {{.year}} {{.site.beian}}</footer>
```

- 文件不存在表示没有数据，不报错。
- JSON 语法错误、或顶层不是对象（数组/字符串等）会直接报错：serve 启动失败、gen 退出码非零。
- 数据文件参与热重编译：改 `data.json` 后刷新页面即可看到新数据。
- **缺键不报错**：数据里没有的键（例如把 `site.title` 写成 `site.titl`）按 Go 模板原生语义渲染成空字符串，只会静默留白，需要自行核对键名。
- `data.json` 只是模板数据源，不会进产物目录。

内置模板函数只有两个：

```html
{{"<b>加粗</b>"|HTML}}                 <!-- 原样输出 HTML，不转义 -->
<script>const cfg = {{JS "hello"}};</script>   <!-- 值编码为 JSON 字面量 -->
```

单页专属数据（例如每页不同的 title）不在设计范围内：`data.json` 是站点级共享数据，页面差异直接写在各自的 html 里。

## URL 规则

| 请求 | 结果 |
| --- | --- |
| `/` | 渲染 `index.html` |
| `/about/` | 渲染 `about.html` |
| `/about`、`/about.html` | `301` 到 `/about/` |
| `/index.html` | `301` 到 `/` |
| `/help/` | 先找 `help/index.html`，回退 `help.html` |
| `/help/74/` | 渲染 `help/74.html` |
| `/static/css/site.css` | 磁盘文件直出 |
| `/templates/header.html` | `404`：局部模板不可访问 |
| 其余 | 纯文本 `404`（无自定义 404 页面、不做目录列表） |

页面 URL 一定以 `/` 结尾，和静态托管对 `<目录>/index.html` 的解析方式一致，因此本地预览不会出现「本地能跑、线上裂开」。

## gen 产物

```
dist/
├── index.html
├── about/index.html
├── help/index.html
├── help/74/index.html
├── static/...             （原样拷贝）
└── robots.txt             （原样拷贝）
```

- 页面按目录式落盘：`about.html` → `dist/about/index.html`。
- 除页面、局部模板目录、忽略项之外的**所有文件原样拷贝**，相对路径不变。
- 生成前清空产物目录（`-clean=false` 可关闭）。护栏：产物目录不能是页面目录本身、页面目录的父目录、局部模板目录，也不能是用户主目录；命中直接报错退出，不会删任何东西。
- 任一页面渲染失败即整体失败、非零退出，不做静默跳过。

## 忽略规则

扫描（页面发现、资源拷贝、文件监听）时固定忽略：

- 以 `.` 开头的文件与目录（`.git`、`.DS_Store` 等）
- `node_modules/`
- `serve` 固定忽略 `dist/`；`gen` 忽略 `-out` 指定的产物目录

## 与参考实现（`ustat/www/server`）的差异

本项目参考了 `ustat/www` 中 `server` 包的做法：所有 html 解析进同一模板命名空间、URL 去扩展名查模板、未命中模板再回退静态文件、模板变更自动重编译。

刻意的差异：

| 项 | `ustat/www` | goweb |
| --- | --- | --- |
| 依赖 | gin + unrolled/render | 标准库 + fsnotify |
| 模板函数 | i18n、DB 查询等一大堆 | 只有 `HTML`、`JS` |
| 渲染数据 | `c.Keys`（中间件注入的运行时数据） | `templates/data.json`（站点级静态数据） |
| URL 语义 | 带扩展名与不带扩展名都能直接 200 | 目录式严格一致，非目录 URL 一律 301 |
| 多语言 | `lang/` 前缀模板 + locales JSON | 不做 |
| 错误页 | `404` 模板 | 纯文本 404 |
| 落地页 | 运行时渲染 | `gen` 产出纯静态 |

## 目前不做

浏览器热刷新（SSE 注入）、CSS 热替换、自定义 404 页面、多语言、资源内容哈希、每页独立数据（`data.json` 是站点级共享数据）。模板与 `data.json` 变更后自动重编译，浏览器手动刷新即可。
