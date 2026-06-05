# R1 Implementation Spec — Relative Strength Ranking

> **Design only — no code changes.** 本文件把 `docs/SCANNER_ENHANCEMENT_PLAN.md`（Rev 2）的
> **R1：全市場 Relative Strength Ranking** 展開為可實作的規格。
> 上游：`docs/SCANNER_MASTER_DESIGN.md`。定位不變（EOD 波段、1~4 週、持有 5~20 日）。
>
> 程式碼片段一律為**型別 / 公式草圖**，標示落點檔案，非要立即提交的程式。

---

## 1. Overview & Scope

新增「個股相對於**全市場**的強弱排名」，輸出兩個欄位：

- **`RSScore`**：原始相對強度（近期加權的多區間報酬）。
- **`RSRank`**：在全市場母體中的百分位（1–99）。`RSRank = 95` ＝ 贏過市場 95% 的股票。

用途（本 spec 範圍）：
1. 改寫 RocketScore 第 2 組「個股相對強勢」，以 RSRank 為主。
2. 作為 Watchlist 飆股候選的**排序軟門檻 + tie-break**。
3. 在 `--no-market`（fast mode）下以**快取母體**提供可用的 RSRank，否則優雅退化。

**不在範圍**：回測中的 RS（survivorship-bias 校正屬 backtest 模組）、族群層級 RS（已存在）、
RS 的歷史時間序列繪圖。

---

## 2. Dependencies & Preconditions

| # | 事項 | 現況 | 本 spec 要求 |
|---|------|------|-------------|
| D1 | **全市場母體** | `fetcher.FetchAll()` 已含 TWSE(TW) + TPEX(TWO) | 直接沿用，母體完整，無需新抓取 |
| D2 | **歷史長度** | `history_range` 預設 2y（≈ 480+ 交易日） | RS 需 ≥ 253 根日線（取 close[n-1-252]）；2y 足夠 |
| D3 | **還原股價（關鍵）** | `Candle.Close` 來自 Yahoo `indicators.quote.close`＝**未還原**收盤；目前**未解析 `adjclose`** | 252 日報酬會被除權息 / 分割扭曲。**R1.0 子任務**：在 `yahoo.go` 加解析 `indicators.adjclose`，RS 的區間報酬優先用還原價，缺值再退回 raw close。OHLC 維持 raw（漲停 / 指標需真實價位） |
| D4 | **候選資料** | Watchlist 與 market 都用同一 `history_range` 抓取 | fast mode 下 watchlist 仍有 2y close 可算 RSScore |

> D3 是 R1 唯一需要動到 fetcher 的地方，且僅新增欄位解析，不改既有 OHLC 行為。

---

## 3. RSScore Definition

落點：新檔 `internal/scanner/relstrength.go`。

### 公式（與 Rev 2 計畫一致）

設還原收盤序列 `ac[]`（oldest-first，長度 n）。各區間報酬：

```
retK = (ac[n-1] / ac[n-1-K] - 1) * 100        # K ∈ {63, 126, 189, 252}
RSScore = 0.40*ret63 + 0.20*ret126 + 0.20*ret189 + 0.20*ret252
```

- 63 / 126 / 189 / 252 ≈ 季 / 半年 / 三季 / 年；**近季權重最高**，符合 IBD 精神（非照抄其公式）。
- 權重與 lookbacks 走 config（見 §9），預設如上、和為 1.0。

### 有效性（`RSValid`）

`RSValid = true` 僅當：
- `n ≥ max(lookbacks)+1`（預設 253），且
- 所有用到的 `ac[n-1-K] > 0`（防 0 / 負 / NaN）。

否則 `RSValid = false`、`RSScore = 0`、不參與母體排名、`RSRank = 0`（語意 = N/A）。

---

## 4. RSRank Definition

### 母體（Universe）

- **全市場掃描母體** = `ScanMarket` 抓到、通過基本過濾（`min_price`、`min_avg_volume`）、
  **且為普通股**、且 `RSValid` 的股票集合 `U`，`N = |U|`。
- 上市 + 上櫃皆納入（D1）。
- 過濾掉的低價 / 低量股**不進母體**（避免雞蛋水餃股稀釋分母），但若它們同時在 watchlist，
  仍可對母體分布取百分位（見 §6.3）。
