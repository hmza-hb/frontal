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

Use `m<number>-<short-topic>`, for example `m0-discipline`.

A milestone may ship as several pull requests that each merge to the default branch. Every PR branches from the default branch after the previous one in that milestone is merged. Do not stack long-lived feature branches unless a file in the later PR truly depends on an unmerged commit.

Stage only the files that belong in that pull request. Do not use `git add -A` when a milestone is split.

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
git checkout main
git pull
git checkout -b m0-discipline
git add .cursor/rules/engineering-discipline.mdc AGENTS.md
git commit -m "$(cat <<'EOF'
Describe why this change exists.

EOF
)"
git status
```

Push and `gh pr create` stay a separate choice. Use the pull request body from the handover. Do not add `--no-verify`.

## Splitting a milestone

When one milestone touches unrelated surfaces, split it into reviewable pull requests. Each pull request merges to the default branch before the next one in the sequence branches. The full merge order for M0 is in `docs/MILESTONES.md`.

Handover from an agent lists one pull request at a time: branch name, explicit `git add` paths, description, and bash. The maintainer merges, updates the default branch, then opens the next pull request.

## Design changes

Update the living doc that the decision affects, and add an ADR when the decision is hard to reverse. ADRs use context, decision, alternatives, and consequences. Release notes and tradeoffs go in `CHANGELOG.md`.

## Source style

No comments in source, tests, or config. No TypeScript `any`. No Go `any` where a concrete type fits. Names describe the result.
