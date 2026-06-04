# R2 Implementation Spec — New High Analysis + VCP

> **Design only — no code changes.** 把 `docs/SCANNER_ENHANCEMENT_PLAN.md`（Rev 2）的
> **R2：New High Analysis + Volatility Contraction Pattern** 展開為可實作規格。
> 上游：`docs/SCANNER_MASTER_DESIGN.md`、`docs/SPEC_R1_RS_RANKING.md`。
> 定位不變（EOD 波段、1~4 週、持有 5~20 日）。程式片段為型別 / 公式草圖，標落點檔案。

---

## 1. Overview & Scope

兩個互補的「進場品質」因子，目標是在 R1 篩出的**領漲股**裡，再選出**剛突破 / 收斂到位**者，
降低假突破與過早進場：

- **New High Analysis**：把現有單一「60 日新高」擴充為 **20 / 60 / 120 / 250 日**多週期新高，
  加上**距 52 週高的距離**（領導力閘門），合成 `NewHighScore`。
- **VCP（Volatility Contraction Pattern）**：吸收 Minervini 概念（非照抄），偵測「整理一波比一波緊、
  量一波比一波縮」，由現有 Consolidation 模組擴充出 `VCPScore`，回答「整理是否愈來愈緊」。

**不在範圍**：突破當下的下單觸發（屬使用面）、週線新高（屬 R4）、VCP 的歷史回測勝率（屬 R5）。

---

## 2. Dependencies & Preconditions

| # | 事項 | 現況 | 要求 |
|---|------|------|------|
| D1 | **還原股價** | `Candle.Close` 未還原（見 R1 spec D3） | **New High 一律以「還原收盤」為基準**（見 §3 決策）；依賴 R1.0 的 adjclose 解析。未做 R1.0 時可先用 raw close 並標資料品質旗標 |
| D2 | **歷史長度** | 預設 2y（≈ 480+ 交易日） | 250 日新高需 ≥ 251 根；不足者該旗標 false 並標 partial |
| D3 | **既有 Consolidation** | `analyzeConsolidation` 找單一 tight base（成長視窗，range cap=`0.08+0.004k`），輸出 `PivotHigh/BaseLow/VolumeDryUpRatio/PriceCompressionScore/SupportHoldScore/BaseQualityScore/...` | VCP 在此模組**擴充**，複用 `windowHighLow / avgVolume`，不重寫 |
| D4 | **既有新高邏輯** | `rotation.go: memberSnapshot` 已有 60 日 `NewHigh`、20 日 `NewHigh20`（族群用，以 close 比 prior-high） | 個股 `StockAnalysis` **目前無**新高欄位；R2 補上並統一語意 |

---

## 3. New High Analysis

落點：新檔 `internal/scanner/newhigh.go`；由 `scanner.go: analyze()` 呼叫填入 `StockAnalysis`。

### 3.1 設計決策：以「還原收盤」比價，而非用 High

- 新高判定 = **今日還原收盤 ≥ 前 L 根的還原收盤最高**（排除今日）。
- **理由**：(a) adjclose 只還原收盤，無還原 High/Low（H/L 還原需 adj factor，成本高）；
  以 close 比 close **一致且免受分割扭曲**；(b) 用收盤而非盤中高，對波段更保守、少假新高。
- 52 週高同樣以**還原收盤序列**取 250 根最高（含今日）。

### 3.2 欄位（New High）

`StockAnalysis` 新增（或包成 `NewHigh NewHighInfo`）：

| 欄位 | 型別 | 說明 |
|------|------|------|
| `H20 / H60 / H120 / H250` | bool | 是否創各週期新高（close ≥ 前 L 根 close 最高） |
| `H20Valid…H250Valid` | bool | 該週期是否有足夠歷史（n-1 ≥ L） |
| `High52w` | float64 | 近 250 根還原收盤最高（含今日；不足則用可得根數，標 partial） |
| `PctFrom52wHigh` | float64 | `(close/High52w - 1)*100`，≤ 0；0 = 正在 52 週高 |
| `NewHighScore` | float64 | 0–100 綜合分（見 §3.4） |

