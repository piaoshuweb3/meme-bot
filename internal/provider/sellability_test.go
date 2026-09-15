package provider

import (
	"context"
	"errors"
	"testing"
)

// 判定口径必须被穷举：unknown 绝不能等同于"安全"。
func TestClassifySellability(t *testing.T) {
	cases := []struct {
		name        string
		buys, sells int
		minSamples  int
		wantVerdict SellabilityVerdict
	}{
		{"有卖出即可卖", 3, 1, 5, VerdictSellable},
		{"只有买入且样本足够→无人卖出", 5, 0, 5, VerdictNoSellPath},
		{"只有买入但样本不足→未知", 4, 0, 5, VerdictUnknown},
		{"零事件→未知", 0, 0, 5, VerdictUnknown},
		{"大量卖出", 100, 80, 5, VerdictSellable},
		{"边界：刚好达标", 5, 0, 5, VerdictNoSellPath},
		{"边界：差一笔", 4, 0, 5, VerdictUnknown},
	}
	for _, c := range cases {
		got, evidence := ClassifySellability(c.buys, c.sells, c.minSamples)
		if got != c.wantVerdict {
			t.Fatalf("%s：得到 %s，期望 %s（依据：%s）", c.name, got, c.wantVerdict, evidence)
		}
		if evidence == "" {
			t.Fatalf("%s：必须给出人类可读依据", c.name)
		}
	}
}

func TestClassifySellabilityHandlesBadInput(t *testing.T) {
	// 负数按 0 处理，不 panic
	if v, _ := ClassifySellability(-3, -1, 5); v != VerdictUnknown {
		t.Fatalf("负数输入应为 unknown：%s", v)
	}
	// minSamples<=0 时用默认阈值
	if v, _ := ClassifySellability(MinSellSamples, 0, 0); v != VerdictNoSellPath {
		t.Fatalf("默认阈值下应判定 no_sell_path：%s", v)
	}
}

// 只要存在卖出就不该被误判为不可卖——误判会让我们错过真实机会。
func TestSellableOnlyWhenSellsExist(t *testing.T) {
	rep := &SellabilityReport{Verdict: VerdictSellable, Sells: 1}
	if !rep.Sellable() {
		t.Fatal("sellable 应返回 true")
	}
	for _, v := range []SellabilityVerdict{VerdictUnknown, VerdictNoSellPath} {
		r := &SellabilityReport{Verdict: v}
		if r.Sellable() {
			t.Fatalf("%s 不得被视为可卖（证据不足/无人卖出都不能当作通过）", v)
		}
	}
	var nilRep *SellabilityReport
	if nilRep.Sellable() {
		t.Fatal("nil 报告不得被视为可卖")
	}
}

func TestSellabilityAssessUsesInjectedCounter(t *testing.T) {
	// 用 stub 计数器验证 Assess 的组合逻辑（含比例计算与证据文案）
	stub := &stubCounter{stats: TransferStats{Buys: 10, Sells: 4}}
	rep, err := assessWithCounter(context.Background(), stub, "base", "0xToken", "0xPool", 10, 100)
	if err != nil {
		t.Fatalf("Assess 失败：%v", err)
	}
	if rep.Verdict != VerdictSellable {
		t.Fatalf("应判可卖：%+v", rep)
	}
	if rep.Buys != 10 || rep.Sells != 4 {
		t.Fatalf("统计透传错误：%+v", rep)
	}
	// 4/(10+4) = 0.2857...
	if rep.SellRatio < 0.28 || rep.SellRatio > 0.29 {
		t.Fatalf("卖出比例计算错误：%v", rep.SellRatio)
	}
	if rep.Source == "" || rep.CheckedAt.IsZero() {
		t.Fatalf("应标注来源与时间：%+v", rep)
	}
}

func TestSellabilityAssessPropagatesError(t *testing.T) {
	stub := &stubCounter{err: errors.New("rpc down")}
	if _, err := assessWithCounter(context.Background(), stub, "base", "0xT", "0xP", 1, 2); err == nil {
		t.Fatal("上游失败必须返回错误，不能静默给出结论")
	}
}

type stubCounter struct {
	stats TransferStats
	err   error
}

func (s *stubCounter) CountTransfers(context.Context, string, string, string, uint64, uint64) (TransferStats, error) {
	if s.err != nil {
		return TransferStats{}, s.err
	}
	return s.stats, nil
}

func TestHexHelpers(t *testing.T) {
	if got := hexUint64(0); got != "0x0" {
		t.Fatalf("0 应为 0x0：%q", got)
	}
	if got := hexUint64(255); got != "0xff" {
		t.Fatalf("255 应为 0xff：%q", got)
	}
	if v, err := parseHexUint64("0xFF"); err != nil || v != 255 {
		t.Fatalf("解析失败：%v %v", v, err)
	}
	if _, err := parseHexUint64("0x"); err == nil {
		t.Fatal("空高度应报错")
	}
	if _, err := parseHexUint64("zz"); err == nil {
		t.Fatal("非法高度应报错")
	}

	addr := "0x10687368eF1be3f178de0fCCf5EdfF49e1C258B1"
	if !isHexAddress(addr) {
		t.Fatal("合法地址被判非法")
	}
	for _, bad := range []string{"", "0x123", "10687368eF1be3f178de0fCCf5EdfF49e1C258B1", "0xZZZZ7368eF1be3f178de0fCCf5EdfF49e1C258B1"} {
		if isHexAddress(bad) {
			t.Fatalf("非法地址被判合法：%q", bad)
		}
	}

	topic := padTopicHex(addr)
	if len(topic) != 66 || topic != "0x000000000000000000000000"+"10687368ef1be3f178de0fccf5edff49e1c258b1" {
		t.Fatalf("topic 填充错误：%q", topic)
	}
}
