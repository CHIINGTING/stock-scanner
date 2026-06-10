# R6 Backtest — Final Interpretation（完整回測結論）

> **Docs only — no code, no config default, no scanner / report change.**
> 本文件僅整理 R6 系列回測的最終解讀，不修改任何程式碼、不調整 `configs/config.yaml` 預設值、
> 不改變 live scanner / report 行為。
> 上游：`internal/r6backtest`（最新 commit `2687d58`）、`docs/SPEC_R6_3_STOP_BENCHMARK_INTERPRETATION.md`、
> `docs/SCANNER_ENHANCEMENT_PLAN.md`、`docs/SPEC_R5_3_PARAMETER_BENCHMARK.md`。

---

## 0. 文件定位（務必先讀）

```text
R6 是離線回測與策略研究，不是自動交易系統。
所有結論僅作為候選策略與風險評估依據。
不自動下單、不接 broker、不替使用者執行交易。
```

重點宣告：

1. **R6 是 analysis / backtest，不是定案策略，更不是交易指令。**
2. 以下任何「較佳 / 較強 / 較穩健」都是**對照結論**，不是策略決定。
3. **BASELINE stop 不變**；ATR_3 / PCT_15 只是候選，不是正式預設。
4. **Setup D 是 case study**，event_count 極少、不可外推。
5. 樣本期 **2024-06 → 2026-06 偏多頭 / 回升**，所有結論皆受此 regime 偏誤限制。
6. **定案前需 fresh / 全市場 / 跨 regime 再驗證。**

---

## 1. R6 覆蓋範圍

| 模組 | 內容 | 性質 |
|---|---|---|
| **Setup A** | MA20 / MA60 pullback（回測拉回至均線後續表現） | 策略候選 |
| **Setup B** | 52 週高後回檔 5 / 8 / 10 / 15 / 20% | 策略候選 |
| **Setup C** | 真實 VCP retest（量縮收斂後回測突破點） | 策略候選 |
| **Setup D** | crash-regime survivor case study（殺盤中 HIGH_RS 是否相對抗跌） | regime case study |
| **R6-3** | stop policy benchmark（9 種停損對照） | 風險對照 |

- 共同條件：跑既有 `.cache`（read-only，無 Yahoo）；母體約 1973 檔；
  覆蓋 **2024-06-03 → 2026-06-05**；warmup 250；horizons 5 / 10 / 20 / 60d；entry = i+1 open。
- 統計語意：**stop-adjusted return 為主**、**hold-to-horizon 為對照**；
  drawdown 為 **stop-aware realized drawdown**（只算到出場 / 停損為止）。

---

## 2. 主要結論

1. **A_MA20 優於 A_MA60。**
   MA20 pullback 在各 horizon 的勝率與報酬均優於 MA60 pullback；
   靠近 MA20 的回檔買點品質較高、回升較快。

2. **B 深回檔 15–20% 的 hold return 較強。**
   52 週高後較深的回檔（15–20%）在 hold-to-horizon（尤其 60d）報酬較高，
   但同時更依賴「不被洗出」——即停損政策對深回檔的影響最大（見第 3 節）。

3. **C_VCP_MA20 樣本足夠，但 baseline stop 對 VCP retest 過嚴。**
   真實 VCP retest 的樣本數足以判讀，方向有效；
   但 BASELINE 停損在 VCP retest 上 stop_hit 偏高、容易在正常收斂震盪中被洗出，
   壓抑了 stop-adjusted return。

4. **D 顯示 HIGH_RS 在殺盤中相對抗跌，但 event_count 極少、不可外推。**
   crash-regime 下 HIGH_RS cohort 的相對報酬（vs market proxy）優於 LOW_RS，
   方向符合「強者抗跌」直覺；但 **event_count 極少**（本批次集中於 2025 春季與 2026-03 邊際事件），
   confidence 永遠 LOW，**僅作 regime case study，不可外推**。

---

## 3. Stop benchmark 結論（R6-3）

1. **BASELINE stop 偏嚴、容易洗出。**
   BASELINE（BREAK_MA60 + PCT_−10）在每個 setup 都是報酬最差的停損、stop_hit_rate 最高；
   唯一優點是 realized drawdown 最淺。

2. **ATR_3 是目前較穩健的候選。**
   含停損者中報酬最佳或並列最佳、`stop_saved_or_hurt_delta` 最小、
   stop_hit 中等、回撤介於 BASELINE 與 NO_STOP 之間 → 最一致的風險 / 報酬折衷。

3. **PCT_15 是較簡單備案。**
   淺回檔與 ATR_3 接近，深回檔略遜 ATR_3；勝在規則簡單。

4. **NO_STOP 報酬最高，但尾部回撤最大。**
   60d 報酬全面最高，但 dd_p90 最深；報酬優勢「並非免費」。

5. **baseline 不改，ATR_3 / PCT_15 只是候選。**
   本階段不改任何預設停損；候選須跨 regime 驗證後才考慮定案。

> 詳見 `docs/SPEC_R6_3_STOP_BENCHMARK_INTERPRETATION.md`。

---

## 4. 重要限制

1. **資料窗 2024-06 → 2026-06 偏多頭 / 回升。**
   此 regime 結構性偏好 NO_STOP / 寬停損；持續空頭時停損保護價值會上升。
   故「NO_STOP 報酬最高」不可外推。

2. **crash regime 事件數極少。**
   Setup D 的 event_count 極少且集中於少數時點，僅供 case study。

3. **60d forward 樣本少於 5 / 10 / 20d。**
   資料窗尾端不足 60 天的進場無法形成 60d forward，故 60d 結論的樣本數最小、最脆弱。

4. **結果是 backtest / analysis，不是交易指令。**
   所有數字為歷史對照，非未來保證，非下單建議。

---

## 5. 後續建議

1. **fresh data 再跑一次**（重新抓取最新 / 完整資料，不依賴既有 `.cache`）。
2. **跨 regime 驗證**（納入更多空頭 / 殺盤期，特別補強 Setup D 的事件數）。
3. **再決定是否新增 stop profile**（以 ATR_3 為主、PCT_15 為簡化備案，仍須驗證後定案）。
4. **再決定是否把 ATR_3 / PCT_15 做成可選參數**（先做成 opt-in，不動預設）。
5. **不要直接改 live scanner 預設**（所有變更須先過 fresh / 跨 regime 驗證）。

---

## 6. 最終定位宣告（務必保留）

```text
R6 是離線回測與策略研究，不是自動交易系統。
所有結論僅作為候選策略與風險評估依據。
不自動下單、不接 broker、不替使用者執行交易。
```

---

*（本文件為 R6 系列回測的最終解讀，docs only，不修改任何程式碼、設定、scanner 或 report。
所有「較佳 / 候選」皆為對照結論，最終策略與停損政策須以台股 fresh / 全市場 / 跨 regime 資料驗證為準。）*
