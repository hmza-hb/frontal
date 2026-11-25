#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
mobile="$root/apps/mobile"
cd "$root"
git add "$mobile/.gitignore"
git add \
  "$mobile/package.json" \
  "$mobile/package-lock.json" \
  "$mobile/app.json" \
  "$mobile/index.ts" \
  "$mobile/App.tsx" \
  "$mobile/App.test.tsx" \
  "$mobile/tsconfig.json" \
  "$mobile/eslint.config.js" \
  "$mobile/eslint/no-comments.js" \
  "$mobile/AGENTS.md" \
  "$mobile/assets" \
  "$mobile/src"
if [[ -f "$mobile/.env.example" ]]; then
  git add "$mobile/.env.example"
fi
if git status --porcelain | rg -q 'node_modules|/dist/|\.expo'; then
  echo "Refusing to stage: node_modules, dist, or .expo is still staged." >&2
  exit 1
fi
