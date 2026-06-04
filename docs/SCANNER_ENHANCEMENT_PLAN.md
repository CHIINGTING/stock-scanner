# Scanner Enhancement Plan — Revision 2

> 本文件是 **設計計畫（design only）**，不含程式碼、不修改任何程式。
> 上游依據：`docs/SCANNER_MASTER_DESIGN.md`（目前實作的權威描述）。
>
> Scanner 定位不變：**台股盤後（EOD）波段交易助理**。
> 目標：**尋找未來 1~4 週有機會上漲的股票**；預計持有 **5~20 個交易日**。
> 三方向（FinLab / TradingView / Hades）僅**吸收概念**，不抄原始碼、不合併專案、不變成 Quant 平台。
> 不使用盤中、不使用五檔、不使用逐筆成交；多週期一律由日線 OHLCV 聚合。
>
> 所有「建議資料結構」以欄位表呈現，是**設計草圖**，非要立即加入的程式碼。

### Revision 2 變更摘要（不推翻 R1，僅調整優先序與投資價值）

1. **新增三個高 ROI 選股因子**：全市場 Relative Strength Ranking（RS Score / RS Rank）、
   多週期 New High Analysis（20/60/120/250 日）、VCP（VolatilityContractionScore）。
2. **MomentumFlow 提權**：從「附加層」升級為與 RocketStage **並列的雙主軸決策**，新增聯合決策表與情境範例。
3. **Multi-Timeframe 縮編**：以 **Daily + Weekly** 為主，**Monthly 延後**（理由見該節）。
4. **Backtest 重排優先序**：先 Win Rate / Avg Return / Profit Factor / Max Drawdown（含成本），
   其次 Sharpe / Sortino，**Factor IC 最後**（避免過早量化）。
5. **Roadmap 以「實際提升選股命中率」重排**，而非以量化研究完整度排序。
6. 新增結尾章節：What Should Be Implemented First / High ROI Features / Features To Delay /
   Revised Roadmap / Expected Impact。

---

## Current Architecture Summary

（摘自 Master Design，作為對照基準）

- **資料**：Yahoo 日線 OHLCV（range 預設 2y）+ TWSE/TPEX 全清單。純 EOD，無盤中 / 逐筆 / 五檔。
- **主流程**：`Rotation（族群）→ Sector → Stock（飆股候選）→ Watchlist（決策卡）→ Position`。
- **指標**（皆日線）：MA20、VolumeMA、KDJ、Bollinger、RSI(14)、ATR(14)、SMA 工具。
- **三套分數**：Composite 0–100、BestFourPoint 0–5、Rocket 0–100
  （族群資金流入 25 / 個股相對強勢 20 / 技術接近噴出 25 / 量能結構健康 15 / 尚未過熱 15 扣分）。
- **三層 Stage**：RocketStage（個股）、RotationStage（族群）、ShortTermFlowStage（族群短線）。
- **族群三層時間框架**：短線 1~5 日、中期 20 日、波段 60 日（MA60）。
- **Backtest（現況）**：pattern + bucket 的歷史勝率參考，**固定 forward = 5 日，無成本 / 無權益曲線 / 無風險調整指標 / 無環境分層**。
- **相對強勢（現況）**：只有「個股 vs 族群」與「族群間」兩種，**缺全市場排名**。
- **新高（現況）**：只有 60 日新高。

---

## Enhancement Goals

1. **先提升「選股命中率」**：優先補強被實證長期有效的選股因子（相對強勢、創新高、量縮收斂），
   而非先把回測做到學術完整。
2. **方向重於位置**：用 MomentumFlow 與 RocketStage 共同決策，避免「在對的位置買進、卻買在動能轉弱」。
3. **多週期一致性**：用日線聚合週線，給波段判斷加上趨勢背書（Monthly 視價值再決定）。
4. **可驗證但不過度量化**：回測先回答「會不會賺、賺多少、最大會賠多少」，風險調整與因子分析後置。
5. **漸進不破壞**：以新增欄位 / 新增模組為主，既有輸出向後相容。