### 3.3 計算（公式草圖）

```
ac[] = 還原收盤（oldest-first），close = ac[n-1]
for L in {20,60,120,250}:
    if n-1 >= L:
        priorHigh_L = max(ac[n-1-L .. n-2])
        H{L}      = close >= priorHigh_L
        H{L}Valid = true
    else: H{L}=false; H{L}Valid=false
High52w        = max(ac[max(0,n-250) .. n-1])
PctFrom52wHigh = (close/High52w - 1)*100
```

### 3.4 NewHighScore（0–100）

對 1~4 週波段的取捨（Rev 2 結論）：**60 日新高＝時機核心、距 52 週高＝領導資格、
20 日＝觸發、120 日＝佐證、剛創 250 日高且過熱＝反而扣分**。

```
volConfirm = volRatio >= newhigh.vol_confirm_ratio   # 預設 1.5
s = 0
if H60:  s += 40; if volConfirm: s += 10     # 核心：60 日新高（量增確認）
if H20:  s += 8                               # 即時觸發
if H120: s += 12                             # 中期佐證
# 領導力（距 52 週高）：
switch PctFrom52wHigh:
    >= -10: s += 30
    >= -25: s += 18
    >= -50: s += 6
    else:   s += 0
# 領導力閘門：距 52 週高過遠 → 非領漲，設上限
if PctFrom52wHigh < -leader_far_pct (預設 50): s = min(s, 35)
# 過熱抑制：創 250 日高但已過度延伸（離 MA20 太遠或 RSI 過高）→ 視為追高非起漲
if H250 and (extFromMA20 > newhigh.overext_ma20_pct or RSI >= newhigh.overext_rsi):
    s = round(s * 0.6)
NewHighScore = clamp(s, 0, 100)
```

> 全綠（H20+H60 量增+H120+距高≤10%）時 40+10+8+12+30 = **100**，自然封頂。

### 3.5 如何影響 Score

- **RocketScore 第 3 組「技術接近噴出」（上限 25）改寫**（落點 `rocket.go: g3`）：
  目前 = `BaseQuality/100*12 + NearPreviousHigh(+6) + bullAlign(+4) + justBroke/near(+3)`。
  NewHighScore 已內含「接近前高 + 突破 + 領導力」，故**用它取代 NearPreviousHigh 子項**：

  | 子項 | 配分 | 規則 |
  |------|------|------|
  | BaseQuality | 0–10 | `BaseQualityScore/100*10`（含 VCP 加成，見 §4） |
  | **NewHighScore** | 0–8 | `NewHighScore/100*8`（取代原 NearPreviousHigh +6） |
  | bullAlign | 0–4 | 多頭排列 |
  | justBroke / 逼近 | 0–3 | 剛突破 pivot 或 ≥pivot×0.97 |

  和 ≤ 25，維持組上限。
- **Composite Score 不動**（保市場掃描相容）；新高欄位僅供顯示與 Watchlist 卡。

### 3.6 如何影響 Stage

不重寫 stage 決策樹，只**強化既有條件**（落點 `rocket.go`）：

- `justBroke` 之外，新增**新高佐證**：`H20 || H60` 且在 pivot 附近 → 強化 `BREAKOUT_START` 信心。
- 自有效 base 創 **60 日新高** → 助 `PRE_BREAKOUT → BREAKOUT_START` 過渡。
- `extended/climax` 之外，新增**過熱佐證**：`H250 且過度延伸` → 推向 `OVERHEATED`（與 §3.4 抑制一致）。

---

## 4. Volatility Contraction Pattern (VCP)

落點：擴充 `internal/scanner/consolidation.go`（新增 `analyzeVCP` 與欄位）。

### 4.1 概念與資料來源

- 好的突破前，整理呈現**連續收縮**：回檔深度 15% → 10% → 6% → 4%，且量逐波遞減。
- 僅用日線 OHLCV：以 zigzag 抓 swing 高 / 低，量測每段「峰→谷」回檔深度與量能。

