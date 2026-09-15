// 预算感知的调用调度：限流（令牌桶）+ 退避 + 审计队列选择。
//
// 为什么需要：外部数据源（GoPlus、行情、链上探针）都有配额，且限流后的重试会加剧阻塞。
// 两个纪律（参考 nhovongoc0-max/meme-radar 的 selectAuditQueue）：
//  1. **限流即停**：一旦上游回 429/限流，本轮立即停止后续调用并退避，
//     而不是把剩余请求全部撞上去（那只会延长被封禁的时间）；
//  2. **预算感知**：每一轮审计只花固定预算，按"优先级高 + 更久没审"排序，
//     超出预算的顺延——避免少数热点把配额吃光、其余对象永远轮不到。
package provider

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- 令牌桶 ----

// TokenBucket 令牌桶。
//
// 语义：以 refillPerSecond 匀速补充，容量上限 capacity（允许短时突发）。
// 不依赖真实时钟：now 由调用方传入，便于测试与回测复用。
type TokenBucket struct {
	capacity        float64
	refillPerSecond float64
	tokens          float64
	last            time.Time
}

// NewTokenBucket 构建令牌桶：perMinute 为每分钟允许调用数，burst 为突发容量（<=0 时取 perMinute）。
func NewTokenBucket(perMinute, burst int) *TokenBucket {
	if perMinute <= 0 {
		perMinute = 60
	}
	if burst <= 0 {
		burst = perMinute
	}
	return &TokenBucket{
		capacity:        float64(burst),
		refillPerSecond: float64(perMinute) / 60.0,
		tokens:          float64(burst),
	}
}

// Allow 尝试取用 1 个令牌；不足时返回 false 与建议等待时长。
func (b *TokenBucket) Allow(now time.Time) (bool, time.Duration) {
	if b == nil {
		return true, 0
	}
	b.refill(now)
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	missing := 1 - b.tokens
	wait := time.Duration(missing / b.refillPerSecond * float64(time.Second))
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return false, wait
}

// Refund 归还一个令牌（调用失败且非限流时使用，避免把失败也算作消耗）。
func (b *TokenBucket) Refund() {
	if b == nil {
		return
	}
	if b.tokens < b.capacity {
		b.tokens++
	}
}

func (b *TokenBucket) refill(now time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if b.last.IsZero() {
		b.last = now
		return
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens = math.Min(b.capacity, b.tokens+elapsed*b.refillPerSecond)
	b.last = now
}

// ---- 按 key 限流 + 退避 ----

const (
	// minRateLimitBackoff 上游限流后的最短退避。
	minRateLimitBackoff = 30 * time.Second
	// maxRateLimitBackoff 退避上限。
	maxRateLimitBackoff = 30 * time.Minute
)

type backoffState struct {
	attempts int
	nextAt   time.Time
}

// Limiter 按 provider key 维度做限流与退避（并发安全）。
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*TokenBucket
	backoff map[string]backoffState
	// PerMinute 每个 key 的默认配额（首次见到的 key 用之初始化）
	PerMinute int
	Burst     int
}

// NewLimiter 构建限流器。
func NewLimiter(perMinute, burst int) *Limiter {
	return &Limiter{buckets: make(map[string]*TokenBucket), backoff: make(map[string]backoffState), PerMinute: perMinute, Burst: burst}
}

// Allow 判断该 key 当前是否允许调用；不允许时给出建议等待时长。
func (l *Limiter) Allow(key string, now time.Time) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if st, ok := l.backoff[key]; ok && !st.nextAt.IsZero() {
		if now.Before(st.nextAt) {
			return false, st.nextAt.Sub(now)
		}
		// 退避期已过：清除退避，但保留 attempts（用于下次限流时的递增退避）
		st.nextAt = time.Time{}
		l.backoff[key] = st
	}

	b, ok := l.buckets[key]
	if !ok {
		b = NewTokenBucket(l.PerMinute, l.Burst)
		l.buckets[key] = b
	}
	return b.Allow(now)
}

// Refund 归还该 key 的一个令牌（调用失败且非限流时使用）。
// 理由：失败并不代表配额被上游消费，白扣会让限流器比真实配额更早失效。
func (l *Limiter) Refund(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[key]; ok {
		b.Refund()
	}
}

// MarkSuccess 记一次成功调用（清除退避计数）。
func (l *Limiter) MarkSuccess(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.backoff, key)
}

// MarkRateLimited 记录上游限流，返回本轮应退避的时长（指数增长，封顶）。
//
// 调用方拿到时长后应当**立即停止本轮后续调用**——继续请求只会加剧限流。
func (l *Limiter) MarkRateLimited(key string, now time.Time) time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.backoff[key]
	st.attempts++
	d := NextRateLimitBackoff(st.attempts)
	st.nextAt = now.Add(d)
	l.backoff[key] = st
	return d
}

// NextRateLimitBackoff 退避时长（30s 起翻倍，上限 30 分钟）。
func NextRateLimitBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := minRateLimitBackoff << uint(minInt(attempts-1, 6))
	if d > maxRateLimitBackoff {
		d = maxRateLimitBackoff
	}
	return d
}

// IsRateLimitedError 判断错误是否属于"上游限流/配额耗尽"。
//
// 判据取保守集合（429、too many requests、rate limit、quota）——宁可漏判也不能误判：
// 误判会让正常错误被当成限流，从而无谓地停止整轮调用。
func IsRateLimitedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"429", "too many requests", "rate limit", "ratelimit", "quota exceeded", "限流"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// ---- 审计队列调度 ----

// AuditCandidate 审计候选。
type AuditCandidate struct {
	Key         string    // 唯一标识（如 chain|token）
	Score       float64   // 优先级（越高越先审）
	LastAudited time.Time // 上次审计时间（越久越优先）
}

// SelectAuditQueue 在预算内挑选本轮要审计的对象（纯函数）。
//
// 规则：
//   - 距上次审计不足 minInterval 的对象跳过（避免反复审同一个，浪费配额）；
//   - 排序：Score 降序 → LastAudited 更早者优先 → Key 字典序（保证结果稳定可测）；
//   - 只取前 budget 个，其余顺延到下一轮。
func SelectAuditQueue(cands []AuditCandidate, budget int, now time.Time, minInterval time.Duration) []string {
	if budget <= 0 || len(cands) == 0 {
		return nil
	}
	if minInterval < 0 {
		minInterval = 0
	}

	fresh := make([]AuditCandidate, 0, len(cands))
	for _, c := range cands {
		if c.Key == "" {
			continue
		}
		if minInterval > 0 && !c.LastAudited.IsZero() && now.Sub(c.LastAudited) < minInterval {
			continue
		}
		fresh = append(fresh, c)
	}

	sort.SliceStable(fresh, func(i, j int) bool {
		if fresh[i].Score != fresh[j].Score {
			return fresh[i].Score > fresh[j].Score
		}
		if !fresh[i].LastAudited.Equal(fresh[j].LastAudited) {
			return fresh[i].LastAudited.Before(fresh[j].LastAudited)
		}
		return fresh[i].Key < fresh[j].Key
	})

	if len(fresh) > budget {
		fresh = fresh[:budget]
	}
	out := make([]string, 0, len(fresh))
	for _, c := range fresh {
		out = append(out, c.Key)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
