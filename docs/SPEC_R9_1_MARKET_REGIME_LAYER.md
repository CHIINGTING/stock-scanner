# SPEC R9-1 — Market Regime Strategy Layer (Design)

> **⚠️ SUPERSEDED／已納入 — canonical spec 為 `docs/SPEC_MARKET_DASHBOARD.md`，請先讀那份。**
>
> R9 從未實作。**Market Dashboard** 接手「regime」這件事，成為 repo 內**唯一**的 regime 引擎；
> R9 的價格／廣度想法沒有被丟掉，而是成為新設計三層架構裡的**第一、二層結構骨幹**。
> 本檔**全文保留**（核准決策 5，2026-08-09），因為 §5（breadth 演算法）、§6（HighVolatility
> 百分位法）、§7（決策表結構）、§9（regime × R6 setup 解讀表）的推導仍會被實作直接引用，
> 作廢會讓後人找不到這些推導過程。
>
> **哪些 R9 內容被實際採納**（見新 spec §3、§5、§9）：
>
> - **結構式決策表的形式**（明示、有序、第一個命中者勝），而非分數門檻——這是 R9 想對的核心。
> - **0050 作為 benchmark proxy**（新 spec 進一步用 `Benchmark` 型別把「這是 ETF、不是指數」外顯）。
> - **breadth 由既有 universe 自算**（`breadthAboveMA20 / MA60`），不依賴任何新端點。
> - **HighVolatility 用百分位（P80/252d）而非絕對門檻，且與 regime 正交**。
> - **degrade 紀律**：資料不足 → UNKNOWN／降 Confidence，不得 panic；look-ahead 紀律；
>   Reasons/Caveats 中文佐證；文案紅線（不得含買／賣／下單／進場價）；不自動交易。
>
> **哪些被改變**：
>
> - **`CRASH` / `RECOVERY` 由 primary regime 降級為正交旗標 `Flags{CrashEvent, RecoveryEvent}`**。
>   理由：它們描述的是**事件**，不是**結構狀態**；佔用 regime 名額會與 `DISTRIBUTION` 互搶同一天。
>   這正是 R9 自己對 `HighVolatility` 已做過的判斷，新 spec 只是把同一邏輯貫徹到底。
> - **五態改為 `BULL` / `BULL_PULLBACK` / `SIDEWAYS` / `DISTRIBUTION` / `BEAR`（+ `UNKNOWN`）**。
>   新增 R9 沒有的 `BULL_PULLBACK`（主趨勢未破的有秩序修正）與 `DISTRIBUTION`（指數還撐著、
>   內部先壞）。兩者的主要規則（R8 vs R3）條件互斥；殘差規則 R10 則靠決策表順位不重疊
>   （詳見新 spec §7，該處明列了哪些不變量可寫、哪些不可寫）。
> - **`Confidence` 由分級改為可解釋的 0..1 數值**（completeness × agreement × conviction）。
> - R9 只有 price／breadth 兩維；新 spec 另外引入法人維度（futures／cash／margin）作為
>   **Evidence**，負責把 `BULL` 修正成 `DISTRIBUTION`。
>
> 歷史脈絡：`docs/SPEC_R10_MARKET_DASHBOARD.md`（更早一版的需求原文＋舊實作對照，仍保留供追溯）
> **自稱**接手 R9——其開頭寫著「R9 開頭指向的就是這份文件」。但本檔從未指向過 R10。
> canonical spec 現已改為不綁 R 編號的 `docs/SPEC_MARKET_DASHBOARD.md`。

**Status:** Design only. No code in this commit.
**Scope of this doc:** R9-1 detection + the full R9 roadmap (R9-1 → R9-4).
**Owner intent:** 在市場轉弱時，不要讓 scanner 用同一套「超級多頭」邏輯解讀所有股票。
R9 是一層 **regime 判斷與解讀層**，不是改分數、不是自動交易。

> ~~這份文件是 R9 的 source of truth。實作前先讀這裡；改設計先改這裡。~~
> **（已失效，2026-08-09）** 這句是 R9 撰寫當時的敘述，與本檔頂端的 SUPERSEDED 狀態衝突。
> 現在的 source of truth 是 `docs/SPEC_MARKET_DASHBOARD.md`：**實作前先讀那份，改設計先改那份。**
> 本檔僅供推導追溯，不再更新。
> 與既有 R6 / R7 / R8 SPEC 文件同慣例（`docs/SPEC_R6_*`, `docs/SPEC_R8_*`）。

