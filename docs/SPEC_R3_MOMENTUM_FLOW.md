# R3 Implementation Spec — MomentumFlow（動能流向，與 RocketStage 聯合決策）

> **Design only — no code changes.** 把 `docs/SCANNER_ENHANCEMENT_PLAN.md`（Rev 2）的
> **R3：MomentumFlow 提權為雙主軸** 展開為可實作規格。
> 上游：`docs/SCANNER_MASTER_DESIGN.md`、`SPEC_R1_RS_RANKING.md`、`SPEC_R2_NEWHIGH_VCP.md`。
> 定位不變（EOD 波段、1~4 週、持有 5~20 日）。程式片段為型別 / 公式草圖，標落點檔案。

---

## 1. Overview & Scope

RocketStage 描述**位置**（築底 / 突破前 / 主升 / 過熱…），MomentumFlow 描述**方向**
（動能正在累積 / 延續 / 衰退 / 結構轉折）。R3 把兩者升級為**並列雙主軸**，共同決定
`WatchAction / ExplosionProb / RiskWarning / DaysToWatch / Reasons` 與 `RocketScore` 修正。

核心價值：**擋掉「位置對、但動能轉弱」的單**（如 `MAIN_RUN × FADING`），並在 `FAILED` 之前
用 `STRUCTURAL_SHIFT_DOWN` 提前示警。

**不在範圍**：週線確認（`WeeklyConfirm` 欄位先保留，預設 false，待 R4 接上）；
族群層級動能（已有 `ShortTermFlowStage`，與本因子並存、不合併，見 §11）。

---

## 2. Dependencies & Preconditions

| # | 事項 | 現況 | 要求 |
|---|------|------|------|
| D1 | **swing 高低偵測** | R2 VCP 將引入 zigzag（swing high/low） | R3 **共用** R2 的 zigzag helper（落點 `consolidation.go` 或抽到 `internal/scanner/swing.go`），不重造 |
| D2 | **還原股價** | `Candle.Close` 未還原（R1 D3） | 報酬 / 斜率類計算優先用還原收盤；結構偵測用近窗 OHLC（窗 ≤ ~40 日，分割落窗內罕見，可標旗標） |
| D3 | **漲停籌碼動態** | `detectLimitStatus` 已有 `LimitLockedLowVol` 等 | **FADING 偵測必須尊重「量縮≠轉弱」**：漲停鎖量不得判為動能衰退（§4.3 guard） |
| D4 | **既有 rocket 中間量** | `computeRocket` 已算 ma5/10/20、ret1/5/20、rsi、bullAlign、extFrom5、volRatio、justBroke、candle shape | R3 直接複用，不重算 |

---

## 3. Data Structures

### 3.1 `MomentumFlow`（列舉）

| 值 | 含義 |
|----|------|
| `MOMENTUM_BUILDING` | 動能累積（由平/低轉升） |
| `MOMENTUM_CONTINUATION` | 動能延續（既有上升動能維持） |
| `MOMENTUM_FADING` | 動能衰退（高檔轉弱 / 背離） |
| `STRUCTURAL_SHIFT_UP` | 向上結構轉折（翻多） |
| `STRUCTURAL_SHIFT_DOWN` | 向下結構轉折（翻空，FAILED 前兆） |
| `MOMENTUM_NEUTRAL` | 預設 / 無明確方向（fallback） |

> 加入 `MOMENTUM_NEUTRAL` 作為 fallback，避免在訊號不明時被迫貼標籤。

### 3.2 `MomentumState`（落點 `internal/scanner/momentum.go`）

| 欄位 | 型別 | 說明 |
|------|------|------|
| `Flow` | MomentumFlow | 六態之一 |
| `Score` | float64 | 0–100 **偏多動能信心**（用於 RocketScore 修正的縮放） |
| `SlopeAccel` | float64 | 動能二階（短步調 − 長步調，>0 加速） |
| `Divergence` | bool | 價量 / 價-RSI **空頭背離** |
| `StructureTrend` | string | `HH_HL`(高高低低墊高) / `LH_LL`(高低墊低) / `HIGHER_LOWS` / `LOWER_HIGHS` / `MIXED` / `NEUTRAL` |
| `WeeklyConfirm` | bool | 週線是否同向（**R4 前恆為 false**） |
| `Note` | string | 一句話可解釋結論 |