### 4.2 新增欄位（`Consolidation`）

| 欄位 | 型別 | 說明 |
|------|------|------|
| `VCPScore` | float64 | 0–100，收斂品質 |
| `IsVCP` | bool | `VCPScore ≥ vcp.min_score` 且單調性達標 |
| `Contractions` | []float64 | 各段回檔深度 %（oldest-first） |
| `ContractionCount` | int | 段數 |
| `FinalContractionPct` | float64 | 最後一段深度 %（愈小愈緊） |

### 4.3 偵測演算法（zigzag → 收縮腿）

於 VCP 視窗（建議 base 視窗往回擴至 `min(60, n-1)`，涵蓋多段收縮）：

```
threshold = vcp.zigzag_pct (%)   # 預設 3.0；建議可改為「base range 的比例」自適應
1) 以 close（或 H/L）走 zigzag：自起點追極值，當反向回撤 ≥ threshold 確認轉折，
   產生交替的 swing high(SH)、swing low(SL)。
2) 取每個「SH→其後 SL」為一段收縮：depth = (SH - SL)/SH * 100。
3) Contractions = 依時間排列的 depth 序列；ContractionCount = len。
4) 各段均量：legAvgVol_i = avgVolume(SH_i..SL_i)。
```

### 4.4 VCPScore（0–100）

```
if ContractionCount < vcp.min_contractions (預設 2): VCPScore=0; IsVCP=false; return
cCount = clamp((ContractionCount-1)/3, 0, 1)               # 2段→0.33, 4段→1
mono   = (#相鄰 depth_i > depth_{i+1}) / (ContractionCount-1)   # 單調收緊比例
tight  = clamp((vcp.loose_pct - FinalContractionPct) /
               (vcp.loose_pct - vcp.final_tight_pct), 0, 1)     # loose15%→0, tight4%→1
volDry = legAvgVol_last < legAvgVol_first
           ? clamp(1 - legAvgVol_last/legAvgVol_first, 0, 1) : 0
VCPScore = clamp(100*(0.30*cCount + 0.30*mono + 0.25*tight + 0.15*volDry), 0, 100)
IsVCP    = VCPScore >= vcp.min_score (預設 50) && mono >= 0.6
```

> 設計：**單調收緊（mono）與末段緊度（tight）權重最高**——這正是 VCP 與「單純窄幅整理」的差異。

### 4.5 與 BaseQualityScore 整合（加成、不取代）

落點：`analyzeConsolidation` 末段（算完 `BaseQualityScore` 後）。

```
vcpBonus = clamp(VCPScore/100 * vcp.quality_bonus_max, 0, vcp.quality_bonus_max)  # 預設上限 12
BaseQualityScore = clamp(BaseQualityScore + vcpBonus, 0, 100)
```

- VCP 獎勵「**逐步**收斂」，與現有 `PriceCompressionScore`（單一視窗 tightness）互補，非重複；
  但為避免過度疊加，bonus 設上限 12 並可 config。

### 4.6 如何影響 PRE_BREAKOUT 判斷

落點：`rocket.go` 的 `preBreak` 與 `explosionProb`。

- 現行 `preBreak = NearPreviousHigh && BaseQuality≥50 && VolumeDryUpRatio<1.0 && 逼近 pivot && !BrokePlatform`。
- **VCP 升級**：`preBreak && IsVCP` → 設 `HighConfidencePreBreak=true`：
  - `ExplosionProb` 由 MEDIUM **上修為 HIGH**（即使分數略低於原 75 門檻）；
  - `DaysToWatch` 縮短（如 PRE_BREAKOUT 由「1~3 天」→「1~2 天」）；
  - Reasons 加「整理三段收斂 10%→6%→4%、量縮，VCP 成形」。
- **收斂失敗**：`IsVCP` 但末段**放量跌破前低 / 平台**（`BrokePlatform` 或末段 volRatio≥1.5 收黑）
  → 不給 PRE_BREAKOUT，往 `FAILED` / `MOMENTUM_FADING`（R3）看，RiskWarning 標「VCP 失敗」。

