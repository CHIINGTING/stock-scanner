---
name: news-module
description: "Phase-1 shadow news/sentiment module — internal/news, external source facts, gating"
metadata: 
  node_type: memory
  type: project
  originSessionId: bcd1944c-6a53-43ac-ab97-0659a3e2ad20
---

消息面 (news/sentiment) module, Phase 1 = SHADOW MODE (display/record/store only; never
changes RocketScore/WatchAction/ExplosionProb/Action/sort). Design doc:
docs/SPEC_R9_1... no — see /Users/deep.huang/.claude/plans/sequential-crafting-lark.md
(approved plan). Mirrors the R8 ETF-flow shadow pattern: dedicated `WatchlistEntry.News
*news.StockNewsView`, post-pass `scanner.AttachNews` (scanner imports news, not vice-versa),
two-layer `enable_news`/`show_news` (both default false → byte-identical output), report ⑩
per-card section + market news banner behind `GuardrailViewOptions.ShowNews`.

**External source facts (saved to avoid re-research):**
- SocialWorkerDaily = PRIMARY, easy. WordPress REST API `GET /wp-json/wp/v2/posts?search=股癌筆記`
  → JSON with `date_gmt` (reliable), `link` (`notes-of-gooaye-ep-NNN`), `title.rendered`
  (`股癌筆記EP677`), `content.rendered`. RSS `/feed/` is fallback (mixed cats, filter by title/slug).
  Category slug `notes-of-gooaye` returns EMPTY — must discover by title-search, not category id.
- TWETQ (twetq.com) = Taiwan stock QUANT tool, NOT a 股癌 blog. Cloudflare: default UA → 403,
  browser UA via net/http → 200 (no headless needed). `/stock/<code>` server-rendered quant data
  (name+code in title/og/JSON-LD, no directional opinion). `/podcast` = JS-rendered shell, NO
  server-side episodes → best-effort provider degrades to empty (graceful, isolated failure).

Stack: stdlib + yaml.v3 only (no x/net) — HTML parse via regexp, feeds via encoding/xml.
Dedup key = normalized episode `gooaye-ep-N` (cross-source merge, no double-count), fingerprint
fallback. Classifier = keyword lexicon (NO LLM) with negation + compound (突破失敗) + support-
softening (修正…仍在季線之上→RISK_WARNING); splits on 。！？；\n NOT comma. Post-review fixes:
market banner groups by SignalType NOT polarity (BULLISH≠ROTATION_IN); polarity() only for
divergence/conflict; BuildSignals classifies EVERY source (not longest) + marks Conflict on
opposing same-entity signals. Snapshot = `reports/news_YYYYMMDD_HHMMSS.json` PER RUN (no
intraday overwrite; uniquePath guards same-second); ObservedAt = wall-clock time.Now(), and
main SKIPS live fetch when --date != today (news.SameDay) to avoid look-ahead. Snapshot signal
keeps raw_title + content_excerpt + evidence (MED-6). Alias/theme dict = configs/news_aliases.yaml.
Known-limited (TODO, not done): provider status enum (MED-7), fingerprint hardening (MED-8),
long-sentence over-attribution (被動元件 can read BULLISH from a broad-strength sentence).
All Phase A–D + review fixes implemented + tested; NOT yet committed (user reviews before commit).
See [[autonomous-execution-rules]] [[signal-validator]].
