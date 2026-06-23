# SPEC R8-6c — Full Daily Holdings Source Discovery & Official Adapter

狀態：**RESOLVED + adapter 已落地（code）**。找到 00981A 的 `full_daily_holdings`
**公開、純 HTTP、無 browser automation** 來源（統一投信 ezMoney 官方頁），並實作
`EzMoney` adapter（`internal/etfflow/ezmoney.go`），標 `coverage_type = full_holdings`，
可進 R8 flow。

相關：
[SPEC_R8_6B_SOURCE_DISCOVERY.md](SPEC_R8_6B_SOURCE_DISCOVERY.md)（coverage 分類 / guardrail；
ezMoney 先前被標 BLOCKED，本文件解除）、
[SPEC_R8_1_ETF_FLOW.md](SPEC_R8_1_ETF_FLOW.md)（snapshot schema / flow 定義）、
[SPEC_R8_5_MANUAL_INGEST.md](SPEC_R8_5_MANUAL_INGEST.md)（手動 ingest CLI）、
[R8_AUTONOMOUS_EXECUTION_LOG.md](R8_AUTONOMOUS_EXECUTION_LOG.md)（自動執行紀錄）。

---

## 0. 目標回顧

找到 00981A「逐日、完整、含持有股數」的公開來源，欄位需含：

```
stock_code / stock_name / holding_shares / holding_weight / as_of_date
```

只有符合 `full_daily_holdings` 才可進 R8 flow（`CalculateFlows` / `delta_shares` /
sell-pressure）。R8-6b 已釘死 MoneyDJ = `partial_top_n`、WantGoo =
`quarterly_composition`，兩者**不可**進 flow。

---

## 1. 結論（先講重點）

| 問題 | 結論 |
|------|------|
| 有沒有 `full_daily_holdings` 公開來源？ | **有** |
| 來源 | 統一投信 ezMoney 官方基金頁：`https://www.ezmoney.com.tw/ETF/Fund/Info?fundCode=49YTW` |
| 有 holding_shares？ | **有**（`Share`，單位 股；schema 標「股數」） |
| 有 holding_weight？ | **有**（`NavRate`，持股權重 %） |
| 有 as_of_date？ | **有**（`TranDate`，逐日；探測當下 = 2026-06-23） |
| 完整還是 top-N？ | **完整**（50 檔成分；`sum(Amount)` 完全等於 ST 類別 `Value`，非 top-N 截斷） |
| 需要 browser automation？ | **不需要**（單次純 HTTP GET 即取得 server-side 內嵌 JSON） |
| 需要登入 / captcha / 私有簽章 API？ | **否** |

> 這推翻了 R8-6b §5.2 對 ezMoney 的 BLOCKED 判定。先前 BLOCKED 的原因是既有原型
> （`fetch_00981a_official.py`、`scripts/fetch_00981a_daily_official.mjs`）用 Playwright
> 去頁面「找下載按鈕」，因而誤以為頁面只能靠瀏覽器取得。實際上**完整持股已 server-side
> 內嵌在 HTML**，純 HTTP GET 即可解析，無須 browser automation。

---

## 2. 資料如何取得（純 HTTP）

ezMoney 把當日完整投組 server-side render 進頁面，內嵌在：

```html
<div id="DataAsset" data-content="[ ...HTML-escaped JSON... ]"></div>
```

`DataAsset` 是資產類別陣列（NAV / OUT_UNIT / 股票(ST) / 現金(CASH) / 期貨(GD) …）。
**股票（`AssetCode == "ST"`）** 類別底下的 `Details` 陣列即每一檔個股：

| JSON 欄位 | 意義 | 對應 |
|-----------|------|------|
| `DetailCode` | 股票代號（"2330"） | stock_code |
| `DetailName` | 股票名稱（"台積電"） | stock_name |
| `Share` | 股數（11,960,000，單位 股） | holding_shares |
| `NavRate` | 持股權重（10.13 %） | holding_weight |
| `TranDate` | 資料日期（"2026-06-23T00:00:00"） | as_of_date |
| `Amount` | 市值（NTD） | （驗證用） |

完整性佐證：探測當下 ST 類別 `Details` 有 50 檔，`sum(Amount) == ST.Value`
（290,778,016,200），`sum(NavRate) ≈ 98.94%`（其餘為現金 / 期貨）→ 確為**全部股票持股**，
非前 N 大。與既有人工匯出 `data/00981A_TW_holding_20260430.csv`（2330 = 11,960,000 股）
單位一致（股）。

### 2.1 session cookie（非登入）

首次 GET 會被 302 導回同一 URL 並 `Set-Cookie: __nxquid=...`（HttpOnly / Secure /
SameSite=Lax / ~24h）。這是站台的 session / anti-bot bootstrap，**任何瀏覽器或帶 cookie
jar 的 client 都會自動處理**，**不是帳號登入、無帳密、無 captcha、無私有簽章**。
因此仍落在「公開頁面、單次請求」的硬性約束內（R8-6 spec §4.7）。

