# Scanner Master Design

> 本文件是 stock-scanner（Stock Radar）的**永久設計文件 / Architecture Document**。
> 目的：(1) 作為未來開發依據；(2) 避免 context 遺失；(3) 讓新 session 能直接接手；
> (4) 記錄目前所有設計決策與資料結構。
>
> 維護原則：**程式行為改變時，請同步更新本文件。** 文件以「目前實際實作」為準，
> 推測或尚未落地的內容一律放到 `Future Roadmap` 或 `Open Questions`。
>
> 最後校對基準：對照 `internal/scanner/*`、`internal/indicator/*`、`internal/fetcher/*`、
> `cmd/scanner/main.go`、`configs/*`、`README.md`（2026-06 校對）。

---

## Project Goal

Scanner 的定位是**台股盤後（EOD）波段交易助理**，回答交易者最關心的三個問題：

1. **這支股票現在能買嗎？**
2. **我的持股該續抱還是停損？**
3. **量能是否支持這波漲勢？**

核心定位已從「當沖 / 今天最強選股」調整為**尋找未來 1~4 週有機會的波段標的**
（見 memory: scanner-direction）。設計哲學：真正的大波段通常是
**先有族群輪動、再有個股表態**，資金會從已噴出的中後段族群流向下一波接棒族群。

因此整個系統圍繞一條主流程運作：

```
Rotation（族群輪動）→ Sector（族群強弱）→ Stock（個股飆股候選）
                                          → Watchlist（決策卡片）→ Position（持倉進出場）
```

它**不是技術指標展示工具**——不丟出一堆指標數字，而是輸出**可執行的交易建議**
（BUY / WATCH / STOP LOSS …）、價位計畫（進場 / 停損 / 目標）與一句話結論。
設計靈感部分來自 twstock 的 BestFourPoint：給建議、不給原始指標；要求多個獨立條件同時確認。

---

## Non Goals

Scanner **不負責**以下事項，這些是刻意排除的範圍：

- **不做當沖 / 盤中即時訊號**：資料是日線 EOD，沒有逐筆、沒有盤中 tick、沒有委買委賣五檔。
- **不做下單 / 串接券商 API**：只輸出建議，不執行交易。
- **不做基本面 / 財報 / 籌碼（法人、融資券、分點）分析**：純技術面 + 量價。
- **不保證封單 / 內外盤等精細籌碼判斷**：漲停動態是用日線 OHLCV + 量比「近似推斷」，非真實封單資料。
- **不做投資組合最佳化 / 資金配置 / 風險平價**：持倉只做單檔層級的進出場建議。
- **不做即時推播 / 排程自動執行**：目前是手動 CLI 執行，產生 HTML 報告。
- **不是回測平台**：內建 backtest 只為「當前型態的歷史勝率參考」，不是完整策略回測框架。
- **不構成投資建議**：所有輸出僅供技術研究，最終決策由使用者負責。

---

## Design Principles

1. **給建議，不給原始數字**（BestFourPoint 哲學）。輸出 Action 與一句話結論，
   指標數字只作為展開後的佐證。
2. **多重確認、取保守值**。Action 由「BFP 質性條件（幾個成立）」與「量化分數」
   兩條訊號 blend，取**較保守**的結果（`blendAction`），降低假訊號。
3. **量能是第一公民**。量價佔評分最高權重（25/100）。但有重要例外原則：
   **「量縮本身不是問題，問題是量縮時價格有沒有失守。」** → 漲停鎖量不扣分。
4. **族群優先於個股**。個股飆股分數的最大單一權重來自「族群資金流入」（25/100）。
5. **早期輪動優先於過熱**。排序刻意把 EARLY / CONFIRMED 往前加權、HOT / LATE 往後淡化，
   回答「下一波資金去哪」而非「今天誰最強」。
6. **整理時間只做分類、不做加扣分**。Base 的好壞看 `BaseQualityScore`（壓縮 + 量縮 +
   支撐 + 接近前高 + 族群流入 − 爆量長黑 − 跌破平台），**不是越久越好**。
