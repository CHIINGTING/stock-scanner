# Market Dashboard MVP — Phase 1 設計報告

> 狀態：**設計已核准（2026-08-09）。本文件已依核准決策修訂。仍未寫任何程式碼。**
> 所有官方端點皆於 2026-08-09 **實際發送請求驗證**，欄位取自真實回應，非憑記憶。

---

## 0. 已核准決策（2026-08-09）

| # | 決策 | 對設計的影響 |
| --- | --- | --- |
| 1 | **Big5**：允許 `golang.org/x/text`，但優先找官方 UTF-8 端點；不得自行手寫 Big5 解碼器 | ✅ **已找到 TAIFEX UTF-8 JSON 端點**（§3.5 已重寫）。每日抓取零新相依；`x/text` 僅在**歷史回補**時需要 |
| 2 | **基準標的**：MVP 用 0050，但**必須明確建模為 proxy**，不得標成 TAIEX | 新增 `Benchmark` 型別（`Benchmark0050` / `BenchmarkTAIEX`），MVP 預設 `Benchmark0050`；所有輸出欄位命名為 `benchmark_*` 而非 `taiex_*` |
| 3 | **Breadth 主來源**：既有全市場 universe（`.cache`），提供 MA20/MA60/MA120 比例與 advancing ratio；TWSE 官方漲跌家數降為次要驗證資料 | §7.2 已更新；M4 不依賴任何網路端點 |
| 4 | **既有 `internal/market/`**：原地重構，不砍掉重寫；保留 package 切分、provider 契約、config/default、mock 基礎、storage、scanner 隔離、既有測試 | §14 已改為「重構」而非「刪除」；`provider/mock` **保留**（原本規劃刪除，現改為保留作為測試替身） |
| 5 | **R9**：不刪除，標為 superseded／已納入；其 price/breadth 決策表概念餵給新的 Price/Breadth analyzer 與結構式 regime。最終只能有一個 regime 引擎 | §2.2(5) 維持原建議；R9 標頭改指向新 spec（實作階段才動） |
| 6 | **Spec 命名**：不綁 R 編號，採領域導向 `docs/SPEC_MARKET_DASHBOARD.md`；里程碑編號另外在 roadmap 引用 | 本文件（PLAN）為施工計畫；核准後另立 `docs/SPEC_MARKET_DASHBOARD.md` 為 canonical spec |
| 7 | **Regime 設計退回重做** | §9 已整段重寫：改為 PrimaryStructure → BreadthQuality + InstitutionalPosture → 公開 regime 的三層設計 |

---

## 1. Repository Findings

### 1.1 最重要的發現：`internal/market/` 已經存在

工作區裡已經有一套 Market Dashboard 實作（**未 commit**），來自更早的需求（現已歸檔為
`docs/SPEC_R10_MARKET_DASHBOARD.md`）：

```
internal/market/
  model/     dashboard.go score.go regime.go module.go config.go
  provider/  provider.go  mock/
  analyzer/  cash.go currency.go futures.go margin.go options.go vix.go
             sector.go score.go regime.go states.go text.go
  service/   service.go storage.go
cmd/market-dump/
```

它已經做對的事，**應該保留**：

| 既有資產 | 為什麼可以直接沿用 |
| --- | --- |
| 四層切分 model / provider / analyzer / service | 正是本次要求的責任分離；`model` 零內部相依，無循環 |
| `provider.Provider` 介面 + `ErrUnavailable` sentinel | 隔離契約已寫好：單一來源失敗不阻斷其餘模組 |
| `MarketConfig.Defaulted()` | 門檻全走 config、零值自動填 baseline，符合 repo 慣例 |
| `service/storage.go`：`<dir>/<date>.json` | 每日一檔快照，已有 Save/Load + 測試 |
| `analyzer` 皆為純函式 + table-driven 測試 | 符合 repo 測試慣例 |
| 文案紅線：regime 敘述不得含買/賣/下單/進場價 | 與本次 SHADOW-ONLY 定位一致 |
| 完全不 import `internal/scanner` / `internal/report` | 「不影響既有 scanner」已成立，非口號 |

它與本次需求**衝突、必須改掉**的地方：

| 衝突 | 現況 | 本次要求 |
| --- | --- | --- |
| **Regime 由分數門檻決定** | `analyzer.Classify()`：`ms.Total < BearMax → Bear`、`>= BullMin → Bull` | §10 明文禁止 `if score >= 70 { regime = Bull }` |
| **模組依賴非 TWSE/TAIFEX 資料** | 有 Options / Currency / VIX 三個 analyzer（權重共 35%） | §4 MVP 只准 TWSE + TAIFEX；VIX/DXY 沒有官方台灣來源 |
| **沒有 Evidence 層** | analyzer 直接產出 `ModuleScore{Score,Weight}` 餵進加權平均 | §8 要求 analyzer → Evidence → Engine 的中介層 |
| **Confidence 只有 LOW/MEDIUM** | 由「可用模組數」決定 | §17 要求可解釋的百分比，含 analyzer 一致性 |
| **單日快照、無歷史脈絡** | Futures 用絕對口數門檻、Cash 只看單日 | §13/§14 要求 position vs change vs 歷史百分位、1D/5D/20D |

**建議：原地重構，不要新開 package。** 保留骨架與慣例，抽換引擎內臟。這同時滿足
「ONE market regime source of truth」與「不要過度設計 package 階層」。

### 1.2 已經存在、可直接餵資料的資產

| 資產 | 位置 | 對本案的價值 |
| --- | --- | --- |
| **官方 TWSE/TPEX HTTP provider 範本** | `internal/institution/{twse,tpex,provider}.go` | 已 live-verified 打過 `twse.com.tw/rwd/zh/fund/T86`：`http.NewRequestWithContext` + status 檢查 + `stat != "OK"` → `ErrNoData`。**新的 provider 照抄這個形狀即可**，不需要發明新模式 |
| **每日快照儲存範本** | `internal/institution/snapshot.go` | atomic temp+rename、`Validate()`、`{market}_{date}.json`。比 `market/service/storage.go` 更嚴謹，建議統一往這個靠 |
| **`.cache` 全市場日 K** | 1,986 檔 × 487 根（2024-07→2026-07） | **Breadth 可以完全用它算，不需要任何新端點** |
| **`.cache/0050_TW.json`** | 487 根日 K | **Price 結構（MA20/60/120/200、20d/60d 報酬、已實現波動）完全用它算，不需要新端點** |
| **均線工具** | `internal/indicator/ma.go`、`bias.go` | 直接重用，不重寫 |
| **as-of / look-ahead 紀律** | `internal/r6backtest/` | 每日只用當日含以前的 bar，已有成例 |
| **兩層開關慣例** | `enable_*` / `show_*`，預設 false | 直接沿用，SHADOW-ONLY 天然成立 |

### 1.3 相依性現況

`go.mod` 只有 `gopkg.in/yaml.v3`。全案 **stdlib-only**。這對 TAIFEX 有直接影響（見 §3.5）。

---

## 2. Existing R9 Analysis

`docs/SPEC_R9_1_MARKET_REGIME_LAYER.md`（477 行，design-only，無程式碼）。

### 2.1 R9 提出了什麼

