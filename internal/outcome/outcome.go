// Package outcome 实现「影子表现跟踪」：对被过滤 / 放行 / 成交的候选做抽样，
// 跟踪其后续多个时间视野的表现，用于验证**过滤器本身**是否有效。
//
// 为什么需要它：回测只能回答"策略在历史数据上如何"，无法回答
// "我今天的拒绝是否是错拒"。没有对照样本，过滤器只会越调越自信而无法证伪。
//
// 设计要点（借鉴 nhovongoc0-max/meme-radar 的 outcomes）：
//   - 对被过滤样本做**稳定抽样**（只依赖 chain:token 的哈希，与后续涨跌无关）
//     → 避免"只跟踪表现好的样本"这种选择性偏差；
//   - 只有在样本量达标后才允许据其调参（见 MinSamplesForCalibration）。
package outcome

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"time"
)

// Horizon 采样视野。
type Horizon struct {
	Key      string
	Duration time.Duration
}

// DefaultHorizons 七个视野（与 meme-radar 对齐，便于横向比较）。
var DefaultHorizons = []Horizon{
	{Key: "m5", Duration: 5 * time.Minute},
	{Key: "m15", Duration: 15 * time.Minute},
	{Key: "m30", Duration: 30 * time.Minute},
	{Key: "h1", Duration: time.Hour},
	{Key: "h2", Duration: 2 * time.Hour},
	{Key: "h6", Duration: 6 * time.Hour},
	{Key: "h24", Duration: 24 * time.Hour},
}

// Decision 决策分层。
type Decision string

const (
	// DecisionFiltered 被漏斗拦截（对照组的核心来源）。
	DecisionFiltered Decision = "FILTERED"
	// DecisionSignaled 生成信号但未成交。
	DecisionSignaled Decision = "SIGNALED"
	// DecisionExecuted 实际成交。
	DecisionExecuted Decision = "EXECUTED"
)

// 抽样方式。
const (
	SamplingFull    = "FULL"      // 全量跟踪
	SamplingHashMod = "HASH_MOD5" // 稳定 1/5 抽样
)

// MinSamplesForCalibration 样本量阈值：低于此值不得据该分层调参。
// 这是纪律而非建议——用不足样本调参等于拟合噪声。
const MinSamplesForCalibration = 50

// DefaultSampleGrace 到期后再等一段时间才采样（给数据源一点稳定时间）。
const DefaultSampleGrace = 60 * time.Second

// MaxRetryBackoff 重试退避上限。
const MaxRetryBackoff = time.Hour

// ShouldSample 以 SHA256(chain:token) 做稳定抽样。
//
// 关键性质：结果只取决于地址本身，与后续涨跌、热度、出现顺序**无关**
// → 不会引入选择性偏差（这是该机制可信的前提）。
func ShouldSample(chain, token string, mod uint16) bool {
	if mod <= 1 {
		return true
	}
	sum := sha256.Sum256([]byte(chain + ":" + token))
	return binary.BigEndian.Uint16(sum[:2])%mod == 0
}

// SamplingFor 返回某决策应采用的抽样方式：
// 被过滤的候选数量最大，用稳定抽样控制存储；信号与成交全量跟踪。
func SamplingFor(d Decision) string {
	if d == DecisionFiltered {
		return SamplingHashMod
	}
	return SamplingFull
}

// Sample 单次采样结果。
type Sample struct {
	Horizon   string
	TargetAt  time.Time
	Price     float64
	Ret       float64 // price/baseline - 1
	SampledAt time.Time
}

// Retry 采样失败的重试状态。
type Retry struct {
	Attempts int
	Code     string
	NextAt   time.Time
}

// Record 一条待跟踪记录。
type Record struct {
	ID            int64
	Chain         string
	Token         string
	Decision      Decision
	Reason        string
	BaselineAt    time.Time
	BaselinePrice float64
	Sampling      string
	Version       string
	Metric        map[string]float64
	Samples       map[string]Sample
	Retries       map[string]Retry
}

// NewRecord 构造记录（自动决定抽样方式；被过滤样本若未命中抽样则不跟踪）。
//
// 基准价缺失（<=0）时放弃跟踪：没有基准就无法计算收益，任何"补一个价"的做法都是臆造。
func NewRecord(chain, token string, d Decision, reason string, at time.Time,
	price float64, version string, metric map[string]float64) (*Record, bool) {
	if price <= 0 {
		return nil, false
	}
	sampling := SamplingFor(d)
	if sampling == SamplingHashMod && !ShouldSample(chain, token, 5) {
		return nil, false
	}
	return &Record{
		Chain: chain, Token: token, Decision: d, Reason: reason,
		BaselineAt: at.UTC(), BaselinePrice: price, Sampling: sampling,
		Version: version, Metric: metric,
		Samples: make(map[string]Sample), Retries: make(map[string]Retry),
	}, true
}

