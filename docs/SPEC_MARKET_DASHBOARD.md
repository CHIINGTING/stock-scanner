# SPEC — Market Dashboard（市場儀表板 / 結構式 Regime 引擎）

> **本文件是 Market Dashboard 這個功能的唯一 canonical spec。**
> 命名刻意不綁 R 編號（核准決策 6）：這是一個長期存在的**領域**，不是一次性的 release。
> 里程碑編號（M1…M9）只在 §16 的施工進度表出現。
>
> 相關文件的角色：
>
> | 文件 | 角色 |
> | --- | --- |
> | **本文件** | canonical spec。設計要改，先改這裡 |
> | `docs/PLAN_MARKET_DASHBOARD_MVP.md` | 施工計畫與決策紀錄（含 2026-08-09 實際打過的端點驗證原文） |
> | `docs/SPEC_R10_MARKET_DASHBOARD.md` | 更早一版的需求原文（§A）＋ 舊實作對照（§B），歷史保存 |
> | `docs/SPEC_R9_1_MARKET_REGIME_LAYER.md` | R9 設計原稿，**SUPERSEDED**，概念已被本文件吸收，全文保留供推導追溯 |
>
> ⚠️ **版本控管注意**：`.gitignore` 第 5 行忽略整個 `docs/`，既有 docs 是以前被
> force-add 進去的。上表三份文件中，**`SPEC_MARKET_DASHBOARD.md`（本檔）、
> `PLAN_MARKET_DASHBOARD_MVP.md`、`SPEC_R10_MARKET_DASHBOARD.md` 目前都未被 git 追蹤**
> （只有 `SPEC_R9_1_MARKET_REGIME_LAYER.md` 已在版控中）。
> commit 時必須 **三份一起** `git add -f`——只加本檔的話，canonical spec 會指向兩個
> repo 裡不存在的檔案，而那兩份正是決策紀錄與端點驗證原文的唯一出處。
>
> **狀態（2026-08-09）**：**MVP 全部里程碑 M1–M8 已實作、測試綠、逐 commit 於乾淨
> worktree 驗證過**。本文件描述的型別、分類器、決策表、provider、引擎、每日流程都有
> 對應程式碼；唯一仍屬規格而無實作的是 §16 標為 M9 的報告呈現（另議）。
>
> 實作與規格的差異、歷史驗證結果、已知限制，一律記在 §18。

---

## 1. 定位與紅線

### 1.1 這是什麼

Market Dashboard 是 scanner 之上的一層**市場環境判讀**。它回答的問題是：

```
現在的市場結構是什麼？這個判斷有多可信？我為什麼這樣判？
```

它**不**回答「要不要買、買哪一檔、幾塊買」。

### 1.2 紅線（不可協商）

| # | 紅線 | 型別／結構層次上的落實 |
| --- | --- | --- |
| 1 | **SHADOW-ONLY**：不影響既有 scanner 的 score / action / 排序 / 停損 | `internal/market/` 完全不 import `internal/scanner`、`internal/report`、`cmd/scanner`；沒有任何回寫路徑 |
| 2 | **不自動下單、不產生交易指令** | 產物只有 `Snapshot`（分數／狀態／證據），沒有下單介面 |
| 3 | **regime 文案不得含「買 / 賣 / 下單 / 進場價」** | 承接舊 `RegimeInfo` 就寫在型別註解上的紅線；描述環境，不指示交易 |
| 4 | **Market Score 不得決定 Regime** | 兩者由不同函式產生，簽章互不傳參（§6） |
| 5 | **MISSING ≠ NEUTRAL** | `RawSnapshot` 各資料集為 pointer；`Evidence.Available=false` 直接排除於加權之外（§8） |
| 6 | **兩層開關預設 false** | `MarketConfig.Enabled`（算不算）／`Show`（顯不顯示），沿用 repo 的 `enable_*` / `show_*` 慣例 |

紅線 1 目前是**結構性成立**，不是口號：`internal/market` 的 import graph 裡沒有 scanner。

---

## 2. 唯一事實來源：`internal/market/model`

所有型別定義以 `internal/market/model/` 的程式碼為準。本文件的 Go 片段皆自程式碼摘錄，
若兩者不一致，**以程式碼為準並回頭修本文件**。

```
internal/market/model/
    evidence.go    Direction / Strength / Evidence / MissingEvidence / UsableEvidence
    raw.go         RawSnapshot / IndexData / BreadthData / CashData /
                   MarginBalanceData / FuturesOIData / SourceMeta / SourceStatus
    structure.go   Benchmark / PrimaryStructure / BreadthQuality / InstitutionalPosture /
                   StructureView / RegimeDecision / Metric* keys / Rule* ids
    snapshot.go    Snapshot / Flags / Validate / SnapshotSchemaVersion
    regime.go      Regime 五態 + UNKNOWN（另含 Deprecated 的 Confidence / RegimeInfo）
    config.go      MarketConfig / defaultWeights / StructureThresholds / FuturesThresholds
    module.go      Module* 常數 + 舊 flow 的資料型別（FuturesData / MarginData …）
    dashboard.go   舊 Dashboard / SectorRank / Opportunity / Risk（保留，未接進新流程）
    score.go       舊 MarketScore / ModuleScore
```

`model` 對其他 internal package **零相依**，因此 provider / analyzer / service / cmd 都能
匯入而不產生循環。

### 2.1 兩個與計畫書不同的型別名稱

計畫書 §5 寫的是 `MarginData` 與 `FuturesData`，但這兩個名稱在 `module.go` 已被舊流程佔用
（且舊型別帶著 `PriceChangePct`、`DailyChange` 這類**已推導**的欄位，正是新設計要排除在
raw 之外的東西）。落地時改名，**spec 一律採用程式碼裡的實際名稱**：

| 計畫書寫的 | **實際型別名稱** | 原因 |
| --- | --- | --- |
| `MarginData` | **`MarginBalanceData`** | 避開 `module.go` 既有的 `MarginData`；名稱也更精確描述內容（餘額，不是判斷） |
| `FuturesData` | **`FuturesOIData`** | 避開 `module.go` 既有的 `FuturesData`；明示它是未平倉（OI），不含 DailyChange |

兩組型別並存到 M6 汰除舊流程為止。

---

## 3. 三層架構

```
第一層  PrimaryStructure       ← 只看 benchmark 價格結構（MVP = 0050 proxy）
        STRONG_UPTREND / UPTREND / RANGE / WEAKENING / DOWNTREND / UNKNOWN

第二層  BreadthQuality         ← 只看市場廣度品質
        HEALTHY / COOLING / NARROWING / COLLAPSING / UNKNOWN

        InstitutionalPosture   ← 只看法人證據（futures + cash + margin）
        SUPPORTIVE / NEUTRAL / DETERIORATING / UNKNOWN

第三層  Regime                 ← 由上述三者經「明示決策表」推導（§5）
        BULL / BULL_PULLBACK / SIDEWAYS / DISTRIBUTION / BEAR / UNKNOWN
```

三層都是**離散列舉**，每層可獨立寫 table-driven 測試，且三層的結果**全部存進 snapshot**。
這樣設計的唯一目的是：日後判錯時能回答「是哪一層判錯」，而不是重讀今天的程式碼去猜。

### 3.1 Benchmark 必須外顯

```go
type Benchmark string

const (
    Benchmark0050  Benchmark = "0050"  // MVP 預設：ETF proxy，.cache 已有 487 根
    BenchmarkTAIEX Benchmark = "TAIEX" // 未來：真正的加權指數
)
```

MVP 用 0050，因為 `.cache/0050_TW.json` 已有兩年日 K，可立刻全段回測。
但它是 **ETF proxy，不是指數**：歷史紀錄裡若把它標成 TAIEX，會污染日後所有比較。
所以 `StructureView.Benchmark` 是必存欄位，輸出欄位一律命名 `benchmark_*` 而非 `taiex_*`。

### 3.2 第一層：PrimaryStructure（只用 benchmark 價格）

輸入全部來自 `.cache` 的 benchmark 日 K，as-of（只用當日含以前的 bar，不看未來）。
`ddHigh60` = 距 60 日最高收盤的回檔幅度（>= 0）。

| # | PrimaryStructure | 條件（第一個命中者勝） |
| --- | --- | --- |
| 1 | `UNKNOWN` | 歷史 bar 數 < `MinBenchmarkBars`（預設 **120**，理由見 §12） |
| 2 | `DOWNTREND` | `close < MA60` 且 `close < MA120` 且 `MA60 下彎` 且 `ret20 < 0` |
| 3 | `WEAKENING` | `close < MA20` 且 `close < MA60` 且 `ret20 < 0` 且 `close > MA120`（長線支撐未破） |
| 4 | `STRONG_UPTREND` | `close > MA20 > MA60 > MA120`（完全多頭排列）且 `MA60 上揚` 且 `ret60 > 0` 且 `ddHigh60 ≤ NearHighDDPct`（預設 3%，見下方註） |
| 5 | `UPTREND` | `close > MA60` 且 `MA60 上揚` 且 `ret60 > 0` |
| 6 | `RANGE` | 其餘（MA 糾結、`\|ret60\|` 小、在 MA60 上下震盪） |

`WEAKENING` 是整張表的關鍵中間態：**中期結構已破（跌破 MA20/MA60）、長期支撐仍在
（守住 MA120）**。它是 `BULL_PULLBACK` 最常出現的位置；把它與 `DOWNTREND` 分開，
才不會把一次有秩序的修正讀成空頭市場。

> ⚠️ **`NearHighDDPct` 是共用欄位（落地推斷，非計畫書指定）。**
> 計畫書 §9.2 第 4 列寫的是字面的 `ddHigh60 ≤ 3%`，沒有指名 config 欄位。
> 本文件把它綁到 `NearHighDDPct`，理由是兩處語意相同（皆為「距 60 日收盤高點的近高判定」）
> 且預設值同為 3。**後果**：這一個欄位同時支配三處——第一層的 `STRONG_UPTREND`、
> 第二層 `NARROWING` 的 `nearHigh` 背離測試、以及 R7 的額外條件。
> 調它會**一次改動三個地方**。若日後校準發現三者需要不同的近高門檻，
> 正確做法是新增獨立欄位（例如 `StrongUptrendDDPct`），而不是硬調共用值。
> 對照 §11.2。

