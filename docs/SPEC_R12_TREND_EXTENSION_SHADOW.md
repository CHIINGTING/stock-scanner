# SPEC R12 — Trend / Extension / Sector Heat（Shadow Only）

> **Source of truth：commit `a4675aa` 的實作。** 本文件描述已存在的程式碼，不提出新設計。
>
> 本文件**不修改** production code、**不調整**任何 threshold、**不新增**功能，
> 也**不把 validation 結果改寫成 scoring 建議**。
>
> 數據細節見 [`docs/R12_VALIDATION_RESULTS.md`](R12_VALIDATION_RESULTS.md)，本文件只引用其結論。

本文件刻意分成三個層次，且**不得混用**：

| 層次 | 意義 | 可變更性 |
|---|---|---|
| **§A Design Contract** | 目前實作實際保證的行為 | 改動需改 code，且需通過 §A.9 的證明測試 |
| **§B Validation Findings** | 已觀測到的事實與其限制 | 只能被新的驗證取代，不能被「調參後比較好看」取代 |
| **§C Future Promotion Criteria** | 要離開 shadow-only 之前必須先成立的條件 | 尚未滿足，全部未實作 |

R12 與 Market Dashboard 內部的 regime 規則編號 `R0…R11` 無關，後者是決策表列號，不是 release 編號。

---

# §A Design Contract

## A.1 Feature scope / non-goals

### Scope

把掃描器**已經算出**的價格序列，導出三個彼此獨立、但只在合併後才有意義的讀數：

```
Slope       → 趨勢方向（均線自己的斜率，不是價格斜率）
BIAS        → 價格在該趨勢中的位置
SectorHeat  → 這個趨勢有沒有族群共振
```

合併成**一個確定性狀態**（§A.7），附上證據碼（§A.8），呈現於報表 ⑮。

### Non-goals（明確排除，不是「以後再說」）

| 不做 | 原因 |
|---|---|
| 影響 BUY / WATCH / SELL | R12 是 shadow layer，§A.4 為機制保證 |
| 影響 Score / RocketScore / Stage / 排序 / 部位大小 | 同上 |
| 產生買賣建議或進場指令 | 狀態是**描述**，不是動作 |
| 建立第二套 sector taxonomy | 沿用 `configs/sectors.yaml`（§A.6） |
| 重算既有指標 | 均線與乖離一律取自 `internal/indicator`（§A.5） |
| 新增第五套「EXTENDED」定義 | 分位不可得時 fallback 到既有 R10-1 標籤（§A.6.4） |
| 依回測結果調參 | §B.4；門檻在看到結果後未改動 |

## A.2 Architecture / call flow

```
internal/indicator            純函式，無狀態
  ├── SMA                     （既有，唯一均線來源）
  ├── BIAS                    （既有，唯一乖離公式）
  ├── MASlopePerDay           （R12 新增）
  ├── BIASSeries              （R12 新增）
  ├── PercentileRank          （R12 新增）
  └── BIASPercentile          （R12 新增）
          ↓
internal/scanner/rotation.go
  buildSector()               既有成員迴圈 ── 同一次迴圈內收集 HeatMemberInput
      └── ComputeSectorHeat() → SectorRotation.Heat（per-sector 部分）
  ScanRotation()
      ├── normalizeRelStrength()   （既有）
      └── NormalizeSectorHeat()    （R12：cross-sectional，只讀 Heat、只寫 Heat）
          ↓
internal/scanner/watchlist.go
  EnrichWatchlist()
      ├── computeRocket()          ← 分數／動作／機率／排序在此定案
      ├── …（R7-1 / R6-7 / R10-1 / R10-2 各 shadow 層）
      └── attachTrendExtension()   ← R12 POST-PASS，在上述全部之後
              ↓
      WatchlistEntry.TrendExt      ← ShadowSignals **之外**的專屬欄位
          ↓
internal/report/report.go
  ⑮ 區塊                        只格式化，零策略重算
```

**驗證用**（不在 production 路徑）：`internal/r6backtest/trendext.go` 以
`scanner.ComputeSectorHeat` / `NormalizeSectorHeat` / `DefaultTrendExtThresholds()`
重建歷史 cohort——**重用出貨中的程式與門檻**，而非複製一份。

