package outcome

import (
	"math"
	"testing"
	"time"
)

var testBase = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

// 稳定抽样必须"只依赖地址"：同地址重复调用结果一致，且分布大致均匀。
// 这是整套对照机制可信的前提——若抽样与后续涨跌相关，就等于人为挑选样本。
func TestShouldSampleIsStable(t *testing.T) {
	for i := 0; i < 50; i++ {
		a := ShouldSample("base", "0xTokenA", 5)
		b := ShouldSample("base", "0xTokenA", 5)
		if a != b {
			t.Fatal("同一输入两次调用结果不一致（抽样不稳定）")
		}
	}
	if !ShouldSample("base", "0xAny", 1) {
		t.Fatal("mod<=1 应全量采样")
	}
	if !ShouldSample("base", "0xAny", 0) {
		t.Fatal("mod=0 应全量采样")
	}
}

func TestShouldSampleIsBoundedAroundOneInFive(t *testing.T) {
	hits := 0
	total := 2000
	for i := 0; i < total; i++ {
		token := "0xtoken" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
		if ShouldSample("base", token, 5) {
			hits++
		}
	}
	// 期望 ~20%；给足容差但必须明显落在合理区间（防哈希取模写错导致全命中/全不命中）
	low, high := total/10, total*3/10
	if hits < low || hits > high {
		t.Fatalf("抽样比例异常：命中 %d/%d（期望约 %d）", hits, total, total/5)
	}
}

func TestNewRecordRequiresBaselinePrice(t *testing.T) {
	if _, ok := NewRecord("base", "0xT", DecisionSignaled, "", testBase, 0, "v1", nil); ok {
		t.Fatal("无基准价不应跟踪（不能臆造基准）")
	}
	if _, ok := NewRecord("base", "0xT", DecisionSignaled, "", testBase, -1, "v1", nil); ok {
		t.Fatal("负价格不应跟踪")
	}
	rec, ok := NewRecord("base", "0xT", DecisionSignaled, "", testBase, 1.5, "v1", nil)
	if !ok || rec == nil {
		t.Fatal("有效价格应可跟踪")
	}
	if rec.Sampling != SamplingFull {
		t.Fatalf("信号应全量跟踪：%s", rec.Sampling)
	}
	if !rec.BaselineAt.Equal(testBase) {
		t.Fatalf("基准时间应转为 UTC 保存：%v", rec.BaselineAt)
	}
}

func TestNewRecordFilteredUsesStableSampling(t *testing.T) {
	kept, skipped := 0, 0
	for i := 0; i < 200; i++ {
		token := "0xf" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
		if _, ok := NewRecord("base", token, DecisionFiltered, "liquidity", testBase, 1, "v1", nil); ok {
			kept++
		} else {
			skipped++
		}
	}
	if kept == 0 || skipped == 0 {
		t.Fatalf("被过滤样本应部分保留：kept=%d skipped=%d", kept, skipped)
	}
}

func TestDueHonorsGraceAndRetryBackoff(t *testing.T) {
	rec, _ := NewRecord("base", "0xT", DecisionExecuted, "", testBase, 100, "v1", nil)

	// 刚过 5 分钟但仍在 grace 内 → 不到期
	if got := rec.Due(DefaultHorizons, testBase.Add(5*time.Minute+30*time.Second), DefaultSampleGrace); len(got) != 0 {
		t.Fatalf("grace 内不应到期：%v", got)
	}

	// 过了 5 分钟 + grace → m5 到期
	got := rec.Due(DefaultHorizons, testBase.Add(5*time.Minute+90*time.Second), DefaultSampleGrace)
	if len(got) != 1 || got[0].Key != "m5" {
		t.Fatalf("m5 应到期：%+v", got)
	}

	// 采样成功后不再到期
	rec.SetSample(got[0], testBase.Add(5*time.Minute), testBase.Add(6*time.Minute), 110)
	if again := rec.Due(DefaultHorizons, testBase.Add(10*time.Minute), DefaultSampleGrace); len(again) != 0 {
		t.Fatalf("已采样的视野不应再次到期：%+v", again)
	}

	// 失败后进入退避期 → 不到期；退避结束后恢复
	rec2, _ := NewRecord("base", "0xT2", DecisionExecuted, "", testBase, 100, "v1", nil)
	now := testBase.Add(5*time.Minute + time.Minute)
	rec2.SetFailure(Horizon{Key: "m5", Duration: 5 * time.Minute}, "NO_PRICE", now)
	if got := rec2.Due(DefaultHorizons, now.Add(time.Minute), DefaultSampleGrace); len(got) != 0 {
		t.Fatalf("退避期内不应重试：%+v", got)
	}
	after := NextRetry(1, now).Add(time.Second)
	if got := rec2.Due(DefaultHorizons, after, DefaultSampleGrace); len(got) == 0 {
		t.Fatal("退避结束后应恢复采样")
	}
}

