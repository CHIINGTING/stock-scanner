# R8-1 — ETF Flow / Active Rotation Context（施工計畫 / Implementation Plan）

> **Plan only — 尚未開工。** 本文件是 R8 的設計與施工計畫，等明確「開工 R8-1」指示後才寫 code。
> R8 為 **display-only context**：不改分數 / 排序 / WatchAction / ExplosionProb / stop profile 預設、不自動交易。
> 預設 `enable_etf_flow: false`、`show_etf_flow: false`，report 預設外觀完全不變。
> 第一版資料源僅 `ManualCSVSource`（手動匯入 CSV），**不做任何爬蟲**。

---

## 0. 定位（務必先讀）

```text
R8 = ETF Flow / Active Rotation Context。
這是「延遲揭露的資金流背景」，不是盤中即時 signal，不是交易指令。

目標：讓 report 能看出
  - 某檔股票下跌，是否可能來自 ETF 被動換股賣壓
  - 某檔股票是否被主動式 ETF 連續減碼（主動資金題材切換）
  - 某族群是否正被主動式 ETF 加碼 / 減碼
```

### 0.1 紅線（本文件全程適用）

- ETF flow 是 **延遲揭露** 的資金流背景，**不是盤中即時買賣**。
- **不是交易指令**。
- **不自動下單**。
- **不接 broker**。
- **不改 score / sort / WatchAction / ExplosionProb**。
- ETF 減碼 ≠ 基本面惡化；ETF 賣壓消化中 ≠ 可以買；必須搭配 scanner 既有訊號交叉判讀。

### 0.2 為什麼要做

案例：聯電曾遇 ETF 換股策略，連續數日賣出大量張數。這類下跌不一定等於基本面轉弱，也可能是
ETF 再平衡 / 高股息換股 / 主動式 ETF 轉題材。Scanner 目前看得到價格 / RS / NewHigh / VCP /
MomentumFlow / MTF / Guardrail，但看不到 ETF 持股變化。R8 補這個缺口——以**背景 context**的形式。

---

## 1. 三項已拍板的設計決策

1. **欄位型別**（避免 scanner 套件膨脹、避免進 ShadowSignals）：
   ```go
   WatchlistEntry.ETFFlow *etfflow.StockFlowSignal `json:"etf_flow,omitempty"`
   ```
   - 型別定義在 `internal/etfflow` 套件，scanner 匯入它。
   - 是 `WatchlistEntry` 的**專屬欄位**，比照 `HoldingHorizon`(R7-1) / `HorizonHint`(R6-7)。
   - **絕不**放進 `ShadowSignals`（`watchlist.go:19`）——那個結構會被 C6b guardrail scoring 讀取。
   - `computeRocket` / `rocketInput` 永遠讀不到 ETF flow。

2. **資料源**：第一版只做 `ManualCSVSource`（從手動匯出的 CSV 讀）。
   - **不做** official PCF / issuer / MoneyDJ / capitalfund 任何 crawler。
   - 理由：台股 ETF 每日持股資料源不穩定、各 issuer 格式不同；ManualCSVSource 足以讓 snapshot /
     flow / signal / report 全部完成開發與測試。official PCF adapter 之後獨立做，**不卡住 R8 主線**。

3. **Sector rotation**：第一版只做 **context 顯示**，不升級成 sector signal scoring。
   - 第一版只追 `00981A` + `00919`，ETF 數量太少。
   - sector delta 可顯示「主動 ETF 是否切題材」，但**不改 `rotation.go`**、**不進 `SectorRotation` scoring**、
     **不進 `RocketScore`**。

---

## 2. Data lag 設計（揭露延遲）

ETF 持股是**延遲揭露**資料（官方/發行商通常 T+1 或更久才公布前一交易日成分）。R8 必須把延遲明確標示，
避免被誤當盤中即時。

### 2.1 新增欄位

| 欄位 | 意義 |
|---|---|
| `as_of_date` | 這份持股「資料對應的交易日」（持股截止日） |
| `fetched_at` | 實際抓取 / 匯入這份資料的時間戳 |
| `data_lag_days` | `today_trading_date − as_of_date` 的交易日落差（揭露延遲天數） |

