package provider

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var budgetBase = time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

func TestTokenBucketAllowsBurstThenThrottles(t *testing.T) {
	// 每分钟 60 → 每秒 1；突发容量 5
	b := NewTokenBucket(60, 5)

	for i := 0; i < 5; i++ {
		if ok, wait := b.Allow(budgetBase); !ok {
			t.Fatalf("第 %d 次应在突发容量内被允许（wait=%v）", i+1, wait)
		}
	}
	ok, wait := b.Allow(budgetBase)
	if ok {
		t.Fatal("突发容量耗尽后应被拒绝")
	}
	if wait <= 0 {
		t.Fatalf("拒绝时必须给出建议等待时长：%v", wait)
	}

	// 时间前进 1 秒 → 应补回 1 个令牌（用传入的 now，不依赖真实时钟）
	if ok, _ := b.Allow(budgetBase.Add(time.Second)); !ok {
		t.Fatal("1 秒后应补充 1 个令牌")
	}
	// 再立即取用应再次被拒（令牌已用完）
	if ok, _ := b.Allow(budgetBase.Add(time.Second)); ok {
		t.Fatal("同一时刻不应连续放行超过补充速率")
	}
}

func TestTokenBucketRefillIsCappedAtCapacity(t *testing.T) {
	b := NewTokenBucket(60, 2)
	_, _ = b.Allow(budgetBase) // 用掉 1 个
	// 长时间空闲后只应补到容量上限（2），不会无限累积
	for i := 0; i < 2; i++ {
		if ok, _ := b.Allow(budgetBase.Add(time.Hour)); !ok {
			t.Fatalf("空闲后第 %d 次应被允许", i+1)
		}
	}
	if ok, _ := b.Allow(budgetBase.Add(time.Hour)); ok {
		t.Fatal("令牌数必须封顶在容量，不能无限累积")
	}
}

func TestTokenBucketRefund(t *testing.T) {
	b := NewTokenBucket(60, 1)
	if ok, _ := b.Allow(budgetBase); !ok {
		t.Fatal("首个令牌应可用")
	}
	if ok, _ := b.Allow(budgetBase); ok {
		t.Fatal("容量 1 时第二次应被拒")
	}
	b.Refund()
	if ok, _ := b.Allow(budgetBase); !ok {
		t.Fatal("Refund 后应可再次取用（失败调用不该白白消耗配额）")
	}
}

func TestTokenBucketDefaults(t *testing.T) {
	// 非法参数应回落到可用默认值，而不是 panic 或永不放行
	b := NewTokenBucket(0, 0)
	if ok, _ := b.Allow(budgetBase); !ok {
		t.Fatal("默认参数应可用")
	}
	var nilBucket *TokenBucket
	if ok, _ := nilBucket.Allow(budgetBase); !ok {
		t.Fatal("nil 桶应放行（未配置限流 = 不限流）")
	}
	nilBucket.Refund() // 不应 panic
}

func TestLimiterRateLimitBackoffAndRecovery(t *testing.T) {
	l := NewLimiter(60, 2)

	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("goplus", budgetBase); !ok {
			t.Fatalf("第 %d 次应在配额内", i+1)
		}
	}
	if ok, _ := l.Allow("goplus", budgetBase); ok {
		t.Fatal("配额耗尽应拒绝")
	}

	// 上游限流 → 退避期内一律拒绝
	d := l.MarkRateLimited("goplus", budgetBase)
	if d < minRateLimitBackoff {
		t.Fatalf("首次退避不得低于 %v：%v", minRateLimitBackoff, d)
	}
	if ok, wait := l.Allow("goplus", budgetBase.Add(d/2)); ok {
		t.Fatalf("退避期内应拒绝（wait=%v）", wait)
	}
	// 退避结束（且时间足够补回令牌）→ 恢复
	if ok, _ := l.Allow("goplus", budgetBase.Add(d+2*time.Minute)); !ok {
		t.Fatal("退避结束后应恢复调用")
	}

	// 不同 key 互不影响
	if ok, _ := l.Allow("dexscreener", budgetBase); !ok {
		t.Fatal("不同 provider 的配额应相互独立")
	}
}

func TestLimiterMarkSuccessClearsBackoff(t *testing.T) {
	l := NewLimiter(60, 10)
	l.MarkRateLimited("goplus", budgetBase)
	l.MarkSuccess("goplus")
	// 退避被清除 → 立即应可用（令牌充足）
	if ok, _ := l.Allow("goplus", budgetBase); !ok {
		t.Fatal("MarkSuccess 后应清除退避")
	}
	// nil 限流器必须放行（未配置 = 不限流）
	var nilLimiter *Limiter
	if ok, _ := nilLimiter.Allow("x", budgetBase); !ok {
		t.Fatal("nil Limiter 应放行")
	}
	nilLimiter.MarkSuccess("x")
	if d := nilLimiter.MarkRateLimited("x", budgetBase); d != 0 {
		t.Fatalf("nil Limiter 的退避应为 0：%v", d)
	}
}

