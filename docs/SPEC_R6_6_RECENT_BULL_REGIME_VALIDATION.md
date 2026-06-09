# R6-6 — Recent Bull Regime Validation（Current Bull Regime Verdict）

> **Docs only — no code default / scanner / report / scoring / stop-profile change.**
> 本文件整理 R6-6 的對照結果與「Current Bull Regime Verdict」，不修改任何預設、不改 live scanner / report /
> RocketScore / WatchAction / ExplosionProb / stop profile 預設。
> 上游：`internal/r6backtest`（新增 `recentbull.go` / `recentbull_output.go` + `-recentbull`）、
> `docs/SPEC_R6_BACKTEST_INTERPRETATION.md`、`docs/SPEC_R6_3_STOP_BENCHMARK_INTERPRETATION.md`、
> `docs/SPEC_R6_5_FRESH_DATA_RERUN.md`。

---

## 0. 定位（務必先讀）

```text
本次 R6-6 是 Recent Bull Regime Validation（近期強多頭驗證）。
不是 cross-regime validation。
不可把結果包裝成 cross-regime / fresh market regime validation。

結論只適用於近期強多頭 / 超級多頭環境。
不能外推到空頭、盤整、殺盤 regime。
不能直接改 live scanner / config / stop profile 預設。
R6 是離線回測，不下單、不接 broker、不替使用者交易。
```

**主要判斷週期 = 20d。** 對應近期台股強勢股節奏：`強勢 → 處置(~10d) → 冷卻/下跌 → 出處置 → 再攻擊`，
處置 + 再處置/出處置合計 ≈ 20 個交易日。
**5d/10d = early reaction / path observation；60d = optional reference，不作主判斷。**

---

## 1. 方法與資料

- **不抓資料**：沿用既有 `.cache/`（read-only，`2024-06-03 → 2026-06-08`，488 交易日，1974 檔）。不新增 fresh cache、不覆蓋任何 cache。
- 重用既有引擎（`collectEntries` / `buildTrade` / `ComputeStats`），新增 `-recentbull` 把**同一批 A/B/C 進場**依**進場訊號日**切入巢狀視窗，依 20d 聚合。**Setup D 不納入**（強多頭視窗無 crash regime）。
- 執行：
  ```bash
  go run ./cmd/r6backtest -cache .cache -out reports/r6_6_recent_bull -recentbull
  ```
- 視窗（以 axis 末日 2026-06-08 為錨，按進場日，巢狀 2m ⊂ 4m ⊂ 6m）：

| 視窗 | 角色 | signal_date 範圍 |
|---|---|---|
| recent_2m | overlay（最近盤、20d 成熟樣本最稀） | 2026-04-08 → 2026-06-08 |
| **recent_4m** | **PRIMARY（主結論）** | 2026-02-08 → 2026-06-08 |
| recent_6m | context（20d 成熟樣本最足） | 2025-12-08 → 2026-06-08 |

## 2. 20d Maturity 規則

- `last_available_date = 2026-06-08`；**20d maturity cutoff = 2026-05-11**（軸上回推 20 交易日）。
- 只有 `signal_date <= 2026-05-11` 才進 20d 統計；落在最後 20 交易日者標 **`UNMATURED_20D`，完全排除於 20d 之外**（仍計入 signal_count）。
- status（依 available_20d）：`≥30 OK / 12–29 LOW_SAMPLE / <12 INSUFFICIENT`。
- 跨視窗 unmatured 數恆為 129（A_MA20）等定值——因 unmatured = 最後 20 交易日訊號，與視窗起點無關，matured 才隨視窗成長（sanity 已驗：matured + unmatured = signal）。
- **recent_2m 20d matured 最稀** → 主結論以 **recent_4m / recent_6m** 的 20d 為準；recent_2m 數字保留作近端參考。

---

## 3. Current Bull Regime Verdict（逐項，20d、stop=BASELINE 除非另註）

> 數值取自 `reports/r6_6_recent_bull/backtest_recent_bull_summary_20260609.md`（4m 主、6m 佐）。

### Q1. A_MA20 是否仍優於 A_MA60 → ✅ 是（強多頭下更明顯）
| 視窗 | A_MA20 win/avg_20d | A_MA60 win/avg_20d |
|---|---|---|
| recent_4m | 40.5% / **5.7%** | 21.9% / 0.8% |
| recent_6m | 39.4% / **5.5%** | 28.2% / 1.7% |
A_MA20 在近期強多頭顯著優於 A_MA60（A_MA60 stop_hit 90%+、20d 報酬近 0）。