`PrimaryStructure.Bullish()` 定義為 `STRONG_UPTREND || UPTREND`，語意由型別自己擁有，
決策表不重複這個判斷。


> **「MA60 上揚」的定義**：`MA60(今日) > MA60(N 個交易日前)`，N = `StructureThresholds.MASlopeDays`
> （預設 **5**，即一個交易週）。一天的均線跳動在 60 日均線上是雜訊，決策表講的是**斜率**。
> 對應地，規格中的「MA60 下彎」實作為「**非上揚**」，因此也涵蓋完全持平的情形——長線支撐已破
> 的市場裡，持平的 60 日均線不算健康證據。此門檻走 config，校準時不需改程式碼。

### 3.3 第二層之一：BreadthQuality（主來源 = 既有 universe）

`b20` = universe 中收盤站上自身 MA20 的比例（`MetricBreadthMA20`）；
`drop20` = `b20` 相對其近 20 個交易日最高值的下滑百分點（`MetricBreadthDrop20`）；
`nearHigh` = `ddHigh60 ≤ NearHighDDPct`。

| # | BreadthQuality | 條件（第一個命中者勝） |
| --- | --- | --- |
| 1 | `UNKNOWN` | 有效家數 < `MinBreadthUniverse`（預設 500） |
| 2 | `COLLAPSING` | `b20 < BreadthCollapsePct`（預設 30%） |
| 3 | `NARROWING` | `nearHigh` **且**（`b20 < BreadthNarrowPct` **或** `drop20 ≥ BreadthDropNarrowPP`）← **兩個分支都要求 `nearHigh`**，見 §16.1 Q1 |
| 4 | `HEALTHY` | `b20 ≥ BreadthHealthyPct`（55%）且 `drop20 < BreadthDropCoolPP`（10pp） |
| 5 | `COOLING` | 其餘 |

`NARROWING` 的第一個條件是「指數還在高點附近、但站上均線的股票不到一半」——
這是 leadership narrowing，而且它是**價格與內部的比較**，不是單一門檻。

`BreadthQuality.Deteriorating()` = `NARROWING || COLLAPSING`。

**主來源是 `.cache` 的全市場 universe，不是任何網路端點**（核准決策 3）。理由：
可完整回補、與 scanner 同源、零新相依。TWSE 官方漲跌家數（`BreadthData`）語意不同
（「今天漲跌」vs「站上均線比例」），只作為**次要 cross-check**，兩者不加總。

### 3.4 第二層之二：InstitutionalPosture

由三個 Evidence（`ForeignFutures` / `ForeignCash` / `Margin`）的 `Direction` 彙整：

| # | InstitutionalPosture | 條件 |
| --- | --- | --- |
| 1 | `UNKNOWN` | 可用 evidence < 2 |
| 2 | `DETERIORATING` | ≥2 個 `BEARISH`　**或**　（外資期貨淨空 5 日持續增加 **且** 外資現貨 5 日淨賣超） |
| 3 | `SUPPORTIVE` | ≥2 個 `BULLISH` 且無 `BEARISH + STRONG/EXTREME` |
| 4 | `NEUTRAL` | 其餘 |

第 2 條括號部分是**獨立成立的觸發條件**：期貨與現貨同步轉空，即使 margin 中性也算惡化——
這是台股最典型的出貨組合。

**只有一個可用 evidence 時是 `UNKNOWN`，不是 `NEUTRAL`**：一個資料點構不成「態勢」，
把單一來源斷線讀成「市場平靜」是最危險的一種誤判。

### 3.5 StructureView：第一、二層的完整輸出

```go
type StructureView struct {
    Benchmark Benchmark            `json:"benchmark"`
    Structure PrimaryStructure     `json:"structure"`
    Breadth   BreadthQuality       `json:"breadth_quality"`
    Posture   InstitutionalPosture `json:"institutional_posture"`

    TrendIntact       bool `json:"trend_intact"`       // close > MA120 && MA60 rising
    PriceResilient    bool `json:"price_resilient"`    // dd_from_high60 <= threshold || close > MA60
    OrderlyCorrection bool `json:"orderly_correction"` // correction inside [min,max] with no capitulation day
    FailedHighs       int  `json:"failed_highs"`       // touched the 60d high then lost MA20 within 3 days

    Metrics map[string]float64 `json:"metrics,omitempty"`
}
```

四個述詞**外顯儲存，不在讀取時重算**：決策表直接分支在它們上面，存下來才能重現當日判斷。

`StructureView.Known()` = 第一層與第二層 breadth 都不是 UNKNOWN 且皆為合法值。
**`Known() == false` 時決策表必須回 `RegimeUnknown`，不得 fallback 到 `SIDEWAYS`。**

Metric key 常數（`structure.go`，已持久化，可新增、**不可改名或改用途**）：

```
close  ma20  ma60  ma120  ma200(僅脈絡)  ret20  ret60  dd_from_high60
breadth_above_ma20  breadth_above_ma60  breadth_above_ma120
breadth_drop20_pp   breadth_valid_count  advancing_ratio
breadth_base60      breadth_base120
realized_vol20      realized_vol20_percentile
```

`breadth_valid_count` 是 **MA20 的母體**，也是充足性閘門的依據；`breadth_base60` /
`breadth_base120` 分別是另外兩個廣度指標各自的母體。

> **母體不足時該 metric 會被「省略」，而不是寫成一個數字。**
>
> `breadth_above_ma60` / `breadth_above_ma120` 只有在**自己的母體**達到
> `MinBreadthUniverse` 時才寫入；未達時該 key **不存在**，但 `breadth_base60` /
> `breadth_base120` 一律寫入，所以省略是可解釋的。
>
> 理由：百分比脫離分母就無法事後稽核——「8 檔裡的 75%」與「1900 檔裡的 75%」在快照裡
> 長得一模一樣。這是 M2 落地時在真實 `.cache` 發現稀疏日汙染母體後加上的保護（見 §16 M2
> 與 §11.2 的 `MinCoverage`）。讀取端本來就必須走 §4 契約條款 4 的 `Metric(key)` ok
> 分支，因此省略對消費者是安全的。

---

## 4. Evidence 契約

Evidence 是 analyzer 的**唯一**輸出。analyzer 永遠不直接碰 Market Score：
它產出標準化證據，引擎（M7）才決定這份證據值多少。這把「資料說了什麼」與
「我們給它幾分」徹底分開。

```go
type Direction string
const (
    DirBullish Direction = "BULLISH"
    DirNeutral Direction = "NEUTRAL"
    DirBearish Direction = "BEARISH"
    DirUnknown Direction = "UNKNOWN"   // 「看不出來」，與 NEUTRAL（「看了，是平衡的」）不同
)

type Strength string
const (
    StrengthWeak     Strength = "WEAK"
    StrengthModerate Strength = "MODERATE"
    StrengthStrong   Strength = "STRONG"
    StrengthExtreme  Strength = "EXTREME"
    StrengthUnknown  Strength = "UNKNOWN"
)

type Evidence struct {
    Source     string             `json:"source"`
    Direction  Direction          `json:"direction"`
    Strength   Strength           `json:"strength"`
    Confidence float64            `json:"confidence"` // 0..1，此 analyzer 自身的確信度
    Summary    string             `json:"summary"`    // 中文一句話佐證
    Metrics    map[string]float64 `json:"metrics,omitempty"`

    Available bool   `json:"available"`
    Reason    string `json:"reason,omitempty"`
}
```

Source 識別字（`evidence.go` 常數，持久化字串，**不可改名**）：
`Price` / `Breadth` / `ForeignFutures` / `ForeignCash` / `Margin`。

契約條款：

1. **缺資料一律走 `MissingEvidence(source, reason)`**，它產生
   `Available=false, Direction=UNKNOWN, Strength=UNKNOWN`。
   禁止用「NEUTRAL + 空 metrics」代表缺資料。
2. **`Usable()` 需同時成立** `Available && Direction != UNKNOWN && Direction.Valid()`。
   引擎只能消費 `UsableEvidence()` 篩出來的子集。
3. **`Metrics` 逐字持久化**。日後要用不同門檻重判同一天，可離線重算，
   不必重新請求 TWSE/TAIFEX（與 `internal/r6backtest` 用 `.cache` 離線回測同一思路）。
   Metric key 可自由新增，**不可改名或改用途**。
4. **`Metric(key)` 必須用 ok 分支**，不可把「沒有這個 metric」當成 0——0 對多數欄位是合法值。
5. `Direction.Sign()` 映到 −1/0/+1，供 agreement 計算；`UNKNOWN` 無 sign，
   呼叫前必須先濾掉。

---

## 5. 第三層：公開 Regime 決策表（明示、有序、第一個命中者勝）

### 5.1 五態 + UNKNOWN

```go
type Regime string
const (
    RegimeBull         Regime = "BULL"          // 健康多頭，參與度廣
    RegimeBullPullback Regime = "BULL_PULLBACK" // 主趨勢未破，有秩序修正
    RegimeSideways     Regime = "SIDEWAYS"      // 無方向性優勢
    RegimeDistribution Regime = "DISTRIBUTION"  // 指數還撐著，內部先壞
    RegimeBear         Regime = "BEAR"          // 趨勢與結構皆空
    RegimeUnknown      Regime = "UNKNOWN"       // 資料不足／缺失
)
```

### 5.2 輔助述詞（全部走 config，**BASELINE, NOT CALIBRATED**）

```
trendIntact       = close > MA120 且 MA60 上揚                    （主趨勢未破）
priceResilient    = ddHigh60 ≤ ResilientDDPct(5%) 或 close > MA60  （價格還撐得住）
orderlyCorrection = CorrectionMinPct(3%) ≤ ddHigh60 ≤ CorrectionMaxPct(15%)
                    且 近 20 日無單日跌幅 > CapitulationDayPct(4%)
failedHighs       = 近 FailedHighLookbackDays(20) 日內，
                    「觸及 60 日高點後 FailedHighWindowDays(3) 日內跌破 MA20」的次數
```

### 5.3 決策表

