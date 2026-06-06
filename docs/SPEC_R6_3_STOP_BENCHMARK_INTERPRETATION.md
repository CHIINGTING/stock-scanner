# R6-3 Stop Policy Benchmark — Interpretation（解讀）

> **Docs only — no code, no config default, no scanner logic change.**
> 本文件不修改任何程式碼、不調整 `configs/config.yaml` 預設值、不改變 scanner / report 行為。
> 上游：`internal/r6backtest`（commit `86c6689`）、`docs/SCANNER_ENHANCEMENT_PLAN.md`、
> `docs/SPEC_R5_3_PARAMETER_BENCHMARK.md`。

---

## 0. 文件定位（務必先讀）

```text
這是 benchmark interpretation，不是 stop policy 定案。
ATR_3 / PCT_15 是候選，不是正式預設。
BASELINE 目前仍不改。
live scanner / config / report 都不改。
```

重點宣告：

1. **R6-3 是 benchmark，不是定案。** 以下任何「較佳」都是對照結論，不是策略決定。
2. **BASELINE（BREAK_MA60 + PCT_−10）在目前資料中偏嚴、容易洗出**（stop_hit 高、報酬最差）。
3. **ATR_3 是目前較穩健的風險 / 報酬折衷候選。**
4. **PCT_15 是較簡單的候選。**
5. **NO_STOP 報酬最高，但尾部回撤最深。**
6. **樣本期 2024-06～2026-06 偏多頭 / 回升，不能外推所有市況。**
7. **定案前需 fresh / 全市場 / 跨 regime 再驗證。**
8. **不自動下單、不接 broker、不影響 live scanner**；輸出只談候選 / 勝率 / 風險 / 參考進場區 / stop policy comparison。

---

## 1. 範圍與資料

- 來源：`internal/r6backtest` 的 `-stopbench`，跑既有 `.cache`（read-only，無 Yahoo）。
- 母體：1973 檔；覆蓋 **2024-06-03 → 2026-06-05**；warmup 250；horizons 5/10/20/60；entry = i+1 open。
- 7 setups（A_MA20 / A_MA60 / B_PULLBACK_5/8/10/15/20）× 9 stop policies。
- 統計語意：**stop-adjusted return 為主**（horizon 前命中停損則以 stop price 計）、**hold-to-horizon 為對照**；
  `stop_saved_or_hurt_delta = avg_stop_adjusted − avg_hold`（正=保護、負=過早洗出）；
  `dd_avg / dd_p90` 為 **stop-aware realized drawdown**（只算到出場/停損為止，故各 policy 不同）。

---

## 2. 各 setup 的 20d / 60d 表現（avg return %；粗體 = 該 setup 最佳）

| setup | 指標 | BASELINE | PCT_10 | PCT_12 | PCT_15 | ATR_2 | ATR_3 | SWING | MA60c | NO_STOP |
|---|---|---|---|---|---|---|---|---|---|---|
| A_MA20 | 20d | 3.0 | 3.3 | 3.7 | 4.1 | 3.4 | 4.1 | 4.1 | 3.7 | **4.4** |
| A_MA20 | 60d | 5.0 | 7.6 | 8.8 | 10.0 | 7.0 | 9.9 | 9.3 | 6.5 | **13.1** |
| A_MA60 | 20d | 1.3 | 2.5 | 2.6 | 2.8 | 2.4 | **2.8** | 2.6 | 1.9 | **2.8** |
| A_MA60 | 60d | 1.5 | 3.4 | 3.5 | 3.6 | 2.7 | 3.8 | 3.3 | 2.4 | **4.2** |
| B_5 | 60d | 5.3 | 8.9 | 10.8 | 13.6 | 9.3 | 14.7 | 15.0 | 10.3 | **21.0** |
| B_8 | 60d | 5.0 | 9.3 | 11.4 | 14.4 | 10.7 | 16.4 | 15.3 | 9.9 | **22.5** |
| B_10 | 60d | 4.9 | 9.4 | 11.4 | 14.4 | 11.2 | 16.7 | 14.6 | 9.5 | **22.9** |
| B_15 | 60d | 2.7 | 8.5 | 10.1 | 13.5 | 11.1 | 17.7 | 12.4 | 6.4 | **23.1** |
| B_20 | 60d | 1.4 | 8.7 | 11.3 | 14.4 | 13.0 | 19.5 | 5.2 | 5.2 | **24.3** |

