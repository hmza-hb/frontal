# ADR 0001: Stack and monorepo

Date: 2026-11-22
Status: accepted

## Context

Frontal needs one client for Android, iOS, and web, an optional cloud API, and a place for versioned contracts. Desktop is planned and not required for the first milestones. The repository was empty apart from a license.

## Decision

Keep one Git repository.

- `apps/mobile` is an Expo app for Android, iOS, and web.
- `services/api` is a Go process for optional cloud features.
- `contracts/openapi` holds the versioned HTTP contract.
- `supabase/migrations` holds the optional Postgres schema.
- The Go process and Postgres are not required to run a local session.

## Alternatives

- Separate repositories for the client and the API. Rejected because the contract and the milestone sequence would drift.
- A Node API next to the Expo app. Rejected because the team committed to Go for the cloud process.
- Starting with desktop packaging. Rejected until an ADR says the Expo app cannot cover that surface.

## Consequences

Contributors clone one repo and can run the client without the API. Production topology for the API and Postgres is still open. Web local inference is not implied by the web target of the Expo app.