## A.3 Default-off behavior

雙層開關，沿用 repo 既有慣例（`enable_*` 決定是否計算，`show_*` 決定是否顯示）：

```yaml
scanner:
  enable_trend_extension: false   # 預設 false
  show_trend_extension: false     # 預設 false
```

| 組合 | 行為 |
|---|---|
| `enable=false` | 完全不計算。`WatchlistEntry.TrendExt` 與 `SectorRotation.Heat` 皆為 **nil** |
| `enable=true, show=false` | 有計算、有掛載，報表不顯示 |
| `enable=true, show=true` | 計算 + 顯示 ⑮ |
| `enable=false, show=true` | 不計算，報表也沒有東西可顯示 |

**nil 而非空殼**：關閉時欄位是 nil，不是「計算後清空」。這是報表輸出 byte-identical
的前提，也讓旗標真正零成本。

⑮ 的 CSS 只受 `ShowTrendExtension` 控制，不依附任何其他旗標。

## A.4 Shadow-only guarantees

四道機制，皆為結構性保證，不依賴註解或紀律：

1. **專屬欄位**：`WatchlistEntry.TrendExt` 刻意放在 `ShadowSignals` **之外**。
   C6b guardrail scoring 只讀 `ShadowSignals`，因此 R12 資料**在型別上就到不了計分路徑**。
2. **Post-pass 時序**：`attachTrendExtension` 在 `computeRocket` 之後執行。
   執行當下分數、動作、機率、排序均已定案。
3. **Heat 不進 rotation 分數**：`SectorRotation.Heat` 不參與 `Score` / `OppScore` /
   `Stage` / 排序（§A.6.5）。
4. **回歸證明**：以真實 `EnrichWatchlist` 輸出的 JSON 指紋比對（§A.9）。

**R12 不影響**：`BUY` / `WATCH` / `SELL`、`Score`、`RocketScore`、`RocketStage`、
`WatchAction`、`ExplosionProb`、排序、停損、部位大小、Market Regime、既有任何決策。

## A.5 MASlopePerDay

```
MASlopePerDay(ma, lookback) = ((ma[t] / ma[t-lookback]) - 1) / lookback * 100
```

單位：**% per trading day**。

R12 使用的兩個視窗：

```
MA20Slope5D  = MASlopePerDay(SMA(close, 20),  5)
MA60Slope10D = MASlopePerDay(SMA(close, 60), 10)
```

**為何是比例而非相減**：`ma[t] - ma[t-5]` 的單位是「元」，NT$500 與 NT$20 的股票無法比較。
除以 lookback 讓不同視窗成為同一單位，MA20/5 日與 MA60/10 日可以並列在同一張決策表。

**測量對象是均線自己的斜率**，不是收盤價斜率。

**不可得即不可得**：歷史不足、或任一端點 ≤ 0（含 SMA 暖身期的 0）→ `ok=false`。
呼叫端存為 `nil`，**絕不回傳 0**——0 會被讀成「持平」，那是完全不同的事實。

## A.6 BIAS、SectorHeat 與其規則

### A.6.1 BIAS20Percentile

```
BIAS_N          = (Close - SMA_N) / SMA_N * 100        （沿用既有 indicator.BIAS）
BIASSeries      = 每一根「均線已成立」的 bar 的 BIAS_N（暖身期略過，不補 0）
BIAS20Percentile = BIAS20[今日] 在該股自身最近 252 筆 BIAS20 觀測中的百分位
```

- window = **252**（約一個交易年）
- 最小樣本 = **120**；不足 → **unavailable（nil）**，不退回中位數、不猜
- 百分位定義 = 樣本中 **≤ 今日值** 的比例（同值計入，平手不應被讀成「低於平均」）

**為何用分位**：固定的「+10% 算過熱」會在同一天把低波動公用事業判成過熱、
把高波動小型股判成正常。分位問的是唯一能跨股票遷移的問題——**這對「這一檔」而言有多不尋常**。

### A.6.2 SectorHeat formula

```
Heat = 0.35 × Breadth
     + 0.30 × RelativeMomentum
     + 0.20 × Participation
     + 0.15 × VolumeConfirmation
```