掛載：`rocketInput += momentum MomentumState`；`rocketOutput += Momentum MomentumState`；
`WatchlistEntry` 透過 `rocketOutput` 帶出（或直接加欄位）。

---

## 4. Detection（僅日線 OHLCV）

落點：`internal/scanner/momentum.go: computeMomentum(candles, ind, consol) MomentumState`。

### 4.1 Building blocks（共用中間量）

```
S = momentum.accel_window_short (預設 3)
L = momentum.accel_window_long  (預設 20)
slopeShort = (ac[n-1]/ac[n-1-S] - 1) / S      # 每日步調（還原收盤）
slopeLong  = (ac[n-1]/ac[n-1-L] - 1) / L
SlopeAccel = slopeShort - slopeLong            # >0 加速、<0 減速（沿用 rotation 的 accel 思路）

rsiUp   = ind.RSI[n-1] > ind.RSI[n-1-S]        # RSI 近升
keyMA   = SMA(ac, momentum.key_ma)             # 結構關鍵均線（預設 20）
reclaim = ac[n-1] > keyMA[n-1] && wasBelow(keyMA, lookback)   # 站回關鍵均線
loseMA  = ac[n-1] < keyMA[n-1] && wasAbove(keyMA, lookback)   # 跌破關鍵均線
volUpBias = 近窗上漲日均量 > 下跌日均量          # 量偏買方
```

**結構（StructureTrend）**：用共用 zigzag（D1）取最近 2~3 個 swing 高 (SH) 與低 (SL)：
- `HH_HL`：SH 墊高 且 SL 墊高 → 多頭結構。
- `LH_LL`：SH 墊低 且 SL 墊低 → 空頭結構。
- `HIGHER_LOWS`：SL 墊高（底部改善，尚未 HH）。
- `LOWER_HIGHS`：SH 墊低（頭部轉弱）。
- 否則 `MIXED / NEUTRAL`。

**空頭背離（Divergence）**：取最近兩個 swing 高 `SHa(舊) → SHb(新)`，
若 `價格 SHb ≥ SHa` 但（`RSI@SHb < RSI@SHa` **或** `量@SHb < 量@SHa`）→ `Divergence=true`。

### 4.2 分類順序（priority；先判規模較大/較急者）

```
1) STRUCTURAL_SHIFT_DOWN
2) STRUCTURAL_SHIFT_UP
3) MOMENTUM_FADING
4) MOMENTUM_CONTINUATION
5) MOMENTUM_BUILDING
6) MOMENTUM_NEUTRAL  (default)
```

### 4.3 各態判準

| Flow | 判準（皆日線） |
|------|----------------|
| **SHIFT_DOWN** | `consol.BrokePlatform` **或** (`loseMA` 且 `ret5 < 0`) **或** (`StructureTrend ∈ {LH_LL, LOWER_HIGHS}` 且 跌破前一 swing low) |
| **SHIFT_UP** | `reclaim`（站回 keyMA 且先前在其下 ≥ N 日）**或** `StructureTrend` 由 `LH_LL/LOWER_HIGHS` 轉為 `HIGHER_LOWS/HH_HL` |
| **FADING** | (`SlopeAccel < accel_neg_thresh` **或** `Divergence`) 且 仍高檔（`ac[n-1] > MA20` 且 `ret20 > 0`)。**Guard：** `limitStatus == LimitLockedLowVol` → 不可判 FADING（量縮≠轉弱，D3） |
| **CONTINUATION** | `bullAlign`(MA5>MA10>MA20) 且 `ret20 > 0` 且 `|SlopeAccel|` 小（穩定）且 `!Divergence` 且 `ac[n-1] > MA10` |
| **BUILDING** | `SlopeAccel > accel_pos_thresh` 且 `rsiUp` 且 RSI 在中低區（如 35~60）且 `volUpBias` 且 `!extended`(extFrom5 不大) |
| **NEUTRAL** | 以上皆否 |

### 4.4 Score（0–100，偏多動能信心）

```
s = 50
s += clamp(SlopeAccel * accel_scale, -25, +25)     # 加速度貢獻
if volUpBias: s += 8 else: s -= 4
switch StructureTrend: HH_HL +15 / HIGHER_LOWS +8 / LOWER_HIGHS -8 / LH_LL -15 / else 0
if Divergence: s -= 12
MomentumState.Score = clamp(s, 0, 100)
```

