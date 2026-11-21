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

Use `m<number>-<short-topic>`, for example `m0-foundation`.

## Tests

Ship a unit test with the behavior. Ship an integration test when a request crosses a boundary. Ship a Playwright test when a user-visible web flow changes. Add a native device test when web cannot prove the flow.

Run the gates in `docs/DEPLOYMENT.md` before handing a slice over.

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
git checkout -b m0-foundation
git add -A
git commit -m "$(cat <<'EOF'
Describe why this change exists.

EOF
)"
git status
```

Push and `gh pr create` stay a separate choice. Use the pull request body from the handover. Do not add `--no-verify`.

## Design changes

Update the living doc that the decision affects, and add an ADR when the decision is hard to reverse. ADRs use context, decision, alternatives, and consequences. Release notes and tradeoffs go in `CHANGELOG.md`.

## Source style

No comments in source, tests, or config. No TypeScript `any`. No Go `any` where a concrete type fits. Names describe the result.