7. **無前視偏誤（no look-ahead）**。回測的 pattern 偵測只用到當下 bar `i` 之前的資料。
8. **保守抓取、善待資料源**。低並發 + 請求間隔 + TTL 快取 + EOF 冷卻，避免被 Yahoo 限速。
9. **資料不足就降級，不要崩**。候選普遍要求 ≥ 30 根 K 棒；回測要求 ≥ 40 根；
   不足時給 NOT_READY / LOW confidence 而非報錯。
10. **繁體中文輸出**。所有 reasons / note / 風險說明皆為繁中，面向台股使用者。

---

## Data Source

| 用途 | 來源 | 端點 / 說明 |
|------|------|-------------|
| OHLCV 日線（個股歷史） | **Yahoo Finance Chart API** | `query1.finance.yahoo.com/v8/finance/chart/{code}.TW(O)`；range 預設 `2y` |
| 上市（TWSE）全清單 | **TWSE OpenAPI** | `openapi.twse.com.tw/v1/exchangeReport/STOCK_DAY_ALL` |
| 上櫃（TPEX）全清單 | **TPEX OpenAPI** | `tpex.org.tw/openapi/v1/tpex_mainboard_daily_close_quotes` |

### 市場別（Market）

- `TW` → 上市（TWSE），抓 `{code}.TW`
- `TWO` → 上櫃（TPEX），抓 `{code}.TWO`
- 留空 → **自動偵測**：先試 `.TW`，網路層級失敗（`errNetworkFailure`）才試 `.TWO`，
  結果以 `marketCache`（`sync.Map`）快取。注意：是「網路失敗才換」而非「無資料才換」。

### 抓取與保護機制（`internal/fetcher/fetcher.go`，可由 `configs/config.yaml` 調整）

- `concurrency`（預設 3，建議 ≤ 5）：worker 數。
- `request_delay_ms`（預設 400）：每個 worker 請求間隔。
- `timeout_sec`（預設 30）：單次 HTTP timeout。
- `cache_ttl_min`（預設 15）：OHLCV TTL 快取，同 ticker 15 分鐘內不重抓（`.cache/` 目錄）。
- `eof_cooldown_min`（預設 5）：遇 EOF / 連線重置後，該 ticker 冷卻期內不再請求。
- `history_range`（預設 `2y`）：回測樣本依賴歷史長度，建議 ≥ 1y。

### 核心資料結構

- `fetcher.Candle`：單日 OHLCV bar（Date / Open / High / Low / Close / Volume）。
- `fetcher.StockData`：單檔完整歷史，**oldest-first 排序**；帶 `Source`（market /
  portfolio / watchlist）、`Market`、`CostBasis`、`Shares`（僅 portfolio）。
- `fetcher.StockInfo`：輕量描述（Symbol / Name / Market），組 fetch job 用。

### 已知限制

- 假日 / 休市日 TWSE / TPEX 不提供當日資料，市場掃描會空（建議交易日 15:00 後執行）。
- Yahoo 速率限制下會出現「無資料」，需調低 concurrency 或調高 delay。
- **無逐筆 / 封單 / 內外盤資料** → 漲停籌碼動態為日線近似（見 Volume Analysis）。

---

## Scoring System

系統有**三套並行的分數**，服務不同頁面，不要混為一談：

### A. 個股綜合分數（Composite Score，0–100）—— `scorer.go: score()`

用於市場掃描排序與個股展開細節。權重：

| 組件 | 權重上限 | 函式 | 備註 |
|------|---------|------|------|
| MA20 趨勢 | +20 / 最低 −15 | `scoreMA20` | 連續上揚 / 下彎天數分級，可為負 |
| RSI 動能 | +20 / 最低 −12 | `scoreRSI` | 超賣高分、超買懲罰 |
| KDJ 擺盪 | +20 / 最低 −18 | `scoreKDJ` | 黃金 / 死亡交叉、多空排列 |
| 量能 | +25 / 最低 0 | `analyzeVolume` | **最高權重**；漲停特例覆寫 |
| Bollinger | +15 / 最低 −8 | `scoreBB` | 收縮、突破、擴張分級 |

最終 clamp 至 [0, 100]。注意各組件可為負，故分數是「淨值」。

