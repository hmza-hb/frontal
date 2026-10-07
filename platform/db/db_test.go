package db

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hmza-hb/lead-intelligence/platform/config"
	"github.com/hmza-hb/lead-intelligence/platform/logging"
	"github.com/hmza-hb/lead-intelligence/platform/testutil"
)

func testPool(t *testing.T) *Pool {
	t.Helper()
	return testutil.Postgres(t)
}

func fsWith(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

func TestMigrateAppliesPendingFiles(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	src := Source{Module: "demo", FS: fsWith(map[string]string{
		"0001_first.sql":  `CREATE TABLE demo_first (id bigint PRIMARY KEY)`,
		"0002_second.sql": `CREATE TABLE demo_second (id bigint PRIMARY KEY)`,
	})}

	res, err := Migrate(ctx, pool, []Source{src}, logging.Discard())
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(res.Applied) != 2 {
		t.Fatalf("Applied = %v, want 2 files", res.Applied)
	}
	if res.Applied[0] != "demo/0001_first.sql" || res.Applied[1] != "demo/0002_second.sql" {
		t.Errorf("applied out of order: %v", res.Applied)
	}
	if res.Total() != 2 || len(res.Skipped) != 0 {
		t.Errorf("unexpected result: %+v", res)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM demo_first`).Scan(&n); err != nil {
		t.Fatalf("query demo_first: %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	src := Source{Module: "demo", FS: fsWith(map[string]string{
		"0001_first.sql": `CREATE TABLE demo_first (id bigint PRIMARY KEY)`,
	})}

	if _, err := Migrate(ctx, pool, []Source{src}, logging.Discard()); err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, pool, []Source{src}, logging.Discard())
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(res.Applied) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("second run applied %v, skipped %v", res.Applied, res.Skipped)
	}
}

func TestMigrateDetectsDrift(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	first := Source{Module: "demo", FS: fsWith(map[string]string{
		"0001_first.sql": `CREATE TABLE demo_first (id bigint PRIMARY KEY)`,
	})}
	if _, err := Migrate(ctx, pool, []Source{first}, logging.Discard()); err != nil {
		t.Fatal(err)
	}

	edited := Source{Module: "demo", FS: fsWith(map[string]string{
		"0001_first.sql": `CREATE TABLE demo_first (id text PRIMARY KEY)`,
	})}
	_, err := Migrate(ctx, pool, []Source{edited}, logging.Discard())
	if err == nil {
		t.Fatal("Migrate silently accepted an edited applied migration")
	}
	if !strings.Contains(err.Error(), "0001_first.sql") || !strings.Contains(err.Error(), "checksum") &&
		!strings.Contains(err.Error(), "modified after it was applied") {
		t.Errorf("drift error is not actionable: %v", err)
	}
}

func TestMigrateIgnoresCRLFDrift(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := Migrate(ctx, pool, []Source{{Module: "demo", FS: fsWith(map[string]string{
		"0001_first.sql": "CREATE TABLE demo_first (id bigint PRIMARY KEY)\n",
	})}}, logging.Discard()); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, pool, []Source{{Module: "demo", FS: fsWith(map[string]string{
		"0001_first.sql": "CREATE TABLE demo_first (id bigint PRIMARY KEY)\r\n",
	})}}, logging.Discard()); err != nil {
		t.Fatalf("CRLF checkout reported drift: %v", err)
	}
}

func TestMigrateRollsBackAFailedFile(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	src := Source{Module: "demo", FS: fsWith(map[string]string{
		"0001_ok.sql":  `CREATE TABLE demo_ok (id bigint PRIMARY KEY)`,
		"0002_bad.sql": `CREATE TABLE demo_bad (id bigint PRIMARY KEY); SELECT this_is_not_sql()`,
	})}

	if _, err := Migrate(ctx, pool, []Source{src}, logging.Discard()); err == nil {
		t.Fatal("Migrate accepted invalid SQL")
	}

	// 0001 committed before 0002 failed, and 0002 must not be recorded.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE module='demo' AND name='0001_ok.sql'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("0001 recorded %d times, want 1", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE module='demo' AND name='0002_bad.sql'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the failed migration was recorded as applied")
	}
}

func TestMigrateSkipsEmptyAndNonSQLFiles(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	res, err := Migrate(ctx, pool, []Source{{Module: "demo", FS: fsWith(map[string]string{
		"0001_real.sql":  `CREATE TABLE demo_real (id bigint PRIMARY KEY)`,
		"0002_blank.sql": "   \n\n",
		"README.md":      "not sql",
	})}}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 {
		t.Fatalf("Applied = %v, want only the real SQL file", res.Applied)
	}
}

func TestMigrateRejectsUnnamedSource(t *testing.T) {
	pool := testPool(t)
	_, err := Migrate(context.Background(), pool, []Source{{FS: fsWith(map[string]string{})}}, logging.Discard())
	if err == nil {
		t.Fatal("Migrate accepted a source with no module name")
	}
}

func TestMigrateAcrossModules(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	res, err := Migrate(ctx, pool, []Source{
		{Module: "alpha", FS: fsWith(map[string]string{"0001_a.sql": `CREATE TABLE alpha_a (id int)`})},
		{Module: "beta", FS: fsWith(map[string]string{"0001_b.sql": `CREATE TABLE beta_b (id int)`})},
	}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 2 {
		t.Fatalf("Applied = %v", res.Applied)
	}
	// Same filename in two modules must not collide.
	if res.Applied[0] == res.Applied[1] {
		t.Error("two modules produced the same migration key")
	}
}

func TestInTxCommitsOnSuccess(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE tx_demo (id int)`); err != nil {
		t.Fatal(err)
	}
	if err := InTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tx_demo (id) VALUES (1)`)
		return err
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tx_demo`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 committed", n)
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE tx_rollback (id int)`); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("abort")
	err := InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tx_rollback (id) VALUES (1)`); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("InTx returned %v, want the caller's error", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tx_rollback`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rows = %d, want 0 after rollback", n)
	}
}

func TestInTxRollsBackOnPanic(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE tx_panic (id int)`); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("InTx swallowed the panic")
			}
		}()
		_ = InTx(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO tx_panic (id) VALUES (1)`); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tx_panic`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rows = %d, want 0: the panic must not commit", n)
	}
}