- **0050 為 primary proxy**（理由：R6 Setup D 已用 `ProxySymbol = "0050"`，銜接成本低）；`^TWII` 列為 optional。
- **結構式 regime 決策表**：均線排列 + 20d/60d 報酬 + breadth 門檻，優先序由上而下第一個命中者勝。
- **breadth 用既有 universe 算**：`breadthAboveMA20 = 收盤 > 自身 MA20 的家數 / 有效家數`。
- **HighVolatility 是獨立布林，不是 regime**：`realizedVol20 >= P80(過去252日分佈)`，理由是「regime 是方向/結構，volatility 是波動程度，兩者正交」。
- degrade 規則、look-ahead 紀律、Reasons/Caveats 中文佐證、文案紅線、不自動交易聲明。

### 2.2 回答 §12 的五個問題

**(1) 哪些 R9 訊號應該重用？**

- ✅ **結構式決策表的形式**（非分數門檻）——這正是 §10 要的東西，R9 已經想對了。
- ✅ **0050 proxy + 均線排列 + 20d/60d 報酬**。
- ✅ **breadth 由既有 universe 自算**（`breadthAboveMA20/MA60`）。
- ✅ **HighVolatility 用百分位而非絕對門檻**，且與 regime 正交。
- ✅ **degrade 規則**：資料不足 → UNKNOWN / 降 Confidence，不得 panic。

**(2) 哪些 R9 模型/介面仍然有用？**

`MarketRegimeResult{PrimaryRegime, HighVolatility, Confidence, Reasons, Caveats}` 的**形狀**有用：
把「方向結構」「波動」「信心」「佐證」「但書」分開。本案沿用此形狀，但 `Confidence` 改為
可解釋的數值（§10），並新增 Evidence 陣列。

**(3) 哪些 R9 概念與本設計衝突？**

| R9 | 本案 | 處置 |
| --- | --- | --- |
| `CRASH` 是 primary regime | 五態沒有 CRASH | **降級為正交旗標**（同 HighVolatility），不佔 regime 名額 |
| `RECOVERY` 是 primary regime | 五態沒有 RECOVERY | 同上，或直接併入 `BULL_PULLBACK` 的 Reasons |
| 沒有 `BULL_PULLBACK` | 需要 | 新增：主趨勢多頭但短期修正 |
| 沒有 `DISTRIBUTION` | 需要，且是重點 | 新增：指數仍強但內部惡化 |
| 只有 price/breadth 兩個維度 | 還要 futures/cash/margin | R9 決策表成為 **Structure 維度**，法人維度另外進 Evidence |

`CRASH`/`RECOVERY` 正交化是關鍵取捨：它們描述的是**事件**（殺盤、剛落底），不是**結構狀態**。
硬塞進五態會讓 `DISTRIBUTION` 與 `CRASH` 互相搶位。R9 自己對 HighVolatility 已經做過同樣判斷
（「刻意不把 HIGH_VOL 做成互斥 regime」），照同一邏輯處理即可。

**(4) R9 應該如何成為新 Market Dashboard 的一部分？**

R9 = 新架構裡的 **PriceAnalyzer + BreadthAnalyzer + StructureClassifier**。
它不是獨立的一層，而是提供 regime 判斷的**結構骨幹**；法人資料（futures/cash/margin）
提供的是 **確認 / 背離的證據**，負責把 `BULL` 修正成 `DISTRIBUTION`。

```
R9 的 price+breadth 決策表  →  結構初判（BULL / SIDEWAYS / BEAR）
        +
法人 Evidence（futures/cash/margin 背離）  →  修正為 BULL_PULLBACK / DISTRIBUTION
```

**(5) 舊 R9 spec 該更新還是作廢？**

**建議：標為 SUPERSEDED，但保留全文。** 理由：R9 §5（breadth 演算法）、§6（HighVolatility
百分位法）、§7（決策表結構）、§9（regime × R6 setup 解讀表）有實質內容會被新實作直接引用；
作廢會讓後人找不到這些推導。做法比照它現在的開頭：加一段指向本文件的 superseded 註記，
內容不刪。**本次不動它，等你核准。**

---

## 3. Official Data Source Research

> 以下所有回應皆為 2026-08-09 實際請求所得。ROC 日期格式 `1150807` = 民國115年08月07日。

### 3.1 TAIEX 指數

| 項目 | 內容 |
| --- | --- |
| 來源 | TWSE OpenAPI |
| 端點 | `GET https://openapi.twse.com.tw/v1/indicesReport/MI_5MINS_HIST` |
| 格式 | JSON 陣列 |
| 欄位 | `Date`(ROC)、`OpeningIndex`、`HighestIndex`、`LowestIndex`、`ClosingIndex` |
| 實測 | 回傳 **5 筆**（1150803–1150807），**無 date 參數、無歷史** |

補充來源（含成交量與漲跌點）：

| 端點 | 欄位 | 實測 |
| --- | --- | --- |
| `GET /v1/exchangeReport/FMTQIK` | `Date`,`TradeVolume`,`TradeValue`,`Transaction`,`TAIEX`,`Change` | 同樣只有 5 筆 |
| `GET https://www.twse.com.tw/rwd/zh/afterTrading/FMTQIK?date=YYYYMM01&response=json` | `日期,成交股數,成交金額,成交筆數,發行量加權股價指數,漲跌點數` | **可帶日期，回傳該月資料** → 可回補 |

⚠️ **限制**：OpenAPI 全系列**不接受日期參數**，只給最近數日。任何回補都必須走 `rwd` 端點。

### 3.2 Market Breadth（漲跌家數）

| 項目 | 內容 |
| --- | --- |
| 來源 | TWSE RWD |
| 端點 | `GET https://www.twse.com.tw/rwd/zh/afterTrading/MI_INDEX?date=YYYYMMDD&type=MS&response=json` |
| 位置 | `tables[7]`，title = `漲跌證券數合計` |
| 欄位 | `['類型','整體市場','股票']` |
| 實測（20260807） | `上漲(漲停) 4,547(191) / 497(14)`、`下跌(跌停) 7,085(108) / 509(3)`、`持平 917/68`、`未成交 16,639/5`、`無比價 2,288/3` |

⚠️ **重要限制**：
- **必須用「股票」欄，不能用「整體市場」**——後者含權證/ETF，4,547 這種數字會嚴重灌水。
- 漲停/跌停數**內嵌在括號裡**（`497(14)`），需字串解析，不是獨立欄位。
- `tables[]` 陣列位置**不保證穩定**，必須用 `title == "漲跌證券數合計"` 比對，不能寫死 index 7。

❌ **不可用的來源**：`GET /v1/opendata/twtazu_od`（集中市場漲跌證券數統計表）實測
出表日期為 `1150605`（兩個月前）且數字為累計值，**既不即時也非單日**。已排除。

✅ **替代方案（建議主用）**：用 `.cache` 的 1,986 檔日 K 自算
`breadthAboveMA20 / breadthAboveMA60`，如 R9 §5。**零新端點、可完整回補、與 scanner 同源**。
TWSE 的漲跌家數作為**次要 cross-check**（它是「今天漲跌」，與「站上均線比例」是不同語意，兩者互補）。

### 3.3 Foreign Cash Flow（外資買賣金額）

| 項目 | 內容 |
| --- | --- |
| 來源 | TWSE RWD |
| 端點 | `GET https://www.twse.com.tw/rwd/zh/fund/BFI82U?dayDate=YYYYMMDD&type=day&response=json` |
| 欄位 | `['單位名稱','買進金額','賣出金額','買賣差額']`，單位為**元** |
| 實測（20260807） | `外資及陸資(不含外資自營商)`：買 310,339,947,885 / 賣 351,055,691,675 / 淨 **-40,715,743,790** |
| 其他列 | 自營商(自行買賣)、自營商(避險)、投信、外資自營商、合計 |