---

## 1. R9 目標與定位

### 1.1 定位

```text
R6 = setup 回測（pullback / VCP / crash survivor 的歷史表現）
R7 = HoldingHorizon shadow（stage + ATR 參考持有區間）
R8 = ETF / 主動資金 / 被動換股背景
R9 = 現在的市場環境下，這些訊號「該怎麼被解讀」
```

R9 回答的問題只有一個：

```text
在目前的 market regime 下，
這個 setup 應該「升級解讀 / 降級解讀 / 只當觀察」？
```

R9 **不**回答「要不要買、要不要賣、幾塊買」。

### 1.2 為什麼要做

目前 scanner 的強勢股邏輯（RS / NewHigh / VCP / MomentumFlow / MTF / R6 pullback / R8 ETF Flow / HorizonHint）都是**牛市假設**下最好用：

| 牛市成立 | 熊市 / 殺盤失真 |
|---|---|
| 強者恆強 | 強股也會補跌 |
| 回檔是機會 | 回檔可能變破底 |
| 突破容易延續 | 突破容易假突破 |
| VCP 容易成功 | VCP 容易失敗 |
| 停損太緊會被洗掉 | 停損保護價值變高 |
| 20d 處置→冷卻→再攻擊循環有參考價值 | pullback 邏輯本身失真 |

所以 R9 的目的是讓 report 明講：**現在這個環境，這些訊號要打幾折看。**

### 1.3 R9 第一版硬性邊界（display / report only）

```text
不改 RocketScore
不改 WatchAction
不改 ExplosionProb
不改 sorting
不改 stop profile 預設
不改 R6 backtest 結論
不改 R8 ETF flow signal
不改 R7 HoldingHorizon / R6-7 HorizonHint
不自動下單、不接 broker、不做交易執行
```

R9 是 **regime 解讀層**，不是自動交易系統。

---

## 2. R9 子階段（R9-1 / R9-2 / R9-3 / R9-4）

| 階段 | 範圍 | 碰資料層? | 顯示? | gating |
|---|---|---|---|---|
| **R9-1** | Regime Detection 引擎：market proxy 抓取 + breadth + volatility + 分類，純算不接任何 downstream | 是（新增 0050 proxy 抓取） | 否（shadow-only，可離線 dump 驗證） | `enable_market_regime` |
| **R9-2** | Regime-Aware Report ⑩ 全域 banner：regime + HighVol + 主要解讀 + 適合/降級 setup + caveat | 否 | 是 | `show_market_regime` |
| **R9-3** | Regime-Aware Setup Interpretation：同一 setup 依 regime 給升/降/觀察文案（VCP / pullback / NewHigh / MTF / R8 / HorizonHint） | 否 | 是 | `show_market_regime` |
| **R9-4** | **未來，本版不做**：regime 才允許影響 risk warning / sorting。需獨立 master flag + 跨 regime 驗證 | — | — | future only |

每階段可獨立 commit、獨立回退。R9-1 不開 `show_market_regime` 時對 report 位元組零影響。

**本文件（R9-1 design commit）不含任何 code。** marketregime package / regime-check cmd / proxy fetcher / report ⑩ template / config flags 全部等這份 doc 通過後才動。

---

## 3. Regime 類型

### 3.1 Primary regime（互斥）

```text
BULL       多頭
SIDEWAYS   盤整
BEAR       空頭
CRASH      殺盤
RECOVERY   復甦
UNKNOWN    資料不足 / 無法判斷
```

### 3.2 HighVolatility（獨立布林，不互斥）

```text
HighVolatility = true / false
```

**刻意不把 HIGH_VOL 做成互斥 regime。** 原因：

```text
regime 是「方向 / 結構」狀態
volatility 是「波動程度」
兩者正交，要能組合出：
  BULL + HighVol / BEAR + HighVol / CRASH + HighVol
```

### 3.3 輸出型別（草案，實作於 R9-1）

```go
type MarketRegimeResult struct {
    PrimaryRegime  string   // BULL / SIDEWAYS / BEAR / CRASH / RECOVERY / UNKNOWN
    HighVolatility bool
    Confidence     string   // LOW / MEDIUM
    Reasons        []string // 命中此 regime 的佐證（中文）
    Caveats        []string // 邊界 / 注意事項（中文）
}
```

