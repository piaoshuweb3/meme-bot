package strategy

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"meme-bot/internal/model"
)

// ---------------------------------------------------------------------------
// 内存实现（本地开发 / 单元测试）
// ---------------------------------------------------------------------------

// MemoryPositions 是 model.PositionStore 的内存实现。
type MemoryPositions struct {
	mu   sync.RWMutex
	data map[string]*model.Position
}

// NewMemoryPositions 构建内存持仓存储。
func NewMemoryPositions() *MemoryPositions {
	return &MemoryPositions{data: make(map[string]*model.Position)}
}

// Open 实现 model.PositionStore。
func (m *MemoryPositions) Open(p *model.Position) error {
	if p == nil {
		return errors.New("positions: nil position")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.ID == "" {
		p.ID = "pos-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	cp := *p
	m.data[p.ID] = &cp
	return nil
}

// Update 实现 model.PositionStore。
func (m *MemoryPositions) Update(p *model.Position) error {
	if p == nil {
		return errors.New("positions: nil position")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *p
	m.data[p.ID] = &cp
	return nil
}

// Get 实现 model.PositionStore。
func (m *MemoryPositions) Get(id string) (*model.Position, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.data[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

// ListOpen 实现 model.PositionStore。
func (m *MemoryPositions) ListOpen(chain string) ([]*model.Position, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.Position, 0, len(m.data))
	for _, p := range m.data {
		if p.Status != model.PositionOpen {
			continue
		}
		if chain != "" && !strings.EqualFold(p.Chain, chain) {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	return out, nil
}

// Close 实现 model.PositionStore。
func (m *MemoryPositions) Close(id string, exitPriceUSD float64, realizedPnLUSD float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.data[id]
	if !ok {
		return nil
	}
	p.Status = model.PositionClosed
	p.ExitPriceUSD = exitPriceUSD
	p.RealizedPnLUSD = realizedPnLUSD
	p.ClosedAt = time.Now().UTC()
	p.UpdatedAt = p.ClosedAt
	return nil
}

var _ model.PositionStore = (*MemoryPositions)(nil)

// ---------------------------------------------------------------------------
// PostgreSQL 实现
// ---------------------------------------------------------------------------

// PGPositions 是 model.PositionStore 的 PostgreSQL 实现。
type PGPositions struct {
	pool *pgxpool.Pool
}

// NewPGPositions 构建 PostgreSQL 持仓存储。
func NewPGPositions(pool *pgxpool.Pool) *PGPositions { return &PGPositions{pool: pool} }

// Open 实现 model.PositionStore。
func (p *PGPositions) Open(pos *model.Position) error {
	if pos == nil {
		return errors.New("positions: nil position")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const q = `
		INSERT INTO positions (
			chain, token, token_symbol, amount, decimals, entry_price_usd, current_price_usd,
			peak_price_usd, invested_usd, stop_loss_pct, trailing_stop_pct,
			liquidity_at_entry_usd, current_liquidity_usd, status, signal_id, dry_run, opened_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING id::text`
	amount := "0"
	if pos.Amount != nil {
		amount = pos.Amount.String()
	}
	status := string(pos.Status)
	if status == "" {
		status = string(model.PositionOpen)
	}
	signalID := pos.SignalID
	return p.pool.QueryRow(ctx, q,
		strings.ToLower(pos.Chain), strings.ToLower(pos.Token), pos.TokenSymbol, amount, pos.Decimals,
		pos.EntryPriceUSD, pos.CurrentPriceUSD, pos.PeakPriceUSD, pos.InvestedUSD,
		pos.StopLossPct, pos.TrailingStopPct, pos.LiquidityAtEntryUSD, pos.CurrentLiquidityUSD,
		status, nullUUID(signalID), pos.DryRun, pos.OpenedAt, time.Now().UTC(),
	).Scan(&pos.ID)
}

// Update 实现 model.PositionStore。
func (p *PGPositions) Update(pos *model.Position) error {
	if pos == nil || pos.ID == "" {
		return errors.New("positions: invalid position")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const q = `
		UPDATE positions SET
			current_price_usd = $2, peak_price_usd = $3, current_liquidity_usd = $4,
			status = $5, updated_at = NOW()
		WHERE id = $1`
	_, err := p.pool.Exec(ctx, q, pos.ID, pos.CurrentPriceUSD, pos.PeakPriceUSD,
		pos.CurrentLiquidityUSD, string(pos.Status))
	return err
}

// Get 实现 model.PositionStore。
func (p *PGPositions) Get(id string) (*model.Position, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := p.query(ctx, `WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// ListOpen 实现 model.PositionStore。
func (p *PGPositions) ListOpen(chain string) ([]*model.Position, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if chain == "" {
		return p.query(ctx, `WHERE status = 'open'`)
	}
	return p.query(ctx, `WHERE status = 'open' AND chain = $1`, strings.ToLower(chain))
}

// Close 实现 model.PositionStore。
func (p *PGPositions) Close(id string, exitPriceUSD float64, realizedPnLUSD float64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const q = `
		UPDATE positions SET
			status = 'closed', exit_price_usd = $2, realized_pnl_usd = $3,
			closed_at = NOW(), updated_at = NOW()
		WHERE id = $1`
	_, err := p.pool.Exec(ctx, q, id, exitPriceUSD, realizedPnLUSD)
	return err
}

func (p *PGPositions) query(ctx context.Context, where string, args ...any) ([]*model.Position, error) {
	q := `
		SELECT id::text, chain, token, COALESCE(token_symbol,''), amount::text, decimals,
		       entry_price_usd, current_price_usd, peak_price_usd, invested_usd,
		       stop_loss_pct, trailing_stop_pct, liquidity_at_entry_usd, current_liquidity_usd,
		       status, COALESCE(exit_price_usd,0), COALESCE(realized_pnl_usd,0),
		       COALESCE(signal_id::text,''), dry_run, opened_at, COALESCE(closed_at, to_timestamp(0)), updated_at
		  FROM positions ` + where

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Position
	for rows.Next() {
		var (
			pos      model.Position
			amount   string
			status   string
			openedAt time.Time
			closedAt time.Time
		)
		if err := rows.Scan(
			&pos.ID, &pos.Chain, &pos.Token, &pos.TokenSymbol, &amount, &pos.Decimals,
			&pos.EntryPriceUSD, &pos.CurrentPriceUSD, &pos.PeakPriceUSD, &pos.InvestedUSD,
			&pos.StopLossPct, &pos.TrailingStopPct, &pos.LiquidityAtEntryUSD, &pos.CurrentLiquidityUSD,
			&status, &pos.ExitPriceUSD, &pos.RealizedPnLUSD, &pos.SignalID, &pos.DryRun,
			&openedAt, &closedAt, &pos.UpdatedAt,
		); err != nil {
			return nil, err
		}
		pos.Status = model.PositionStatus(status)
		pos.Amount = parseBigInt(amount)
		pos.OpenedAt = openedAt
		if !closedAt.IsZero() && closedAt.Unix() > 0 {
			pos.ClosedAt = closedAt
		}
		out = append(out, &pos)
	}
	return out, rows.Err()
}

var _ model.PositionStore = (*PGPositions)(nil)

// ---- 小工具 ----

func parseBigInt(s string) *big.Int {
	if strings.TrimSpace(s) == "" {
		return big.NewInt(0)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return v
}

func nullUUID(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

var _ = pgx.ErrNoRows
