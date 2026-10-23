# 开发与贡献

这一篇讲本地开发命令、源码结构、测试分层、发布流程与贡献约定。

## 开发

```bash
go build ./...
go vet ./...
go test -short ./...          # 单元测试（跳过 e2e，秒级）
go test ./...                 # 单元 + 端到端
go test -race -count=1 ./...  # 完整（CI 同款）
go test -run TestE2E -v ./... # 只看端到端
go test -short -cover ./...   # 覆盖率
```

命名约定：**大写开头的标识符表示「站点模型 API」**（`Site`/`LoadSite`/`Generate`/`Resolve`…），
小写的是内部工具（`newHandler`/`servableFile`/`skipInWalk`…）。整个仓库是单个 `package main`，
导出与否不影响可用性，这套大小写只是用来标示「这是对外的概念模型还是实现细节」；例外是
`fileConfig` 的字段必须大写（yaml.v3 要求）。

源码结构（`package main`，扁平分层，不做过度抽象）：

| 文件 | 职责 |
| --- | --- |
| `main.go` | 子命令与参数分发 |
| `config.go` | `.goweb.yml` 加载、项目根发现与 `flag > 配置 > 默认` 的取值规则 |
| `init.go` | `init` 子命令：默认配置模板（键集合由测试与 `fileConfig` 对齐）与覆盖保护 |
| `color.go` | 终端日志的 status 上色（仅终端、遵循 NO_COLOR）与状态码配色 |
| `links.go` | `check` 的站内链接校验：收集 href/src/srcset，按 `urlIsPublished` 判断目标是否会出现在产物里 |
| `site.go` | 三个根与项目根解析、页面发现、模板命名空间、`data.json` 加载、URL 路由解析、渲染 |
| `serve.go` | HTTP 处理、访问日志、fsnotify 防抖重载 |
| `gen.go` | 静态产物落盘（`generateRun` 同时支撑生成与 `check` 校验模式）、资源按所属根映射、产物路径冲突检测、清空护栏 |
| `gitignore.go` | `.gitignore` 子集解析与匹配（`**` 交给 doublestar，语义用真实 git 逐条对拍验证） |
| `fingerprint.go` | 页面里站内资源引用的 `?v=<内容哈希>` 自动改写 |
| `funcs.go` | 内置模板函数（`asset`/`default`/`fail`/`dict`/文本工具/模式与时间）与其加载上下文 |

### 测试分层

测试只打公开边界，不针对内部实现；每一层都有明确的观察面：

