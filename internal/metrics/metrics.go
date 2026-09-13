// Package metrics 提供轻量指标注册表，输出 Prometheus 文本格式。
//
// 不引入 prometheus client：需求只有 counter/gauge/summary，自实现更小、更可控。
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type kind string

const (
	kindCounter kind = "counter"
	kindGauge   kind = "gauge"
)

type sample struct {
	kind  kind
	help  string
	value float64
	count float64
	sum   float64
}

// Registry 指标注册表（并发安全）。
type Registry struct {
	mu      sync.Mutex
	samples map[string]*sample
}

// New 构建注册表，并预注册内置指标。
func New() *Registry {
	r := &Registry{samples: make(map[string]*sample)}
	r.describe("memebot_signals_total", kindCounter, "生成的交易信号总数")
	r.describe("memebot_signals_filtered_total", kindCounter, "被安全/流动性过滤丢弃的信号数")
	r.describe("memebot_orders_total", kindCounter, "提交的订单总数")
	r.describe("memebot_orders_failed_total", kindCounter, "失败订单总数")
	r.describe("memebot_alerts_total", kindCounter, "发出的告警总数")
	r.describe("memebot_swaps_observed_total", kindCounter, "观测到的 Swap 事件总数")
	r.describe("memebot_positions_open", kindGauge, "当前持仓数量")
	r.describe("memebot_equity_usd", kindGauge, "当前组合权益（美元）")
	r.describe("memebot_slippage_bps", kindGauge, "最近一次成交滑点（bps）")
	r.describe("memebot_paused", kindGauge, "熔断状态：1=暂停 0=正常")
	return r
}

func (r *Registry) describe(name string, k kind, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.samples[name]; !ok {
		r.samples[name] = &sample{kind: k, help: help}
	}
}

// Inc 计数器自增。
func (r *Registry) Inc(name string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.samples[name]
	if !ok {
		s = &sample{kind: kindCounter}
		r.samples[name] = s
	}
	s.value += delta
}

// Set 设置 gauge 值。
func (r *Registry) Set(name string, v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.samples[name]
	if !ok {
		s = &sample{kind: kindGauge}
		r.samples[name] = s
	}
	s.kind = kindGauge
	s.value = v
}

// Observe 记录一次观测（累加到 summary 的 count/sum）。
func (r *Registry) Observe(name string, v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.samples[name]
	if !ok {
		s = &sample{kind: kindGauge}
		r.samples[name] = s
	}
	s.count++
	s.sum += v
}

// Snapshot 返回指标快照（测试与调试用）。
//
// 语义：
//   - 纯 counter/gauge：返回当前值；
//   - 同时被 Observe 记录过的指标：额外返回 <name>_avg（平均值），
//     用于滑点、延迟这类"观测型"指标。
func (r *Registry) Snapshot() map[string]float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]float64, len(r.samples))
	for k, v := range r.samples {
		out[k] = v.value
		if v.count > 0 {
			out[k+"_avg"] = v.sum / v.count
		}
	}
	return out
}

// Handler 返回 Prometheus 文本格式的 /metrics 处理器。
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		r.mu.Lock()
		names := make([]string, 0, len(r.samples))
		for name := range r.samples {
			names = append(names, name)
		}
		sort.Strings(names)

		var b strings.Builder
		for _, name := range names {
			s := r.samples[name]
			if s.help != "" {
				fmt.Fprintf(&b, "# HELP %s %s\n", name, s.help)
			}
			fmt.Fprintf(&b, "# TYPE %s %s\n", name, s.kind)
			fmt.Fprintf(&b, "%s %g\n", name, s.value)
			if s.count > 0 {
				fmt.Fprintf(&b, "%s_count %g\n", name, s.count)
				fmt.Fprintf(&b, "%s_sum %g\n", name, s.sum)
			}
		}
		r.mu.Unlock()

		_, _ = w.Write([]byte(b.String()))
	})
}
