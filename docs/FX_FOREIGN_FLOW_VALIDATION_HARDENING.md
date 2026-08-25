# FX × Foreign Flow — Validation Hardening

執行日期：2026-08-25　·　branch `research/usdtwd-market-context`
來源：`bin/r6backtest -fx`　·　輸出 `reports/backtest_fx_summary_20260825.md`

---

## 結論（先講）

> **全部都沒有。** USD/TWD、DXY、Residual TWD、以及 FX × Foreign Flow 交互作用，
> **沒有一項**對 Scanner 提供獨立且可驗證的增量資訊。

而且更重要的是：**上一輪的正向發現是假的。** 它由兩個可修復的缺陷造成 ——
選擇偏誤與把重疊觀測值當獨立樣本 —— 兩者修掉之後，效果消失並反轉。

| 項目 | 判定 |
|---|---|
| USD/TWD 獨立增量資訊 | **REJECTED** |
| DXY 獨立增量資訊 | **REJECTED** |
| Residual TWD 獨立增量資訊 | **INCONCLUSIVE**（+0.4pp，樣本 11） |
| FX × Foreign Flow 交互作用 | **REJECTED** |

**Production Scoring：NO MIGRATION.**

---

## 上一輪的診斷是錯的（先更正）

上一份報告寫：

> 「TWSE BFI82U 對特定舊日期回 HTTP 307……這是端點對舊資料的行為，不是限流。」

**這是錯的。** 證據：`2025-03-10` 先前回 307，本輪同樣的 header 回 **HTTP 200 且資料完整**。
7 個先前「不可能取得」的日期全部成功。

真正的機制是 **CDN 對持續請求量的節流**：
- 長時間連續走訪 → 進入 307 狀態，後續全部失敗（重試無用，仍在節流窗內）
- 短爆發（40 筆後停）→ **40/40 成功，零重試**
- 307 回應一律 784B、**無 Location header**、`x-cache: MISS, MISS, EXPIRED`

代價是一個研究結論建立在可修復的缺口上。這正是 M1 要求「先不要假設是限流」的理由。

---

## M1 — Coverage Audit

建立逐日診斷（`FetchDiagnostic`）：HTTP status、是否有 Location、body 大小、嘗試次數。
分類為 `AVAILABLE / NO_SESSION / NOT_FETCHED / HTTP_REDIRECT / HTTP_ERROR /
EMPTY_RESPONSE / PARSER_FAILURE / INVALID_PAYLOAD / TRANSPORT_ERROR / UNKNOWN`。

交易日曆取自 scanner 自己的 `.cache` —— 那些日期就是台股交易日，且與研究同源，
audit 不可能與研究對「哪些天存在」有分歧。

初始狀態：538 個交易日，可用 360（66.9%），缺 178（全部 `NOT_FETCHED`，舊流程未存診斷）。

---

## M2 — Coverage Repair ✅

短爆發策略（`-limit 40`，單次嘗試，600–700ms 間隔，輪次間隔 45–60s）：

```
66.9% → 78.6% → 86.1% → 87.0% → 90.0% → 95.5% → 99.8%
```

最終 **537 / 538（99.8%）**。唯一缺的 `2026-07-10` 是 `NO_SESSION` —— 交易所本身無資料，
依 missing-data contract **維持 MISSING，不以 0 或前值填補**。

同時修掉一個設計缺陷：原本只在結束時存檔，一次中斷就損失整輪成果（實際發生過）。
現在每 20 筆 checkpoint。

### 選擇偏誤：已消除

| FX context | 有外資資料 | 缺外資資料 |
|---|---|---|
| | 修復前 → **修復後** | 修復前 → **修復後** |
| TWD_STRONG_APPRECIATION | 3.6% → **8.0%** | 16.9% → **0%** |
| TWD_APPRECIATION | 9.4% → **12.7%** | 19.1% → **0%** |
| TWD_STABLE | 50.8% → **44.7%** | 32.0% → **0%** |

4.7 倍偏斜 → **無偏斜**（唯一在外的 1 天是貶值日）。上一輪的阻擋點解除。

---

## M3 — DXY Data Foundation ✅

| | |
|---|---|
| source | Yahoo `DX-Y.NYB`（ICE 美元指數） |
| archive | `data/fx/dxy.json`，1256 bars，2021-08-25 → 2026-08-24 |

**時間語意（實測）**：bar 以 **NY 午夜**分界，標記 D 收在 NY D 23:59 = **台北 D+1 11:59**。
台股 D 日 13:30 收盤時未完成 → 與 USD/TWD 同契約，只用 **≤ D−1**。

