// Package ws 提供轻量 WebSocket 实时推送：Hub 负责连接管理与事件广播。
//
// 设计要点：
//   - 单写者语义：每条连接只由一个写泵（writePump）写入，避免并发写导致帧错乱；
//   - 慢客户端保护：发送队列满时丢弃该条事件并计数，绝不阻塞事件源；
//   - 心跳：服务端定期 ping，客户端 pong 超时即断开，及时回收死连接；
//   - 来源校验：握手按白名单校验 Origin（复用 CORS 白名单语义），支持可选令牌鉴权。
package ws

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// EventType 事件类型（前端据此分发到不同 UI 区域）。
type EventType string

const (
	// EventSignal 新信号 / 信号状态变化（pending → confirmed / expired / executed）。
	EventSignal EventType = "signal"
	// EventOrder 订单状态迁移。
	EventOrder EventType = "order"
	// EventPosition 持仓变化（开仓 / 浮动盈亏刷新 / 平仓）。
	EventPosition EventType = "position"
	// EventAlert 告警（分级）。
	EventAlert EventType = "alert"
	// EventSystem 系统事件（熔断、连接问候等）。
	EventSystem EventType = "system"
)

// Event 推送给客户端的事件载荷。
type Event struct {
	Type EventType `json:"type"`
	At   time.Time `json:"at"`
	Data any       `json:"data,omitempty"`
}

// Stats 运行统计（用于 /healthz 与运维观察）。
type Stats struct {
	Clients       int   `json:"clients"`
	Sent          int64 `json:"sent"`
	Dropped       int64 `json:"dropped"`
	TotalAccepted int64 `json:"total_accepted"`
}

// Hub 连接枢纽：注册表 + 广播。
type Hub struct {
	log *zap.Logger

	mu      sync.RWMutex
	clients map[*client]struct{}

	statMu        sync.Mutex
	sent          int64
	dropped       int64
	totalAccepted int64
}

// NewHub 构建 Hub。
func NewHub(log *zap.Logger) *Hub {
	if log == nil {
		log = zap.NewNop()
	}
	return &Hub{log: log, clients: make(map[*client]struct{})}
}

// Clients 当前连接数。
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Stats 返回运行统计快照。
func (h *Hub) Stats() Stats {
	h.mu.RLock()
	clients := len(h.clients)
	h.mu.RUnlock()

	h.statMu.Lock()
	defer h.statMu.Unlock()
	return Stats{
		Clients:       clients,
		Sent:          h.sent,
		Dropped:       h.dropped,
		TotalAccepted: h.totalAccepted,
	}
}

// queue 向单个连接入队（非阻塞），并统一维护 Sent/Dropped 统计。
func (h *Hub) queue(c *client, payload []byte) bool {
	select {
	case c.send <- payload:
		h.statMu.Lock()
		h.sent++
		h.statMu.Unlock()
		return true
	default:
		h.statMu.Lock()
		h.dropped++
		h.statMu.Unlock()
		h.log.Debug("ws: client buffer full, event dropped", zap.String("client", c.remote))
		return false
	}
}

// Broadcast 向所有连接广播事件（非阻塞：队列满则丢弃并计数）。
//
// 返回成功入队的连接数（便于测试与观察）。
func (h *Hub) Broadcast(evt Event) int {
	if evt.At.IsZero() {
		evt.At = time.Now().UTC()
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		h.log.Warn("ws: marshal event failed", zap.String("type", string(evt.Type)), zap.Error(err))
		return 0
	}

	h.mu.RLock()
	targets := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	queued := 0
	for _, c := range targets {
		if h.queue(c, payload) {
			queued++
		}
	}
	return queued
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	h.statMu.Lock()
	h.totalAccepted++
	h.statMu.Unlock()
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
	}
	h.mu.Unlock()
	c.closeOnce.Do(func() { close(c.send) })
}