> Score 主要供 §6 的 RocketScore 修正縮放與卡片顯示；Flow 標籤才是決策主鍵。

---

## 5. Joint Decision：RocketStage × MomentumFlow

落點：`rocket.go`，在既有 stage 判定**之後**套用。

### 5.1 WatchAction 覆寫矩陣

| RocketStage \ Flow | BUILDING | CONTINUATION | FADING | SHIFT_UP | SHIFT_DOWN |
|--------------------|----------|--------------|--------|----------|------------|
| **PRE_BREAKOUT** | PREPARE_ENTRY（最高信心） | PREPARE_ENTRY | **WAIT**（降級） | PREPARE_ENTRY | **REMOVE**/觀望 |
| **BREAKOUT_START** | BREAKOUT_BUY | BREAKOUT_BUY | 縮手等量增 | BREAKOUT_BUY | REMOVE/減碼 |
| **MAIN_RUN** | WATCH_CLOSELY | 拉回 PULLBACK_BUY | **TAKE_PROFIT**（提前） | WATCH_CLOSELY | **TAKE_PROFIT**/減碼 |
| **BASE_BUILDING** | WATCH_CLOSELY | WATCH_CLOSELY | WAIT | **WATCH_CLOSELY（升級）** | REMOVE |
| **OVERHEATED** | TAKE_PROFIT | TAKE_PROFIT | **TAKE_PROFIT** | TAKE_PROFIT | **REMOVE** |
| **NOT_READY** | WAIT | WAIT | WAIT | WATCH_CLOSELY | REMOVE |
| **FAILED** | REMOVE | REMOVE | REMOVE | WATCH_CLOSELY（觀察反轉） | REMOVE |

- `NEUTRAL` → 回退到既有 `watchActionFor(stage, …)`（不覆寫）。
- **規則優先**：`SHIFT_DOWN` 一律走「REMOVE / 減碼」分支（先於 FAILED 的風險前置）。

### 5.2 ExplosionProb 調整（避免雙重計分）

> Rev 2 原則：**一個調分（RocketScore）、一個調機率標籤（ExplosionProb）**，不可互相疊乘。

機制：
1. RocketScore 先吃 §6 的動能修正項 → `explosionProb(stage, modifiedScore)` 自然反映（**不另外 notch**）。
2. 僅保留**類別式 guardrail 覆寫**（categorical，非加成）：
   - `SHIFT_DOWN` → 強制 `LOW`。
   - `MAIN_RUN × FADING` → 強制 `LOW`。
   - `(PRE_BREAKOUT|BREAKOUT_START) × (BUILDING|SHIFT_UP)` 且 `consol.IsVCP`(R2) → 允許 `HIGH`。

### 5.3 DaysToWatch / RiskWarning / Reasons

- **DaysToWatch**：`BUILDING/SHIFT_UP` → 縮短（接近發動）；`FADING` → 「等回檔再評估」；`SHIFT_DOWN` → 「—」。
- **RiskWarning**（與既有 `rocketRisk` 合流，取最嚴重者）：
  - FADING → 「動能轉弱 / 創高量縮，提防假突破」。
  - SHIFT_DOWN → 「結構轉空、跌破關鍵支撐（先於型態失效）」。
- **Reasons**：每個 Flow 對應一句話（如「突破前夕＋動能正在累積，密切準備」）。

---

## 6. RocketScore Modifier

落點：`computeRocket` 末段（算完 `out.Score` 後）。

```
mod = momentum.score_modifier[Flow]            # 見 §7 config
out.Score = int(clampFloat(float64(out.Score) + mod, 0, 100) + 0.5)
```

- 修正項**有上限**（`|mod| ≤ modifier_cap`，預設 12）；先加修正再 clamp 0–100。
- 預設：CONTINUATION +6、BUILDING +5、SHIFT_UP +8、NEUTRAL 0、FADING −6、SHIFT_DOWN −12。
- **此為唯一「調分」管道**；ExplosionProb 只做 §5.2 的類別覆寫，避免雙重計分。

---

## 7. Config Additions