`data_lag_days` 在「**比對當日 scanner 跑的交易日**」與「snapshot 的 `as_of_date`」時計算，存進
per-stock 訊號裡，供 report 顯示。

### 2.2 Report 必顯字句（固定、不可省略）

```text
此為延遲揭露資料，不代表盤中即時買賣。
```

只要 ⑨ 區有 render（`show_etf_flow == true` 且 `ETFFlow.Computed`），這行**一律**出現，並一併顯示
`as_of_date` 與 `data_lag_days`（例：「資料日 2026-06-10，延遲 2 個交易日」）。

---

## 3. 子階段拆分

| 階段 | 內容 | 主要產出 |
|---|---|---|
| **R8-1** | Holdings ingestion：ManualCSVSource → 正規化 → snapshot schema + 持久化 | `internal/etfflow/snapshot.go`, `ingest.go` |
| **R8-2** | Flow calculator：snapshot 差分、連續天數、壓力比、加總、族群 delta（純函式） | `internal/etfflow/flow.go` |
| **R8-3** | Signal classification + strength 分級 + data_lag 標記 | `internal/etfflow/signal.go` |
| **R8-4** | Report 顯示（⑨ 區）+ `WatchlistEntry.ETFFlow` 掛載（post-loop attach） | 改 `report.go`, `watchlist.go` |
| **R8-5** | Config（預設全關）+ 測試 + forbidden-token guardrail | `config.yaml`, `*_test.go` |

> 建議分 PR：**R8-1 + R8-2 先合**（資料層，可完全離線用手填 CSV 測），R8-3~R8-5 後合。
> 資料層不被資料源不確定性卡住。

---

## 4. 第一版追蹤的 ETF

```yaml
- code: "00981A"  type: "active"    # 主動式 ETF 代表
- code: "00919"   type: "passive"   # 高股息 / 規則型代表
```

資料模型穩定後再擴充（00878 / 00929 / 00940 / 00713 / 00980A / 00982A）。第一版不貪多。

---

## 5. 資料來源（R8-1）

第一版唯一來源：**ManualCSVSource**。

### 5.1 Adapter 介面（為日後 official adapter 預留，但第一版只實作 Manual）

```go
// HoldingsSource 抽象不同來源；第一版只實作 ManualCSVSource。
type HoldingsSource interface {
    // Load 回傳某 ETF 在某 as_of_date 的原始持股列。
    Load(etfCode, asOfDate string) ([]RawHolding, error)
}
```

### 5.2 ManualCSVSource

- 從**手動匯出的 CSV**讀（路徑與檔名格式在 R8-1 開工時定，例如 `<dir>/raw/<etf>_<asOfDate>.csv`）。
- CSV 欄位（草案）：`stock_code, stock_name, holding_shares, holding_raw_unit, holding_weight`。
- **本文件不附任何 sample CSV，不建立任何實體資料。**

### 5.3 單位正規化

- 持股單位（股 / 張 / 千股）一律轉成 **股**。
- 原始值（`holding_raw_unit`）與正規化後（`holding_shares`，股）**都保留**。

### 5.4 來源追溯

每份 snapshot 都存 `source_url`（手動匯入可填來源說明字串）、`fetched_at`、`as_of_date`。

---

## 6. Snapshot schema（R8-1）

`internal/etfflow/snapshot.go`，JSON 持久化（比照 `fetcher/cache.go` 的 file-cache 風格；
**實際資料目錄不在本 plan PR 建立、不 commit 任何 snapshot**）：

