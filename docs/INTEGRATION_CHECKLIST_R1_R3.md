# Integration Construction Checklist — R1–R3（可施工版）

> **Design only — 本文件不改程式，是開工時照表施工的清單。**
> 來源：`SPEC_R1_RS_RANKING.md`、`SPEC_R2_NEWHIGH_VCP.md`、`SPEC_R3_MOMENTUM_FLOW.md`（預設值均已定案）。
> 上游：`SCANNER_MASTER_DESIGN.md`、`SCANNER_ENHANCEMENT_PLAN.md`（Rev 2）。
>
> **重要前提**：Scanner **已實作** 族群輪動、short_term_flow、backtest、rocket_candidate、
> watch_action/stage priority map、Rotation 分頁、Watchlist 飆股候選追蹤（兩層卡片）。
> R1–R3 **只新增** RS Rank、52 週高/多週期新高、VCP、MomentumFlow，並**擴充**既有計分與 UI。
> 下方一律標記：🆕 新增 / 🔧 修改既有 / ✅ 已實作（僅驗證，不重做）。

---

## 1. 會被動到的檔案清單

### 1.1 既有檔案（🔧 修改 / ✅ 驗證）

| 檔案 | 標記 | 變更 |
|------|------|------|
| `internal/fetcher/yahoo.go` | 🔧 | 解析 `indicators.adjclose`，null 退 raw close（R1.0） |
| `internal/fetcher/types.go` | 🔧 | `Candle += AdjClose float64`（缺值 = Close） |
| `internal/scanner/types.go` | 🔧 | `StockAnalysis` += RS 欄位、新高欄位 |
| `internal/scanner/scanner.go` | 🔧 | `Config` 內嵌 RS/NewHigh/VCP/Momentum 子設定；`analyze()` 填新高/RSBasis；新增 `ComputeRS()` |
| `internal/scanner/consolidation.go` | 🔧 | VCP 欄位 + `analyzeVCP` + BaseQuality 加成；抽 zigzag 至 `swing.go` |
| `internal/scanner/rocket.go` | 🔧 | `rocketInput` 加欄位；g2/g3 改寫；preBreak/explosionProb VCP-aware；R3 聯合決策 + Score 修正 |
| `internal/scanner/watchlist.go` | 🔧 | `EnrichWatchlist` 算新高/動能、收 RS table、排序軟門檻、帶出 MomentumState |
| `internal/scanner/rotation.go` | ✅ | 不必改；`memberSnapshot` 舊新高邏輯**可選**與 R2 統一（清債，不阻擋） |
| `cmd/scanner/main.go` | 🔧 | full mode 算 RS + 存快照 + 傳 rsTable；fast mode 載快照/退化 |
| `configs/config.yaml` | 🔧 | 新增 `scanner.rs / newhigh / vcp / momentum` 區段 |
| `internal/report/report.go` | 🔧 | 新增 RS/52w/VCP/動能 標籤與徽章；擴充 priority map（見 §5） |

### 1.2 新增檔案（🆕）

| 檔案 | 內容 |
|------|------|
| `internal/scanner/relstrength.go` | RSScore、母體百分位、非普通股過濾、snapshot save/load |
| `internal/scanner/newhigh.go` | 多週期新高旗標、52 週距離、NewHighScore |
| `internal/scanner/swing.go` | zigzag swing 高低偵測（R2 VCP 與 R3 共用） |
| `internal/scanner/momentum.go` | SlopeAccel/Divergence/StructureTrend、MomentumFlow 分類、MomentumState |
| `internal/scanner/relstrength_test.go` | RS 單元/百分位/快照測試 |
| `internal/scanner/newhigh_test.go` | 新高/52w 測試 |
| `internal/scanner/momentum_test.go` | 動能五態/guard 測試 |
| `.cache/rs_snapshot.json` | 執行期產生（fast mode 母體快照） |

### 1.3 分類整理

| 類別 | 檔案 |
|------|------|
| **config/yaml** | `configs/config.yaml`（🔧）；`scanner.Config`（🔧 內嵌型別） |
| **type（資料結構）** | `fetcher/types.go`、`scanner/types.go`、`scanner/consolidation.go`、`scanner/momentum.go`（🆕 型別） |
| **service（計算）** | `relstrength.go`、`newhigh.go`、`swing.go`、`momentum.go`（🆕）；`rocket.go`、`consolidation.go`、`watchlist.go`、`scanner.go`、`main.go`（🔧） |
| **fetch** | `yahoo.go`、`fetcher/types.go`（🔧 adjclose） |
| **UI** | `report.go`（🔧） |
| **test** | `relstrength_test.go`、`newhigh_test.go`、`momentum_test.go`（🆕）；`consolidation_test.go`、`rocket_test.go`、`watchlist_test.go`（🔧 補案例） |