輸出 0–100。四項權重為 **baseline 假設**（「breadth 最重要、量能最次要」），非校準常數。

### A.6.3 Component definitions

| 成分 | 定義 | 備註 |
|---|---|---|
| **Breadth** | 收盤 > 自身 MA20 的成員比例 | 分母是**MA20 可計算的成員數**，不是全體。MA20 不可得者排除，不計為「未站上」 |
| **RelativeMomentum** | 該族群 **median** 20 日報酬，在所有可測族群中的 cross-sectional 百分位 | 跨族群比較，非絕對報酬門檻。只有一個可測族群時 = 50（誠實的「無跨切面資訊」） |
| **Participation** | 20 日報酬為正的成員比例 | 區分「整族一起漲」與「一兩檔權值股拉抬」 |
| **VolumeConfirmation** | 量比 ≥ 1.5（沿用既有 `volExpRatioMin`）的成員比例 | **比例形式**：某檔 50 倍爆量與另一檔 1.6 倍，計入權重相同 |

**Robust aggregation**：報酬用 **median**，其餘三項用**比例**。全程無平均數。
既有 `SectorRotation.AvgReturn20` 是平均數，一檔漲停就能代表整族——這正是 Heat 要避免的。

### A.6.4 「Extended」的判定與 fallback

```
extended = (BIAS20Percentile 可得) ? BIAS20Percentile >= 90
                                   : BiasView.Risk ∈ {HIGH, EXTREME}
```

分位為主。分位不可得時 fallback 到**既有的 R10-1 標籤**，而非另立一組數字——
repo 內已有四處各自定義「過度延伸」（§C.4），R12 刻意不成為第五處。

### A.6.5 ValidMemberCount >= 4 rule

```
ValidMemberCount < 4  →  Status = INSUFFICIENT_DATA
                         Heat 與四項成分皆不發布（維持 0）
                         MemberCount / ValidMemberCount / Coverage 仍照實揭露
```

**門檻依實際 taxonomy 訂定**：`configs/sectors.yaml` 有 36 個族群、成員數中位數 6、
最小 2（`電機機械`、`電感`）。在 4 的門檻下，比例以 25% 為級距——粗，但每一級都有真實成員支撐。
低於 4 時「breadth 百分比」只是算術表演：2 檔成員的比例只可能是 0、50 或 100。

**INSUFFICIENT_DATA ≠ 0。** 讀取端必須先檢查 `Status`。未檢查而直接讀數值者得到 0，
而不是一個看似合理、可能被拿去行動的中間值。

不可測族群**同時被排除於 cross-sectional 排名母體之外**——測不到的族群不得影響測得到的族群的名次。

### A.6.6 SectorHeat vs SectorRotation.Score

**Heat 是 `SectorRotation` 的 component，不是第二個族群分數。**

| | `SectorRotation.Score` | `SectorRotation.Heat` |
|---|---|---|
| 回答 | 哪個族群強 | 這個漲勢**廣不廣、參與度夠不夠** |
| 聚合 | 平均數為主 | median 與比例 |
| 小樣本保護 | 無 | `ValidMemberCount >= 4` |
| 影響 `Score`/`OppScore`/`Stage`/排序 | 是（既有行為） | **否** |
| 影響 `RocketScore` g1 族群群組 | 是（既有行為） | **否** |
| 預設 | 一律計算 | 需 `enable_trend_extension` |

Heat 只被 R12 shadow layer 與報表讀取。刻意不新增第二個 0–100 族群分數，
否則報表會出現兩個互相矛盾的族群評分。

## A.7 TrendExtension state machine

**確定性、有序決策表**，第一個命中的 case 勝出。順序是契約的一部分。

前置：`MA20Slope` 或 `BIAS20` 任一不可得 → `INSUFFICIENT_DATA`（不猜）。

判定變數：

```
up20      = MA20Slope >  +0.04 %/day        （flat band = 0.04）
down20    = MA20Slope <  -0.04 %/day
notDown60 = MA60Slope >= -0.04 %/day        （MA60 不可得 → false）
up60      = MA60Slope >  +0.04 %/day        （MA60 不可得 → false）
nearMA    = |BIAS20| <= 3.0 %
extended  = 見 §A.6.4
heatKnown / heatStrong(>=65) / heatHealthy(>=45) / heatWeak(<=35)
```