---

## Relative Strength Ranking（全市場，新增 — 最高優先）

> 目前缺「全市場 RS 排名」。這是 IBD / Minervini 系統的基石，也是對「1~4 週領漲股」
> 命中率提升最大的單一因子。**新增 `RSScore` 與 `RSRank`。**

### 計算方法

- **RSScore（原始強度）**：採近一年、近期加權的多區間報酬（IBD 式精神，非照抄公式）。
  建議：`RSScore = 0.4×ret63 + 0.2×ret126 + 0.2×ret189 + 0.2×ret252`
  （63/126/189/252 交易日 ≈ 季 / 半年 / 三季 / 年；近季權重最高）。
  需 ≥ 252 根日線（預設 2y 足夠）；不足者標記 `RSValid = false`，不參與排名。
- **RSRank（百分位 1–99）**：對**全市場掃描通過基本過濾**（min_price / min_avg_volume）的股票，
  把 RSScore 做百分位排名。`RSRank = 95` 代表贏過市場 95% 的股票。

### 排名方式

- 以全市場掃描（`FetchAll` → ScanMarket）所得母體計算百分位，與市場掃描共用同一批資料，
  **不額外增加抓取**。
- **退化處理（重要）**：`--no-market` 模式下沒有全市場母體 →
  (a) 沿用上一次全掃描快取的 RSRank（標時間戳）；或 (b) 退化為族群內相對強勢並標 `RSRank = N/A`。
  此行為需在卡片明確標示，避免誤判。

### 如何影響 RocketScore

- **改寫第 2 組「個股相對強勢」（上限 20）**：目前用「ret20 > 族群均」這種粗略判斷，
  升級為以 RSRank 為主、族群相對為輔：

  | RSRank | 配分（組內） | 含義 |
  |--------|------------|------|
  | ≥ 90 | 滿配 | 市場領漲股，Minervini/IBD 進場前提 |
  | 80–89 | 高 | 強勢，合格候選 |
  | 70–79 | 中 | 及格邊緣 |
  | < 70 | 低 / 倒扣 | 非領漲股，1~4 週波段命中率顯著下降 |

### 如何影響排序

- **軟性門檻 + 排序鍵**：Watchlist 飆股候選以 `RSRank ≥ 80`（可 config）作為**優先呈現**門檻；
  未達者不剔除但排後。
- **OppScore / RocketScore 同分時**，以 RSRank 為 tie-break，讓真正領漲者浮上來。

---

## New High Analysis（多週期，強化）

> 把現有「60 日新高」擴充為 **20 / 60 / 120 / 250 日**四檔，並定義哪些最適合 1~4 週波段。

### 哪些最適合 1~4 週波段

| 視窗 | 角色 | 對 5~20 日持有的價值 |
|------|------|----------------------|
| **20 日新高** | 即時觸發 | 最及時但雜訊高，**單用易假突破**，宜作「進場觸發」而非選股 |
| **60 日新高** | **主力訊號** | 與波段最契合：確認中期轉強、剛離開整理區，**選股與計分核心** |
| 120 日新高 | 趨勢確認 | 強化「非曇花一現」，加分項 |
| **250 日新高（52 週）** | 領導力背景 | 不是要剛破 250 日高，而是**距 52 週高 ≤ ~25%**＝具領漲資格（品質閘門） |

> 結論：**60 日新高（時機）＋ 接近 52 週高 ≤25%（資格）** 是黃金組合；20 日新高當觸發、120 日當佐證。
> 剛創 250 日新高且已大漲一段者，反而要往 OVERHEATED 看。

### 如何影響 Score

- 新增 `NewHighScore`（0–100，併入「技術接近噴出」組或 Composite 突破項）：
  - 創 60 日新高且量比 ≥ 1.5 → 突破確認加分；
  - 距 52 週高 ≤ 25% → 領導力加分；> 50% → 視為非領漲、設上限不灌分；
  - 同時創 60+120 日新高 → 趨勢一致性加分。

### 如何影響 Stage

