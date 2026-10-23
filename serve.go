package main

import (
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// reloadDebounce 是文件变更后触发站点重载的防抖窗口，避免连续保存时反复重载。
	reloadDebounce = 150 * time.Millisecond
	// readHeaderTimeout 限制请求头读取时间，避免连接被长时间占用。
	readHeaderTimeout = 10 * time.Second
)

// handler 是 serve 的 HTTP 入口：目录式路由 + 模板渲染 + 静态文件直出，
// 开启热重载时还会注入刷新脚本并开放 SSE 端点。
type handler struct {
	site   *Site
	logger *slog.Logger
	// live 为 nil 表示未开启浏览器热重载。
	live *liveReloadHub
}

// newHandler 构造 serve 的 HTTP 处理器；live 为 nil 时不注入脚本、SSE 端点也返回 404。
func newHandler(site *Site, logger *slog.Logger, live *liveReloadHub) http.Handler {
	return &handler{site: site, logger: logger, live: live}
}

// ServeHTTP 处理一次请求：包一层 statusRecorder 记录状态码，再打一条访问日志。
// 日志只留 method/path/status：开发时关心的是「哪个请求返回了什么状态」，
// 耗时留给浏览器 devtools 或压测工具，不占终端宽度。
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	recorder := &statusRecorder{ResponseWriter: w}
	h.serve(recorder, r)

	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	h.logger.Info("处理请求",
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
	)
}

// serve 派发一次请求。
func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// 热重载端点优先于站点 URL 解析，站点里同名文件让位。
	if h.live != nil && r.URL.Path == liveReloadPath {
		h.serveLiveReload(w, r)
		return
	}

	switch route := h.site.Resolve(r.URL.Path); route.Kind {
	case RoutePage:
		h.renderPage(w, route.Name)
	case RouteFile:
		h.serveFile(w, r, route.File)
	default:
		http.NotFound(w, r)
	}
}