| # | RuleID 常數 | Structure | Breadth | Institutions | 額外條件 | → Regime |
| --- | --- | --- | --- | --- | --- | --- |
| **R0** | `RuleUnknown` | **`Known() == false`**：第一層 `UNKNOWN` **或** breadth `UNKNOWN` | ↤ 同左 | — | — | `UNKNOWN` |
| **R1** | `RuleBearDowntrend` | `DOWNTREND` | any | **any** | — | **`BEAR`** |
| **R2** | `RuleBearCollapse` | `WEAKENING` | `COLLAPSING` | any | — | `BEAR` |
| **R3** | `RuleDistBreadth` | `STRONG_UPTREND` / `UPTREND` / `RANGE` / `WEAKENING` | `NARROWING` 或 `COLLAPSING` | any | `priceResilient` | **`DISTRIBUTION`** |
| **R4** | `RuleDistCoolingInst` | `STRONG_UPTREND` / `UPTREND` / `RANGE` | `COOLING` | `DETERIORATING` | `priceResilient` | `DISTRIBUTION` |
| **R5** | `RuleDistFailedHighs` | `UPTREND` / `RANGE` | any | `DETERIORATING` | `failedHighs ≥ FailedHighsMin(2)` | `DISTRIBUTION` |
| **R6** | `RuleBullStrong` | `STRONG_UPTREND` | `HEALTHY` / `COOLING` | ≠ `DETERIORATING` | — | **`BULL`** |
| **R7** | `RuleBullUptrend` | `UPTREND` | `HEALTHY` | `SUPPORTIVE` / `NEUTRAL` | `ddHigh60 < NearHighDDPct(3%)` | `BULL` |
| **R8** | `RulePullbackOrderly` | `UPTREND` / `WEAKENING` | `HEALTHY` / `COOLING` | ≠ `DETERIORATING` | `trendIntact` **且** `orderlyCorrection` | **`BULL_PULLBACK`** |
| **R9** | `RuleSidewaysRange` | `RANGE` | `HEALTHY` / `COOLING` | ≠ `DETERIORATING` | — | `SIDEWAYS` |
| **R10** | `RulePullbackWeakening` | `WEAKENING` | any | any | `trendIntact` | `BULL_PULLBACK` |
| **R11** | `RuleSidewaysFallback` | （fallback） | — | — | — | `SIDEWAYS` |

RuleID 常數定義在 `structure.go`，字串即 `"R0"`…`"R11"`。

> **R0 的判準是 `Known()`，不只是「第一層 UNKNOWN」。**
> 計畫書 §9.5 的 R0 列只寫了 Structure 欄的 `UNKNOWN`，Breadth 欄留白，這是該表的模糊處。
> 但 M1 已落地的 `StructureView.Known()`（`internal/market/model/structure.go`）doc comment
> 明寫：*When false the decision table must return RegimeUnknown rather than falling through
> to SIDEWAYS*，而 `Known()` **同時**檢查第一層與 breadth。**程式碼是事實來源**，
> 因此本表以 `Known()` 為準。這是釐清計畫書的模糊處，不是變更已核准的設計。
>
> 注意 `Known()` **不檢查 `InstitutionalPosture`**：法人層 UNKNOWN **不會**觸發 R0
> （理由見 §15.5 最後一列——一次法人斷線不應抹掉純價格結構就能成立的判斷）。

**為什麼一定要存 RuleID**：日後有人質疑「為什麼 2026-08-07 判 DISTRIBUTION」，
答案是「R3」，而不是「去重讀今天版本的程式碼」。`Snapshot.Validate()` 因此規定：
**regime 不是 UNKNOWN 時，RuleID 不得為空**。

### 5.4 決策表要保證的三個不變量（M3 的測試）

**(a) 法人惡化不得把 DOWNTREND 翻成 DISTRIBUTION**

R1 排在所有 DISTRIBUTION 規則（R3/R4/R5）**之前**，且條件中完全沒有 Institutions 欄位。
結構是空頭就是 `BEAR`，法人證據只能進 `Reasons`，改不了結論。
不變量測試：**任何 `DOWNTREND` 且 `Known()` 成立的輸入，不論法人證據怎麼組合，
輸出恆為 `BEAR`。**（`Known()` 這個前提不可省略：廣度為 `UNKNOWN` 時 R0 會先命中並回
`UNKNOWN`——完整推導見 §7.3 不變量 C 與其對應的 ❌ 反例。）

**(b) `BULL_PULLBACK` 與 `DISTRIBUTION` 必須結構性可分** → 見 §7。

**(c) 不得用 Market Score 門檻** → 見 §6。

---

## 6. 為什麼 Score 不參與 Regime 判斷

整張決策表沒有任何一格引用 `Score`。這不是自律，而是**型別層次的禁止**：

- `Score` 與 `Regime` 由**兩個不同的引擎**產生（`score.go` 與 `structure.go`/`regime.go`）。
- 結構分類器的**簽章裡收不到 score**，所以 `if score >= 70 { regime = BULL }` 這行
  在編譯層次就寫不出來。
- `Snapshot` 同時保存兩者，但它們是**並列的兩個結論**，沒有任何一條程式路徑
  讓分數門檻選出 regime。

**為什麼要這樣**：Score 描述的是「方向性強度」，Regime 描述的是「結構狀態」。
兩者可以合法地不一致——分數 67（偏多）而結構是 `BULL_PULLBACK` 或甚至 `DISTRIBUTION`，
這正是最有價值的訊號組合。若讓分數決定 regime，就等於刪掉這個組合，
regime 淪為分數的另一種寫法。

M3 會有一條測試：**固定其餘輸入，`Score` 從 0 掃到 100，`Regime` 必須恆定不變。**

舊的分數門檻分類器的現況（**事實陳述，非計畫**）：

- `model.RegimeThresholds{BullMin, BullPullbackMin, BearMax}` 與 `MarketConfig.Regime`
  **已標 `Deprecated:`**（`model/config.go`）。
- `analyzer.Classify(ms, th, confidenceMinModules)`（`analyzer/regime.go`）
  **尚未加任何 Deprecated 標記**，目前仍是可呼叫的既有流程；隨 M3 決策表一併移除。

（`internal/market` 目前總共只有四處 `Deprecated:`，全部落在 `model/regime.go` 與
`model/config.go`。）

---

## 7. `BULL_PULLBACK` 與 `DISTRIBUTION` 的結構性區別

`BULL_PULLBACK` 有**兩條**產生規則，它們的性質不同，必須分開談，否則會寫出錯誤的測試。

### 7.1 R8（正面規則）vs DISTRIBUTION：條件互斥

下表**只描述 R8**，不適用於 R10：

| | `BULL_PULLBACK`（**僅 R8**） | `DISTRIBUTION`（R3/R4/R5） |
| --- | --- | --- |
| 價格 | **已經實際修正**：`orderlyCorrection`（回檔 3–15%） | **還沒真正修正**：`priceResilient`（回檔 ≤5% 或仍在 MA60 上） |
| 廣度 | `HEALTHY` / `COOLING`（降溫但未壞） | `NARROWING` / `COLLAPSING`（或 `COOLING` + 法人惡化） |
| 法人 | ≠ `DETERIORATING` | `DETERIORATING`，或廣度已糟到不需法人佐證 |
| 主趨勢 | `trendIntact` 是**必要條件** | 不要求（結構可以還很漂亮，這正是危險之處） |
| 一句話 | **價格跌了、內部還健康** | **價格沒跌、內部先壞了** |

R8 與 R3 的**條件交集為空**：R8 要求廣度為 `HEALTHY`/`COOLING`，R3 要求
`NARROWING`/`COLLAPSING`。這一組不需要靠順序就互斥。

### 7.2 R10（殘差規則）：不重疊靠的是**順位**，不是條件

R10 = 「`WEAKENING` 且 `trendIntact`」，**Breadth 與 Institutions 兩欄都是 `any`，
且不要求 `orderlyCorrection`**。它是一條由 `trendIntact` 守門的**殘差規則**：
中期結構已破但長期支撐仍在，先歸為修正而非空頭。

因此下面這條敘述**不成立**，不可以拿去寫測試：

```
❌ BULL_PULLBACK ⇒ breadth ∈ {HEALTHY, COOLING}
❌ BULL_PULLBACK ⇒ institutions ≠ DETERIORATING
❌ BULL_PULLBACK ⇒ orderlyCorrection
```

可達的反例：`WEAKENING` + `NARROWING` + `ddHigh60 > 5%` + `close < MA60` + `trendIntact`。
逐條檢查：R2 不中（廣度非 `COLLAPSING`）、R3 不中（`priceResilient` 為假）、
R4/R5 的 Structure 欄不含 `WEAKENING`、R8 不中（廣度為 `NARROWING`）→ **R10 命中**，
產生一個**廣度為 `NARROWING` 的 `BULL_PULLBACK`**。

這在設計上尚可接受：該情境的價格已實質破線（回檔 >5% 且失守 MA60），
不再是 `DISTRIBUTION` 所定義的「價格沒跌、內部先壞」形狀。
但要講清楚——**這是本決策表順位的推論結果，計畫書未明文論證此點**，
不是一個被核准過、不該碰的取捨。

> **開放問題（M3）**：應以歷史驗證 R10 是否該要求廣度 ≠ `NARROWING`
> （不滿足時就落到 R11 → `SIDEWAYS`），或至少在此情境補一條 Caveat
> 說明「廣度已轉窄，此 pullback 判定僅由長期支撐支撐」。

### 7.3 M3 可以寫、與不可以寫的不變量

**`BULL_PULLBACK` 與 `DISTRIBUTION` 不可能在同一天同時被輸出**——但成立的理由是
**R3/R4/R5 全部排在 R8/R10 之前**（決策表第一個命中者勝），
不是「所有 BULL_PULLBACK 的條件都與 DISTRIBUTION 互斥」。

> ⚠️ **寫這一節的規則**：任何要放進下表左欄的敘述，都必須逐一對照 §5.3 的
> **全部 12 條規則**驗證，確認沒有任何輸入組合會被**更前面的規則**攔截。
> **順位優先於條件**——這是本表最容易犯錯的地方，下面每一條都附了驗證軌跡。

#### ✅ 可以寫的不變量（逐條已對 R0–R11 驗證）