- 創 20/60 日新高 + 在 pivot 附近 + 量增 → 強化 `BREAKOUT_START`。
- 自有效 base 創 60 日新高 → 助 `PRE_BREAKOUT → BREAKOUT_START` 過渡。
- 創新高但偏離 5 日線過大 / 已是長漲後創 250 日高 → 推向 `OVERHEATED`（與「尚未過熱」組相呼應）。

---

## Volatility Contraction Pattern（VCP，新增）

> 吸收 Mark Minervini 的 VCP 概念（**不照抄**）：好的突破前，整理會「一波比一波緊、量一波比一波縮」。
> 例如波動由 15% → 10% → 6% → 4% 逐步收斂。設計 `VCPScore` 回答「整理是否愈來愈緊」。

### 如何使用現有 Consolidation 模組

- 現有 `analyzeConsolidation` 只找**單一** base 視窗，輸出 `RangePct / VolumeDryUpRatio /
  PriceCompressionScore`。VCP 需要看**多段連續收縮**。
- 設計（沿用現有 `windowHighLow` / `avgVolume`）：在 base 區間內由近往遠切出 2~4 段
  「swing 高→低」收縮腿，量測每腿**回檔深度 %** 與**均量**：
  - 深度單調遞減（T1 > T2 > T3…）→ 收斂成立；
  - 最後一段深度極小（如 ≤ 5~6%）→ 高度壓縮；
  - 各段均量遞減 → 量縮配合。
- `VCPScore`（0–100）= f(收縮段數 ≥ 2、深度單調遞減程度、末段緊度、量能遞減)。

### 如何與 BaseQualityScore 整合

- **方案（建議）**：把 VCP 當作 BaseQuality 的**品質加成**而非取代：
  `BaseQualityScore += clamp(VCPScore 對應加成, 0, ~15)`。
  既有 `PriceCompressionScore / VolumeDryUpRatio / SupportHoldScore` 仍是基底，
  VCP 額外獎勵「逐步收斂」這個更高階的型態（單純窄幅 ≠ VCP）。
- VCPScore 亦獨立輸出於卡片，作為可解釋理由（「整理三段收斂 10%→6%→4%、量縮」）。

### 如何影響 PRE_BREAKOUT 判斷

- 現行 `preBreak` 條件加入 VCP：`NearPreviousHigh + BaseQuality ≥ 50 + 量縮 + 逼近 pivot`
  之上，若 `VCPScore` 高 → **升級為高信心 PRE_BREAKOUT、ExplosionProb 上修**。
- VCP 成立但**末段放量破前低** → 收斂失敗，往 FAILED / MOMENTUM_FADING 看。

---

## MomentumFlow（提權為雙主軸）

> RocketStage 描述**位置**，MomentumFlow 描述**方向**。Revision 2 將兩者**並列為主軸**，
> 共同決定 WatchAction / ExplosionProb / RiskWarning（不再是單純附加層）。

### MomentumFlow 五態（沿用 R1 定義）

| Flow | 概念 | 判定（日線斜率二階 + 量能背離 + 高低點結構；可選週線確認） |
|------|------|--------------------------------------------------------------|
| `MOMENTUM_BUILDING` | 動能累積 | 量 / RSI 斜率由平轉升、量縮後初放量、KDJ 低檔上彎、base 收斂 |
| `MOMENTUM_CONTINUATION` | 動能延續 | 多頭排列維持、回檔不破短均後再放量、報酬斜率穩定為正 |
| `MOMENTUM_FADING` | 動能衰退 | 創高量縮 / RSI 背離、上影增多、報酬斜率轉平 |
| `STRUCTURAL_SHIFT_UP` | 向上轉折 | 站回關鍵均線 + 高低點由墊低轉墊高（可選週線轉強） |
| `STRUCTURAL_SHIFT_DOWN` | 向下轉折 | 高點墊低、跌破平台 / 頸線（FAILED 的前兆，提前示警） |

### RocketStage × MomentumFlow 聯合決策

