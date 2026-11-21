# Frontal

Frontal is a local-first conversational and agentic app. The device holds the chats. Auto mode uses a frontier model after cloud-inference consent. Private mode stays on the device. Sync is a separate, optional backup.

Version 0.0.0 is the foundation: rules, docs, a placeholder client, and a health check. There is no chat yet. Local mode does not need the API.

The milestone order is [docs/MILESTONES.md](docs/MILESTONES.md). Design and tradeoffs live in [docs](docs) and [docs/adr](docs/adr).

## Gates

Client, from `apps/mobile`:

```bash
npm install
npm run lint
npm run typecheck
npm test
```

API, from `services/api`:

```bash
go test ./...
golangci-lint run
```

Migrations, from the repository root:

```bash
node --test scripts/migrations.test.mjs
```

When Docker and the Supabase CLI are installed, `supabase db reset` applies the local migrations. See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md).

Web smoke test:

```bash
npm run export:web --prefix apps/mobile
npm install --prefix e2e
npm test --prefix e2e
```

## License

Apache-2.0. See [LICENSE](LICENSE).
