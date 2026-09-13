// Package subscription 实现套餐、订阅与支付回调（含幂等与返佣触发）。
package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"meme-bot/internal/affiliate"
	"meme-bot/internal/metrics"
)

// Plan 套餐。
type Plan struct {
	ID            int            `json:"id"`
	Name          string         `json:"name"`
	PriceMonthly  float64        `json:"price_monthly"`
	MaxSignalsDay int            `json:"max_signals_day"`
	MaxAPICalls   int            `json:"max_api_calls"`
	Features      map[string]any `json:"features"`
	IsActive      bool           `json:"is_active"`
}

// Subscription 订阅。
type Subscription struct {
	ID               int64      `json:"id"`
	UserID           int64      `json:"user_id"`
	PlanID           *int       `json:"plan_id,omitempty"`
	PlanName         string     `json:"plan_name,omitempty"`
	Status           string     `json:"status"`
	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
}

// PaymentOrder 支付订单。
type PaymentOrder struct {
	ID        int64     `json:"id"`
	OrderID   string    `json:"order_id"`
	UserID    int64     `json:"user_id"`
	PlanID    int       `json:"plan_id"`
	Amount    float64   `json:"amount"`
	Currency  string    `json:"currency"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Service 订阅服务。
type Service struct {
	pool       *pgxpool.Pool
	affiliate  *affiliate.Service
	metrics    *metrics.Registry
	periodDays int
}

// NewService 构建订阅服务。
func NewService(pool *pgxpool.Pool, aff *affiliate.Service, reg *metrics.Registry) *Service {
	return &Service{pool: pool, affiliate: aff, metrics: reg, periodDays: 30}
}

// Plans 返回在售套餐。
func (s *Service) Plans(ctx context.Context) ([]Plan, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, price_monthly, max_signals_day, max_api_calls, features, is_active
		  FROM plans WHERE is_active = TRUE ORDER BY price_monthly ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Plan
	for rows.Next() {
		var p Plan
		var features []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.PriceMonthly, &p.MaxSignalsDay, &p.MaxAPICalls, &features, &p.IsActive); err != nil {
			return nil, err
		}
		p.Features = map[string]any{}
		if len(features) > 0 {
			_ = jsonUnmarshal(features, &p.Features)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreatePayment 创建支付订单（真实收款由外部支付渠道/链上 USDC 完成）。
func (s *Service) CreatePayment(ctx context.Context, userID int64, planID int, currency string) (*PaymentOrder, error) {
	if currency == "" {
		currency = "USD"
	}
	var price float64
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT price_monthly, is_active FROM plans WHERE id = $1`, planID).Scan(&price, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("subscription: 套餐不存在")
	}
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, errors.New("subscription: 套餐已下架")
	}

	orderID := fmt.Sprintf("ord_%d_%d_%d", userID, planID, time.Now().UTC().UnixNano())
	var o PaymentOrder
	err = s.pool.QueryRow(ctx, `
		INSERT INTO payment_orders (order_id, user_id, plan_id, amount, currency, status)
		VALUES ($1,$2,$3,$4,$5,'pending')
		RETURNING id, order_id, user_id, plan_id, amount, currency, status, created_at`,
		orderID, userID, planID, price, strings.ToUpper(currency),
	).Scan(&o.ID, &o.OrderID, &o.UserID, &o.PlanID, &o.Amount, &o.Currency, &o.Status, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// PaymentResult 支付处理结果。
type PaymentResult struct {
	OrderID      string    `json:"order_id"`
	UserID       int64     `json:"user_id"`
	PlanID       int       `json:"plan_id"`
	Deduped      bool      `json:"deduped"` // 是否重复回调（幂等命中）
	PeriodEnd    time.Time `json:"period_end"`
	Commissioned bool      `json:"commissioned"`
}

// HandlePaymentSucceeded 处理支付成功回调。
//
// 幂等保证：先以 order_id 做状态迁移（pending → succeeded），
// 只有迁移成功的首次调用才会激活订阅与触发返佣。
func (s *Service) HandlePaymentSucceeded(ctx context.Context, orderID string) (*PaymentResult, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, errors.New("subscription: order_id 不能为空")
	}

	var (
		userID int64
		planID int
		amount float64
		status string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, plan_id, amount, status FROM payment_orders WHERE order_id = $1`, orderID).
		Scan(&userID, &planID, &amount, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("subscription: 订单不存在")
	}
	if err != nil {
		return nil, err
	}
	if status == "succeeded" {
		// 已处理过：返回当前订阅状态，避免重复计费/重复返佣
		sub, _ := s.Status(ctx, userID)
		res := &PaymentResult{OrderID: orderID, UserID: userID, PlanID: planID, Deduped: true}
		if sub != nil && sub.CurrentPeriodEnd != nil {
			res.PeriodEnd = *sub.CurrentPeriodEnd
		}
		return res, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE payment_orders SET status = 'succeeded', updated_at = NOW() WHERE order_id = $1 AND status = 'pending'`,
		orderID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		// 并发回调：另一个请求已抢先处理
		sub, _ := s.Status(ctx, userID)
		res := &PaymentResult{OrderID: orderID, UserID: userID, PlanID: planID, Deduped: true}
		if sub != nil && sub.CurrentPeriodEnd != nil {
			res.PeriodEnd = *sub.CurrentPeriodEnd
		}
		return res, nil
	}

	periodEnd := time.Now().UTC().AddDate(0, 0, s.periodDays)
	_, err = tx.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, status, current_period_end)
		VALUES ($1,$2,'active',$3)
		ON CONFLICT (user_id) DO UPDATE SET
			plan_id = EXCLUDED.plan_id,
			status = 'active',
			current_period_end = EXCLUDED.current_period_end,
			updated_at = NOW()`,
		userID, planID, periodEnd)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	res := &PaymentResult{OrderID: orderID, UserID: userID, PlanID: planID, PeriodEnd: periodEnd}

	// 返佣：失败不阻断订阅生效，但需记录
	if s.affiliate != nil {
		if err := s.affiliate.CreateCommission(ctx, userID, amount, orderID); err != nil {
			return res, fmt.Errorf("subscription: 订阅已生效但返佣写入失败: %w", err)
		}
		res.Commissioned = true
	}
	if s.metrics != nil {
		s.metrics.Inc("memebot_subscriptions_activated_total", 1)
	}
	return res, nil
}

// Status 查询用户订阅状态（未订阅时返回 inactive）。
func (s *Service) Status(ctx context.Context, userID int64) (*Subscription, error) {
	var (
		sub      Subscription
		planID   *int
		planName *string
		period   *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.plan_id, p.name, s.status, s.current_period_end
		  FROM subscriptions s LEFT JOIN plans p ON p.id = s.plan_id
		 WHERE s.user_id = $1`, userID).
		Scan(&sub.ID, &sub.UserID, &planID, &planName, &sub.Status, &period)
	if errors.Is(err, pgx.ErrNoRows) {
		return &Subscription{UserID: userID, Status: "inactive"}, nil
	}
	if err != nil {
		return nil, err
	}
	sub.PlanID = planID
	if planName != nil {
		sub.PlanName = *planName
	}
	sub.CurrentPeriodEnd = period
	if sub.Status == "active" && period != nil && time.Now().UTC().After(*period) {
		sub.Status = "expired"
	}
	return &sub, nil
}

