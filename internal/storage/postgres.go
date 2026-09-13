// Package storage 提供 PostgreSQL / Redis 连接的构建与健康检查。
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"meme-bot/internal/config"
)

// NewPostgres 建立 PostgreSQL 连接池。
func NewPostgres(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.Database.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if cfg.Database.MaxConns > 0 {
		pc.MaxConns = cfg.Database.MaxConns
	}
	if cfg.Database.MinConns > 0 {
		pc.MinConns = cfg.Database.MinConns
	}
	if cfg.Database.MaxConnLifetimeMinutes > 0 {
		pc.MaxConnLifetime = time.Duration(cfg.Database.MaxConnLifetimeMinutes) * time.Minute
	}

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}