### B. BestFourPoint（BFP）質性條件（0–5）—— `scorer.go: bestFourPoint()`

5 個獨立 PASS/FAIL 檢查點，UI 以 ●●●○○ 呈現：

| # | 條件 | PASS 判準（摘要） |
|---|------|------------------|
| 1 | 趨勢 | 價站上 MA20 **且** MA20 連 ≥ 2 日上揚 |
| 2 | 動能 | RSI 在 25–65（最佳區）；超賣 20–30 也視為 PASS |
| 3 | 擺盪 | KDJ 黃金交叉 **或** 多頭排列（K>D 且 K<80） |
| 4 | 量能 | 價漲量增（量比 ≥ 1.3） **或** 量比 ≥ 2.0；漲停特例覆寫 |
| 5 | 突破 | 突破布林上軌 / 收縮後站上中線 / 多頭擴張 / 站上中線 |

### C. Action 決策 —— blend B + A 取保守值

- `actionFromBFP(points)`：5→STRONG BUY、4→BUY、3→WATCH、2→HOLD、1→REDUCE、0→SELL。
- `rawAction(score)`：≥78→STRONG BUY、≥62→BUY、≥47→WATCH、≥32→HOLD、≥18→REDUCE、else→SELL。
- `blendAction`：取兩者中**較保守**者（rank 較低者）。
- 持倉再經 `positionAdvice` 覆寫成 STOP LOSS / TAKE PROFIT / REDUCE（見 Position 規則）。

完整 Action 列舉（`types.go`）：STRONG BUY / BUY / WATCH / HOLD / REDUCE /
TAKE PROFIT / STOP LOSS / SELL。

### D. 飆股候選分數（Rocket Score，0–100）—— `rocket.go: computeRocket()`

Watchlist 專用，5 組加權（與 Composite Score 完全不同）：

| 組 | 上限 | 內容 |
|----|------|------|
| 1 族群資金流入 | 25 | 短線流向 INFLOW(+15)/NEUTRAL(+7) + 族群 stage（EARLY/CONFIRMED +10、HOT +5、LATE +2）；無族群給 8 |
| 2 個股相對強勢 | 20 | 20 日報酬 > 族群均(+8/否+3) + RSI 區間 + 支撐守住分 ≥60(+6) |
| 3 技術接近噴出 | 25 | BaseQuality/100×12 + 接近前高(+6) + 多頭排列(+4) + 剛突破/逼近(+3) |
| 4 量能結構健康 | 15 | 價漲量增(+6) + 整理量縮(+5) + 無爆量長黑/漲停失敗(+4) |
| 5 尚未過熱（扣分） | 15 | 起始 15，偏離 5 日線>12%(−8)、長上影(−4)、開高走低(−3)、族群流出(−5) |

分數帶說明（README）：0–39 不適合 / 40–59 有潛力未就緒 / 60–74 準備中 /
75–89 高機率準備發動 / 90–100 已主升或過熱。

### E. 族群分數（Sector Score）—— 見 Industry Strength 章節。

---

## Stage Analysis

系統有**三種獨立的 stage**，分屬不同層級：

### 1. 飆股階段（RocketStage）—— 個股，`rocket.go`

決策樹（由危險 / 成熟往初期判斷，第一個命中者勝）：

| Stage | 觸發（摘要） | 操作建議 WatchAction |
|-------|-------------|----------------------|
| FAILED 失敗 | 跌破平台 / 收破 MA20 且 ret5<−5% / 族群流出且收破 MA10 | REMOVE_FROM_WATCHLIST |
| OVERHEATED 過熱 | 偏離 5 日線>12% 或 ret5>25%，或 climax（漲停失敗 / 長上影爆量） | TAKE_PROFIT |
| BREAKOUT_START 起漲 | 剛突破前高（昨收 ≤ 突破點、今收 > 突破點、量比 ≥ 1.3） | BREAKOUT_BUY |
| MAIN_RUN 主升 | 多頭排列 + ret20 ≥ 15% + 站上 MA5 + 未過度延伸 | 拉回量縮→PULLBACK_BUY，否則 WATCH_CLOSELY |
| PRE_BREAKOUT 突破前 | 接近前高 + BaseQuality ≥ 50 + 量縮 + 逼近突破點且未破平台 | PREPARE_ENTRY |
| BASE_BUILDING 築底 | 有 base（非 NoBase）+ 站上 MA20 + BaseQuality ≥ 40 | WATCH_CLOSELY |
| NOT_READY 未就緒 | 以上皆否 / 資料 < 30 根 | WAIT |