// PlanFor 返回某用户当前生效套餐（无订阅返回 nil）。
func (s *Service) PlanFor(ctx context.Context, userID int64) (*Plan, error) {
	sub, err := s.Status(ctx, userID)
	if err != nil {
		return nil, err
	}
	if sub.Status != "active" || sub.PlanID == nil {
		return nil, nil
	}
	var p Plan
	var features []byte
	err = s.pool.QueryRow(ctx, `
		SELECT id, name, price_monthly, max_signals_day, max_api_calls, features, is_active
		  FROM plans WHERE id = $1`, *sub.PlanID).
		Scan(&p.ID, &p.Name, &p.PriceMonthly, &p.MaxSignalsDay, &p.MaxAPICalls, &features, &p.IsActive)
	if err != nil {
		return nil, err
	}
	p.Features = map[string]any{}
	_ = jsonUnmarshal(features, &p.Features)
	return &p, nil
}

// ConsumeAPICalls 记录外部 API 调用次数，并检查配额（按天计数）。
func (s *Service) ConsumeAPICalls(ctx context.Context, userID int64, calls int) (remaining int, err error) {
	plan, err := s.PlanFor(ctx, userID)
	if err != nil {
		return 0, err
	}
	if plan == nil {
		return 0, errors.New("subscription: 无有效订阅，无法调用外部 API")
	}

	var used int
	err = s.pool.QueryRow(ctx, `
		INSERT INTO api_usage (user_id, day, calls) VALUES ($1, CURRENT_DATE, $2)
		ON CONFLICT (user_id, day) DO UPDATE SET calls = api_usage.calls + EXCLUDED.calls
		RETURNING calls`, userID, calls).Scan(&used)
	if err != nil {
		return 0, err
	}
	if used > plan.MaxAPICalls {
		return 0, fmt.Errorf("subscription: 已超出套餐 API 配额（%d/%d）", used, plan.MaxAPICalls)
	}
	return plan.MaxAPICalls - used, nil
}
