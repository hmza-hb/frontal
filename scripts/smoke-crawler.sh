#!/usr/bin/env bash
#
# End-to-end smoke test for crawld.
#
# The Go tests already cover the API against httptest and a real Postgres. This
# script covers what those cannot: the actual binary, the actual configuration
# loader, the actual command line, and a real HTTP server on the other end.
#
# It is deliberately self-contained and destructive only to a scratch schema it
# creates and drops.
#
#   TEST_DATABASE_URL=postgres://... ./scripts/smoke-crawler.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="${TMPDIR:-/tmp}/crawld-smoke.$$"
SCHEMA="crawld_smoke_$$"
TARGET_PORT="${CRAWL_SMOKE_TARGET_PORT:-18099}"
SERVICE_PORT="${CRAWL_SMOKE_SERVICE_PORT:-18080}"

cleanup() {
  local rc=$?
  set +e
  [ -n "${SERVICE_PID:-}" ] && kill "$SERVICE_PID" 2>/dev/null
  [ -n "${TARGET_PID:-}" ] && kill "$TARGET_PID" 2>/dev/null
  wait 2>/dev/null
  [ -n "${DSN:-}" ] && psql_admin() { PGPASSWORD="$PGPASS" psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDB" -q -c "$1" >/dev/null 2>&1; } && psql_admin "DROP SCHEMA IF EXISTS $SCHEMA CASCADE"
  rm -rf "$WORK"
  exit $rc
}
trap cleanup EXIT

fail() { printf '\n\033[31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }
pass() { printf '  \033[32mok\033[0m %s\n' "$*"; }

# A stale process on the port would answer the health check and quietly serve
# a different schema, producing results that look real and are not. Refuse to
# start on a busy port rather than test someone else's server.
port_in_use() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null && { exec 3<&- 3>&-; return 0; }
  return 1
}

for p in "$TARGET_PORT" "$SERVICE_PORT"; do
  port_in_use "$p" && fail "port $p is already in use; stop it or set CRAWL_SMOKE_*_PORT"
done

# ─── Database ────────────────────────────────────────────────────────────────
DSN="${TEST_DATABASE_URL:-${DATABASE_URL:-}}"
[ -n "$DSN" ] || fail "set TEST_DATABASE_URL or DATABASE_URL to run the smoke test"
command -v psql >/dev/null 2>&1 || fail "psql is required to manage the scratch schema"

# Split the DSN just enough to drive psql for the schema dance.
rest="${DSN#*://}"
creds="${rest%%@*}"
hostport="${rest#*@}"; hostport="${hostport%%/*}"
PGUSER="${creds%%:*}"; PGPASS="${creds#*:}"; PGPASS="${PGPASS%%\?*}"
PGHOST="${hostport%%:*}"
PGPORT="${hostport##*:}"; [ "$PGPORT" = "$PGHOST" ] && PGPORT=5432
PGDB="postgres"

mkdir -p "$WORK"
export PGPASSWORD="$PGPASS"
psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDB" -q -c "DROP SCHEMA IF EXISTS $SCHEMA CASCADE; CREATE SCHEMA $SCHEMA;" \
  || fail "could not create scratch schema $SCHEMA"

# ─── Target site ─────────────────────────────────────────────────────────────
cat > "$WORK/target.go" <<'GO'
package main

// A tiny site for the smoke test: robots.txt with a crawl-delay, a sitemap, an
// ETag so the conditional-GET path is actually exercised, one disallowed path,
// and one non-HTML resource so content filtering is exercised too.

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := os.Args[1]

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nCrawl-delay: 1\nSitemap: http://"+r.Host+"/sitemap.xml\n")
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`+
			`<url><loc>http://%s/contact</loc></url></urlset>`, r.Host)
	})
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", fmt.Sprintf("%q", "v1-"+r.URL.Path))
		if r.Header.Get("If-None-Match") == fmt.Sprintf("%q", "v1-"+r.URL.Path) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<a href="/about">About</a> <a href="/logo.png">Logo</a>`)
		case "/about":
			fmt.Fprint(w, `<h1>Acme Industrial</h1><p>We manufacture valves since 1994.</p>`)
		default:
			fmt.Fprint(w, `<p>Contact: sales@acme.example</p>`)
		}
	})

	fmt.Fprintln(os.Stderr, "target listening on "+port)
	_ = http.ListenAndServe("127.0.0.1:"+port, mux)
}
GO

(cd "$WORK" && go mod init smoke >/dev/null 2>&1; go build -o "$WORK/target" target.go) || fail "could not build the target site"
"$WORK/target" "$TARGET_PORT" >"$WORK/target.log" 2>&1 &
TARGET_PID=$!

# ─── Service ─────────────────────────────────────────────────────────────────
(cd "$ROOT/crawler" && go build -o "$WORK/crawld" ./cmd/crawld) || fail "could not build crawld"

DATABASE_URL="${DSN}&search_path=${SCHEMA}" \
HTTP_ADDR="127.0.0.1:${SERVICE_PORT}" \
CRAWLER_ALLOW_PRIVATE_HOSTS=true \
CRAWLER_CONCURRENCY=4 \
  "$WORK/crawld" >"$WORK/crawld.log" 2>&1 &
SERVICE_PID=$!

wait_for() {
  local url=$1 tries=${2:-60}
  for _ in $(seq 1 "$tries"); do
    curl -sf -m 2 -o /dev/null "$url" 2>/dev/null && return 0
    # Our own service must not have died while we waited.
    kill -0 "$SERVICE_PID" 2>/dev/null || return 1
    sleep 0.5
  done
  return 1
}

wait_for "http://127.0.0.1:${TARGET_PORT}/robots.txt"  || { cat "$WORK/target.log" >&2; fail "target site never came up"; }
wait_for "http://127.0.0.1:${SERVICE_PORT}/healthz"     || { cat "$WORK/crawld.log" >&2; fail "crawld never became healthy"; }

API="http://127.0.0.1:${SERVICE_PORT}"
SITE="http://127.0.0.1:${TARGET_PORT}"

# jget <json> <key-path> — a tolerant field reader, so a failure prints something
# useful rather than a parse error.
jget() { python3 -c 'import json,sys;d=json.loads(sys.stdin.read() or "{}")
k=sys.argv[1]
for p in k.split("."):
    d = d.get(p) if isinstance(d, dict) else None
print("" if d is None else d)' "$1"; }

# ─── Assertions ──────────────────────────────────────────────────────────────
echo
echo "== probes =="
pass "healthz $(curl -s "${API}/healthz" | jget status)"
pass "version $(curl -s "${API}/version" | jget version)"

echo
echo "== input validation =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${API}/v1/crawl" \
  -H 'content-type: application/json' -d '{"seeds":["file:///etc/passwd"]}')
[ "$code" = 400 ] && pass "rejects file:// seeds ($code)" || fail "file:// seed returned $code, want 400"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${API}/v1/crawl" \
  -H 'content-type: application/json' -d '{}')
[ "$code" = 400 ] && pass "requires seeds ($code)" || fail "empty crawl returned $code, want 400"

echo
echo "== cold crawl =="
COLD=$(curl -s -X POST "${API}/v1/crawl" -H 'content-type: application/json' -d "{
  \"seeds\": [\"${SITE}/\"],
  \"sitemaps\": [\"${SITE}/sitemap.xml\"],
  \"follow_links\": true,
  \"max_pages\": 10
}")
fetched=$(printf '%s' "$COLD" | jget fetched)
notmod=$(printf '%s' "$COLD" | jget not_modified)
filtered=$(printf '%s' "$COLD" | jget filtered)
bytes=$(printf '%s' "$COLD" | jget bytes_retained)
runid=$(printf '%s' "$COLD" | jget run_id)

[ -n "$runid" ] || fail "no run id returned: $COLD"
[ "$fetched" -ge 2 ] 2>/dev/null || fail "fetched=$fetched, want at least 2: $COLD"
[ "$notmod" = 0 ]    || fail "a cold crawl reported $notmod not-modified responses"
[ "$filtered" -ge 1 ] 2>/dev/null || fail "filtered=$filtered, want the PNG filtered out: $COLD"
[ "$bytes" -gt 0 ] 2>/dev/null    || fail "bytes_retained=$bytes, want the stored bodies counted: $COLD"
pass "fetched=$fetched filtered=$filtered bytes_retained=$bytes run=$runid"

echo
echo "== warm crawl must revalidate, not re-download =="
WARM=$(curl -s -X POST "${API}/v1/crawl" -H 'content-type: application/json' -d "{
  \"seeds\": [\"${SITE}/\"],
  \"follow_links\": true,
  \"max_pages\": 10
}")
wf=$(printf '%s' "$WARM" | jget fetched)
wn=$(printf '%s' "$WARM" | jget not_modified)
wb=$(printf '%s' "$WARM" | jget bytes_retained)
[ "$wn" -ge 1 ] 2>/dev/null || fail "warm crawl got $wn 304s, want at least 1: $WARM"
[ "$wb" = 0 ] 2>/dev/null   || fail "warm crawl re-downloaded $wb bytes, want 0: $WARM"
pass "not_modified=$wn bytes_retained=$wb (fetched=$wf)"

echo
echo "== stored document =="
DOC=$(curl -s "${API}/v1/documents?url=$(python3 -c 'import sys,urllib.parse;print(urllib.parse.quote(sys.argv[1],safe=""))' "${SITE}/about")")
docid=$(printf '%s' "$DOC" | jget id)
hash=$(printf '%s' "$DOC" | jget content_hash)
[ -n "$docid" ] || fail "document not found by url: $DOC"
[ -n "$hash" ]  || fail "stored document has no content hash: $DOC"
pass "by url -> id=$docid hash=${hash%%:*}:..."

code=$(curl -s -o /dev/null -w '%{http_code}' "${API}/v1/documents/${docid}")
[ "$code" = 200 ] && pass "by id ($code)" || fail "Get by id returned $code, want 200"

code=$(curl -s -o /dev/null -w '%{http_code}' "${API}/v1/documents/does-not-exist")
[ "$code" = 404 ] && pass "unknown id -> 404" || fail "unknown id returned $code, want 404"

echo
echo "== robots =="
ROBOTS=$(curl -s "${API}/v1/hosts/127.0.0.1:${TARGET_PORT}/robots")
delay=$(printf '%s' "$ROBOTS" | jget crawl_delay_ms)
[ "$delay" = 1000 ] || fail "crawl delay reported as ${delay}ms, want 1000: $ROBOTS"
pass "crawl_delay_ms=$delay"

echo
echo "== stats =="
STATS=$(curl -s "${API}/v1/stats?run_id=${runid}")
depth=$(printf '%s' "$STATS" | jget frontier_depth)
[ "$depth" = 0 ] || fail "frontier_depth=$depth, want 0 after the run drained"
pass "frontier drained (depth=0)"

echo
printf '\033[32mPASS\033[0m crawld smoke test\n'
