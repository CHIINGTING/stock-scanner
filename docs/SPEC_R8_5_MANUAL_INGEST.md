# R8-5 — Manual ETF Holdings Ingest（施工計畫 / Implementation Plan）

> **Plan only — 尚未開工。** 本文件是 R8-5 的施工計畫，等明確「開工 R8-5a」指示後才寫 code。
> R8-5 只補「資料入口」：把 **manual CSV → ETF holdings snapshot JSON**，使其可被 R8-4 的
> `etfflow.LoadHistory` 讀回、進而在 report ⑨ 顯示為 **ETF Flow / Active Rotation Context**。
> **第一版只做 manual ingest，不做任何 crawler。** 預設 `enable_etf_flow:false` / `show_etf_flow:false`
> 完全不變；資料產物一律不進 repo。

---

## 0. 定位（務必先讀）

```text
R8-5 = ETF Flow 資料入口補完整（manual ingest）。
Manual CSV → etfflow.Ingest → etfflow.Save → data/etf_holdings/<etf>_<as_of>.json
            → R8-4 LoadHistory → CalculateFlows → Classify → report ⑨（display-only）
```

第一版正式支援兩檔：

```text
00981A：active ETF（主動式）
00919 ：passive / high-dividend ETF（高股息 / 規則型）
```

ETF flow 是**延遲揭露的資金流背景**，不是盤中即時訊號、不是交易指令、不自動下單、不接 broker、
不改 score / sort / WatchAction / ExplosionProb（沿用 R8 全程紅線）。

---

## 1. R8-5 子階段

| 階段 | 內容 |
|---|---|
| **R8-5a** | ingest CLI（`cmd/etfflow-ingest`）+ `CSVFileSource`（讀任意 `--input` 路徑）+ 抽共用 `parseHoldingsCSV` |
| **R8-5b** | 正式 `watch_etfs`（00981A active + 00919 passive）寫進 config；`.gitignore` 精準忽略 + `.gitkeep` 保結構 |
| **R8-5c** | 端到端測試：CSV → Ingest → Save → LoadHistory → CalculateFlows → Classify（→ report ⑨） |
| **R8-5d** | 驗證 runbook（手動 snapshot 跑出 ⑨）+「strength 只觀察、不校準」備註 |

> 預設仍 `enable_etf_flow:false` / `show_etf_flow:false`：R8-5 只補資料入口，使用者**本機 opt-in** 才看得到 ⑨。
> 子階段可拆多個 atomic commit（R8-5a code / R8-5b config+ignore / R8-5c test），不混。

---

## 2. CLI 形式：`cmd/etfflow-ingest`（獨立 cmd）

採獨立 cmd（與 `cmd/r6backtest`、`cmd/scanner` 同為頂層工具）。**比較**：接在 `cmd/scanner` 需加
subcommand/flag 分流、污染主掃描 CLI；獨立 cmd 邊界乾淨、可獨立測試。轉檔屬維運工具，不應混入
scanner 主流程 → **採獨立**。

```bash
go run ./cmd/etfflow-ingest \
  --etf-code 00981A \
  --etf-name "統一台股增長主動式 ETF" \
  --etf-type active \
  --date 2026-06-10 \
  --input ./incoming/00981A_20260610.csv \
  --snapshot-dir data/etf_holdings \
  --source-url "manual"
```

旗標（草案）：

| flag | 必填 | 說明 |
|---|---|---|
| `--etf-code` | ✓ | ETF 代號（如 `00981A`） |
| `--etf-name` | ✓ | ETF 名稱（寫入 snapshot，display 用） |
| `--etf-type` | ✓ | `active` \| `passive`（其他值報錯，不猜） |
| `--date` | ✓ | `as_of_date`，`YYYY-MM-DD`（持股對應交易日） |
| `--input` | ✓ | manual CSV 路徑（任意路徑） |
| `--snapshot-dir` | ✗ | 預設 `data/etf_holdings` |
| `--source-url` | ✗ | provenance 字串，預設 `manual` |

流程：

```text
驗證 flags
  → CSVFileSource{Path: --input}
  → etfflow.Ingest(src, meta, time.Now())   // meta = {code,name,type,as_of,source_url}
  → etfflow.Save(snapshot-dir, snap)          // 寫 <etf>_<as_of>.json
  → 印出輸出路徑 + 持股筆數
```

驗證規則：`--etf-type ∈ {active,passive}`、`--date` 可被 `YYYY-MM-DD` 解析（由 `Snapshot.Validate`
把關）、`--input` 檔存在；任一不符 → 非 0 退出碼 + 清楚錯誤訊息。CLI `main` 維持薄，邏輯落在
`internal/etfflow` 既有/新函式，便於測試。

---

## 3. CSV 欄位格式（沿用 R8-1，**不加 alias**）