| 测试 | 边界 | 覆盖内容 |
| --- | --- | --- |
| `site_test.go` | `LoadSite` / `Reload` / `Render` / `Resolve` / `WatchPaths` / `ignoredPath` | 页面发现与忽略规则、try_files 三候选顺序与路径归一化、data.json 语义、坏模板保留上一版、并发渲染与重载 |
| `roots_test.go` | `LoadSite`（三根）与 CLI 配置链路 | 三个根与项目根解析（含向上查找、`--config` 显式路径、模板根在项目根之外）、产物映射与冲突报错、忽略规则只作用于 gen、`.goweb.yml` 优先级与未知字段 |
| `gitignore_test.go` | `ignoreMatcher` | 模式子集语义，并与真实 `git check-ignore` 在同一批路径上逐条对拍 |
| `fingerprint_test.go` | `fingerprintHTML` | 属性白名单、跳过规则（外链/`data:`/页面/模板/已带查询串）、相对路径、`srcset`、无改动时逐字节保真、与 `{{asset}}` 同哈希 |
| `minify_test.go` | `minifyBytes` / `Generate`（Minify） | 扩展名映射、页面与资源的压缩、`<pre>`/内联元素空格逐字节保真、JS 不改变量名、`/*!` 许可声明保留、不可压缩文件字节一致 |
| `gen_test.go` | `Generate` | 产物树、资源拷贝（嵌套目录/符号链接/忽略项）、清空护栏、快速失败、错误路径 |
| `listen_test.go` | `listenWithFallback` / `nextPort` | 端口递增规则（主机不变、`:0`/非法端口不递增）、真实 socket 上的落位、显式端口快速失败、非占用错误不被掩盖、连续端口耗尽 |
| `serve_test.go` | `newHandler` / `watchSite` / `selfOutputFiles` | 状态码与响应头、访问日志、渲染失败回 500、fsnotify 重载与失败韧性、新增文件被发现、热重载注入范围与广播时机 |
| `livereload_test.go` | `liveReloadHub` / `injectLiveReload` | 脚本注入位置（`</body>` 前/追加/大小写）、订阅与注销、慢客户端不阻塞广播 |
| `open_test.go` | `browserURL` / `browserCommand` / `openInBrowser` | 监听地址到浏览器地址的推导（通配→localhost、IPv6 方括号）、三平台命令与 `GOWEB_OPEN_COMMAND` 覆盖、打开失败只记日志不影响服务 |
| `color_test.go` | `colorizeStatus` / `colorWriter` / `wantColorLogs` | 2xx 不上色、3xx/4xx/5xx 配色、分片写入的整行处理、非终端与 NO_COLOR 时不上色、访问日志不含 duration |
| `check_test.go` | `Generate`（Check）/ `urlIsPublished` | 不落盘且不动已有产物、问题汇总（渲染失败+路径冲突+坏链接一次列全）、`--links=false`、链接指向被忽略目标、URL 与产物路径一致性的判定 |
| `init_test.go` | `runInit` / `defaultConfigTemplate` | 生成内容与模板一致、键集合与 `fileConfig` 对齐、`addr` 只作注释、不改变默认行为、已存在报错且不改文件、`--force`、向上查找不生成嵌套配置、`--config` 指定路径、init 后能直接 build |
| `main_test.go` | cobra 命令树 | 参数默认值与短选项、公共参数前置、错误前缀与 usage、`--version` 注入、`dev`/`build` 别名等价 |
| `regression_test.go` | `LoadSite` / `Resolve` / `Generate` / `check` / `Render` | 审计回归锁：模板根覆盖校验、URL 与产物路径一致（多写页面根 404、页面根外 html 直出、百分号编码）、index 相对链接双基准、页面根资源拷贝、产物目录符号链接拒绝、软链接策略、冲突单条化、gen 日志 fingerprint 字段、默认只监听 127.0.0.1 |
| `e2e_test.go` | **真实二进制 + 真实 HTTP** | 编译 goweb → 跑 `gen`（产物清单、幂等、护栏退出码、坏数据失败、产物不含热重载脚本、`pages/ + templates/ + static/` 三根布局与 `.goweb.yml` 驱动）与 `serve`（全部 URL 语义、405/404、缓存头、SSE 推送刷新事件、`--livereload=false`、坏模板韧性、被忽略文件本地仍可见、`--version`/`help`/`completion`） |
| `e2e_test.go`（自动开浏览器） | 真实 `serve` + `GOWEB_OPEN_COMMAND` | 现场编译一个「把收到的参数写进文件」的记录器程序当打开命令（跨平台，不弹窗），验证 serve 确实把**真实监听地址**交给它，且 `--open=false` 完全不调用 |

端到端测试用 `--addr 127.0.0.1:0` 让内核分配端口，再从启动日志里读回真实地址，因此不会与并行任务抢端口；`go test -short` 会跳过它（需要编译与监听）。常规 e2e 一律带 `--open=false`，避免在开发机或 CI 上真的弹出浏览器窗口。

测试质量本身经过故障注入验证：分别破坏模板目录隔离、尾斜杠语义、产物目录护栏与解析错误处理，对应测试都会立刻变红。

CI 在 Ubuntu、macOS 与 Windows 上运行 `gofmt` 检查、`go mod tidy` 校验、`go vet`、单元测试（含覆盖率与竞态检测）与端到端测试。竞态检测需要 C 编译器，镜像里没有时该步骤会打印一条 notice 并跳过（不静默降级）。

### 发布

推一个 `v*` 标签即触发 [release.yml](../.github/workflows/release.yml)：先在 Ubuntu 上跑完整测试，再由 GoReleaser 按 [.goreleaser.yaml](../.goreleaser.yaml) 交叉编译 macOS/Linux 的 amd64 与 arm64、生成 `checksums.txt` 并创建 GitHub Release。

发布说明由 GoReleaser 按 `.goreleaser.yaml` 的 `changelog.groups` 自动生成：提交按类型分组为「新功能 / 修复 / 性能与重构 / 文档 / 其他」，只过滤 `ci:`、`chore:`、`test:` 这类工程噪声。因此提交信息要遵守 `<type>(<scope>): <summary>`，否则会落进「其他」。自动说明是一行一条的摘要；需要「概览、升级提示、操作步骤」这类多段式说明时，在 Release 创建后手动补写：

```bash
gh release edit v0.3.0 --notes-file release-notes.md
```

补写内容以用户视角为准：先写这次发布解决了什么问题、有无行为变化，再写升级注意事项，最后放完整变更链接。

```bash
# 本地预演一次完整打包（产物落在 dist/，不会发布）
goreleaser release --snapshot --clean
```

## 贡献

欢迎 Issue 与 PR：

1. Fork 并新建分支。
2. 提交前确保 `gofmt -l .` 无输出、`go vet ./...` 与 `go test ./...` 全部通过。
3. 新增行为请补上对应的单元测试。
4. Commit message 沿用 `<type>(<scope>): <summary>`（如 `feat(gen): 支持 --clean=false`）。

---

[← 返回 README](../README.md)
