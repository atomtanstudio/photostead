// Package store owns all SQL. Reads for a viewer go through the visible_assets() gate.
package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"photostead.com/server/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	Pool *pgxpool.Pool
	Q    *db.Queries
}

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 32
	// JIT compiling only pays off for long analytic queries. Ours are short, but a scan of the
	// whole library crosses its cost threshold, and compiling then takes longer than the query
	// (the rail's counts: 450 ms with JIT, 50 ms without).
	if _, ok := cfg.ConnConfig.RuntimeParams["jit"]; !ok {
		cfg.ConnConfig.RuntimeParams["jit"] = "off"
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to Postgres: %w", err)
	}
	return &Store{Pool: pool, Q: db.New(pool)}, nil
}

func (s *Store) Close() { s.Pool.Close() }

// Migrate applies Photostead's schema and River's job tables. Both only move forward
// automatically; every migration also has a down step.
func (s *Store) Migrate(ctx context.Context) error {
	sqlDB := stdlib.OpenDBFromPool(s.Pool)
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub(migrations, "migrations"))
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("schema migrations: %w", err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(s.Pool), nil)
	if err != nil {
		return err
	}
	if _, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("job queue migrations: %w", err)
	}
	return nil
}

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(q *db.Queries) error) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error { return fn(s.Q.WithTx(tx)) })
}
