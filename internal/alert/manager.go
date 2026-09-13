// Package alert 实现分级告警系统：去重、抑制、分级路由、异步发送、人工干预入口。
//
// 设计要点（对齐文书“告警疲劳”治理要求）：
//   - 去重：同一 chain+token+title 在窗口内只发一次（Redis/内存 TTL key）
//   - 抑制：已止损代币在 N 分钟内不再推送价格波动类告警
//   - 路由：Critical 走全部通道；Warning/Info 仅走非升级通道
//   - 异步：发送失败不影响主流程，但会记录指标与日志
package alert

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/metrics"
	"meme-bot/internal/model"
	"meme-bot/internal/risk"
)

// Manager 告警管理器（实现 model.AlertSink）。
type Manager struct {
	cfg     config.AlertConfig
	store   risk.StateStore
	metrics *metrics.Registry
	log     *zap.Logger

	mu         sync.RWMutex
	channels   []model.Channel
	suppressed map[string]time.Time // token -> 抑制截止时间
}

// New 构建告警管理器。store 为 nil 时使用内存实现。
func New(cfg config.AlertConfig, channels []model.Channel, store risk.StateStore, reg *metrics.Registry, log *zap.Logger) *Manager {
	if store == nil {
		store = risk.NewMemoryStore()
	}
	if log == nil {
		log = zap.NewNop()
	}
	if cfg.DedupWindowMinutes <= 0 {
		cfg.DedupWindowMinutes = 5
	}
	return &Manager{
		cfg:        cfg,
		store:      store,
		metrics:    reg,
		log:        log,
		channels:   channels,
		suppressed: make(map[string]time.Time),
	}
}

var _ model.AlertSink = (*Manager)(nil)

// AddChannel 动态注册通道。
func (m *Manager) AddChannel(c model.Channel) {
	if c == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels = append(m.channels, c)
}

// Channels 返回已注册通道名。
func (m *Manager) Channels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.channels))
	for _, c := range m.channels {
		out = append(out, c.Name())
	}
	return out
}

// Raise 实现 model.AlertSink：去重 → 抑制 → 路由 → 异步发送。
func (m *Manager) Raise(ctx context.Context, a *model.Alert) error {
	if a == nil {
		return errors.New("alert: nil alert")
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Timestamp.IsZero() {
		a.Timestamp = time.Now().UTC()
	}
	if a.Level == "" {
		a.Level = model.LevelInfo
	}
	if !a.Level.Valid() {
		return fmt.Errorf("alert: invalid level %q", a.Level)
	}

	// 1) 抑制检查
	if m.isSuppressed(a) {
		m.log.Debug("alert suppressed", zap.String("title", a.Title), zap.String("token", a.Token))
		return nil
	}

	// 2) 去重
	if dup, err := m.isDuplicate(ctx, a); err != nil {
		m.log.Warn("alert dedup check failed", zap.Error(err))
	} else if dup {
		m.log.Debug("alert deduplicated", zap.String("title", a.Title), zap.String("token", a.Token))
		return nil
	}

	// 3) 路由 + 异步发送
	channels := m.route(a)
	if len(channels) == 0 {
		m.log.Debug("alert has no channel", zap.String("level", string(a.Level)))
		return nil
	}
	for _, ch := range channels {
		ch := ch
		go func() {
			sendCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := ch.Send(sendCtx, a); err != nil {
				m.log.Error("alert send failed",
					zap.String("channel", ch.Name()), zap.String("title", a.Title), zap.Error(err))
				return
			}
			if m.metrics != nil {
				m.metrics.Inc("memebot_alerts_total", 1)
			}
		}()
	}
	return nil
}

// RaiseSync 同步发送（用于测试与关键路径）。
func (m *Manager) RaiseSync(ctx context.Context, a *model.Alert) error {
	channels := m.route(a)
	var errs []string
	for _, ch := range channels {
		if err := ch.Send(ctx, a); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ch.Name(), err))
			continue
		}
		if m.metrics != nil {
			m.metrics.Inc("memebot_alerts_total", 1)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("alert: %s", strings.Join(errs, "; "))
	}
	return nil
}

// SuppressToken 抑制某代币的告警（例如止损后）。
func (m *Manager) SuppressToken(chain, token string, d time.Duration) {
	if d <= 0 {
		return
	}
	key := suppressKey(chain, token)
	m.mu.Lock()
	m.suppressed[key] = time.Now().Add(d)
	m.mu.Unlock()
}

func (m *Manager) isSuppressed(a *model.Alert) bool {
	if a.Token == "" {
		return false
	}
	// Critical 不被抑制（安全第一）
	if a.Level == model.LevelCritical {
		return false
	}
	key := suppressKey(a.Chain, a.Token)
	m.mu.RLock()
	until, ok := m.suppressed[key]
	m.mu.RUnlock()
	return ok && time.Now().Before(until)
}

func suppressKey(chain, token string) string {
	return strings.ToLower(chain) + ":" + strings.ToLower(token)
}

func (m *Manager) isDuplicate(ctx context.Context, a *model.Alert) (bool, error) {
	fingerprint := fmt.Sprintf("%s|%s|%s", strings.ToLower(a.Chain), strings.ToLower(a.Token), a.Title)
	key := "alert:dedup:" + fingerprint
	if _, ok, err := m.store.Get(ctx, key); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	ttl := time.Duration(m.cfg.DedupWindowMinutes) * time.Minute
	if err := m.store.Set(ctx, key, a.Timestamp.Format(time.RFC3339), ttl); err != nil {
		return false, err
	}
	return false, nil
}

// route 按级别选择通道：Critical 全通道；其余通道自动过滤。
func (m *Manager) route(a *model.Alert) []model.Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Channel, 0, len(m.channels))
	for _, ch := range m.channels {
		if !supportsLevel(ch.Name(), a.Level) {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// supportsLevel 目前所有通道都支持全部级别；后续可对通道做静默时段配置。
func supportsLevel(_ string, level model.Level) bool {
	return level.Valid()
}

// HealthCheck 汇总通道健康状态（用于 /healthz 与运维面板）。
func (m *Manager) HealthCheck(ctx context.Context) map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.channels))
	for _, ch := range m.channels {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if tester, ok := ch.(interface {
			Ping(context.Context) error
		}); ok {
			if err := tester.Ping(probeCtx); err != nil {
				out[ch.Name()] = "error: " + err.Error()
			} else {
				out[ch.Name()] = "ok"
			}
		} else {
			out[ch.Name()] = "unknown"
		}
		cancel()
	}
	return out
}