canonical header（順序無關、大小寫無關、容忍前後空白／千分位逗號／`%` 後綴）：

```text
stock_code, stock_name, holding_shares, holding_raw_unit, holding_weight
```

**欄位語意（明確定義，避免誤解）：**

```text
holding_shares   = 在 manual CSV 中代表「原始數量」（raw quantity），
                   會搭配 holding_raw_unit 轉換成「股」後寫入 snapshot.HoldingShares。
                   （沿用 R8-1 既有欄名；holding_raw_value alias 先不做，避免 schema 分支過早擴張。）
holding_raw_unit = 原始單位，支援：股 / 張 / 千股
                   張 = 1,000 股；千股 = 1,000 股；股 = 1
                   未知單位 → 報錯，不猜（NormalizeToShares 已如此）。
holding_weight   = 權重（%），可帶 % 後綴。
```

範例（**僅文件示意，不會 commit 成 sample CSV 檔**）：

```text
stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight
2330,台積電,1200,張,12.5
2303,聯電,800000,股,6.0
```

---

## 4. snapshot 輸出格式（沿用 R8-1，不改 schema）

`etfflow.Ingest` 組裝、`etfflow.Save` 寫出：

```text
data/etf_holdings/<etf_code>_<as_of_date>.json
```

欄位（R8-1 `Snapshot`）：

```text
etf_code
etf_name
etf_type           # active | passive
as_of_date         # YYYY-MM-DD（持股對應交易日）
fetched_at         # time.Now()（CLI 注入；ingest 當下）
holdings[]         # {stock_code, stock_name, holding_shares(已正規化為股), holding_raw_unit, holding_weight}
source_url         # provenance 字串（manual CSV 填 --source-url）
```

可選 provenance：另存原始 CSV 到 `data/etf_holdings/raw/<etf>_<as_of>.csv`（方便日後 `ManualCSVSource`
重讀／稽核）。列為 **nice-to-have**，非必要。

**資料產物（`*.json` / `raw/*.csv`）一律不進 repo**（見 §5）。

---

## 5. `.gitignore` 策略（精準，保留結構）

`data/` 內已有**已追蹤**檔 `data/stock_names_zh.yaml`，**不可整包 ignore `data/`**。建議 `.gitignore` 新增：

```text
data/etf_holdings/*.json
data/etf_holdings/raw/*.csv
```

並追蹤兩個結構檔（非資料）以保留目錄：

```text
data/etf_holdings/.gitkeep
data/etf_holdings/raw/.gitkeep
```

註：`LoadHistory` 已對「缺目錄」安靜降級（回 `(nil,nil)`），所以 `.gitkeep` 屬「方便」非「必要」。

> ⚠️ 本 plan PR **不改 `.gitignore`、不新增 `data/etf_holdings`、不新增 `.gitkeep`、不新增 sample CSV**。
> 上述忽略規則與結構檔於 R8-5b 實作時才加。

---

## 6. 共用 parser 重構（R8-5a，DRY 裁示）

把既有 `ManualCSVSource.Load` 的 CSV 解析核心抽成共用函式，讓 `ManualCSVSource` 與新的
`CSVFileSource` 都呼叫它：

```go
// internal/etfflow/ingest.go（加法重構，行為不變）
func parseHoldingsCSV(r io.Reader) (...holdings..., error)

type CSVFileSource struct { Path string }       // 讀任意路徑
func (s CSVFileSource) Load(_, _ string) (...)   // 開檔 → parseHoldingsCSV
// ManualCSVSource.Load 改為：開既有路徑慣例檔 → parseHoldingsCSV
```

理由：避免兩份 parser、保持 `ManualCSVSource` / `CSVFileSource` 行為一致、既有 R8-1 測試可保護不退化。

> 開工待定案的小點：使用者裁示寫為 `parseHoldingsCSV(r io.Reader) ([]Holding, error)`。
> 但 R8-1 既有 `HoldingsSource.Load` 回傳 `[]RawHolding`、且**正規化（NormalizeToShares）集中於
> `Ingest`**。為與已提交介面一致、維持「正規化單一出處」，建議共用層回傳 `[]RawHolding`，
> 正規化仍由 `Ingest` 統一處理；最終回傳型別於 R8-5a code 時定案。此屬實作細節，不影響本計畫其餘決策。

---

## 7. 測試清單（全部 `t.TempDir()`，**不寫 repo fixture**）

