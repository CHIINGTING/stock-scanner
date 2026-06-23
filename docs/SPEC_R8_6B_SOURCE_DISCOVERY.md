# SPEC R8-6b — ETF Holdings Source Discovery & Classification

狀態：Discovery 完成 + **coverage guardrail 已落地（code）**。本文件做來源分類與 guardrail；
guardrail 已在 `internal/etfflow` 以 `coverage_type` 欄位 + `LoadHistory` 過濾 + fetcher
PARTIAL 語意實作（見 §3.1 / §6）。**仍不把任何 partial / 季度來源接進 R8 flow。**

相關：[SPEC_R8_1_ETF_FLOW.md](SPEC_R8_1_ETF_FLOW.md)（snapshot schema / flow 定義）、
[SPEC_R8_5_MANUAL_INGEST.md](SPEC_R8_5_MANUAL_INGEST.md)（手動 ingest CLI）、
[R8_AUTONOMOUS_EXECUTION_LOG.md](R8_AUTONOMOUS_EXECUTION_LOG.md)（本輪自動執行紀錄）。
R8-6a MoneyDJ auto fetcher 已改造為 **top-n-only probe**（不可進 flow，見 §6）。

---

## 0. 為什麼需要這份文件

R8 ETF flow（`CalculateFlows` / `delta_shares` / sell-pressure / 連續賣出天數）只有在
**逐日、完整、含持有股數**的持股資料下才成立。若把不符合的來源（前 N 大、季度比例）
誤接進 flow，會產生**假的賣壓訊號**（例如某股「跌出前 10 名」被誤判成「被賣光」，或拿
季度比例當成當日股數變化）。因此先把每個來源的涵蓋型別釘死，再決定用途。

---

## 1. MoneyDJ — `partial_top_n`

| 項目 | 值 |
|------|----|
| 來源頁 | `https://www.moneydj.com/etf/x/basic/basic0007.xdjhtm?etfid=<code>.TW` |
| coverage_type | **`partial_top_n`** |
| top_n | **10**（持股明細表只揭露前 10 大成分） |
| 每日資料日期 (as_of_date) | **有**（每日；探測 00981A 取得 2026-06-12，揭露延遲約 3 天） |
| holding_shares（持有股數） | **有**（前 10 大，單位 股；例：2330 = 11,960,000 股） |
| holding_weight（比例） | 有（投資比例 %） |
| 是否完整持股 | **否**（僅前 10 大；第 11 名以後不揭露） |

判定與限制：

- ✅ 可作 **top holdings context**（前 10 大每日權重 / 股數）。
- ❌ **不可直接進 R8 flow**：top-10 的 `delta_shares` 會漏掉第 11 名後的部位；個股
  「跌出前 10」與「實際賣光」在此資料下**無法區分**。
- ❌ **不可把「某股缺席前 10」解讀為賣出**。
- ⚠️ 抓取方式：單次公開頁 GET（已實作於 R8-6a fetcher，加 User-Agent + timeout，
  無 crawler / 無 browser automation）。但因 markup 可能變動，需 fixture 測試保護。

> R8-6a 的 MoneyDJ fetcher 目前**不得**以 `full_holdings` 名義 commit / 寫入 production
> snapshot。改造方向見 §6。

---

## 2. WantGoo — `quarterly_composition`

| 項目 | 值 |
|------|----|
| 來源頁 | `https://www.wantgoo.com/stock/etf/<code>/constituent`（SPA，資料由站內 API 載入） |
| 站內資料 API | `/stock/etf/<code>/shareholding-data`（成分比例）、`/stock/etf/<code>/top-five-net-asset-value-ratio-data`（每月前五大 NAV 比例） |
| coverage_type | **`quarterly_composition`** |
| 成分涵蓋 | 全成分**比例**（比 MoneyDJ top-10 完整），但本質是季度成分 |
| holding_shares（持有股數） | **無**（成分物件欄位為 `shareholdingRate` 比例、`close`、`change`、`changePercent`；無 shares 欄位） |
| holding_weight（比例） | 有（季度比例） |
| 每日資料日期 | **無**。**季度更新**（1、4、7、10 月，資料為「上一季」持股；頁面明文標示） |
| 背後 API 是否提供每日完整 | **否**。同一份季度比例資料、無股數；且 `shareholding-data` 對純 HTTP 回 400 BadRequest（需站內 JS 的請求機制），抓取**不穩定** |

判定與限制：

- ✅ 可作 **季度持股結構 / 權重 context**（標示「上一季」與季別）。
- ❌ **不可做 `delta_shares`**（無股數）。
- ❌ **不可做 sell pressure**（季度比例 ≠ 當日部位變化）。
- ❌ **不可拿季度資料判斷當日換股**。
- ❌ **不可把缺席某股解讀為賣出**。
- ❌ **不可標記為 `full_holdings` 或 `full_daily_holdings`**。