---

## 2. 新增資料結構彙總

> 既有結構（sector/rotation、short_term_flow、backtest、rocket_candidate）**不新增欄位**，
> 列出僅供對照與接線確認。真正新增的只有 RS / 新高 / VCP / Momentum 四類 + `Candle.AdjClose`。

### 2.1 🆕 `fetcher.Candle`（R1.0）
| 欄位 | 型別 | 說明 |
|------|------|------|
| `AdjClose` | float64 | 還原收盤；adjclose 缺值時 = `Close` |

### 2.2 🆕 RS Rank（R1，加在 `StockAnalysis`）
| 欄位 | 型別 | 說明 |
|------|------|------|
| `RSScore` | float64 | 加權多季報酬 |
| `RSRank` | int | 1–99；0 = N/A |
| `RSValid` | bool | 是否參與排名 |
| `RSBasis` | string | `live` / `cache:YYYY-MM-DD` / `na` |

### 2.3 🆕 52 週高 / 多週期新高（R2，加在 `StockAnalysis`）
| 欄位 | 型別 |
|------|------|
| `H20 / H60 / H120 / H250` | bool |
| `H20Valid … H250Valid` | bool |
| `High52w` | float64 |
| `PctFrom52wHigh` | float64（≤0） |
| `NewHighScore` | float64（0–100） |

### 2.4 🆕 VCP（R2，加在 `Consolidation`）
| 欄位 | 型別 |
|------|------|
| `VCPScore` | float64（0–100） |
| `IsVCP` | bool |
| `Contractions` | []float64（深度%，oldest-first） |
| `ContractionCount` | int |
| `FinalContractionPct` | float64 |

### 2.5 🆕 MomentumFlow（R3，新型別）
`MomentumFlow` 列舉：`MOMENTUM_BUILDING / _CONTINUATION / _FADING / STRUCTURAL_SHIFT_UP / _DOWN / MOMENTUM_NEUTRAL`。

`MomentumState`：
| 欄位 | 型別 |
|------|------|
| `Flow` | MomentumFlow |
| `Score` | float64（0–100） |
| `SlopeAccel` | float64 |
| `Divergence` | bool |
| `StructureTrend` | string（HH_HL/LH_LL/HIGHER_LOWS/LOWER_HIGHS/MIXED/NEUTRAL） |
| `WeeklyConfirm` | bool（R4 前恆 false） |
| `Note` | string |

### 2.6 🔧 內部傳遞欄位
- `rocketInput += rsRank int, rsValid bool, newHighScore float64, momentum MomentumState`。
- `rocketOutput += Momentum MomentumState`；`WatchlistEntry += Momentum MomentumState`。

### 2.7 ✅ 既有（對照，不新增）
- **sector/rotation**：`SectorRotation`（Score/OppScore/Stage/五組件/三層欄位/Stocks…）。
- **short_term_flow**：`ShortTermFlowScore/Dir/Stage`、`Avg1/3/5dGain`、`UpRatio` 等。
- **backtest**：`Backtest{PatternName, Stock/SectorSampleCount, Stock/SectorWinRate, AvgReturn, AvgDrawdown, RiskReward, Confidence}`。
- **rocket_candidate**：`WatchlistEntry`、`RocketScore/Stage`、`WatchAction`、`ExplosionProb`、價位欄位、`RiskLabel/Warning`。

---

## 3. 共用 helper / scoring function 清單

