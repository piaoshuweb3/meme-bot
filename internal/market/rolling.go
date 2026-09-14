// Package market 提供行情衍生统计：滚动窗口均值（用于"相对均值突增"判定）。
//
// 为什么不能只做比值近似：成交量/流动性的**比值**在不同代币间不可比（池子深度差异极大），
// 因此信号漏斗需要"当前值相对该代币自身近期均值"的倍数——即本包的滚动窗口。
package market

import (
	"sync"
	"time"
)

// sample 单个观测样本。
type sample struct {
	at    time.Time
	value float64
}

// RollingWindow 按 key 维护带时间戳的样本，提供窗口均值与倍数。
//
// 并发安全；样本超窗或超容量自动淘汰（内存有界）。
type RollingWindow struct {
	mu       sync.Mutex
	window   time.Duration
	capacity int
	samples  map[string][]sample

	// clock 判定样本新鲜度所用的时钟。
	// 必须在回测中注入事件时钟：若沿用 wall clock，历史样本会被判为"超窗"全部剔除，
	// 导致突增倍数恒为 0（回测结论失真）。应在启动阶段注入，之后不再变更。
	clock func() time.Time
}

// NewRollingWindow 构建滚动窗口；window <= 0 默认 1 小时，capacity <= 0 默认 720。
func NewRollingWindow(window time.Duration, capacity int) *RollingWindow {
	if window <= 0 {
		window = time.Hour
	}
	if capacity <= 0 {
		capacity = 720
	}
	return &RollingWindow{
		window:   window,
		capacity: capacity,
		samples:  make(map[string][]sample),
		clock:    time.Now,
	}
}

// WithClock 注入时钟（回测场景：用事件时间判断样本新鲜度）。
// 传 nil 表示保持不变。
func (w *RollingWindow) WithClock(now func() time.Time) *RollingWindow {
	if now != nil {
		w.clock = now
	}
	return w
}

// now 当前时间（带 UTC 归一与 nil 兜底）。
func (w *RollingWindow) now() time.Time {
	if w.clock == nil {
		return time.Now().UTC()
	}
	return w.clock().UTC()
}

// Observe 记录一次观测（at 为零时使用当前时间）。
func (w *RollingWindow) Observe(key string, value float64, at time.Time) {
	if key == "" || value <= 0 {
		return
	}
	if at.IsZero() {
		at = w.now()
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	list := append(w.samples[key], sample{at: at, value: value})
	list = trim(list, at.Add(-w.window), w.capacity)
	w.samples[key] = list
}

// Mean 返回窗口内均值与样本数。
func (w *RollingWindow) Mean(key string) (mean float64, count int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	list := trim(w.samples[key], w.now().Add(-w.window), w.capacity)
	w.samples[key] = list
	if len(list) == 0 {
		return 0, 0
	}
	var sum float64
	for _, s := range list {
		sum += s.value
	}
	return sum / float64(len(list)), len(list)
}

// Multiple 返回 current 相对窗口均值的倍数。
//
// 样本不足（< minSamples）时返回 0，表示"无法判定"——调用方应据此跳过突增判定，
// 避免冷启动阶段的误报。
func (w *RollingWindow) Multiple(key string, current float64, minSamples int) float64 {
	if current <= 0 {
		return 0
	}
	if minSamples <= 0 {
		minSamples = 3
	}
	mean, count := w.Mean(key)
	if count < minSamples || mean <= 0 {
		return 0
	}
	return current / mean
}

// Len 当前样本数（诊断用）。
func (w *RollingWindow) Len(key string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.samples[key])
}

// trim 剔除窗口外样本并限制容量（保留最新）。
func trim(list []sample, cutoff time.Time, capacity int) []sample {
	idx := 0
	for idx < len(list) && list[idx].at.Before(cutoff) {
		idx++
	}
	list = list[idx:]
	if len(list) > capacity {
		list = list[len(list)-capacity:]
	}
	return list
}