| # | 不變量 | 驗證軌跡 |
| --- | --- | --- |
| A | **每天只會輸出恰好一個 regime** | 決策表有序、第一個命中者勝，且 R11 是無條件 fallback → 必有且僅有一條命中 |
| B | **`Known() == false` ⇒ 恆為 `UNKNOWN`** | R0 是第一列，無法被任何規則搶先 |
| C | **`DOWNTREND` 且 `Known()` ⇒ 恆為 `BEAR`**（不論法人證據怎麼組合） | `Known()` 排除 R0；`DOWNTREND` 使 R1 命中，而 R1 的 Breadth/Institutions 兩欄皆 `any` → 必中，後續規則無機會 |
| D | **`BULL_PULLBACK` ⇒ `trendIntact`** | 只有 R8 與 R10 產生 `BULL_PULLBACK`，兩者都把 `trendIntact` 列為必要條件 |
| E | **廣度 ∈ {`NARROWING`, `COLLAPSING`} 且 `priceResilient` 且〔Structure ∈ {`STRONG_UPTREND`, `UPTREND`, `RANGE`}　**或**　（`WEAKENING` 且廣度 = `NARROWING`）〕⇒ 必為 `DISTRIBUTION`** | 兩個分支的 Structure/Breadth 皆非 UNKNOWN → R0 不中；Structure 不含 `DOWNTREND` → R1 不中；分支一 Structure ≠ `WEAKENING`、分支二廣度 ≠ `COLLAPSING` → **R2 不中**；R3 三欄全部滿足 → 命中 |
| F | **固定其餘輸入，`Score` 由 0 掃到 100，`Regime` 恆定不變** | 決策表沒有任何一格引用 `Score`，且分類器簽章收不到它（§6） |

#### ❌ 不可以寫的敘述（全部有可達反例）

| 錯誤敘述 | 反例 / 原因 |
| --- | --- |
| `BULL_PULLBACK ⇒ 廣度 ∈ {HEALTHY, COOLING}` | R10 的 Breadth 欄是 `any`（§7.2 反例） |
| `BULL_PULLBACK ⇒ 法人 ≠ DETERIORATING` | R10 的 Institutions 欄是 `any` |
| `BULL_PULLBACK ⇒ orderlyCorrection` | 只有 R8 要求，R10 不要求 |
| `DOWNTREND ⇒ BEAR`（**無 `Known()` 限定**） | `DOWNTREND` + 廣度 `UNKNOWN` → `Known()` 為假 → **R0 先命中 → `UNKNOWN`**。§5.4(a) 的「不論**法人證據**」只排除了法人層，並未排除廣度 UNKNOWN，因此 `Known()` 這個前提必須額外寫出來 |
| 廣度 `NARROWING`/`COLLAPSING` 且 `priceResilient` ⇒ `DISTRIBUTION`（**無 Structure 限定**） | 三個反例：① `DOWNTREND` + `NARROWING` + `priceResilient` → **R1 先命中 → `BEAR`**（且 R3 的 Structure 欄根本不含 `DOWNTREND`，R3 永不可能命中）；② `WEAKENING` + `COLLAPSING` + `priceResilient` → **R2 先命中 → `BEAR`**；③ Structure `UNKNOWN` → **R0 → `UNKNOWN`** |

最後兩列是本 spec 自己曾經寫錯的兩條，留在這裡當作**反面教材**：
它們犯的正是本節開頭強調的錯——只檢查了目標規則的條件，沒有檢查更前面的規則會不會先攔截。

#### 這相對舊版的進步

舊版把 `BULL_PULLBACK` 當成純殘差（「不是 BULL 也不是 BEAR 就叫 pullback」），
完全無法與 `DISTRIBUTION` 區分。本版把主要情境收斂成有正面條件的 R8，
只把「中期破線但長期未破」這一小塊留給 R10 兜底，
並用規則順位保證它不會吃掉 DISTRIBUTION 該抓到的日子。

---

## 8. UNKNOWN 是合法狀態，MISSING ≠ NEUTRAL

### 8.1 UNKNOWN 不是第六種市場狀態

`UNKNOWN` 是「資料撐不起一個判斷」時的誠實答案。它存在的唯一目的是：
**讓「歷史不足」永遠不會悄悄變成 `SIDEWAYS`。**

每一層都有自己的 UNKNOWN，且各自獨立降級：

| 層 | UNKNOWN 觸發條件 |
| --- | --- |
| `PrimaryStructure` | benchmark bar 數 < `MinBenchmarkBars` |
| `BreadthQuality` | 有效家數 < `MinBreadthUniverse` |
| `InstitutionalPosture` | 可用 evidence < 2 |
| `Regime` | `StructureView.Known() == false`（第一層或 breadth 為 UNKNOWN） |
| `Direction` / `Strength` | 對應來源不可得 |

`NewSnapshot()` 一律從 UNKNOWN 開始，而不是從 Go 零值開始——`Regime` 的零值是 `""`，
那會序列化成一個沒人定義過的 regime。

### 8.2 MISSING ≠ NEUTRAL 的型別落實

```go
type RawSnapshot struct {
    Date      string    `json:"date"`
    FetchedAt time.Time `json:"fetched_at"`

    Index   *IndexData         `json:"index,omitempty"`
    Breadth *BreadthData       `json:"breadth,omitempty"`
    Cash    *CashData          `json:"cash,omitempty"`
    Margin  *MarginBalanceData `json:"margin,omitempty"`
    Futures *FuturesOIData     `json:"futures,omitempty"`

    Sources []SourceMeta `json:"sources,omitempty"`
}
```

**每個資料集都是 pointer。`nil` 就是缺**，與「struct 存在但值是 0」（市場真的印出 0）
在型別層次上分得開。analyzer 必須分支在 nil 上並回 `MissingEvidence`，
**不得把 nil 強制解讀成中性**。

`Snapshot.Validate()` 把這條寫成硬性檢查：
**`Available=false` 的 evidence，Direction 與 Strength 必須都是 UNKNOWN**，
否則拒絕寫檔。這是阻止「缺資料被記錄成中性讀數」的最後一道閘門。

### 8.3 失效行為總表

| 狀況 | 行為 |
| --- | --- |
| 單一端點逾時 / 500 | 該 evidence `Available=false`，其餘照常；completeness 下降 → Confidence 下降 |
| 假日 / 無交易資料 | provider 回 `ErrNoData`（`SourceNoData`）；整份 snapshot 不產生，**不寫檔** |
| 回應日期 ≠ 請求日期 | `ErrDateMismatch`，視同該來源缺（**絕不接受錯位資料**） |
| 部分欄位缺（如融券） | 該 Metric 缺，evidence 仍可產生但 `Confidence` 打折並記 Caveat |
| `.cache` 歷史不足 | Price/Breadth `Available=false` → 第一層 UNKNOWN → **regime 直接 UNKNOWN** |
| 可用 evidence < 2 | `InstitutionalPosture = UNKNOWN`；Score 仍計算但標記低完整度 |

**絕不做的事**：補零、內插、沿用昨日值頂替今日、把缺資料當中性。

### 8.4 來源狀態

```go
type SourceStatus string
const (
    SourceOK      SourceStatus = "OK"      // 有資料且解析成功
    SourceNoData  SourceStatus = "NO_DATA" // 上游有回應，但無交易資料（假日）
    SourceError   SourceStatus = "ERROR"   // 傳輸 / 解析 / 驗證失敗
    SourceSkipped SourceStatus = "SKIPPED" // 本次未請求
)
```

`SourceMeta` 保留 `URL` 與 `AsOfDate`：一個令人意外的數字，必須能追回到產生它的那次請求。
`AsOfDate` 是**回應宣稱**的交易日，必須等於請求日——TWSE 假日會靜默回最近交易日，
接受它會讓整份快照日期錯位。

---

## 9. 正交旗標 `Flags`（承接 R9，不佔 regime 名額）

```go
type Flags struct {
    HighVolatility bool `json:"high_volatility"` // realized vol20 落在自身分佈的高百分位
    CrashEvent     bool `json:"crash_event"`     // 急跌 + 廣度崩壞
    RecoveryEvent  bool `json:"recovery_event"`  // 崩跌後不久重新站回 MA20
}
```

```
HighVolatility  realizedVol20 ≥ P(VolPercentile=80) of 過去 VolLookbackDays(252) 日分佈
CrashEvent      benchmark ret20 ≤ CrashRet20Pct(−8%) 且 b20 < BreadthCollapsePct(30%)
RecoveryEvent   前 N 日曾 CrashEvent，近 5 日重新站回 MA20
```

**為什麼 CRASH / RECOVERY 是旗標而非 regime**：它們描述的是**事件**（殺盤、剛落底），
不是**結構狀態**。硬塞進五態會讓 `CRASH` 與 `DISTRIBUTION` 互搶同一天的名額。
R9 自己對 `HighVolatility` 已做過同樣判斷（「刻意不把 HIGH_VOL 做成互斥 regime」），
本設計只是把同一邏輯貫徹到 CRASH/RECOVERY。

旗標與 regime 可以共存：「BEAR + crash event」「BULL_PULLBACK + high volatility」
都是合法且有意義的組合。

旗標**個別降級**：算不出百分位就只是 `HighVolatility=false`，不會讓 regime 變 UNKNOWN。

---

## 10. 官方資料來源

> 以下端點皆於 **2026-08-09 實際發送請求驗證**，欄位取自真實回應（詳細原文見
> `docs/PLAN_MARKET_DASHBOARD_MVP.md` §3）。ROC 日期 `1150807` = 民國 115 年 08 月 07 日。

### 10.1 已驗證可用的端點

| 資料 | 端點 | 關鍵欄位 / 位置 | 對應型別 |
| --- | --- | --- | --- |
| **指數** | `GET https://www.twse.com.tw/rwd/zh/afterTrading/FMTQIK?date=YYYYMM01&response=json` | `日期,成交股數,成交金額,成交筆數,發行量加權股價指數,漲跌點數` | `IndexData` |
| **漲跌家數** | `GET https://www.twse.com.tw/rwd/zh/afterTrading/MI_INDEX?date=YYYYMMDD&type=MS&response=json` | `tables[]` 中 `title == "漲跌證券數合計"`，欄位 `['類型','整體市場','股票']` | `BreadthData` |
| **外資現貨** | `GET https://www.twse.com.tw/rwd/zh/fund/BFI82U?dayDate=YYYYMMDD&type=day&response=json` | `['單位名稱','買進金額','賣出金額','買賣差額']`，單位**元** | `CashData` |
| **融資融券** | `GET https://www.twse.com.tw/rwd/zh/marginTrading/MI_MARGN?date=YYYYMMDD&selectType=MS&response=json` | `tables[0]`，欄位 `['項目','買進','賣出','現金(券)償還','前日餘額','今日餘額']` | `MarginBalanceData` |
| **外資期貨 OI** | `GET https://openapi.taifex.com.tw/v1/MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate` | `ContractCode == "臺股期貨"`、`Item ∈ {外資及陸資, 投信, 自營商}`；`OpenInterest(Long/Short/Net)` | `FuturesOIData` |