- **排除非普通股（已定案）**：ETF、特別股、全額交割股、DR 不進母體（避免污染百分位）。
  建議以代號規則 + 旗標近似判斷：
  - ETF：代號以 `00` 開頭（如 0050、00878）→ 排除。
  - 特別股：代號帶英文字母（如 `2891B`、`2887A`）→ 排除。
  - TDR / DR：代號落在 DR 區段（如 `91xx`、`9181` 類）→ 排除。
  - 全額交割股：無法由代號判斷，建議維護一份小型排除清單（config `rs.exclude_codes`），
    或暫以「成交量極低 + 特定旗標」近似，列為後續精修。
  - watchlist 內的非普通股仍可顯示，但 `RSRank` 標 `RSBasis="na"`（不對普通股母體取名次）。

### 百分位演算法（含 tie 處理）

對母體中股票 i：

```
below = #{ j ∈ U : RSScore_j <  RSScore_i }
equal = #{ j ∈ U : RSScore_j == RSScore_i }      # 含自己
p_i   = (below + 0.5*equal) / N                   # mid-rank，∈ (0,1)
RSRank_i = clamp( round( p_i * 99 ), 1, 99 )
```

- 最高分 → 約 99；中位 → ~50；最低 → clamp 後為 1。
- mid-rank 讓並列分數得到相同 RSRank，避免抖動。
- 實作上先把 `RSScore` 排序一次，用前綴計數 O(N log N)；不需 O(N²)。

---

## 5. New Data Structures

### 5.1 個股欄位（擴充既有 struct）

`internal/scanner/types.go: StockAnalysis` 新增：

| 欄位 | 型別 | 說明 |
|------|------|------|
| `RSScore` | float64 | 原始相對強度 |
| `RSRank` | int | 1–99；0 = N/A |
| `RSValid` | bool | 是否參與排名 |
| `RSBasis` | string | `"live"`（當日母體）/ `"cache:YYYY-MM-DD"`（快取母體）/ `"na"` |

`internal/scanner/watchlist.go: WatchlistEntry` 透過 `A StockAnalysis` 已自動帶上述欄位，
無需重複欄位。

### 5.2 RS 快照（fast mode 用）

落點：`.cache/rs_snapshot.json`（沿用 `fetcher` cache_dir）。

```json
{
  "date": "2026-06-04",
  "generated_at": "2026-06-04T15:32:10+08:00",
  "universe_size": 1862,
  "lookbacks": [63,126,189,252],
  "weights": [0.4,0.2,0.2,0.2],
  "scores": { "2330": 152.4, "2317": 73.1, "...": 0 }
}
```

- `scores` = symbol → RSScore（**整個母體**，供 fast mode 重建分布取百分位）。
- 記錄 `lookbacks/weights` 以便載入時驗證設定一致（不一致則視為失效）。

---

## 6. Pipeline Integration

### 6.1 Full market mode（未 `--no-market`）

落點：`cmd/scanner/main.go` 第 2 階段之後、3.5（EnrichWatchlist）之前。

```
ScanMarket(marketStocks)                     # 既有：算 StockAnalysis、過濾、排序、截 TopN
   ↓ (需要完整母體 → 在截斷前或用未截斷的 marketStocks 計算)
ComputeRS(marketStocks, cfg.RS)              # 新：算每檔 RSScore → 建分布 → 算 RSRank
   → 回填 marketResults 的 RS 欄位 (RSBasis="live")
   → 產出 rsTable: map[symbol]{RSScore,RSRank}
   → SaveSnapshot(.cache/rs_snapshot.json)
   ↓
EnrichWatchlist(..., rsTable, rsDistribution)  # 傳入供 watchlist 取用
```

> 注意：`ScanMarket` 目前回傳已截斷 TopN 的結果。RS 計算需要**完整母體分布**，
> 因此 `ComputeRS` 應吃**未截斷的 `marketStocks`**（或 ScanMarket 改回傳完整 + 截斷兩份）。
> 建議新增 `ComputeRS` 獨立吃 `[]fetcher.StockData`，不動 `ScanMarket` 簽名（相容）。

### 6.2 Fast mode（`--no-market`）

```
LoadSnapshot(.cache/rs_snapshot.json)
  ├─ 不存在 / 設定不符 / 過期(> snapshot_max_age_days) → RS 全部 N/A，RSBasis="na"
  └─ 有效 → 建 sorted distribution；
            對每檔 watchlist 算 RSScore → 二分搜尋取百分位 → RSRank
            RSBasis = "cache:" + snapshot.date
```

- 過期門檻 `snapshot_max_age_days`（calendar，預設 7）。
- console 與卡片標示基準日，避免把舊母體當成今日。

### 6.3 Watchlist 取得 RSRank（兩種來源）