```yaml
scanner:
  momentum:
    enabled: true
    accel_window_short: 3
    accel_window_long: 20
    accel_pos_thresh: 0.0008      # slopeShort-slopeLong 超過 → 加速（需以台股資料校準）
    accel_neg_thresh: -0.0008
    accel_scale: 12000            # 將 SlopeAccel 映射到 ±25 的縮放（隨 thresh 一起校準）
    key_ma: 20                    # 結構關鍵均線（20 或 50）
    reclaim_lookback: 5           # 站回 / 跌破關鍵均線的「先前」視窗
    zigzag_pct: 3.0               # 共用 R2 swing 偵測（建議與 vcp.zigzag_pct 同源）
    score_modifier:
      building: 5
      continuation: 6
      shift_up: 8
      fading: -6
      shift_down: -12
      neutral: 0
    modifier_cap: 12
```

`enabled:false` → 不算動能、Flow=NEUTRAL、WatchAction/ExplosionProb 回既有邏輯、Score 不修正（向後相容）。

> `accel_*` 與 `accel_scale` 的數值高度依賴台股價格尺度，**預設為起點，須以 R5 回測校準**。

---

## 8. Integration & Flow

```
EnrichWatchlist(item):
   ind   = calcIndicators(candles)
   consol = analyzeConsolidation(...)            # R2 後含 VCP
   mom   = computeMomentum(candles, ind, consol) # R3 新
   rk    = computeRocket(rocketInput{..., momentum: mom, ...})
            ├─ 既有：算 stage / score / 價位
            ├─ §6：Score += score_modifier[mom.Flow]（clamp）
            ├─ §5.1：WatchAction = jointAction(stage, mom.Flow)（NEUTRAL 不覆寫）
            ├─ §5.2：ExplosionProb guardrails
            └─ §5.3：DaysToWatch / RiskWarning / Reasons 併入 mom
   e.MomentumState = mom
```

落點檔案：`internal/scanner/momentum.go`（新）、`rocket.go`（聯合決策 + 修正）、
`watchlist.go`（呼叫 + 帶出）、`swing.go`（與 R2 共用 zigzag，可選抽出）。

---

## 9. Edge Cases

| 情境 | 處理 |
|------|------|
| 資料 < `accel_window_long + S` | Flow=NEUTRAL、Score=50、不修正 |
| 震盪無清楚 pivot | StructureTrend=NEUTRAL；SHIFT 條件多半不成立 |
| **漲停鎖量** | §4.3 guard：不得判 FADING（量縮≠轉弱，D3） |
| 訊號衝突（同時像 FADING 又像 CONTINUATION） | §4.2 priority order 決定（FADING 先於 CONTINUATION） |
| 分割落在近窗 | 斜率用還原收盤規避；結構偵測可標資料品質旗標 |
| `STRUCTURAL_SHIFT_DOWN` 與 stage `FAILED` 同時 | 一致（都導向 REMOVE）；SHIFT_DOWN 的價值在 **FAILED 之前**就觸發 |
| 反彈逃命波（跌深後急彈） | SHIFT_UP 需 `reclaim` 或結構翻多佐證，單純一根長紅不算（避免假轉折） |

---

## 10. Backward Compatibility

- 新欄位預設零值 / NEUTRAL；`momentum.enabled=false` → 與今日行為相容。
- WatchAction：NEUTRAL 不覆寫，回退 `watchActionFor`。
- ExplosionProb：無 guardrail 命中時，等同既有 `explosionProb(stage, score)`。
- Composite Score / 市場掃描 / HTML 既有欄位不動（新增為主）。

---

## 11. 與族群 ShortTermFlowStage 的分工（明確化）

- `MomentumFlow`＝**個股自身**動能方向；`ShortTermFlowStage`（rotation）＝**族群**短線資金。
- 兩者**並存、不合併**：卡片分兩列顯示（「個股動能：CONTINUATION」「族群短線：EARLY_ROTATION」）。
- 決策上：個股 MomentumFlow 主導 WatchAction；族群 flow 已在 RocketScore 第 1 組計分，
  此處不重複計分（避免與 §6 疊加）。

---

## 12. Report / UI

- 卡片新增「動能」列：Flow 標籤（BUILDING 綠 / CONTINUATION 藍 / FADING 橙 / SHIFT_UP 深綠 / SHIFT_DOWN 紅 / NEUTRAL 灰）+ `Note`。
- 二維摘要徽章：`PRE_BREAKOUT × BUILDING`、`MAIN_RUN × FADING` 等，一眼看出「位置×方向」。
- FADING / SHIFT_DOWN 時 RiskWarning 以紅字置頂。