`Confidence`：
- 資料充足且條件明確 → **MEDIUM**
- 邊界情況 / 剛翻轉 / RECOVERY（樣本天生少，比照 R6 Setup D crash 處理）→ **LOW**

---

## 4. 0050 primary proxy 設計

### 4.1 為什麼是 0050

R9-1 v1 以 **0050 為唯一 primary market proxy**：

```text
0050 已在 R6 Setup D / crash survivor 用過（internal/r6backtest/regime.go: ProxySymbol = "0050"）
與既有 R6 crash regime 邏輯較容易銜接
```

### 4.2 加權指數 ^TWII 是 optional / future

```text
^TWII 列為 optional cross-check / future extension
R9-1 v1 不強制做 ^TWII
未來若要做：與 0050 交叉確認（背離時降 Confidence / 加 Caveat）
```

### 4.3 proxy 衍生量

從 0050 日 K 計算（用既有 `internal/indicator/ma.go`）：

```text
MA20 / MA60 / MA120 / MA200          均線排列（多頭 / 空頭 / 糾結）
20d return                            短期方向與殺盤判斷
60d return                            中期方向
realizedVol20 / ATR20（見 §6）        HighVolatility
```

### 4.4 look-ahead 紀律

比照 r6backtest 的 as-of 慣例：**每一日只用當日（含）以前的 bar**，不得用未來資料。
proxy 歷史不足（< 需要的最長窗，例如 MA200 → 200+ bars；若只用到 MA120 則 120+）時 → 相關判斷 degrade，Confidence 降 LOW 或直接 UNKNOWN。

---

## 5. breadth 計算

Breadth 衡量「市場廣度」，用既有市場 universe（`fetcher.FetchAll()` 的結果），**不新增任何個股資料源**。

```text
breadthAboveMA20 = (universe 中 Close > 自身 MA20 的家數) / (有效家數)
breadthAboveMA60 = (universe 中 Close > 自身 MA60 的家數) / (有效家數)   # 選用
```

選用補充（v1 可先不做，列 future）：

```text
新高家數 / 新低家數（可重用 NewHigh 的裸函式）
跌破 MA20 / MA60 的比例（= 1 - breadthAbove*）
```

degrade 規則：universe 為空 / 全 nil / 有效家數過少 → breadth 視為無效 → regime 往 UNKNOWN 靠、Confidence 降 LOW。**不得 panic。**

> 註：`internal/r6backtest/regime.go` 的 `BuildRegimePanel` 已有「0050 20d return ≤ 門檻 = crash + breadth-below-MA20 + 事件切段」的歷史版本，可作 R9 演算法藍本；但它是 backtest 套件、binary crash-only、跑歷史 Universe，**R9 不直接複用**，另做 live 多分類版本。

---

## 6. HighVolatility 計算

用 0050 的**已實現波動度百分位**，避免絕對門檻在不同時期失真：

```text
realizedVol20 = stdev(0050 日報酬, 20d) * sqrt(252)      # 年化已實現波動
HighVolatility = realizedVol20 >= P80( 過去 ~252 日的 realizedVol20 分佈 )
                 OR  (ATR20 / Close) >= 絕對上限門檻        # fallback，防歷史太短
```

設計要點：
- **百分位法**自適應市場長期波動水準。
- **絕對門檻**當 fallback（歷史不足 / 分佈無法建立時）。
- 結果放 `MarketRegimeResult.HighVolatility`，與 `PrimaryRegime` **正交**。
- 門檻走 config（`regime_vol_percentile`, `regime_vol_abs_atr_pct`），下列為 baseline：

| 參數 | baseline | 說明 |
|---|---|---|
| `regime_vol_percentile` | 80 | realizedVol20 的高波動百分位門檻 |
| `regime_vol_lookback` | 252 | 建百分位分佈的回看天數 |
| `regime_vol_abs_atr_pct` | （待定） | fallback：ATR20/Close 絕對門檻 |

> **baseline thresholds, not final calibrated parameters** — 之後需實測校準。

---

## 7. Regime 判斷決策表

門檻全部走 config；下表數字為 **v1 baseline 草案，非定案**。優先序由上而下，**第一個命中者勝**。

