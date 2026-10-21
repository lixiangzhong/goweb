# 文档索引

深入内容在这里，项目概览见[根 README](../README.md)。

- [目录约定](layout.md) —— 项目根、页面/模板/资源三个根与产物映射。
- [模板](templates.md) —— 单一模板命名空间、内置模板函数与 dev/prod 模式。
- [模板数据](data.md) —— `templates/data.json` 的渲染语义与行为约定。
- [静态资源与缓存失效](assets.md) —— 自动补 `?v=<内容哈希>` 的规则与关闭方式。
- [URL 规则](routing.md) —— `serve` 的解析顺序与本地/线上逐条对照。
- [浏览器热重载](livereload.md) —— 文件监听、SSE 广播与注入边界。
- [生成静态站](generate.md) —— `goweb gen` 的产物映射与清空护栏。
- [部署](deploy.md) —— nginx 配置与各静态托管平台差异。
- [命令与参数](cli.md) —— 全部参数、`.goweb.yml` 与 shell 补全。
- [设计取舍与边界](design.md) —— 关键设计取舍与目前不做的范围。
- [开发与贡献](development.md) —— 本地开发、测试分层、发布与贡献约定。

---

[← 返回 README](../README.md)
