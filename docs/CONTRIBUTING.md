# Contributing

Frontal is built one milestone at a time. The sequence is `docs/MILESTONES.md`. Do not start a later milestone inside an earlier branch.

## Lifecycle

1. Problem
2. Requirements
3. Technical design
4. GitHub issue
5. Implementation branch
6. Tests
7. Pull request
8. Review
9. Merge
10. Release

## Branch

Use `m<number>-<short-topic>`, for example `m1-splash`.

Stage only the files that belong in that pull request. Do not use `git add -A` when a milestone is split. For `apps/mobile`, never run `git add apps/mobile`. Commit `apps/mobile/.gitignore` first, then run `scripts/stage-mobile.sh` and review `git status` before you commit.

## Tests

Ship a unit test with the behavior. Ship an integration test when a request crosses a boundary. Ship a Playwright test when a user-visible web flow changes. Add a native device test when web cannot prove the flow.

Run the gates in `docs/DEPLOYMENT.md` before handing a slice over.

## Pull request size

A pull request’s counted diff is at most 120 lines (insertions plus deletions against the merge base). Exclude `package-lock.json`, `go.sum`, and `dist/`. Tests and docs count. If a slice would exceed 120 lines, split it. Do not omit tests to fit.

```bash
git diff --numstat origin/main...HEAD \
  ':(exclude)**/package-lock.json' \
  ':(exclude)**/go.sum' \
  ':(exclude)**/dist/**'
```

## Pull request

The description includes:

- Problem being solved
- Technical implementation
- Architectural implications
- Testing performed
- Screenshots or recordings when the UI changes
- Breaking changes, or a statement that there are none

## Agent handover

An agent does not run `git commit`, `git push`, or `gh pr create`. It leaves:

- The list of files changed
- The pull request description
- Bash the maintainer pastes

```bash
git checkout main
git pull
git checkout -b m1-splash
./scripts/stage-mobile.sh
git add docs e2e CHANGELOG.md README.md
git status
git commit -m "$(cat <<'EOF'
Describe why this change exists.

EOF
)"
```

Push and `gh pr create` stay a separate choice. Use the pull request body from the handover. Do not add `--no-verify`.

## Design changes

Update the living doc that the decision affects, and add an ADR when the decision is hard to reverse. ADRs use context, decision, alternatives, and consequences. Release notes and tradeoffs go in `CHANGELOG.md`.

## Source style

No comments in source, tests, or config. No TypeScript `any`. No Go `any` where a concrete type fits. Names describe the result.
