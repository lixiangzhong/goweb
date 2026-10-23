# URL 规则

这一篇讲 `serve` 的 URL 解析顺序（等效 nginx `try_files`）与本地/线上对照结果。

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
| `/templates/*`、`/dist/*` | 模板根 / 产物目录下的路径，视为不存在 | 404 | 404 | 404 | 404 |
| `/__goweb/livereload` | 热重载 SSE 端点（保留路径） | 200（`--livereload=false` 时为 404） | — | — | — |
| 其他 | 都不命中 | 纯文本 `404` | 404 | 404 | 404 |

要点：

- **`/about` 能开、`/about/` 打不开**，这不是 bug：扁平产物里没有 `about/` 目录，`$uri` 的目录检查必然失败——nginx 实测同样是 404。
- 非规范路径（`//about//`、`/a/../about`、`/about/.`）**静默归一化，不重定向**，与 nginx 的 `merge_slashes` + `remove_dot_segments` 行为一致。
- 模板根、产物目录、隐藏项与 `node_modules` 一律视为不存在，与产物内容保持一致。
- **页面根之外的 html**（例如资源根里的 `.html`）会原样进产物，按 `$uri` 静态直出；页面根里的 html 只会渲染成页面，没有被登记成页面的一律 404（避免把未渲染的模板直接吐给浏览器）。
- **URL 必须与产物路径一致**：文件虽然存在，但按产物映射会落到另一个路径时（例如 `dir: pages` 时的 `/pages/x.html`）本地直接 404——`serve` 与产物共用同一套映射，这是「本地能开、线上 404」不会出现的根本原因。
- **index 页的相对链接**：`/help/` 与 `/help`（无尾斜杠）都能访问 index 页，但后者在浏览器里把 `/help` 当文件，相对链接基准是 `/`。`check` 对两种基准都校验，因此能抓出「以 `/help` 打开时断链」的相对链接。
- 因此**站内链接写 `/about.html` 最稳**（任何托管都必然可用）；想让无扩展名写法在线上也可用，见[部署](deploy.md)里的那一行 `try_files`。
- `/__goweb/` 是 goweb 的保留前缀，站点里同名文件会让位给内置端点。

非 GET/HEAD 请求返回 `405`；开发期响应带 `Cache-Control: no-store`，避免改完文件被浏览器旧副本挡住。

---

[← 返回 README](../README.md)