// renderPage 渲染页面模板；失败时把原因写回浏览器，便于前端直接定位。
func (h *handler) renderPage(w http.ResponseWriter, name string) {
	body, err := h.site.Render(name)
	if err != nil {
		h.logger.Error("渲染页面失败", "page", name, "err", err)
		http.Error(w, "goweb 渲染失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 热重载脚本只注入 serve 的页面响应，gen 产物保持干净。
	if h.live != nil {
		body = injectLiveReload(body, liveReloadSnippet)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 开发服务器始终取最新内容，避免改完页面被浏览器旧副本挡住。
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(body); err != nil {
		h.logger.Debug("写响应失败", "page", name, "err", err)
	}
}

// serveFile 直出磁盘上的静态文件（css/js/图片等）。
func (h *handler) serveFile(w http.ResponseWriter, r *http.Request, filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

// statusRecorder 记录响应状态码，用于访问日志。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader 透传状态码，并只记录第一个——后续重复调用不会覆盖访问日志里的状态码。
func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

// Write 透传响应体；没显式调用过 WriteHeader 时按 200 记录。
func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

// Flush 透传底层 writer 的刷写能力。
// 少了它，包装后的响应就不再满足 http.Flusher，SSE 长连接会被误判为不支持流式响应。
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// siteWatcher 监听站点文件变更并在防抖后重载模板与数据。
type siteWatcher struct {
	site    *Site
	logger  *slog.Logger
	watcher *fsnotify.Watcher
	// onReload 在每次成功重载后调用，用于通知浏览器刷新；可为 nil。
	onReload func()
	// selfOutputs 是本进程日志输出指向的文件；它们的变更是自己写出来的，
	// 必须忽略，否则每行日志都会触发重载、重载又写日志，形成自触发死循环。
	selfOutputs []os.FileInfo

	mu    sync.Mutex
	timer *time.Timer
	// stopped 由 stop 置位：之后不再排新的重载。
	stopped bool
	// wg 跟踪正在执行的重载，stop 时等它们结束，避免关停后还在打日志。
	wg sync.WaitGroup
}

// selfOutputFiles 返回本进程 stdout/stderr 指向的常规文件。
// 典型场景是 `goweb serve > site/build.log`：日志文件落在站点里时会被监听到。
// 终端（字符设备）与管道不会命中，因此正常用法不受影响。
func selfOutputFiles() []os.FileInfo {
	var files []os.FileInfo
	for _, stream := range []*os.File{os.Stdout, os.Stderr} {
		info, err := stream.Stat()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, info)
	}
	return files
}

// isSelfOutput 判断路径是否就是本进程的日志输出文件。
func isSelfOutput(files []os.FileInfo, path string) bool {
	if len(files) == 0 {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, file := range files {
		if os.SameFile(file, info) {
			return true
		}
	}
	return false
}

// watchSite 启动站点文件监听，返回停止监听的函数；onReload 可为 nil。
func watchSite(site *Site, logger *slog.Logger, onReload func()) (func(), error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	sw := &siteWatcher{
		site:        site,
		logger:      logger,
		watcher:     watcher,
		onReload:    onReload,
		selfOutputs: selfOutputFiles(),
	}
	if err := sw.addPaths(); err != nil {
		// 监听失败不该拖垮开发服务器：热重载只是附属功能，
		// 路由、模板渲染与静态文件照常工作，降级为「改完手动刷新」。
		watcher.Close()
		logger.Warn("文件监听不可用，热重载已关闭", "err", err)
		return func() {}, nil
	}
	go sw.loop()
	return sw.stop, nil
}

// stop 关闭监听并等待在途重载结束：停止后防抖计时器不再触发，
// 也不会再补挂监听，避免关停瞬间还打一条「补挂文件监听失败」的告警。
func (w *siteWatcher) stop() {
	w.mu.Lock()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	_ = w.watcher.Close()
	w.wg.Wait()
}

// loop 消费文件系统事件，忽略产物与隐藏项，其余触发防抖重载。
func (w *siteWatcher) loop() {
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if w.site.ignoredPath(event.Name) || isSelfOutput(w.selfOutputs, event.Name) {
				continue
			}
			w.schedule()
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Warn("文件监听出错", "err", err)
		}
	}
}

// schedule 在防抖窗口结束后重载站点。
func (w *siteWatcher) schedule() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(reloadDebounce, w.fire)
		return
	}
	w.timer.Reset(reloadDebounce)
}

// fire 是防抖窗口到期后的回调：持锁确认未停止并登记一次在途重载，
// 让 stop 能等到它结束（计入 WaitGroup 后再执行，避免与 wg.Wait 竞态）。
func (w *siteWatcher) fire() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.wg.Add(1)
	w.mu.Unlock()
	defer w.wg.Done()
	w.reload()
}

// reload 重载模板与 data.json，并补挂新增目录的监听。
// 重载失败说明模板或数据正改到一半，此时保留上一份可用内容并报错，不让开发服务器挂掉。
// 只有重载成功才通知浏览器刷新：失败时刷新只会看到同一份旧内容，反而让人困惑。
func (w *siteWatcher) reload() {
	if err := w.site.Reload(); err != nil {
		w.logger.Error("站点重载失败，继续使用上一份模板与数据", "err", err)
	} else {
		w.logger.Info("站点已重载", "pages", len(w.site.Pages()))
		if w.onReload != nil {
			w.onReload()
		}
	}
	if err := w.addPaths(); err != nil {
		w.logger.Warn("补挂文件监听失败", "err", err)
	}
}

// addPaths 为站点内的文件与目录补挂监听。
// fsnotify 的 Add 对已监听的路径是幂等的，因此这里每次全量补挂：
// 文件被删除后重建（编辑器常见的原子保存）也能重新挂上监听。
func (w *siteWatcher) addPaths() error {
	paths, err := w.site.WatchPaths()
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range paths {
		if err := w.watcher.Add(p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