| RocketStage | MomentumFlow | WatchAction | ExplosionProb | RiskWarning |
|-------------|--------------|-------------|---------------|-------------|
| **PRE_BREAKOUT** | **BUILDING** | PREPARE_ENTRY（最高信心） | **HIGH ↑** | 低；設好突破帶 |
| **PRE_BREAKOUT** | **FADING** | WAIT（降級，不預掛單） | **LOW ↓** | 「逼近前高但動能轉弱，提防假突破」 |
| **PRE_BREAKOUT** | SHIFT_DOWN | REMOVE / 觀望 | LOW | 「型態收斂失敗、結構轉弱」 |
| **BREAKOUT_START** | BUILDING / CONTINUATION | BREAKOUT_BUY | HIGH | 低；量不足才提醒假突破 |
| **BREAKOUT_START** | FADING | 縮手，等量增確認 | MEDIUM ↓ | 「突破當下動能不足」 |
| **MAIN_RUN** | CONTINUATION | WATCH_CLOSELY / 拉回 PULLBACK_BUY | MEDIUM | 低 |
| **MAIN_RUN** | **FADING** | **提前 TAKE_PROFIT / 收緊停利** | LOW ↓ | 「主升動能轉弱、創高量縮，保護獲利」 |
| **BASE_BUILDING** | **STRUCTURAL_SHIFT_UP** | WATCH_CLOSELY（候選升級） | MEDIUM ↑ | 低 |
| **BASE_BUILDING** | FADING | 續觀察，不急 | LOW | 「築底中動能未到」 |
| **任何** | **STRUCTURAL_SHIFT_DOWN** | **REMOVE / 減碼（先於 FAILED）** | LOW | 「結構轉空、跌破關鍵支撐」 |

### 對其他輸出的影響

- **DaysToWatch**：BUILDING / SHIFT_UP → 縮短（接近發動）；FADING / SHIFT_DOWN → 「等回檔再評估」或「—」。
- **Reasons**：每種組合對應一句話可解釋結論（如「突破前夕＋動能正在累積，密切準備」）。
- **RocketScore**：MomentumFlow 以**有上限的修正項**（建議 ±10 內，或對「尚未過熱」組做乘數）併入，
  不破壞 0–100 結構；**避免與 ExplosionProb 雙重計分**（一個調分、一個調機率標籤）。

---

## Multi-Timeframe（縮編：Daily + Weekly 優先，Monthly 延後）

> 重新評估 Monthly 的必要性。持有 5~20 個交易日 ≈ 1~4 週。

### 方案比較

| 方案 | 內容 | 對 5~20 日持有的價值 | 成本 / 風險 |
|------|------|----------------------|-------------|
| **A. Daily + Weekly（建議）** | 日線抓時機，週線（1 根 = 5 日）定波段趨勢。4 週 ≈ 4 根週線，**週線正是波段的自然週期** | 高：趨勢背書直接對應持有期 | 低：2y ≈ 100+ 週線，指標可靠 |
| B. Daily + Weekly + Monthly | 再加月線定大方向 | 邊際：月線 1 根 = 20+ 日，對 5~20 日持有反應太慢；2y 僅 ~24 根月線，指標不穩 | 較高：樣本不足、易誤導 |

### 結論

- **採方案 A**。Monthly 對本持有期價值有限，**延後到未來版本**。
- 「大方向不能逆勢」這個 Monthly 想提供的功能，可用**日線 200 日均線（約等於年線）做長期濾網**
  近似達成——不需聚合月線，零額外資料、樣本充足。
- 因此多週期設計收斂為：`TimeframeView(Daily)`、`TimeframeView(Weekly)`、
  `MultiTimeframeAlignment(D,W)` + 一個 `LongTermFilter`（200 日 MA 之上 / 之下）。
- `TimeframeTrendScore / TimeframeMomentumScore / SignalStrength / RiskWarning` 設計沿用 R1，
  但只在 D / W 兩週期計算。

---

## Backtest Upgrade Plan（重排優先序）

> 理由：Scanner **仍在建立訊號階段**，先確認訊號「會不會賺」，避免過早量化。
> 成本 / 滑價放在第一梯次（否則勝率不真實），但風險調整與因子分析後置。

