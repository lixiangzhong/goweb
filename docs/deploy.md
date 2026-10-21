# 部署

这一篇讲不同静态托管下的 nginx 配置、平台差异与 `file://` 注意事项。

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

---

[← 返回 README](../README.md)