衍生：`ExplosionProb`（HIGH/MEDIUM/LOW）、`DaysToWatch`（依 stage 給天數）。

### 2. 輪動階段（RotationStage）—— 族群整體，`rotation.go: classifyStage()`

`EARLY`（醞釀）/ `CONFIRMED`（確認）/ `HOT`（過熱）/ `LATE`（末段）。
判準（由成熟往初期）：ret20≥25 且 RSI≥70 → LATE；新高比≥50 且 RSI≥63 → HOT；
突破比≥40 → CONFIRMED；量增比≥40 且 ret20>0 → EARLY；其餘 → EARLY。
用於排序加權：`stageWeight` EARLY 1.15 / CONFIRMED 1.10 / HOT 0.85 / LATE 0.60。

### 3. 短線流向階段（ShortTermFlowStage）—— 族群短線，`rotation.go: classifyShortStage()`

`EARLY_ROTATION` / `CONFIRMED_ROTATION` / `OVERHEATED` / `WEAKENING`。
**關鍵設計**：把短線（1~5 日）強度與中期（20 日）強度**對比**：
短強中弱 → EARLY_ROTATION（資金剛流入、20 日尚未反映，最早期候選）；
短中皆強 → CONFIRMED；短中皆強且超買 → OVERHEATED；短弱 / 流出 → WEAKENING。

---

## Trend Analysis

趨勢判斷分布在多個層級：

### 個股趨勢（MA20 為主）

- `MA20ConsecutiveRising / Falling`：連續上揚 / 下彎天數，是趨勢分數與 BFP 趨勢條件的核心。
- `MA20TrendLabel`：輸出 ↑↑↑ / ↑↑ / ↑ / → / ↓ / ↓↓ / ↓↓↓ 七級標籤。
- BFP 趨勢條件 PASS = 站上 MA20 **且** MA20 連 ≥ 2 日上揚。
- 多頭排列（`bullAlign`）：MA5 > MA10 > MA20，用於 rocket MAIN_RUN 判定與技術組分數。

### 族群波段趨勢（60 日層）—— `rotation.go`

- `MA60Slope`：MA60 近 5 日上揚成員占比（%）。
- `AboveMA60Ratio`：站上 MA60 成員占比（%）。
- `TrendStrength = 0.6×MA60Slope + 0.4×AboveMA60Ratio`。
- `TrendLabel`：≥60 確認上升 / ≥35 尚未確認 / else 轉弱。

### 三層時間框架（族群輪動核心模型）

| 層 | 視窗 | 角色 |
|----|------|------|
| 短線流向 | 1~5 日 | **領先指標**，最早反映資金轉向 |
| 中期強度 | 20 日 | 即 Sector Score（強 / 中 / 弱） |
| 波段趨勢 | 60 日 | MA60 斜率 + 站上 MA60（確認 / 未確認 / 轉弱） |

設計理由：20 日強度反應慢，資金通常先在短線出現跡象，才慢慢反映到 20 日；
因此用短線當領先訊號、20 日確認、60 日定方向。

---

## Volume Analysis

量能是評分最高權重（25/100），實作於 `scorer.go: analyzeVolume()` 與漲停偵測。

### 量比（Volume Ratio）

`量比 = 當日量 / 20 日均量(VolumeMA)`。分級：>2.0 爆量、1.0–2.0 正常放量、<0.8 縮量。

### 價量訊號（PriceVolumeSignal）

價漲量增 ✅（最佳）/ 價漲量縮 ⚠️（小心假突破）/ 價跌量增 ❌（賣壓重）/ 價跌量縮 ⚠️（等方向）。
另有漲停特例兩種：`漲停鎖量`、`漲停失敗`。

### 量能分數（0–25）