---

## 5. New Data Structures（彙總）

1. `StockAnalysis`（types.go）+= New High 欄位（§3.2）。
2. `Consolidation`（consolidation.go）+= VCP 欄位（§4.2）。
3. `rocketInput`（rocket.go）+= `newHighScore float64`（供 g3 / stage 取用；VCP 已在 `consol`）。
4. `WatchlistEntry` 透過 `A` 與 `Consol` 自動帶上述，無需新欄位。

---

## 6. Proposed Modules / Touch Points

| 檔案 | 動作 |
|------|------|
| `internal/scanner/newhigh.go` | **新**：新高旗標、52 週距離、NewHighScore |
| `internal/scanner/scanner.go` | `analyze()` 填入 New High 欄位（用還原收盤）+ 傳 newHighScore 給 watchlist/rocket |
| `internal/scanner/consolidation.go` | **擴充**：`analyzeVCP` + VCP 欄位 + BaseQuality 加成 |
| `internal/scanner/rocket.go` | g3 改寫、preBreak/explosionProb VCP-aware、stage 新高佐證 |
| `configs/config.yaml` | 新增 `scanner.newhigh` / `scanner.vcp` 區段 |
| `internal/report/report.go` | 卡片新增 NewHighScore / VCP 標籤（可選） |

原則：以新增 + 擴充為主，不重寫既有函式主體。

---

## 7. Config Additions

```yaml
scanner:
  newhigh:
    enabled: true
    lookbacks: [20, 60, 120, 250]
    vol_confirm_ratio: 1.5       # 60 日新高的量增確認門檻
    leader_within_pct: 25        # 距 52 週高 ≤ 此值＝具領導力（影響分級）
    leader_far_pct: 50           # 距 52 週高 > 此值＝非領漲，封頂
    overext_ma20_pct: 20         # 創 250 日高且離 MA20 超過此% → 過熱抑制
    overext_rsi: 75
  vcp:
    enabled: true
    zigzag_pct: 3.0              # swing 反轉門檻（太小=雜訊、太大=漏末段）
    min_contractions: 2          # 最少收縮段數（理想 3+）
    final_tight_pct: 6.0         # 末段目標緊度
    loose_pct: 15.0             # 緊度縮放上界
    min_score: 50              # IsVCP 門檻
    quality_bonus_max: 12       # 加入 BaseQualityScore 的上限
```

`enabled:false`（任一）→ 該因子完全停用、相關欄位零值、評分回原狀（向後相容）。

---

## 8. Edge Cases

| 情境 | 處理 |
|------|------|
| 歷史 < 251 根 | `H250Valid=false`、`High52w` 用可得根數並標 partial |
| 無 base（NoBase） | 不算 VCP（VCPScore=0、IsVCP=false） |
| 整理震盪無清楚 pivot | 收縮段 < min → VCPScore=0 |
| 區間**擴張**（VCP 反例） | mono 低 → 低分，不誤判為 VCP |
| 分割 / 除權息落在 lookback | 新高用還原收盤（D1）規避；VCP 視窗 ≤60 日內若偵測到 adj factor 變動可標旗標（可選） |
| 創 250 日高但長漲後過熱 | §3.4 抑制 + stage 推向 OVERHEATED |
| zigzag_pct 設太大漏掉末段 4% 收縮 | 文件標註調參；建議自適應門檻（base range 比例）作為 R2 後續優化 |
| 量資料缺 / 均量 0 | volDry=0、volConfirm=false（不灌分） |

---

## 9. Backward Compatibility

- 新欄位預設零值；`newhigh.enabled=false` 與 `vcp.enabled=false` → 與今日行為相容。
- g3 改寫在 `newhigh.enabled=false` 時退回原 `NearPreviousHigh(+6)` 子項。
- VCP bonus 在 `vcp.enabled=false` 時為 0，BaseQualityScore 不變。

---

## 10. Report / UI

- 卡片新增：`NH 88`（NewHighScore 分級配色）、距 52 週高 `-7%`、新高旗標（20/60/120/250 點亮）、
  `VCP ✓ 3段 10→6→4%`。