| # | 條件 | 狀態 |
|---|---|---|
| 0 | slope 或 BIAS 不可得 | `INSUFFICIENT_DATA` |
| 1 | `down20 && BIAS20 <= 0` | `WEAKENING` |
| 2 | `up20 && extended` | `EXTENDED` |
| 3 | `up20 && notDown60 && nearMA && !heatWeak` | `PULLBACK_IN_UPTREND` |
| 4 | `up20 && heatWeak` | `SECTOR_DIVERGENCE` |
| 5 | `up20 && notDown60 && heatStrong` | `TREND_CONFIRMED` |
| 6 | `up20 && heatStrong` | `SECTOR_CONFIRMED` |
| 7 | `up20 && up60 && heatHealthy` | `TREND_CONFIRMED` |
| 8 | 其他 | `NEUTRAL` |

### 順序為何是契約

**第 2 列必須排在所有多方列之前。** 這一個排序就是「EXTENDED 永遠不會升級成更強的買訊」
的機制保證：無論斜率多陡、族群多熱，只要 `extended` 成立就停在 EXTENDED。
測試以 heat × slope 的組合逐一枚舉驗證此性質。

**MA60 不可得不等於看空。** 通常代表新上市。需要長趨勢一致的 cell（3、5、7）明確要求它；
其餘 cell 不查詢它。缺 MA60 的個股仍會得到可用狀態。

## A.8 Context / evidence codes

由 analyzer 產生，報表**原樣呈現**，renderer 不重算任何判斷。

| 類別 | 代碼 |
|---|---|
| 斜率 | `MA20_SLOPE_UP` / `MA20_SLOPE_DOWN` / `MA20_SLOPE_FLAT` / `MA60_SLOPE_UP` / `MA60_SLOPE_DOWN` / `SLOPE_ALIGNED_UP` |
| 位置 | `BIAS20_EXTENDED` / `BIAS20_NEAR_MA` / `BIAS20_BELOW_MA` / `REBOUND_IN_DOWNTREND` |
| 族群 | `SECTOR_HEAT_STRONG` / `SECTOR_HEAT_WEAK` / `SECTOR_PARTICIPATION_STRONG` / `SECTOR_VOLUME_CONFIRM` / `SECTOR_HEAT_UNKNOWN` |

族群類代碼由**族群層產生一次**，經由 `TrendExtensionView.Codes` 攜帶下來，
不在個股層以第二組門檻重算。

`REBOUND_IN_DOWNTREND`（價格在**下彎**的 MA20 之上）獨立標記：單看價格會讀成偏多，實則不是。

## A.9 No scoring / action / sort impact — proof

契約由測試證明，不由註解宣稱：

| 測試 | 證明什麼 |
|---|---|
| `TestTrendExtensionDisabledIsByteIdentical` | 開關開啟後，真實 `EnrichWatchlist` 輸出的決策指紋（Score / Action / Reasons / 進出場價 / RocketScore / Stage / ExplosionProb / WatchAction / 風險 / 族群欄位 / Consol / Backtest / Shadow）**逐位元相同** |
| `TestTrendExtensionPreservesOrder` | post-pass 不重排觀察清單 |
| `TestTrendExtensionNilWhenDisabled` | 關閉時欄位為 nil，非空殼 |
| `TestSectorHeatNilWhenDisabled` | Heat 關閉時為 nil，且 rotation 的 `Score` / `OppScore` / `Stage` / 五大成分 / 三層輪動數值不變 |
| `TestSectorHeatInsufficientEndToEnd` | 3 成員族群走完真實 rotation 路徑後回報 `INSUFFICIENT_DATA`，且不發布 Heat 值 |
| `TestTrendExtDefaultOutputUnchanged` | 預設報表輸出 byte-identical |
| `TestExtendedNeverBecomesConfirmation` | EXTENDED 不因斜率／熱度升高而升級 |
| `TestPullbackIsNotPromoted` | 報表不得出現「加碼／逢低買進／高勝率／最佳買點」等措辭 |
| `TestTrendExtExtendedRendersAsWarning` | 報表不得出現「必跌／預期下跌／空頭反轉／賣出訊號」 |
| `TestUnknownSectorIsNotDivergence` | UNKNOWN 不被當作 WEAK |
| `TestConditionsAreCausal`（回測） | 截斷未來 bar 後每個條件判定不變 |
| `TestBacktestUsesShippedThresholds` | 回測量測的是出貨門檻，非鏡像副本 |

