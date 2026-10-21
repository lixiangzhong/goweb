# 浏览器热重载

这一篇讲 `serve` 的文件监听、SSE 广播与注入边界。

`goweb serve` 默认开启浏览器热重载：站点文件一改，浏览器自己刷新，不用手动按 F5。

```
站点文件变更 ──fsnotify──▶ 防抖 150ms ──▶ 重载模板与 data.json
                                              │ 成功
                                              ▼
                        广播到 /__goweb/livereload ──SSE──▶ 页面 location.reload()
```

- **注入只在 serve**：页面 HTML 在 `</body>` 之前被插入一小段脚本（带 `data-goweb-livereload` 标记），`goweb gen` 的产物**不含任何脚本**——有测试保证。
- **端点**：`GET /__goweb/livereload`，`text/event-stream`，20 秒发一次保活注释；断线由浏览器自动重连。
- **不会自我触发**：`goweb serve > site/build.log` 这种把日志写进站点目录的写法是安全的——监听会跳过本进程 stdout/stderr 指向的文件（否则每行日志都会触发一次重载，形成死循环）。
- **触发范围**：页面、局部模板、`templates/data.json`、css/js/图片等任何被监听的站点文件；变更一律整页刷新（不做 CSS 热替换）。
- **只在成功时刷新**：模板改到一半（语法错误）时服务端保留上一份可用内容、终端打印错误，但**不**通知浏览器刷新——避免刷新后看到的还是旧内容而误以为没生效。
- **关掉它**：`goweb serve --livereload=false`（`-l=false`）。关闭后不注入脚本、端点返回 404，开发时看到的 HTML 与线上产物完全一致。

---

[← 返回 README](../README.md)