// client 单条 WebSocket 连接。
type client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	remote    string
	closeOnce sync.Once
}

// HandlerConfig 握手与心跳配置。
type HandlerConfig struct {
	// AllowedOrigins 允许的 Origin 白名单（与 CORS 同语义）；空表示仅允许无 Origin 的客户端。
	AllowedOrigins []string
	// RequireAuth 是否强制令牌鉴权（?token=JWT 或 Authorization 头）。
	RequireAuth bool
	// VerifyToken 令牌校验函数（RequireAuth 为 true 时必填）。
	VerifyToken func(token string) error
	// PingInterval 服务端心跳间隔。
	PingInterval time.Duration
	// PongWait 等待 pong 的超时。
	PongWait time.Duration
	// WriteWait 单次写超时。
	WriteWait time.Duration
	// SendBuffer 每连接发送队列长度。
	SendBuffer int
	// MaxMessageSize 客户端上行消息上限（本服务以下行为主）。
	MaxMessageSize int64
}

func (c HandlerConfig) withDefaults() HandlerConfig {
	if c.PingInterval <= 0 {
		c.PingInterval = 25 * time.Second
	}
	if c.PongWait <= 0 {
		c.PongWait = 60 * time.Second
	}
	if c.WriteWait <= 0 {
		c.WriteWait = 10 * time.Second
	}
	if c.SendBuffer <= 0 {
		c.SendBuffer = 64
	}
	if c.MaxMessageSize <= 0 {
		c.MaxMessageSize = 4096
	}
	return c
}

// Handler 返回 Gin 处理器（GET /ws）：握手 → 注册 → 读写泵。
func (h *Hub) Handler(cfg HandlerConfig) gin.HandlerFunc {
	cfg = cfg.withDefaults()
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return originAllowed(r.Header.Get("Origin"), cfg.AllowedOrigins)
		},
	}

	return func(c *gin.Context) {
		if cfg.RequireAuth {
			token := strings.TrimSpace(c.Query("token"))
			if token == "" {
				token = strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
			}
			if cfg.VerifyToken == nil || cfg.VerifyToken(token) != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "WebSocket 鉴权失败（缺少或无效 token）"})
				return
			}
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			h.log.Warn("ws: upgrade failed", zap.Error(err))
			return
		}

		cl := &client{
			hub:    h,
			conn:   conn,
			send:   make(chan []byte, cfg.SendBuffer),
			remote: c.ClientIP(),
		}
		h.register(cl)
		h.log.Info("ws: client connected", zap.String("remote", cl.remote), zap.Int("clients", h.Clients()))

		// 连接问候：前端可据此立刻确认链路可用
		if payload, err := json.Marshal(Event{
			Type: EventSystem,
			At:   time.Now().UTC(),
			Data: map[string]any{"message": "connected", "clients": h.Clients()},
		}); err == nil {
			h.queue(cl, payload)
		}

		go cl.writePump(cfg)
		cl.readPump(cfg) // 阻塞至连接结束
	}
}

// readPump 读取循环：消费 pong/close 并维护读期限（客户端上行目前不处理）。
func (c *client) readPump(cfg HandlerConfig) {
	defer func() {
		c.hub.unregister(c)
		_ = c.conn.Close()
		c.hub.log.Info("ws: client disconnected",
			zap.String("remote", c.remote), zap.Int("clients", c.hub.Clients()))
	}()

	c.conn.SetReadLimit(cfg.MaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(cfg.PongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(cfg.PongWait))
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(cfg.PongWait))
	}
}

// writePump 单写者循环：消费发送队列 + 定期 ping。
func (c *client) writePump(cfg HandlerConfig) {
	ticker := time.NewTicker(cfg.PingInterval)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(cfg.WriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(cfg.WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// originAllowed 校验 Origin 白名单；无 Origin（原生 App / 脚本）放行。
func originAllowed(origin string, allowed []string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if a == "*" || strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}