| 梯次 | 指標 / 能力 | 用途 |
|------|------------|------|
| **第一（先做）** | **Win Rate、Average Return、Profit Factor、Max Drawdown**，全部**含交易成本 / 滑價**，並支援 **forward 5 / 10 / 20 日** | 直接回答「這訊號賺不賺、最大會賠多少」，對應 1~4 週持有 |
| 第二（其次） | Sharpe、Sortino、權益曲線、出場規則回測（stop / target / 移動停損） | 報酬品質與出場驗證 |
| **第三（最後）** | Factor IC、分位數報酬、Regime 分層、勝率信賴區間 | 科學化歸因，等訊號穩定後再做 |

- 現有 `internal/scanner/backtest.go` 保留為「即時卡片 pattern 勝率快查」；
  策略級驗證走新 `internal/backtest/`（共用 pattern detector 與 bucket）。
- **驗證循環**：新因子（RS / NewHigh / VCP / MomentumFlow）先以第一梯次指標量測命中率提升，
  有效才正式進評分；Factor IC 等到第三梯次再回頭做精細歸因。

---

## Proposed New Data Structures（彙總，設計草圖）

1. **選股因子**：`RSScore / RSRank / RSValid`（個股級）；`NewHighFlags{H20,H60,H120,H250}` +
   `PctFrom52wHigh` + `NewHighScore`；`VCPScore` + `Contractions []float64`（各段深度）。
2. **動能**：`MomentumState{Flow, Score, SlopeAccel, Divergence, StructureTrend, WeeklyConfirm, Note}`。
3. **多週期**：`TimeframeView{TF, TrendScore, TrendState, MomentumScore, MomentumState, Valid}`、
   `MultiTimeframe{Daily, Weekly, AlignmentScore, AlignmentLabel, SignalStrength, LongTermFilter, Note}`。
4. **回測**：`Trade`、`BacktestReport{WinRate, AvgReturn, ProfitFactor, MaxDrawdownPct, …}`、`CostModel`
   （第二梯次再加 EquityCurve / Sharpe / Sortino；第三梯次再加 Regime / FactorIC）。
5. **既有結構新增欄位**（不改既有語意）：`WatchlistEntry` += RS / NewHigh / VCP / MomentumState /
   MultiTimeframe；`StockAnalysis`（可選）+= RSScore / RSRank。

---

## Proposed New Modules

| 模組 | 職責 | 對應 |
|------|------|------|
| `internal/scanner/relstrength.go` | 全市場 RSScore / RSRank 計算與百分位 | RS（最高優先） |
| `internal/scanner/newhigh.go` | 多週期新高 + 52 週高距離 + NewHighScore | New High |
| （擴充）`internal/scanner/consolidation.go` | 加 VCP 多段收斂偵測與 VCPScore | VCP |
| `internal/scanner/momentum.go` | MomentumFlow 計算 + 與 RocketStage 聯合決策 | Hades |
| `internal/timeframe/` (aggregate/mtf) | 日→週聚合、D/W TimeframeView、Alignment、200日長期濾網 | TradingView |
| `internal/backtest/` (engine/metrics/cost) | 第一梯次策略回測（含成本、多 forward） | FinLab |

原則：新增 package、以組合接上既有 `internal/scanner`，不重寫既有檔案。

---

## Scoring Changes

- **RocketScore（0–100 維持）**：
  - 第 2 組「個股相對強勢」改以 **RSRank** 為主（見 RS 節）。
  - 第 3 組「技術接近噴出」納入 **NewHighScore** 與 **VCP 加成**（經由 BaseQualityScore）。
  - **MomentumFlow** 以有上限修正項併入（±10 內），不雙重計分。
- **排序**：RSRank 作為軟門檻與 tie-break；MomentumFlow 方向影響 ExplosionProb 與卡片排序。
- **Composite Score / 市場掃描語意不動**（保相容）；新因子主要進 Watchlist 卡與排序。
- 新權重 / 閾值走 config（漸進償還寫死債務，不必一次到位）。