### Q2. B 15–20% deep pullback 是否還等得到，還是太慢 → ⚠️ 太慢（頻率低）
recent_4m 進場訊號數（signal_count）：B_5 **3272** / B_8 2509 / B_10 1963 / B_15 980 / **B_20 332**。
深回檔在強多頭**進場機會稀少**（B_20 訊號只有 B_5 的 ~10%）→ 「等不太到」。

### Q3. B 5–10% shallow 是否更適合超級多頭 → ✅ 是
- shallow 訊號數遠多於 deep（見 Q2），且 20d stop-adjusted 報酬與 deep 相當：recent_4m avg_20d B_5 7.2% / B_8 7.3% / B_10 7.0% vs B_15 6.2% / B_20 7.7%（deep 無明顯優勢）。
- 結論：超級多頭下 **shallow（5–10%）兼具「等得到 + 報酬不輸」**，較適合；deep 的 hold-edge（hold_20d 隨深度升高）在 stop-adjusted 下被抵銷且頻率太低。

### Q4. C_VCP_MA20 是否比一般 A_MA20 pullback 更有優勢 → ❌ 近期強多頭下「沒有」優勢
| 視窗 | C_VCP_MA20 win/avg_20d | A_MA20 win/avg_20d |
|---|---|---|
| recent_4m | 35.1% / 4.0% | **40.5% / 5.7%** |
| recent_6m | 36.5% / 4.1% | **39.4% / 5.5%** |
**與全 2y R6 結論分歧**：在近期強多頭，VCP retest 相對一般 MA20 拉回**無額外優勢、甚至略遜**（收斂/突破型在普漲環境的篩選力下降）。此為 recent-bull 特定觀察，不否定全期結論。

### Q5. C_VCP_BASE_LOW 是否太慢 / 樣本太少 → ✅ 太慢且樣本不足
recent_2m **INSUFFICIENT（avail_20d=8）**；recent_4m 34、recent_6m 84，avg_20d 僅 0.6–1.0%、stop_hit 93–94%。**近端樣本最薄、報酬最弱** → 太慢、不宜作近端依據。

### Q6. ATR_3 是否仍是好折衷 → ✅ 是（含停損者中最穩健）
含停損者中 20d 報酬最佳或並列最佳、`stop_Δ20` 最接近 0、stop_hit 中等。
例（recent_6m）：A_MA20 ATR_3 avg 7.7%（NO_STOP 8.1%，stop_hit 34.7%）；B_10 ATR_3 10.0%（NO_STOP 10.6%）；C_VCP_MA20 ATR_3 7.0%（NO_STOP 7.3%）。

### Q7. PCT_15 是否太寬或剛好 → ✅ 剛好（簡化備案，略遜 ATR_3）
PCT_15 與 ATR_3 接近、規則更簡單，stop_hit ~24–32%、rdd_p90 ~−17~−19%（非過寬）。
例（recent_6m）：A_MA20 PCT_15 7.6%（ATR_3 7.7%）；B_10 9.2%（ATR_3 10.0%）；C_VCP 6.8%（ATR_3 7.0%）。

### Q8. NO_STOP 是否明顯勝出、尾部回撤是否可接受 → ⚠️ 報酬最高，但尾部最深且近端可能被低估
NO_STOP 在每個 setup/視窗 20d avg 最高，但 **rdd_p90 一律最深**（recent_6m：A_MA20 −22.9% / B_10 −26.2% / C_VCP −23.0%）。
強多頭雖較能容忍寬停損，但 **尾部回撤實際存在**，且 **近端視窗因 forward truncation 會低估尾部 dd** → **不可據此稱「尾部可接受」**。

### Q9. BASELINE 是否在超級多頭下明顯過嚴 → ✅ 是（更明顯）
BASELINE 在每個 setup/視窗 20d 報酬最差、stop_hit 最高（A_MA20 72–83%、C_VCP 79–83%、A_MA60 90%+），realized dd 最淺。
強多頭下「過早洗出」代價最大（`stop_Δ20` 最負）→ BASELINE 過嚴的特性在近期更突出。

---

## 4. 可作為「現在盤勢參考」的結論（status=OK、20d）
1. 近期強多頭，**A_MA20 拉回優於 A_MA60**、優於深回檔等待。
2. **shallow（5–10%）拉回**兼顧頻率與 20d 報酬，較適合當前環境；deep（15–20%）等不太到。
3. 含停損操作下 **ATR_3 / PCT_15** 的 20d 表現接近 NO_STOP 而尾部較淺；**BASELINE 明顯過嚴**。
4. **C_VCP_MA20 在近期強多頭未優於一般 MA20 拉回**（與全期結論不同，屬盤勢特性）。