≥4.0x 且漲 →25；≥2.5x 且漲 →20；≥1.5x 且漲 →15；≥1.5x 且跌 →5；≥0.8x →8；>0 →2。
買賣比 `calcBuySellRatio` >1.5 加 5、<0.7 扣 5（目前以近 5 日上漲天數比例近似，
非真正 (close−low)/(high−low)，見 Technical Debt）。

### 大單偵測

量比 ≥ 3.0 標記大單：配漲 = 主力建倉、配跌 = 主力出貨。

### 漲停籌碼動態（`detectLimitStatus`，日線近似）

台股漲跌幅 ±10%，但**只有日線 OHLCV、無封單資料**，故由開高低收 + 量比近似：

| 標記 | 判準（日線近似） | 評價 / 對量能的影響 |
|------|-----------------|---------------------|
| `LOCKED_LIMIT_UP_LOW_VOLUME` 漲停鎖量 🔒 | 漲幅 ≥ 9%、收在當日最高（≥High×0.998）、量比 0<vr<1 | **中性偏多**：量能不扣分、量能條件視為 PASS、量能分給 18 |
| `LIMIT_UP_FAILED` 漲停失敗 ⚠️ | 盤中觸及漲停（highGain ≥ 9%）但收盤 ≤ High×0.97 且量比 ≥ 1.5 | **負面**：量能分 0、量能條件 FAIL |
| `DISTRIBUTION_AFTER_LIMIT_UP` 出貨 ⚠️ | 前一日漲停鎖住，今日收黑且量比 ≥ 1.5 | **負面**：同上 |

核心原則：**量縮 ≠ 轉弱，要看價格有沒有失守。** 此特例會覆寫一般量能判斷
（`analyzeVolume` 與 BFP 量能條件皆有 switch 覆寫）。

### 族群層級資金流向（MoneyFlow）—— `rotation.go: moneyFlowRatio()`

MFI 式：每日典型價 (H+L+C)/3 × 量，依典型價漲跌計入正 / 負流，近 5 日
正規化為 [−1, +1]。`classifyFlow`：≥+0.2 流入 ↑ / ≤−0.2 流出 ↓ / 中間中性。

---

## Relative Strength

### 個股相對強勢

- `Return20`：20 日報酬（%），是個股相對強度的基礎來源。
- 飆股分數第 2 組：個股 ret20 是否 > 所屬族群 AvgReturn20（贏 +8、輸 +3），
  即「個股相對於自己族群是否更強」。

### 族群相對強勢（跨族群正規化）—— `rotation.go: normalizeRelStrength()`

- 取各族群 `AvgReturn20`，做 **min-max 正規化** → 0–100 的 `RelStrength`。
- 全族群同強時，每個給 50。
- `RelStrength` 是 Sector Score 最大權重組件（30%）。

> 註：目前相對強勢是**自定義族群池內**的橫向比較，沒有對大盤（加權指數 / 0050）
> 的 beta 或 alpha 計算（見 Open Questions）。

---

## Industry Strength

族群分析是整個系統的上游，實作於 `internal/scanner/rotation.go`，
族群定義在 `configs/sectors.yaml`。

### 族群清單（configs/sectors.yaml）

格式：`sectors[].name` + `stocks[]{code, name, market?}`。一檔可屬多個族群
（去重抓取後再分配回各族群，`main.go: groupBySector`）。目前為人工維護的種子清單，
已含：矽光子、PCB、CCL、ABF、重電、電機機械、玻璃基板、矽晶圓、半導體材料、記憶體、
被動元件、鑽孔、低軌衛星、機器人、ASIC、金融股、電子零組件、伺服器、電源、IC設計、
工業機械、鋼鐵、紡織、水泥、塑化…等。

> 分類原則（近期決策）：每檔盡量只歸一個族群、避免重疊。例：「重電」（華城 / 中興電 /
> 士電 / 亞力）與「電機機械」（東元 / 大同）與「工業機械」（工具機）三者切開。

### Sector Score（0–100）—— 五大組件加權

