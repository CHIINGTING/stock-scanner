# 專案功能需求與進度總表

> 產生日期：2026-08-09
> 依據：git 歷史、`docs/SPEC_*.md`、`configs/config.yaml` 的**實際開關值**、以及目前工作區狀態。
> 這份是「走到哪」的快照，不是設計文件。架構看 `docs/SCANNER_MASTER_DESIGN.md`，路線圖看 `docs/SCANNER_ENHANCEMENT_PLAN.md`。

---

## 0. 專案定位（沒變過的三條紅線）

Stock Radar 是**盤後（EOD）波段選股助理**，持有週期 1–4 週，主要觀察窗 **20 個交易日**。
核心流程：`Rotation → Sector → Stock → Watchlist → Position`。

貫穿所有功能的三條紀律：

1. **Shadow-first**：新訊號一律先做成影子層（算、存、顯示），預設關閉，`enable_*` / `show_*` 兩層開關。
2. **不改分數**：影子層不得影響 `Score` / `Action` / `RocketScore` / `WatchAction` / 排序 / 停損。
3. **不自動下單**：全案沒有券商介接；`BUY` / `AUTO_BUY` / `PLACE_ORDER` 這類字串在回測產出裡由測試禁止出現。

---

## 1. 圖例

| 記號 | 意思 |
| --- | --- |
| ✅ | 已完成並 commit，功能在線上跑 |
| 🧪 | 已完成並 commit，但**仍在觀察模式**（開關暫開／結論未採用） |
| 🟡 | 已實作、測試綠，**尚未 commit** |
| 📝 | 只有設計文件，沒有程式碼 |
| ⛔ | 已作廢／被取代 |

---

## 2. 功能軌總表

| # | 功能軌 | 想解決的問題 | 進度 | 目前開關 | 下一步 |
| --- | --- | --- | --- | --- | --- |
| 0 | **基礎掃描 / 持倉 / 飆股候選** | 這支能買嗎？該續抱還停損？ | ✅ | 常開 | — |
| 1 | **族群輪動 Rotation** | 下一波資金去哪裡？（三層：1–5d 短線流向 / 20d 中期 / 60d 趨勢） | ✅ | 常開 | 成員代號需人工複查 |
| 2 | **R1 相對強度 RS Rank** | 全市場排名取代原本粗糙的 g2 相對強度 | 🧪 | `enable_rs_rank: true`（註記「觀察模式暫開」） | — |
| 3 | **R2 新高 + VCP** | 領導股 gate、整理型態品質 | 🧪 | `enable_new_high` / `enable_vcp: true` | — |
| 4 | **R3 MomentumFlow** | 動能與階段的聯合判斷表 | 🧪 | `enable_momentum_flow: true` | — |
| 5 | **R4 多週期 MTF** | 日＋週對齊、200 日長線濾網 | 🧪 | `enable_multi_timeframe` / 風險提示 / 排序 tie-break 皆 true | — |
| 6 | **C6b Guardrail Scoring** | 上面四個訊號要不要真的動分數 | ⚠️ 見 §5 | `enable_signal_guardrail_scoring: true`（**已 commit**） | 確認 fresh-data A/B 是否已補做 |
| 7 | **R5 參數校準** | 訊號門檻不能照抄美股 | ✅ | — | R5-3 benchmark 文件已在，可續跑 |
| 8 | **R6 拉回回測框架** | 回檔進場到底行不行？停損policy哪個好？ | ✅ 見 §4 | 離線工具，不影響線上 | **cross-regime 驗證仍未做（主線阻塞）** |
| 9 | **R6-7 持有週期提示** | 報告上標「這型態該看多久」 | 🧪 | `show_horizon_hint: true`（註記預設 false） | 門檻仍是 v1 常數，待校準 |
| 10 | **R7-1 HoldingHorizon** | 另一套 stage+ATR 的持有週期 | 🧪 影子層 | 未接進報告 | 與 R6-7 的關係尚未決定 |
| 11 | **R8 ETF 資金流** | ETF 成分股增減持＝法人態度 | 🧪 | `enable_etf_flow` / `show_etf_flow: true`（註記「勿 commit」但**已 commit**） | 見 §5 |
| 12 | **R9 市場 Regime 層** | 空頭時不要用多頭邏輯解讀 | ⛔ 被 R10 取代 | — | 只留 §9 的 regime × setup 對照表 |
| 13 | **R10 市場儀表板** | regime 的單一歸屬地（5 種 regime） | 🟡 只有 mock provider | `cmd/market-dump` 離線 demo | 接真實資料源 + News/Sector 重用（見 SPEC §B-3） |
| 14 | **R10-1 法人籌碼 + 乖離風險** | 三大法人買賣超、追價/超跌風險 | 🟡 | `enable_institution` / `enable_bias: false` | commit + 開觀察模式 |
| 15 | **R10-2 K 線型態** | K 線型態辨識（影子層） | 🟡 | `enable_candlestick` / `show_candlestick` 未列入 config（＝false） | commit + 把開關寫進 config.yaml |
| 16 | **消息面 News（Phase 1）** | 股癌／新聞的情緒影子層 | 🧪 | `enable_news` / `show_news: true`（註記預設 false） | Phase 2 才允許影響分數 |
| 17 | **訊號驗證器** | 我過去的判斷到底準不準？ | ✅ | 獨立 CLI，不碰 scanner | trailing-stop positionAdvice（C）未做 |
| 18 | **分析歷史 sidecar** | 結構化歷史，不再靠解析 HTML | 🟡 | — | commit |
| 19 | **訊號共振 signal_confluence** | 多個影子層同向時的共振 | 🟡 | — | commit |
| 20 | **區間策略回測面板** | 「當初這樣買賣會怎樣」的互動回測 | 🟡 | 掛在報告「🔬 回測洞察」分頁 | commit（含 `backtest.html`） |

