// Command backtest 用历史或合成事件回放验证策略表现。
//
// 用法：
//
//	go run ./cmd/backtest -emit-sample > events.jsonl      # 生成示例事件
//	go run ./cmd/backtest -events events.jsonl             # 回放并打印摘要
//	go run ./cmd/backtest -events events.jsonl -out report.json
//
// 说明：回测复用实盘策略漏斗与风控引擎（同一套代码），仅把时钟换成事件时间、
// 把执行换成按事件价格 + 滑点 + 手续费的模拟成交。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"meme-bot/internal/address"
	"meme-bot/internal/backtest"
	"meme-bot/internal/config"
	"meme-bot/internal/logger"
	"meme-bot/internal/model"
	"meme-bot/internal/risk"
	"meme-bot/internal/strategy"
)

// lf 避免在源码中出现转义换行（便于跨工具链生成与审阅）。
var lf = string(rune(10))

// alwaysLarge 是"画像缺失"时的兜底评分器：把所有买入视为候选大额买入。
type alwaysLarge struct{}

// Score 实现 model.ScorerPort。
func (alwaysLarge) Score(*model.AddressProfile) float64 { return 1 }

// IsEligible 实现 model.ScorerPort。
func (alwaysLarge) IsEligible(context.Context, string, string) (bool, float64, error) {
	return true, 1, nil
}

// IsLargeBuy 实现 model.ScorerPort。
func (alwaysLarge) IsLargeBuy(context.Context, string, string, float64) (bool, error) {
	return true, nil
}

var (
	flagEvents   = flag.String("events", "", "事件文件（JSONL，每行一个 SwapEvent）")
	flagConfig   = flag.String("config", "configs/config.yaml", "配置文件路径")
	flagOut      = flag.String("out", "", "报告输出路径（JSON；留空只打印摘要）")
	flagSample   = flag.Bool("emit-sample", false, "输出示例事件到 stdout 后退出")
	flagEquity   = flag.Float64("equity", 10000, "初始权益（USD）")
	flagSlippage = flag.Int("slippage-bps", 150, "滑点（bps，双边各计一次）")
	flagFee      = flag.Int("fee-bps", 30, "手续费（bps，双边各计一次）")
	flagConfirm  = flag.Bool("follow-confirm", true, "是否要求跟风确认后才入场")
	flagTakeProf = flag.Float64("take-profit", 0.60, "止盈比例（0 关闭）")
	flagStopLoss = flag.Float64("stop-loss", 0.25, "止损比例（0 用配置默认）")
	flagHoldMin  = flag.Int("max-holding-minutes", 240, "最大持仓时长（分钟，0 不限）")
	flagLargeAll = flag.Bool("treat-all-as-large", true, "无画像时把所有买入按大额处理（快速验证漏斗）")
)