| 權重 | 組件 | 定義 |
|------|------|------|
| 30% | RelStrength 相對強度 | 族群平均 20 日報酬，跨族群 min-max 正規化 |
| 25% | NewHighRatio 新高比例 | 創 60 日新高的成員占比 |
| 20% | BreakoutRatio 突破比例 | 突破 20 日整理高點 / 站上布林上軌的成員占比 |
| 15% | VolExpansion 量能放大 | 量比 ≥ 1.5 的成員占比 |
| 10% | MA60Slope | MA60 近 5 日上揚的成員占比 |

### 機會調整排序（OppScore）

排序**不是**用 Sector Score，而是用機會調整分數：

```
blended  = 0.5 × Score + 0.5 × ShortTermFlowScore
OppScore = blended × stageWeight(Stage)
```

讓「資金剛流入、20 日尚未反映」的 EARLY 族群提早浮上來，HOT / LATE 往後淡化。
排第一的不一定分數最高，而是「最值得布局」者。

### 短線流向分數（ShortTermFlowScore，0–100）

由 1~5 日 breadth / 動能組成（權重和為 1.0）：1d 漲幅 0.10、3d 0.15、5d 0.10、
上漲家數比 0.15、量能放大比 0.15、站上 5/10 日均線比 0.15、創 20 日新高比 0.10、
動能加速 0.10。`ShortTermFlowDir`（INFLOW/OUTFLOW/NEUTRAL）由 3 日漲幅 + 上漲家數比判定。

### 成員快照（SectorStock）

展開族群可見每檔：Close、Return20、是否新高 / 突破、量比、MA60 狀態、MoneyFlow 箭頭、
重用個股 `analyze()` 的 Action、以及 1/3/5 日漲幅等短線欄位。

---

## Breakout Detection

突破判斷散落在三處，定義略有不同（見 Technical Debt）：

### 1. 族群成員突破（rotation `memberSnapshot`）

`Breakout = 收盤 > 前 20 根整理高點(consoHigh)` **或** `收盤 > 布林上軌`。
`NewHigh = 收盤 ≥ 前 60 根最高(priorHigh)`。`NewHigh20` 同 consoHigh 條件。

### 2. 整理 / Base 偵測（`consolidation.go: analyzeConsolidation`）

由最後一根往回擴張視窗，找「相對其長度仍 tight」的整理區：
range cap = `0.08 + 0.004×k`（長 base 容許較寬）。產出：

- `Bucket`（型態分類，僅分類不加扣分）：MICRO_BASE 3–5 / SHORT_BASE 6–10 /
  SWING_BASE 11–20 / MID_BASE 21–40 / LONG_BASE 41–60 / NO_BASE 無明顯整理。
- `PivotHigh`（突破價，近 60 根最高）、`BaseLow`（支撐 / 平台下緣）。
- `VolumeDryUpRatio`（base 均量 / base 前 10 日均量，<1 = 量縮）。
- `PriceCompressionScore`（tightness + 布林收縮）、`SupportHoldScore`（守住 MA10 + 低點墊高）。
- `NearPreviousHigh`（收盤 ≥ 前高×0.97）、`HigherLows`、`BigVolDown`（爆量長黑）、`BrokePlatform`（收破平台）。
- `BaseQualityScore` = 0.25×壓縮 + 0.25×量縮分 + 0.25×支撐 + 接近前高(+10) +
  族群流入(+15) − 爆量長黑(20) − 跌破平台(30)，clamp [0,100]。

### 3. 飆股「剛突破」（`rocket.go: justBroke`）

`收盤 > 突破點(consol.PivotHigh)` 且 `昨收 ≤ 突破點×1.001` 且 `量比 ≥ 1.3`
→ 觸發 BREAKOUT_START。突破時量比 < 1.5 會被標記「假突破」風險。

### 4. 回測 pattern（`backtest.go`，含 `VOLUME_BREAKOUT_AFTER_BASE` 等）

見 Scanner 內建 5 種 pattern，皆 as-of bar `i` 偵測，無前視。

---

## Scanner Result Structure

執行流程（`cmd/scanner/main.go`，四階段 + 一個連動步驟）：

