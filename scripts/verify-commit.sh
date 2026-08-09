#!/usr/bin/env bash
# Verify that an EXACT commit builds and passes on its own, in a throwaway worktree.
#
# The working tree lies. Untracked or later-stage files silently satisfy dependencies, so a
# green `go test` in the working tree says nothing about what was actually committed — this
# bit us once already (a committed test referenced a symbol that only existed in an
# untracked file). The committed artifact is the thing that has to pass.
#
#   scripts/verify-commit.sh [commit-ish]     # defaults to HEAD
set -uo pipefail

COMMIT="${1:-HEAD}"
REPO_ROOT="$(git rev-parse --show-toplevel)"
SHA="$(git -C "$REPO_ROOT" rev-parse --short "$COMMIT")"
SUBJECT="$(git -C "$REPO_ROOT" log --format=%s -1 "$SHA")"
WT="$(mktemp -d)/wt-$SHA"

cleanup() { git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "── verifying $SHA — $SUBJECT"
git -C "$REPO_ROOT" worktree add -q --detach "$WT" "$SHA" || { echo "  worktree add FAILED"; exit 1; }

status=0
run() { # run <label> <cmd...>
  local label="$1"; shift
  local out
  if out="$("$@" 2>&1)"; then
    echo "  $label OK"
  else
    echo "  $label FAILED"
    echo "$out" | head -25 | sed 's/^/    /'
    status=1
  fi
}

cd "$WT" || exit 1
run "build" go build ./...
run "vet  " go vet ./...
run "test " go test ./... -count=1

# gofmt is a hard gate for the paths this feature owns. It is deliberately NOT run over the
# whole repo: several pre-existing packages (internal/fetcher, internal/r6backtest,
# internal/validator) are already unformatted, and reformatting unrelated user code to make
# a gate go green would be a worse offence than the gate it satisfies.
FMT_PATHS="${VERIFY_FMT_PATHS:-internal/market cmd/market-fetch cmd/market-dump cmd/market-backfill}"
fmt_targets=""
for p in $FMT_PATHS; do [ -e "$p" ] && fmt_targets="$fmt_targets $p"; done
unformatted="$([ -n "$fmt_targets" ] && gofmt -l $fmt_targets 2>/dev/null || true)"
if [ -z "$unformatted" ]; then
  echo "  gofmt OK"
else
  echo "  gofmt FAILED"; echo "$unformatted" | sed 's/^/    /'; status=1
fi

if [ "$status" -eq 0 ]; then
  echo "  ✅ $SHA is green from a clean checkout"
else
  echo "  ❌ $SHA is NOT green from a clean checkout"
fi
exit "$status"
