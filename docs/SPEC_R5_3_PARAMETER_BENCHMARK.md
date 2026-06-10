# R5-3 Reference Parameter Benchmark（參數校準基準表）

> **Docs only — no code, no config default, no scanner logic change.**
> 本文件不修改任何程式碼、不調整 `*.yaml` 預設值、不改變 scanner 行為。
> 上游：`docs/SCANNER_MASTER_DESIGN.md`、`docs/SCANNER_ENHANCEMENT_PLAN.md`、
> `SPEC_R1_RS_RANKING.md`、`SPEC_R2_NEWHIGH_VCP.md`、`SPEC_R3_MOMENTUM_FLOW.md`、
> 以及 `INTEGRATION_CHECKLIST_R1_R3.md` §8「風險與待校準項目」。
> 定位不變（EOD 波段、1~4 週、持有 5~20 日）。

---

## 0. 文件定位（務必先讀）

```text
Reference Parameter Benchmark 是 calibration baseline，不是最終參數。
FinLab / TradingView / 經典技術分析只作為市場慣例參考。
最終參數必須用我們自己的台股 watchlist / 回測資料驗證。
```

換句話說，本表的角色與「不是什麼」：

| 是什麼 | 不是什麼 |
|--------|----------|
| 校準的**起跑線**（baseline / 先驗區間） | ❌ 最終定案參數 |
| 市場慣例的**參考座標**，幫助判斷我們的預設是否離譜 | ❌ 「照抄 FinLab」 |
| R5 第一梯次回測的**對照組來源** | ❌ 「照抄 TradingView」 |
| 收斂搜尋空間、減少純拍腦袋 | ❌ 「直接採用外部參數」 |

> 任何外部數值進入本專案前，都必須經過**台股 watchlist / 回測資料**驗證
> （Win Rate / Avg Return / Profit Factor / Max DD，含交易成本，forward 5/10/20 日）。
> 外部慣例與台股實證衝突時，**以台股實證為準**。

---

## 1. Scope & 用法

- **範圍**：把 `INTEGRATION_CHECKLIST_R1_R3.md` §8 列出的待校準項目，逐一補上
  「市場慣例參考值（baseline）」與「建議掃描區間」，作為 R5 校準的對照組。
- **不在範圍**：實際回測結果、最終定案數值、config 改動、新因子。
- **用法**：R5 校準時，對每個參數
  1. 取本表 baseline 作為**對照組**（control）；
  2. 在「建議掃描區間」內以台股資料 grid / walk-forward 搜尋；
  3. 用我方驗收指標選定，並回寫各 SPEC 的「待校準」段落。

> 欄位說明：
> **目前預設** = 專案現行起點值（見各 SPEC / config）。
> **市場慣例 baseline** = FinLab / TradingView / 經典 TA 常見值（僅參考）。
> **建議掃描區間** = R5 在台股資料上實測的搜尋範圍（仍待驗證）。
> **來源慣例** = 該 baseline 的出處脈絡（非背書，不代表照抄）。

---

## 2. R1 — Relative Strength / RS Ranking

| 參數 | 目前預設 | 市場慣例 baseline | 建議掃描區間 | 來源慣例（僅參考） |
|------|----------|-------------------|--------------|---------------------|
| **RS Rank gate（百分位）** | 80 | RS Rating ≥ 80（強勢門檻）；趨勢樣板放寬到 ≥ 70 | 70 / 75 / 80 / 85 | IBD RS Rating、Minervini Trend Template 慣例 |
| **RS 回看權重窗** | 多窗加權（見 R1 SPEC） | 3 / 6 / 9 / 12 月加權（近月權重高） | 對台股縮短：1 / 3 / 6 月為主 | 經典相對強度的多週期加權 |
| **母體** | 全市場 | 同板塊 / 全市場兩種口徑 | 全市場 vs 上市櫃分流比較 | 相對強度需明確 universe |

