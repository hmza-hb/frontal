# platform

The shared kernel. Every other module imports this one; this one imports
nothing from the workspace.

**It is a leaf on purpose.** If the platform ever wants to be split into
several products, `platform` is published first — it has no sibling
dependencies, so `git mv platform ../platform` and `go mod tidy` is the whole
procedure.

## What lives here

| Package | Responsibility |
| --- | --- |
| `config` | Environment configuration and validation. Only cross-cutting concerns: environment, logging, HTTP, database. |
| `db` | Postgres pool, `InTx`, and the migration engine (checksummed, drift-detecting, advisory-locked). |
| `httpx` | HTTP server: timeouts, request IDs, panic recovery, JSON responses, uniform errors, probes, graceful shutdown. |
| `logging` | `slog` construction and run/request identity carried through `context`. |
| `observe` | Dependency-free Prometheus-compatible metrics registry. |
| `retry` | One backoff policy, with jitter, for the whole platform. |
| `ratelimit` | Token-bucket limiter and a keyed table with LRU/idle eviction. |
| `circuit` | Circuit breaker with half-open probing. |
| `hash` | Content addressing. Every cache, dedupe and novelty decision keys off it. |
| `testutil` | Postgres-per-test schemas, a controllable HTTP origin, a fake clock. |

## What this module deliberately does not contain

Anything about leads, companies, people, crawling, or research. Those belong to
the module that owns the concept. `platform` grows a dependency on the domain
only when there is no module left to put the code in — which so far has not
happened.

## Migrations

`db.Migrate` takes a list of `Source{Module, FS}` — one per module — and
applies them in filename order, each in its own transaction. The rules:

* **Applied files are immutable.** A file whose content changed after it ran is
  an error, not a re-run. Editing an applied migration silently corrupts a live
  database.
* **CRLF is not drift.** Line endings are normalised before hashing so a Windows
  checkout does not fail a deploy.
* **Concurrent migrators are safe.** A session advisory lock on a dedicated
  connection serialises them.
* **A failure rolls back that file only.** Everything before it stays applied.

Zero-padded numeric prefixes (`0001_`, `0002_`) give ordering without a
manifest file.

## Testing

```bash
# Unit tests need nothing.
go test ./...

# Postgres-backed tests create a private schema per test and drop it after.
TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/leads go test ./...
```

Without `TEST_DATABASE_URL` (or `DATABASE_URL`) the Postgres tests skip rather
than fail, so a clean checkout still gets a green test run.

`testutil.Postgres` gives each test its own schema via the connection's
`search_path`, which means tests never collide, never need truncation, and can
safely run in parallel.