---

## Stage / Momentum Flow Changes

- RocketStage 七態**不變**。
- MomentumFlow **提權為與 RocketStage 並列的主軸**，以上方「聯合決策表」共同決定行動與機率。
- WatchAction 由「單看 RocketStage」升級為「RocketStage × MomentumFlow」查表。
- STRUCTURAL_SHIFT_DOWN 提供 **FAILED 前的提前示警**，降低套在反轉點的機率。

---

## Risk Control Improvements

- **動能風險前置**：FADING / SHIFT_DOWN 提前降評等、縮短 DaysToWatch、收緊停利（不等 FAILED）。
- **領導力風險**：RSRank < 70 的候選即使型態漂亮也標「非領漲股」風險。
- **新高風險**：距 52 週高過遠或長漲後創 250 日高 → 標「追高 / 非起漲」風險。
- **出場回測（第二梯次）**：用 MAE / MFE 校準停損距離，取代寫死的 ATR×2 / 0.93。
- **長期濾網**：跌破 200 日 MA 的標的，波段做多風險提示。

---

## Revised Roadmap

> 排序原則：**以實際提升選股命中率為優先**，量化研究完整度後置。
> R1~R4 為實證有效的選股因子（強先驗），R5 起以第一梯次回測驗證並校準。

| 階段 | 內容 | 依賴 | 命中率貢獻 |
|------|------|------|-----------|
| **R1** | **全市場 RS Ranking（RSScore / RSRank）** → 改寫 RocketScore 第 2 組 + 排序軟門檻 | 全市場掃描資料 | ★★★★★ 領漲股過濾，單一最大 |
| **R2** | **New High Analysis（20/60/120/250 + 52週距離）+ VCPScore（擴充 Consolidation）** → 併入 BaseQuality / PRE_BREAKOUT | R1 可並行 | ★★★★ 進場品質、減少假突破 |
| **R3** | **MomentumFlow v1 + RocketStage×MomentumFlow 聯合決策** → WatchAction / ExplosionProb / RiskWarning | 無 | ★★★★ 避免買在動能轉弱 |
| **R4** | **Daily + Weekly 多週期 + 200日長期濾網** → SignalStrength、趨勢背書 | 無 | ★★★ 趨勢一致性 |
| **R5** | **Backtest 第一梯次**：Win Rate / Avg Return / Profit Factor / Max DD（含成本、forward 5/10/20） | R1–R4 | ★★★ 驗證並校準上面因子 |
| **R6** | Backtest 第二梯次：Sharpe / Sortino / 權益曲線 / 出場規則回測 | R5 | ★★ 報酬品質 |
| **R7（延後）** | Monthly 週期、Regime 分層、Factor IC、閾值全面 config 化 | R5,R6 | ★ 科學化、邊際 |

> 建議交付節奏：R1 → R2 → R3 可快速連發（皆提升命中率且相對獨立），R4 補趨勢背書，
> R5 上線後形成「加因子 → 量測命中率 → 保留有效者」的循環。

---

## What Should Be Implemented First

1. **R1：全市場 Relative Strength Ranking（RSScore / RSRank）。** 對「找未來 1~4 週領漲股」
   命中率提升最大、與既有市場掃描共用資料、不需新資料源、可立即改寫 RocketScore 第 2 組。
2. 緊接 **R2（New High + VCP）** 與 **R3（MomentumFlow 聯合決策）**——三者組成「強勢 + 好型態 +
   對的方向」的選股黃金三角。

---

## High ROI Features

| 功能 | ROI | 理由 |
|------|-----|------|
| 全市場 RS Ranking | **極高** | 領漲股過濾，實證最有效的單一選股因子；零額外資料成本 |
| New High（60 日 + 52 週距離） | 高 | 時機 + 領導資格，直接對應波段進場 |
| VCPScore（擴充現有模組） | 高 | 大幅減少假突破；複用 Consolidation，工程量小 |
| MomentumFlow 聯合決策 | 高 | 把「位置對但動能轉弱」的賠錢單擋掉；提前風險示警 |
| Daily + Weekly 多週期 | 中高 | 趨勢背書，週線正是波段天然週期 |
| Backtest 第一梯次（含成本） | 中高 | 讓上述因子「可被驗證」，但本身不直接選股 |