- `stat` 欄位為 `"OK"`；非交易日回非 OK → 對應 `ErrNoData`（與 `internal/institution` 同慣例）。
- 全部六列都應存進 raw（符合需求「retain other institutional categories」），analyzer 只看外資列。
- 這與既有 `internal/institution` 的 T86 是**不同端點**：T86 是**逐檔**，BFI82U 是**市場總計**。互不重疊。

### 3.4 Margin（融資融券）

| 項目 | 內容 |
| --- | --- |
| 來源 | TWSE RWD |
| 端點 | `GET https://www.twse.com.tw/rwd/zh/marginTrading/MI_MARGN?date=YYYYMMDD&selectType=MS&response=json` |
| 位置 | `tables[0]`，title = `115年08月07日 信用交易統計` |
| 欄位 | `['項目','買進','賣出','現金(券)償還','前日餘額','今日餘額']` |
| 實測（20260807） | `融資(交易單位)` 前日 8,958,261 → 今日 **8,986,438**；`融券(交易單位)` 191,897 → **192,740**；`融資金額(仟元)` 532,804,981 → **537,664,510** |

- **當日變化可直接由「今日餘額 − 前日餘額」得到**，不需自行接續前一日 → 少一次請求、少一個 off-by-one 風險。
- ⚠️ OpenAPI 的 `/v1/exchangeReport/MI_MARGN` 是**逐檔**（實測 1,291 列），**不是**市場彙總，不可混用。

### 3.5 Foreign Futures Positioning（外資期貨未平倉）

核准決策 1 要求「優先使用官方 UTF-8 端點」。**已找到，Big5 因此退出每日路徑。**

#### 主用（每日）— TAIFEX OpenAPI，UTF-8 JSON

| 項目 | 內容 |
| --- | --- |
| 端點 | `GET https://openapi.taifex.com.tw/v1/MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate` |
| 目錄 | `https://openapi.taifex.com.tw/swagger.json`（實測 135 個端點） |
| 格式 | **UTF-8 JSON**，英文 key |
| 欄位 | `Date`,`ContractCode`,`Item`,`TradingVolume(Long/Short/Net)`,`OpenInterest(Long)`,`OpenInterest(Short)`,`OpenInterest(Net)`, 及各自契約金額(千元) |
| 篩選 | `ContractCode == "臺股期貨"`、`Item ∈ {外資及陸資, 投信, 自營商}` |
| 實測（20260807 外資及陸資） | `OpenInterest(Long)=8321`、`(Short)=96232`、**`(Net)=-87911`** |
| 交叉驗證 | 與 Big5 CSV 端點**數字完全一致**（-87,911），兩路已互相驗證 |

⚠️ **限制：不接受日期參數。** 實測 `?date=` / `?Date=` / `?queryDate=` 皆被忽略，一律回最新交易日。

#### 備用（僅歷史回補）— Big5 CSV

| 項目 | 內容 |
| --- | --- |
| 端點 | `POST https://www.taifex.com.tw/cht/3/futContractsDateDown` |
| 表單 | `queryStartDate=YYYY/MM/DD&queryEndDate=YYYY/MM/DD&commodityId=TXF` |
| 格式 | CSV 15 欄，**Big5**（實測 utf-8 解碼失敗、big5/cp950 成功） |
| 日期區間 | ✅ 實測可用：8/03 −90,038、8/04 −87,858、8/05 −87,199、8/06 −89,383、8/07 −87,911 |

**結論與相依性**：

```
每日抓取  → OpenAPI JSON（UTF-8）→ 零新相依
歷史回補  → Big5 CSV            → 需要 golang.org/x/text/encoding/traditionalchinese
```

`x/text` 的引入**限縮在回補指令內**（`cmd/market-backfill`），每日路徑與 analyzer 完全不碰。
一次回補 252 個交易日之後，後續全靠自己累積的 snapshot，不再需要該端點。
這符合決策 1「genuinely required 才用」——它確實是取得歷史百分位的唯一官方途徑。

### 3.6 官方資料缺口（誠實回報，不用第三方頂替）

| 需求 | 官方 TWSE/TAIFEX 是否提供 | 處置 |
| --- | --- | --- |
| VIX / 恐慌指數 | ❌ 無台灣官方來源（TAIFEX 有台指選擇權波動率指數 VIX，但不在本次已驗證範圍） | **MVP 移除此模組** |
| 美元指數 DXY / USD-TWD | ❌ 非證交所/期交所業務 | **MVP 移除 Currency 模組** |
| 選擇權 Put/Call Ratio | 🔶 TAIFEX 有，但本次未驗證端點 | 移出 MVP，列 Phase 2 |
| 族群 Cycle Score / News Score | 🔶 repo 內部已有，但非本 MVP 範圍（§3 明文排除 news） | 移出 MVP |

---

## 4. Proposed MVP Architecture

```
                TWSE RWD                    TAIFEX
        ┌───────────┴───────────┐              │
   BFI82U      MI_MARGN     MI_INDEX*     futContractsDateDown
        └───────────┬───────────┘              │
                    ▼                          ▼
              provider/twse.go           provider/taifex.go
                    └────────────┬─────────────┘
                                 ▼
                          model.RawSnapshot          ← 只有數字，沒有判斷
                                 │
              .cache（0050 + universe）──┐
                                 ▼      ▼
                             analyzer/*.go
              price · breadth · futures · cash · margin
                                 │
                                 ▼
                       []model.Evidence               ← 標準化證據
                                 │
                    ┌────────────┼────────────┐
                    ▼            ▼            ▼
                 score.go   structure.go  confidence.go
                    │            │            │
                 Score 0-100   Regime      Confidence
                    └────────────┼────────────┘
                                 ▼
                          model.Snapshot
                                 ▼
                   service/storage.go → data/market/YYYY-MM-DD.json
```

`*MI_INDEX` breadth 為**選用 cross-check**；主 breadth 來自 `.cache`。

**刻意不做的抽象**：沒有 plugin registry、沒有 DI 容器、沒有 event bus。
Provider 是一個介面、Analyzer 是一組純函式、Engine 是三個純函式。

---

## 5. Core Models

沿用既有 `internal/market/model` 的檔案切分。**新增**：

```go
// ── evidence.go（新）────────────────────────────────────────────────
// Direction/Strength 是新概念，repo 既有 enum（scanner.Action / WatchAction /
// news.SignalType / etfflow.FlowSignal）都綁定各自語境，不適合共用。
type Direction string
const (
    DirBullish Direction = "BULLISH"
    DirNeutral Direction = "NEUTRAL"
    DirBearish Direction = "BEARISH"
)

type Strength string
const (
    StrengthWeak     Strength = "WEAK"
    StrengthModerate Strength = "MODERATE"
    StrengthStrong   Strength = "STRONG"
    StrengthExtreme  Strength = "EXTREME"
)

// Evidence 是 analyzer 的唯一輸出。Metrics 保留產生此判斷的原始數字，
// 供日後回測驗證「哪個 analyzer 真的有用」。
type Evidence struct {
    Source     string             `json:"source"`      // "Price" | "Breadth" | ...
    Direction  Direction          `json:"direction"`
    Strength   Strength           `json:"strength"`
    Confidence float64            `json:"confidence"`  // 0..1，此單一 analyzer 的自信程度
    Summary    string             `json:"summary"`     // 中文一句話佐證
    Metrics    map[string]float64 `json:"metrics"`     // 原始數字，key 穩定不可改名
    Available  bool               `json:"available"`   // false = MISSING，非 NEUTRAL
    Reason     string             `json:"reason,omitempty"` // Available=false 時的原因
}
```