```text
① 資料不足（proxy 歷史 < 門檻 或 breadth 無效）
   → UNKNOWN

② CRASH（殺盤，最優先）
   0050 20d return <= -8%
   AND breadth 惡化（below MA20 比例 > 70%，即 breadthAboveMA20 < 30%）
   → CRASH

③ BEAR
   0050 < MA60 且 < MA120
   AND 0050 20d return < 0
   AND breadthAboveMA20 < 40%
   → BEAR

④ RECOVERY
   先前 N 日內曾為 CRASH / BEAR
   AND 0050 近 5 日重新站回 MA20
   AND breadthAboveMA20 由低翻升（動能轉正）
   AND 0050 20d return 轉正
   → RECOVERY

⑤ BULL
   0050 > MA20 > MA60（多頭排列）
   AND 0050 60d return > 0
   AND breadthAboveMA20 >= 55%
   → BULL

⑥ SIDEWAYS（其餘）
   0050 在 MA60 上下震盪、20d return 接近 0、breadth 中性
   → SIDEWAYS
```

### 7.1 baseline 門檻表

| Regime | 條件（baseline，非定案） | config keys（草案） |
|---|---|---|
| CRASH | 20d ret ≤ −8% AND breadthAboveMA20 < 30% | `regime_crash_ret20`, `regime_crash_breadth` |
| BEAR | <MA60 且 <MA120 AND 20d ret < 0 AND breadthAboveMA20 < 40% | `regime_bear_breadth` |
| RECOVERY | 前 N 日曾 CRASH/BEAR AND 近 5 日站回 MA20 AND breadth 翻升 AND 20d ret > 0 | `regime_recovery_lookback` |
| BULL | >MA20>MA60 AND 60d ret > 0 AND breadthAboveMA20 ≥ 55% | `regime_bull_breadth` |
| SIDEWAYS | 其餘 | — |

> **全部為 baseline thresholds, not final calibrated parameters。**

### 7.2 Reasons / Caveats 規則

- 每個命中的 regime，往 `Reasons` 塞至少一句中文佐證（例：「0050 跌破 MA60/MA120，20d −6.2%，breadth above-MA20 32%」）。
- 若接近相鄰 regime 邊界（例：差一項就變 BEAR）→ 塞 `Caveats`。
- RECOVERY 一律 Confidence = LOW。

---

## 8. ⑩ Report 全域 banner 顯示方式

### 8.1 版位

```text
⑩ Market Regime / Strategy Context
```

- **全域 banner**：放在「市場掃描」頁最上方、在 ①–⑨ 個股表格之上。
- **不是** per-stock 卡片內容（有別於 ⑨ ETF 是掛在每張卡片）。R9 是全市場單一狀態。

### 8.2 傳遞方式（R9-2 實作，additive）

- `Report.Generate(...)` 加一個 **additive** 參數 `regime *scanner.MarketRegimeResult`（或放進 `reportData`）。
- `regime == nil`（未計算 / 未啟用）→ 整個 ⑩ **不渲染**，既有 report 位元組不變。
- `show_market_regime == false` → 即使有 regime 也不顯示。

### 8.3 banner 欄位

```text
目前 regime（BULL / SIDEWAYS / BEAR / CRASH / RECOVERY / UNKNOWN）
HighVolatility（true / false）
主要解讀
適合觀察的 setup
需要降級的 setup
風險 caveat
佐證（proxy / breadth，來自 Reasons）
```

### 8.4 範例文案

```text
⑩ 市場環境：BEAR ＋ 高波動
   主要解讀：強股訊號需降級；突破 / VCP 成功率可能下降。
   適合觀察：相對抗跌、是否守 MA60、D_CRASH_SURVIVOR（低信心）。
   需降級：追高突破、淺回檔 pullback、單純 VCP retest。
   佐證：0050 跌破 MA60/MA120，20d −6.2%，breadth above-MA20 32%。
   ※ 僅作 regime context，非交易指令。
```

```text
⑩ 市場環境：BULL ＋ 高波動
   主要解讀：強多頭環境，主要觀察 20d 處置 / 冷卻 / 再攻擊循環。
   適合觀察：MA20 淺回檔 retest、RS≥70、接近 52 週高、MTF 日週同步。
   需降級：追高無量股。
   ※ 停損過緊可能造成洗出；ATR_3 / PCT_15 仍只作候選觀察。
```

```text
⑩ 市場環境：CRASH
   主要解讀：殺盤環境，不做突破追價解讀；此類訊號僅代表相對抗跌。
   適合觀察：誰沒死 / 誰少跌 / 誰先止跌 / 是否有資金回補。
   需等待：市場止跌與價格確認。
```