> 台股備註：中小型股流動性與漲跌停制度，使極端百分位易被少數標的扭曲；
> RS 門檻須與流動性過濾**一起**校準，不可單獨沿用美股 80 的慣例。

---

## 3. R2 — New High Analysis & VCP

### 3.1 New High / 52 週位置

| 參數 | 目前預設 | 市場慣例 baseline | 建議掃描區間 | 來源慣例（僅參考） |
|------|----------|-------------------|--------------|---------------------|
| **52 週高點距離 bucket** | ≤10% / ≤25% / ≤50%；leader_far 50% | 距 52 週高 ≤ 25% 視為強勢；距 52 週低 ≥ 30% | 邊界 10/15/20/25/30% 比較 | Minervini Trend Template |
| **New High 視窗** | 20 / 60 / 120 / 250 日 | 多週期新高（季 / 半年 / 年） | 維持，校準各窗權重 | 經典 breakout / 箱型突破 |

### 3.2 VCP 品質（Volatility Contraction Pattern）

| 參數 | 目前預設 | 市場慣例 baseline | 建議掃描區間 | 來源慣例（僅參考） |
|------|----------|-------------------|--------------|---------------------|
| **min_contractions** | 2 | 2–6 次收縮，逐次收斂 | 2 / 3 | Minervini VCP |
| **final_tight（末段收縮幅度）** | 6% | 末段 ~3–10%（越緊越佳） | 4 / 6 / 8% | Minervini VCP |
| **loose（初段收縮上限）** | 15% | 首段可達 ~25%，逐段收斂 | 12 / 15 / 20% | VCP 收斂結構 |
| **min_score** | 50 | 無外部標準（專案自訂分數） | 40 / 50 / 60 | 內部分數，純台股校準 |
| **VCP bonus 上限** | +12 | — | 檢查與 PriceCompression 是否重疊 | 防雙重計分 |
| **量縮確認** | 見 R2 SPEC | 收縮末段量能乾涸（volume dry-up） | 與漲停鎖量 guard 一致 | VCP 量能特徵 |

> 台股備註：漲停 / 跌停與當沖比重會扭曲「量縮」判讀；
> VCP 量能門檻務必與 R3 的「量縮≠轉弱」guard（漲停鎖量）對齊，避免誤判。

---

## 4. R3 — MomentumFlow 與聯合決策

| 參數 | 目前預設 | 市場慣例 baseline | 建議掃描區間 | 來源慣例（僅參考） |
|------|----------|-------------------|--------------|---------------------|
| **stage adjustment 權重** | building+5 / cont+6 / shiftup+8 / fading−6 / shiftdown−12；cap 12 | 無外部標準（專案自訂修正量） | 各態 ±3 ~ ±15，cap 8/12/15 | 純台股勝率差校準 |
| **momentum accel 正/負門檻** | `accel_pos/neg_thresh` | 依價格尺度，無通用值 | 改百分比步調或 ATR 正規化後掃描 | 高度依賴價格尺度 |
| **accel_scale** | 既有 | — | 正規化後重訂 | 同上 |
| **RSI（背離 / 動能輔助）** | 見 R3 SPEC | 週期 14；超買 70 / 超賣 30 | 週期 9 / 14；門檻 70/80、20/30 | TradingView / Wilder 預設 |
| **MACD（動能輔助）** | 見 R3 SPEC | 12 / 26 / 9 | 維持，必要時縮短測快訊號 | TradingView / 經典 MACD 預設 |
| **zigzag_pct（R2/R3 共用）** | 3.0% 固定 | TradingView ZigZag 預設 5% | 3 / 5%，或自適應（base range 比例） | TradingView ZigZag |

> 台股備註：`accel_*` 與 zigzag 百分比對台股價格尺度最敏感（高低價股不公平），
> 是本表中**最須以台股資料重訂、最不可照搬外部**的一組。

---

## 5. R4 — 多週期 / 長期濾網

