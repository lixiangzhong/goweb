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
	// reloadDebounce 是文件变更后触发模板重编译的防抖窗口，避免连续保存时反复重编译。
	reloadDebounce = 150 * time.Millisecond
	// readHeaderTimeout 限制请求头读取时间，避免连接被长时间占用。
	readHeaderTimeout = 10 * time.Second
)

// handler 是 serve 的 HTTP 入口：目录式路由 + 模板渲染 + 静态文件直出。
type handler struct {
	site   *Site
	logger *slog.Logger
}

// newHandler 构造 serve 的 HTTP 处理器。
func newHandler(site *Site, logger *slog.Logger) http.Handler {
	return &handler{site: site, logger: logger}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
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
		"duration", time.Since(start),
	)
}

// serve 派发一次请求。
func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	switch route := h.site.Resolve(r.URL.Path); route.Kind {
	case RouteRedirect:
		http.Redirect(w, r, route.Location, http.StatusMovedPermanently)
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

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

// siteWatcher 监听站点文件变更并在防抖后重编译模板。
type siteWatcher struct {
	site    *Site
	logger  *slog.Logger
	watcher *fsnotify.Watcher

	mu    sync.Mutex
	timer *time.Timer
}

// watchSite 启动站点文件监听，返回停止监听的函数。
func watchSite(site *Site, logger *slog.Logger) (func(), error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	sw := &siteWatcher{
		site:    site,
		logger:  logger,
		watcher: watcher,
	}
	if err := sw.addPaths(); err != nil {
		watcher.Close()
		return nil, err
	}
	go sw.loop()
	return func() { watcher.Close() }, nil
}

// loop 消费文件系统事件，忽略产物与隐藏项，其余触发防抖重编译。
func (w *siteWatcher) loop() {
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if w.site.Ignored(event.Name) {
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

// schedule 在防抖窗口结束后重编译模板。
func (w *siteWatcher) schedule() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer == nil {
		w.timer = time.AfterFunc(reloadDebounce, w.reload)
		return
	}
	w.timer.Reset(reloadDebounce)
}

// reload 重编译模板并补挂新增目录的监听。
// 编译失败说明模板正改到一半，此时保留上一份可用模板并报错，不让开发服务器挂掉。
func (w *siteWatcher) reload() {
	if err := w.site.Reload(); err != nil {
		w.logger.Error("模板重编译失败，继续使用上一份模板", "err", err)
	} else {
		w.logger.Info("模板已重编译", "pages", len(w.site.Pages()))
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
