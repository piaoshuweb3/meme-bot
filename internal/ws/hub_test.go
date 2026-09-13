package ws

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func newTestServer(t *testing.T, hub *Hub, cfg HandlerConfig) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/ws", hub.Handler(cfg))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func waitClients(t *testing.T, hub *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Clients() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待连接数=%d 超时，实际 %d", want, hub.Clients())
}

func readEvent(t *testing.T, conn *websocket.Conn) Event {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取事件失败：%v", err)
	}
	var evt Event
	if err := json.Unmarshal(payload, &evt); err != nil {
		t.Fatalf("事件不是合法 JSON：%v", err)
	}
	return evt
}

func TestHubBroadcastToClient(t *testing.T) {
	hub := NewHub(nil)
	srv := newTestServer(t, hub, HandlerConfig{AllowedOrigins: []string{"http://localhost:3000"}})

	h := http.Header{}
	h.Set("Origin", "http://localhost:3000")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), h)
	if err != nil {
		t.Fatalf("握手失败：%v", err)
	}
	defer func() { _ = conn.Close() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("期望 101，实际 %d", resp.StatusCode)
	}

	// 连接问候
	if hello := readEvent(t, conn); hello.Type != EventSystem {
		t.Fatalf("首个事件应为 system 问候，实际 %s", hello.Type)
	}

	waitClients(t, hub, 1)
	if queued := hub.Broadcast(Event{Type: EventSignal, Data: map[string]any{"token": "0xabc"}}); queued != 1 {
		t.Fatalf("应有 1 条连接入队，实际 %d", queued)
	}
	evt := readEvent(t, conn)
	if evt.Type != EventSignal {
		t.Fatalf("期望 signal 事件，实际 %s", evt.Type)
	}
	if evt.At.IsZero() {
		t.Fatal("事件应带时间戳")
	}

	stats := hub.Stats()
	if stats.Sent < 2 || stats.TotalAccepted != 1 {
		t.Fatalf("统计异常：%+v", stats)
	}
}

func TestHubRejectsForeignOrigin(t *testing.T) {
	hub := NewHub(nil)
	srv := newTestServer(t, hub, HandlerConfig{AllowedOrigins: []string{"http://localhost:3000"}})

	h := http.Header{}
	h.Set("Origin", "http://evil.example")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), h)
	if err == nil {
		_ = conn.Close()
		t.Fatal("非白名单 Origin 必须被拒绝")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("期望 403，实际 %+v", resp)
	}
	if hub.Clients() != 0 {
		t.Fatal("被拒绝的握手不应留下连接")
	}
}

func TestHubRequireAuth(t *testing.T) {
	hub := NewHub(nil)
	verify := func(token string) error {
		if token == "good-token" {
			return nil
		}
		return errors.New("invalid token")
	}
	srv := newTestServer(t, hub, HandlerConfig{RequireAuth: true, VerifyToken: verify})

	if conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), nil); err == nil {
		_ = conn.Close()
		t.Fatal("缺少 token 必须被拒绝")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %+v", resp)
	}

	if conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv)+"?token=bad", nil); err == nil {
		_ = conn.Close()
		t.Fatal("错误 token 必须被拒绝")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %+v", resp)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv)+"?token=good-token", nil)
	if err != nil {
		t.Fatalf("正确 token 应握手成功：%v", err)
	}
	defer func() { _ = conn.Close() }()
	if hello := readEvent(t, conn); hello.Type != EventSystem {
		t.Fatalf("鉴权通过后应收到问候，实际 %s", hello.Type)
	}
}

func TestHubClientCleanup(t *testing.T) {
	hub := NewHub(nil)
	srv := newTestServer(t, hub, HandlerConfig{})

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatalf("握手失败：%v", err)
	}
	waitClients(t, hub, 1)
	_ = conn.Close()
	waitClients(t, hub, 0)

	if hub.Broadcast(Event{Type: EventSystem}) != 0 {
		t.Fatal("无连接时广播不应入队")
	}
}

func TestOriginAllowed(t *testing.T) {
	allowed := []string{"http://localhost:3000", "https://panel.example.com"}
	cases := []struct {
		origin string
		want   bool
	}{
		{"", true},                          // 非浏览器客户端
		{"http://localhost:3000", true},     // 白名单精确匹配
		{"HTTP://LOCALHOST:3000", true},     // 大小写不敏感
		{"https://panel.example.com", true}, // 白名单第二项
		{"http://localhost:3001", false},    // 端口不同
		{"http://evil.example", false},      // 非白名单
	}
	for _, c := range cases {
		if got := originAllowed(c.origin, allowed); got != c.want {
			t.Fatalf("originAllowed(%q) = %v，期望 %v", c.origin, got, c.want)
		}
	}
	if !originAllowed("http://anything", []string{"*"}) {
		t.Fatal("通配符白名单应放行任意来源")
	}
}