| 參數 | 目前預設 | 市場慣例 baseline | 建議掃描區間 | 來源慣例（僅參考） |
|------|----------|-------------------|--------------|---------------------|
| **均線堆疊（趨勢背書）** | 見 R4 | 50 / 150 / 200 日多頭排列；價 > 50 > 150 > 200 | 台股常用 20 / 60 / 120 / 240 對照 | Minervini Trend Template |
| **長期濾網（空方風險）** | 跌破 200 日 MA 提示 | 收盤 < 200MA 視為弱勢 | 200 日 vs 台股 240 日（年線） | 經典長期趨勢濾網 |
| **WeeklyConfirm（週線同向）** | 見 R4 | 週線方向一致才加分 | 校準加分量 | 多週期一致性 |

> 台股備註：台股慣用「年線 = 240 日」而非美股 200 日；
> 兩者皆列為對照，最終以台股回測選定。

---

## 6. 出場 / 風險（R5–R6 校準，先列 baseline）

| 參數 | 目前預設 | 市場慣例 baseline | 建議掃描區間 | 來源慣例（僅參考） |
|------|----------|-------------------|--------------|---------------------|
| **ATR 週期** | 見 SPEC | 14 | 10 / 14 / 20 | Wilder ATR 預設 |
| **停損距離** | ATR×2 / 0.93（寫死） | ATR×2~3；吊燈出場 3×ATR | 用 MAE / MFE 校準（R6） | Chandelier / ATR stop 慣例 |
| **forward window（驗證）** | 5 / 10 / 20 日 | 與持有 1~4 週對齊 | 維持 | 與波段定位一致 |

> 停損為**寫死值**，明確待 R6 第二梯次以 MAE / MFE 校準，本表僅列慣例對照。

---

## 7. 系統 / 資料品質（沿用 §8，非外部慣例）

> 以下屬內部工程參數，**無外部市場慣例可參考**，列出僅為校準完整性，
> 應以台股資料與營運觀測決定，不引用 FinLab / TradingView。

| 參數 | 目前預設 | 校準 / 觀測方式 | 風險 |
|------|----------|------------------|------|
| **backtest confidence sample count** | sector≥30 / stock≥15 HIGH… | R5 一併檢視與 forward window 互動 | 樣本不足→信心虛高 |
| **rocket_candidate_score 分帶** | 60 / 75 / 90… | R5 校準各帶實際發動率 | 加新因子後分帶失準 |
| **short_term_flow score threshold** | 既有（rotation 常數） | R5 一併檢視 | 既有，非 R1–R3 引入 |
| **snapshot_max_age_days** | 7（calendar） | 連假後改交易日計 | fast mode 用到過舊母體 |
| **adjclose 缺值比例** | null 退 raw | 監測 null 率，必要時加品質旗標 | RS / 新高失真 |

---

## 8. 校準流程（R5 如何使用本表）

1. **建立對照組**：以「市場慣例 baseline」跑一輪台股回測，作為 control。
2. **搜尋**：在「建議掃描區間」內 grid / walk-forward，避免過度擬合（留 out-of-sample）。
3. **選定**：用我方驗收指標（Win Rate / Avg Return / Profit Factor / Max DD，含成本）。
4. **回寫**：把定案值寫回對應 SPEC 的「待校準」段落，標注「已由台股資料校準」。
5. **衝突處理**：外部慣例 vs 台股實證衝突 → **以台股實證為準**，並記錄差異原因。

---

## 9. 驗收（本文件本身）

1. 不含任何程式碼 / config / report 變更（純 docs commit）。
2. 文件定位段（§0）清楚標明 baseline ≠ 最終參數，且須台股資料驗證。
3. 涵蓋 §8 全部待校準項目，每項標明 baseline 與掃描區間。
4. 外部來源僅作「參考慣例」，無「照抄 / 直接採用」字樣。

---

*（本文件為 R5-3 參數校準基準表，docs only，不修改任何程式碼、設定或 scanner 邏輯。
所有外部數值僅為 calibration baseline，最終參數以台股 watchlist / 回測資料驗證為準。）*