| Function（草圖簽名） | 檔案 | 標記 | 用途 |
|----------------------|------|------|------|
| `isCommonStock(code) bool` | relstrength.go | 🆕 | RS universe filter（排除 ETF/特別股/DR；全額交割靠 exclude_codes） |
| `RSScore(adjCloses []float64, cfg) (float64, bool)` | relstrength.go | 🆕 | 加權多季報酬 + valid |
| `rankPercentile(scores []float64) map[…]int` | relstrength.go | 🆕 | mid-rank 百分位 → 1–99 |
| `SaveRSSnapshot / LoadRSSnapshot` | relstrength.go | 🆕 | fast mode 母體快照（含過期/設定一致檢查） |
| `newHighFlags(adjCloses, n) (H20..H250, valid…)` | newhigh.go | 🆕 | 多週期新高 |
| `distFrom52wHigh(adjCloses) float64` | newhigh.go | 🆕 | 距 52 週高 % |
| `NewHighScore(flags, pctFrom52w, volRatio, rsi, extMA20) float64` | newhigh.go | 🆕 | 0–100 |
| `detectSwings(candles, zigzagPct) []Swing` | swing.go | 🆕 | zigzag 高低（R2/R3 共用） |
| `detectVCP(candles, ind, cfg) (Contractions, …)` | consolidation.go | 🆕 | 收縮腿偵測 |
| `VCPScore(contractions, vols, cfg) (float64, bool)` | consolidation.go | 🆕 | 收斂品質分 |
| `computeMomentum(candles, ind, consol, cfg) MomentumState` | momentum.go | 🆕 | 動能分類 |
| `jointWatchAction(stage, flow) WatchAction` | rocket.go | 🆕 | §5.1 矩陣 |
| `explosionProb(stage, score)` | rocket.go | 🔧 | 加類別 guardrail（SHIFT_DOWN→LOW 等），不雙重計分 |
| `score group g2 / g3` | rocket.go | 🔧 | g2 用 RSRank、g3 用 NewHighScore（取代 NearPreviousHigh） |
| `applyMomentumModifier(score, flow, cfg)` | rocket.go | 🆕 | ±12 修正、clamp 0–100 |
| `analyzeConsolidation(...)` | consolidation.go | 🔧 | 末段加 VCP bonus（上限 12）|
| `stageWeight / classifyStage / ScanRotation` | rotation.go | ✅ | sector opportunity score（既有，不改） |
| `backtestConfidence(stockN, sectorN)` | backtest.go | ✅ | 既有 |
| `stagePriority / actionPriority`（template func） | report.go | 🔧 | 既有 priority map；新增動能列徽章排序鍵（見 §5） |
| `riskLabel/Warning 合流` | rocket.go | 🔧 | FADING/SHIFT_DOWN 風險併入、取最嚴重 |

---

## 4. Config / YAML 清單

| 設定 | 標記 | 動作 |
|------|------|------|
| `configs/config.yaml → scanner.rs` | 🆕 | `enabled, lookbacks, weights, min_history, min_rank_gate:80, use_adjusted_close, exclude_non_common:true, exclude_codes, snapshot_path, snapshot_max_age_days` |
| `configs/config.yaml → scanner.newhigh` | 🆕 | `enabled, lookbacks:[20,60,120,250], vol_confirm_ratio, leader_within_pct:25, leader_far_pct:50, overext_ma20_pct, overext_rsi` |
| `configs/config.yaml → scanner.vcp` | 🆕 | `enabled, zigzag_pct, min_contractions:2, final_tight_pct, loose_pct, min_score, quality_bonus_max:12` |
| `configs/config.yaml → scanner.momentum` | 🆕 | `enabled, accel_window_short/long, accel_pos/neg_thresh, accel_scale, key_ma, reclaim_lookback, zigzag_pct, score_modifier{…}, modifier_cap:12` |
| `scanner.Config`（scanner.go） | 🔧 | 內嵌上述四個子 struct（yaml tag 對齊） |
| `fetcher.history_range` | ✅ | 維持 `2y`；RS 需 ≥253 根，2y 足夠（不改，僅驗證） |
| `configs/sectors.yaml` | ✅ | **不改**（R1–R3 與族群清單無關） |
| `strategies.yaml` | ⏸️ | **R1–R3 不需要**；策略定義屬 R5 backtest，延後 |
| watchlist display config | 🆕(可選) | 若要可調顯示欄位/門檻，可加 `report.watchlist{ show_rs, show_vcp, show_momentum }`；否則沿用程式預設 |

> 驗收：四個 `enabled` 全設 false → 解析正常且行為等同今日（黃金回歸）。

---

## 5. UI 變更 checklist（`internal/report/report.go`）

