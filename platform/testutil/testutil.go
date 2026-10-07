// Package testutil provides the test-only helpers the platform's modules
// share: an isolated Postgres schema per test, a fixture HTTP origin, and
// deterministic clocks.
//
// It is a normal (non _test) package so that every module in the workspace can
// import it from its own test files.
package testutil

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DatabaseURLEnv names the environment variable holding the test DSN. Tests
// that need Postgres skip when it is unset, so `go test ./...` still works on
// a machine with no database.
const DatabaseURLEnv = "TEST_DATABASE_URL"

// Postgres returns a pool bound to a private schema that is dropped when the
// test finishes. Each test therefore gets its own tables with no cleanup race
// and no interference from parallel tests.
//
// The caller must call Cleanup (via t.Cleanup inside this function) — which it
// does. Callers only need to handle the skip.
func Postgres(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv(DatabaseURLEnv)
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skipf("set %s (or DATABASE_URL) to run Postgres-backed tests", DatabaseURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := openPool(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to %s: %v", DatabaseURLEnv, err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping database: %v", err)
	}

	schema := fmt.Sprintf("t_%s_%d", sanitise(t.Name()), rand.Uint32())
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}

	// search_path is set per connection via the DSN so every pooled connection
	// in this test sees the same schema, including ones created later.
	pool, err := openPool(ctx, withSearchPath(dsn, schema))
	if err != nil {
		_, _ = admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatalf("open scoped pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatalf("ping scoped pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		_, _ = admin.Exec(dropCtx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		admin.Close()
	})
	return pool
}

// PostgresInMissingSchema returns a pool pointed at a schema that does NOT
// exist yet, and drops it when the test finishes. It exists to test the code
// paths that have to cope with that state — a deployment pointing search_path
// at a fresh per-module schema — which Postgres refuses to reproduce with a
// normal CREATE SCHEMA first.
func PostgresInMissingSchema(t *testing.T, schema string) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv(DatabaseURLEnv)
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skipf("set %s (or DATABASE_URL) to run Postgres-backed tests", DatabaseURLEnv)
	}
	if !bareSchemaName.MatchString(schema) {
		t.Fatalf("schema name %q must be a bare identifier", schema)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := openPool(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to %s: %v", DatabaseURLEnv, err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		_, _ = admin.Exec(dropCtx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		admin.Close()
	})

	pool, err := openPool(ctx, withSearchPath(dsn, schema))
	if err != nil {
		t.Fatalf("open pool for missing schema %s: %v", schema, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// bareSchemaName guards the interpolated identifier above.
var bareSchemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// openPool builds a small, healthy pool. pgxpool panics on a zero
// HealthCheckPeriod and an unbounded pool makes test runs unpredictable on a
// busy machine, so both are pinned here rather than at every call site.
func openPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.HealthCheckPeriod = 30 * time.Second
	cfg.MaxConns = 8
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 10 * time.Minute
	return pgxpool.NewWithConfig(ctx, cfg)
}

func withSearchPath(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

func sanitise(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// FreePort returns a TCP port that was free a moment ago. Racy by nature, but
// adequate for binding a test server; httptest.NewServer is preferred where
// possible.
func FreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Origin is a controllable HTTP server for crawler and extractor tests.
type Origin struct {
	*httptest.Server

	hits   atomic.Int64
	mu     chan struct{}
	routes map[string]http.HandlerFunc
}

// NewOrigin starts a server. Routes are registered before the first request.
func NewOrigin(t *testing.T, routes map[string]http.HandlerFunc) *Origin {
	t.Helper()
	o := &Origin{mu: make(chan struct{}, 1), routes: routes}
	o.mu <- struct{}{}
	o.Server = httptest.NewServer(http.HandlerFunc(o.serve))
	t.Cleanup(o.Close)
	return o
}

func (o *Origin) serve(w http.ResponseWriter, r *http.Request) {
	o.hits.Add(1)
	o.mu <- struct{}{}
	h, ok := o.routes[r.URL.Path]
	o.mu <- struct{}{}
	if !ok {
		http.NotFound(w, r)
		return
	}
	h(w, r)
}

// Hits reports how many requests the origin has served.
func (o *Origin) Hits() int64 { return o.hits.Load() }

// Static is the common case: a fixed body for one path.
func Static(body, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}
}

// StaticRoutes builds a route map from path to body.
func StaticRoutes(contentType string, bodies map[string]string) map[string]http.HandlerFunc {
	out := make(map[string]http.HandlerFunc, len(bodies))
	for path, body := range bodies {
		out[path] = Static(body, contentType)
	}
	return out
}

// Clock is a manually advanced clock for tests that must not sleep.
type Clock struct {
	mu  chan struct{}
	now time.Time
}

// NewClock returns a clock fixed at a stable instant.
func NewClock() *Clock {
	c := &Clock{mu: make(chan struct{}, 1), now: time.Unix(1_700_000_000, 0).UTC()}
	c.mu <- struct{}{} // seed the token
	return c
}

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	<-c.mu
	defer func() { c.mu <- struct{}{} }()
	return c.now
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	<-c.mu
	c.now = c.now.Add(d)
	c.mu <- struct{}{}
}

// Sleep records the requested duration and advances the clock. A real time.Timer
// cannot be injected into every call site, so limiters that accept a sleep hook
// use this instead.
func (c *Clock) Sleep(d time.Duration) error {
	c.Advance(d)
	return nil
}
