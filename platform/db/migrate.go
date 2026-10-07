package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Source is one module's set of migration files. Every module owns its own
// migrations directory and passes it here; the runner only cares about the
// files, not which module wrote them.
type Source struct {
	// Module is the owner, used in logs and in the applied-file key.
	Module string
	// FS must contain .sql files at its root.
	FS fs.FS
}

// migrationLockKey serialises migrations across processes. Any constant works
// as long as every process agrees on it; a session-level advisory lock is used
// so an abandoned connection cannot leave a migration half-applied.
const migrationLockKey int64 = 8_675_309

const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    module       text        NOT NULL,
    name         text        NOT NULL,
    checksum     text        NOT NULL,
    applied_at   timestamptz NOT NULL DEFAULT now(),
    duration_ms  integer     NOT NULL DEFAULT 0,
    PRIMARY KEY (module, name)
)`

// MigrationResult reports what Migrate did.
type MigrationResult struct {
	Applied []string // "module/file.sql", in application order
	Skipped []string // already applied with a matching checksum
}

// Total reports how many migrations the run considered.
func (r MigrationResult) Total() int { return len(r.Applied) + len(r.Skipped) }

// Migrate applies every pending migration in name order within each module,
// each in its own transaction.
//
// A migration whose checksum no longer matches what is recorded in the
// database is treated as an error, not a re-run: silently applying a changed
// file to a live database is how schemas rot.
func Migrate(ctx context.Context, pool *Pool, sources []Source, log *slog.Logger) (MigrationResult, error) {
	var result MigrationResult

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return result, fmt.Errorf("db: acquire for migrate: %w", err)
	}
	defer conn.Release()

	// Session advisory lock: held on this dedicated connection, so it cannot be
	// lost to a pooled connection being returned mid-migration.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return result, fmt.Errorf("db: acquire migration lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	// A search_path naming a schema that does not exist yet makes every
	// unqualified CREATE fail with "no schema has been selected", which tells an
	// operator nothing. Creating it here is the behaviour a deployment actually
	// wants when it points search_path at a per-module schema.
	if err := ensureTargetSchema(ctx, conn.Conn()); err != nil {
		return result, err
	}

	if _, err := conn.Exec(ctx, createMigrationsTable); err != nil {
		return result, fmt.Errorf("db: create schema_migrations: %w", err)
	}

	applied, err := loadApplied(ctx, conn.Conn())
	if err != nil {
		return result, err
	}

	for _, src := range sources {
		files, err := migrationFiles(src)
		if err != nil {
			return result, err
		}
		for _, f := range files {
			key := src.Module + "/" + f.name
			sum := hashSQL(f.body)

			if prev, ok := applied[key]; ok {
				if prev != sum {
					return result, fmt.Errorf(
						"db: migration %s was modified after it was applied (recorded %s, file %s); "+
							"add a new migration instead of editing an applied one", key, short(prev), short(sum))
				}
				result.Skipped = append(result.Skipped, key)
				continue
			}

			start := time.Now()
			if err := runMigration(ctx, conn.Conn(), src.Module, f.name, sum, f.body, time.Since(start)); err != nil {
				return result, fmt.Errorf("db: apply %s: %w", key, err)
			}
			if log != nil {
				log.InfoContext(ctx, "migration applied",
					"migration", key, "duration_ms", time.Since(start).Milliseconds())
			}
			result.Applied = append(result.Applied, key)
		}
	}
	return result, nil
}

type migrationFile struct {
	name string
	body string
}

func migrationFiles(src Source) ([]migrationFile, error) {
	if strings.TrimSpace(src.Module) == "" {
		return nil, errors.New("db: migration source is missing a module name")
	}
	entries, err := fs.ReadDir(src.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("db: read migrations for %s: %w", src.Module, err)
	}
	var out []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := fs.ReadFile(src.FS, e.Name())
		if err != nil {
			return nil, fmt.Errorf("db: read %s/%s: %w", src.Module, e.Name(), err)
		}
		if strings.TrimSpace(string(body)) == "" {
			continue
		}
		out = append(out, migrationFile{name: e.Name(), body: string(body)})
	}
	// Lexicographic order is the contract: zero-padded numeric prefixes give
	// deterministic ordering without a manifest file.
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// ensureTargetSchema creates the first schema named in search_path when it is
// missing. A search_path of "public" (or empty, or a quoted path that is not a
// bare identifier) is left alone: those already exist or are not ours to create.
func ensureTargetSchema(ctx context.Context, conn *pgx.Conn) error {
	var raw string
	if err := conn.QueryRow(ctx, `SELECT current_setting('search_path')`).Scan(&raw); err != nil {
		return fmt.Errorf("db: read search_path: %w", err)
	}
	first, _, _ := strings.Cut(raw, ",")
	first = strings.TrimSpace(first)
	// A quoted or "$user"-style entry is a valid schema name we must not guess at.
	if first == "" || first == "$user" || !bareIdentifier.MatchString(first) {
		return nil
	}

	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, first).Scan(&exists); err != nil {
		return fmt.Errorf("db: check schema %q: %w", first, err)
	}
	if exists {
		return nil
	}
	if _, err := conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{first}.Sanitize()); err != nil {
		return fmt.Errorf("db: create schema %q from search_path: %w", first, err)
	}
	return nil
}

// bareIdentifier matches a schema name that is safe to interpolate. Every other
// name is handled by quoting or skipped entirely.
var bareIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func runMigration(ctx context.Context, conn *pgx.Conn, module, name, sum, body string, took time.Duration) error {
	return InTx(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, body); err != nil {
			return fmt.Errorf("execute: %w", err)
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (module, name, checksum, duration_ms) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (module, name) DO NOTHING`,
			module, name, sum, took.Milliseconds())
		return err
	})
}

func loadApplied(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, `SELECT module || '/' || name, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var key, sum string
		if err := rows.Scan(&key, &sum); err != nil {
			return nil, fmt.Errorf("db: scan schema_migrations: %w", err)
		}
		out[key] = sum
	}
	return out, rows.Err()
}

func hashSQL(body string) string {
	// Normalise line endings so a checkout with CRLF does not look like drift.
	norm := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

func short(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