---

## 9. R6 / R8 / HorizonHint 在不同 regime 下如何解讀（R9-3）

**不改任何 R6/R7/R8/HorizonHint 的輸出。** R9-3 只加一句「在目前 regime 下該怎麼看」的**純文案**，來自一張 `regime × setup → 解讀字串` 對照表（純資料 / 純函式），不回寫任何分數欄位。

| 訊號 | BULL | SIDEWAYS | BEAR | CRASH |
|---|---|---|---|---|
| **R6 pullback / VCP_MA20 retest** | 20d 觀察循環適用；A_MA20>A_MA60、C_VCP_MA20 有樣本 | 週期縮短，看 5/10d early reaction | 降級，等站回 MA60 / breadth 改善 | 不作進攻，只看是否抗跌 |
| **HorizonHint（20d 主週期）** | 照常（R6-6 近期強多頭驗證支撐） | 提示「延伸性打折」 | 標註「牛市 pullback 邏輯失真」 | 「止跌前不宜以此週期解讀」 |
| **R8 ETF Flow** | 主動加碼＝加分觀察 | 中性 | 被動賣壓當風險 caveat | 看是否有資金回補 |
| **NewHigh / 突破** | 可延續 | 不追高、防假突破 | 假突破機率升，降級 | 不作突破追價 |
| **MTF** | 日週同步當強訊 | 中性 | weekly downtrend 提高警戒 | 同 BEAR |
| **RS / 相對強度** | RS≥70 找強股 | RS 緩升觀察 | HIGH_RS vs LOW_RS、相對大盤少跌 | 相對大盤 20d +5pp 以上才留意 |
| **D_CRASH_SURVIVOR** | 不適用 | 不適用 | 可看，但一律 LOW confidence（crash 樣本少） | 主要觀察對象，仍 LOW confidence |

### 9.1 各 regime 的重點摘要（供文案生成）

- **BULL**：找強股、找回檔後再攻擊、不要太早被洗出、20d 處置循環、ETF 主動資金是否加碼。
- **SIDEWAYS**：不追高、看箱型 / VCP / 量縮 / 突破失敗風險；持有週期可能比牛市短（5d/10d early reaction 更重要，20d 仍看但別過度期待延伸）。
- **BEAR**：保護資金、降低 false breakout、確認相對抗跌、等市場風險下降；看是否守 MA60、ETF 是否被動賣壓。
- **CRASH**：不急著找進攻點；誰沒死 / 誰少跌 / 誰先止跌 / 誰有資金回補；需等市場止跌與價格確認。
- **RECOVERY**：確認翻升是否延續；一律 LOW confidence。

---

## 10. 測試清單

### 10.1 R9-1 引擎（table-driven 單元測試）

- 各 regime 邊界 fixture：BULL / BEAR / CRASH / SIDEWAYS / RECOVERY 各造一組合成 proxy + breadth。
- HighVolatility：高波動 / 低波動 / 歷史不足走 fallback。
- proxy 缺失 / 歷史 < 門檻 → UNKNOWN，不 panic。
- breadth universe 為空 / 全 nil / 有效家數過少 → 不 panic，degrade 到 UNKNOWN / LOW。
- 決策優先序：CRASH 壓過 BEAR、RECOVERY 條件互斥性。
- **無 look-ahead**：每日只用當日以前的 bar（比照 r6backtest as-of 慣例）。

### 10.2 R9-2 / R9-3 顯示

- `show_market_regime == false` → report 位元組完全不變（golden / snapshot 比對，比照現有 `internal/report/report_test.go`）。
- `regime == nil` → ⑩ 不出現。
- ⑩ 各 regime × HighVol 組合文案正確。
- **文案紅線 lint**：斷言輸出**不含**禁詞（見 §13）。

### 10.3 不變量測試（最關鍵）

- 開 / 關 R9 全鏈，逐檔比對 `RocketScore / WatchAction / ExplosionProb / 排序 / StopLoss` **完全相同**（比照 `TestAttachETFFlowDoesNotChangeScore` 手法）。

---

## 11. 會新增 / 修改哪些檔案（實作階段，非本 commit）

> 以下為 R9-1/2/3 **實作時** 的檔案計畫。**本 doc commit 不含這些。**

### 11.1 新增

