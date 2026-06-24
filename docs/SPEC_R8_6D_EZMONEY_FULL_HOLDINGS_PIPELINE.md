# SPEC R8-6d — ezMoney Full Holdings Pipeline（正式化）

狀態：**DONE（code + tests + docs）**。把 R8-6c 找到的統一投信 ezMoney 官方完整持股來源
收斂成可維護、可擴充、可日常執行的 pipeline：集中式 fundCode registry、強化 parser
validation、正式化 snapshot schema（含 reconciliation 審計）、收斂 CLI（`--etf` /
`--fund-code` override / `--dry-run`）、補齊 guardrail 與 httptest 測試。

相關：
[SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md](SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md)（來源探查 / adapter 起點）、
[SPEC_R8_6B_SOURCE_DISCOVERY.md](SPEC_R8_6B_SOURCE_DISCOVERY.md)（coverage 分類 / guardrail）、
[SPEC_R8_1_ETF_FLOW.md](SPEC_R8_1_ETF_FLOW.md)（snapshot schema / flow）、
[R8_AUTONOMOUS_EXECUTION_LOG.md](R8_AUTONOMOUS_EXECUTION_LOG.md)。

---

## 0. 為什麼有這檔（pipeline 邊界）

R8-6c 證明了 ezMoney 是「公開、純 HTTP、無 browser automation」的 `full_daily_holdings`
來源。R8-6d 把它變成**正式可用**：

- 不再把 fundCode 散落在 CLI；改成單一 registry。
- parser 對缺漏 / 不一致資料一律**回 error，絕不產生半套 snapshot**。
- snapshot 帶 source / fund_code / 逐筆市值 / reconciliation 審計，可被既有 flow 辨識。
- CLI 用 `--etf` 自動查 fundCode，可 `--fund-code` 覆蓋，可 `--dry-run` 只驗證不寫檔。
- 所有 guardrail 不變：只有 `full_holdings` 進 flow；top-N / quarterly 一律擋。

關鍵安全事實（同 R8-6c）：ezMoney 是**官方公開頁**，單次 GET 即取得 server-side 內嵌的
`DataAsset` JSON；**不需要 browser automation**；首次 302 + `__nxquid` 是公開頁正常的
session 流程（cookie jar 自動處理），**不是登入繞過**；**不高頻抓取**；**真實 snapshot 不
commit**。

---

## 1. fundCode registry（`internal/etfflow/registry.go`）

單一事實來源：ETF code →（provider, fundCode, market, coverage, name, type）。

```go
type ETFMapping struct {
    ETFCode, Provider, FundCode, Market, Coverage, Name, Type string
}
func LookupETF(etfCode, provider string) (ETFMapping, bool) // provider "" = 只比對 code
func RegisteredETFs(provider string) []ETFMapping
```

目前內建（fundCode 皆取自 ezMoney 官方頁 `DataFundList` 的 `sStockNo ↔ sFundCode`）：

| ETF | Provider | FundCode | Type | Coverage |
|-----|----------|----------|------|----------|
| 00981A 主動統一台股增長 | ezmoney | 49YTW | active | full_holdings |
| 00403A 主動統一升級50 | ezmoney | 63YTW | active | full_holdings |
| 00988A 主動統一全球創新 | ezmoney | 61YTW | active | full_holdings |

- parser **不含** mapping（parser 只懂怎麼讀一個 provider）。
- 找不到 mapping 且未給 `--fund-code` → CLI 明確報錯、non-zero exit。

### 1.1 如何新增其他統一投信主動式 ETF（mapping 流程）

1. 開啟該 ETF 的 ezMoney 官方頁（單次公開 GET，**不得** browser automation）。
2. 從頁面 `DataFundList` 找該 ETF 的 `sStockNo`（如 `00403A`）對應的 `sFundCode`（如 `63YTW`）。
3. 在 `etfRegistry` 加一行（`Provider: ezmoney`、`Coverage: full_holdings`、填 name/type）。
4. 跑 `go test ./internal/etfflow/...`（`TestEtfRegistryEntriesComplete` 會檢查欄位齊全）。
5. **未經官方頁確認的 fundCode 一律不得新增**（避免抓錯基金）。