```go
// ── raw.go（新）─────────────────────────────────────────────────────
// Provider 的產出。只有數字與來源資訊，沒有任何 bullish/bearish 判斷。
type RawSnapshot struct {
    Date      string    `json:"date"`
    FetchedAt time.Time `json:"fetched_at"`

    Index    *IndexData    `json:"index,omitempty"`
    Breadth  *BreadthData  `json:"breadth,omitempty"`
    Cash     *CashData     `json:"cash,omitempty"`
    Margin   *MarginData   `json:"margin,omitempty"`
    Futures  *FuturesData  `json:"futures,omitempty"`

    Sources  []SourceMeta  `json:"sources"`  // 每個來源的 URL / 狀態 / 取得時間
}

// nil pointer = MISSING。這是「缺資料」與「中性」在型別層次的區分。

type FuturesData struct {
    Date      string  `json:"date"`
    LongOI    float64 `json:"long_oi"`     // 多方未平倉口數
    ShortOI   float64 `json:"short_oi"`    // 空方未平倉口數
    NetOI     float64 `json:"net_oi"`      // 多空未平倉口數淨額（官方直接給，不自算）
    // DailyChange 不放這裡 —— 它是 analyzer 由歷史推導的，不是 provider 的原始欄位。
}

type CashData struct {
    ForeignBuy  float64          `json:"foreign_buy"`   // 元
    ForeignSell float64          `json:"foreign_sell"`
    ForeignNet  float64          `json:"foreign_net"`
    Others      map[string]int64 `json:"others"`        // 投信/自營/合計 全留
}

type MarginData struct {
    MarginBalanceLots  float64 `json:"margin_balance_lots"`   // 融資今日餘額（交易單位）
    MarginPrevLots     float64 `json:"margin_prev_lots"`      // 融資前日餘額
    MarginBalanceKTWD  float64 `json:"margin_balance_ktwd"`   // 融資金額（仟元）今日
    MarginPrevKTWD     float64 `json:"margin_prev_ktwd"`
    ShortBalanceLots   float64 `json:"short_balance_lots"`    // 融券今日餘額
    ShortPrevLots      float64 `json:"short_prev_lots"`
}

type BreadthData struct {
    Advancing  int `json:"advancing"`    // 股票欄，非整體市場
    Declining  int `json:"declining"`
    Unchanged  int `json:"unchanged"`
    LimitUp    int `json:"limit_up"`
    LimitDown  int `json:"limit_down"`
}
```

```go
// ── structure.go 的型別（新）─────────────────────────────────────────
// Benchmark 明示「我們拿什麼當市場代表」。核准決策 2：MVP 用 0050，但絕不標成 TAIEX。
type Benchmark string
const (
    Benchmark0050  Benchmark = "0050"   // MVP 預設：ETF proxy，已在 .cache，有 487 根歷史
    BenchmarkTAIEX Benchmark = "TAIEX"  // 未來：真加權指數，需 rwd 逐月回補
)

// PrimaryStructure 是「價格結構」，只由 benchmark 價格決定，不看廣度、不看法人、不看 score。
type PrimaryStructure string
const (
    StructureStrongUptrend PrimaryStructure = "STRONG_UPTREND"
    StructureUptrend       PrimaryStructure = "UPTREND"
    StructureRange         PrimaryStructure = "RANGE"
    StructureWeakening     PrimaryStructure = "WEAKENING"
    StructureDowntrend     PrimaryStructure = "DOWNTREND"
    StructureUnknown       PrimaryStructure = "UNKNOWN"
)

// BreadthQuality 是「廣度品質」，NARROWING 專指「指數在高檔但廣度未跟上」的背離。
type BreadthQuality string
const (
    BreadthHealthy    BreadthQuality = "HEALTHY"
    BreadthCooling    BreadthQuality = "COOLING"
    BreadthNarrowing  BreadthQuality = "NARROWING"
    BreadthCollapsing BreadthQuality = "COLLAPSING"
    BreadthUnknown    BreadthQuality = "UNKNOWN"
)

// InstitutionalPosture 彙整 futures/cash/margin 三個 Evidence 的方向。
type InstitutionalPosture string
const (
    PostureSupportive    InstitutionalPosture = "SUPPORTIVE"
    PostureNeutral       InstitutionalPosture = "NEUTRAL"
    PostureDeteriorating InstitutionalPosture = "DETERIORATING"
    PostureUnknown       InstitutionalPosture = "UNKNOWN"
)

// StructureView 是第一、二層的完整輸出，連同推導用的原始量一起存進 snapshot，
// 事後才有辦法回答「是哪一層判錯」。
type StructureView struct {
    Benchmark  Benchmark            `json:"benchmark"`
    Structure  PrimaryStructure     `json:"structure"`
    Breadth    BreadthQuality       `json:"breadth_quality"`
    Posture    InstitutionalPosture `json:"institutional_posture"`

    // 決策表用到的述詞，全部外顯，不藏在函式內部。
    TrendIntact       bool `json:"trend_intact"`
    PriceResilient    bool `json:"price_resilient"`
    OrderlyCorrection bool `json:"orderly_correction"`
    FailedHighs       int  `json:"failed_highs"`

    Metrics map[string]float64 `json:"metrics"` // ma20/ma60/ma120/ma200/ret20/ret60/dd_high60/b20/b60/drop20…
}

// RegimeDecision 記錄「命中第幾條規則」，讓每天的判斷都可追溯到決策表的某一列。
type RegimeDecision struct {
    Regime   Regime        `json:"regime"`
    RuleID   string        `json:"rule_id"`   // "R0".."R11"，對應 §9.5 決策表
    View     StructureView `json:"view"`
    Reasons  []string      `json:"reasons"`
    Caveats  []string      `json:"caveats"`
}
```

```go
// ── snapshot.go（新，取代現有 dashboard.go 的角色）──────────────────
type Snapshot struct {
    Date        string     `json:"date"`
    GeneratedAt time.Time  `json:"generated_at"`
    SchemaVer   int        `json:"schema_version"`   // 供日後遷移

    Score      float64        `json:"score"`          // 0..100（與 Regime 互不傳參）
    Regime     Regime         `json:"regime"`
    RuleID     string         `json:"rule_id"`        // 命中的決策表列，可追溯
    Structure  StructureView  `json:"structure"`      // 第一、二層的完整輸出
    Confidence float64        `json:"confidence"`     // 0..1
    Flags      Flags          `json:"flags"`          // 正交旗標

    Evidence   []Evidence  `json:"evidence"`
    Raw        RawSnapshot `json:"raw"`               // 完整原始值，供回測重算

    Reasons  []string `json:"reasons"`
    Caveats  []string `json:"caveats"`
}

// Flags 是與 regime 正交的布林旗標（承接 R9 的 HighVolatility 設計）。
type Flags struct {
    HighVolatility bool `json:"high_volatility"`
    CrashEvent     bool `json:"crash_event"`   // R9 的 CRASH 降級於此
    RecoveryEvent  bool `json:"recovery_event"`// R9 的 RECOVERY 降級於此
}

type Regime string
const (
    RegimeBull         Regime = "BULL"
    RegimeBullPullback Regime = "BULL_PULLBACK"
    RegimeSideways     Regime = "SIDEWAYS"
    RegimeDistribution Regime = "DISTRIBUTION"
    RegimeBear         Regime = "BEAR"
    RegimeUnknown      Regime = "UNKNOWN"
)
```

