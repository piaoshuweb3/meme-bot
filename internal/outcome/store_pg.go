package outcome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoDB 表示数据库不可用（调用方应降级而不是失败）。
var ErrNoDB = errors.New("outcome: 数据库不可用")

// PGStore 是影子跟踪的 PostgreSQL 实现。
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore 构建存储；pool 为 nil 时所有方法返回 ErrNoDB（便于无库环境降级）。
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// Available 数据库是否可用。
func (s *PGStore) Available() bool { return s != nil && s.pool != nil }

// Save 写入决策记录，并为其预建各视野的待采样行（幂等：唯一约束保证重复写入不产生副本）。
func (s *PGStore) Save(ctx context.Context, rec *Record, horizons []Horizon) error {
	if !s.Available() {
		return ErrNoDB
	}
	if rec == nil {
		return errors.New("outcome: nil record")
	}

	var metric []byte
	if len(rec.Metric) > 0 {
		var err error
		metric, err = json.Marshal(rec.Metric)
		if err != nil {
			return fmt.Errorf("outcome: 序列化 metric 失败: %w", err)
		}
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO signal_outcomes
		    (chain, token, decision, reason, baseline_at, baseline_price, sampling, strategy_version, metric)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (chain, token, decision, baseline_at)
		DO UPDATE SET metric = EXCLUDED.metric, reason = EXCLUDED.reason
		RETURNING id`,
		rec.Chain, rec.Token, string(rec.Decision), rec.Reason,
		rec.BaselineAt, rec.BaselinePrice, rec.Sampling, rec.Version, metric,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("outcome: 写入记录失败: %w", err)
	}
	rec.ID = id

	for _, h := range horizons {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO signal_outcome_samples (outcome_id, horizon, target_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (outcome_id, horizon) DO NOTHING`,
			id, h.Key, rec.BaselineAt.Add(h.Duration),
		); err != nil {
			return fmt.Errorf("outcome: 预建采样行(%s) 失败: %w", h.Key, err)
		}
	}
	return nil
}

// Pending 一条到期待采样项（含计算收益所需的最小上下文）。
type Pending struct {
	OutcomeID     int64
	Chain         string
	Token         string
	Horizon       string
	TargetAt      time.Time
	BaselinePrice float64
	Attempts      int
}

// DueSamples 返回当前到期且尚未成功采样的待办。
//
// 过滤条件与 outcome.Due 的语义保持一致：已采样跳过、退避期内跳过、未到期跳过；
// 排序让"尝试次数少的"优先，避免个别失败项长期占用配额。
func (s *PGStore) DueSamples(ctx context.Context, now time.Time, grace time.Duration, limit int) ([]Pending, error) {
	if !s.Available() {
		return nil, ErrNoDB
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.chain, o.token, s.horizon, s.target_at, o.baseline_price, s.attempts
		  FROM signal_outcome_samples s
		  JOIN signal_outcomes o ON o.id = s.outcome_id
		 WHERE s.sampled_at IS NULL
		   AND o.baseline_price > 0
		   AND s.target_at <= $1
		   AND (s.next_at IS NULL OR s.next_at <= $1)
		 ORDER BY s.attempts ASC, s.target_at ASC
		 LIMIT $2`,
		now.Add(-grace), limit)
	if err != nil {
		return nil, fmt.Errorf("outcome: 查询待采样失败: %w", err)
	}
	defer rows.Close()

	var out []Pending
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.OutcomeID, &p.Chain, &p.Token, &p.Horizon, &p.TargetAt, &p.BaselinePrice, &p.Attempts); err != nil {
			return nil, fmt.Errorf("outcome: 解析待采样行失败: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkSampled 记录一次成功采样。
func (s *PGStore) MarkSampled(ctx context.Context, outcomeID int64, horizon string, price, ret float64, at time.Time) error {
	if !s.Available() {
		return ErrNoDB
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE signal_outcome_samples
		   SET sampled_at = $3, price = $4, ret = $5, error_code = NULL, next_at = NULL
		 WHERE outcome_id = $1 AND horizon = $2`,
		outcomeID, horizon, at.UTC(), price, ret)
	if err != nil {
		return fmt.Errorf("outcome: 记录采样失败: %w", err)
	}
	return nil
}