---

## 2. EzMoney parser validation（`internal/etfflow/ezmoney.go`）

`Parse` 委派給 `ParseValidated`，後者執行全部檢查，任何一項失敗即回 error（**no partial
snapshot**，R8-6d Hard-Stop §4/§5）：

1. 頁面必須存在 `<div id="DataAsset" data-content="...">`。
2. `data-content` HTML-unescape 後必須能 parse JSON。
3. 必須有股票類別 `ST`，且 `ST.Details` 非空。
4. 每筆持股必須有：股票代號、股票名稱、`Share`(股數)、`Amount`(市值)、可解析的 `TranDate`。
   - `Share` / `Amount` 以 `*float64` 解析：**欄位缺失（key 不存在）** 與**合法的 0**
     可區分；缺失 → error，0 → 保留（如出清中部位）。
5. `sum(Details.Amount)` 必須與 `ST.Value`（來源宣告的股票總額）對齊，容差
   `ezAmountTolerance = 0.5%`：足以吸收來源四捨五入，但遠小於任何截斷（top-N 會漏掉大量
   市值而對不齊）→ 不齊則 error（Hard-Stop §5）。
6. `ST.Value <= 0` → error（無法對帳）。

對齊成功才產生 `HoldingsValidation{ StockCategory, HoldingCount, SumAmount,
CategoryValue, AmountMatched }`。

> 沒有 silent fallback、沒有空 snapshot、沒有「有 holdings 就過」。

---

## 3. Snapshot schema（`internal/etfflow/snapshot.go`，additive）

新增欄位（皆 `omitempty`，向後相容既有 snapshot）：

```json
{
  "etf_code": "00981A",
  "etf_type": "active",
  "as_of_date": "2026-06-23",
  "source": "ezmoney",
  "fund_code": "49YTW",
  "source_url": "https://www.ezmoney.com.tw/ETF/Fund/Info?fundCode=49YTW",
  "coverage_type": "full_holdings",
  "holdings": [
    { "stock_code": "2330", "stock_name": "台積電",
      "holding_shares": 11960000, "holding_raw_unit": "股",
      "holding_weight": 10.13, "holding_amount": 29780400000 }
  ],
  "validation": {
    "stock_category": "ST", "holding_count": 50,
    "sum_amount": 290778016200, "category_value": 290778016200,
    "amount_matched": true
  }
}
```

- `coverage_type = full_holdings` → `IsFlowEligible()` true → `LoadHistory` 收 → 可進
  `CalculateFlows`。
- `holding_shares` 永遠正規化為股；`holding_amount` 為來源市值（審計用）。
- `source` / `fund_code` 標示 provenance；`validation` 為對帳審計紀錄。
- top-N / quarterly snapshot **不可能**被標成 full：coverage 由 parser/fetcher 蓋章，
  `IsFlowEligible` 只認 `full_holdings`（或空字串 legacy），其餘 fail closed。

---

## 4. CLI（`cmd/etfflow-fetch`）

```bash
# 1) 自動查 registry 得到 fundCode（49YTW），抓官方完整持股：
go run ./cmd/etfflow-fetch --source ezmoney --etf 00981A --snapshot-dir data/etf_holdings

# 2) 手動覆蓋 fundCode：
go run ./cmd/etfflow-fetch --source ezmoney --etf 00981A --fund-code 49YTW

# 3) 只抓 + 驗證，不寫 snapshot：
go run ./cmd/etfflow-fetch --source ezmoney --etf 00981A --dry-run

# 4) MoneyDJ（top-10，context only）→ PARTIAL，預設不寫、exit≠0：
go run ./cmd/etfflow-fetch --source moneydj --etf 00981A
```

CLI 行為（全部在打網路前驗證）：

- `--etf`（主要）；`--etf-code` 為已棄用別名仍可用。
- `--source ezmoney`：fundCode 先用 `--fund-code`，否則查 registry；都沒有 → 報錯、exit≠0。
- `--source moneydj`：coverage 非 full → PARTIAL，不進 flow（`--allow-partial` 只寫 tagged probe）。
- `--dry-run`：fetch + parse + validate，但不 `Save`，`output: (dry-run: not written)`。
- 未知 `--source` / 缺 `--etf` / 未註冊且無 fundCode / 壞 `--require-date` → 明確錯誤 + exit≠0。
- snapshot 預設寫 `data/etf_holdings/`（已 gitignore，**真實資料不 commit**）。