---

## 3. 資料源分類表（coverage_type）

| coverage_type | 定義 | 可進 R8 flow？ | 允許用途 |
|---------------|------|----------------|----------|
| `full_daily_holdings` | 逐日、完整成分、含持有股數 | ✅ 是 | `CalculateFlows` / `delta_shares` / sell-pressure / 連續賣出天數 |
| `partial_top_n` | 僅前 N 大（含股數、可每日） | ❌ 否 | 只做 top holdings context |
| `quarterly_composition` | 季度成分比例（無股數、非每日） | ❌ 否 | 只做季度持股結構 context |
| `unusable` | 不可解析 / 不穩定 / 無用欄位 | ❌ 否 | 不使用 |

目前歸類：

| 來源 | coverage_type |
|------|---------------|
| MoneyDJ basic0007 | `partial_top_n`（top_n = 10） |
| WantGoo constituent | `quarterly_composition` |
| 統一投信 ezMoney 官方頁（49YTW） | `full_daily_holdings`（**自動抓取已落地**：純 HTTP，見 R8-6c） |
| 手動匯出 full holdings CSV | `full_holdings`（經 `cmd/etfflow-ingest`） |

> **R8-6c 更新（2026-06-24）**：ezMoney 已確立為**公開、純 HTTP、無 browser automation**
> 的 `full_daily_holdings` 自動來源（`EzMoney` adapter），可進 R8 flow。R8 flow 進入路徑因此
> 為：**ezMoney 自動抓取** 或 **手動匯出 full holdings CSV**（empty / `full_holdings` 標籤）。
> MoneyDJ（top_n_only）/ WantGoo（quarterly）仍不可進 flow。詳見
> [SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md](SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md)。

### 3.1 Snapshot `coverage_type` 欄位對照（registry → code）

本文件 registry 用語對應到 `internal/etfflow` snapshot 的 `coverage_type` 欄位值：

| registry 用語（本文件） | snapshot `coverage_type` 欄位值（code） | `IsFlowEligible()` | `LoadHistory` |
|--------------------------|------------------------------------------|--------------------|---------------|
| `full_daily_holdings` | `full_holdings`（或**空字串**＝legacy/手動 full） | ✅ true | 讀入 → 可進 flow |
| `partial_top_n` | `top_n_only`（+ `top_n`、`coverage_note`） | ❌ false | **跳過** |
| `quarterly_composition` | `quarterly_composition` | ❌ false | **跳過** |
| `unusable` / 無法判定 | `unknown`（或任何未識別值，fail closed） | ❌ false | **跳過** |

欄位（`internal/etfflow/snapshot.go`）：

```go
CoverageType string `json:"coverage_type,omitempty"` // full_holdings | top_n_only | quarterly_composition | unknown
TopN         int    `json:"top_n,omitempty"`
CoverageNote string `json:"coverage_note,omitempty"`
```

- `IsFlowEligible()` 只在 `coverage_type ∈ {"", full_holdings}` 回 true；其餘一律 false（fail closed）。
- `LoadHistory`（R8 flow 的唯一輸入閘口）只回傳 flow-eligible snapshot，partial/季度/unknown **永不**進
  `CalculateFlows`。

---

## 4. Guardrails（強制規則）

1. **只有 `full_daily_holdings` 可進 `CalculateFlows`。**
2. `partial_top_n` / `quarterly_composition` / `unusable` **一律不可進 flow**。
3. **缺席某股不可解讀成賣出**（前 N 大缺席 ≠ 賣光；季度名單缺席 ≠ 當日出清）。
4. **比例資料不可解讀成股數變化**（weight delta ≠ share delta）。
5. **不可拿季度資料判斷當日換股 / 當日流向。**
6. snapshot 必須帶 `coverage_type`；flow 計算前必須檢查 `coverage_type == full_daily_holdings`，
   否則拒絕計算（fail closed）。
7. 抓取一律**公開頁面**：不繞過登入 / captcha / 私有簽章 API，不對站台加壓（單次請求、
   加 User-Agent 與 timeout，無 crawler、無 browser automation）。
8. **不得 commit** `data/etf_holdings/*.json`（真實 snapshot 一律 gitignore）。

---

## 5. Full daily holdings 來源探查結論

目標：找到 `full_daily_holdings`（逐日、完整、含持有股數）公開來源。

### 5.1 候選：統一投信 ezMoney 官方 PCF / 投資組合明細