**保留原樣**：`RegimeInfo` 的 Description/Strategy/Risk 三段固定文案、文案紅線。

---

## 6. Provider Design

```go
type Provider interface {
    Name() string
    Fetch(ctx context.Context, date string) (*model.RawSnapshot, error)
}
```

兩個實作，各自只管自己的來源：

**`provider/twse.go`** — 三次 GET（BFI82U / MI_MARGN / MI_INDEX），組成 Cash+Margin+Breadth。
**`provider/taifex.go`** — 一次 POST，組成 Futures。

Provider 的職責邊界（嚴格）：

| 做 | 不做 |
| --- | --- |
| HTTP 請求（`NewRequestWithContext` + timeout） | 判斷多空 |
| status code 驗證、`stat != "OK"` → `ErrNoData` | 計算 regime / score |
| 逗號千分位、ROC 日期、`497(14)` 括號拆解 | 決定策略 |
| 欄位數與型別基本驗證 | 補值、內插、猜測 |
| 記錄 `SourceMeta{URL, FetchedAt, Status}` | 因為某欄缺就整包丟棄 |

**交易日驗證**：回應內的日期必須等於請求日期，不符即 `ErrDateMismatch`——TWSE 假日會回
最近交易日，靜默接受會造成錯位。

**HTTP 慣例**：照抄 `internal/institution/twse.go`——stdlib `net/http`、
`http.Client{Timeout}`、context、不引入任何 HTTP 框架、**不自動重試**（repo 現況無重試機制，
不在此案引入新慣例）。

---

## 7. Analyzer Design

每個 analyzer 皆為純函式 `func(...) model.Evidence`，無 I/O、無狀態。

### 7.1 PriceAnalyzer（資料源：`.cache/0050_TW.json`）

輸入 0050 日 K（as-of，只用當日含以前）。輸出 Metrics：
`ma20, ma60, ma120, ma200, ret20, ret60, close_vs_ma20, ma_alignment, realized_vol20, vol_percentile`

- Direction：均線多頭排列 + ret60 > 0 → BULLISH；跌破 MA60/MA120 + ret20 < 0 → BEARISH。
- Strength：由 ret20 幅度與均線乖離決定。
- 同時產出 `HighVolatility`（R9 §6 百分位法，P80/252d，ATR fallback）。

### 7.2 BreadthAnalyzer（主資料源：`.cache` 全 universe；次要：TWSE 漲跌家數）

Metrics：`breadth_above_ma20, breadth_above_ma60, valid_count, adv_decl_ratio, limit_up, limit_down`

- 兩個來源語意不同（「站上均線比例」vs「今日漲跌家數」），**不加總、各自成為 Metrics**。
- Direction 主要由 `breadth_above_ma20` 決定；TWSE 漲跌家數作為佐證與背離偵測。
- 有效家數過少 → `Available=false`（MISSING），不是 NEUTRAL。

### 7.3 ForeignFuturesAnalyzer（§13 的三分法）

**必須拆成三個獨立面向，不可合成單一 bullish/bearish：**

| 面向 | Metric | 說明 |
| --- | --- | --- |
| **Position** | `net_oi` | 當前淨部位（-87,911） |
| **Change** | `net_oi_change_1d`, `_5d` | 增空 / 減空 / 加多 / 平倉——**方向的變化比絕對值重要** |
| **Crowding** | `net_oi_percentile_252d` | 相對近一年的極端程度 |

Evidence 產出範例（三者可並存且看似矛盾，這是刻意的）：

```
Direction:  BEARISH        （淨空單）
Strength:   EXTREME        （252 日百分位第 3 百分位）
Summary:    外資淨空 87,911 口（近一年最空的 3%），但單日回補 1,472 口
Metrics:    net_oi=-87911, net_oi_change_1d=+1472, net_oi_percentile_252d=3
```

Crowding 高時**降低該 evidence 的 Strength 權重**並在 Caveats 註明「極端部位具軋空風險」，
落實你 §13 的「Extreme Short ≠ 直接 Bearish」。

歷史百分位需要 252 日 futures 歷史 → **M2 階段先做回補**（TAIFEX 日期區間查詢已驗證可用）。

### 7.4 ForeignCashAnalyzer（§14 的滾動視窗）

Metrics：`net_1d, net_5d, net_20d, net_5d_direction, net_20d_direction`

- **不以單日定調**。判斷主軸為 5D/20D，1D 只作為「近期轉折」佐證。
- 實測案例：8/07 單日 −407 億，但若 5D/20D 為正，Direction 不得判 BEARISH，
  只在 Summary 註明「單日轉賣超，但 5 日仍淨買超」。
- 20D 需要 20 個交易日回補 → M2 一併處理。

### 7.5 MarginAnalyzer（§15 的脈絡化）

Metrics：`margin_balance, margin_chg_1d, margin_chg_5d, short_balance, short_chg_1d, index_ret_1d, index_ret_5d`

判斷矩陣**以 5 日為主**（單日雜訊過大）：

| 指數 | 融資 | 解讀 | Direction |
| --- | --- | --- | --- |
| ↑ | ↓ | 籌碼健康，散戶未追價 | BULLISH |
| ↓ | ↑ | 散戶攤平／套牢 | BEARISH |
| ↑ | ↑ | 追價，需留意 | NEUTRAL（Caveat） |
| ↓ | ↓ | 去槓桿 | NEUTRAL |

四格對應的門檻全部進 config，標記 **BASELINE, NOT CALIBRATED**。

---

## 8. Market Score Design

### 8.1 建議權重（與你的範例不同，附理由）

| 模組 | 你的範例 | **建議** | 理由 |
| --- | --- | --- | --- |
| Price / Breadth | 30% | **35%** | 唯一有 487 根完整歷史、可立即回測驗證的維度；其餘三者上線時歷史為零 |
| Foreign Futures | 25% | **20%** | 與 Foreign Cash 同為「外資」，兩者相關性高；合計 45% 已足夠，原 50% 等於讓單一參與者主導過半分數 |
| Foreign Cash | 25% | **25%** | 台股外資現貨買賣超與指數同向性最高，維持 |
| Margin | 20% | **20%** | 維持 |

**核心理由**：Futures 與 Cash 是同一群參與者的兩個面向，不是獨立證據。把 Price/Breadth
（市場本身的實際行為）提高到 35%，可避免「外資做什麼」壓過「市場實際發生什麼」。

所有權重進 config，可調。**標記 BASELINE, NOT CALIBRATED**——真正的權重應該等
§12 的歷史快照累積夠久後，用「Score > 70 之後 TAIEX 5/10/20 日表現」回測決定。

### 8.2 Evidence → Score 的映射

```
每個 Evidence 先轉成 0..100 的 subScore：
    BULLISH  + WEAK/MODERATE/STRONG/EXTREME → 60 / 70 / 82 / 92
    NEUTRAL                                  → 50
    BEARISH  + WEAK/MODERATE/STRONG/EXTREME → 40 / 30 / 18 /  8

Score = Σ(subScore × weight × evidenceConfidence) / Σ(weight × evidenceConfidence)
        僅計入 Available == true 的 evidence
```