- watchlist 股**在當日母體 `U` 內** → 直接用 `rsTable` 的 RSRank（`live`）。
- watchlist 股**不在 U**（被 price/volume 濾掉，或 fast mode）→ 自行算 RSScore，
  對「當日分布」或「快照分布」取百分位（§4 公式，N 用該分布大小）。

---

## 7. RocketScore Group-2 Rewrite

落點：`internal/scanner/rocket.go: computeRocket()` 的 `g2`（上限 20）。
新增輸入：`rsRank int`、`rsValid bool`（由 `EnrichWatchlist` 傳入 `rocketInput`）。

### 新配分（三子項，和 ≤ 20）

| 子項 | 配分 | 規則 |
|------|------|------|
| **相對強勢（RS）** | 0–10 | RSRank ≥ 90 → 10；80–89 → 7；70–79 → 4；< 70 → 1。**RS 無效 / N/A → 退化**：`ret20 > sectorAvg ? 7 : 3`，無族群 → 5（再 clamp ≤ 10） |
| 動能（RSI） | 0–6 | 40–68 → 6；68–75 → 3；其餘 → 2（沿用現有） |
| 支撐（Base） | 0–4 | `SupportHoldScore ≥ 60` → 4（由現行 6 重新縮放，使總和恰為 20） |

`g2 = clampFloat(rs + rsi + support, 0, 20)`。

> 設計重點：RS 成為第 2 組主成分，但**保留退化路徑**，確保 fast mode / 新上市股仍有合理分數，
> 不會因 RS 缺失而整組歸零。

---

## 8. Sorting & Soft Gate

### 8.1 Watchlist 排序（`EnrichWatchlist` 末段）

由「單純 RocketScore desc」改為**分層比較**：

```
gate = (RSRank >= rs.min_rank_gate)        # 預設 80；RS N/A 視為未達 gate=false
排序鍵（依序）：
  1) gate           desc   # 達標者整批在前（未達不剔除，只排後）
  2) RocketScore    desc
  3) RSRank         desc   # tie-break
```

- 達 gate 與否只影響**呈現順序**，不改 RocketScore 數值（避免雙重懲罰）。
- `rs.min_rank_gate` 走 config，可關閉（設 0 → gate 永遠成立 = 退回純 RocketScore 排序）。

### 8.2 Market scan 排序

- 維持 `Score desc`（相容）；可選把 `RSRank desc` 當 tie-break。**預設不改**，降低風險。

---

## 9. Config Additions

落點：`configs/config.yaml` 的 `scanner` 區段（對應 `scanner.Config`）。

```yaml
scanner:
  rs:
    enabled: true                 # 總開關；false → 完全沿用今日行為
    lookbacks: [63, 126, 189, 252]
    weights:   [0.4, 0.2, 0.2, 0.2]   # 與 lookbacks 對齊，和應為 1.0
    min_history: 253              # 計 RSScore 所需最少日線
    min_rank_gate: 80             # watchlist 排序軟門檻（已定案 80）；0 = 關閉
    use_adjusted_close: true      # RS 報酬優先用還原價（依賴 D3）
    exclude_non_common: true      # 排除 ETF/特別股/DR/全額交割（已定案）
    exclude_codes: []             # 全額交割等無法由代號判斷者的手動排除清單
    snapshot_path: "rs_snapshot.json"   # 相對 cache_dir
    snapshot_max_age_days: 7      # 快照過期門檻（calendar days）
```

`enabled: false` 時：不算 RS、RocketScore 第 2 組走原邏輯、排序回到純 RocketScore——
**完全向後相容**。

---

## 10. Edge Cases

| 情境 | 處理 |
|------|------|
| 歷史 < 253 根（新上市 / 資料缺） | `RSValid=false`、不入母體、`RSRank=0`、RocketScore 走退化路徑 |
| 過去某 lookback 收盤 ≤ 0 / NaN | 該股 `RSValid=false` |
| 未還原價導致跳空（除權息 / 分割） | 啟用 `use_adjusted_close`（D3）；無 adjclose 才退 raw，並可標資料品質旗標 |
| 母體過小（N < 某門檻，如 < 30） | RSRank 失真：標 `RSBasis="na"`，走退化（fast mode 無快照時常見） |
| RSScore 並列 | mid-rank 百分位（§4），並列同 RSRank |
| watchlist 股被 price/volume 濾出母體 | 仍對母體分布取百分位（§6.3），不因不在母體而無 RSRank |
| 快照存在但 lookbacks/weights 與當前 config 不符 | 視為失效 → N/A 退化（避免基準不一致） |
| 假日 / 休市日執行（市場掃描空） | 母體空 → 不覆寫快照；fast 模式照常用既有快照（標基準日） |