| 項目 | 標記 | 動作 / 驗收 |
|------|------|------------|
| Rotation 分頁放最後 | ✅ | 已是分頁之一；**驗收**：確認 tab 順序（市場/持倉/飆股候選/輪動）輪動在末 |
| Watchlist = 飆股候選追蹤 | ✅ | 已實作；不重做 |
| 首頁精簡列表 + 點擊展開決策卡 | ✅ | 兩層已實作；新欄位塞入既有列表/卡片 |
| 階段/操作建議用 priority map（非字母排序） | ✅🔧 | `stagePriority`/`actionPriority` **已存在**；**驗收**：列表預設排序鍵用之；R3 改 WatchAction 後**更新 actionPriority 涵蓋所有值**（TAKE_PROFIT/REMOVE 等） |
| RS Rank 標籤 | 🆕 | 列表加 `RS 95` 欄；配色 ≥90 綠/80–89 藍/<70 灰；fast mode 附「(基準日…)」 |
| 距 52 週高 / 新高旗標 | 🆕 | 卡片顯示 `-7%` + 20/60/120/250 點亮；NewHighScore 條 |
| VCP 標籤 | 🆕 | 卡片 `VCP ✓ 3段 10→6→4%`；失敗顯示紅標「VCP 失敗」 |
| 動能列 + 二維徽章 | 🆕 | 卡片加「動能：CONTINUATION」列；列表加 `階段×動能` 徽章（如 `MAIN_RUN×FADING`）；配色見 R3 §12 |
| 飆股分/族群/階段/操作/風險排序 | ✅🔧 | 既有排序維持；**新增** RS gate 為第一排序鍵（達標在前），RocketScore 次之，RSRank tie-break |
| 風險置頂 | 🔧 | FADING/SHIFT_DOWN 時 RiskWarning 以紅字置頂 |

**UI 驗收條件**
- 列表排序：先「RSRank≥80」分層、再 RocketScore、再 RSRank；階段/操作以 priority map 排，非字母。
- 新欄位在 `enabled=false` 時隱藏或顯示 N/A，不報錯。
- 二維徽章對 NEUTRAL 不顯示矛盾標籤（只顯示階段）。

---

## 6. 測試清單

| 類型 | 檔案 | 案例 | 驗收條件 |
|------|------|------|----------|
| **unit** | relstrength_test | RSScore 數值、253 臨界、非普通股過濾 | 數值=手算；252→invalid、253→valid |
| **scoring** | rocket_test | g2(RSRank 95/75/60/N/A)、g3(NewHighScore)、動能修正 bounded、無雙重計分 | 各組 ≤ 上限；總分 clamp 0–100 |
| **scoring** | consolidation_test | VCP 15→10→6→4 高分、擴張低分、BaseQuality 加成封頂 | VCPScore 區間正確、bonus≤12 |
| **yaml parsing** | scanner_test/config | 四新區段解析、缺省值、`enabled:false` | 解析無誤；缺值用預設 |
| **edge case** | newhigh_test | 歷史<251→H250 invalid、距高>50% 封頂35、H250 過熱×0.6 | 旗標/封頂正確 |
| **edge case** | momentum_test | 衝突 priority、SHIFT 假轉折防呆、**漲停鎖量不判 FADING** | priority 正確；guard 生效 |
| **UI sorting** | report_test（或 template func 單元） | stagePriority/actionPriority 覆蓋所有列舉、列表排序鍵 | 非字母序；新值有 priority 不 panic |
| **insufficient data** | scanner_test | <30 根 skip、<253 RS invalid、<12 無 base | 不 panic、降級正確 |
| **cache / history range** | relstrength_test | snapshot round-trip、過期、設定不符失效 | 過期→N/A 退化；round-trip 一致 |
| **黃金回歸** | 全體 | 四 `enabled=false` | 輸出 == 今日基準 |

---

## 7. 實作順序（Phase 1–7 ↔ commit）