`evidenceConfidence` 讓「資料完整但訊號模糊」的 analyzer 自動降低影響力，
不需要另設一套 override 規則。

---

## 9. Market Regime Design（修訂版 — 三層結構式，Score 完全不參與）

> 本節為 2026-08-09 核准決策 7 的重寫。舊版「price/breadth → BULL/SIDEWAYS/BEAR，
> 再用 deteriorationCount ≥ 2 → DISTRIBUTION」已作廢：它把 `BULL_PULLBACK` 當成殘差
> 處理，無法與 `DISTRIBUTION` 結構性區分。

### 9.1 三層設計

```
第一層  PrimaryStructure   ← 只看 benchmark 價格結構（0050 proxy）
        STRONG_UPTREND / UPTREND / RANGE / WEAKENING / DOWNTREND / UNKNOWN

第二層  BreadthQuality     ← 只看市場廣度品質
        HEALTHY / COOLING / NARROWING / COLLAPSING / UNKNOWN
        InstitutionalPosture ← 只看法人證據（futures + cash + margin）
        SUPPORTIVE / NEUTRAL / DETERIORATING / UNKNOWN

第三層  MarketRegime       ← 由上述三者經「明示決策表」推導
        BULL / BULL_PULLBACK / SIDEWAYS / DISTRIBUTION / BEAR / UNKNOWN
```

三層都是**離散列舉**，每一層都可以獨立寫 table-driven 測試，也都會存進 snapshot，
所以事後可以回頭問「是哪一層判錯」。

### 9.2 第一層：PrimaryStructure（只用 benchmark 價格）

輸入全部來自 `.cache` 的 benchmark（MVP = 0050），as-of，不看未來 bar。
`ddHigh60` = 距 60 日最高收盤的回檔幅度。

| # | PrimaryStructure | 條件（第一個命中者勝） |
| --- | --- | --- |
| 1 | `UNKNOWN` | 歷史 < 200 bars（MA200 算不出來） |
| 2 | `DOWNTREND` | `close < MA60` **且** `close < MA120` **且** `MA60 下彎` **且** `ret20 < 0` |
| 3 | `WEAKENING` | `close < MA20` **且** `close < MA60` **且** `ret20 < 0` **且** `close > MA120`（長線支撐未破） |
| 4 | `STRONG_UPTREND` | `close > MA20 > MA60 > MA120`（完全多頭排列）**且** `MA60 上揚` **且** `ret60 > 0` **且** `ddHigh60 ≤ 3%` |
| 5 | `UPTREND` | `close > MA60` **且** `MA60 上揚` **且** `ret60 > 0` |
| 6 | `RANGE` | 其餘（MA 糾結、`|ret60|` 小、在 MA60 上下震盪） |

`WEAKENING` 的定位：**中期結構已破、長期支撐仍在**——它是 `RANGE`／`UPTREND` 與
`DOWNTREND` 之間的過渡帶，也是 `BULL_PULLBACK` 最常出現的位置。

### 9.3 第二層之一：BreadthQuality

`b20` = universe 中收盤站上自身 MA20 的比例；`drop20` = `b20` 相對其近 20 個交易日
最高值的下滑百分點；`nearHigh` = `ddHigh60 ≤ 3%`。

| # | BreadthQuality | 條件（第一個命中者勝） |
| --- | --- | --- |
| 1 | `UNKNOWN` | 有效家數 < 500 |
| 2 | `COLLAPSING` | `b20 < 30%` |
| 3 | `NARROWING` | （`b20 < 45%` **且** `nearHigh`）← **背離特徵**　**或**　`drop20 ≥ 20pp` |
| 4 | `HEALTHY` | `b20 ≥ 55%` **且** `drop20 < 10pp` |
| 5 | `COOLING` | 其餘（`b20 ∈ [40%,55%)`，或 `drop20 ∈ [10,20)pp` 但 `b20 ≥ 40%`） |

`NARROWING` 的第一個條件就是「指數還在高點附近、但站上均線的股票只剩不到一半」——
這正是你要的 leadership narrowing，而且它是**價格與內部的比較**，不是單一門檻。

### 9.4 第二層之二：InstitutionalPosture

由三個 Evidence（ForeignFutures / ForeignCash / Margin）的 `Direction` 彙整：

| # | InstitutionalPosture | 條件 |
| --- | --- | --- |
| 1 | `UNKNOWN` | 可用 evidence < 2 |
| 2 | `DETERIORATING` | ≥2 個 `BEARISH`　**或**　（外資期貨淨空 5 日持續增加 **且** 外資現貨 5 日淨賣超） |
| 3 | `SUPPORTIVE` | ≥2 個 `BULLISH` **且** 無 `BEARISH+STRONG/EXTREME` |
| 4 | `NEUTRAL` | 其餘 |

第 2 條的括號部分是**單獨成立的觸發條件**：外資期貨與現貨同步轉空，即使 margin 中性，
也算 deterioration——這是台股最典型的出貨組合。

### 9.5 第三層：公開 Regime 決策表（明示、有序、第一個命中者勝）

輔助述詞（全部走 config，標記 BASELINE, NOT CALIBRATED）：

```
trendIntact       = close > MA120  且  MA60 上揚          （主趨勢未破）
priceResilient    = ddHigh60 ≤ 5%  或  close > MA60        （價格看起來還撐得住）
orderlyCorrection = 3% ≤ ddHigh60 ≤ 15%  且  近 20 日無單日跌幅 > 4%
failedHighs       = 近 20 日內「觸及 60 日高點後 3 日內跌破 MA20」的次數
```

| # | Structure | Breadth | Institutions | 額外條件 | → Regime |
| --- | --- | --- | --- | --- | --- |
| **R0** | `UNKNOWN` | — | — | — | `UNKNOWN` |
| **R1** | `DOWNTREND` | any | **any** | — | **`BEAR`** |
| **R2** | `WEAKENING` | `COLLAPSING` | any | — | `BEAR` |
| **R3** | `STRONG_UPTREND` / `UPTREND` / `RANGE` / `WEAKENING` | `NARROWING` 或 `COLLAPSING` | any | `priceResilient` | **`DISTRIBUTION`** |
| **R4** | `STRONG_UPTREND` / `UPTREND` / `RANGE` | `COOLING` | `DETERIORATING` | `priceResilient` | `DISTRIBUTION` |
| **R5** | `UPTREND` / `RANGE` | any | `DETERIORATING` | `failedHighs ≥ 2` | `DISTRIBUTION` |
| **R6** | `STRONG_UPTREND` | `HEALTHY` / `COOLING` | ≠ `DETERIORATING` | — | **`BULL`** |
| **R7** | `UPTREND` | `HEALTHY` | `SUPPORTIVE` / `NEUTRAL` | `ddHigh60 < 3%` | `BULL` |
| **R8** | `UPTREND` / `WEAKENING` | `HEALTHY` / `COOLING` | ≠ `DETERIORATING` | `trendIntact` **且** `orderlyCorrection` | **`BULL_PULLBACK`** |
| **R9** | `RANGE` | `HEALTHY` / `COOLING` | ≠ `DETERIORATING` | — | `SIDEWAYS` |
| **R10** | `WEAKENING` | any | any | `trendIntact` | `BULL_PULLBACK` |
| **R11** | （fallback） | — | — | — | `SIDEWAYS` |

### 9.6 這張表如何滿足你的三個硬性要求

