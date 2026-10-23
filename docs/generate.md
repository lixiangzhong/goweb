# 生成静态站

这一篇讲 `goweb gen`（别名 `goweb build`，两者等价）的产物路径映射、拷贝规则与清空护栏。
想在不产生产物的前提下把同样的检查跑一遍（外加站内链接检查），用 `goweb check`。

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
- 生成前清空产物目录（`--clean=false` 关闭）。护栏：产物目录不能是项目根、页面/模板/资源三个根本身或它们的父目录，也不能是用户主目录或符号链接；命中直接报错，不会删任何东西。
- 产物目录落在页面根/资源根内部（例如 `--out static`）时，清空会删掉该目录里原有的源码：`gen` 会先打印告警再继续，交付前请确认 `--out` 指向的是专用产物目录。
- 任一页面渲染失败即整体失败、非零退出，不做静默跳过。

## 压缩（--minify，默认开启）

`gen`/`build` 默认把产物压一遍：**只去空白与注释，不改写代码语义**。`serve` 不压缩，开发时看到的始终是可读的渲染结果。

| 处理 | 文件类型 |
| --- | --- |
| 压缩 | 页面 HTML、`static/` 下的 `.html`/`.htm`、`.css`、`.js`/`.mjs`、`.json`、`.svg`、`.xml` |
| 逐字节拷贝 | 其他一切（图片、字体、`.txt`、二进制……） |

几条硬保证（都有测试锁定）：

- **JS 不改变量名**（`KeepVarNames`）：这是 minify 不是 uglify，内联 `<script>` 里依赖 `eval`、函数源码文本或调试符号的代码不会被改坏。
- **`<pre>` / `<textarea>` 内的空白逐字节保留**；内联元素之间的空格（`<span>a</span> <span>b</span>`）也保留——不会被压成 `ab`。
- **CSS/JS 里的 `/*! 许可声明 */` 保留**（注释内部空白会被压掉）；普通注释删除。
- 压缩只作用于**产物**：`--minify=false` 时产物与 `serve` 渲染结果一致（逐字节可比对）。
- 不做 `.gz`/`.br` 预压缩：交给 nginx 或托管平台更合适。

两个容易误解的点：

- **模板里的 HTML 注释本来就没了**：`html/template` 在解析阶段就会丢掉注释，压缩与否都一样。压缩删注释只影响「页面根外被当资源拷贝的 `.html`」与内联 `<style>`/`<script>`。
- 压缩会省略可选闭合标签（如 `</p>`）与不必要的属性引号；这是合法的 HTML5，但**依赖正则处理 HTML 的下游工具可能不适应**，这种场景请用 `--minify=false`。

---

[← 返回 README](../README.md)