| Phase | 內容 | commit | 驗收 |
|-------|------|--------|------|
| **P1 data model / config** | `Candle.AdjClose` + yahoo adjclose；四 config 子 struct + yaml | C1 | build 綠；adjclose 缺值退 raw；config 解析 |
| **P2 helper / scoring** | `swing.go`、`relstrength.go`、`newhigh.go`、VCP(`consolidation.go`)、`momentum.go`（純函式，先不接線） | C2,C4部份,C5,C6 | 各 helper 單元測試綠 |
| **P3 sector rotation** | ✅ 既有，無新工作（僅確認 RS/動能不影響族群計分） | — | 族群輸出不變 |
| **P4 backtest** | ✅ 既有，R1–R3 不動（R5 才升級）；確認 `members` 仍正確傳入 | — | backtest 卡片不變 |
| **P5 rocket candidate** | g2(RS)/g3(NewHigh) 改寫、preBreak/prob VCP-aware、R3 聯合決策 + Score 修正；`rocketInput` 接線 | C3,C7 | 計分/決策正確；無雙重計分 |
| **P6 Watchlist + main 接線 + UI** | `ComputeRS`+快照（main.go）、`EnrichWatchlist` 接 RS/動能 + 排序軟門檻、report 標籤/徽章/排序鍵 | C2,C3,C8 | full/fast mode RS 正確；UI 排序與標籤 |
| **P7 tests / calibration** | 補齊 §6 測試 + 黃金回歸；標記待校準項（§8）交 R5 | C9 | 全測試綠；回歸通過 |

> 與 §1.1 的嚴格順序對齊：`R1.0(P1) → R1(P2/P6) → R2(P2/P5) → R3(P2/P5)`。
> 每個 commit 維持「對應 flag=false → 行為不變」，可隨時暫停不破壞主線。

---

## 8. 風險與待校準項目（集中列出）

> 以下數值皆為**起點預設**，須由 R5 第一梯次回測（Win Rate/Avg Return/Profit Factor/Max DD，含成本）校準後固定。

| 項目 | 目前預設 | 校準方式 | 風險 |
|------|----------|----------|------|
| **RS Rank threshold（gate）** | 80 | R5 比較 70/75/80 對命中率 | 太嚴漏中小型飆股、太寬稀釋 |
| **52w high score bucket** | ≤10%/≤25%/≤50% 分級；leader_far 50% | R5 看各 bucket 前向報酬 | bucket 邊界影響領導力判定 |
| **VCP quality threshold** | min_score 50、min_contractions 2、final_tight 6%、loose 15% | R5 看 IsVCP 群組勝率 | 門檻鬆→假 VCP；緊→樣本少 |
| **VCP bonus 上限** | +12（進 BaseQuality） | 檢查與 PriceCompression 是否重疊灌分 | 重複計分 |
| **rocket_candidate_score threshold** | 既有分帶（60/75/90…） | R5 校準各帶實際發動率 | 分帶與新因子加入後失準 |
| **stage adjustment weight（動能修正）** | building+5/cont+6/shiftup+8/fading−6/shiftdown−12；cap 12 | R5 驗證各 Flow 真實勝率差 | 純拍腦袋起點值 |
| **momentum accel 門檻/scale** | `accel_pos/neg_thresh`、`accel_scale` | 改百分比步調或 ATR 正規化後校準 | 高度依賴價格尺度，最易失準 |
| **backtest confidence sample count** | sector≥30/stock≥15 HIGH… | ✅ 既有，R5 一併檢視 | 與新 forward window 互動 |
| **short_term_flow score threshold** | ✅ 既有（rotation 內常數） | R5 一併檢視 | 既有，非 R1–R3 引入 |
| **zigzag_pct（R2/R3 共用）** | 3.0% 固定 | 改自適應（base range 比例）後校準 | 高低價股不公平、漏末段收縮 |
| **adjclose 缺值比例** | null 退 raw | 監測 null 率，必要時加資料品質旗標 | RS/新高失真 |
| **snapshot_max_age_days** | 7（calendar） | 連假後改交易日計 | fast mode 用到過舊母體 |

---

## 驗收總綱（Definition of Done, R1–R3）

1. `go build ./... && go vet ./... && go test ./...` 全綠。
2. 四個 `enabled` 全 false → 輸出與今日**逐位元相同**（黃金回歸）。
3. full mode：龍頭股 RSRank 偏高、快照生成；fast mode：用快照 RSRank 並標基準日。
4. RocketScore 無雙重計分（VCP/族群flow/動能 各單一管道；計算順序正確）。
5. WatchAction 由 `RocketStage × MomentumFlow` 矩陣決定；ExplosionProb 僅類別 guardrail。
6. UI 列表以 priority map 排序（非字母），新欄位/徽章正確、`enabled=false` 時不報錯。
7. §8 待校準項目全部標記並移交 R5，未當成定論。

---

*（本文件為 R1–R3 可施工整合清單，design only，不修改任何程式碼。）*
