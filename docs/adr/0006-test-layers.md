# ADR 0006: Test layers

Date: 2026-11-22
Status: accepted

## Context

The project requires unit, integration, and end-to-end tests with behavior changes. Native device farms are not available for the foundation. On-device inference and SQLCipher still need real platform proof later.

## Decision

- Unit tests cover pure behavior.
- Integration tests cross one boundary, such as the HTTP handler or a temporary database.
- Playwright against the exported Expo web app covers user-visible web flows.
- A native device test is added when web cannot prove the flow.
- SQLCipher and on-device runtimes are spikes with their own tests. They are not treated as done because a port compiled on one laptop.
- Tests use temporary databases and fake providers. They do not call a paid model or a production Supabase project.

## Alternatives

- Device-farm end-to-end tests on every pull request from the first commit. Rejected because the farm does not exist and would block the discipline work.
- Snapshot-only UI checks. Rejected because they do not prove a send, a consent, or a health payload.
- Skipping end-to-end until the chat screen. Rejected because the placeholder is already a served page and the harness should exist before the first real screen.

## Consequences

CI can stay on hosted runners. A green web suite does not prove an iOS Foundation Model or an Android AICore call. Those milestones add the platform test they need.