---

## Features To Delay

| 功能 | 延後原因 |
|------|----------|
| **Monthly 週期** | 對 5~20 日持有反應太慢、2y 樣本僅 ~24 根；用 200 日 MA 濾網近似即可 |
| **Sharpe / Sortino** | 訊號未定型前，風險調整報酬意義有限，列第二梯次 |
| **Factor IC / 分位數歸因** | 過早量化；等訊號穩定、樣本足再做（第三梯次） |
| **Regime 分層回測** | 需大盤資料與較大樣本，價值在後期校準才顯現 |
| **權益曲線 / 投組層級** | 超出單檔波段助理範圍，且非命中率關鍵 |
| **參數最佳化 / walk-forward** | 過擬合風險高、工程量大，最後評估 |

---

## Expected Impact

- **R1（RS Ranking）**：把候選池從「技術面漂亮」收斂到「市場真正領漲」，預期**顯著降低弱勢股誤選**，
  是命中率提升的主引擎。
- **R2（New High + VCP）**：在領漲股中再篩「剛突破 / 收斂到位」者，**減少假突破與過早進場**。
- **R3（MomentumFlow）**：用方向過濾位置，**擋掉「位置對但動能轉弱」的單**，降低買在反轉點。
- **R4（D+W 多週期）**：趨勢背書，**降低逆勢波段**的比例。
- **R5（第一梯次回測）**：提供「會不會賺、最大回撤多少」的客觀證據，讓 R1~R4 的權重可被**數據校準**，
  形成自我修正循環。
- 整體：四個選股因子疊加 + 一個驗證循環，目標是**提高「進場後 5~20 日上漲」的命中率與盈虧比**，
  而非把 Scanner 變成量化研究平台。

---

## What NOT to Change

- **定位不變**：台股盤後 EOD 波段助理；目標未來 1~4 週、持有 5~20 日。
- **不引入盤中 / 逐筆 / 五檔**；週線一律由日線聚合，Monthly 延後。
- **不重寫專案、不合併專案、不抄原始碼**（FinLab / TradingView / Hades 只取概念）。
- **不把 Scanner 變成 Quant 平台**（不做投組最佳化、不做參數最佳化、不過早量化）。
- **不改 Composite Score 與市場掃描既有語意**；新因子進 Watchlist 卡與排序。
- **既有 Stage / Action / HTML 報告維持向後相容**——以新增欄位 / 區塊為主。
- **維持「給建議、不給原始數字」與繁中可解釋性**。

---

## Open Questions

1. **RSRank 母體與更新頻率**：只用上市還是上市+上櫃？`--no-market` 時用快取 RSRank 還是退化族群相對？快取多久算過期？
2. **RS 區間權重**（0.4/0.2/0.2/0.2）是否需用台股資料微調？是否要排除剛上市未滿 250 日者？
3. **New High 的「接近 52 週高」門檻**用 25% 還是更嚴（如 15%）？是否隨族群屬性調整？
4. **VCP 最少收縮段數**：2 段就算還是要 3 段？末段緊度門檻（≤5%？）如何定？需回測校準。
5. **MomentumFlow 修正項併入 RocketScore 的形式**（±固定分 / 乘數 / 只調排序）需第一梯次回測比較。
6. **Weekly 聚合的週界定義**：以實際交易日（可能因休市不足 5 日）或自然週？跨年週如何處理？
7. **200 日 MA 長期濾網**是硬門檻（跌破即排除）還是軟性風險標籤？
8. **forward window 對「1~4 週」的主指標**：以 10 日還是 20 日為主要勝率口徑？或並列呈現？

---

*（本文件為設計計畫第二版，不修改任何程式碼。落地時各模組以對應原始檔與本計畫為準。）*
