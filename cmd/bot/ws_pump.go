package main

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"meme-bot/internal/model"
	"meme-bot/internal/strategy"
	"meme-bot/internal/ws"
)

// wsEventPump 周期性对比快照并把「变化」推送给 WebSocket 客户端。
//
// 说明：后端内部是快照比对（2s / 5s），对前端而言仍是实时推送——
// 前端的 10–20s 轮询可退化为降级路径，正常时完全不轮询。
func wsEventPump(
	ctx context.Context,
	hub *ws.Hub,
	engine *strategy.SignalEngine,
	positions model.PositionStore,
	log *zap.Logger,
) {
	if hub == nil || engine == nil {
		return
	}
	sigTicker := time.NewTicker(2 * time.Second)
	posTicker := time.NewTicker(5 * time.Second)
	defer sigTicker.Stop()
	defer posTicker.Stop()

	seenSignals := make(map[string]string)   // signal id -> status
	seenPositions := make(map[string]string) // position id -> 标记价格

	for {
		select {
		case <-ctx.Done():
			return
		case <-sigTicker.C:
			active := engine.Active()
			for _, s := range active {
				if prev, ok := seenSignals[s.ID]; !ok || prev != string(s.Status) {
					seenSignals[s.ID] = string(s.Status)
					hub.Broadcast(ws.Event{Type: ws.EventSignal, Data: s})
				}
			}
			// 防止 map 无限增长：信号量收敛后重建索引
			if len(seenSignals) > len(active)*4+32 {
				next := make(map[string]string, len(active))
				for _, s := range active {
					next[s.ID] = string(s.Status)
				}
				seenSignals = next
			}
		case <-posTicker.C:
			if positions == nil {
				continue
			}
			open, err := positions.ListOpen("")
			if err != nil {
				log.Debug("ws pump: list positions failed", zap.Error(err))
				continue
			}
			for _, p := range open {
				key := fmt.Sprintf("%.10f", p.CurrentPriceUSD)
				if prev, ok := seenPositions[p.ID]; !ok || prev != key {
					seenPositions[p.ID] = key
					hub.Broadcast(ws.Event{Type: ws.EventPosition, Data: p})
				}
			}
		}
	}
}

// wsAlertSink 装饰器：告警既走原通道（Telegram/日志），也实时推给 WS 客户端。
type wsAlertSink struct {
	inner model.AlertSink
	hub   *ws.Hub
}

// Raise 实现 model.AlertSink。
func (s *wsAlertSink) Raise(ctx context.Context, a *model.Alert) error {
	if s.hub != nil && a != nil {
		s.hub.Broadcast(ws.Event{Type: ws.EventAlert, Data: a})
	}
	if s.inner == nil {
		return nil
	}
	return s.inner.Raise(ctx, a)
}
