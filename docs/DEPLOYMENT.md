# Deployment

Status: local toolchain and CI. Production hosting is not decided.

## What runs today

Local chat does not need `services/api`. The API is an optional process. Version 0.0.0 has a launch screen and no chat yet. The gates below prove the toolchain.

## Client

From `apps/mobile`:

```bash
npm install
npm run lint
npm run typecheck
npm test
npm run export:web
```

`npm start` opens the Expo dev server for Android, iOS, and web.

## API

From `services/api`:

```bash
go test ./...
golangci-lint run
go run ./cmd/api
```

The process listens on `:8080` unless `ADDR` is set. `GET /v1/health` returns `{"status":"ok"}`.

## End to end

From the repository root, after the web export:

```bash
npm install --prefix e2e
npm test --prefix e2e
```

Playwright serves `apps/mobile/dist` and asserts the launch sequence reaches the auth destination.

## Migration filenames

```bash
node --test scripts/migrations.test.mjs
```

SQL files in `supabase/migrations` use the name `YYYYMMDDHHMMSS_short_name.sql`. The directory is empty at this version.

## Local Supabase

When Docker is available and the Supabase CLI is installed:

```bash
supabase db reset
```

Run that from the repository root after `supabase start`. It applies the forward-only migrations to a local Postgres. There is nothing to apply yet. If Docker is not available, the filename check above is the local gate.

CI runs the migration filename test only. A job that applies SQL in Postgres starts once real migrations exist and the workflow can start the local Supabase stack reliably.

## CI

`.github/workflows/ci.yml` runs the client lint, typecheck, and unit test; the Go test and `golangci-lint`; the OpenAPI contract test inside `go test`; the migration filename test; and the Playwright smoke test against the exported web app.

## Not decided yet

- Where the optional API and Postgres run in production.
- How client builds are submitted to app stores.
- How secrets for a managed frontier provider would be issued, if that opt-in is ever approved.