func main() {
	flag.Parse()

	if *flagSample {
		if err := backtest.SampleJSONL(os.Stdout); err != nil {
			fmt.Fprint(os.Stderr, "emit sample: "+err.Error()+lf)
			os.Exit(1)
		}
		return
	}
	if *flagEvents == "" {
		fmt.Fprint(os.Stderr, "用法："+lf)
		fmt.Fprint(os.Stderr, "  backtest -events events.jsonl [-config configs/config.yaml] [-out report.json]"+lf)
		fmt.Fprint(os.Stderr, "  backtest -emit-sample > events.jsonl"+lf)
		os.Exit(2)
	}

	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fmt.Fprint(os.Stderr, "load config: "+err.Error()+lf)
		os.Exit(1)
	}
	log, err := logger.New(cfg)
	if err != nil {
		fmt.Fprint(os.Stderr, "init logger: "+err.Error()+lf)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	src, err := backtest.LoadJSONL(*flagEvents)
	if err != nil {
		fmt.Fprint(os.Stderr, err.Error()+lf)
		os.Exit(1)
	}

	// 回测用行情桩：价格与流动性由事件驱动；安全报告恒通过（聚焦信号与风控）
	stub := backtest.NewMarketStub(0)

	// 评分器：默认兜底（无画像）；关闭 -treat-all-as-large 时使用（空的）内存画像
	var scorer model.ScorerPort = alwaysLarge{}
	if !*flagLargeAll {
		scorer = address.NewScorer(cfg.Score, cfg.Signal.LargeBuyMultiple, address.NewMemoryStore(), log)
	}

	sigEngine := strategy.NewSignalEngine(cfg.Signal, model.SignalFilter{
		MaxBuyTax:         0.10,
		MaxSellTax:        0.10,
		MinLiquidityUSD:   10000,
		RejectHoneypot:    true,
		RejectMintable:    true,
		RequireOpenSource: true,
	}, stub, scorer, nil, nil, log)

	riskEngine := risk.New(cfg.Risk, risk.NewMemoryStore(), nil, nil, log)
	riskEngine.SetEquity(*flagEquity)

	btCfg := backtest.Config{
		InitialEquityUSD:  *flagEquity,
		SlippageBps:       *flagSlippage,
		FeeBps:            *flagFee,
		MaxHoldingMinutes: *flagHoldMin,
		StopLossPct:       *flagStopLoss,
		TakeProfitPct:     *flagTakeProf,
		FollowConfirm:     *flagConfirm,
	}
	engine := backtest.New(btCfg, sigEngine, riskEngine, log)
	engine.OnEvent = stub.Observe

	start := time.Now()
	rep, err := engine.Run(context.Background(), src)
	if err != nil {
		fmt.Fprint(os.Stderr, "backtest run: "+err.Error()+lf)
		os.Exit(1)
	}
	if !*flagLargeAll {
		rep.Notes = append(rep.Notes, "画像来自空的内存存储：可加 -treat-all-as-large 兜底，或接入 PG 画像")
	}
	rep.Notes = append(rep.Notes, "回放耗时 "+time.Since(start).Round(time.Millisecond).String())

	printSummary(rep)

	if *flagOut != "" {
		blob, mErr := json.MarshalIndent(rep, "", "  ")
		if mErr != nil {
			fmt.Fprint(os.Stderr, "marshal report: "+mErr.Error()+lf)
			os.Exit(1)
		}
		if wErr := os.WriteFile(*flagOut, blob, 0o644); wErr != nil {
			fmt.Fprint(os.Stderr, "write report: "+wErr.Error()+lf)
			os.Exit(1)
		}
		fmt.Print(lf + "报告已写入：" + *flagOut + lf)
	}
}

// printSummary 打印人类可读的回测摘要。
func printSummary(rep *backtest.Report) {
	m := rep.Metrics
	fmt.Println("=== 回测报告 ===")
	fmt.Printf("事件 %d 条 · 信号 %d 个 · 成交 %d 笔（胜 %d / 负 %d）· 被风控拒绝 %d 次%s",
		m.Events, m.Signals, m.Trades, m.Wins, m.Losses, m.RejectedByRisk, lf)
	fmt.Printf("胜率 %.2f%% · 盈亏比 %.2f · 总盈亏 %+.2f USD（%+.2f%%）%s",
		m.WinRate*100, m.ProfitFactor, m.TotalPnLUSD, m.ReturnPct*100, lf)
	fmt.Printf("最终权益 %.2f USD · 最大回撤 %.2f%% · 手续费 %.2f USD · 平均持仓 %.1f 分钟%s",
		m.FinalEquityUSD, m.MaxDrawdown*100, m.FeesPaidUSD, m.AvgHoldingMin, lf)
	if m.Trades > 0 {
		fmt.Printf("平均盈利 %+.2f USD · 平均亏损 %+.2f USD%s", m.AvgWinUSD, m.AvgLossUSD, lf)
	}
	if len(rep.Trades) > 0 {
		fmt.Println("--- 逐笔成交 ---")
		for _, t := range rep.Trades {
			fmt.Printf("  %s %s 入 %.8g 出 %.8g 盈亏 %+.2f USD (%.2f%%) · %s · %.1f 分钟%s",
				t.Token, t.Source, t.EntryPrice, t.ExitPrice, t.PnLUSD, t.ReturnPct*100, t.ExitReason, t.HoldingMin, lf)
		}
	}
	for _, n := range rep.Notes {
		fmt.Printf("· %s%s", n, lf)
	}
}