00981A 為統一投信主動式 ETF（fundCode `49YTW`）。主動式 ETF 依規定**每日揭露 PCF（申購買回
清單）/ 投資組合明細**，由發行商官方公布：

```
holdings: https://www.ezmoney.com.tw/ETF/Fund/Info?fundCode=49YTW
PCF:      https://www.ezmoney.com.tw/ETF/Transaction/PCF?fundCode=49YTW
```

**內容上**這份資料符合 `full_daily_holdings`：逐日、完整成分（非前 N 大）、含個股代號 + 持有
股數 + 權重 + 資料日期。已有一份**人工匯出**樣本可佐證（`data/00981A_TW_holding_20260430.csv`，
date/ticker/stock_name/weight_pct/shares，含 2330/2383/2454/2327…，明顯多於 top-10）。

> **更新（R8-6c，2026-06-24）：本節對 ezMoney 的 BLOCKED 判定已被推翻。** ezMoney 完整每日
> 持股其實 **server-side 內嵌**於頁面 `<div id="DataAsset">`，純 HTTP GET 即可解析，**不需**
> browser automation。先前 BLOCKED 是因既有 Playwright 原型只會「找下載按鈕」而誤判。已實作
> `EzMoney` adapter（`full_holdings`，可進 flow）。詳見
> [SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md](SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md)。
> 下方 §5.2 / §5.3 / §5.4 保留為歷史紀錄。

### 5.2 為何（曾）判定為 BLOCKED（不接自動 flow）— 已由 R8-6c 推翻

| 約束（§4.7 / hard-stop） | ezMoney 現況 |
|---------------------------|--------------|
| 不可 browser automation（Playwright/Selenium） | ❌ 現有原型 `fetch_00981a_official.py`、`scripts/fetch_00981a_daily_official.mjs` **皆以 Playwright** 抓取，代表頁面為 JS 渲染 / 需瀏覽器下載觸發，純 HTTP GET 取不到完整表 |
| 不可高頻 crawler / 不加壓 | 自動每日抓需排程打站 |
| 不可登入 / captcha / 私有 API | 未驗證有無，但前項已足以 BLOCK |

> 因此**未確立**「公開純 HTTP、無 browser automation」可取得 ezMoney 完整每日持股的途徑。
> 依 hard-stop 規則（需 browser automation → 停止、記錄 BLOCKED、不繞過），**不**建置 ezMoney
> 自動 fetcher，也**未**對該站發出探測請求（避免加壓 / 繞過）。

### 5.3 結論（明文）

```
目前未找到 00981A full_daily_holdings 的「公開、純 HTTP、無 browser automation」自動來源。
MoneyDJ 只能 top_n_only。
WantGoo 只能 quarterly_composition。
ezMoney 官方 PCF 內容雖為 full_daily_holdings，但唯一可行抓取為 browser automation（被禁），故 BLOCKED。
R8 flow 暫時只能使用「人工匯出 full holdings CSV」（cmd/etfflow-ingest），由人工從官方頁匯出，
非由 scanner 自動爬取。
```

### 5.4 後續（R8-6c，未動工）

- 若要把 ezMoney full holdings 自動化，須先找到**不需 browser automation 的公開取得方式**
  （例如官方提供的 CSV/XLS 直接下載連結、或公開 JSON endpoint），再比照本文件分類確認三要件
  （逐日 + 完整 + 股數）後，才允許標 `full_holdings` 進 flow。
- 在那之前，唯一進 flow 的路徑是**人工 full holdings CSV**。

---

## 6. R8-6a MoneyDJ fetcher — 已改造為 top-n-only probe（已落地）

R8-6a MoneyDJ auto fetcher 已依本 registry 改造並落地（`internal/etfflow/moneydj.go`、
`holdings_fetcher.go`、`cmd/etfflow-fetch`）：

- `MoneyDJ.Coverage()` 回傳 `coverage_type = top_n_only`、`top_n = 10`、`coverage_note =
  "MoneyDJ page exposes top holdings only; not a full holdings snapshot."`。
- fetcher 對「coverage 非 full」回 `status = PARTIAL`：**預設不寫入** production snapshot、
  CLI exit ≠ 0。
- 僅在 `--allow-partial` 時寫出 probe，且 probe snapshot **仍標 `top_n_only`**；CLI 印出
  `WARNING: PARTIAL … NOT eligible for R8 flow`。
- `LoadHistory` 以 `IsFlowEligible()` 過濾，**即使 probe 被寫出也會被跳過**，永不進
  `CalculateFlows`。
- 抓取維持單次公開頁 GET（User-Agent + timeout，無 crawler、無 browser automation）；解析
  由 fixture 測試保護。