---

## 5. Fetcher / HTTP（`internal/etfflow/holdings_fetcher.go`）

- `defaultPager` 用 `net/http/cookiejar`：單次 `GET` 即可跟隨「302-to-self + Set-Cookie
  (`__nxquid`)」的公開 session bootstrap，**無 browser automation、無登入、無 captcha**。
- 誠實 User-Agent（標明用途），合理 timeout，**單次請求、無 crawler、無激進 retry**（不對
  官方站加壓）。
- `SelfValidatingParser`（可選介面）：full-holdings parser 額外回傳 reconciliation
  metadata，Fetcher 蓋到 `Snapshot.Validation`。MoneyDJ 不實作 → 只 `Parse`、無 validation。
- 測試以 `httptest` 覆蓋「302 + cookiejar」案例，**完全不打外網**、不需 Playwright。

---

## 6. Guardrails（再驗證，未改 scoring/sorting/...）

| coverage_type | 進 flow？ | 來源 | 測試 |
|---------------|-----------|------|------|
| `full_holdings` / 空字串 legacy | ✅ | ezMoney / 手動 full CSV | `TestLoadHistorySkipsNonFullCoverage`、`TestEzMoneyFetchIsFlowEligible` |
| `top_n_only` | ❌ | MoneyDJ top-10 | `TestMoneyDJStaysPartial`、loader 過濾 |
| `quarterly_composition` | ❌ | WantGoo 季度 | loader 過濾 |
| `unknown` / 未識別 | ❌（fail closed） | — | loader 過濾 |

- 為什麼 MoneyDJ top-N 不進 flow：只揭露前 10 大，無法區分「跌出前 10」與「賣光」，
  `delta_shares` 會失真。
- 為什麼 WantGoo quarterly 不進 flow：季度比例、無股數、非每日，無法做當日流向。
- 為什麼 ezMoney 可進 flow：逐日、完整成分、含股數，且 `sum(Amount)==ST.Value` 自我對帳。
- **未改** RocketScore / WatchAction / ExplosionProb / sorting / broker / R6 / R7 / HorizonHint。

---

## 7. 測試（`go test ./...` 全過，offline）

- `registry_test.go`：lookup（code/provider/大小寫/未命中）、欄位齊全、provider 過濾。
- `ezmoney_test.go`：success + Amount、`ParseValidated` 對帳、coverage、URL、
  失敗案例（缺 blob / 缺 ST / 空 ST / 缺 code / 缺 name / 缺 Share / 缺 Amount /
  壞 TranDate / Amount mismatch / 無 category value）、e2e flow-eligible + schema 回放。
- `pipeline_test.go`：`httptest` 302+cookiejar、LoadHistory 過濾非 full、MoneyDJ 仍 partial、
  `--dry-run` 不寫檔。
- fixture 為**合成資料**（`testdata/ezmoney_test_holdings.html`），非真實持股；測試不打外網、
  不需 browser automation。

---

## 8. 資料政策（不得 commit）

`configs/config.yaml`、`index.html`、`stocks.yaml`、`.gitignore`、`data/*.csv`、
`data/etf_holdings/*.json`、`fetch_00981a_official.py`、`scripts/`、`.agents/`、任何真實
snapshot / 原始官方 response dump / cookie / token 一律不 commit（既有 dirty 亦不混入本線）。

---

## 9. 後續建議

1. 視需求把更多統一投信主動式 ETF 依 §1.1 流程加進 registry（每筆都要官方頁確認）。
2. 日常抓取以**低頻 cron 或人工**觸發 CLI；先 `--dry-run` 驗證再實際寫入。
3. 若 ezMoney 改版導致 `DataAsset` / `ST` 結構變動，parser 會先在 validation 失敗、不會默默
   產生壞資料；屆時更新 `ezmoney.go` 與 fixture。