---

## 3. 主線故事：從「找股票」到「驗證自己」

三個階段，時間順序大致如此：

1. **建訊號**（R1→R5）：把選股從單一技術指標，換成 RS／新高／VCP／動能／多週期五個影子層，逐一校準門檻。
2. **驗訊號**（R6、訊號驗證器）：不再問「指標好不好看」，改問「這規則過去賺不賺」。R6 是離線回測框架，訊號驗證器是回頭驗證自己發過的報告。
3. **補脈絡**（R8→R10、消息面）：股票以外的資訊——ETF 資金流、三大法人籌碼、市場 regime、消息面——全部以影子層加入，先看再說。

**目前這一輪**（2026-07 起）同時開了三條沒收尾的線：R10 系列（市場儀表板／法人／K 線）、分析歷史 sidecar，以及這次新做的區間回測面板。共通點是**都還沒 commit**。

---

## 4. R6 回測：做完了什麼、卡在哪

這是投入最深的一軌，也是**唯一有明確阻塞**的一軌。

| 階段 | 內容 | 狀態 |
| --- | --- | --- |
| R6-2a | 框架：Universe loader、as-of RS panel、引擎、CSV/markdown schema | ✅ |
| R6-2b | Setup A（MA20/MA60 拉回）、Setup B（5–20% 深回檔），停損調整後報酬 | ✅ |
| R6-2c | Setup C 真 VCP 回測 | ✅ |
| R6-2d | Setup D 崩盤倖存者**個案研究**（event_count=3，永遠標 LOW confidence） | ✅ |
| R6-3 | 停損政策 benchmark + 判讀文件 | ✅ |
| R6-4 | 停損 profile 提案（**只有文件，未採用**） | 📝 |
| R6-5 | fresh-cache 可重現性重跑 → **結果穩定，無需暫停** | ✅ |
| R6-6 | 近期多頭 regime 驗證（20d 為主） | ✅ |
| **cross-regime** | **真正的跨環境（空頭／崩盤）驗證** | ❌ **未做** |