```
[1/4] 讀 stocks.yaml → 抓 Positions / Watchlist OHLCV
[2/4] 抓全市場清單（TWSE/TPEX）→ ScanMarket（過濾 + 排序 + TopN）
[3/4] 讀 sectors.yaml → 抓族群成員（去重）→ ScanRotation
[3.5] EnrichWatchlist：把 Watchlist 個股連動到族群輪動，產生飆股決策卡
[4/4] report.Generate → reports/report_YYYYMMDD.html
```

### 主要輸出型別

- **`StockAnalysis`**（market / portfolio 共用）：現價、量、（持倉）成本 / 股數 /
  損益、Score、Action、Reasons、BFP（0–5 + 各條件）、價位（Entry/Stop/T1/T2）、
  指標快照（RSI/MA20/MA20Trend/KDJ/BB/量比/ATR）、量能分析、漲停狀態。
- **`SectorRotation`**（族群）：Score / OppScore / Stage、五大組件、AvgReturn20 / AvgRSI、
  MoneyFlow / FlowState、三層輪動欄位（短線分數 / 方向 / 階段 / 一句話、中期強度 / 標籤、
  60 日趨勢 / 標籤）、成員 `[]SectorStock`。
- **`WatchlistEntry`**（飆股決策卡，最豐富）：內含 `StockAnalysis` + 族群連動
  （Sector / FlowDir / MidLabel / Stage / Note）+ `Consolidation` + `Backtest` +
  RocketScore / Stage / ExplosionProb / DaysToWatch + 價位計畫（突破 / 支撐 / 停損 /
  進場區 / 停利區）+ WatchAction + Reasons + 風險（Label / Warning）。

### 輸出格式

- **HTML 報告**：`reports/report_YYYYMMDD.html`（`internal/report/report.go` 產生），
  分頁：市場掃描、持倉、🚀 飆股候選（兩層：精簡列表 → 展開決策卡）、🔄 輪動。
- **Console**：進度與「早期輪動候選」摘要。

### 執行參數（旗標）

`--no-market` / `--top N` / `--all` / `--no-rotation` / `--date YYYY-MM-DD` /
`--stocks` / `--sectors` / `--config`。Makefile 捷徑：`run-fast` / `run-rotation` /
`run` / `run-top100` / `run-top500` / `run-all`。

### 過濾門檻（market scan）

`min_price`（預設 10.0）、`min_avg_volume`（預設 500000，對 VolumeMA[n-1]）；
所有分析普遍要求 ≥ 30 根 K 棒、回測 ≥ 40 根。

---

## Position Management（補充：持倉進出場規則）

> 不在使用者指定章節，但屬核心設計，補記於此。`scorer.go: positionAdvice()` 覆寫 base action：

- **STOP LOSS**：虧損 ≤ −15%（無條件）；或 ≤ −7% 且 MA20 下彎 + KDJ 死叉；或 ≤ −10% 且（下彎或死叉）。
- **TAKE PROFIT**：浮盈 ≥ 30%；或 ≥ 15% 且 RSI > 72；或 ≥ 15% 且 KDJ 死叉；或 ≥ 20% 且 MA20 下彎。
- **REDUCE**：浮盈 ≥ 12% 且 RSI > 65。
- **價位計畫**（`priceTargets`）：停損取 ATR(2×) 與布林下軌×0.99 之較高者，
  fallback entry×0.93；T1 = entry + 2×risk 或布林上軌取高；T2 = entry + 3.5×risk。

---

## Backtest（補充：型態歷史勝率）

`backtest.go`：偵測當前 bar 的 pattern（依優先序第一個命中），在
**同 pattern + 同整理 bucket** 的歷史情境統計未來 5 日報酬 / 最大回撤 / 勝率 / 風報比。
雙層：個股自身歷史 + 族群成員歷史 pool（樣本更多、信心更高）。
Confidence：sector ≥30 或 stock ≥15 → HIGH；≥12 / ≥6 → MEDIUM；else LOW。
Pattern 優先序：VOLUME_BREAKOUT_AFTER_BASE > PULLBACK_THEN_STRENGTH >
SECOND_ATTACK_NEAR_HIGH > MA_BULL_PULLBACK > GENERIC_STRENGTH。

---

## Future Roadmap

> 尚未實作，依價值排序的候選方向：

