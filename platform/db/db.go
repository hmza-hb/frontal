// Package db owns the Postgres connection pool and the migration engine.
//
// Repositories in other modules take a *pgxpool.Pool directly rather than a
// platform type, so they depend on pgx — not on this package — and can be
// lifted into another repository without dragging the kernel along.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hmza-hb/lead-intelligence/platform/config"
)

// Pool is the concrete pool type used across the platform.
type Pool = pgxpool.Pool

// Open creates a pool and verifies it can actually reach the database. A
// misconfigured DSN fails here rather than at the first query.
func Open(ctx context.Context, cfg config.DatabaseConfig, appName string) (*Pool, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("db: DATABASE_URL is required")
	}
	cfg = withDefaults(cfg)

	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("db: parse DATABASE_URL: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.MaxConnIdleTime = 10 * time.Minute
	poolCfg.HealthCheckPeriod = cfg.HealthCheck
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	// Server-side statement timeout. It bounds the damage a pathological query
	// can do even if the client's context deadline is lost.
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = ms(cfg.StatementTimeout)
	poolCfg.ConnConfig.RuntimeParams["application_name"] = appName
	// Prepare statements are cached per connection; the platform's hot paths
	// (frontier claims, run ledger) reuse the same SQL on every call.
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping %s: %w", appName, err)
	}
	return pool, nil
}

// withDefaults fills in anything a caller left at zero. A library must not
// hand a zero HealthCheckPeriod to pgxpool, which panics on it.
func withDefaults(cfg config.DatabaseConfig) config.DatabaseConfig {
	if cfg.MaxConns < 1 {
		cfg.MaxConns = 8
	}
	if cfg.MinConns < 0 {
		cfg.MinConns = 0
	}
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.StatementTimeout <= 0 {
		cfg.StatementTimeout = 30 * time.Second
	}
	if cfg.HealthCheck <= 0 {
		cfg.HealthCheck = 30 * time.Second
	}
	return cfg
}

// WaitReady blocks until the database answers a ping or ctx is done. Services
// call it on boot so a rolling deploy waits for Postgres rather than crash
// looping.
func WaitReady(ctx context.Context, pool *Pool, every time.Duration) error {
	if every <= 0 {
		every = time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	var last error
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("db: not ready: %w (last error: %v)", ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

// Beginner is anything that can start a transaction. Both *pgxpool.Pool and a
// single acquired connection satisfy it, which is what lets Migrate hold a
// session advisory lock on one connection while running its DDL on that same
// connection.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// InTx runs fn inside a transaction, rolling back on error or panic. Nested
// calls are not supported: pass the tx explicitly instead of relying on
// ambient state.
func InTx(ctx context.Context, b Beginner, fn func(tx pgx.Tx) error) (err error) {
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// Use a context that survives cancellation so the rollback is not
			// itself cut short by a cancelled request.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// LogStats writes pool utilisation at a debug level. Cheap enough to call from
// a background ticker, and the first thing anyone looks at when the database
// looks slow.
func LogStats(ctx context.Context, pool *Pool, log *slog.Logger) {
	s := pool.Stat()
	log.DebugContext(ctx, "postgres pool",
		"acquired", s.AcquiredConns(),
		"idle", s.IdleConns(),
		"total", s.TotalConns(),
		"max", s.MaxConns(),
		"acquire_count", s.AcquireCount(),
		"acquire_duration_ms", s.AcquireDuration().Milliseconds(),
		"empty_acquire_count", s.EmptyAcquireCount(),
	)
}

func ms(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	return fmt.Sprintf("%d", d.Milliseconds())
}