- `internal/marketregime/`（新套件，仿 `internal/etfflow` 的乾淨分離）
  - `regime.go`（`MarketRegimeResult` + `DetectRegime()` 純函式）
  - `breadth.go`（over-universe breadth）
  - `volatility.go`（HighVolatility）
  - `interpret.go`（R9-3 regime × setup 文案表）
  - `*_test.go`
- `docs/SPEC_R9_1_MARKET_REGIME_LAYER.md`（**本檔**）
- （選用）`cmd/regime-check/`：離線 dump 當日 regime 供 R9-1 驗證，不進 report。

### 11.2 修改（全部 additive、gated，預設關）

- `internal/scanner/scanner.go`：新增 `EnableMarketRegime` / `ShowMarketRegime` + regime 門檻 config 欄位。
- `cmd/scanner/main.go`：`EnableMarketRegime` 開啟時才 fetch 0050 proxy、算 regime，作為 **post-pass**（不進 `EnrichWatchlist`、不進 `computeRocket`）。
- `internal/report/report.go`：`Generate` 加 additive `regime` 參數 + ⑩ template（`show_market_regime` gated）。
- `internal/fetcher/`：新增 0050 market proxy 抓取路徑。
- `configs/config.yaml`：新增 `market_regime:` 區塊（預設 disable）。

### 11.3 config（草案，實作階段才加）

```yaml
market_regime:
  enable_market_regime: false      # 總開關：是否計算 regime。預設 false
  show_market_regime: false        # 顯示開關：是否渲染 ⑩。預設 false
  # ── baseline thresholds, not final calibrated parameters ──
  regime_crash_ret20: -8.0
  regime_crash_breadth: 30.0
  regime_bear_breadth: 40.0
  regime_bull_breadth: 55.0
  regime_recovery_lookback: 20
  regime_vol_percentile: 80
  regime_vol_lookback: 252
  # regime_vol_abs_atr_pct: <待定>
```

---

## 12. 保證不碰哪些檔案 / 邏輯

R9 全程**不改**下列（第一版 display / report only）：

- `internal/scanner/rocket.go`（RocketScore / RocketStage / ExplosionProb / WatchAction）
- `internal/scanner/watchlist.go` 的評分與 `EnrichWatchlist` 評分路徑、排序 comparator
- `internal/scanner/relstrength.go` / `newhigh` / `vcp` / `momentum` / MTF 的訊號輸出
- `internal/scanner/horizonhint.go`（R6-7）、`holdinghorizon.go`（R7-1）
- `internal/etfflow/*`（R8 訊號與 `signal.go`）
- `internal/r6backtest/*`（R6 結論、stop profile 預設、`regime.go` 皆不改）
- 任何 stop / stop profile 預設
- Guardrail A/B 預設（R9 與 scoring 無關，只加 R9 自己的 disable 開關，不動 guardrail 預設）

不新增任何 broker / 下單 / 自動交易路徑。

---

## 13. 文案紅線

⑩ banner 與 R9-3 解讀文案**禁止**出現下列（測試需 lint 斷言）：

```text
可以買 / 可以賣
建議買入 / 建議賣出
到價下單 / 掛單 / 進場價 xx 買
```

**准許**用語：

```text
觀察 / 降級解讀 / 升級解讀 / 等待確認
市場環境偏弱 / 相對抗跌 / 僅作 regime context
```

原則：R9 只描述「這個環境下訊號該打幾折看」，不給任何交易指令。

---

## 14. 不自動交易 / 不接 broker / 不下單聲明

```text
R9 是 market regime 解讀層，不是自動交易系統。
第一版（R9-1/2/3）：display-only / report-only。
不改 RocketScore / WatchAction / ExplosionProb / sorting / stop profile。
不自動下單、不接 broker API、不做交易執行、不代使用者操作股票。
R9-4（regime 影響 risk warning / sorting）= future only, requires separate validation，本版不做。
```

---

## Appendix A — 與既有 memory / SPEC 對齊

- Guardrail A/B：A=daily(scoring off) / B=control(scoring on)；B 尚未 fresh-data 驗證前不設預設。R9 不動 guardrail 預設。
- R6 backtest：docs phase done、R6-5 fresh-cache 可重現（commit c6c0f45）；ATR_3 為 top stop 候選但 **stop profile 預設不變**。R9 不改 R6 結論。
- R8 ETF Flow：display / context only（延遲揭露），R9-3 只在文案層引用，不改其 signal。