// MarkFailed 记录一次采样失败并写入退避时间（下次由 next_at 控制）。
func (s *PGStore) MarkFailed(ctx context.Context, outcomeID int64, horizon, code string, attempts int, nextAt time.Time) error {
	if !s.Available() {
		return ErrNoDB
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE signal_outcome_samples
		   SET attempts = $3, error_code = $4, next_at = $5
		 WHERE outcome_id = $1 AND horizon = $2`,
		outcomeID, horizon, attempts, code, nextAt.UTC())
	if err != nil {
		return fmt.Errorf("outcome: 记录重试失败: %w", err)
	}
	return nil
}

// ListSince 读取窗口内的记录（含已采集样本），供覆盖率统计使用。
//
// 说明：这里刻意只取"决策分层 + 基准 + 样本"，不取指标明细，
// 以免统计接口把大量 JSONB 从库里拉到内存。
func (s *PGStore) ListSince(ctx context.Context, since time.Time, limit int) ([]*Record, error) {
	if !s.Available() {
		return nil, ErrNoDB
	}
	if limit <= 0 {
		limit = 5000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.chain, o.token, o.decision, o.baseline_at, o.baseline_price, o.sampling, o.strategy_version,
		       s.horizon, s.target_at, s.sampled_at, s.price, s.ret, s.attempts, s.error_code, s.next_at
		  FROM signal_outcomes o
		  LEFT JOIN signal_outcome_samples s ON s.outcome_id = o.id
		 WHERE o.baseline_at >= $1
		 ORDER BY o.baseline_at DESC
		 LIMIT $2`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("outcome: 查询记录失败: %w", err)
	}
	defer rows.Close()

	byID := make(map[int64]*Record)
	var order []int64
	for rows.Next() {
		var (
			id                                        int64
			chain, token, decision, sampling, version string
			baselineAt                                time.Time
			baselinePrice                             float64
			horizon, errorCode                        *string
			targetAt, sampledAt, nextAt               *time.Time
			price, ret                                *float64
			attempts                                  *int
		)
		if err := rows.Scan(&id, &chain, &token, &decision, &baselineAt, &baselinePrice, &sampling, &version,
			&horizon, &targetAt, &sampledAt, &price, &ret, &attempts, &errorCode, &nextAt); err != nil {
			return nil, fmt.Errorf("outcome: 解析记录行失败: %w", err)
		}

		rec, ok := byID[id]
		if !ok {
			rec = &Record{
				ID: id, Chain: chain, Token: token, Decision: Decision(decision),
				BaselineAt: baselineAt, BaselinePrice: baselinePrice, Sampling: sampling, Version: version,
				Samples: make(map[string]Sample), Retries: make(map[string]Retry),
			}
			byID[id] = rec
			order = append(order, id)
		}

		if horizon == nil {
			continue
		}
		if sampledAt != nil && price != nil {
			r := 0.0
			if ret != nil {
				r = *ret
			}
			smp := Sample{Horizon: *horizon, Price: *price, Ret: r, SampledAt: sampledAt.UTC()}
			if targetAt != nil {
				smp.TargetAt = *targetAt
			}
			rec.Samples[*horizon] = smp
			continue
		}
		if attempts != nil && *attempts > 0 {
			code := ""
			if errorCode != nil {
				code = *errorCode
			}
			next := time.Time{}
			if nextAt != nil {
				next = nextAt.UTC()
			}
			rec.Retries[*horizon] = Retry{Attempts: *attempts, Code: code, NextAt: next}
		}
	}

	out := make([]*Record, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, rows.Err()
}