1. **大盤 / 相對強弱基準**：個股 / 族群相對加權指數或 0050 的 RS（目前只有族群池內橫向比）。
2. **族群清單自動化**：目前 sectors.yaml 為人工種子清單，考慮接產業分類資料源自動更新。
3. **回測框架化**：把「型態歷史勝率」升級為可調參數、含交易成本 / 滑價的策略回測。
4. **資料源備援**：Yahoo 限速時的第二來源（TWSE/TPEX 歷史、其他 API）。
5. **自動排程 / 推播**：交易日收盤後自動跑 + 報告推送（Email / LINE / Slack）。
6. **持倉風控進階**：移動停損、分批進出、跨持倉曝險彙總。
7. **參數可配置化**：目前許多門檻（分數帶、stage 判準、權重）寫死於程式，宜外移到 config。
8. **單元測試覆蓋**：現有測試（rotation / watchlist / limitup / sectors / yahoo_backfill）
   擴充到 scorer / consolidation / rocket / backtest 的關鍵分支。

---

## Technical Debt

> 目前已知問題 / 不一致，動到相關模組時應留意：

1. **`calcBuySellRatio` 是近似值**：註解明說「應使用 (close−low)/(high−low)」，
   實際只用近 5 日上漲天數比例，未用到 high/low。買賣比因此偏粗略。
2. **突破定義在三處不一致**：rotation（20 根高點 OR 布林上軌）、consolidation（PivotHigh =
   60 根高點）、rocket（justBroke 用 PivotHigh）。語意接近但門檻不同，易混淆。
3. **大量寫死的 magic numbers**：分數帶、stage 判準、各權重、漲停閾值（9% / 0.97 / 1.5）
   散落程式中，未集中管理或外移 config。
4. **`ScanWatchlist` 形同未使用**：watchlist 實際走 `EnrichWatchlist`（3.5 步），
   `scanner.go: ScanWatchlist` 仍存在但主流程未呼叫。
5. **自動偵測市場的成本**：留空 market 會多一次網路請求且僅在「網路失敗」才換 suffix，
   無資料（API 回空）不會換 → 偶有判錯市場風險。README 建議明確填 market。
6. **`windowHighLowF` 是 `windowHighLow` 的薄包裝**：可移除。
7. **報告為單檔 HTML 模板**：`report.go` 體量大、樣式與邏輯耦合；尚未模組化。
8. **history_range 與回測信心耦合**：預設 2y，但若使用者調短，backtest 樣本驟減、
   confidence 降為 LOW，UI 端未特別提示原因。
9. **無 CI / 整合測試**：抓取層（Yahoo/TWSE/TPEX）依賴外部 API，缺離線 fixture 測試。

---

## Open Questions

> 尚未決定、需要產品 / 策略層拍板的事項：

1. **相對強度的基準要不要納入大盤？** 目前是族群池內橫向比較，是否加入對 0050 / 加權指數的 alpha？
2. **族群歸屬的單一性 vs 多重性**：近期決策傾向「每檔只歸一族群、避免重疊」，
   但金融、電子等大類天然跨族群，是否需要「主族群 + 次族群」的設計？
3. **sectors.yaml 由誰維護、多久更新？** 人工種子清單會過時，是否需要版本化 / 自動校對機制？
4. **Watchlist 的進出是否要自動化？** FAILED → REMOVE 是否應實際改寫 stocks.yaml，
   還是僅建議由人決定？
5. **回測 forward window 固定 5 日是否合適？** 對「1~4 週波段」的定位，是否應提供
   10 / 20 日 forward 的勝率？
6. **分數帶與 stage 閾值是否需要隨市場環境（多 / 空頭）自適應？** 目前為固定常數。
7. **漲停近似的誤判率有多高？** 無封單資料下，LOCKED / FAILED / DISTRIBUTION 的判準
   （9% / High×0.97 / 量比 1.5）是否需要用歷史資料校準？
8. **是否要支援 ETF / 權證 / 興櫃？** 目前範圍是上市 + 上櫃普通股。

---

*（本文件描述「目前設計」，不含程式碼。實作細節以對應原始檔為準。）*