**(a) 法人惡化不得把 DOWNTREND 翻成 DISTRIBUTION**

`R1` 排在所有 `DISTRIBUTION` 規則（R3/R4/R5）**之前**，且條件裡完全沒有 Institutions 欄位
（標示為 `any`）。結構是空頭，就是 `BEAR`，法人證據只能進 `Reasons`，改不了結論。
這一條會寫成不變量測試：**任何 `DOWNTREND` 輸入，不論法人證據怎麼組合，輸出恆為 `BEAR`。**

**(b) BULL_PULLBACK 與 DISTRIBUTION 必須結構性可分**

兩者的條件在**價格**與**廣度**兩軸上互斥，不是靠計數器分高下：

| | BULL_PULLBACK（R8/R10） | DISTRIBUTION（R3/R4/R5） |
| --- | --- | --- |
| 價格 | **已經實際修正**：`orderlyCorrection`（回檔 3–15%） | **還沒真正修正**：`priceResilient`（回檔 ≤5% 或仍在 MA60 上） |
| 廣度 | `HEALTHY` / `COOLING`（降溫但未崩） | `NARROWING` / `COLLAPSING`（或 `COOLING` + 法人惡化） |
| 法人 | ≠ `DETERIORATING` | `DETERIORATING`，或廣度已足夠糟到不需要法人佐證 |
| 主趨勢 | `trendIntact` 為必要條件 | 不要求（結構可以還很漂亮，這正是危險之處） |
| 一句話 | 價格跌了、內部還健康 | 價格沒跌、內部先壞了 |

**兩者不可能同時命中**：R8 要求 `ddHigh60 ≥ 3%` 且廣度非 NARROWING；
R3 要求廣度 NARROWING/COLLAPSING。條件交集為空，且 R3 順位在前。

**(c) 不得用 Market Score 門檻**

整張表沒有任何一格引用 `Score`。`Score` 與 `Regime` 由**不同函式**產生，
彼此不互相傳參——`structure.go` 的簽章裡根本收不到 score，型別層次即禁止。
會有一條測試：`Score` 從 0 掃到 100，固定其餘輸入，`Regime` 必須恆定不變。

### 9.7 正交旗標（承接 R9，不佔 regime 名額）

```
HighVolatility  realizedVol20 ≥ P80(過去 252 日分佈)，ATR 絕對門檻為 fallback
CrashEvent      benchmark ret20 ≤ −8% 且 b20 < 30%          （R9 的 CRASH）
RecoveryEvent   前 N 日曾 CrashEvent，近 5 日重新站回 MA20    （R9 的 RECOVERY）
```

## 10. Confidence Design

輸出 0..1，由**三個可解釋的因子相乘**，每個因子都能單獨印出來給你看：

```
Confidence = completeness × agreement × conviction

completeness = Σ(可用 evidence 的權重) / Σ(全部權重)
               → 5 個全到 = 1.00；缺 Margin(20%) = 0.80

agreement    = 1 - (方向分歧度)
               把每個 evidence 的 Direction 映到 +1/0/-1，取加權標準差正規化
               → 全部同向 = 1.00；兩兩對立 = 接近 0.3

conviction   = 加權平均的 evidenceConfidence × Strength 係數
               → 全 EXTREME = 1.00；全 WEAK = 0.5
```

你 §17 的兩個例子驗算：

| 情境 | completeness | agreement | conviction | Confidence |
| --- | --- | --- | --- | --- |
| Price/Breadth/Cash/Futures 全 bullish | 1.00 | 0.95 | 0.88 | **0.84** |
| Price bull、Breadth bear、Cash 中性、Futures bear | 1.00 | 0.42 | 0.72 | **0.30** |

**不假裝精準**：Confidence 一律無條件捨去到小數第二位；當 `completeness < 0.6` 時，
Confidence 上限強制壓在 0.5，並在 Caveats 明列缺哪個來源。

---

## 11. Missing Data / Failure Behavior

**核心原則：MISSING ≠ NEUTRAL。** 型別層次落實——`RawSnapshot` 各欄為 pointer，
`nil` 即缺；`Evidence.Available=false` 即缺，**不產生 subScore，直接排除在加權之外**，
而不是給 50 分。

| 狀況 | 行為 |
| --- | --- |
| 單一端點逾時 / 500 | 該 evidence `Available=false`，其餘照常；`completeness` 下降 → Confidence 下降 |
| 假日 / 無交易資料 | provider 回 `ErrNoData`；整份 snapshot 不產生，**不寫檔** |
| 回應日期 ≠ 請求日期 | `ErrDateMismatch`，視同該來源缺（絕不接受錯位資料） |
| 部分欄位缺（如融券缺） | 該 Metric 缺，evidence 仍可產生但 `Confidence` 打折並記 Caveat |
| `.cache` 歷史不足 | Price/Breadth `Available=false` → 因為 regime Step 1 只靠它們，**regime 直接 UNKNOWN** |
| 可用 evidence < 2 | regime = UNKNOWN，Score 仍計算但標記 `low_completeness` |

**絕不做的事**：補零、內插、沿用昨日值頂替今日、把缺資料當中性。

---

## 12. Historical Storage

`data/market/YYYY-MM-DD.json`，一日一檔，atomic temp+rename（照 `internal/institution/snapshot.go`）。

**存什麼**：不只最終分數，而是**足以離線重算的全部素材**——

```
Snapshot{
  score, regime, confidence, flags,     ← 結論
  evidence[]  { direction, strength, confidence, metrics{...} },   ← 每個 analyzer 的判斷 + 原始數字
  raw{ index, breadth, cash, margin, futures, sources[] },         ← provider 原始值
  schema_version                                                    ← 供遷移
}
```

這樣設計是為了直接回答你 §18 列的四個問題：

| 你想問的 | 靠哪個欄位 |
| --- | --- |
| Score > 70 之後 TAIEX 5/10/20 日如何？ | `score` + 既有 `.cache` 的 0050/TAIEX 前推報酬 |
| regime = DISTRIBUTION 後有多常下跌？ | `regime` + 前推報酬 |
| 哪個 Analyzer 歷史上真的有用？ | `evidence[].direction` 逐一對前推報酬做命中率 |
| 哪些訊號是假警報？ | `evidence[].metrics` 保留原始數字，可重跑不同門檻而**不需重新抓資料** |

**關鍵設計**：因為 `raw` 與 `metrics` 都留著，日後調整權重或門檻可以**離線重算整段歷史**，
不必重新請求 TWSE/TAIFEX——這與 R6 用 `.cache` 離線回測是同一個思路。

**回補**：M2/M3 各寫一支一次性回補指令，把 TAIFEX（日期區間可查）與 TWSE（逐日）
往回抓 252 個交易日，讓百分位與滾動視窗一上線就有脈絡。

---

## 13. Files To Add