func TestSetSampleComputesReturn(t *testing.T) {
	rec, _ := NewRecord("base", "0xT", DecisionExecuted, "", testBase, 100, "v1", nil)
	h := Horizon{Key: "h1", Duration: time.Hour}
	if !rec.SetSample(h, testBase.Add(time.Hour), testBase.Add(time.Hour), 150) {
		t.Fatal("有效采样应成功")
	}
	if got := rec.Samples["h1"].Ret; math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("收益应为 0.5：%v", got)
	}
	if rec.SetSample(h, testBase.Add(time.Hour), testBase.Add(time.Hour), 0) {
		t.Fatal("价格为 0 的采样应视为失败")
	}
}

func TestNextRetryBackoffIsCapped(t *testing.T) {
	now := testBase
	if got := NextRetry(1, now).Sub(now); got != 2*time.Minute {
		t.Fatalf("首次退避应为 2 分钟：%v", got)
	}
	if got := NextRetry(3, now).Sub(now); got != 8*time.Minute {
		t.Fatalf("第三次退避应为 8 分钟：%v", got)
	}
	for _, n := range []int{6, 10, 100} {
		if got := NextRetry(n, now).Sub(now); got > MaxRetryBackoff {
			t.Fatalf("退避应封顶 %v，实际 %v（attempts=%d）", MaxRetryBackoff, got, n)
		}
	}
	if got := NextRetry(0, now).Sub(now); got != 2*time.Minute {
		t.Fatalf("attempts<1 应按 1 处理：%v", got)
	}
}

func TestCoverageMedianAndPositiveRate(t *testing.T) {
	h := []Horizon{{Key: "h1", Duration: time.Hour}}
	mk := func(token string, price float64) *Record {
		rec, _ := NewRecord("base", token, DecisionExecuted, "", testBase, 100, "v1", nil)
		rec.SetSample(h[0], testBase.Add(time.Hour), testBase.Add(time.Hour), price)
		return rec
	}
	// 收益：+0.5 / +1.0 / -0.5 → 中位数 +0.5，正收益率 2/3
	recs := []*Record{mk("0xa", 150), mk("0xb", 200), mk("0xc", 50)}
	now := testBase.Add(2 * time.Hour)

	cov := Coverage(recs, h, now)
	stats, ok := cov[DecisionExecuted]
	if !ok || len(stats) != 1 {
		t.Fatalf("应按决策分层返回：%+v", cov)
	}
	st := stats[0]
	if st.Eligible != 3 || st.Completed != 3 || st.Missing != 0 {
		t.Fatalf("覆盖统计错误：%+v", st)
	}
	if st.Median == nil || math.Abs(*st.Median-0.5) > 1e-9 {
		t.Fatalf("中位数应为 0.5：%v", st.Median)
	}
	if st.PositiveRate == nil || math.Abs(*st.PositiveRate-2.0/3.0) > 1e-9 {
		t.Fatalf("正收益率应为 2/3：%v", st.PositiveRate)
	}
	if st.Calibratable {
		t.Fatal("3 个样本不应具备调参资格（阈值 %d）", MinSamplesForCalibration)
	}
}

func TestCoverageExcludesNotYetDueAndReportsMissingAsNil(t *testing.T) {
	h := []Horizon{{Key: "h24", Duration: 24 * time.Hour}}
	rec, _ := NewRecord("base", "0xT", DecisionFiltered, "liquidity", testBase, 100, "v1", nil)

	// 尚未到期：不计入分母
	cov := Coverage([]*Record{rec}, h, testBase.Add(time.Hour))
	st := cov[DecisionFiltered][0]
	if st.Eligible != 0 || st.Completed != 0 {
		t.Fatalf("未到期不应计入分母：%+v", st)
	}
	if st.Median != nil || st.PositiveRate != nil {
		t.Fatal("无样本时统计必须为 nil——返回 0 会被误读为『收益为 0』")
	}

	// 到期但未采样：计入 eligible 与 missing，统计仍为 nil
	cov = Coverage([]*Record{rec}, h, testBase.Add(25*time.Hour))
	st = cov[DecisionFiltered][0]
	if st.Eligible != 1 || st.Missing != 1 || st.Completed != 0 {
		t.Fatalf("缺失应被显式计数：%+v", st)
	}
	if st.Median != nil {
		t.Fatal("缺失时中位数必须为 nil")
	}
}

func TestCoverageCalibratableAtThreshold(t *testing.T) {
	h := []Horizon{{Key: "h1", Duration: time.Hour}}
	recs := make([]*Record, 0, MinSamplesForCalibration)
	for i := 0; i < MinSamplesForCalibration; i++ {
		rec, _ := NewRecord("base", "0xtok"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26)), DecisionExecuted, "", testBase, 100, "v1", nil)
		rec.SetSample(h[0], testBase.Add(time.Hour), testBase.Add(time.Hour), 120)
		recs = append(recs, rec)
	}
	cov := Coverage(recs, h, testBase.Add(2*time.Hour))
	st := cov[DecisionExecuted][0]
	if st.Completed != MinSamplesForCalibration {
		t.Fatalf("样本数错误：%d", st.Completed)
	}
	if !st.Calibratable {
		t.Fatalf("样本达到阈值（%d）应具备调参资格", MinSamplesForCalibration)
	}
}