```go
type Holding struct {
    StockCode      string  `json:"stock_code"`
    StockName      string  `json:"stock_name"`
    HoldingShares  int64   `json:"holding_shares"`    // 正規化為「股」
    HoldingRawUnit string  `json:"holding_raw_unit"`  // "股" | "張" | "千股"
    HoldingWeight  float64 `json:"holding_weight"`    // %
}

type Snapshot struct {
    ETFCode          string    `json:"etf_code"`
    ETFName          string    `json:"etf_name"`
    ETFType          string    `json:"etf_type"`                 // "active" | "passive"
    AsOfDate         string    `json:"as_of_date"`               // 資料對應交易日 YYYY-MM-DD
    UnitsOutstanding int64     `json:"etf_units_outstanding,omitempty"`
    NAVOrAUM         float64   `json:"nav_or_aum,omitempty"`
    Holdings         []Holding `json:"holdings"`
    SourceURL        string    `json:"source_url"`
    FetchedAt        time.Time `json:"fetched_at"`
}
```

---

## 7. Flow calculation（R8-2，純函式）

輸入「`as_of` 當日 snapshot + 前一交易日 snapshot（+ 更早 N 天用於連續天數）」，
**不讀未來、不 mutate**（比照 `horizonhint.go` 的純函式紀律）。

### 7.1 每檔股票 × 每檔 ETF

```text
delta_shares          = today.shares - prev.shares
delta_weight          = today.weight - prev.weight
consecutive_buy_days  / consecutive_sell_days   // 回看歷史 snapshots 連號，中斷即重置
flow_pressure_ratio   = abs(delta_shares) / stock_avg_volume_20d
```

> `stock_avg_volume_20d` 已存在於 `StockAnalysis.AvgVolume20`（`types.go:97`）。
> **pressure ratio 的 join 在 attach 時做**（見 §11），calculator 本身只吐 `delta_shares`，
> 避免 `etfflow` 套件反向依賴 `scanner`。`avg_volume == 0` → ratio 不計算，標 `Computed=false`，不 panic。

### 7.2 多 ETF 加總（per stock）

```text
total_active_etf_delta_shares   / total_passive_etf_delta_shares   / total_etf_delta_shares
active_sell_pressure_ratio      / passive_sell_pressure_ratio      / aggregate_sell_pressure_ratio
```

### 7.3 族群層級（context only）

用既有 `fetcher/sectors.go` + `configs/sectors.yaml` 的 code→sector 映射：

```text
active_etf_sector_delta[sector]    // 看主動 ETF 是否從半導體切到 PCB / CCL / 被動元件
passive_etf_sector_delta[sector]
```

**只算 + 顯示，不改 `rotation.go`、不進 `SectorRotation` scoring、不進 `RocketScore`。**

---

## 8. Active / Passive 分類

**不靠演算法猜，靠 config 明示**：每檔 `watch_etf` 都標 `type`，一路帶進 snapshot，計算時據此分流到
active / passive 兩組加總。簡單、可控、可測。

---

## 9. Signal classification + strength（R8-3）

`internal/etfflow/signal.go`。

### 9.1 訊號列舉

```text
ETF_FLOW_NEUTRAL
PASSIVE_ETF_SELL_PRESSURE   / PASSIVE_ETF_BUY_SUPPORT
ACTIVE_ETF_SELL_PRESSURE    / ACTIVE_ETF_BUY_SUPPORT
ACTIVE_ETF_CONSENSUS_SELL   / ACTIVE_ETF_CONSENSUS_BUY     // 多檔主動 ETF 同向（第一版僅 1 檔 active，保留語意）
ETF_AGGREGATE_SELL_PRESSURE / ETF_AGGREGATE_BUY_SUPPORT
```

### 9.2 Strength 分級（門檻待校準，做成模組常數，不寫死）

| Strength | 條件（初版草案，待校準） |
|---|---|
| LOW | `consecutive >= 3` 或 `pressure > 0.2` |
| MEDIUM | `pressure > 0.5` |
| HIGH | `pressure > 1.0` |
| EXTREME | `pressure > 1.0` 且 `consecutive >= 5`（保留，可後調） |

### 9.3 per-stock 訊號輸出型別（草案）