> dd_p90（realized）對照：BASELINE 最淺（約 −6.5 ~ −13.8%），NO_STOP 最深（約 −20.5 ~ −26.2%），
> ATR_3 / PCT_15 居中（約 −16 ~ −21%）。

---

## 3. 報酬最高

**NO_STOP 在 7 個 setup 的 60d 報酬全部最高**；20d 除 A_MA60（與 ATR_3 並列）外亦最高。
**含停損者中報酬最佳 = ATR_3**（僅 B_5 由 SWING_LOW 微幅領先）。

## 4. 回撤最低（stop-aware realized drawdown）

**BASELINE 全部最低**（如 A_MA60 −2.9 / −6.5、B_20 −7.1 / −13.6），**MA60_CONFIRM 次低**。
→ 停損在「壓低回撤」這軸確實有效；BASELINE 的唯一優點就是回撤最小。

## 5. stop_hit_rate 過高（過早洗出）

- **過緊**：BASELINE 72.8–92.7%、MA60_CONFIRM 58.9–88.3%、ATR_2 36–62%。
- **合理**：ATR_3 18.5–46.6%、PCT_15 18–31%、SWING_LOW 28–40%。

## 6. ATR_3 是否多數 setup 的穩定折衷

**是。** 7 個 setup 全部：ATR_3 為含停損者中報酬最佳或並列最佳、`delta` 最小（−0.5 ~ −0.7）、
stop_hit 中等、回撤介於 BASELINE 與 NO_STOP 之間 → **最一致的風險 / 報酬折衷候選**。

## 7. PCT_15 是否接近 ATR_3

**淺回檔接近、深回檔落後。** A_MA20 / B_5 兩者 60d 幾乎並列；但 setup 越深 ATR_3 拉開
（B_15：17.7 vs 13.5；B_20：19.5 vs 14.4），回撤相近。→ **PCT_15 是「更簡單」的次選**，
deep-pullback 上略遜 ATR_3。

## 8. NO_STOP 的報酬優勢是否伴隨不可接受回撤

**優勢伴隨最深尾部回撤。** NO_STOP 比 ATR_3 多賺約 3–5%（60d），但 dd_p90 多承受約 4–6%
（B_20：報酬 24.3% vs 19.5%，dd_p90 −25.2% vs −21.6%）。對「波段、需控回撤」的定位，
ATR_3 以較小報酬犧牲換明顯較淺的尾部風險 → **NO_STOP 的報酬優勢並非「免費」**。

## 9. BASELINE 是否應降級為「過嚴 stop」

**從 benchmark 看：是。** BASELINE 在每個 setup 都是報酬最差的停損、stop_hit 最高（洗出），
唯一優點（最低回撤）僅略勝 ATR_3 卻付出大幅報酬代價。**標記為「過嚴 / 過早洗出」，但本階段不改預設。**

## 10. 是否建議後續新增 default stop profile（先不改）

**方向（待驗證、暫不實作）**：未來可考慮新增以 `ATR_3` 為主、`PCT_15` 為簡化備案的 default stop
profile 候選；BASELINE 保留為「保守 / 低回撤」選項。**前提：必須在 fresh / 全市場資料、跨 regime
再驗證後才定案。** 本文件不觸發任何此類變更。

---

## 11. 回測偏誤提醒（務必保留）

樣本期 **2024-06 → 2026-06 以多頭 / 回升為主**（僅 2025-04 一次大跌），此 regime
**結構性偏好 NO_STOP / 寬停損**。在持續空頭，停損的保護價值會明顯上升。
因此「NO_STOP 報酬最高」**不可外推到所有市況**；ATR_3 之所以較穩健，正因它在報酬與尾部風險間
較平衡、對 regime 較不敏感。

---

## 12. 結論

- R6-3 是 **benchmark**，不是定案。
- **ATR_3 / PCT_15 是候選**（ATR_3 較全面，PCT_15 較簡單）。
- **BASELINE 不變**；不改 live scanner、不改正式 config、不改 report。
- 定案前需 **fresh / 全市場 / 跨 regime** 再驗證。
- 不自動下單、不接 broker；本模組為決策支援。

---

*（本文件為 R6-3 stop policy benchmark 之解讀，docs only，不修改任何程式碼、設定或 scanner 邏輯。
所有「較佳 / 候選」皆為對照結論，最終 stop policy 須以台股 fresh / 全市場 / 跨 regime 資料驗證為準。）*
