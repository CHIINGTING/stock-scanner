# R8 Autonomous Execution Log

本檔記錄 R8-6（ETF holdings source discovery + coverage guardrail）的自動執行過程。
每階段追加：時間 / 做了什麼 / 改了哪些檔案 / 測試結果 / 是否 commit / commit hash /
風險限制 / 下一步。

安全邊界（全程遵守）：不交易、不下單、不接 broker、不改 RocketScore / WatchAction /
ExplosionProb / sorting、不把不完整資料當完整持股。

> 注意：本檔被 `.gitignore` 擋住（`*EXECUTION*`/`*LOG*` 類規則），以 `git add -f` 單檔加入。

---

## Phase 0 — 盤點現況（2026-06-24）

**做了什麼**：盤點 repo 既有狀態與 etfflow 程式碼，確認 R8-6a fetcher（MoneyDJ）為**未
commit** 狀態、且現有設計把 MoneyDJ 當完整 snapshot 直接走 Ingest→Save→LoadHistory→flow，
存在「top-10 被當完整持股」污染 R8 flow 的風險。

**盤點重點**：

- `internal/etfflow/`：snapshot.go / loader.go / flow.go / signal.go / ingest.go 已 tracked；
  moneydj.go / holdings_fetcher.go / moneydj_test.go / testdata、`cmd/etfflow-fetch/` 為**未
  tracked**（R8-6a 未 commit）。
- `LoadHistory` 是 R8 flow（`CalculateFlows`）的唯一輸入閘口 → guardrail 應在此落地。
- 既有 dirty（前次 session 殘留，**本輪不動**）：`.gitignore`、`configs/config.yaml`、
  `index.html`、`stocks.yaml`、`data/00981A_TW_holding_20260430.csv`、`fetch_00981a_official.py`、
  `scripts/`、`.agents/`。
- gitignore 驗證：`data/etf_holdings/*.json` 已被忽略；`docs/SPEC_*` 未被忽略；
  `docs/R8_AUTONOMOUS_EXECUTION_LOG.md` **被忽略**（需 `add -f`）。

**測試結果**：未改 code，僅讀取。
**是否 commit**：否。
**風險 / 限制**：MoneyDJ 既有設計會污染 flow（待 Phase 2 修正）。
**下一步**：實作 coverage guardrail。

---

## Phase 1 — Source discovery 文件（SPEC_R8_6B）

**做了什麼**：更新 `docs/SPEC_R8_6B_SOURCE_DISCOVERY.md`：

- 新增 §3.1「Snapshot `coverage_type` 欄位對照（registry → code）」：把 registry 用語
  （`full_daily_holdings`/`partial_top_n`/`quarterly_composition`/`unusable`）對應到 snapshot 的
  `coverage_type` 欄位值（`full_holdings`/`top_n_only`/`quarterly_composition`/`unknown`），並標明
  `IsFlowEligible()` 與 `LoadHistory` 行為。
- 重寫 §5：full daily holdings 來源探查結論（見下方 Phase 3）。
- 重寫 §6：MoneyDJ fetcher 已改造為 top-n-only probe（已落地）。

**分類結論**（不變、釘死）：

- MoneyDJ = `partial_top_n` / 欄位 `top_n_only`，top_n = 10。
- WantGoo = `quarterly_composition`。

**測試結果**：docs only。
**是否 commit**：與 Phase 3 合併為 docs commit（見文末）。
**風險 / 限制**：無。
**下一步**：落地 code guardrail。

---

## Phase 2 — Coverage guardrail code + tests

**時間**：2026-06-24。

**做了什麼**：

1. `snapshot.go`：新增 coverage 常數（`full_holdings`/`top_n_only`/`quarterly_composition`/
   `unknown`）、Snapshot 三個 optional 欄位（`CoverageType`/`TopN`/`CoverageNote`）、
   `IsFlowEligible()`（空字串或 `full_holdings` → true；其餘 fail closed）。
2. `loader.go`：`LoadHistory` 以 `IsFlowEligible()` 過濾，partial/季度/unknown **不進** flow。
3. `moneydj.go`：新增 `MoneyDJ.Coverage()` = `top_n_only` / top_n 10 / note。
4. `holdings_fetcher.go`：`HoldingsParser` 介面加 `Coverage()`；新增 `StatusPartial` 與
   `Coverage` 型別；`Fetch` 多 `allowPartial` 參數，coverage 非 full → PARTIAL，預設不寫，
   `--allow-partial` 才寫 **tagged probe**（仍標 top_n_only、LoadHistory 仍跳過）。
5. `cmd/etfflow-fetch/main.go`：新增 `--allow-partial` flag、coverage 報表欄位、PARTIAL 警告、
   把 status→exit 決策抽成純函式 `statusError`（離線可測）。
