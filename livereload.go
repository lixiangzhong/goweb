package main

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	// liveReloadPath 是浏览器热重载的 SSE 端点；它优先于站点自身的页面与静态文件解析。
	liveReloadPath = "/__goweb/livereload"
	// liveReloadPingInterval 是 SSE 保活注释的发送间隔，避免空闲连接被中途的代理掐断。
	liveReloadPingInterval = 20 * time.Second
	// liveReloadMarker 标记注入的脚本，便于识别与移除。
	liveReloadMarker = "data-goweb-livereload"
	// liveReloadSnippet 是注入页面末尾的热重载脚本：订阅 SSE，收到 reload 就整页刷新。
	// 只由 serve 注入，gen 产物里不会出现。
	liveReloadSnippet = `<script ` + liveReloadMarker + `>(()=>{` +
		`const s=new EventSource("` + liveReloadPath + `");` +
		`s.onmessage=e=>{if(e.data==="reload")location.reload()}})()</script>`
)

// liveReloadHub 管理所有已连接的浏览器页面。
type liveReloadHub struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

// newLiveReloadHub 创建订阅中心。
func newLiveReloadHub() *liveReloadHub {
	return &liveReloadHub{subscribers: make(map[chan struct{}]struct{})}
}

// subscribe 注册一个订阅者，返回接收通知的通道与注销函数。
// 通道带 1 个缓冲：同一个浏览器积压多次通知没有意义，合并成一次即可。
func (h *liveReloadHub) subscribe() (<-chan struct{}, func()) {
	events := make(chan struct{}, 1)

	h.mu.Lock()
	h.subscribers[events] = struct{}{}
	h.mu.Unlock()

	return events, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subscribers[events]; !ok {
			return
		}
		delete(h.subscribers, events)
		close(events)
	}
}

// broadcast 通知所有订阅者刷新。通道已满或订阅者已退出时跳过，绝不阻塞重载流程。
func (h *liveReloadHub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for events := range h.subscribers {
		select {
		case events <- struct{}{}:
		default:
		}
	}
}

// serveLiveReload 处理 SSE 长连接：站点文件变更时推一条 reload 事件。
func (h *handler) serveLiveReload(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "当前连接不支持流式响应", http.StatusInternalServerError)
		return
	}

	events, unsubscribe := h.live.subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// 断线后 1 秒重连（浏览器默认 3 秒）。
	if _, err := io.WriteString(w, "retry: 1000\n\n"); err != nil {
		return
	}
	flusher.Flush()

	ping := time.NewTicker(liveReloadPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			if _, err := io.WriteString(w, "data: reload\n\n"); err != nil {
				// 浏览器关页面、切标签都会走到这里，属于正常断开。
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// injectLiveReload 把热重载脚本插到最后一个 </body> 之前；没有 body 结束标签时追加到末尾。
func injectLiveReload(body []byte, snippet string) []byte {
	lowered := bytes.ToLower(body)
	index := bytes.LastIndex(lowered, []byte("</body>"))

	injected := make([]byte, 0, len(body)+len(snippet))
	if index < 0 {
		injected = append(injected, body...)
		return append(injected, snippet...)
	}
	injected = append(injected, body[:index]...)
	injected = append(injected, snippet...)
	return append(injected, body[index:]...)
}