```text
✓ CSV parse（欄序無關、空白 / 千分位 / % 容忍）
✓ 股 / 張 / 千股 → 股 換算
✓ 未知單位報錯（不猜）
✓ invalid date 報錯（Ingest / Validate 擋）
✓ CSVFileSource 讀任意路徑、缺檔報錯
✓ 端到端：CSV → Ingest → Save → LoadHistory 讀回，欄位一致
✓ 00981A active / 00919 passive 兩範例各跑一遍（合成資料）
✓ CalculateFlows + Classify 串接（delta / 連續天數 / strength 合理）
✓ report ⑨：合成 snapshot → LoadHistory → attach → render（show=true 出現 / false 不出現）
✓ forbidden tokens 不出現在 ⑨（沿用 R8-4 count-diff 手法）
```

> CLI `main` 薄，測底層函式（`CSVFileSource` / `parseHoldingsCSV` / `Ingest` / `Save` / `LoadHistory`），
> 不為 `main()` 寫脆弱的 exec 測試。

---

## 8. 會新增 / 修改哪些檔案

**新增**

```text
cmd/etfflow-ingest/main.go            # ingest CLI（薄）
internal/etfflow/csvfile.go           # CSVFileSource（+ 視情況含共用 parseHoldingsCSV）
internal/etfflow/ingest_e2e_test.go   # 端到端測試
data/etf_holdings/.gitkeep            # 結構（可選；R8-5b）
data/etf_holdings/raw/.gitkeep        # 結構（可選；R8-5b）
docs/SPEC_R8_5_MANUAL_INGEST.md       # 本文件
```

**修改**

```text
internal/etfflow/ingest.go            # 抽出共用 parseHoldingsCSV（加法重構，行為不變）
configs/config.yaml                   # watch_etfs 填 00981A+00919；flags 維持 false（R8-5b）
.gitignore                            # 精準忽略 data/etf_holdings 產物（R8-5b）
Makefile                              # （可選）make etfflow-ingest 便利目標
```

> 本 plan PR **只新增 `docs/SPEC_R8_5_MANUAL_INGEST.md` 一個檔**，不動其餘任何檔案。

---

## 9. 哪些檔案保證不碰

```text
internal/scanner/rocket.go / scorer.go / rotation.go        # 評分 / 排序 / 族群
internal/scanner/watchlist.go / etfflow_attach.go           # R8-4 attach 邏輯
internal/report/report.go（⑨ 渲染邏輯）
internal/etfflow/flow.go / signal.go / loader.go / snapshot.go  # R8-2/3/4/1 演算法與 schema（不改）
computeRocket / rocketInput / ShadowSignals
R6 backtest（internal/r6backtest, cmd/r6backtest）/ R7 HoldingHorizon / R6-7 HorizonHint
```

（`snapshot.go` 不改 schema；`ingest.go` 僅加法重構不改既有行為，`ManualCSVSource` 測試保持綠燈。）

---

## 10. Runbook：手動 CSV → snapshot → scanner ⑨ 驗證（R8-5d）

```text
1. 手動匯出 00981A / 00919 連續數日持股 CSV（canonical 欄位，見 §3）。
2. 對每檔每日跑 cmd/etfflow-ingest：
     go run ./cmd/etfflow-ingest --etf-code 00981A --etf-type active \
       --date 2026-06-10 --input ./incoming/00981A_20260610.csv
   → 產生 data/etf_holdings/00981A_2026-06-10.json（不進 repo）。
3. 本機暫時設定（不提交此變更，或用獨立 config 檔）：
     enable_etf_flow: true
     show_etf_flow:   true
     etf_flow.watch_etfs: [00981A(active), 00919(passive)]
4. 跑 scanner，watchlist 含受影響個股 → 開 report，確認 ⑨：
     「⑨ ETF Flow / Active Rotation Context」
     「此為延遲揭露資料，不代表盤中即時買賣。」
     資料日 / 延遲 N 個交易日
     主動式 ETF 行 / 高股息／規則型 ETF 行 / 解讀 / 注意
5. 比對 show_etf_flow=false 時 ⑨ 消失，且 RocketScore / WatchAction / ExplosionProb / 排序逐字不變。
```

runbook 本身寫在本文件；實際匯出的 CSV 與產生的 snapshot **不進 repo**。

---

## 11. strength：只觀察、不校準

R8-5 **不動門檻**，維持 `etfflow.DefaultStrengthThresholds`（pressure 0.2 / 0.5 / 1.0、
consecutive 3 / 5）。先用真實 00981A / 00919 手動 snapshot **觀察** ⑨ 輸出是否合理、蒐集樣本；
門檻校準留待後續（`StrengthThresholds` 已集中、可由 config 覆寫，校準時零結構改動）。

---

## 12. R8-5 不准做（重申）

不要做：crawler / official PCF adapter / MoneyDJ adapter / 自動下載 ETF holdings / 自動下單 /
broker API / 交易執行。

不要改：RocketScore / WatchAction / ExplosionProb / sorting / R6 backtest / R7 HoldingHorizon /
R6-7 HorizonHint / R8-4 report 邏輯。