func TestOpenValidatesConfig(t *testing.T) {
	_, err := Open(context.Background(), config.DatabaseConfig{}, "test")
	if err == nil {
		t.Fatal("Open accepted an empty DSN")
	}
}

func TestOpenReportsUnreachableDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := config.DatabaseConfig{
		URL:            "postgres://postgres:postgres@127.0.0.1:1/nope?sslmode=disable",
		MaxConns:       1,
		ConnectTimeout: 2 * time.Second,
	}
	_, err := Open(ctx, cfg, "test")
	if err == nil {
		t.Fatal("Open succeeded against a dead port")
	}
	if !strings.Contains(err.Error(), "ping") {
		t.Errorf("error should identify the failing stage: %v", err)
	}
}

func TestWaitReadyReturnsOnHealthyDatabase(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := WaitReady(ctx, pool, 10*time.Millisecond); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
}

func TestMigrationKeyIsStable(t *testing.T) {
	if got := path.Base(hashSQL("SELECT 1")); got == "" {
		t.Fatal("hashSQL returned an empty digest")
	}
	if hashSQL("SELECT 1\r\n") != hashSQL("SELECT 1\n") {
		t.Error("hashSQL is sensitive to line endings")
	}
}

func TestMigrateCreatesMissingTargetSchema(t *testing.T) {
	// A deployment that points search_path at a per-module schema should not
	// have to create it by hand, and must not be met with "no schema has been
	// selected to create".
	const schema = "created_by_migrate"
	pool := testutil.PostgresInMissingSchema(t, schema)
	ctx := context.Background()

	// The precondition: the schema really is absent.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("precondition failed: the schema already exists")
	}

	src := Source{Module: "demo", FS: fsWith(map[string]string{
		"0001_first.sql": `CREATE TABLE demo_in_new_schema (id bigint PRIMARY KEY)`,
	})}
	if _, err := Migrate(ctx, pool, []Source{src}, logging.Discard()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// The table must have landed in the schema named by search_path, not public.
	var resolved string
	if err := pool.QueryRow(ctx,
		`SELECT table_schema FROM information_schema.tables
		 WHERE table_name = 'demo_in_new_schema'`).Scan(&resolved); err != nil {
		t.Fatalf("table was not created: %v", err)
	}
	if resolved != schema {
		t.Errorf("table is in schema %q, want %q", resolved, schema)
	}
}

func TestEnsureTargetSchemaRespectsSearchPathShape(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		path    string
		wantNew string // schema that must be created, "" for none
	}{
		{"bare identifier is created", "made_by_test_a", "made_by_test_a"},
		{"quoted entry is left alone", `"made by test b"`, ""},
		{"dollar user is left alone", `"$user"`, ""},
		{"a name needing quoting is left alone", `"Mixed Case"`, ""},
		{"only the first entry is used", "made_by_test_c, public", "made_by_test_c"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()

			if _, err := conn.Exec(ctx, `SET search_path TO `+tc.path); err != nil {
				t.Skipf("server rejects this search_path: %v", err)
			}
			defer func() {
				_, _ = conn.Exec(ctx, `SET search_path TO public`)
				// Leave the shared pool exactly as we found it.
				_, _ = conn.Exec(ctx, `DROP SCHEMA IF EXISTS made_by_test_a CASCADE`)
				_, _ = conn.Exec(ctx, `DROP SCHEMA IF EXISTS made_by_test_c CASCADE`)
			}()

			if err := ensureTargetSchema(ctx, conn.Conn()); err != nil {
				t.Fatalf("ensureTargetSchema: %v", err)
			}

			var exists bool
			if tc.wantNew != "" {
				if err := conn.QueryRow(ctx,
					`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`,
					tc.wantNew).Scan(&exists); err != nil {
					t.Fatal(err)
				}
				if !exists {
					t.Errorf("schema %q was not created", tc.wantNew)
				}
			}
		})
	}
}