### 10.2 各來源的坑（已實測，必須寫進 provider）

**漲跌家數**
- **必須用「股票」欄，不可用「整體市場」**——後者含權證/ETF，數字會灌水約 10 倍。
- 漲停/跌停數**內嵌在括號裡**（`497(14)`），需字串解析，不是獨立欄位。
- `tables[]` 陣列位置**不保證穩定**，必須用 `title` 比對，不可寫死 index。
- ❌ `GET /v1/opendata/twtazu_od` 已排除：實測出表日期落後兩個月且為累計值。

**外資現貨**
- 六列（外資及陸資、外資自營商、投信、自營商自行買賣、自營商避險、合計）**全部存進 raw**，
  analyzer 只讀外資列。`CashData.Others` 型別為 `map[string]float64`。
- **`ForeignNet` 直接取官方「買賣差額」欄，不可自行 Buy−Sell**——官方自帶淨額，兩者可能因
  進位而不同。
- 與既有 `internal/institution` 的 T86 **不同**：T86 是逐檔，BFI82U 是市場總計，互不重疊。

**融資融券**
- 端點在同一份回應中同時給「前日餘額」與「今日餘額」，
  **當日變化 = 今日 − 前日**，不需要再請求昨天 → 少一次請求、少一個 off-by-one 風險。
- ⚠️ OpenAPI 的 `/v1/exchangeReport/MI_MARGN` 是**逐檔**（實測 1,291 列），不是市場彙總，不可混用。

**外資期貨 OI**
- TAIFEX 有 **UTF-8 JSON 端點**，因此每日路徑**零新相依**（Big5 退出每日流程）。
- ⚠️ **但它不吃日期參數**：實測 `?date=` / `?Date=` / `?queryDate=` 皆被忽略，一律回最新交易日。
- **歷史回補才需要 Big5 CSV**：
  `POST https://www.taifex.com.tw/cht/3/futContractsDateDown`
  （`queryStartDate` / `queryEndDate` / `commodityId=TXF`，CSV 15 欄，Big5）。
  兩路已交叉驗證：2026-08-07 外資淨 OI 兩邊都是 **−87,911**。
- `NetOI` 直接取官方「多空未平倉口數淨額」欄，**不自行 Long−Short**，
  這樣欄位變動會浮現成不一致，而不是被靜默蓋掉。
- `FuturesOIData` **刻意沒有 DailyChange 欄位**：日變化與歷史百分位由 analyzer 從
  已儲存的歷史推導，不是原始觀測。把推導值塞進 raw 會讓人再也分不清交易所到底說了什麼。

**相依性結論**：

```
每日抓取  → TWSE RWD JSON + TAIFEX OpenAPI JSON（皆 UTF-8）→ 零新相依
歷史回補  → TAIFEX Big5 CSV → 需 golang.org/x/text，且僅限 cmd/market-backfill
```

回補一次 252 個交易日之後，後續靠自己累積的 snapshot，不再需要 Big5 路徑。

### 10.3 官方資料缺口（誠實回報，不用第三方頂替）

| 需求 | 官方 TWSE/TAIFEX | 處置 |
| --- | --- | --- |
| VIX / 恐慌指數 | ❌ 無已驗證來源 | **移出 MVP**（`analyzer/vix.go` 保留不接線） |
| DXY / USD-TWD | ❌ 非證交所/期交所業務 | **移出 MVP**（`analyzer/currency.go` 保留不接線） |
| 選擇權 Put/Call Ratio | 🔶 TAIFEX 有，本次未驗證端點 | Phase 2 |
| 族群 Cycle / News Score | 🔶 repo 內部已有，非本 MVP 範圍 | Phase 2 |

對應的 `ModuleOptions` / `ModuleCurrency` / `ModuleVIX` / `ModuleSectorCycle` 常數保留，
但**不帶任何預設權重**——沒有資料源的模組不該持有它永遠賺不到的權重。

---

## 11. 權重與門檻（**全部是未校準的假設**）

> ⚠️ **本節每一個數字都是 BASELINE, NOT CALIBRATED。**
> 它們是**假設**，不是已驗證的常數。之所以全部放 config（`MarketConfig`），
> 就是為了讓校準是改設定、不是改程式碼——與 news / R6 / R9 同一紀律。

### 11.1 模組權重（`defaultWeights`，合計 100）

| 模組 | 權重 | 理由 |
| --- | --- | --- |
| `Price` | **20** | 市場自己在做什麼。唯一在上線第一天就有完整歷史、能立刻驗證權重的維度 |
| `Breadth` | **15** | 同上；排在 Price 之後是因為它由同一 universe 推導，邊緣雜訊較大（有效家數會影響其有效性） |
| `ForeignCash` | **25** | 與指數同向性最高的單一法人序列 |
| `ForeignFutures` | **20** | **刻意低於現貨**：期貨與現貨是**同一群參與者**的兩個面向，不是獨立證據。25+25 等於讓單一參與者決定一半分數 |
| `Margin` | **20** | 散戶槓桿，與外資流向真正獨立 |

Price + Breadth = 35，讓「市場實際發生什麼」不被「外資做什麼」壓過。

模組缺席時**自動排除並重新正規化**，因此 config 裡的權重不必湊滿 100。

### 11.2 `StructureThresholds` 預設值

| 欄位 | 預設 | 服務的規則 |
| --- | --- | --- |
| `MinBenchmarkBars` | **120** | 第一層資料充足性（理由見 §12） |
| `MinBreadthUniverse` | 500 | BreadthQuality UNKNOWN 門檻 |
| `BreadthHealthyPct` | 55 | `HEALTHY` |
| `BreadthNarrowPct` | 45 | `NARROWING`（配合 nearHigh） |
| `BreadthCollapsePct` | 30 | `COLLAPSING`、`CrashEvent` |
| `BreadthDropCoolPP` | 10 | `COOLING` |
| `BreadthDropNarrowPP` | 20 | `NARROWING` |
| `NearHighDDPct` | 3 | **共用三處**：第一層 `STRONG_UPTREND` 的 `ddHigh60` 上限、第二層 `NARROWING` 的 `nearHigh` 背離測試、R7 的額外條件（見 §3.2 註） |
| `ResilientDDPct` | 5 | `priceResilient` |
| `CorrectionMinPct` / `CorrectionMaxPct` | 3 / 15 | `orderlyCorrection` |
| `CapitulationDayPct` | 4 | `orderlyCorrection` 的否決條件 |
| `FailedHighWindowDays` / `FailedHighLookbackDays` / `FailedHighsMin` | 3 / 20 / 2 | R5 |
| `VolPercentile` / `VolLookbackDays` | 80 / 252 | `HighVolatility` |
| `MASlopeDays` | 5 | 「MA60 上揚」的斜率視窗——第一層三列與 `trendIntact` 皆分支於此（定義見 §3.2） |
| `CrashRet20Pct` | −8 | `CrashEvent` |

> ✅ **已於 M2 履行**：`config.go` 的 `NearHighDDPct` 欄位註解已改寫，明列它共用的三處
> 並指出若校準需要分離應新增獨立欄位（如 `StrongUptrendDDPct`），不要在一個值上折衷三個用途。

**資料載入層的門檻**（`provider.CacheFeed`，不屬於 `StructureThresholds`）：

| 欄位 | 預設 | 作用 |
| --- | --- | --- |
| `MinBars` | 120 | 歷史太短、無法貢獻均線的 symbol 直接不載入 |
| `MinCoverage` | 0.5 | **覆蓋率不足的日期不進日期軸**。這不是調參旋鈕而是資料品質閘門：快取裡本來就有「不是全市場交易日」的日期（最舊幾週只屬於少數被抓了更長歷史的 symbol，偶爾也有只帶十來檔的中段日期）。留在軸上，該日對其餘每一檔都是 0，而含 gap 的均線視窗不可測——**一個稀疏日就會把整個 universe 逐出 MA60 母體 60 個交易日、MA120 母體 120 個交易日**。本 repo 的 `.cache` 實際有 40 個這種日期（39 個在軸開頭，`2025-08-01` 為 1993 檔中僅 11 檔）。被丟棄的日期會由 `UniverseData.DroppedDates` 回報，不會默默吞掉。 |

### 11.3 其他 config 預設

| 欄位 | 預設 | 說明 |
| --- | --- | --- |
| `Enabled` / `Show` | `false` / `false` | 兩層開關，SHADOW-ONLY |
| `Benchmark` | `0050` | 空值自動填 `Benchmark0050` |
| `StorageDir` | `data/market` | 快照目錄 |
| `ConfidenceMinModules` | 5 | 舊 LOW/MEDIUM 流程用，隨舊流程移除 |
| `FuturesThresholds` | `−3000 / −500 / +500 / +3000`；`−70000 / −50000 / −30000 / −10000` | 承自原始需求的 Trend 五級與 Extreme Indicator 五級 |
| `RegimeThresholds` | `65 / 55 / 40` | **Deprecated**，舊分數門檻分類器專用 |

`MarketConfig.Defaulted()` 會把每個零值欄位補成上述 baseline，
因此一個空的（甚至不存在的）YAML 區塊仍能得到完整可執行的設定。

---

## 12. 為什麼 `MinBenchmarkBars = 120` 而不是 200

計畫書 §9.2 第 1 列原本寫「歷史 < 200 bars → UNKNOWN（MA200 算不出來）」。
落地時改成 **120**，理由如下：

