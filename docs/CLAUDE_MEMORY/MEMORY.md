# Memory Index

- [Scanner direction](scanner-direction.md) — repositioned for 1–4 week swing trades; core flow Rotation→Sector→Stock→Watchlist→Position
- [Scanner master design](scanner-master-design.md) — canonical architecture doc at docs/SCANNER_MASTER_DESIGN.md; read first, keep updated
- [Scanner enhancement plan](scanner-enhancement-plan.md) — design-only roadmap at docs/SCANNER_ENHANCEMENT_PLAN.md; FinLab/TradingView/Hades concepts
- [Guardrail A/B status](scanner-ab-guardrail-status.md) — A=daily(scoring off) / B=control(scoring on); B validated on cache only, never commit B default until fresh-data rerun
- [R6 backtest status](r6-backtest-status.md) — pullback backtest in internal/r6backtest; DOCS PHASE DONE + R6-5 fresh-cache reproducibility rerun DONE & STABLE (commit c6c0f45, no pause); baseline over-stops, ATR_3 top candidate; next = TRUE cross-regime validation (NOT done), stop profile defaults unchanged
- [Signal validator](signal-validator.md) — cmd/validate-signals + internal/validator; market tab = universe scan (default BUY-only), forward prices bounded by .cache extent so recent windows go PENDING
- [Autonomous execution rules](autonomous-execution-rules.md) — within an assigned task scope I may create/test/commit safe files without per-file asking; hard stops + commit discipline + report format
- [News module](news-module.md) — Phase-1 shadow news layer in internal/news; SWD=WP REST API (primary), TWETQ=Cloudflare/JS best-effort; enable_news/show_news default off; snapshot keeps PublishedAt≠ObservedAt
- [Segment backtest panel](segment-backtest-panel.md) — internal/segbacktest + cmd/backtest-panel → backtest.html; JS engine tested via jsc; now the sole content of the report's 回測洞察 tab (lazy iframe); reference site is copyrighted, don't copy it
