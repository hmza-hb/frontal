# Agent instructions

Frontal’s discipline lives in `.cursor/rules/`. Read the always-on rules before changing the repo.

- `engineering-discipline.mdc` — no comments, no `any`, one milestone at a time, no commits or pull requests from the agent.
- `local-first.mdc` — device is the system of record; Auto / Best quality / Private; inference consent is separate from sync.
- `delivery-lifecycle.mdc` — issue, branch, tests, and the required pull request sections.
- `documentation.mdc` — the six engineering docs, ADRs, and `CHANGELOG.md`.
- `testing.mdc` — unit, integration, and end-to-end coverage.

Language rules apply by path: TypeScript, Go, HTTP contracts, Supabase, and chat UI.

End a slice with the files changed, a pull request description, and bash the maintainer runs. Do not run `git commit`, `git push`, or `gh pr create`.