bucket 用的時區**從回應中讀取**（`exchangeTimezoneName`）而非寫死 —— 兩個序列的交易所不同
（London vs New York），寫死其一會讓另一個少一天或多一天。

### 一個必須修的語意錯誤

第一版把 DXY 套上 TWD 詞彙，輸出 `USD/TWD 下跌 → TWD_STRONG_APPRECIATION` 給 **DXY**。
**那是事實錯誤**：DXY 下跌代表美元走弱，與台幣無關。已建立獨立詞彙
（`USD_APPRECIATION` 等），並由 `TestDXYContextIsAboutTheDollar` 守住 ——
任何 DXY 讀數帶 `TWD_` 前綴即失敗。

---

## M4 — FX vs DXY Decomposition ✅

```
ΔUSDTWD_t = α + β·ΔDXY_t + residual_t
```

- α、β 由**結束於 t 之前**的滾動視窗擬合
- **被解釋的那一點被排除於自己的擬合之外**

第二點是關鍵。若 t 參與擬合，它會幫忙挑選那個「預測」它的係數，殘差因算術原因縮小，
分解看起來乾淨但是循環論證。`TestDecompositionExcludesThePoint` 擾動最後一根：
殘差必須改變，**β 與 α 必須完全不動**。

另修一個近奇異擬合缺陷：測試 fixture 在 20 日尺度上離散度趨近零 → `sxx≈0` → β 爆成 −3e8。
真實資料也可能出現。現在對回歸變數的標準差設下限（0.05 個百分點），
低於此**拒絕擬合**而非回傳天文數字。

---

## M5 — Effective Sample Size ✅

Canonical 統計改用**非重疊**觀測值：選了一天就跳過其前瞻視窗內的所有日期。

這一步的影響是決定性的：

```
baseline:  raw 538 天  →  effective N = 26
```

538 個交易日、20 日前瞻視窗，**最多只有約 26 個真正獨立的觀測值**。
先前報告的「n = 77」宣稱了約二十倍於實際的證據量。

---

## M6 — Statistical Validation ✅

95% bootstrap 區間，在非重疊樣本上重抽（blocking 已由建構完成，可被檢視，
而非藏在重抽常式裡）。seed 由樣本內容決定 → **同樣輸入永遠得到同樣區間**。
n < 3 時**不產生區間**，而非產生一個精確到小數點的虛構。

### 20d 結果（effective N）

| condition | raw | **eff N** | win% | 95% CI | median |
|---|---|---|---|---|---|
| **FX_BASELINE_ALL_DATES** | 538 | **26** | 42.3% | 23–62% | −0.66% |
| FX_TWD_APPRECIATION | 111 | 11 | 27.3% | 0–55% | −1.83% |
| FX_TWD_DEPRECIATION | 187 | 18 | 33.3% | 17–56% | −0.78% |
| FGN_SELL | 292 | 25 | 36.0% | 20–56% | −0.66% |
| FGN_STRONG_SELL | 94 | 19 | 42.1% | 21–63% | −0.92% |
| **FX_DEPR_X_FGN_SELL** | 116 | **15** | **26.7%** | 7–47% | −0.86% |
| DXY_USD_APPRECIATION | 184 | 15 | 20.0% | 0–40% | −1.78% |
| RESIDUAL_TWD_WEAK | 143 | 15 | 26.7% | 7–47% | −0.86% |
| FX_FGN_CONFIRMATION | 173 | 20 | 35.0% | 15–55% | −0.93% |
| FX_FGN_DIVERGENCE | 124 | 20 | 40.0% | 20–60% | −0.45% |

**每一個區間都跨越 40 個百分點以上，且全部與 baseline 重疊。**
沒有任何一列與雜訊可區分。

---

## M7 — Incremental Value ✅

只問一件事：**加上匯率條件，比單純的外資腿多帶來什麼？**

| interaction | leg | leg win | interact win | **Δ** | 保留 |
|---|---|---|---|---|---|
| FX_DEPR_X_FGN_SELL | FGN_SELL | 36.0% (n=25) | 26.7% (n=15) | **−9.3** | 40% |
| DXY_UP_X_FGN_SELL | FGN_SELL | 36.0% (n=25) | 28.6% (n=14) | **−7.4** | 39% |
| RESIDUAL_TWD_WEAK_X_FGN_SELL | FGN_SELL | 36.0% (n=25) | 36.4% (n=11) | +0.4 | 28% |
| FX_FGN_CONFIRMATION | FGN_SELL | 36.0% (n=25) | 35.0% (n=20) | −1.0 | 59% |
| FX_APPR_X_FGN_BUY | FGN_BUY | 33.3% (n=24) | 30.0% (n=10) | −3.3 | 23% |

