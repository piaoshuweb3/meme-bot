package address

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"meme-bot/internal/model"
)

// PGStore 是 model.ProfileStore 的 PostgreSQL 实现。
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore 构建 PostgreSQL 画像存储。
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

var _ model.ProfileStore = (*PGStore)(nil)

// Get 实现 model.ProfileStore。
func (s *PGStore) Get(ctx context.Context, chain, address string) (*model.AddressProfile, error) {
	const q = `
		SELECT address, chain, win_rate, profit_factor, max_drawdown, avg_hold_seconds,
		       consistency, size_consistency, median_buy_usd, total_trades, winning_trades,
		       recent_score, tags, is_blacklisted, COALESCE(blacklist_reason,''),
		       COALESCE(last_active, to_timestamp(0)), updated_at
		  FROM address_profiles
		 WHERE chain = $1 AND address = $2`
	var p model.AddressProfile
	var tags []string
	err := s.pool.QueryRow(ctx, q, strings.ToLower(chain), strings.ToLower(address)).Scan(
		&p.Address, &p.Chain, &p.WinRate, &p.ProfitFactor, &p.MaxDrawdown, &p.AvgHoldSeconds,
		&p.Consistency, &p.SizeConsistency, &p.MedianBuyUSD, &p.TotalTrades, &p.WinningTrades,
		&p.RecentScore, &tags, &p.IsBlacklisted, &p.BlacklistReason, &p.LastActive, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, t := range tags {
		p.Tags = append(p.Tags, model.AddressTag(t))
	}
	return &p, nil
}

// Upsert 实现 model.ProfileStore。
func (s *PGStore) Upsert(ctx context.Context, p *model.AddressProfile) error {
	if p == nil {
		return errors.New("address: nil profile")
	}
	tags := make([]string, 0, len(p.Tags))
	for _, t := range p.Tags {
		tags = append(tags, string(t))
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	const q = `
		INSERT INTO address_profiles (
			address, chain, win_rate, profit_factor, max_drawdown, avg_hold_seconds,
			consistency, size_consistency, median_buy_usd, total_trades, winning_trades,
			recent_score, tags, is_blacklisted, blacklist_reason, last_active, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (address, chain) DO UPDATE SET
			win_rate = EXCLUDED.win_rate,
			profit_factor = EXCLUDED.profit_factor,
			max_drawdown = EXCLUDED.max_drawdown,
			avg_hold_seconds = EXCLUDED.avg_hold_seconds,
			consistency = EXCLUDED.consistency,
			size_consistency = EXCLUDED.size_consistency,
			median_buy_usd = EXCLUDED.median_buy_usd,
			total_trades = EXCLUDED.total_trades,
			winning_trades = EXCLUDED.winning_trades,
			recent_score = EXCLUDED.recent_score,
			tags = EXCLUDED.tags,
			is_blacklisted = EXCLUDED.is_blacklisted,
			blacklist_reason = EXCLUDED.blacklist_reason,
			last_active = EXCLUDED.last_active,
			updated_at = EXCLUDED.updated_at`
	_, err := s.pool.Exec(ctx, q,
		strings.ToLower(p.Address), strings.ToLower(p.Chain), p.WinRate, p.ProfitFactor,
		p.MaxDrawdown, p.AvgHoldSeconds, p.Consistency, p.SizeConsistency, p.MedianBuyUSD,
		p.TotalTrades, p.WinningTrades, p.RecentScore, tags, p.IsBlacklisted,
		nullIfEmpty(p.BlacklistReason), nullTime(p.LastActive), p.UpdatedAt,
	)
	return err
}

// ListTop 实现 model.ProfileStore。
func (s *PGStore) ListTop(ctx context.Context, chain string, limit int) ([]*model.AddressProfile, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `
		SELECT address, chain, win_rate, profit_factor, max_drawdown, avg_hold_seconds,
		       consistency, size_consistency, median_buy_usd, total_trades, winning_trades,
		       recent_score, tags, is_blacklisted, COALESCE(blacklist_reason,''),
		       COALESCE(last_active, to_timestamp(0)), updated_at
		  FROM address_profiles
		 WHERE is_blacklisted = FALSE`
	args := []any{}
	if chain != "" {
		q += ` AND chain = $1`
		args = append(args, strings.ToLower(chain))
	}
	q += ` ORDER BY recent_score DESC LIMIT ` + itoa(limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.AddressProfile
	for rows.Next() {
		var p model.AddressProfile
		var tags []string
		if err := rows.Scan(
			&p.Address, &p.Chain, &p.WinRate, &p.ProfitFactor, &p.MaxDrawdown, &p.AvgHoldSeconds,
			&p.Consistency, &p.SizeConsistency, &p.MedianBuyUSD, &p.TotalTrades, &p.WinningTrades,
			&p.RecentScore, &tags, &p.IsBlacklisted, &p.BlacklistReason, &p.LastActive, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		for _, t := range tags {
			p.Tags = append(p.Tags, model.AddressTag(t))
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// IsBlacklisted 实现 model.ProfileStore。
func (s *PGStore) IsBlacklisted(ctx context.Context, chain, address string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM address_blacklist WHERE chain = $1 AND address = $2)`,
		strings.ToLower(chain), strings.ToLower(address)).Scan(&exists)
	return exists, err
}

// Blacklist 实现 model.ProfileStore。
func (s *PGStore) Blacklist(ctx context.Context, chain, address, reason string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO address_blacklist (chain, address, reason) VALUES ($1,$2,$3)
		 ON CONFLICT (chain, address) DO UPDATE SET reason = EXCLUDED.reason`,
		strings.ToLower(chain), strings.ToLower(address), reason)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE address_profiles SET is_blacklisted = TRUE, blacklist_reason = $3, updated_at = NOW()
		  WHERE chain = $1 AND address = $2`,
		strings.ToLower(chain), strings.ToLower(address), reason)
	return err
}

// RecordTrade 实现 model.ProfileStore（幂等：同 tx+address+token+side 只记一次）。
func (s *PGStore) RecordTrade(ctx context.Context, rec *model.TradeRecord) error {
	if rec == nil {
		return errors.New("address: nil trade record")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trade_records (chain, address, token, side, amount_usd, price_usd, pnl_usd, tx_hash, ts)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (chain, tx_hash, address, token, side) DO NOTHING`,
		strings.ToLower(rec.Chain), strings.ToLower(rec.Address), strings.ToLower(rec.Token),
		strings.ToLower(string(rec.Side)), rec.AmountUSD, rec.PriceUSD, rec.PnLUSD,
		rec.TxHash, rec.At)
	return err
}

// RecentTrades 实现 model.ProfileStore。
func (s *PGStore) RecentTrades(ctx context.Context, chain, address string, since time.Time, limit int) ([]*model.TradeRecord, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT chain, address, token, side, amount_usd, price_usd, pnl_usd, tx_hash, ts
		  FROM trade_records
		 WHERE chain = $1 AND address = $2 AND ts >= $3
		 ORDER BY ts ASC
		 LIMIT `+itoa(limit),
		strings.ToLower(chain), strings.ToLower(address), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.TradeRecord
	for rows.Next() {
		var t model.TradeRecord
		var side string
		if err := rows.Scan(&t.Chain, &t.Address, &t.Token, &side, &t.AmountUSD, &t.PriceUSD, &t.PnLUSD, &t.TxHash, &t.At); err != nil {
			return nil, err
		}
		t.Side = model.Side(side)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// ---- 小工具 ----

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