6. 測試：`loader_test.go`（top_n_only/quarterly/unknown/未識別值被跳過、full+empty 讀入）、
   `snapshot_test.go`（coverage round-trip + `IsFlowEligible` 表格）、`moneydj_test.go`
   （MoneyDJ PARTIAL 不寫 / allow-partial 寫 probe 但 LoadHistory 跳過；full-coverage 用
   `fakeFullParser` 測 OK/STALE）、`cmd/etfflow-fetch/main_test.go`（`statusError` 各狀態）。

**改了哪些檔案**：

```
internal/etfflow/snapshot.go            (+欄位/常數/IsFlowEligible)
internal/etfflow/snapshot_test.go       (+coverage round-trip / IsFlowEligible)
internal/etfflow/loader.go              (+flow-eligibility 過濾)
internal/etfflow/loader_test.go         (+coverage skip 測試)
internal/etfflow/moneydj.go             (新檔；+Coverage())
internal/etfflow/moneydj_test.go        (新檔；PARTIAL/full 測試)
internal/etfflow/holdings_fetcher.go    (新檔；+StatusPartial/Coverage/allowPartial)
internal/etfflow/testdata/moneydj_00981A_holdings.html (新檔；fixture)
cmd/etfflow-fetch/main.go               (新檔；+--allow-partial/statusError)
cmd/etfflow-fetch/main_test.go          (新檔；statusError 測試)
```

**測試結果**（全過）：

```
go test ./internal/etfflow/... -count=1   → ok
go test ./cmd/etfflow-fetch/... -count=1  → ok
go test ./...                             → 全 ok（report/scanner/fetcher/r6backtest/timeframe…）
go vet ./...                              → 無輸出（pass）
go build ./...                            → pass
```

**是否 commit**：是。
**commit hash**：`360364a` `feat(scanner): add R8-6 ETF source coverage guardrails`。
**風險 / 限制**：

- 既有 R8-6a 的 fetcher 測試行為改變（MoneyDJ 由 OK/STALE → PARTIAL）；已以 `fakeFullParser`
  保留 full-coverage 的 OK/STALE 路徑覆蓋。
- guardrail 為 fail-closed：任何未識別 `coverage_type` 一律不進 flow。
- 未改任何 scoring / sorting / broker；R8 flow 仍只吃手動 full holdings CSV。

**下一步**：full holdings 來源探查結論寫入 SPEC + 收尾 docs commit。

---

## Phase 3 — Full daily holdings 來源探查

**做了什麼**：在不登入 / 不繞過 / 不用私有 API / 不發探測請求加壓的前提下，評估官方完整每日
holdings 來源，結論寫入 `SPEC_R8_6B §5`。

**發現**：

- **統一投信 ezMoney 官方 PCF / 投資組合明細**（fundCode `49YTW`）**內容上**符合
  `full_daily_holdings`：逐日、完整成分、含個股代號 + 持有股數 + 權重 + 資料日期。已有人工匯出
  樣本佐證（`data/00981A_TW_holding_20260430.csv`，欄位 date/ticker/stock_name/weight_pct/shares，
  成分明顯多於 top-10）。
- **但 BLOCKED**：現有取得原型（`fetch_00981a_official.py`、`scripts/fetch_00981a_daily_official.mjs`）
  **皆使用 Playwright（browser automation）**，代表頁面為 JS 渲染 / 需瀏覽器下載觸發。依 hard-stop
  規則「需 browser automation → 停止、記錄 BLOCKED、不繞過」，**不**建置自動 fetcher，**未**對該站
  發出探測請求。

**明文結論**：

```
目前未找到 00981A full_daily_holdings 的「公開、純 HTTP、無 browser automation」自動來源。
MoneyDJ 只能 top_n_only。
WantGoo 只能 quarterly_composition。
ezMoney 官方 PCF 內容為 full_daily_holdings，但唯一可行抓取為 browser automation（被禁），故 BLOCKED。
R8 flow 暫時只能使用人工匯出 full holdings CSV（cmd/etfflow-ingest）。
```

**測試結果**：docs only。
**是否 commit**：是（與 Phase 1 合併 docs commit）。
**commit hash**：見文末（docs commit）。
**風險 / 限制**：ezMoney 自動化需先找到非 browser-automation 的公開取得方式（R8-6c，未動工）。
**下一步**：無強制下一步；建議見文末。

---

## Hard-stop 觸發紀錄

| 條件 | 是否觸發 | 處置 |
|------|----------|------|
| 需登入 / captcha / 私有 API | 否 | — |
| 需 browser automation（full 官方源）| **是**（ezMoney）| 記錄 BLOCKED，不繞過、不建置（§5.2）|
| 需高頻 crawler | 否 | — |
| 需改 scoring / sorting | 否 | — |
| 需改 broker / 下單 | 否 | — |
| 需 commit 既有 dirty config/index/data | 否 | 全程 explicit add，未碰 dirty |

