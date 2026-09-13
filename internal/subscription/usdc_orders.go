package subscription

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"meme-bot/internal/payment"
)

// USDCOrderSource 用 PostgreSQL 的 payment_orders 实现 payment.OrderSource。
//
// 分层说明：payment 包负责"链上事实"（转账/确认），订阅逻辑仍留在本包，
// 双方通过 payment.OrderSource 接口解耦。
type USDCOrderSource struct {
	svc  *Service
	pool *pgxpool.Pool
}

// NewUSDCOrderSource 构建订单源。
func NewUSDCOrderSource(svc *Service, pool *pgxpool.Pool) *USDCOrderSource {
	return &USDCOrderSource{svc: svc, pool: pool}
}

// PendingOrders 实现 payment.OrderSource：窗口内的待支付订单。
func (s *USDCOrderSource) PendingOrders(ctx context.Context, since time.Time) ([]payment.PendingOrder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT order_id, user_id, amount, created_at
		  FROM payment_orders
		 WHERE status = 'pending' AND amount > 0 AND created_at >= $1
		 ORDER BY created_at ASC
		 LIMIT 500`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []payment.PendingOrder
	for rows.Next() {
		var item payment.PendingOrder
		if err := rows.Scan(&item.OrderID, &item.UserID, &item.AmountUSD, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// MarkPaid 实现 payment.OrderSource：记录链上凭证并激活订阅（幂等）。
//
// 双重幂等：
//  1. usdc_payments.tx_hash 唯一约束 → 同一链上交易只落库一次；
//  2. Service.HandlePaymentSucceeded 的 order_id 状态迁移 → 重复调用返回 deduped。
func (s *USDCOrderSource) MarkPaid(ctx context.Context, orderID, txHash, payer string) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO usdc_payments (order_id, tx_hash, payer, amount, asset, status)
		SELECT order_id, $2, $3, amount, 'usdc', 'confirmed'
		  FROM payment_orders WHERE order_id = $1
		ON CONFLICT (tx_hash) DO NOTHING`, orderID, txHash, payer); err != nil {
		return err
	}
	_, err := s.svc.HandlePaymentSucceeded(ctx, orderID)
	return err
}

var _ payment.OrderSource = (*USDCOrderSource)(nil)