- 收斂失敗時顯示紅標「VCP 失敗（末段放量破低）」。

---

## 11. Test Plan

落點：`internal/scanner/newhigh_test.go`、`consolidation_test.go`（VCP）。

**New High**
1. 合成 close → H20/H60/H120/H250 旗標正確（含臨界根數）。
2. `PctFrom52wHigh`：今日為最高 → 0；回落 7% → −7。
3. NewHighScore：全綠 → 100；距高 −60% → 封頂 35；H250+過熱 → ×0.6。
4. 還原 vs raw：有 adjclose 用還原、缺值退 raw（標旗標）。

**VCP**
5. 構造 15→10→6→4% 量縮序列 → VCPScore 高、IsVCP=true、Contractions=[15,10,6,4]。
6. 擴張序列（4→6→10）→ mono 低、IsVCP=false。
7. 平盤無 pivot → ContractionCount<2 → VCPScore=0。
8. BaseQuality 加成封頂（VCPScore=100 → +12 後 clamp 100）。
9. `preBreak && IsVCP` → ExplosionProb=HIGH、DaysToWatch 縮短。
10. VCP 末段放量破低 → 不給 PRE_BREAKOUT、RiskWarning「VCP 失敗」。

**回歸**
11. 兩 `enabled=false` → 輸出與基準一致。

---

## 12. Implementation Sub-tasks（順序）

1. **R2.1** — `newhigh.go` + `StockAnalysis` 新高欄位 + `analyze()` 填入（用還原收盤，依賴 R1.0）。
2. **R2.2** — `rocket.go` g3 改寫（NewHighScore 取代 NearPreviousHigh）+ stage 新高佐證。
3. **R2.3** — `consolidation.go` 擴充 `analyzeVCP` + VCP 欄位。
4. **R2.4** — BaseQuality VCP 加成 + `preBreak`/`explosionProb` VCP-aware（HighConfidencePreBreak）。
5. **R2.5** — config 綁定 + report 標籤 + console。
6. **R2.6** — 測試（§11）。

> 最小可用版：R2.1 + R2.2（新高進評分）→ R2.3 + R2.4（VCP 進品質與 PRE_BREAKOUT）。

---

## 13. Resolved Decisions

- 新高一律以**還原收盤**比價（規避 H/L 還原問題、對波段更保守）。
- NewHighScore 以 **60 日為核心、距 52 週高為領導閘門**；過熱抑制 ×0.6。
- **`leader_within_pct = 25%`**（距 52 週高 ≤25% 算具領導力）— 已定案。
- VCP **複用** Consolidation，當 **BaseQuality 加成（上限 12）**，不取代既有壓縮 / 量縮分。
- **`vcp.min_contractions = 2`**（最少 2 段收縮成立，品質靠 mono/tight 分把關）— 已定案。
- VCP 主要影響 **PRE_BREAKOUT 信心與 ExplosionProb**，失敗則導向 FAILED/FADING。
- Composite Score 與市場掃描排序不動（相容）。

---

## Open Questions

1. **zigzag 門檻固定 3% 還是自適應（base range 比例）？** 固定值對高低價股不公平，自適應較穩但需校準。
2. **新高用收盤 vs 盤中高**：收盤較保守，是否漏掉「盤中破前高收回」的有效訊號？是否需並存兩版旗標？
3. **VCP 與既有 `PriceCompressionScore` 是否仍有重疊灌分**？bonus 上限 12 是否需再降？
4. **過熱抑制 ×0.6 與 stage OVERHEATED 是否雙重懲罰**？需確認兩者分工。
5. **VCP 視窗是否該與「整理 bucket」綁定**（MICRO_BASE 是否允許 VCP）？極短 base 可能段數不足。

> 已定案（移出 Open Questions）：`leader_within_pct=25%`；`vcp.min_contractions=2`（品質靠 R5 回測再校準）。

---

*（本文件為 R2 實作規格，design only，不修改任何程式碼。）*
