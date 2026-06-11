# R6-7 — Report Holding Horizon Hint（施工計畫 / Implementation Plan）

> **Plan only — 尚未開工。** 本文件是 R6-7 的施工計畫，等明確「開工」指示後才寫 code。
> R6-7 為 **display-only**：不改分數 / 排序 / WatchAction / ExplosionProb / stop profile 預設、不自動交易。
> 預設 `show_horizon_hint: false`，report 預設外觀完全不變。
> 上游依據：`docs/SPEC_R6_6_RECENT_BULL_REGIME_VALIDATION.md`（20d primary horizon 的回測支撐）。

---

## 0. 定位（務必先讀）

```text
R6-7 = 把「主要觀察週期」顯示到 report 的 display 層。
不是自動交易建議，不是叫使用者一定持有幾天。
依目前型態 + R6 回測結果，在 report 顯示「主要觀察週期 = 20 個交易日」。
20d 對應近期台股強勢股節奏：強勢 → 處置(~10d) → 冷卻/下跌 → 出處置 → 再攻擊 ≈ 20 交易日。
5d/10d = early reaction / 早期反應；60d = optional reference / 中期參考。
20d 的可信度由 R6-6 近期強多頭驗證支撐（recent_4m / recent_6m 為主結論）。
```

## 0.1 兩項已拍板的設計決策

1. **欄位命名（避開 R7-1 撞名）。** `WatchlistEntry.HoldingHorizon` 已被 **R7-1**（`*HoldingHorizonResult`，stage+ATR shadow signal，`holdinghorizon.go`，PR #2 已合併、尚未進 report）佔用。R6-7 新欄位採用：
   ```go
   WatchlistEntry.HorizonHint *HoldingHorizonHint `json:"horizon_hint,omitempty"` // R6-7 report hint
   ```
   型別名維持 spec 的 `HoldingHorizonHint`，欄位名為 `HorizonHint`。R7-1 的 `HoldingHorizon` 完全不動。
2. **R6-7 ↔ R7-1 各自獨立。** R6-7 依本 spec 以 setup matching 獨立實作；R7-1 shadow 維現狀、**不進 report**。report ⑧ 只顯示 R6-7。R7-1 是否保留 / 改名 / 退場日後再議，本輪不碰。

---

## 1. 顯示內容（report ⑧）

接在現有 `⑦ Guardrail Signals` 之後（`internal/report/report.go` 約 L1201），新增：

```text
⑧ 回測觀察週期
主要觀察週期：20 個交易日
早期反應：5 / 10 日
中期參考：60 日

匹配型態：<MatchedSetup>
理由：
- <Reason ...>
注意：
- 這是觀察週期，不是交易指令
- baseline stop 對此類型可能過嚴
- ATR_3 / PCT_15 仍是候選，不是正式預設
```

僅在 `show_horizon_hint == true && HorizonHint != nil && HorizonHint.Computed` 時 render。

### 文案紅線
- ✅ 允許：主要觀察週期 / 回測觀察窗口 / 早期反應 / 中期參考 /「這是觀察週期，不是交易指令」
- ❌ 禁止：建議買入 / 建議持有到第 20 天 / 到期賣出 / 自動停損

---

## 2. 型別

新增 `internal/scanner/horizonhint.go`：

```go
type HoldingHorizonHint struct {
    Computed      bool
    PrimaryDays   int      // 20
    EarlyDays     []int    // [5, 10]
    ReferenceDays []int    // [60]
    MatchedSetup  string   // C_VCP_MA20_RETEST / B_PULLBACK_5|8|10|15|20 / A_MA20_PULLBACK / A_MA60_PULLBACK / DEFAULT
    Confidence    string   // LOW / MEDIUM
    Reason        []string
    Caveat        []string
}
```

掛載：`WatchlistEntry.HorizonHint *HoldingHorizonHint `json:"horizon_hint,omitempty"``（nil 表示未計算 / 開關關閉；資料不足時為非 nil 且 `Computed=false`）。

---

## 3. Setup matching（對應到實際既有欄位）

優先序 **C → B → A → D → Default**。輸入全為既算好的 shadow / 指標（display-only，不回灌任何分數）。

| Setup | 觸發條件（實際欄位） | MatchedSetup | Primary / Confidence |
|---|---|---|---|
| **C** VCP | `Shadow.VCP.Computed && Shadow.VCP.Valid && Shadow.VCP.QualityScore >= 70 && Shadow.RS.RSRankPercentile >= 70 && Shadow.Momentum.Flow != StructuralShiftDown` | `C_VCP_MA20_RETEST` | 20 / MEDIUM |
| **B** 52週高回檔 | `Shadow.NewHigh.DistanceFrom52wHighPct >= -25 && NewHighScore 高 && Shadow.RS.RSRankPercentile >= 70 && 近期有回檔`；回檔 5–10% → `B_PULLBACK_5/8/10`，15–20% → `B_PULLBACK_15/20` | `B_PULLBACK_*` | 20 / MEDIUM |
| **A** MA20/MA60 拉回 | 接近 MA20 → `A_MA20_PULLBACK`(MEDIUM)；接近 MA60 → `A_MA60_PULLBACK`(LOW/MEDIUM)。MA60 不在 `StockAnalysis`，需自 candles 算（沿用 R7-1 `indicator.SMA(closes,60)` 做法） | `A_MA20_PULLBACK` / `A_MA60_PULLBACK` | 20 / 見左 |
| **D** crash survivor | v1 **不主動 match**。未來若加 crash context，一律 LOW 並標「殺盤 case study、event_count 少、不可外推」 | — | — |
| **Default** | 未明確匹配，仍顯示 20d 觀察週期 | `DEFAULT` | 20 / LOW |

