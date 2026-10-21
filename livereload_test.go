package main

import (
	"strings"
	"testing"
	"time"
)

func TestInjectLiveReload(t *testing.T) {
	const snippet = "<script>RELOAD</script>"

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "插到 body 结束标签之前",
			body: "<html><body>hello</body></html>",
			want: "<html><body>hello<script>RELOAD</script></body></html>",
		},
		{
			name: "大小写不敏感",
			body: "<html><BODY>hello</BODY></html>",
			want: "<html><BODY>hello<script>RELOAD</script></BODY></html>",
		},
		{
			name: "多个 body 时插到最后一个之前",
			body: "<body>a</body><body>b</body>",
			want: "<body>a</body><body>b<script>RELOAD</script></body>",
		},
		{
			name: "没有 body 标签时追加到末尾",
			body: "<p>fragment</p>",
			want: "<p>fragment</p><script>RELOAD</script>",
		},
		{
			name: "空响应也追加",
			body: "",
			want: "<script>RELOAD</script>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := string(injectLiveReload([]byte(test.body), snippet))
			if got != test.want {
				t.Fatalf("注入结果 = %q，期望 %q", got, test.want)
			}
			if count := strings.Count(got, snippet); count != 1 {
				t.Fatalf("脚本应恰好注入一次，实际 %d 次", count)
			}
		})
	}
}

// TestLiveReloadHubUnsubscribe 覆盖注销：通道被关闭，重复注销不会 panic。
func TestLiveReloadHubUnsubscribe(t *testing.T) {
	hub := newLiveReloadHub()
	events, unsubscribe := hub.subscribe()

	unsubscribe()
	if _, ok := <-events; ok {
		t.Fatal("注销后通道应已关闭")
	}
	// 重复注销不应 panic，也不应影响其它订阅者。
	unsubscribe()

	other, otherUnsubscribe := hub.subscribe()
	defer otherUnsubscribe()
	hub.broadcast()
	select {
	case <-other:
	case <-time.After(time.Second):
		t.Fatal("其它订阅者未收到广播")
	}
}

// TestLiveReloadHubBroadcastNeverBlocks 覆盖慢客户端：通道满时继续广播，不阻塞重载流程。
func TestLiveReloadHubBroadcastNeverBlocks(t *testing.T) {
	hub := newLiveReloadHub()
	events, unsubscribe := hub.subscribe()
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			hub.broadcast()
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("广播被慢客户端阻塞")
	}

	// 缓冲为 1：连续广播只会留下一次待处理通知。
	if _, ok := <-events; !ok {
		t.Fatal("订阅者应至少收到一次通知")
	}
	select {
	case <-events:
		t.Fatal("同一个订阅者不应积压多次通知")
	default:
	}
}