**沒有一項為正**（residual 的 +0.4pp 在 n=11 上是純雜訊）。

上一輪報告的 **FX_DEPR_X_FGN_SELL「+5.3pp 增量」→ 現在是 −9.3pp。**

### 為什麼會翻轉

1. **選擇偏誤消除**：外資資料從 360 天增至 537 天，先前缺的日子系統性偏向台幣升值
2. **重疊觀測值移除**：n 從 116 降到 15

兩者都不是調參數，都是修正錯誤。

---

## Regime 分層

`FGN_SELL` 上一輪「兩個 regime 同號」的穩定性也消失了：

| condition | UP eff20 | UP win | DOWN eff20 | DOWN win |
|---|---|---|---|---|
| BASELINE | 9 | 33.3% | 17 | 47.1% |
| FGN_SELL | 9 | 33.3% | 16 | 31.2% |
| FX_DEPR_X_FGN_SELL | 5 | 20.0% | 10 | 20.0% |

**分層後 effective N 只剩 5–17。** 這個數量無法支撐任何 regime 結論 ——
不是「效果在某個 regime 存在」，是「這份資料切一半之後什麼都測不到」。

---

## 統計信心：資料本身不足以回答這個問題

這是本輪最重要的發現，比任何一列數字都重要：

> **538 個交易日 × 20 日前瞻視窗 = 約 26 個獨立觀測值。**
> 要偵測 5–10 個百分點的勝率差異，需要數百個獨立觀測值。
> **這份資料集少了一到兩個數量級。**

延長樣本期間也解決不了：即使十年資料，20 日視窗也只給約 125 個獨立觀測值。
市場級日頻訊號 × 長前瞻視窗，本質上就是一個樣本稀少的問題。

其餘限制：單一 26 個月期間、倖存者偏誤、baseline 本身 20d 勝率僅 42.3%
（中位股大多下跌的一段期間）、regime proxy 僅控制一階趨勢。

---

## Causality

| 測試 | 守住什麼 |
|---|---|
| `TestMetricsAsOfIsCausal` | 台灣 D 日只用 FX ≤ D−1 |
| `TestFXStudyIsCausal` | 跳空出現在隔日而非當日 |
| `TestDXYAndResidualAreCausal` | DXY 與殘差同契約；截斷未來不改變過去 |
| `TestDecompositionExcludesThePoint` | 被解釋點不參與自己的擬合 |
| `TestDecompositionIsCausal` | 回歸只用嚴格早於當日的資料 |
| `TestNonOverlappingRespectsTheForwardWindow` | 20 連續日 @ h=20 → 1 個觀測值 |
| `TestDirectionIsNotInverted` | USDTWD ↑ = 台幣貶值 |
| `TestDXYContextIsAboutTheDollar` | DXY 讀數不得帶 TWD_ 前綴 |
| `TestForeignFlowZeroIsNotMissing` | 存下的 0 ≠ 缺漏 |
| `TestNoDXYArchiveLeavesDollarRowsEmpty` | 無 DXY 封存時不以匯率頂替 |

---

## Production 影響

```
scanner score changed:     NO
scanner decision changed:  NO
sort changed:              NO
```

`go list -deps ./internal/scanner/` 確認對 `internal/fx` **零依賴**。
FX 只出現在報表頁首一行，且有 regression guard 斷言分頁內每一列位元相同。

---

## 最終判定

| 問題 | 判定 | 依據 |
|---|---|---|
| USD/TWD 有獨立增量資訊？ | **REJECTED** | 交互作用 −9.3pp；單獨條件 CI 全數跨越 baseline |
| DXY 有獨立增量資訊？ | **REJECTED** | DXY_UP_X_FGN_SELL −7.4pp；單獨 20.0%（CI 0–40%） |
| Residual TWD 有獨立增量資訊？ | **INCONCLUSIVE** | +0.4pp，n=11 —— 太小，無法判定有或無 |
| FX × Foreign Flow 有增量資訊？ | **REJECTED** | 五個交互作用無一為正 |

### 上一輪的線索已被否定

「貶值 × 賣超在賣超之上再加 +5.3pp」**不成立**。修掉選擇偏誤與重疊計數後是 **−9.3pp**。

### 若要再追

不建議。這不是資料品質問題（覆蓋已達 99.8%），是**樣本量的根本限制**。
要讓這個問題可回答，需要改變問題本身 —— 例如較短的前瞻視窗（會有更多獨立觀測值，
但回答的是不同的問題），或事件型而非日頻的條件。

那是另一個 research topic，不是本 branch 的延伸。