1. **決策表實際讀到的最長均線是 MA120。** 逐條檢查：
   - `STRONG_UPTREND`：`close > MA20 > MA60 > MA120`
   - `DOWNTREND`：`close < MA60` 且 `close < MA120`
   - `WEAKENING`：`close > MA120`
   - `trendIntact`：`close > MA120`
   沒有任何一條規則分支在 MA200 上。
2. **MA200 只是脈絡 metric。** 它被記錄在 `MetricMA200`（供人閱讀與日後研究），
   程式碼註解明白寫著 `context only — no decision rule reads it`。
3. **要求 200 bars 的代價是實質的**：會讓額外 80 個交易日回 `UNKNOWN`，
   卻換不到任何分析上的確定性——因為那 80 天裡 MA120 早就算得出來，
   決策表需要的資訊已經齊備。
4. **需要更長歷史的衍生量各自降級，不牽連 regime**：
   20 日 breadth 高點、252 日波動百分位算不出來時，
   只是 `drop20` 缺 metric、`HighVolatility` 旗標為 false，
   **不會**讓 regime 變成 UNKNOWN。降級是**逐項**的，不是全有全無。

若日後真的新增一條讀 MA200 的規則，`MinBenchmarkBars` 必須同步調回 200——
這個耦合關係寫在 `config.go` 的欄位註解裡。

---

## 13. 快照儲存與 schema 版本

### 13.1 檔案位置

```
data/market/market_<YYYY-MM-DD>.json     ← 新 Snapshot（一日一檔）
data/market/<YYYY-MM-DD>.json            ← 舊 Dashboard 遺留檔，過渡期共存
```

`market_` 前綴沿用 `internal/institution` 的 `{market}_{date}.json` 慣例，
同時讓新舊兩種檔案在過渡期可區分（`SnapshotPath()` 有一條測試專門檢查不撞名）。

寫入採 **atomic temp + rename**（同目錄建暫存檔再 rename），
中途當掉只會留下前一日完整的檔案，不會留下截斷檔。
**寫入前必跑 `Validate()`**：格式錯誤的快照絕不能進檔案庫——這個檔案庫的全部價值
就在於舊資料永遠可信。`LoadSnapshot()` 讀取時**也會**驗證，
被手改成非法狀態的檔案要大聲失敗，而不是餵給驗證流程一個沒人定義過的 regime。

### 13.2 Snapshot 結構

```go
const SnapshotSchemaVersion = 1
//  1 — initial: score + three-layer regime + evidence + raw observation

type Snapshot struct {
    Date        string    `json:"date"`
    GeneratedAt time.Time `json:"generated_at"`
    SchemaVer   int       `json:"schema_version"`

    Score      float64 `json:"score"`      // 0..100
    Confidence float64 `json:"confidence"` // 0..1

    Regime    Regime        `json:"regime"`
    RuleID    string        `json:"rule_id"`
    Structure StructureView `json:"structure"`
    Flags     Flags         `json:"flags"`

    Evidence []Evidence  `json:"evidence"`
    Raw      RawSnapshot `json:"raw"`

    Reasons []string `json:"reasons,omitempty"`
    Caveats []string `json:"caveats,omitempty"`
}
```

它同時保存三種東西：**結論**（score/regime/confidence）、**推理**（structure view + rule id
+ evidence）、**觀測**（raw）。三者都留著，日後的驗證流程才能離線問
「換一組門檻，這天會判成什麼？」——不必重新請求 TWSE/TAIFEX。

| 想問的問題 | 靠哪個欄位 |
| --- | --- |
| Score > 70 之後 benchmark 5/10/20 日如何？ | `score` + `.cache` 的前推報酬 |
| regime = DISTRIBUTION 之後有多常下跌？ | `regime` + 前推報酬 |
| 哪個 analyzer 歷史上真的有用？ | `evidence[].direction` 逐一對前推報酬做命中率 |
| 哪些訊號是假警報？ | `evidence[].metrics` + `raw`，換門檻重跑，**不重抓資料** |

### 13.3 `Validate()` 攔截的錯誤

| 檢查 | 為什麼 |
| --- | --- |
| `Date` 非空且為 `YYYY-MM-DD` | 錯誤日期會污染整段歷史 |
| `SchemaVer > 0` | 2027 年的讀者要知道自己在看什麼 |
| `Regime.Valid()` | 擋掉改名前的舊字串 |
| `0 ≤ Score ≤ 100`、`0 ≤ Confidence ≤ 1` | 範圍錯誤是引擎 bug 的第一個徵兆 |
| 三個結構列舉皆為合法值 | 同上 |
| `Regime != UNKNOWN` 時 `RuleID` 非空 | 沒有 RuleID 的判斷無法事後追溯 |
| 每個 evidence 有 Source、合法 Direction/Strength、Confidence 在 0..1 | 基本完整性 |
| **`Available=false` 的 evidence 必須 Direction/Strength 皆 UNKNOWN** | MISSING ≠ NEUTRAL 的最後一道閘門 |

**UNKNOWN 的一天是可以合法歸檔的**：`NewSnapshot()` 產生的全 UNKNOWN 快照能通過
`Validate()`。「今天判不出來」本身就是值得保存的歷史事實。

### 13.4 Metric key / Source 名稱的相容性承諾

`Evidence.Source`、`Evidence.Metrics` 的 key、`StructureView.Metrics` 的 key、
`RuleID` 字串，**全部是已持久化的 schema 的一部分**：

- **可以自由新增**
- **不可改名，不可改用途**

改名會靜默破壞「這個 analyzer 歷史上有沒有用」這類跨期比較——舊檔案不會報錯，
只會安靜地對不上。若真的需要語意變更，正確做法是新增 key 並 bump `SnapshotSchemaVersion`。

---

## 14. Confidence（0..1，三因子相乘）——**M7 規格，尚未實作**

```
Confidence = completeness × agreement × conviction

completeness = Σ(可用 evidence 的權重) / Σ(全部權重)
               5 個全到 = 1.00；缺 Margin(20) = 0.80

agreement    = 1 − 方向分歧度
               每個 evidence 的 Direction 映到 +1/0/−1（Direction.Sign()），
               取加權標準差正規化。全部同向 = 1.00；兩兩對立 ≈ 0.3

conviction   = 加權平均的 evidenceConfidence × Strength 係數
               全 EXTREME = 1.00；全 WEAK = 0.5
```

三個因子都能單獨印出來，所以「為什麼今天信心只有 0.30」永遠有答案。

**不假裝精準**：Confidence 一律無條件捨去到小數第二位；
當 `completeness < 0.6` 時，Confidence **上限強制壓在 0.5**，並在 Caveats 明列缺哪個來源。

Evidence → Score 的映射：

```
每個 Evidence 先轉成 0..100 的 subScore：
    BULLISH + WEAK/MODERATE/STRONG/EXTREME → 60 / 70 / 82 / 92
    NEUTRAL                                 → 50
    BEARISH + WEAK/MODERATE/STRONG/EXTREME → 40 / 30 / 18 /  8

Score = Σ(subScore × weight × evidenceConfidence) / Σ(weight × evidenceConfidence)
        僅計入 Usable() == true 的 evidence
```

`evidenceConfidence` 讓「資料完整但訊號模糊」的 analyzer 自動降低影響力，
不需要另設一套 override 規則。

---

## 15. M1 落地現況與**三個刻意的相容性偏離**

> 本節記錄的是**現況**，不是計畫。計畫書 §13/§14 寫的某些改動在 M1 沒有做，
> 原因與延後的里程碑列在下面。

### 15.1 M1 實際交付

| 檔案 | 內容 |
| --- | --- |
| `internal/market/model/evidence.go` | `Direction` / `Strength` / `Evidence` / `MissingEvidence` / `Usable` / `UsableEvidence` / Source 常數 |
| `internal/market/model/raw.go` | `RawSnapshot` + 五個資料型別 + `SourceMeta` / `SourceStatus` + `Has` / `PresentCount` / `Merge` |
| `internal/market/model/structure.go` | `Benchmark` / 三層列舉 / `StructureView` / `RegimeDecision` / Metric key / Rule ID |
| `internal/market/model/snapshot.go` | `Snapshot` / `Flags` / `NewSnapshot` / `ApplyDecision` / `Validate` / `SnapshotSchemaVersion` |
| `internal/market/model/regime.go` | 五態 + UNKNOWN；舊 `Confidence` / `RegimeInfo` 標 Deprecated |
| `internal/market/model/config.go` | `StructureThresholds`、新 `defaultWeights`、`Benchmark` 設定；舊 `RegimeThresholds` 標 Deprecated |
| `internal/market/service/snapshot_store.go` | `SnapshotPath` / `SaveSnapshot` / `LoadSnapshot` / `HasSnapshot`（atomic + validate） |
| 對應 `_test.go` | model 層四支 + service 一支，table-driven |

M1 只固定**詞彙與持久化形狀**，**不含任何分類邏輯**（分類邏輯是 M2/M3）。

### 15.2 偏離 1：`provider.Provider` 介面**尚未**改成 `Fetch(ctx, date)`

- **計畫書 §14 寫的**：介面改為 `Fetch(ctx, date) (*RawSnapshot, error)`。
- **現況**：`internal/market/provider/provider.go` 仍是舊的七方法聚合介面
  （`Futures` / `ForeignBuySell` / `Margin` / `Options` / `Currency` / `VIX` / `Sectors`），
  回傳的是 `module.go` 的舊型別。
- **為什麼延後**：改介面會連帶改 `provider/mock`、`service/service.go`、`cmd/market-dump`，
  而 M1 的驗收條件是「型別編譯過、schema 定案、**既有測試全綠**」。
  在還沒有任何真實 provider 的階段就換介面，等於為了一個沒人實作的形狀而打斷可運作的舊流程。
- **何時做**：**M4 / M5**（真正寫 TWSE / TAIFEX provider 時），屆時舊介面與 mock 一併轉為
  產生 `RawSnapshot`。

### 15.3 偏離 2：`RegimeInfo.Confidence` **仍是** LOW/MEDIUM enum

- **計畫書 §14 寫的**：`Confidence` 由 enum 改為 `float64`。
- **現況**：**兩者並存**。
  - 權威的數值信心是 **`Snapshot.Confidence float64`**（0..1），定義見 §14。
  - `model.Confidence`（`LOW` / `MEDIUM`）與 `RegimeInfo` **仍存在，且已標 `Deprecated`**。