```go
type StockFlowSignal struct {
    Computed     bool   `json:"computed"`        // false = 資料不足（其餘為零值）
    AsOfDate     string `json:"as_of_date"`
    FetchedAt    string `json:"fetched_at"`
    DataLagDays  int    `json:"data_lag_days"`

    Signal       string `json:"signal"`          // 上列列舉
    Strength     string `json:"strength"`        // LOW / MEDIUM / HIGH / EXTREME

    ActiveDeltaShares  int64   `json:"active_delta_shares"`
    PassiveDeltaShares int64   `json:"passive_delta_shares"`
    AggregateRatio     float64 `json:"aggregate_sell_pressure_ratio"`
    ActiveConsecutive  int     `json:"active_consecutive_sell_days,omitempty"`
    PassiveConsecutive int     `json:"passive_consecutive_sell_days,omitempty"`

    PerETF  []ETFLeg     `json:"per_etf,omitempty"`   // 每檔 ETF 的 delta / 連續天數
    Lines   []string     `json:"lines,omitempty"`     // report 用、已過 forbidden-token 檢查的文案
}
```

---

## 10. Report 顯示（⑨ 區，display-only）

接在 `⑧ 回測觀察週期`（`report.go:1258-1273`）之後，**比照 ⑧ 的 template 結構**。
gating：`show_etf_flow && e.ETFFlow != nil && e.ETFFlow.Computed`。

### 10.1 顯示範例

```text
⑨ ETF Flow / Active Rotation Context
此為延遲揭露資料，不代表盤中即時買賣。（資料日 2026-06-10，延遲 2 個交易日）

ETF Flow Pressure：HIGH

Passive ETF：
- 00919 近 5 日減碼，估算賣壓占 20 日均量偏高
- 可能屬規則換股 / 被動再平衡賣壓
Active ETF：
- 00981A 近 3 日減碼，主動式 ETF 對此股轉弱

解讀：
- 若價格下跌但 RS / 型態未同步轉弱 → 可能是 ETF / 主動資金換股壓力
- 若 ETF 減碼 + RS 轉弱 + 跌破 MA60 → 資金與技術同步轉弱

注意：
- ETF flow 是延遲揭露的資金流背景，不是盤中即時買賣
- ETF 減碼 ≠ 基本面惡化
- ETF 賣壓消化中 ≠ 可以買，需等待價格確認
- 需搭配 scanner 既有訊號交叉判讀
```

### 10.2 文案紅線（會寫 forbidden-token 測試強制）

- ✅ 允許：「賣壓可能來自 ETF flow」「主動式 ETF 連續減碼」「等待價格確認」「觀察賣壓是否消化」
  「此為延遲揭露資料，不代表盤中即時買賣」
- ❌ 禁止 token：`可以買`、`可以賣`、`買進`、`賣出`、`到價下單`、`建議買`、`建議賣`

---

## 11. 掛載點（attach）

比照 `rsTable` 的做法（`watchlist.go:289` `BuildRSTable`）與 `MTFRiskNote` 的 **post-loop attach**
（`watchlist.go:228-234`）：

1. Run 啟動時把 snapshots 讀成 `flowTable map[stockCode]*etfflow.StockFlowSignal`。
2. 在 `EnrichWatchlist` 的 **post-loop** 依 `e.A.Symbol` 找 flowTable，並用 `e.A.AvgVolume20` 在 attach
   當下完成 `flow_pressure_ratio` 計算，寫進 `e.ETFFlow`。
3. gating：`enable_etf_flow == false` → 整個 flowTable 不建、`e.ETFFlow` 維持 nil。

post-loop attach = 不動 `computeRocket`、不改 `EnrichWatchlist` 主迴圈計分路徑，風險最低。

---

## 12. Config 設計（預設全關；本 plan PR 不改 config）

> **本文件只描述未來 config 形狀；R8-1 plan PR 不會修改 `configs/config.yaml`。**
> 實際加欄位是 R8-5。

`scanner.Config` 未來新增（比照 `EnableHorizonHint` 的 gating 慣例）：

```yaml
enable_etf_flow: false          # 算不算（snapshot 差分 + 訊號）
show_etf_flow:   false          # report ⑨ 顯不顯
etf_flow:
  snapshot_dir: "data/etf_holdings"
  history_days: 10              # 連續天數回看窗
  watch_etfs:
    - { code: "00981A", type: "active" }
    - { code: "00919",  type: "passive" }
  strength:                     # 待校準，可省略走預設
    pressure_low: 0.2
    pressure_medium: 0.5
    pressure_high: 1.0
    consecutive_min: 3
```