實作上：`internal/etfflow/holdings_fetcher.go` 的 `defaultPager` 加上
`net/http/cookiejar`，跟隨 302 並回送 cookie，一次 `GET` 即拿到 200 + 完整 JSON。
此變更對 MoneyDJ（無 cookie 來源）無副作用。

---

## 3. Adapter（`EzMoney`）

`internal/etfflow/ezmoney.go` 實作 `HoldingsParser`：

```go
type EzMoney struct{ FundCode string } // 例：00981A → "49YTW"
func (EzMoney) Name() string
func (e EzMoney) URL(string) string     // ...Fund/Info?fundCode=<FundCode>
func (EzMoney) Coverage() Coverage      // {Type: full_holdings}
func (EzMoney) Parse(body string) (asOfDate string, holdings []RawHolding, err error)
```

- `Parse` **只讀** ST（股票）類別；期貨(GD)、現金(CASH)、NAV 摘要列一律不取。
- 非 ST 類別的 `Details` 為 JSON `null`，以 `json.RawMessage` 容納、不解碼，避免破壞整體解碼。
- 缺 `DataAsset` blob / 缺 ST 類別 / ST 無持股 / `TranDate` 不可解析 → **回 error，不產生半套
  snapshot**（R8 flow 需要真實 `delta_shares`，半套比沒有更糟）。
- `Share` 單位為股（`unitShare`），交由既有 `Ingest` / `NormalizeToShares` 正規化。
- coverage 為 `full_holdings`，經 `Fetcher.Fetch` 蓋章後 `IsFlowEligible() == true`，
  `LoadHistory` 會回傳 → 可進 `CalculateFlows`。

### 3.1 CLI

`cmd/etfflow-fetch`：

```bash
# 00981A 已內建 fundCode（49YTW）、name、type：
go run ./cmd/etfflow-fetch --etf-code 00981A --source ezmoney \
    --snapshot-dir data/etf_holdings

# 其他主動式 ETF 需自帶 --fund-code / --etf-name / --etf-type
go run ./cmd/etfflow-fetch --etf-code XXXXXA --source ezmoney \
    --fund-code <投信內部基金代號> --etf-name <名稱> --etf-type active
```

- `--source ezmoney` 缺 `--fund-code`（且非已知 ETF）→ 報錯、不打網路。
- `--source moneydj`（預設）維持 top10、context-only、PARTIAL 行為不變。

---

## 4. Guardrail 仍然成立

| coverage_type | 可進 R8 flow？ | 來源 |
|---------------|----------------|------|
| `full_holdings`（或空字串 legacy/手動） | ✅ | **ezMoney（本文件）** / 手動 full CSV |
| `top_n_only` | ❌ | MoneyDJ top-10 |
| `quarterly_composition` | ❌ | WantGoo 季度 |
| `unknown` / 未識別 | ❌（fail closed） | — |

- `IsFlowEligible()` / `LoadHistory` 為唯一進 flow 閘口，未改；ezMoney 因 `full_holdings`
  通過，MoneyDJ / WantGoo 仍被擋。
- 「缺席某股 ≠ 賣出」「比例 ≠ 股數」等 R8-6b §4 規則不變。
- snapshot 仍 gitignore（`data/etf_holdings/*.json` 不 commit）；adapter 只負責解析，
  抓取為單次公開 GET，無 crawler / 無 browser automation。

---

## 5. 測試（全 fixture，不打外網）

`internal/etfflow/ezmoney_test.go` + `testdata/ezmoney_test_holdings.html`（合成、非真實持股）：

- `TestEzMoneyParse`：只取 ST、3 檔、欄位正確、0% 權重新進股不被丟棄、HTML-escape 還原。
- `TestEzMoneyCoverageIsFull`：coverage = full_holdings、`IsFull()`。
- `TestEzMoneyURLUsesFundCode`：URL 用 fundCode。
- `TestEzMoneyParseErrors`：缺 blob / 空 ST / 無 ST / 壞 TranDate 一律 error。
- `TestEzMoneyFetchIsFlowEligible`：經 `Fetcher` → StatusOK → snapshot flow-eligible →
  `Save` → `LoadHistory` 回得到（證明真的能進 flow），股數正規化正確。
- `cmd/etfflow-fetch/main_test.go` `TestRunSourceValidationOffline`：未知 source /
  ezmoney 缺 fund-code 於打網路前就報錯。

`go build ./... && go vet ./... && go test ./...` 全過。

---

## 6. 後續建議

1. 其他統一投信主動式 ETF（00403A、00988A…）可沿用 `EzMoney`，只需各自 `--fund-code`；
   建議把 code→fundCode 對照集中（目前 00981A 內建於 `knownETFs`）。
2. 每日抓取排程仍維持「單次公開 GET」原則，勿高頻打站；可由人工或低頻 cron 觸發 CLI。
3. 若 ezMoney 改版（`DataAsset` 結構變動），fixture 測試會先在解析層失敗、不會默默產生壞資料；
   屆時更新 `ezmoney.go` 解析與 fixture 即可。
4. 真實 snapshot JSON 一律不 commit（gitignore），維持 R8-6b §4.8。
