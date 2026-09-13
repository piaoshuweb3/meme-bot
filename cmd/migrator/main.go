// Command migrator 按文件名顺序执行 migrations 目录下的 SQL 脚本。
//
// 用法：
//
//	go run ./cmd/migrator -config configs/config.yaml -dir migrations
//
// 已执行的脚本记录在 schema_migrations 表中，重复执行是幂等的。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"meme-bot/internal/config"
)

var (
	flagConfig = flag.String("config", "configs/config.yaml", "配置文件路径")
	flagDir    = flag.String("dir", "migrations", "SQL 迁移目录")
)

func main() {
	flag.Parse()

	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	files, err := collect(*flagDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collect migrations: %v\n", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Printf("no migration files under %s\n", *flagDir)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	conn, err := pgx.Connect(ctx, cfg.Database.DSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close(ctx) }()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		fmt.Fprintf(os.Stderr, "ensure schema_migrations: %v\n", err)
		os.Exit(1)
	}

	applied := 0
	for _, f := range files {
		version := filepath.Base(f)
		var exists bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
			fmt.Fprintf(os.Stderr, "check %s: %v\n", version, err)
			os.Exit(1)
		}
		if exists {
			fmt.Printf("skip (already applied): %s\n", version)
			continue
		}

		body, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", f, err)
			os.Exit(1)
		}
		if err := applyOne(ctx, conn, version, string(body)); err != nil {
			fmt.Fprintf(os.Stderr, "apply %s: %v\n", version, err)
			os.Exit(1)
		}
		fmt.Printf("applied: %s\n", version)
		applied++
	}

	fmt.Printf("done, %d migration(s) applied\n", applied)
}

// applyOne 在单个事务中执行一个脚本并登记版本号。
func applyOne(ctx context.Context, conn *pgx.Conn, version, body string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, body); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func collect(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}