### 各 setup 理由文案（範例）
- **C**：目前具備 VCP valid / 高 RS / 接近 MA20 支撐；R6 回測中 VCP_MA20 retest 樣本足夠；20d 對應近期處置/冷卻/出處置/再攻擊循環。
- **A**：R6 顯示 A_MA20 優於 A_MA60；MA60 通常較慢，需確認是否仍是強勢股而非轉弱。

### 關鍵依賴（必記）
`Shadow.VCP / RS / NewHigh / Momentum` **只有對應 enable flag 開啟才非 nil**（`enable_vcp` / `enable_rs_rank` / `enable_new_high` / `enable_momentum_flow`）。flag 沒開 → C/B 無法 match → matcher 對 nil 安全降級到 **Default（仍顯示 20d）**，不得 panic。A 只依賴日線指標，較不受影響。

---

## 4. Config（mirror R7-1 風格）

`internal/scanner/scanner.go` Config 新增：
```go
EnableHorizonHint bool `yaml:"show_horizon_hint"` // 預設 false
```
`configs/config.yaml` 加 `show_horizon_hint: false`。預設關 → report 預設不變。

---

## 5. 計算接點

`internal/scanner/watchlist.go` 的 `EnrichWatchlist`，在 rocket 計算與 R7-1 HoldingHorizon **之後**、display 之前：

```go
if s.cfg.EnableHorizonHint {
    h := computeHorizonHint(e /*, 既有 shadow + candles */)
    e.HorizonHint = &h
}
```
與 R7-1 一致：擺在 score 之後，**絕不回灌 RocketScore / WatchAction / ExplosionProb / 排序 / stop**。

---

## 6. 測試 + 紅線守門

新增 `internal/scanner/horizonhint_test.go`：
- setup 優先序（C 優先於 B 優先於 A 優先於 Default）。
- 各 setup 條件邊界（QualityScore=69/70、RS=69/70、DistanceFrom52wHigh=−25 邊界、回檔深度分桶）。
- nil-shadow（各 enable flag 關）→ 降級到 `DEFAULT`、不 panic。
- 資料不足 → `Computed=false`。
- 文案 forbidden-token 掃描：`建議買入` / `持有到第` / `到期賣出` / `自動停損` / `BUY` / `PLACE_ORDER`。

回歸保證：`show_horizon_hint` 在 true/false 之間切換時，RocketScore / WatchAction / ExplosionProb / 排序 / stop profile **byte-identical**。`go build ./... && go vet ./... && go test ./...` 全綠。

---

## 7. 影響檔案

**新增（提交，需 `git add -f` 因 docs/ 被 gitignore）**
- `docs/SPEC_R6_7_REPORT_HOLDING_HORIZON_HINT.md`（本檔）

**新增（開工後）**
- `internal/scanner/horizonhint.go`、`internal/scanner/horizonhint_test.go`

**修改（開工後，最小切面）**
- `internal/scanner/watchlist.go`：`WatchlistEntry` 加 `HorizonHint` 欄位 + `EnrichWatchlist` 加計算接點
- `internal/scanner/scanner.go`：Config 加 `EnableHorizonHint`
- `internal/report/report.go`：新增 ⑧ 區塊（gated）
- `configs/config.yaml`：加 `show_horizon_hint: false`

---

## 8. 明確不碰 / 不做

```text
不改：rocket.go / scorer.go / rotation.go / R6 backtest engine / R6 setup logic /
      stop benchmark / live scoring / WatchAction / ExplosionProb / stop profile 預設 /
      R7-1 HoldingHorizon 欄位與行為
不做：自動下單 / broker API / 交易執行 / AI agent 操作股票
```

---

## 9. 最終定位宣告（務必保留）

```text
R6-7 是 display-only 的「回測觀察週期」提示，不是交易指令。
primary_horizon = 20d（5d/10d 早期反應；60d 中期參考），由 R6-6 近期強多頭驗證支撐。
預設 show_horizon_hint=false，report 預設外觀不變。
不改 score / 排序 / WatchAction / ExplosionProb / stop profile 預設；不下單、不接 broker。
R6-7（setup+回測，進 report）與 R7-1（stage+ATR，shadow）各自獨立。
```

---

*（本文件為 R6-7 施工計畫，plan only，未修改任何程式碼。開工指示後依本計畫實作。）*