```
internal/market/model/
    evidence.go          Direction / Strength / Evidence
    raw.go               RawSnapshot / IndexData / BreadthData / CashData / MarginData / FuturesData / SourceMeta
    structure.go         Benchmark / PrimaryStructure / BreadthQuality / InstitutionalPosture
                         / StructureView / RegimeDecision
    snapshot.go          Snapshot / Flags

internal/market/provider/
    twse.go              BFI82U + MI_MARGN + MI_INDEX（RWD，UTF-8 JSON）
    twse_test.go
    taifex.go            OpenAPI JSON（UTF-8，每日路徑，零新相依）
    taifex_test.go
    testdata/
        bfi82u_20260807.json
        mi_margn_20260807.json
        mi_index_20260807.json
        taifex_major_20260807.json

internal/market/analyzer/
    price.go             → Evidence + PrimaryStructure 的原始量
    breadth.go           → Evidence + BreadthQuality 的原始量
    futures.go（重寫）    → position / change / crowding 三分法
    cash.go（重寫）       → 1D / 5D / 20D 滾動視窗
    margin.go（重寫）     → 以 5 日為主的四格矩陣
    evidence.go          Evidence → subScore 映射
    structure.go         §9.2/§9.3/§9.4 三個分類器
    regime.go（重寫）     §9.5 決策表（取代現行分數門檻版）
    confidence.go        §10 三因子
    （各檔皆配 _test.go，table-driven）

internal/market/service/
    history.go           讀近 N 日 snapshot，供百分位與滾動視窗

cmd/market-fetch/main.go      每日抓取 → snapshot
cmd/market-backfill/main.go   一次性回補（**唯一使用 golang.org/x/text 的地方**）
```

## 14. Files To Modify

核准決策 4：**原地重構，不砍掉重寫。**

| 檔案 | 改什麼 | 為什麼 |
| --- | --- | --- |
| `internal/market/analyzer/regime.go` | 刪除分數門檻分類，改為 §9.5 決策表 | §10 禁止 score→regime |
| `internal/market/analyzer/score.go` | 改吃 `[]Evidence` 而非 `[]ModuleScore` | 導入 Evidence 層 |
| `internal/market/model/regime.go` | 五態改為 `BULL/BULL_PULLBACK/SIDEWAYS/DISTRIBUTION/BEAR`；`Confidence` 由 enum 改 `float64` | 對齊規格 |
| `internal/market/model/config.go` | 移除 Options/Currency/VIX 權重；`RegimeThresholds` 由分數門檻改為結構門檻（MA/廣度/回檔）；新增 `Benchmark` 設定 | MVP 僅 TWSE/TAIFEX |
| `internal/market/model/module.go` | 移除 `ModuleOptions/Currency/VIX` 常數 | 同上 |
| `internal/market/model/dashboard.go` | `Dashboard` → `Snapshot`；Sector/Opportunity/Risk 型別**保留但移出 MVP 流程**（Phase 2 再接） | 不刪既有資產 |
| `internal/market/service/service.go` | 改為 provider → raw → analyzer → evidence → engine | 新流程 |
| `internal/market/service/storage.go` | 改存 `Snapshot`；加 atomic temp+rename | 對齊 `internal/institution/snapshot.go` |
| `internal/market/provider/provider.go` | 介面改為 `Fetch(ctx, date) (*RawSnapshot, error)` | Provider 只出原始資料 |
| `internal/market/provider/mock/` | **保留**，改為產生 `RawSnapshot` | 決策 4：保留 mock 基礎設施 |
| `cmd/market-dump/main.go` | 改讀真實 snapshot，mock 降為 `--mock` 選項 | 保留離線 demo 能力 |
| `configs/config.yaml` | 新增 `market:` 區塊（`enabled: false` / `show: false`） | 兩層開關慣例 |
| `go.mod` | 加 `golang.org/x/text`（僅 backfill 用） | 決策 1 |

**移出 MVP（檔案保留，不接進流程）**：`analyzer/{options,currency,vix,sector}.go`。
理由：它們的資料源不在 TWSE/TAIFEX 範圍內（§3.6），但程式碼本身沒錯，
Phase 2 接上 Put/Call、族群資料後可直接復用。刪掉等於白扔。

**保證不碰**：`internal/scanner/`、`internal/report/`、`cmd/scanner/`、`internal/r6backtest/`、
`internal/news/`、`internal/institution/`、`internal/etfflow/`、所有評分與 BUY/WATCH/SELL 邏輯。

## 15. Implementation Phases

| 階段 | 內容 | 完成時可驗證什麼 | 相依 | 網路 |
| --- | --- | --- | --- | --- |
| **M1** | **models + Evidence 契約 + PrimaryStructure/MarketRegime 型別 + Snapshot + storage + config** | 型別編譯過、schema 定案、快照可存取、既有測試全綠 | — | 無 |
| **M2** | Price + Breadth analyzer（吃 `.cache`）+ §9.2/§9.3 分類器 | **PrimaryStructure 與 BreadthQuality 可用 487 根歷史全段回測** | M1 | 無 |
| **M3** | §9.5 regime 決策表 + 不變量測試 | **DOWNTREND→BEAR 不可翻轉、BULL_PULLBACK/DISTRIBUTION 互斥、Score 掃 0–100 regime 不變** | M2 | 無 |
| **M4** | TWSE provider（BFI82U / MI_MARGN / MI_INDEX）+ testdata 測試 | 真實欄位解析正確 | M1 | 有 |
| **M5** | TAIFEX provider（OpenAPI JSON）+ `cmd/market-backfill`（Big5，x/text） | 外資期貨每日 + 252 日歷史落地 | M1 | 有 |
| **M6** | Futures / Cash / Margin analyzer + InstitutionalPosture | 三個法人維度的 Evidence | M4, M5 |無 |
| **M7** | Engine：score + confidence（regime 已於 M3 完成） | Score / Regime / Confidence 三者齊備 | M3, M6 | 無 |
| **M8** | `cmd/market-fetch` 每日流程 + 端到端測試 | 每日快照自動產生 | M7 | 有 |
| **M9** | （另議）報告呈現 | — | M8 | — |

**與初版的順序差異**：把 **regime 決策表提前到 M3**，在任何法人資料進來之前就做完。

理由：`PrimaryStructure`（M2）與決策表（M3）**完全不需要網路**，而且有兩年歷史可以立刻
全段回測——先確認「結構判斷本身是對的」，再讓法人證據進來微調。反過來做的話，
一旦 regime 判得不對，你分不清是結構邏輯錯還是法人資料錯。
M3 完成時，系統已經有一個**只靠結構、可回測、可用**的 regime 引擎（即 R9 的原始目標）。

每階段獨立 commit、`go test ./...` 綠、`market.enabled: false` 全程不變。

## 16. Open Questions — 已全數裁決（2026-08-09）

| # | 問題 | 裁決 |
| --- | --- | --- |
| 1 | Big5 相依 | 允許 `x/text`，但已找到 UTF-8 官方端點 → **僅回補指令使用** |
| 2 | 0050 vs TAIEX | MVP 用 0050，**明確建模為 proxy**；`Benchmark0050` / `BenchmarkTAIEX` 型別預留 |
| 3 | Breadth 主來源 | **既有 universe 為主**（可回測），TWSE 官方漲跌家數為次要驗證 |
| 4 | 既有 `internal/market/` | **原地重構**，保留 package 切分／provider 契約／config／mock／storage／測試 |
| 5 | R9 | 不刪，標 superseded／已納入；概念餵進 Price/Breadth analyzer 與結構式 regime |
| 6 | Spec 命名 | 採 `docs/SPEC_MARKET_DASHBOARD.md`，不綁 R 編號 |
| 7 | Regime 設計 | 退回重做 → §9 已改為三層結構式決策表 |

### 尚待你點頭才動工的事項

- 本計畫本身（修訂後）是否核准施工。
- M1 的實際檔案清單（見下）是否同意。

---

**本文件為施工計畫，仍未寫任何程式碼。等待 M1 核准。**
