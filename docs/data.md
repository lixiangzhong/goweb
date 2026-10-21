# 模板数据

这一篇讲 `templates/data.json` 如何作为站点级渲染数据，以及缺键等行为约定。

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

---

[← 返回 README](../README.md)