## A.10 語意契約（措辭層級的約束）

以下不是建議，是契約。報表、文件與程式註解皆須遵守。

### UNKNOWN ≠ WEAK

族群熱度「未知」與「弱」是兩件事，**不得合併**：

- 個股無族群、rotation 未執行、或族群成員不足 → `SECTOR_HEAT_UNKNOWN`
- 只有**已知且弱**（Heat ≤ 35）才會觸發 `SECTOR_DIVERGENCE`

缺資料永遠不得升級為負面判斷。這與 repo 全域的 `MISSING ≠ NEUTRAL` 原則一致。

### EXTENDED

```
語意：不要追 / 進場風險升高（Do not chase / elevated entry risk）
```

**不是**：SELL、看空反轉、預期下跌、必跌。

依據見 §B.2：分布不對稱——典型結果差、下檔深，但右尾存在續漲離群值。
把它轉成賣出訊號會在錯誤的方向上犯錯。報表標籤明文帶有「非賣出或看空訊號」。

### PULLBACK_IN_UPTREND

```
語意：上升趨勢中的技術性回檔（僅描述價格位置）
```

**不得**被描述為：較佳進場、加碼點、buy-the-dip edge、高勝率 setup。

依據見 §B.2：驗證**未支持**其為較優進場。顏色亦受此約束——它使用中性色帶，
不與 `TREND_CONFIRMED` 共用建設性色帶，否則等於視覺上宣稱兩者同樣好。

### TREND_CONFIRMED / SECTOR_CONFIRMED

```
TREND_CONFIRMED  ：短期趨勢向上、中期均線不相抵觸、且族群共振（cell 5 / 7）
SECTOR_CONFIRMED ：族群共振已成立，但中期趨勢（MA60）尚未翻揚 —— 族群領先、結構落後（cell 6）
```

兩者皆為**狀態描述**，不是進場指令。特別注意 §B.1：`TREND_CONFIRMED` 的表現改善
**不得**整體歸因於「斜率＋乖離＋族群熱度」三者的組合。

---

# §B Validation Findings

> 完整數據、cohort 表與原始引擎輸出見 [`docs/R12_VALIDATION_RESULTS.md`](R12_VALIDATION_RESULTS.md)。
> 此處只保存結論，且**不得**被改寫為 scoring 建議。

執行：`./bin/r6backtest -trendext`，1,983 檔，2024-06 → 2026-08。

## B.1 目前結論

```
SectorHeat        = primary promotion candidate
MA slope / BIAS   = incremental value UNPROVEN（在 conditioning on SectorHeat 之後）
```

`SECTOR_ONLY` 單獨即達 43.4% 的 20 日勝率，`TREND_CONFIRMED` 為 45.1%——兩者僅差 1.7 個百分點。
因此**不得**把 `TREND_CONFIRMED` 的改善整體歸因於三者組合，其中大部分可能已來自族群熱度本身。

## B.2 部分假設被否證

- **`PULLBACK_IN_UPTREND` 未獲支持為較優進場**：20 日勝率 29.9% **低於** baseline 34.7%，
  均報酬持平，僅回撤略淺。→ §A.10 的措辭約束由此而來。
- **`EXTENDED` 的分離是不對稱的**：中位數與回撤明顯較差，但均值高於中位數，
  代表右尾存在續漲離群值。→ 因此是「不要追」，不是「必跌」。
- **`SECTOR_DIVERGENCE` 方向與設計一致**：表現弱於 `TREND_CONFIRMED`，
  惟樣本僅 943 筆且受 §B.3 限制。

## B.3 Backtest limitations