---

## Commit 清單

| hash | 類型 | 訊息 |
|------|------|------|
| `360364a` | code | feat(scanner): add R8-6 ETF source coverage guardrails |
| （docs commit，見下方更新） | docs | docs(scanner): add R8-6 source discovery registry |

> docs commit 之 hash 於本檔提交時補上（見 git log）。

---

## 建議下一步

1. **R8-6c**：找 ezMoney（或 TWSE/集保）**非 browser-automation** 的公開 full holdings 取得方式
   （官方直接下載連結 / 公開 JSON），確認三要件（逐日 + 完整 + 股數）後才允許進 flow。
2. 維持現況：R8 flow 只吃**人工 full holdings CSV**（`cmd/etfflow-ingest`）。
3. WantGoo 若要做，務必標 `quarterly_composition`、只作季度結構 context。
4. 既有 dirty（config 本機 enable、index.html、official CSV、scripts、fetch_00981a）保留未動，
   待使用者決定是否各自處理；**不**併入本線 commit。

---

## Phase R8-6c — Full daily holdings 來源探查 + 官方 adapter（2026-06-24）

**時間**：2026-06-24（autonomous execution）。

**做了什麼**：替 00981A 找 `full_daily_holdings` 的**非 browser-automation 公開來源**，
找到後實作官方 adapter。詳見 [SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md](SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md)。

**來源 URL / 候選評估**：

- 統一投信 ezMoney 官方頁 `https://www.ezmoney.com.tw/ETF/Fund/Info?fundCode=49YTW`
  → **完整每日持股 server-side 內嵌**於 `<div id="DataAsset" data-content="...JSON...">`，
  ST(股票) 類別 `Details` = 全 50 檔成分。**採用**。
- TWSE `zh/ETFortune/etfInfo/00981A`：JS 渲染、靜態 HTML 無完整持股，OpenAPI 無主動式 ETF 投組 → 不採用。
- TWSE / TPEx / TDCC OpenAPI：無主動式 ETF 每日完整持股 dataset → 不採用。
- MoneyDJ = `partial_top_n`、WantGoo = `quarterly_composition`（沿用 R8-6b 判定，不進 flow）。

**是否有 full holdings**：是（50 檔，`sum(Amount)==ST.Value` 證明非 top-N 截斷）。
**是否有 holding_shares**：是（`Share`，單位 股）。
**是否有 as_of_date**：是（`TranDate`，逐日；探測當下 2026-06-23）。
**是否需要 browser automation**：**否**。單次純 HTTP GET 即取得；首次 302+`__nxquid`
session cookie 由 `cookiejar` 自動處理（非登入 / 無 captcha / 無私有 API）。
→ **推翻 R8-6b §5.2 對 ezMoney 的 BLOCKED**（先前誤判源於既有 Playwright 原型只會「找下載按鈕」）。

**改了哪些檔案**：

- 新增 `internal/etfflow/ezmoney.go`（`EzMoney` parser，coverage = full_holdings）。
- 新增 `internal/etfflow/ezmoney_test.go` + `testdata/ezmoney_test_holdings.html`（合成 fixture，非真實持股）。
- 改 `internal/etfflow/holdings_fetcher.go`：`defaultPager` 加 `net/http/cookiejar`（處理 session bootstrap）。
- 改 `cmd/etfflow-fetch/main.go`：新增 `--source ezmoney` + `--fund-code`，`knownETFs` 補 00981A→49YTW。
- 改 `cmd/etfflow-fetch/main_test.go`：`TestRunSourceValidationOffline`。
- 新增 `docs/SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md`；本檔追加本節。

**測試結果**：`go build ./...` / `go vet ./...` / `go test ./...` 全過（fixture-only，未打外網）。

**是否 commit / commit hash**：feat 與 docs 各一 atomic commit，hash 見「Commit 清單」。

**是否觸 hard stop**：否。session cookie 非登入；無 browser automation / crawler / 私有 API；
未改 scoring / sorting / RocketScore / WatchAction / ExplosionProb / broker；未 push。

**既有 dirty files**：`.gitignore`、`configs/config.yaml`、`index.html`、`stocks.yaml`、
`data/00981A_TW_holding_20260430.csv`、`fetch_00981a_official.py`、`scripts/`、`.agents/`
全程未動、未混入 commit。

**風險 / 限制**：ezMoney 若改版（DataAsset 結構變動），解析層會先失敗、不會默默產生壞資料；
真實 snapshot JSON 仍 gitignore、不 commit；抓取維持單次公開 GET，勿高頻打站。

**下一步**：其他統一投信主動式 ETF（00403A/00988A…）沿用 `EzMoney` + 各自 `--fund-code`；
可把 code→fundCode 對照集中；每日抓取以低頻 cron / 人工觸發。