func TestNextRateLimitBackoffGrowsAndCaps(t *testing.T) {
	if got := NextRateLimitBackoff(1); got != minRateLimitBackoff {
		t.Fatalf("首次应为 %v：%v", minRateLimitBackoff, got)
	}
	if got := NextRateLimitBackoff(2); got != 2*minRateLimitBackoff {
		t.Fatalf("第二次应翻倍：%v", got)
	}
	if got := NextRateLimitBackoff(0); got != minRateLimitBackoff {
		t.Fatalf("attempts<1 应按 1 处理：%v", got)
	}
	for _, n := range []int{7, 20, 100} {
		if got := NextRateLimitBackoff(n); got > maxRateLimitBackoff {
			t.Fatalf("退避必须封顶 %v：%v (attempts=%d)", maxRateLimitBackoff, got, n)
		}
	}
}

func TestIsRateLimitedError(t *testing.T) {
	yes := []string{
		"http 429: too many requests",
		"Too Many Requests",
		"rate limit exceeded",
		"RATE_LIMITED",
		"quota exceeded for today",
		"上游限流",
	}
	for _, m := range yes {
		if !IsRateLimitedError(errors.New(m)) {
			t.Fatalf("应识别为限流：%q", m)
		}
	}
	no := []string{
		"connection refused",
		"invalid price \"abc\"",
		"decode json: unexpected end of JSON input",
		"no pair for token on base",
	}
	for _, m := range no {
		if IsRateLimitedError(errors.New(m)) {
			t.Fatalf("不得误判为限流（会导致无谓停止整轮调用）：%q", m)
		}
	}
	if IsRateLimitedError(nil) {
		t.Fatal("nil 错误不是限流")
	}
}

func TestSelectAuditQueue(t *testing.T) {
	now := budgetBase
	cands := []AuditCandidate{
		{Key: "hot-new", Score: 0.9, LastAudited: now.Add(-time.Hour)},
		{Key: "hot-recent", Score: 0.9, LastAudited: now.Add(-time.Minute)},
		{Key: "mid", Score: 0.5, LastAudited: now.Add(-2 * time.Hour)},
		{Key: "cold", Score: 0.1, LastAudited: now.Add(-24 * time.Hour)},
	}

	// 预算 2：应取分数最高者；同分时"更久没审"优先
	got := SelectAuditQueue(cands, 2, now, 10*time.Minute)
	if len(got) != 2 {
		t.Fatalf("预算 2 应返回 2 个：%v", got)
	}
	if got[0] != "hot-new" {
		t.Fatalf("同分时应优先更久未审计的：%v", got)
	}
	if got[1] != "mid" {
		t.Fatalf("第二个应为次高分：%v", got)
	}

	// minInterval 过滤掉刚审过的
	got = SelectAuditQueue(cands, 5, now, 10*time.Minute)
	for _, k := range got {
		if k == "hot-recent" {
			t.Fatalf("刚审过（< minInterval）的对象不应入选：%v", got)
		}
	}

	// 预算充足时按分数降序返回全部合格项
	got = SelectAuditQueue(cands, 10, now, 0)
	if len(got) != 4 || got[0] != "hot-new" || got[len(got)-1] != "cold" {
		t.Fatalf("应按分数降序返回：%v", got)
	}

	// 边界：零预算 / 空输入 / 空 key
	if got := SelectAuditQueue(cands, 0, now, 0); got != nil {
		t.Fatalf("零预算应返回空：%v", got)
	}
	if got := SelectAuditQueue(nil, 5, now, 0); got != nil {
		t.Fatalf("空输入应返回空：%v", got)
	}
	if got := SelectAuditQueue([]AuditCandidate{{Key: "", Score: 1}}, 5, now, 0); len(got) != 0 {
		t.Fatalf("空 key 应被跳过：%v", got)
	}

	// 结果稳定（同分同时间时按 Key 字典序，便于测试与复现）
	same := []AuditCandidate{
		{Key: "b", Score: 1, LastAudited: now.Add(-time.Hour)},
		{Key: "a", Score: 1, LastAudited: now.Add(-time.Hour)},
	}
	got = SelectAuditQueue(same, 2, now, 0)
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("同分同时间应按 Key 稳定排序：%v", got)
	}
}
