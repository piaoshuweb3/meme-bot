// Package affiliate 实现直推返佣（默认 20%）。
package affiliate

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service 返佣服务。
type Service struct {
	pool *pgxpool.Pool
	rate float64
}

// NewService 构建返佣服务（rate 为直推比例，0.20 = 20%）。
func NewService(pool *pgxpool.Pool, rate float64) *Service {
	if rate <= 0 || rate > 1 {
		rate = 0.20
	}
	return &Service{pool: pool, rate: rate}
}

// Rate 返回当前返佣比例。
func (s *Service) Rate() float64 { return s.rate }

// CreateCommission 在用户付费成功后写入返佣记录（幂等：同一 order+收款人只记一次）。
func (s *Service) CreateCommission(ctx context.Context, fromUserID int64, amount float64, orderID string) error {
	if amount <= 0 {
		return errors.New("affiliate: 金额必须为正")
	}
	var referrer *int64
	err := s.pool.QueryRow(ctx, `SELECT referred_by FROM users WHERE id = $1`, fromUserID).Scan(&referrer)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if referrer == nil {
		return nil // 无推荐人，正常情况
	}

	commission := amount * s.rate
	_, err = s.pool.Exec(ctx, `
		INSERT INTO commissions (from_user_id, to_user_id, amount, rate, order_id, status)
		VALUES ($1,$2,$3,$4,$5,'pending')
		ON CONFLICT (order_id, to_user_id) DO NOTHING`,
		fromUserID, *referrer, commission, s.rate, orderID)
	return err
}

// BindReferral 绑定推荐关系（用户注册后仍可补绑，且不能绑自己）。
func (s *Service) BindReferral(ctx context.Context, userID int64, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("affiliate: 推荐码不能为空")
	}
	var referrerID int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE referral_code = $1`, code).Scan(&referrerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("affiliate: 推荐码无效")
	}
	if err != nil {
		return err
	}
	if referrerID == userID {
		return errors.New("affiliate: 不能绑定自己的推荐码")
	}

	var existing *int64
	if err := s.pool.QueryRow(ctx, `SELECT referred_by FROM users WHERE id = $1`, userID).Scan(&existing); err != nil {
		return err
	}
	if existing != nil {
		return errors.New("affiliate: 已绑定推荐人，不可更改")
	}
	_, err = s.pool.Exec(ctx, `UPDATE users SET referred_by = $2, updated_at = NOW() WHERE id = $1`, userID, referrerID)
	return err
}

// Stats 返佣统计。
type Stats struct {
	PendingCount  int     `json:"pending_count"`
	SettledCount  int     `json:"settled_count"`
	PendingAmount float64 `json:"pending_amount"`
	SettledAmount float64 `json:"settled_amount"`
	InvitedUsers  int     `json:"invited_users"`
}

// Stats 查询某用户的返佣统计。
func (s *Service) Stats(ctx context.Context, userID int64) (*Stats, error) {
	out := &Stats{}
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'settled'),
			COALESCE(SUM(amount) FILTER (WHERE status = 'pending'), 0),
			COALESCE(SUM(amount) FILTER (WHERE status = 'settled'), 0)
		  FROM commissions WHERE to_user_id = $1`, userID).
		Scan(&out.PendingCount, &out.SettledCount, &out.PendingAmount, &out.SettledAmount)
	if err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE referred_by = $1`, userID).Scan(&out.InvitedUsers); err != nil {
		return nil, err
	}
	return out, nil
}

// List 最近返佣明细。
func (s *Service) List(ctx context.Context, userID int64, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, from_user_id, amount, rate, COALESCE(order_id,''), status, created_at
		  FROM commissions WHERE to_user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var (
			id       int64
			fromID   int64
			amount   float64
			rate     float64
			orderID  string
			status   string
			createAt any
		)
		if err := rows.Scan(&id, &fromID, &amount, &rate, &orderID, &status, &createAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "from_user_id": fromID, "amount": amount, "rate": rate,
			"order_id": orderID, "status": status, "created_at": createAt,
		})
	}
	return out, rows.Err()
}

// Settle 结算返佣（管理端）。
func (s *Service) Settle(ctx context.Context, commissionID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE commissions SET status = 'settled' WHERE id = $1`, commissionID)
	return err
}