// Due 返回当前到期且尚未成功采样的视野（考虑 grace 与重试退避）。
//
// 排序：先按尝试次数（少的优先，避免老任务一直卡住新任务），再按目标时间。
func (r *Record) Due(horizons []Horizon, now time.Time, grace time.Duration) []Horizon {
	if r == nil {
		return nil
	}
	out := make([]Horizon, 0, len(horizons))
	for _, h := range horizons {
		if _, done := r.Samples[h.Key]; done {
			continue
		}
		if rt, ok := r.Retries[h.Key]; ok && now.Before(rt.NextAt) {
			continue
		}
		if now.Before(r.BaselineAt.Add(h.Duration).Add(grace)) {
			continue
		}
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := r.Retries[out[i].Key], r.Retries[out[j].Key]
		if ai.Attempts != aj.Attempts {
			return ai.Attempts < aj.Attempts
		}
		return r.BaselineAt.Add(out[i].Duration).Before(r.BaselineAt.Add(out[j].Duration))
	})
	return out
}

// SetSample 记录一次成功采样（price <= 0 视为失败）。
func (r *Record) SetSample(h Horizon, targetAt, sampledAt time.Time, price float64) bool {
	if r == nil || price <= 0 || r.BaselinePrice <= 0 {
		return false
	}
	r.Samples[h.Key] = Sample{
		Horizon:   h.Key,
		TargetAt:  targetAt,
		Price:     price,
		Ret:       price/r.BaselinePrice - 1,
		SampledAt: sampledAt.UTC(),
	}
	delete(r.Retries, h.Key)
	return true
}

// SetFailure 记录一次采样失败并安排退避重试。
func (r *Record) SetFailure(h Horizon, code string, now time.Time) {
	if r == nil {
		return
	}
	rt := r.Retries[h.Key]
	rt.Attempts++
	rt.Code = code
	rt.NextAt = NextRetry(rt.Attempts, now)
	r.Retries[h.Key] = rt
}

// NextRetry 指数退避（2 分钟起逐次翻倍，上限 1 小时）。
func NextRetry(attempts int, now time.Time) time.Time {
	if attempts < 1 {
		attempts = 1
	}
	d := 2 * time.Minute << uint(minInt(attempts-1, 5))
	if d > MaxRetryBackoff {
		d = MaxRetryBackoff
	}
	return now.Add(d)
}

// Stat 单个 (决策, 视野) 的覆盖与表现统计。
type Stat struct {
	Decision     Decision
	Horizon      string
	Eligible     int      // 到期应采样数
	Completed    int      // 已成功采样数
	Missing      int      // 缺失数
	Median       *float64 // 收益中位数（无样本为 nil，不臆造 0）
	PositiveRate *float64 // 正收益率
	Calibratable bool     // 样本是否足够据此调参
}

// Coverage 按决策分层汇总覆盖与表现。
//
// 语义说明：Median/PositiveRate 在无样本时返回 nil——缺失就是缺失，
// 返回 0 会被读成"收益为 0"，属于从模糊数据里制造精度。
func Coverage(records []*Record, horizons []Horizon, now time.Time) map[Decision][]Stat {
	byDecision := make(map[Decision][]*Record)
	for _, r := range records {
		if r == nil {
			continue
		}
		byDecision[r.Decision] = append(byDecision[r.Decision], r)
	}

	result := make(map[Decision][]Stat, len(byDecision))
	for d, list := range byDecision {
		stats := make([]Stat, 0, len(horizons))
		for _, h := range horizons {
			st := Stat{Decision: d, Horizon: h.Key}
			rets := make([]float64, 0, len(list))
			for _, r := range list {
				if now.Before(r.BaselineAt.Add(h.Duration)) {
					continue // 尚未到期，不计入分母（否则覆盖率会被稀释）
				}
				st.Eligible++
				s, ok := r.Samples[h.Key]
				if !ok {
					st.Missing++
					continue
				}
				st.Completed++
				if !math.IsNaN(s.Ret) && !math.IsInf(s.Ret, 0) {
					rets = append(rets, s.Ret)
				}
			}
			if len(rets) > 0 {
				sort.Float64s(rets)
				mid := median(rets)
				pos := 0
				for _, v := range rets {
					if v > 0 {
						pos++
					}
				}
				rate := float64(pos) / float64(len(rets))
				st.Median = &mid
				st.PositiveRate = &rate
			}
			st.Calibratable = st.Completed >= MinSamplesForCalibration
			stats = append(stats, st)
		}
		result[d] = stats
	}
	return result
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
