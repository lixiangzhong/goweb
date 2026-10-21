# 生成静态站

这一篇讲 `goweb gen`（别名 `goweb build`，两者等价）的产物路径映射、拷贝规则与清空护栏。

```bash
goweb gen                          # 产出到 dist/
goweb gen --out public             # 换产物目录（-o）
goweb gen --clean=false            # 不清空，只覆盖同名文件（-c=false）
goweb gen --dir pages --assets .   # 三个根都可配（详见「目录约定」）
```

产物路径按「所属根」映射，因此 URL 与产物路径始终一致：

```
页面根内的页面（--dir，默认 .）
pages/index.html        →    dist/index.html        （对应 URL /）
pages/about.html        →    dist/about.html        （对应 URL /about.html）
pages/help/74.html      →    dist/help/74.html

页面根内的非 html 文件（同样按页面根）
pages/img/logo.png      →    dist/img/logo.png      （对应 URL /img/logo.png）

页面根外的文件（按资源根 --assets，默认项目根）
static/css/site.css     →    dist/static/css/site.css
robots.txt              →    dist/robots.txt

模板根（--template，默认 templates）  →    （不产出、不拷贝）
```

- 页面按**页面根**落盘：`pages/about.html` → `dist/about.html`，页面根那一层不进 URL、也不进产物，且不做 `<页面>/index.html` 的目录式转换。
- 资源按**所属根**落盘：页面根内的文件按页面根、页面根外的按资源根（`--assets`，默认项目根）；除页面、模板根、忽略项之外的文件都**原样拷贝**。
- 两个来源映射到同一产物路径 → **直接报错**并列出两个来源，不做静默覆盖。
- 忽略规则（项目根的 `.gitignore` + 配置里的 `ignore`）**只在 gen 生效**：被忽略的文件不拷贝，被忽略的页面不产出（日志里有 `跳过被忽略的…`）；页面引用了被忽略的资源同样报错。详见[目录约定](layout.md)。
- **只排除当前的产物目录**：`--out` 指向的目录不会被拷进产物（连跑两次也不会自我嵌套），但换过产物目录后，旧的产物目录仍会被当资源拷贝——把它写进 `.gitignore`（推荐 `dist/`）或配置里的 `ignore`。
- 生成前清空产物目录（`--clean=false` 关闭）。护栏：产物目录不能是项目根、页面/模板/资源三个根本身或它们的父目录，也不能是用户主目录；命中直接报错，不会删任何东西。
- 任一页面渲染失败即整体失败、非零退出，不做静默跳过。

---

[← 返回 README](../README.md)