| 限制 | 說明 |
|---|---|
| **統計信心：NOT ESTABLISHED** | 逐 bar 觀測在時間上重疊、同一 episode 內高度自相關；raw N 大幅高估 effective N。通用引擎輸出的 `confidence: HIGH` 對 R12 **不具統計意義**（該標籤只反映觀測筆數門檻） |
| **單一 regime** | 涵蓋期幾乎全為多頭，不足以支撐跨 regime 推論 |
| **Survivorship** | `.cache` 只含現存標的，絕對報酬與勝率偏樂觀；此偏誤同時作用於每一 cohort（含 baseline），故只能讀「相對同一 baseline 的差異」 |
| **熱度面板近似** | 預設每 5 個交易日重算一次（`-heatstep 1` 可改逐日）；此為明示的近似，非靜默取整 |

## B.4 門檻未依結果調整

所有門檻集中於 `internal/scanner/trendext.go` 單一 const 區塊，
且**在看到上述結果之後未更動**。回測量測的是出貨中的假設
（`scanner.DefaultTrendExtThresholds()`），不是為了報告而挑選的最佳組合。

這是刻意的：technical-rule data snooping 的文獻（Brock et al.；Sullivan/Timmermann/White）
討論的正是「因為在某個樣本上好看而被選中」的數字。

---

# §C Future Promotion Criteria

> **全部尚未滿足，全部尚未實作。** 在下列條件成立之前，R12 維持 shadow-only，
> 不進行任何 scoring promotion。

## C.1 Ablation（前置條件之首）

需建立 8 格矩陣：`BASELINE` / `SECTOR_ONLY` / `SLOPE_ONLY` / `BIAS_ONLY` /
`SECTOR+SLOPE` / `SECTOR+BIAS` / `SLOPE+BIAS` / `SECTOR+SLOPE+BIAS`。

**主要問題：**

```
在 SectorHeat 已知的條件下，MA slope 與 BIAS 是否提供增量預測資訊？
```

現況：cohort 1–4 已存在於本次輸出；5–8 未建立。**此問題必須先被回答。**

## C.2 State-entry deduplication

需比較 bar-level（現況）與 **state-entry** cohort：只計入 symbol **首次進入**該狀態的那一天。

```
NEUTRAL          → TREND_CONFIRMED              計入
TREND_CONFIRMED  → TREND_CONFIRMED              不計入
TREND_CONFIRMED  → NEUTRAL → TREND_CONFIRMED    再次計入
```

可選第二法：每 symbol / state 20 個交易日 cooldown。
目的是估計 effective N，讓 §B.3 的「NOT ESTABLISHED」得以前進。

**僅在能與 production code 乾淨隔離時才實作。**

## C.3 Cross-period / regime consistency

至少按 2024 H2、2025、2026 YTD 拆解；MarketRegime 可用後優先改以 regime 分層。

**Promotion 要求：效果在多個期間／regime 間方向一致**，而非僅憑 pooled 表現。

## C.4 Survivorship-aware validation

需要能處理已下市標的的母體，或明確量化其偏誤。
在此之前，R12 結果只能作為 cohort 相對差異，不得作為實盤預期報酬估計。

---

# §D Out-of-scope technical debt

Phase 0 audit 發現的既有問題。**R12 刻意不重構**，僅記錄：

| 項目 | 說明 | 為何不在 R12 處理 |
|---|---|---|
| `SectorRotation.MA60Slope` 誤名 | 實際內容是「MA60 上揚的成員比例」，是 breadth 不是 slope | **不得在 feature commit 內改名**——會製造與 R12 無關的相容性風險 |
| `internal/pricingpower` 孤兒套件 | 完整實作 + 測試，但除自身外無任何檔案 import | 與 R12 無關，重構風險自成一案 |
| 第二套 sector→theme mapping | 位於 `internal/pricingpower/theme.go`，與 `configs/sectors.yaml` 並存 | 同上；R12 本身未新增第二套對應 |
| 「EXTENDED」有四套定義 | `rocket.go` 的 `extFrom5 > 12`（**影響 Stage 與計分**）、`BiasView.Risk`、`pricingpower.OVEREXTENDED`、R12 的 BIAS20 分位 | R12 已避免成為第五套（§A.6.4）；統一四者需動到既有計分，屬獨立議題 |

亦不在 R12 內處理：共用的 `r6backtest` confidence 引擎（其 `HIGH` 標籤問題以 §B.3 的文件形式承載，
修改該引擎會牽動既有 R6 setup）。
