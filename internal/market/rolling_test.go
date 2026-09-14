package market

import (
	"testing"
	"time"
)

func TestRollingWindowMeanAndMultiple(t *testing.T) {
	w := NewRollingWindow(time.Hour, 100)
	now := time.Now().UTC()

	for i := 0; i < 4; i++ {
		w.Observe("base|T", 100, now.Add(-time.Duration(i)*time.Minute))
	}

	mean, count := w.Mean("base|T")
	if count != 4 || mean != 100 {
		t.Fatalf("均值应为 100（4 个样本），实际 %v / %d", mean, count)
	}

	if got := w.Multiple("base|T", 600, 3); got != 6 {
		t.Fatalf("600 相对均值 100 应为 6 倍，实际 %v", got)
	}
	if got := w.Multiple("base|T", 0, 3); got != 0 {
		t.Fatalf("非法当前值应返回 0，实际 %v", got)
	}
	if got := w.Multiple("base|absent", 600, 3); got != 0 {
		t.Fatalf("无样本应返回 0，实际 %v", got)
	}
}

func TestRollingWindowInsufficientSamples(t *testing.T) {
	w := NewRollingWindow(time.Hour, 100)
	now := time.Now().UTC()
	w.Observe("base|T", 100, now)
	w.Observe("base|T", 100, now)

	if got := w.Multiple("base|T", 500, 3); got != 0 {
		t.Fatalf("样本不足（2 < 3）应返回 0 表示暂不判定，实际 %v", got)
	}
	if got := w.Multiple("base|T", 500, 2); got != 5 {
		t.Fatalf("门槛降到 2 时应得到 5 倍，实际 %v", got)
	}
}

func TestRollingWindowEviction(t *testing.T) {
	w := NewRollingWindow(10*time.Minute, 3)
	now := time.Now().UTC()

	// 超窗样本会被剔除
	w.Observe("base|T", 500, now.Add(-30*time.Minute))
	w.Observe("base|T", 100, now.Add(-time.Minute))
	mean, count := w.Mean("base|T")
	if count != 1 || mean != 100 {
		t.Fatalf("超窗样本应被剔除，实际 %v / %d", mean, count)
	}

	// 容量上限：只保留最新
	for i := 0; i < 10; i++ {
		w.Observe("base|T2", float64(i+1), now)
	}
	if n := w.Len("base|T2"); n != 3 {
		t.Fatalf("容量上限应为 3，实际 %d", n)
	}

	// 零值与空 key 被忽略
	w.Observe("", 100, now)
	w.Observe("base|T3", 0, now)
	if n := w.Len("base|T3"); n != 0 {
		t.Fatalf("非法观测应被忽略，实际 %d", n)
	}
}

func TestRollingWindowHonorsInjectedClock(t *testing.T) {
	// 远古事件时间 + 注入时钟：样本必须保留（回测语义）。
	// 修复前 Mean 用 wall clock，会把历史样本全部判为超窗剔除，倍数恒为 0。
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := NewRollingWindow(time.Hour, 64).WithClock(func() time.Time { return base.Add(10 * time.Minute) })
	for i := 0; i < 3; i++ {
		w.Observe("k", 100, base.Add(time.Duration(i)*time.Minute))
	}

	mean, count := w.Mean("k")
	if count != 3 || mean != 100 {
		t.Fatalf("注入时钟下不应剔除历史样本：mean=%v count=%d", mean, count)
	}
	if got := w.Multiple("k", 600, 3); got != 6 {
		t.Fatalf("回测语义下应算出倍数 6：%v", got)
	}

	// 时钟推进到窗口之外 → 样本应被剔除
	w.WithClock(func() time.Time { return base.Add(3 * time.Hour) })
	if _, n := w.Mean("k"); n != 0 {
		t.Fatalf("超出窗口后应剔除样本：%d", n)
	}

	// WithClock(nil) 不应清空时钟
	w.WithClock(nil)
	if w.clock == nil {
		t.Fatal("WithClock(nil) 不应清空时钟")
	}
}
