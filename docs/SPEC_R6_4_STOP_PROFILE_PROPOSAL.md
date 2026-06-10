# R6-4 Stop Profile Proposal（停損 Profile 提案）

> **Docs only — no code, no config default, no scanner / report change.**
> 本文件僅提出 stop profile 的「候選命名與取捨」整理，不修改任何程式碼、不調整 `configs/config.yaml` 預設值、
> 不改變 live scanner / report 行為。
> 上游：`internal/r6backtest`、`docs/SPEC_R6_3_STOP_BENCHMARK_INTERPRETATION.md`、`docs/SPEC_R6_BACKTEST_INTERPRETATION.md`。

---

## 0. 文件定位（務必先讀）

```text
這是 proposal，不是定案。
R6 是離線回測與策略研究，不是自動交易系統。
不自動下單、不接 broker、不替使用者執行交易。
ATR_3 / PCT_15 是候選，不是正式預設。
```

重點宣告：

1. **本文件是 proposal，不是定案。** 以下任何 profile 命名僅為討論用，未被採用為任何預設。
2. **BASELINE 目前不改。** 現行停損維持 `BREAK_MA60 + PCT_-10`，本提案不更動之。
3. **ATR_3 / PCT_15 是候選，不是正式預設。** 需跨 regime 驗證後才考慮定案。
4. **live scanner 預設不改。** 任何 profile 若採用，先做 opt-in，不動既有預設行為。
5. **資料窗 2024-06 → 2026-06 偏多頭 / 回升**，結果不可直接外推；空頭 / 殺盤期停損保護價值會上升。
6. **定案前需 fresh / 全市場 / 跨 regime 再驗證。**

---

## 1. 提案 Profile 對照

下表只是「候選命名 + 取捨」整理，依據 R6-3 stop benchmark（10 setup × 9 policy）的對照結論，
**不是策略決定，也不是預設變更**。

| Profile | 對應 policy | 優點 | 缺點 | 性質 |
|---|---|---|---|---|
| **CONSERVATIVE** | `BASELINE` = `BREAK_MA60 + PCT_-10` | 回撤最低（realized drawdown 最淺） | stop_hit 過高、容易在正常震盪中被洗出 | 現行 / 不改 |
| **BALANCED** | `ATR_3` | 報酬 / 回撤較均衡，stop-adjusted return 最一致 | 需要 ATR 計算（規則較複雜） | 候選 |
| **SIMPLE** | `PCT_15` | 規則簡單、容易解釋 | 深回檔表現不如 ATR_3 | 候選（簡化備案） |
| **RESEARCH** | `NO_STOP` | 本資料窗報酬最高（60d 全面最高） | 尾部回撤最大（dd_p90 最深），不適合直接當實務 stop | 研究對照，非實務 stop |

---

## 2. 各 Profile 說明

### 2.1 CONSERVATIVE = BASELINE（`BREAK_MA60 + PCT_-10`）
- **優點：** 在所有 setup 中 realized drawdown 最淺，下檔保護最強。
- **缺點：** 每個 setup 的 stop_hit_rate 最高、stop-adjusted return 最差；
  在 VCP retest / 正常收斂震盪中容易被洗出。
- **定位：** 現行預設，**本提案不更動**。

### 2.2 BALANCED = ATR_3
- **優點：** 含停損者中報酬最佳或並列最佳、`stop_saved_or_hurt_delta` 最小、
  stop_hit 中等、回撤介於 BASELINE 與 NO_STOP 之間 → 風險 / 報酬折衷最一致。
- **缺點：** 需要 ATR 計算，規則比固定百分比複雜、較難口頭解釋。
- **定位：** **候選**，不是正式預設。

### 2.3 SIMPLE = PCT_15
- **優點：** 固定百分比、規則簡單、容易解釋與檢查；淺回檔表現與 ATR_3 接近。
- **缺點：** 深回檔表現略遜 ATR_3。
- **定位：** **候選**（簡化備案），不是正式預設。

### 2.4 RESEARCH = NO_STOP
- **優點：** 本資料窗（偏多頭）60d 報酬全面最高。
- **缺點：** dd_p90 尾部回撤最大；報酬優勢「並非免費」，**不適合直接當實務 stop**。
- **定位：** 研究 / 對照用途，非實務停損。

---

## 3. 重要限制

1. **資料窗 2024-06 → 2026-06 偏多頭 / 回升。**
   此 regime 結構性偏好 NO_STOP / 寬停損；持續空頭時停損保護價值會上升。
   故「NO_STOP 報酬最高」**不可直接外推**。
2. **本文件僅為命名與取捨整理，未做任何採用決定。**
   是否新增 / 採用任一 profile，須先過 fresh / 全市場 / 跨 regime 驗證。
3. **結果是 backtest / analysis，不是交易指令。**
   所有取捨皆為歷史對照，非未來保證，非下單建議。

---

## 4. 後續（仍為提案，不在本 commit 執行）

1. **fresh data 再跑一次**（不依賴既有 `.cache`）。
2. **跨 regime 驗證**（補強空頭 / 殺盤期）。
3. **若採用，先做 opt-in 參數**（以 BALANCED=ATR_3 為主、SIMPLE=PCT_15 為簡化備案），**不動預設**。
4. **CONSERVATIVE=BASELINE 維持現行預設**，未經跨 regime 驗證前不更換。
5. **不要直接改 live scanner 預設。**

---

## 5. 最終定位宣告（務必保留）

```text
這是 proposal，不是定案。
R6 是離線回測與策略研究，不是自動交易系統。
不自動下單、不接 broker、不替使用者執行交易。
BASELINE 目前不改；ATR_3 / PCT_15 是候選，不是正式預設；live scanner 預設不改。
```

---

*（本文件為 R6-4 停損 profile 提案，docs only，不修改任何程式碼、設定、scanner 或 report。
所有 profile 命名與「優點 / 缺點」皆為對照整理，最終是否採用須以台股 fresh / 全市場 / 跨 regime 資料驗證為準。）*
