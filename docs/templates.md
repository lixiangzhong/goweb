# 模板

这一篇讲模板解析模型、内置模板函数，以及 dev/prod 模式与构建时间。

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

## 内置模板函数

命名规则：**大写输出「不转义的安全类型」**（信任边界），小写驼峰是普通工具。

| 函数 | 用法 | 说明 |
| --- | --- | --- |
| `HTML` | `{{"<b>粗</b>"\|HTML}}` | 原样输出 HTML，不再转义 |
| `JS` | `<script>const c = {{JS "x"}};</script>` | 值编码为 JSON 字面量 |
| `asset` | `{{asset "/static/css/site.css"}}` | → `/static/css/site.css?v=<内容哈希>`。页面里的**普通路径会被自动补哈希**（见[静态资源与缓存失效](assets.md)），这个函数用于自动改写覆盖不到的地方（CSS `url()`、内联 JS）；文件不存在、指向页面或落在模板目录/忽略项里都会**直接报错** |
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

## 模式与构建时间

`--mode` 控制模板里的 `dev` / `prod`：

| 值 | 行为 |
| --- | --- |
| `auto`（默认） | `serve` 用 `dev`、`gen` 用 `prod` |
| `dev` / `prod` | 显式指定；`serve`/`dev` 与 `gen`/`build` 都接受（`check` 按产出方向用 `auto` 的默认值，即 `prod`） |

典型用途是「统计脚本只进线上产物」：`{{if prod}}<script src="/stat.js"></script>{{end}}`。想在本地预览生产产物：`goweb serve --livereload=false --mode prod`（关掉注入才是与 `gen` 逐字节一致的 HTML）。

构建时间默认取**站点内容里最新的文件修改时间**——同一份输入产出同一份结果，`gen` 可复现；需要在 CI 里彻底固定时显式指定：

```bash
goweb gen --build-time 2026-03-05T10:30:00Z
```

---

[← 返回 README](../README.md)