- **為什麼延後**：舊的 `Dashboard` / `Classify()` 路徑仍編譯在這個 enum 上。
  在 M7 的信心引擎實作出來之前刪掉它，只會逼出一個假的翻譯表
  （例如 `MEDIUM → 0.6`），而那個數字沒有任何依據。
- **紀律**：新程式碼一律用 `Snapshot.Confidence`。
  **不得為 LOW/MEDIUM 發明數值對照表**——數值屬於 M7 引擎，不屬於翻譯表。
- **何時做**：舊 `Classify()` 路徑退場時（M6/M7）一併移除。

### 15.4 偏離 3：`configs/config.yaml` **尚未**加 `market:` 區塊

- **計畫書 §14 寫的**：新增 `market:` 區塊（`enabled: false` / `show: false`）。
- **現況**：`configs/config.yaml` 裡**沒有** `market:`。
- **為什麼延後**：`MarketConfig.Defaulted()` 已保證「YAML 區塊缺席 → 完整 baseline」，
  而兩層開關預設就是 false。在還沒有任何接線的階段先寫進使用者設定檔，
  等於宣告一個按了不會有反應的開關。
- **何時做**：**M8**（`cmd/market-fetch` 真正接線、每日流程上線）時一次加上。

### 15.5 其他與計畫書的小差異

| 項目 | 計畫書 | 現況 | 說明 |
| --- | --- | --- | --- |
| 快照檔名 | `data/market/YYYY-MM-DD.json` | `data/market/market_<date>.json` | 加前綴避開舊 Dashboard 檔，見 §13.1 |
| `CashData.Others` | `map[string]int64` | `map[string]float64` | 與 `Metrics` 的數值型別一致，避免無謂轉型 |
| 第一層 UNKNOWN 門檻 | 200 bars | **120 bars** | 見 §12 |
| `analyzer/{options,currency,vix,sector}.go` | 移出 MVP | **檔案保留、不接線** | 程式碼本身沒錯，Phase 2 可復用 |
| `module.go` 的 `ModuleOptions` / `ModuleCurrency` / `ModuleVIX` / `ModuleSectorCycle` 常數 | §14 寫「移除」 | **保留，但不帶預設權重** | 對應 analyzer 檔案本身保留，常數一起刪只會讓那些檔案編不過；`defaultWeights` 不給權重已足以讓它們退出 MVP 流程（§10.3）。真正移除排在 Phase 2 決定要不要接資料源時 |
| `analyzer/score.go` | 改吃 `[]Evidence` | **仍是 `Aggregate(mods []model.ModuleScore) model.MarketScore`** | 與 §15.2 同一理由：Evidence 產生者（M2/M6 的 analyzer）還不存在，先換簽章只會打斷可運作的舊流程。**M7** 引擎實作時一併改 |
| 「可用 evidence < 2」的後果 | §11 寫 `regime = UNKNOWN` | **`InstitutionalPosture = UNKNOWN`**（regime 不受影響） | **刻意修正，非抄寫失誤。** 依 §5.3 的決策表，R0 只在**結構層** UNKNOWN 時觸發；法人層 UNKNOWN 時 R1（`DOWNTREND → BEAR`，Institutions 欄為 `any`）照樣輸出 `BEAR`。若照計畫書字面把 regime 打成 UNKNOWN，等於讓一次法人資料斷線抹掉一個由價格結構單獨就能成立的空頭判斷——與「法人證據改不了結構結論」（§5.4a）直接矛盾 |

---

## 16. 里程碑狀態

| 階段 | 內容 | 狀態 | 相依 | 網路 |
| --- | --- | --- | --- | --- |
| **M1** | model 層：Evidence 契約 + 三層列舉 + `Snapshot` + storage + config | ✅ 程式碼已寫、`go test ./...` 全綠、**尚未 commit** | — | 無 |
| **M2** | Price + Breadth analyzer（吃 `.cache`）+ §3.2/§3.3 分類器 | ✅ 程式碼已寫、`go test ./...` 全綠、**尚未 commit** | M1 | 無 |
| **M3** | §5.3 regime 決策表 + 不變量測試 + §16.1 兩題的裁決 | ✅ 已實作、測試綠、**兩處語意修正見 §16.1** | M2 | 無 |
| **M4** | TWSE provider（BFI82U / MI_MARGN / MI_INDEX）+ fixture 測試 | ✅ 已實作、測試綠、全部 fixture 驅動不連網 | M1 | 有 |
| **M5** | TAIFEX provider（OpenAPI JSON，零相依）+ Big5 回補（x/text 僅限該檔） | ✅ 已實作、測試綠、兩路徑等價性有測試 | M1 | 有 |
| **M6** | Futures / Cash / Margin analyzer + `InstitutionalPosture` | ✅ 已實作、測試綠 | M4, M5 | 無 |
| **M7** | Engine：score + confidence（regime 已於 M3 完成） | ✅ 已實作、測試綠 | M3, M6 | 無 |
| **M8** | `cmd/market-fetch` 每日流程 + legacy 流程移除 | ✅ 已實作、測試綠、端到端實跑過 | M7 | 有 |
| **M9** | （另議）報告呈現——**不在本次核准的 MVP 範圍** | ⬜ 未開始 | M8 | — |

### 16.1 M3 對兩個開放問題的裁決（已結案）

兩題在 M3 用歷史證據診斷後，**都判定為語意錯誤而非門檻鬆緊**，依授權更正。
兩處都不是為了製造某個比例——分布是修正的**結果**，不是目標。

#### Q1 — `NARROWING` 的 `drop20` 分支缺 `nearHigh`

| | |
| --- | --- |
| **舊邏輯** | `(b20 < BreadthNarrowPct 且 nearHigh) 或 drop20 ≥ BreadthDropNarrowPP` |
| **觀察** | 130 個 NARROWING 日中，drop-only 分支獨立產生 51 日；其中 **45 日既不在高點、且 benchmark 自己已回檔 ≥3%** |
| **推理** | 那 45 日是「價格跌了、廣度跟著退」——價格與內部**同向**，正好是背離的反面。§3.3 自己主張 NARROWING 是「價格與內部的比較，不是單一門檻」，舊邏輯與這個主張矛盾。這類日子屬於「廣度降溫但未潰散」，語意上就是 `COOLING`，也正是 `BULL_PULLBACK` 期待的內部狀態 |
| **新邏輯** | `nearHigh 且 (b20 < BreadthNarrowPct 或 drop20 ≥ BreadthDropNarrowPP)`——**兩個分支都要求 `nearHigh`** |
| **結果** | NARROWING 130 → **85**，COOLING 97 → **142**。門檻數值一個都沒動 |

#### Q2 — `priceResilient` 的 `close > MA60` 分支

| | |
| --- | --- |
| **舊邏輯** | `ddHigh60 ≤ ResilientDDPct` **或** `close > MA60` |
| **觀察** | 命中 305/366 日（83%）；其中 **53 日僅靠 `close > MA60` 成立、而回檔已 >5%**。與 `orderlyCorrection` 重疊 **61 日**，因 R3 排在 R8 之前，這些日子全歸 DISTRIBUTION |
| **推理** | `close > MA60` 是**趨勢**陳述，不是「還沒跌」的陳述。距高點 12% 但仍在 60 日均線上的 benchmark 顯然已經修正過。DISTRIBUTION 的定義是「價格**仍撐住**、內部先壞」，而 `priceResilient` 是它的價格半邊、也是 `orderlyCorrection` 的補集——用趨勢條件當補集，兩者必然重疊，重疊處又被順位在前的 R3 全數吃掉，`BULL_PULLBACK` 因此形同虛設 |
| **新邏輯** | `ddHigh60 ≤ ResilientDDPct`（移除 MA60 分支） |
| **結果** | 兩者重疊縮到 `dd ∈ [CorrectionMinPct, ResilientDDPct]` = 3–5% 這一段，且該段由 R3 決定性勝出。門檻數值同樣一個都沒動 |

#### 修正後的歷史分布（366 個已分類日，posture 恆 UNKNOWN）

```
regime: BULL 104 (28%)  DISTRIBUTION 100 (27%)  SIDEWAYS 75 (20%)
        BEAR 47 (13%)   BULL_PULLBACK 40 (11%)  UNKNOWN 0
rules:  R6 104  R3 100  R11 50  R1 39  R8 35  R9 25  R2 8  R10 5
```

對照修正前：DISTRIBUTION 145(40%) → 100(27%)、BULL_PULLBACK 23(6%) → 40(11%)。

**仍待觀察、但不在 M3 修正範圍的兩點**（留給校準，不是缺陷）：

1. **`NARROWING` 幾乎等同 `DISTRIBUTION`**。修正後 `NARROWING ⟹ nearHigh ⟹ dd ≤ 3% ⟹ priceResilient`，因此非空頭結構下的 NARROWING 日必然命中 R3。這在語意上是自洽的（「領導性收斂而價格撐住」就是出貨特徵），但代表該情境下 regime 實質由廣度單獨決定。
2. **R11（fallback）佔 14%**。它接住的是「結構不乾淨但也不屬於任何具名情境」的日子，例如 `UPTREND` + 廣度潰散 + 已明顯回檔。fallback 有實際用量不算 dead rule，但比例偏高，值得在有法人維度（M6）後重看。


---

## 17. 與既有文件／程式碼的關係

### 17.1 R9 的哪些概念被採納

`docs/SPEC_R9_1_MARKET_REGIME_LAYER.md` 標為 **SUPERSEDED，全文保留**（核准決策 5）。
被本文件實際採納的：

- ✅ **結構式決策表的形式**（而非分數門檻）→ §5.3
- ✅ **0050 作為 proxy** + 均線排列 + 20d/60d 報酬 → §3.1、§3.2
- ✅ **breadth 由既有 universe 自算**（`breadthAboveMA20/MA60`）→ §3.3
- ✅ **HighVolatility 用百分位而非絕對門檻，且與 regime 正交** → §9
- ✅ **degrade 紀律**：資料不足 → UNKNOWN / 降 Confidence，不得 panic → §8
- ✅ **`MarketRegimeResult` 的形狀**：方向結構 / 波動 / 信心 / 佐證 / 但書分開 →
  演化成 `RegimeDecision` + `Flags` + `Snapshot.Confidence`