---

## 13. Test Plan

落點：`internal/scanner/momentum_test.go`（+ rocket 聯合決策測試）。

**偵測**
1. 合成「由平轉升 + 量增」→ BUILDING；「多頭排列穩定」→ CONTINUATION。
2. 「創高但 RSI / 量背離」→ FADING（且 Divergence=true）。
3. 「站回 keyMA + 低點墊高」→ SHIFT_UP；「跌破平台 / 失守 keyMA + ret5<0」→ SHIFT_DOWN。
4. **漲停鎖量序列 → 不可判 FADING**（D3 guard 回歸）。
5. 衝突訊號 → priority order 正確（FADING 勝 CONTINUATION）。

**聯合決策**
6. 矩陣抽樣：`PRE_BREAKOUT×BUILDING→PREPARE_ENTRY`、`PRE_BREAKOUT×FADING→WAIT`、
   `MAIN_RUN×FADING→TAKE_PROFIT`、`任意×SHIFT_DOWN→REMOVE`。
7. ExplosionProb guardrail：`SHIFT_DOWN→LOW`、`MAIN_RUN×FADING→LOW`、
   `PRE_BREAKOUT×BUILDING且IsVCP→HIGH`；且**未命中時**等同既有 explosionProb。
8. Score 修正：各 Flow 修正值正確、`|mod|≤cap`、clamp 0–100。

**回歸**
9. `momentum.enabled=false` → 輸出與基準一致。

---

## 14. Implementation Sub-tasks（順序）

1. **R3.1** — `swing.go`：抽出 / 共用 R2 zigzag（swing 高低）。
2. **R3.2** — `momentum.go`：building blocks（SlopeAccel / Divergence / StructureTrend）+ `MomentumState`。
3. **R3.3** — Flow 分類（priority order + §4.3 判準 + 漲停 guard）+ Score。
4. **R3.4** — `rocket.go` 聯合決策：WatchAction 矩陣 + ExplosionProb guardrails + DaysToWatch。
5. **R3.5** — RocketScore 修正項（bounded）+ Reasons / RiskWarning 併入。
6. **R3.6** — config 綁定 + report 二維徽章 + 動能列。
7. **R3.7** — 測試（§13）。

> 依賴：**R3 接在 R2 之後**（共用 zigzag、且 §5.2 HIGH guardrail 用到 `consol.IsVCP`）。
> 最小可用版：R3.1–R3.4（動能進決策）→ R3.5 修正分 → UI/測試。

---

## 15. Resolved Decisions

- MomentumFlow 與 RocketStage **並列雙主軸**，以 §5.1 矩陣決定 WatchAction。
- **唯一調分管道＝§6 RocketScore 修正項（上限 12）**；ExplosionProb 僅做類別 guardrail，**不雙重計分**。
- 加 `MOMENTUM_NEUTRAL` fallback；NEUTRAL 不覆寫既有行為。
- FADING **尊重漲停鎖量**（量縮≠轉弱）。
- `MomentumFlow`（個股）與 `ShortTermFlowStage`（族群）**並存不合併、不重複計分**。
- `WeeklyConfirm` 欄位保留但**待 R4** 才接上週線。

---

## Open Questions

1. **`accel_pos/neg_thresh` 與 `accel_scale` 的台股校準值**：價格尺度差異大，是否改用百分比步調（已用報酬率）或 ATR 正規化？
2. **`key_ma` 用 20 還是 50？** 20 較敏感（早訊號、雜訊多），50 較穩（晚但乾淨）。可能分波段長短而異。
3. **SHIFT_UP 的「站回均線」需幾日確認？** 1 根易假轉折，2~3 根較穩但較慢。
4. **背離只看 swing 高，要不要也納入「價量背離於連續創高」**（非 pivot 版本）？
5. **OVERHEATED × BUILDING 是否真的存在**（過熱還在累積）？或應視為矛盾 → 降為 NEUTRAL？
6. **modifier 預設值（+5/+6/+8/−6/−12）** 需 R5 回測檢驗是否真的提升命中率，再固定。
7. **zigzag 門檻是否與 R2 完全共用同一參數**，還是動能偵測需更靈敏（較小門檻）？

---

*（本文件為 R3 實作規格，design only，不修改任何程式碼。）*