---

## 11. Backward Compatibility

- 全部新欄位預設零值；`rs.enabled=false` → 行為與今日**逐位元相容**。
- 不改 `ScanMarket` / `Composite Score` / `Action` / HTML 既有欄位語意，採**新增**。
- 快照檔不存在不影響 full mode（會即時計算並建立）。

---

## 12. Report / UI

- Watchlist 飆股卡片：新增 **`RS 95`** 標籤（顏色分級：≥90 綠、80–89 藍、<70 灰），
  並在 fast mode 附「(基準日 YYYY-MM-DD)」。
- Market scan 表：可選新增 `RS` 欄（預設顯示）。
- console：full mode 結束印「RS 母體 N 檔、快照已更新」；fast mode 印「使用 RS 快照（基準日 …，剩餘 X 天到期）」。

---

## 13. Test Plan

落點：`internal/scanner/relstrength_test.go`（+ 既有 watchlist/rocket 測試補強）。

**單元**
1. `RSScore` 數值：以合成 close 序列（已知各區間報酬）驗證加權結果。
2. `RSValid`：253 根臨界（252 → false、253 → true）；過去 close=0 → false。
3. 百分位：100 檔線性分布 → 最高 ≈99、中位 ≈50、最低 →1；並列分數同 RSRank。
4. 還原價路徑：有 adjclose 用 adjclose、缺值退 raw。

**整合**
5. Group-2：RSRank 95 / 75 / 60 / N/A 四種 → g2 子項配分與 clamp 正確。
6. 排序：高 RocketScore 但 RSRank 60（未達 gate）排在 中 RocketScore + RSRank 90 之後。
7. 快照 round-trip：save → load → 一致；改 config weights 後 load 視為失效。
8. 過期快照（date 超過 max_age）→ N/A 退化。
9. `rs.enabled=false` → 輸出與基準（今日行為）一致（回歸測試）。

---

## 14. Implementation Sub-tasks (順序)

1. **R1.0** — `yahoo.go` 解析 `indicators.adjclose`，`StockData` 提供還原 close 取用（D3）。
2. **R1.1** — `relstrength.go`：`RSScore`、母體百分位、snapshot save/load。
3. **R1.2** — 接 `main.go` full mode：ComputeRS → 回填 marketResults → 存快照 → 傳入 EnrichWatchlist。
4. **R1.3** — fast mode：load snapshot → watchlist 取百分位 / 退化。
5. **R1.4** — `rocket.go` group-2 改寫 + `rocketInput` 加 rsRank/rsValid。
6. **R1.5** — `EnrichWatchlist` 排序軟門檻 + tie-break。
7. **R1.6** — config 綁定 + report 標籤 + console 訊息。
8. **R1.7** — 測試（§13）。

> 可獨立交付的最小可用版：R1.0–R1.2 + R1.4（full mode 有 RS 並影響評分），
> fast mode（R1.3）與 UI（R1.6）隨後補。

---

## 15. Resolved Decisions

- 母體＝上市 + 上櫃（FetchAll 已涵蓋）。
- **母體排除非普通股**（ETF / 特別股 / DR / 全額交割）— 已定案（§4 過濾規則）。
- RS 公式採 63/126/189/252 加權報酬（0.4/0.2/0.2/0.2），走 config。
- RSRank 採 mid-rank 百分位映射 1–99。
- **`min_rank_gate` 預設 80**（軟門檻、只排序不剔除）— 已定案。
- RS 缺失時 RocketScore **退化**而非歸零。
- Monthly / 風險調整指標等與本 spec 無關（屬 R4 / R5+）。

---

## Open Questions

1. **adjclose 取得穩定度**：Yahoo `adjclose` 偶有 null；缺值退 raw 是否足夠？是否需資料品質旗標進卡片？
2. **全額交割股的判斷方式**：代號無法判斷，先靠手動 `exclude_codes` 清單還是另尋旗標來源？
3. **快照過期門檻 7 天是否合適？** 連假後是否該以「交易日數」而非 calendar days 計？
4. **lookback 是否排除上市未滿 1 年者**，或給「短歷史 RS」較低信心標記？
5. **市場掃描排序要不要納入 RSRank tie-break？** 預設不改以求穩，是否值得開？

> 已定案（移出 Open Questions）：母體排除非普通股；`min_rank_gate=80`。

---

*（本文件為 R1 實作規格，design only，不修改任何程式碼。）*