**主要結論**：`A_MA20 > A_MA60`；深回檔 15–20% 抱著較強但訊號太少太慢；`C_VCP_MA20` 在近期多頭**並沒有贏過單純 `A_MA20`**（與全期結論分歧）；現行 BASELINE 停損**過嚴**（stop_hit 77–93%），`ATR_3` 是目前最穩健候選、`PCT_15` 是簡化備案、`NO_STOP` 帳面最高但尾部回撤最深。

**為什麼還不改線上停損**：資料窗 2024-06→2026-06 是多頭／復甦偏誤，結構上就偏袒寬停損，不可外推。**cross-regime 驗證是採用任何停損 profile 的前置條件，而它還沒做。** 在那之前 BASELINE 不動。

---

## 5. 需要你確認的三處漂移

寫這份表時對照 git 與設定檔，發現三個對不上的地方：

1. **`enable_signal_guardrail_scoring` 已 commit 為 `true`。**
   原本的紀律是「A 模式（false）為每日觀察，B 模式（true）只是對照組，**在 fresh full-market 資料重驗前不得成為 committed 預設**」。現在 committed 值是 true。要嘛 fresh-data A/B 已經補做過（那紀律紀錄該更新），要嘛是不小心提交的。

2. ~~**ETF flow 開關註解寫「本機驗證暫開（勿 commit）」，但已 commit 為 `true`。**~~
   **暫緩** — 2026-08-09 已決定先不處理，維持現狀。

3. ~~**`docs/SPEC_R10_MARKET_DASHBOARD.md` 不存在。**~~
   **已補** — 2026-08-09 由使用者提供需求原文，歸檔為 `docs/SPEC_R10_MARKET_DASHBOARD.md`，
   並附實作對照表。仍待決定的四點列在該文件 §B-3（Confidence 分級 vs 百分比、News Shadow 尚未引用、
   Sector Cycle 尚未接現有族群資料、Regime 門檻未校準）。

---

## 6. 未提交清單（工作區現況）

**新增（untracked）**

| 路徑 | 屬於 |
| --- | --- |
| `internal/institution/`、`cmd/institution-fetch/`、`internal/indicator/bias.go`、`internal/scanner/{institution,bias}_attach.go` | R10-1 法人籌碼 + 乖離風險 |
| `internal/candlestick/`、`internal/scanner/candlestick_attach.go`、`internal/report/report_candlestick_test.go` | R10-2 K 線型態 |
| `internal/market/`、`cmd/market-dump/`、`data/market/` | R10 市場儀表板（mock provider） |
| `internal/analysishistory/`、`internal/validator/{sidecar,cohort}.go` | 分析歷史 sidecar |
| `internal/scanner/signal_confluence.go` | 訊號共振 |
| `internal/segbacktest/`、`cmd/backtest-panel/`、`backtest.html` | 區間策略回測面板 |

**已修改（modified）**：`internal/report/report.go`、`internal/scanner/scanner.go`、`internal/validator/{price,report}.go`、`configs/config.yaml`、`configs/sectors.yaml`、`stocks.yaml`、`README.md`、`Makefile`、`index.html`、`docs/SPEC_R9_1_*.md`

`go build` / `go vet` / `go test ./...` 全綠。

---

## 7. 建議的收尾順序

1. **先收乾淨**：把 R10-1 / R10-2 / sidecar / 共振 / 回測面板分批 commit（各自獨立，互不相依），讓工作區回到乾淨狀態。
2. **處理 §5 三處漂移**：特別是 guardrail scoring 的 committed 預設值，那個直接影響每日排名。
3. **補 R10 設計文件**，或明確標記市場儀表板為暫緩。
4. **主線只剩一件事**：R6 的 cross-regime 驗證。做完才談停損 profile 採用；沒做完就別動 BASELINE。