## 5. 不能外推的事
- 上述全部**僅限近期強多頭**；不可外推空頭 / 盤整 / 殺盤。
- **NO_STOP「尾部可接受」不可外推**：尾部 dd 在近端被資料截斷低估，且空頭下停損保護價值會上升。
- recent_2m 的 20d matured 樣本最稀（部分 cell 偏薄），主結論以 4m/6m 為準。
- universe = 現在在市股票 → **存活者偏誤**仍在，近端報酬偏樂觀。
- 60d 在近端多被 forward truncation 砍薄（avail_60d 遠小於 matured），僅作 optional reference。

## 6. LOW_SAMPLE / INSUFFICIENT cells
- **C_VCP_BASE_LOW_RETEST @ recent_2m：INSUFFICIENT（avail_20d=8）** → 不下結論。
- 其餘 setup×BASELINE 在三視窗皆 status=OK（avail_20d ≥ 34）。
- C_VCP_BASE_LOW @ recent_4m（34）/ recent_6m（84）為 OK 但屬最薄樣本，結論僅作弱參考。
- 完整 270 cell（10 setup × 9 policy × 3 window）見 `reports/r6_6_recent_bull/backtest_recent_bull_20260609.csv`。

## 7. 對 R6-4 stop profile 推進的影響
```text
不推進、不暫停定案——維持現狀。
```
- recent-bull 結果**本就不能用來推進 stop profile 定案**（只代表強多頭單一 regime）。
- 近期未出現會「翻轉」既有判斷的訊號：ATR_3 仍為含停損者最佳/並列最佳、BASELINE 仍過嚴、NO_STOP 仍報酬最高+尾部最深 → 與 R6-3/R6-4 一致。
- 但 **真正的 cross-regime 驗證仍未完成**，仍是 stop profile 定案的前置條件。**BASELINE 不改；ATR_3 / PCT_15 仍為候選；live scanner / stop profile 預設不動。**

---

## 8. 測試與 sanity check
| 檢查 | 結果 |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `go test ./...` | PASS（新增 `recentbull_test.go`：status 門檻、視窗巢狀、maturity 邊界、matured+unmatured==signal、CSV schema） |
| forbidden tokens（BUY/AUTO_BUY/PLACE_ORDER）掃描 r6_6 輸出 | 無命中 |
| maturity sanity | unmatured 跨視窗恆定（129/A_MA20）、matured+unmatured=signal、cutoff=2026-05-11 |
| 引擎 / setup / stop 邏輯 / Trade struct | 未改（僅新增檔案 + 新 flag 分支） |

## 9. 影響檔案
**新增（提交）**
- `docs/SPEC_R6_6_RECENT_BULL_REGIME_VALIDATION.md`
- `internal/r6backtest/recentbull.go`、`internal/r6backtest/recentbull_output.go`、`internal/r6backtest/recentbull_test.go`
- `cmd/r6backtest/main.go`：新增 `-recentbull` flag 分支（**只加新分支，不改既有預設路徑行為**）

**新增但 gitignored（不提交）**
- `reports/r6_6_recent_bull/`

**保證未碰**
- 既有 `internal/r6backtest/*.go` 的現有行為與 `Trade` struct（engine/setups/stops/setup_d/regime/output/types）
- `cmd/scanner/**` 與所有 live scanner 程式；`configs/config.yaml`(含 history_range)、`configs/sectors.yaml`、`stocks.yaml`
- RocketScore / WatchAction / ExplosionProb / report / 評分 / stop profile 預設 / ATR_3·PCT_15 正式狀態
- 既有 `.cache/`、`.cache_fresh_r6_5/`、既有 R6/R6-3/R6-4/R6-5 文件（本文件為新增）
- 無 broker / 自動下單 / 交易執行 / 排程下單路徑

---

## 10. 最終定位宣告（務必保留）

```text
本次 R6-6 是 recent bull regime validation，不是 cross-regime validation。
primary_horizon = 20d；5d/10d 只看早期反應；60d 只作 optional reference。
最近 20 交易日訊號 = UNMATURED_20D，不計入 20d 統計。
結論只適用於近期強多頭，不能外推空頭 / 盤整 / 殺盤。
不改 live scanner / config / stop profile 預設；不下單、不接 broker。
cross-regime 驗證仍未完成，仍為 stop profile 定案前置條件。
```

---

*（本文件為 R6-6 近期強多頭驗證，docs only，不修改任何程式碼預設、設定、scanner 或 report。
所有結論僅限近期強多頭，最終策略與停損政策仍須以台股全市場 / 跨 regime 資料驗證為準。）*