兩層 flag gating（全 repo 慣例）：
- `enable_etf_flow` = shadow 算不算。
- `show_etf_flow` = report 顯不顯。
- 兩者皆 false 為預設，report 預設外觀完全不變。

---

## 13. 測試清單（R8-5）

```text
✓ snapshot parsing（JSON round-trip + 單位 股/張/千股 正規化）
✓ delta_shares 計算（含首日無前一日 → nil / Computed=false fallback）
✓ consecutive_sell_days / consecutive_buy_days（連號中斷重置）
✓ flow_pressure_ratio（avg_volume=0 → 不 panic、標 Computed=false）
✓ active vs passive 分流加總
✓ aggregate flow（active + passive 合計）
✓ sector delta（用 sectors.yaml 映射，context only）
✓ data_lag_days 計算（as_of_date 與當日交易日落差）
✓ strength 分級邊界（0.2 / 0.5 / 1.0 門檻）
✓ nil / 缺資料 fallback（snapshot 缺檔、股票不在任何 ETF）
✓ show_etf_flow=false → report 完全不出現 ⑨ 區
✓ show_etf_flow=true  → 顯示但 RocketScore / Action / 排序逐字不變（golden 對照）
✓ report 一定出現「此為延遲揭露資料，不代表盤中即時買賣。」
✓ forbidden tokens 不出現在 ⑨ 任何 render 結果
✓ enable_etf_flow=false → WatchlistEntry.ETFFlow 維持 nil
```

---

## 14. 會新增 / 修改的檔案（未來實作時，非本 plan PR）

**新增**
```text
internal/etfflow/snapshot.go        # schema + JSON 持久化
internal/etfflow/ingest.go          # HoldingsSource 介面 + ManualCSVSource
internal/etfflow/flow.go            # 計算層（純函式）
internal/etfflow/signal.go          # 分類 + strength + data_lag
internal/etfflow/*_test.go
docs/SPEC_R8_1_ETF_FLOW.md          # 本設計文件（本 PR 唯一新增）
```
**修改（小範圍、加法為主）**
```text
internal/scanner/scanner.go         # Config 加 flag/struct（純加欄位）
internal/scanner/watchlist.go       # WatchlistEntry.ETFFlow 欄位 + post-loop attach
internal/report/report.go           # ⑨ template 區塊 + 少數 template func
cmd/scanner/main.go                 # 讀 snapshot → 建 flowTable → 傳入
configs/config.yaml                 # 上述 flag，預設全關
```

> ⚠️ 本 plan PR（R8-1 design spec）**只新增 `docs/SPEC_R8_1_ETF_FLOW.md` 一個檔**。
> 不寫 code、不改 config、不改 report、不改 scanner、不建立 `data/etf_holdings/`、不 commit sample CSV。

---

## 15. 保證不碰的檔案

```text
internal/scanner/rocket.go            # RocketScore / WatchAction / ExplosionProb
internal/scanner/scorer.go            # 評分
internal/scanner/momentum.go / vcp.go / newhigh.go / relstrength.go / mtf.go
internal/scanner/holdinghorizon.go    # R7-1
internal/scanner/horizonhint.go       # R6-7
internal/scanner/rotation.go          # 族群排序 / scoring 不動
internal/r6backtest/** , cmd/r6backtest/**   # R6 backtest
ShadowSignals struct（watchlist.go:19）       # ETF flow 絕不進此結構
computeRocket / rocketInput                   # 不新增任何 ETF 輸入
sorting 邏輯 / stop profile 預設
config 既有 flag 的預設行為
```

---

## 16. R8 不准做的事（重申）

不要改：live scanner scoring / RocketScore / WatchAction / ExplosionProb / sorting /
R6 backtest / R7 HoldingHorizon / R6-7 HorizonHint / config 既有預設行為。

不要做：自動下單 / broker API / 交易執行 / AI agent 操作股票。