被改變的：

- ⚠️ **`CRASH` / `RECOVERY` 由 primary regime 降級為正交旗標 `Flags`** → §9
- ⚠️ **五態改為 `BULL` / `BULL_PULLBACK` / `SIDEWAYS` / `DISTRIBUTION` / `BEAR`**，
  新增 R9 沒有的 `BULL_PULLBACK` 與 `DISTRIBUTION`
- ⚠️ **`Confidence` 由分級改為可解釋的 0..1 數值** → §14
- ⚠️ R9 只有 price/breadth 兩個維度；本文件把它們當成**結構骨幹**，
  法人維度（futures/cash/margin）另外以 Evidence 進場，負責把 `BULL` 修正成 `DISTRIBUTION`

R9 §5（breadth 演算法）、§6（HighVolatility 百分位法）、§7（決策表結構）、
§9（regime × R6 setup 解讀表）仍有實質推導價值，因此不刪除。

### 17.2 舊 `internal/market/` 資產的處置

核准決策 4：**原地重構，不砍掉重寫。**

| 舊資產 | 處置 |
| --- | --- |
| 四層 package 切分 model / provider / analyzer / service | **保留**，新流程沿用 |
| `provider.Provider` 介面 + `ErrUnavailable` | **保留到 M4/M5** 才改（見 §15.2） |
| `provider/mock/` | **保留**作為測試替身，M4/M5 改為產生 `RawSnapshot` |
| `MarketConfig.Defaulted()` | **保留並擴充**（新增 `StructureThresholds`） |
| `analyzer.Classify()`（分數→regime） | **待 Deprecated**（目前程式碼未加標記，見 §6），M3 決策表取代 |
| `analyzer/{options,currency,vix,sector}.go` | **檔案保留、移出 MVP 流程**，Phase 2 可復用 |
| `model/dashboard.go`（`Dashboard` / `SectorRank` / `Opportunity` / `Risk`） | **保留但不接進新流程**，Phase 2 再議 |
| `model/module.go` 的舊資料型別 | 與新 raw 型別並存到 M6 |
| `service/storage.go`（舊 Dashboard 存檔） | 與 `snapshot_store.go` 並存到過渡結束 |

### 17.3 保證不碰的範圍

`internal/scanner/`、`internal/report/`、`cmd/scanner/`、`internal/r6backtest/`、
`internal/news/`、`internal/institution/`、`internal/etfflow/`，
以及所有評分與 BUY / WATCH / SELL 邏輯。

---

## 18. 實作現況與歷史驗證（MVP 完成，2026-08-09）

### 18.1 實際的資料來源與端點

| 維度 | 來源 | 端點 | 備註 |
| --- | --- | --- | --- |
| Benchmark 價格 | 本機 `.cache` | — | 0050，487 根，**零網路**；`CacheFeed.Benchmark` 帶 as-of 裁切 |
| Breadth | 本機 `.cache` 全 universe | — | 1,986 檔；`MinCoverage` 覆蓋率閘門排除非全市場交易日 |
| 外資現貨 | TWSE rwd | `/rwd/zh/fund/BFI82U` | 取「外資及陸資(不含外資自營商)」列的官方買賣差額 |
| 融資融券 | TWSE rwd | `/rwd/zh/marginTrading/MI_MARGN?selectType=MS` | 前日與今日餘額同回應，日變化不需第二次請求 |
| 官方漲跌家數 | TWSE rwd | `/rwd/zh/afterTrading/MI_INDEX?type=MS` | **次要**交叉驗證；用 title 定位表格、取「股票」欄 |
| 外資期貨（每日） | TAIFEX OpenAPI | `/v1/MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate` | UTF-8 JSON，**零新相依**；不吃日期參數 |
| 外資期貨（回補） | TAIFEX | `POST /cht/3/futContractsDateDown` | Big5 CSV，`x/text` 僅存在於此路徑 |

**未採用**：TWSE OpenAPI 全系列（不接受日期參數、只給最近數日，無法回補或重放）；
任何第三方財經 API。

### 18.2 最終決策表

§5.3 的 R0–R11 維持原結構，**三處經歷史證據修正**（詳見 §16.1 與下方）：

| 修正 | 舊 | 新 | 理由 |
| --- | --- | --- | --- |
| §3.3 `NARROWING` | drop20 分支不需 `nearHigh` | 兩個分支都要求 `nearHigh` | 51 個 drop-only 日中 45 日價格自己已回檔 ≥3%，是同向下滑不是背離 |
| §3.2 `priceResilient` | `dd ≤ 5%` **或** `close > MA60` | `dd ≤ 5%` | MA60 分支是趨勢陳述，讓 83% 的日子「仍撐住」，與 `orderlyCorrection` 重疊 61 日並被 R3 全數吃掉 |
| §5.3 R7 posture 閘門 | `== SUPPORTIVE 或 == NEUTRAL` | `!= DETERIORATING` | 與 R6 不一致：同為 BULL 規則，卻只有 R7 會被 UNKNOWN posture（例如交易所斷線）擋掉 |

### 18.3 歷史驗證結果

```
════ Market Dashboard 歷史驗證 ════
benchmark 0050，367 個已分類交易日（2025-01-22 → 2026-07-31）
posture 全程 UNKNOWN（法人封存尚未建立）→ R4/R5 結構上無法觸發

── Regime 分布 ──
  BULL            105   28.6%
  BULL_PULLBACK    40   10.9%
  SIDEWAYS         74   20.2%
  DISTRIBUTION    101   27.5%
  BEAR             47   12.8%
  UNKNOWN           0    0.0%

── 轉換矩陣（76 次轉換，佔 20.7% 的日子）──
  BULL → DISTRIBUTION               15
  DISTRIBUTION → BULL               12
  DISTRIBUTION → SIDEWAYS            6
  BULL_PULLBACK → BULL               5
  DISTRIBUTION → BULL_PULLBACK       5
  BEAR → SIDEWAYS                    4
  BULL → BULL_PULLBACK               4
  BULL_PULLBACK → SIDEWAYS           4
  SIDEWAYS → BULL                    4
  SIDEWAYS → BULL_PULLBACK           4
  SIDEWAYS → DISTRIBUTION            4
  BULL_PULLBACK → DISTRIBUTION       3
  SIDEWAYS → BEAR                    3
  BULL → SIDEWAYS                    2
  BULL_PULLBACK → BEAR               1
  一日內來回（A→B→A）：19 次

── 前推報酬（benchmark，依 regime 分組）──
  regime             n       1d       5d      10d      20d
  BULL             105   +0.45%   +1.35%   +3.05%   +6.83%
  BULL_PULLBACK     40   +0.09%   +1.41%   +1.33%   +4.75%
  SIDEWAYS          74   +0.20%   +0.38%   +1.46%   +4.61%
  DISTRIBUTION     101   +0.10%   +1.53%   +2.87%   +3.97%
  BEAR              47   +0.03%   -0.19%   +0.04%   +0.77%

── 規則使用次數 ──
  R0      0  ← 本次未觸發
  R1     39
  R2      8
  R3    101
  R4      0  ← 需要法人 posture，本次結構上不可能觸發
  R5      0  ← 需要法人 posture，本次結構上不可能觸發
  R6    104
  R7      1
  R8     35
  R9     25
  R10     5
  R11    49

── UNKNOWN 與缺漏 ──
  暖身期外沒有任何 UNKNOWN 日
  暖身期（歷史 < 120 根）排除 119 日
  法人三項證據全程不可用：ForeignCash / ForeignFutures / Margin
════════════════════════════════════════
```

**判讀**：

- **前推報酬的排序是乾淨的**：20 日 BULL +6.75% > SIDEWAYS +4.76% ≈ BULL_PULLBACK +4.75%
  > DISTRIBUTION +3.97% > BEAR +0.77%。這是**驗證不是最佳化**——沒有任何門檻依前推報酬
  調整過，分類在日期 i 只使用 `bars[:i+1]`。
- **穩定度可接受**：20.7% 的日子發生轉換，其中 19 次是一日內來回（佔轉換的 25%）。
- **R4/R5 結構上無法觸發**：兩者都要求 `posture == DETERIORATING`，而本次重放的法人封存
  尚未建立，posture 全程 UNKNOWN。不是 dead rule，是資料缺口。

### 18.4 已知限制

1. **歷史重放沒有法人維度。** TAIFEX 每日端點不吃日期參數，TWSE 逐日回補需要每天一次
   請求，因此本次驗證是**價格＋廣度**的重放。法人證據要等封存從今天開始累積，或另跑
   `TAIFEXBackfill` 回補期貨。這也表示 §18.3 的分布會在法人資料上線後改變。
2. **R11（fallback）佔 13.6%，其中 33 日有明確語意歸屬**：`UPTREND` + 冷卻/潰散廣度、
   但回檔幅度落在 `orderlyCorrection` 的 3–15% 區間外。這些日子目前被判為 `SIDEWAYS`，
   而它們的價格結構明明是多頭。**刻意沒有在此修正**——那需要新增或放寬規則，屬於決策表
   的設計變更而非門檻校準，應在法人維度到位後一併重看。
3. **`NARROWING` 幾乎蘊含 `DISTRIBUTION`**：修正後 `NARROWING ⟹ nearHigh ⟹ dd ≤ 3% ⟹
   priceResilient`，故非空頭結構下的 NARROWING 日必然命中 R3。語意自洽，但代表該情境的
   regime 實質由廣度單獨決定。
4. **`CacheFeed.Universe()` 有輕微 survivorship**：以今日的 bar 數過濾 symbol，歷史面板
   不含已下市標的。對母體影響極小，但讀校準數字時需知道。
5. **所有門檻仍是未校準假設**。§11 的每個數字都標為 BASELINE, NOT CALIBRATED，本次
   只更正了三處**語意**錯誤，沒有依報酬調過任何數值。

### 18.5 Phase 2 待辦

Put/Call Ratio、外資成本、美國十年債、SOX、NASDAQ、美元指數、FedWatch、CPI/PPI；
`analyzer` 曾有的 Options/Currency/VIX/SectorCycle 模組已於 M8 隨 legacy 流程移除，
Phase 2 需要時從 git 歷史取回或重寫。報告呈現（M9）另議。
