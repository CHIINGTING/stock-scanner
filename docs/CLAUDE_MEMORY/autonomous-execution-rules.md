---
name: autonomous-execution-rules
description: "Standing rule — within an explicitly assigned task scope, create/modify/test/commit safe files without asking each time"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 1ec3ee9c-5e37-48bf-9b80-46739fad0317
---

In an explicitly assigned task scope and within safety bounds, I may autonomously create/modify/test/commit without asking per-file.

**Why:** User wants velocity on safe, testable, reversible engineering work; per-file confirmation is friction.

**How to apply:**
- May auto-create within scope: `*.go`, `*_test.go`, `cmd/<tool>/main.go`, `internal/<pkg>/*.go`, testdata fixtures, mocks/parsers/helpers/adapters/interfaces, safety guardrails, e2e tests, `docs/*.md`, README/runbook, `.gitkeep`.
- May auto-run: gofmt, `go test ./...`, `go vet ./...`, `go build ./...`, git add, git commit. Run full suite before final commit even if changes are package-local.
- Commit discipline: atomic; only this-stage allowed files; before EVERY commit run `git status --short`, `git diff --cached --name-only`, `git diff --cached --stat`. If staged files include anything not belonging to this stage, STOP and fix — never force-commit. Never mix pre-existing dirty files.
- NEVER auto-commit: `configs/config.yaml` local toggles, `index.html`, `data/*.csv`, `data/etf_holdings/*.json`, `data/etf_holdings/raw/*.csv`, `.cache*`, `fetch_00981a_official.py`, off-task `scripts/*`, any real data files. If already dirty, treat as pre-existing and leave untouched.
- docs/ is gitignored: use `git add -f <docs file>` for the specific doc; do NOT edit .gitignore unless task explicitly requires.
- For source-discovery/risk/data-classification/blocked work, auto-update `docs/R8_AUTONOMOUS_EXECUTION_LOG.md` or create `docs/SPEC_*.md`.

**HARD STOP (must pause & report BLOCKED, log to MD):** git push; deleting many files; changing scoring/sorting/RocketScore/WatchAction/ExplosionProb; broker integration; auto-ordering; login/captcha/private API; Playwright/Selenium/browser automation; high-freq crawler; bypassing site limits; treating partial/quarterly data as full holdings; committing local config / index.html / real CSV/JSON data.

**Final report format each autonomous segment:** 1) what done 2) files added/modified 3) commit hash 4) test results 5) any hard stop hit 6) any pre-existing dirty files left untouched 7) next-step suggestion.

Related: [[scanner-ab-guardrail-status.md]] (never commit B default), [[r6-backtest-status.md]] (stop profile defaults unchanged).
