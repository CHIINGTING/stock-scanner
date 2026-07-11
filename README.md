# 📡 Stock Radar — 台股盤後交易助理

Stock Radar 不是技術指標展示工具。

它直接回答你最關心的問題：
- **這支股票現在能買嗎？**
- **我的持股應該續抱還是停損？**
- **量能是否支持這波漲勢？**

---

## 目錄

1. [安裝](#安裝)
2. [快速開始](#快速開始)
3. [stocks.yaml 格式](#stocksyaml-格式)
4. [持倉（positions）](#持倉-positions)
5. [觀察清單（watchlist）= 飆股候選追蹤系統](#觀察清單watchlist-飆股候選追蹤系統)
6. [族群輪動（Rotation）](#族群輪動-rotation)
7. [Action 建議說明](#action-建議說明)
8. [BUY 與 WATCH 的差異](#buy-與-watch-的差異)
9. [量價分析說明](#量價分析說明)
10. [市場別：TW vs TWO](#市場別tw-vs-two)
11. [執行參數](#執行參數)
12. [消息面（News Shadow Module）](#消息面news-shadow-module)
13. [設定檔與所有開關（config.yaml）](#設定檔與所有開關configyaml)
14. [常見問題](#常見問題)

---

## 安裝

### 前置需求

- Go 1.22 以上
- 網路連線（抓取 Yahoo Finance 資料）

### 步驟

```bash
# 1. Clone 專案
git clone <your-repo-url> stock-scanner
cd stock-scanner

# 2. 安裝依賴
go mod tidy

# 3. 編譯
make build
```

---

## 快速開始

### 只分析自己的持股與觀察清單（最快，< 1 分鐘）

```bash
make run-fast
```

### 加上市場掃描前 50 名

```bash
make run
```

### 完整市場掃描（上市 + 上櫃，約 30 分鐘）

```bash
make run-all
```

報告輸出至 `reports/report_YYYYMMDD.html`，用瀏覽器開啟即可。

### 線上查看每日報告（GitHub Pages）

每小時的自動掃描（`.github/workflows/stock-scanner-hourly.yml`）會把當天的 HTML 報告
commit 進 repo，並把最新一份複製成根目錄的 `index.html`，因此可以直接用瀏覽器在線上
查看，不必本機重跑。

**最新報告**（根網址永遠指向最新一份，建議加書籤）：
<https://chiingting.github.io/stock-scanner/>

**指定日期**的歸檔報告（把 `YYYYMMDD` 換成日期）：

```
https://chiingting.github.io/stock-scanner/reports/report_YYYYMMDD.html
```

例如 2026/06/09 的報告：
<https://chiingting.github.io/stock-scanner/reports/report_20260609.html>

**首次啟用（只需做一次）**：到 GitHub repo 的
**Settings → Pages**，將 **Source** 設為 **Deploy from a branch**，
Branch 選 **`main`** / **`/ (root)`**，存檔後等幾分鐘部署完成即可。

> 備註：GitHub 不會直接渲染 repo 內的 `.html`（點進去只會看到原始碼），
> 一定要透過上面的 Pages 網址才能看到渲染後的頁面。

---

## stocks.yaml 格式

所有持股與觀察清單都在這個檔案設定。

```yaml
positions:
  - code: "2337"        # 股票代號
    name: "旺宏"         # 股票名稱（選填，留空則顯示 Yahoo 名稱）
    market: "TW"        # 市場（選填：TW / TWO / 留空自動偵測）
    entry: 173.5        # 進場成本（每股，元）
    shares: 1000        # 持股股數

  - code: "5483"
    name: "中美晶"
    market: "TWO"       # 上櫃股必須填 TWO
    entry: 165
    shares: 2000

watchlist:
  - code: "2408"
    name: "南亞科"
    market: "TW"

  - code: "8299"
    name: "群聯"
    # market 未填 → 自動偵測
```

---

## 持倉（positions）

`positions:` 區段用來追蹤你**已持有**的股票。

系統會根據進場成本與目前技術面，給出以下資訊：

| 欄位 | 說明 |
|------|------|
| 成本 | 你的進場價（entry） |
| 現價 | 最新收盤價 |
| 損益% | (現價 - 成本) / 成本 × 100 |
| 損益額 | (現價 - 成本) × 股數 |
| 交易建議 | HOLD / REDUCE / TAKE PROFIT / STOP LOSS |
| 停損價 | 建議的停損點 |
| 目標1 | 第一目標（通常為布林上軌或 2× 風險） |
| 目標2 | 第二目標（3.5× 風險） |

### 自動觸發條件

**STOP LOSS 觸發：**
- 虧損超過 15%（立即停損）
- 虧損超過 10% 且 MA20 下彎或 KDJ 死叉
- 虧損超過 7% 且 MA20 下彎 + KDJ 死叉同時發生

**TAKE PROFIT 觸發：**
- 浮盈超過 30%（建議全數獲利了結）
- 浮盈超過 15% 且 RSI 超買（>72）
- 浮盈超過 15% 且 KDJ 死亡交叉（動能轉弱）

**REDUCE 觸發：**
- 浮盈超過 12% 且 RSI 偏高（>65）

---

## 觀察清單（watchlist）= 飆股候選追蹤系統

> 核心一句話：**Scanner 找標的，Watchlist 判斷它是不是快變飆股。**

能進觀察清單代表已通過初篩，所以「🚀 飆股候選」分頁不是普通排名表，而是回答 6 件事：
(1) 是不是快發動？(2) 該等／準備／買／移除？(3) 進場看哪個價位？(4) 失敗跌破哪裡放棄？
(5) 有沒有回測支持？(6) 最大風險是什麼？

UI 為**兩層**：第一層精簡列表（股票｜族群｜飆股分數｜階段｜操作建議｜風險），
點任一檔展開**決策卡片**（族群輪動／型態狀態／回測結果／價位計畫／理由／風險）。

### 飆股分數（rocket_candidate_score, 0~100）

5 組加權：族群資金流入(25)＋個股相對強勢(20)＋技術接近噴出(25)＋量能結構健康(15)＋尚未過熱(15−罰分)。

| 分數 | 意義 |
|------|------|
| 0~39 | 不適合觀察 |
| 40~59 | 有潛力，未就緒 |
| 60~74 | 準備中，密切追蹤 |
| 75~89 | 高機率準備發動 |
| 90~100 | 已主升或過熱，小心追高 |

### 飆股階段（rocket_stage）→ 操作建議（watch_action）

| 階段 | 操作建議 |
|------|----------|
| NOT_READY 未就緒 | WAIT |
| BASE_BUILDING 築底 | WATCH_CLOSELY |
| PRE_BREAKOUT 突破前 | PREPARE_ENTRY |
| BREAKOUT_START 起漲 | BREAKOUT_BUY |
| MAIN_RUN 主升 | PULLBACK_BUY（拉回量縮）／ WATCH_CLOSELY |
| OVERHEATED 過熱 | TAKE_PROFIT |
| FAILED 失敗 | REMOVE_FROM_WATCHLIST |

### 整理型態分類（consolidation bucket）

**整理時間只做分類，不是越長越好。** 強勢股急漲後只整理 3~5 天，只要守住支撐、量縮、
高點不墜、低點墊高、族群續流入，仍可是高品質 base（高分 PRE_BREAKOUT）。品質看
`base_quality_score`（壓縮＋量縮＋支撐＋接近前高＋族群流入 − 爆量長黑 − 跌破平台），不看天數。

| bucket | 天數 | 型態 |
|--------|------|------|
| MICRO_BASE | 3~5 | 強勢急拉後短整、強勢換手 |
| SHORT_BASE | 6~10 | 短線再攻前準備 |
| SWING_BASE | 11~20 | 明顯平台、判斷突破 |
| MID_BASE | 21~40 | 波段平台 |
| LONG_BASE | 41~60 | 中長底/大型平台 |

### 回測（backtest）

把「目前型態」拿去過去**同型態 + 同整理 bucket**的情境回測：5 日平均報酬、最大回撤、
勝率、風報比。同時跑**個股**與**族群**兩個層級（族群 = pool 同族群成員歷史，樣本更多、
信心更高）。回測樣本依賴歷史長度，預設抓 2 年（`fetcher.history_range`）。

---

## 族群輪動（Rotation）

Scanner 的定位是**尋找未來 1~4 週有機會的股票**，不是當沖。真正的大波段通常是
**先有族群輪動、再有個股表態**——資金會從已噴出的中後段族群（例如記憶體：南亞科/
華邦電/旺宏）流向下一波接棒族群。

「🔄 輪動」分頁不看「今天最強是誰」，而是回答：

> **「下一波資金可能去哪裡？」**

並刻意把已過熱的族群往後排，優先呈現處於**醞釀 / 確認**階段的族群。

### 族群設定（configs/sectors.yaml）

```yaml
sectors:
  - name: "矽光子"
    stocks:
      - { code: "6442", name: "光聖" }
      - { code: "3163", name: "波若威" }
      # market 留空 → 自動偵測 .TW / .TWO
  - name: "PCB"
    stocks:
      - { code: "3037", name: "欣興" }
```

> 內建為種子清單，請依需求自行複查、增刪族群與成員代號。

### 三層輪動模型

20 日強度反應較慢——資金通常**先**從短線流向出現跡象，**才**慢慢反映到 20 日。
因此每個族群拆成三層觀察：

| 層 | 視窗 | 用途 |
|----|------|------|
| **短線流向** | 1~5 日 | **最早**反映資金轉向（領先指標） |
| **中期強度** | 20 日 | 即 Sector Score（強 / 中 / 弱） |
| **波段趨勢** | 60 日 | MA60 斜率 + 站上 MA60（確認上升 / 尚未確認 / 轉弱） |

**短線流向（short_term_flow）** 輸出三個欄位：

- `short_term_flow_score`：0~100，綜合近 1/3/5 日漲幅、上漲家數比例、量能放大比例、
  站上 5/10 日均線比例、創 20 日新高比例、動能加速（短線步調 vs 20 日步調）。
- `short_term_flow_direction`：`INFLOW`（流入）/ `OUTFLOW`（流出）/ `NEUTRAL`（中性）。
- `short_term_flow_stage`：
  - **EARLY_ROTATION**（早期輪動）：短線轉強、但 20 日尚未反映 → **資金剛流入，早期候選**
  - **CONFIRMED_ROTATION**（確認輪動）：短線與中期同步走強 → 主升段
  - **OVERHEATED**（過熱）：短中皆強且偏超買 → 成熟／追價需謹慎
  - **WEAKENING**（轉弱）：短線流出或無力 → 高檔鈍化、資金可能轉出

**排序**：機會分數混合「短線流向 + 中期強度」再依階段加權，讓
*資金剛流入、20 日尚未反映* 的早期輪動族群能提早浮上來。Console 會額外列出
「早期輪動候選」，展開族群可看到一句話結論與各層細項。

> 範例：某族群 20 日強度＝中、60 日趨勢＝尚未確認，但近 3~5 日多檔同步放量上漲 →
> `short_term_flow = INFLOW`、`stage = EARLY_ROTATION`，結論「資金剛開始流入，屬早期輪動候選」。

### 族群評分（Sector Score，0–100）

| 權重 | 組件 | 說明 |
|------|------|------|
| 30% | 相對強度 | 族群平均 20 日報酬，跨族群正規化 |
| 25% | 新高比例 | 創 60 日新高的成員占比 |
| 20% | 突破比例 | 突破 20 日整理高點 / 站上布林上軌的成員占比 |
| 15% | 量能放大 | 量比 ≥ 1.5 倍均量的成員占比 |
| 10% | MA60 斜率 | MA60 上揚的成員占比 |

### 資金流向（流入 / 流出）

每個族群另外標記近 5 日的**資金淨流向**，採 MFI 式的「典型價 × 量」計算，
依典型價漲跌分正負流，再正規化為 -1~+1：

| 標記 | 含義 |
|------|------|
| **流入 ↑** | 淨流向 ≥ +0.2，近期資金偏買方（量能伴隨上漲） |
| **流出 ↓** | 淨流向 ≤ -0.2，近期資金偏賣方（量能伴隨下跌） |
| **中性** | 介於兩者之間 |

搭配階段一起看：**EARLY/CONFIRMED + 流入**是最理想的布局組合；
**LATE + 流出**則代表資金正在離場，最該避開。展開族群後每檔個股也有各自的流向箭頭。

### 輪動階段（Rotation Stage）

| 階段 | 含義 | 操作意義 |
|------|------|----------|
| **醞釀 EARLY** | 開始有資金流入、量能增加 | 最佳布局時機 |
| **確認 CONFIRMED** | 多檔突破整理區 | 趨勢確立，可介入 |
| **過熱 HOT** | 多檔新高、市場焦點 | 中後段，謹慎追價 |
| **末段 LATE** | 漲幅過大、超買 | 不建議追價 |

排序採**機會調整**：EARLY / CONFIRMED 會被加權往前，HOT / LATE 往後淡化，
所以排名第一的不一定是分數最高的，而是「最值得布局」的族群。
點擊任一族群即可展開其成員股票的個別表現。

### 執行

```bash
make run-rotation      # 只跑族群輪動（跳過全市場掃描，最快）
./bin/scanner --no-rotation        # 跳過族群輪動
./bin/scanner --sectors my.yaml    # 使用自訂族群清單
```

---

## Action 建議說明

| Action | 意義 | 適用情境 |
|--------|------|----------|
| **STRONG BUY** | 強力買進 | 5/5 交易條件全部成立，主力量能確認 |
| **BUY** | 買進 | 4/5 條件成立，技術面明確偏多 |
| **WATCH** | 繼續觀察 | 3/5 條件成立，條件尚未完全到位 |
| **HOLD** | 持有 | 2/5 條件成立，無明確進出場訊號 |
| **REDUCE** | 減碼 | 浮盈已大 + 技術面轉弱，分批獲利 |
| **TAKE PROFIT** | 獲利了結 | 達到目標或出現明確賣出訊號 |
| **STOP LOSS** | 停損出場 | 虧損超標或技術面持續惡化 |
| **SELL** | 賣出 | 技術面全面走空 |

---

## BUY 與 WATCH 的差異

**WATCH（觀察）** — 條件尚未齊全，不應貿然進場：
- 可能 MA20 還在下彎
- 可能量能不足
- 可能 KDJ 尚未交叉
- → 等待訊號確認，設定價格警示

**BUY（買進）** — 多數條件已成立：
- MA20 上揚確認
- RSI 在合理進場區間
- KDJ 多頭排列或黃金交叉
- 量能配合
- → 可以進場，但仍需設好停損

**STRONG BUY（強力買進）** — 所有條件齊全：
- 5/5 交易條件全部成立
- 通常伴隨爆量（主力積極介入）
- 技術結構完美
- → 積極進場，但不要追高超過進場價 3%

---

## 量價分析說明

量能是本系統最重視的指標之一，佔評分 **25 分**（共 100 分）。

### 量價訊號

| 訊號 | 說明 | 操作意義 |
|------|------|----------|
| **價漲量增** ✅ | 漲幅有量能支撐 | 最佳買進訊號 |
| **價漲量縮** ⚠️ | 漲幅缺乏量能 | 小心假突破 |
| **價跌量增** ❌ | 大量下跌，賣壓沉重 | 避免進場，持有者考慮停損 |
| **價跌量縮** ⚠️ | 量縮整理 | 等待方向確認 |

### 量比（Volume Ratio）

```
量比 = 當日成交量 / 20日平均量
```

- 量比 > 2.0：爆量，主力介入
- 量比 1.0–2.0：正常放量
- 量比 < 0.8：縮量，市場觀望

### 大單偵測

當量比超過 3 倍時，系統會標記「大單偵測」：
- 搭配上漲：法人/主力積極建倉
- 搭配下跌：主力出貨，需謹慎因應

### 漲停鎖量 vs 漲停失敗

> **核心原則：量縮本身不是問題，問題是「量縮時價格有沒有失守」。**

開盤急攻漲停後若買盤封住、賣方惜售，成交量自然縮小——這是籌碼鎖定，不該被當成「量縮轉弱」扣分。系統會從每日 K 棒（開高低收 + 量比）推斷漲停籌碼動態：

| 標記 | 判斷條件（日線近似） | 評價 |
|------|----------------------|------|
| **漲停鎖量** 🔒 `LOCKED_LIMIT_UP_LOW_VOLUME` | 漲幅 ≥ 9%、收在當日最高（封住）、量比 < 1（量縮） | **中性偏多**，量能不扣分、量能條件視為通過 |
| **漲停失敗** ⚠️ `LIMIT_UP_FAILED` | 盤中觸及漲停（最高 ≥ +9%），但收盤大幅拉回（收盤 ≤ 最高 × 0.97）且放量（量比 ≥ 1.5） | **負面**，漲停打開後放量下殺 |
| **漲停失敗** ⚠️ `DISTRIBUTION_AFTER_LIMIT_UP` | 前一日漲停鎖住，今日放量下跌 | **負面**，疑似漲停後出貨 |

> 註：資料來源為 Yahoo 的**日線 OHLCV**，沒有逐筆／封單資料，因此「封板、打開、封單增減」是以當日開高低收與量比近似判斷。

---

## 市場別：TW vs TWO

台灣股市分為兩個交易市場：

| 市場 | 說明 | Yahoo 代碼格式 | 例子 |
|------|------|----------------|------|
| **上市（TWSE）** | 台灣證券交易所 | `{code}.TW` | `2330.TW` |
| **上櫃（TPEX）** | 證券櫃檯買賣中心 | `{code}.TWO` | `5483.TWO` |

### 如何判斷是上市還是上櫃？

最簡單的方法：在台股查詢網站（如 台灣 Yahoo 股市）搜尋股票代號，查看是「上市」還是「上櫃」。

### stocks.yaml 設定

```yaml
# 上市股票（TWSE）
- code: "2330"
  market: "TW"      # 明確指定

# 上櫃股票（TPEX）
- code: "5483"
  market: "TWO"     # 明確指定

# 不確定時：留空，系統自動偵測
- code: "3048"
  # market 留空 → 先試 .TW，失敗再試 .TWO
```

**建議**：確定的股票請明確填入 market，可避免一次額外的網路請求。

---

## 執行參數

```bash
# 只分析持股 + 觀察清單（跳過全市場，最快）
./bin/scanner --no-market

# 市場掃描前 50 名（預設）
./bin/scanner --top 50

# 市場掃描前 100 名
./bin/scanner --top 100

# 市場掃描前 500 名
./bin/scanner --top 500

# 掃描全部上市 + 上櫃股票（最慢，約 30 分鐘）
./bin/scanner --all

# 指定分析日期
./bin/scanner --date 2025-12-31

# 使用不同的持股清單檔案
./bin/scanner --stocks my_portfolio.yaml

# 跳過族群輪動分析
./bin/scanner --no-rotation

# 使用不同的族群清單檔案
./bin/scanner --sectors my_sectors.yaml

# 自訂設定檔
./bin/scanner --config configs/config.yaml
```

### Makefile 快捷指令

```bash
make run-fast      # 只跑 Positions + Watchlist（跳過市場掃描與輪動）
make run-rotation  # 只跑族群輪動
make run           # Top 50 市場掃描
make run-top100    # Top 100
make run-top500    # Top 500
make run-all       # 全部（上市 + 上櫃）
```

---

## 5 個交易評分條件（BestFourPoint 啟發）

系統評估 5 個獨立條件，並顯示通過幾個（●●●○○ = 3/5）：

| 條件 | 檢查內容 |
|------|----------|
| **趨勢** | 價格站上 MA20 + MA20 連續上揚 |
| **動能** | RSI 在最佳進場區間（25–65） |
| **擺盪** | KDJ 黃金交叉或多頭排列 |
| **量能** | 量比 > 1.3 且價漲量增 |
| **突破** | 布林帶突破上軌或站上中線 |

滿足條件越多，建議越積極：
- 5/5 → STRONG BUY
- 4/5 → BUY
- 3/5 → WATCH
- 2/5 → HOLD
- 1/5 → REDUCE
- 0/5 → SELL

---

## 消息面（News Shadow Module）

> Phase 1 為 **Shadow Mode（純影子模式）**：只做「顯示 / 記錄 / 保存」，
> **完全不改變任何 BUY / WATCH / SELL、飆股分數、排序、RocketScore、WatchAction、ExplosionProb**。

消息面模組把外部市場觀點（目前為股癌 Podcast 筆記）疊加到既有技術面掃描結果上，
作為「背景 context」，**不是交易指令**。關閉時（預設）報告與行為與過去完全一致。

### 資料來源

| 來源 | 方式 | 說明 |
|------|------|------|
| **SocialWorkerDaily** | WordPress REST API（主）+ RSS fallback | 股癌 Podcast 筆記，穩定、有 EP 集數、完整內文與可靠發布時間，**主要來源** |
| **TWETQ** | 瀏覽器 UA 直連（best-effort） | Cloudflare 台股量化站；`/podcast` 為 JS 渲染，多半降級為空。抓取失敗會被隔離，**不影響**掃描完成 |

### 運作流程

抓取 → **同集去重**（跨來源同一 EP 合併為單一事件，不重複計分）→ **個股 / 族群解析**
（明確字典，不亂猜；無法確定就保留 unresolved）→ **語意分類**（規則式，不使用 LLM）→
**跨來源衝突偵測** → 產生每檔卡片消息 + 市場消息總結 → 寫入**時間戳快照**。

分類型別：`BULLISH` / `BEARISH` / `ROTATION_IN` / `ROTATION_OUT` / `RISK_WARNING` / `NEUTRAL`。

- 具備負向與複合語意：「突破失敗」不判多、「沒有轉弱」不判空、
  「修正…但仍在季線之上」→ `RISK_WARNING`（不會粗暴判強空、也不會誤判 `ROTATION_IN`）。
- 同一事件不同來源方向相反時（例：SWD 被動元件 `ROTATION_OUT` vs TWETQ `BULLISH`），
  **兩邊證據都保留**並標記 `conflict`，不會靜默覆蓋。

### 報告呈現（需 `show_news: true`）

- 每檔股票卡片新增 **⑩ 消息面** 區塊：個股 / 族群訊號、來源 EP、強度、
  Age（訊號新鮮度）、來源分歧標記。
- 市場掃描頁頂端新增 **市場消息面 banner**，依 SignalType 分組顯示：
  🟢 資金輪動進場／🟢 偏多／🟡 風險警示／🔴 資金輪動流出／🔴 偏空，
  並附 Fresh / Active / Expired 計數。**（BULLISH 不會被誤標成 ROTATION_IN。）**

### 歷史快照與防止 look-ahead

- 每次執行寫出 `reports/news_YYYYMMDD_HHMMSS.json`——**每次執行一份、不覆蓋**，
  盤中多次執行（08:30 / 09:30 / …）都各自保留。
- 快照保存：`event_id`、來源與 URL、EP、`raw_title`、內文摘要、`evidence`、
  解析出的個股 / 族群、SignalType、`strength`、`confidence`、`conflict`，以及
  `published_at`（來源發布時間）與 `observed_at`（實際觀察 wall-clock 時間）——兩者分離。
- **防 look-ahead：** 以 `--date` 指定「非今日」執行時，**不會即時抓取當前新聞**
  （避免用今天的新聞假裝是過去看到的）；未來回測只能從 `observed_at` 起算才可使用該訊號。

### 啟用方式

消息面**沒有 CLI 旗標**，透過 `configs/config.yaml` 開啟（預設全關）：

```yaml
scanner:
  enable_news: true    # 抓取 + 解析 + 寫快照（背景計算）
  show_news: true      # 報告顯示 ⑩ 消息面 + 市場 banner
```

| 組合 | 行為 |
|------|------|
| `enable_news: false`（預設） | 完全不抓取、不 attach、不寫快照，報告與行為零改變 |
| `enable_news: true, show_news: false` | **有抓取、有寫快照**，但報告不顯示（純記錄模式） |
| `enable_news: true, show_news: true` | 抓取 + 顯示（完整） |
| `enable_news: false, show_news: true` | 不抓取，報告也沒有東西可顯示 |

> 別名字典：`configs/news_aliases.yaml`（個股別名 → 代號、主題關鍵字 → 族群），可自行增補。

---

## 設定檔與所有開關（config.yaml）

所有行為集中在 `configs/config.yaml`。多數進階功能採**雙層開關**：

- **`enable_*`**：是否**計算 / 抓取**該功能（關 → 完全不算，對既有輸出零影響）。
- **`show_*`**：是否在報告**顯示**（純顯示，不影響分數 / 排序 / 動作）。
- 另有一個 **master flag** `enable_signal_guardrail_scoring`：唯有它為 `true` 時，
  RS / 新高 / VCP / MomentumFlow 這些 shadow 訊號才會**真的影響**飆股分數；
  否則即使各自 `enable` 也只是 shadow-only（只計算、不改分數，即「觀察模式」）。

### fetcher（抓取 / 快取）

| 變數 | 預設 | 說明 |
|------|------|------|
| `concurrency` | 3 | 同時請求數，建議 ≤5 避免 Yahoo 限速 |
| `request_delay_ms` | 400 | 每個 worker 請求間隔（ms），建議 300~500 |
| `timeout_sec` | 30 | 單次 HTTP timeout |
| `cache_ttl_min` | 15 | OHLCV 快取時效（分鐘） |
| `cache_dir` | `.cache` | 快取目錄 |
| `eof_cooldown_min` | 5 | 遇 EOF / 連線重置後該 ticker 冷卻分鐘數 |
| `history_range` | `2y` | 歷史抓取範圍（6mo / 1y / 2y），越長回測樣本越多 |

### scanner — 基礎

| 變數 | 預設 | 說明 |
|------|------|------|
| `min_price` | 10.0 | 最低收盤價（過濾低價股） |
| `min_avg_volume` | 500000 | MA20 量能門檻 |
| `top_n` | 50 | 市場掃描顯示前 N（`--top` 可覆蓋） |
| `use_adjusted_close` | false | 還原收盤總開關；true → RS / 新高 / VCP / 回測改用 AdjClose |

### scanner — 訊號 guardrail / 顯示總開關

| 變數 | 層級 | 說明 |
|------|------|------|
| `enable_signal_guardrail_scoring` | **master** | 唯有 true，shadow 訊號才可影響分數 / 動作 / 機率；預設 false = shadow-only |
| `show_guardrail_signals` | 顯示 | Watchlist 卡片顯示 Guardrail Signals 解釋（實驗性），不影響分數 |
| `show_backtest_insights` | 顯示 | 多一個「🔬 回測洞察」分頁（靜態摘要），不影響分數 |

### scanner — RS 相對強弱（C2）

`enable_rs_rank`（總開關）｜`rs_lookback_days`(120)｜`rs_min_history_days`(100)｜
`rs_universe_exclude_non_common_stock`(true)｜`rs_use_adjusted_close`｜
`rs_leadership_threshold`(80)｜`rs_watch_threshold`(70)

### scanner — 52 週 / 多週期新高（C3）

`enable_new_high`（總開關）｜`nh_lookbacks`([20,60,120,250])｜`nh_min_history_days`(60)｜
`nh_leader_within_pct`(25)｜`nh_near_52w_high_pct`(15)｜`nh_breakout_watch_pct`(5)｜
`nh_leader_strong_pct`(10)｜`nh_leader_far_pct`(50)｜`nh_vol_confirm_ratio`(1.5)｜
`nh_overext_rsi`(75)｜`nh_use_adjusted_close`

### scanner — VCP 波動收縮型態（C4）

`enable_vcp`（總開關）｜`vcp_lookback_days`(60)｜`vcp_min_history_days`(40)｜
`vcp_min_contractions`(2)｜`vcp_min_quality_score`(70)｜`vcp_use_adjusted_close`｜
品質權重 `vcp_tightness_weight`(30) / `vcp_volume_dryup_weight`(25) /
`vcp_monotonic_weight`(20) / `vcp_support_hold_weight`(15) / `vcp_near_breakout_weight`(10)｜
`vcp_zigzag_reversal_pct`(2.5)｜`vcp_min_contraction_depth_pct`(2)｜`vcp_max_contractions`(5)

### scanner — MomentumFlow 個股動能（C5）

`enable_momentum_flow`（總開關）｜`mf_min_history_days`(30)｜
`mf_accel_short_window`(3) / `mf_accel_long_window`(20)｜
`mf_accel_pos_thresh`(0.0008) / `mf_accel_neg_thresh`(-0.0008) / `mf_accel_scale`(12000)｜
`mf_key_ma`(20)｜`mf_reclaim_lookback`(5)｜`mf_zigzag_reversal_pct`(1.5)｜
`mf_rsi_div_lookback`(20)｜`mf_use_adjusted_close`｜
`mf_shift_up_min_below_days`(2) / `mf_shift_up_confirm_days`(2)

### scanner — 多週期 Multi-Timeframe（R4-2，shadow-only）

`enable_multi_timeframe`（總開關）｜`mtf_use_adjusted_close`｜
`mtf_strong_daily_score_threshold`(85) / `mtf_strong_weekly_score_threshold`(85)｜
`mtf_risk_warning_enabled`(true)｜`mtf_sort_tiebreaker_enabled`(true)｜
`mtf_sort_tiebreaker_score_gap`(3)

### scanner — MomentumFlow 分數修正（僅 master flag 開時生效）

`mf_score_modifier_building`(5)｜`mf_score_modifier_continuation`(6)｜
`mf_score_modifier_shift_up`(8)｜`mf_score_modifier_fading`(-6)｜
`mf_score_modifier_shift_down`(-12)｜`mf_score_modifier_cap`(12)

### scanner — HoldingHorizon（R7-1，shadow-only 參考持有區間）

`enable_holding_horizon`（總開關）｜`hh_min_history_days`(70)｜`hh_atr_compress_pct`(4.0)

### scanner — HorizonHint（R6-7，display-only，報告 ⑧）

`show_horizon_hint`（總開關）— 顯示回測觀察週期提示，不影響分數 / 排序 / 動作。

### scanner — ETF Flow（R8，display / context，報告 ⑨）

| 變數 | 說明 |
|------|------|
| `enable_etf_flow` | 是否載入 ETF snapshot 並 attach（預設 false） |
| `show_etf_flow` | 報告 ⑨ 是否顯示（預設 false） |
| `etf_flow.snapshot_dir` | snapshot JSON 目錄（`data/etf_holdings`） |
| `etf_flow.history_days` | 連續天數回看窗（10） |
| `etf_flow.watch_etfs` | 追蹤的 ETF 清單（`code` + `type`: active / passive） |

### scanner — News 消息面（Phase 1，詳見上一節）

| 變數 | 說明 |
|------|------|
| `enable_news` | 抓取 + 解析 + 寫快照（預設 false） |
| `show_news` | 報告 ⑩ + 市場 banner 顯示（預設 false） |
| `news.shadow_mode` | Phase 1 恆 true（保留給 Phase 2） |
| `news.max_age_days` | 超過此天數視為 EXPIRED（仍存快照，10） |
| `news.age.{fresh_days,active_days,decaying_days}` | 生命週期邊界（2 / 5 / 10） |
| `news.aliases_file` | 別名 / 主題字典路徑 |
| `news.snapshot_dir` | 快照目錄（空 → 沿用 `report.output_dir`） |
| `news.timeout_seconds` | 單來源抓取 timeout（15） |
| `news.user_agent` | 瀏覽器 UA（TWETQ 過 Cloudflare 用） |
| `news.providers.socialworkerdaily.{enabled,api_base,max_items}` | SWD 來源設定 |
| `news.providers.twetq.{enabled,base_url}` | TWETQ 來源設定（best-effort） |

### scanner — 技術指標參數

`kdj.k_period`(9) / `d_smooth`(3) / `j_smooth`(3)｜`bollinger.period`(20) / `std_dev`(2.0)

### 其他

| 變數 | 說明 |
|------|------|
| `report.output_dir` | 報告輸出目錄（`./reports`） |
| `stocks_file` | 持股 / 觀察清單（`stocks.yaml`） |
| `sectors_file` | 族群輪動清單（`configs/sectors.yaml`） |

---

## 常見問題

**Q：報告顯示「無資料」？**
A：可能是 Yahoo Finance 速率限制。請稍後再試，或調低 `configs/config.yaml` 的 `concurrency`。

**Q：上櫃股票（如 5483）查不到資料？**
A：請在 stocks.yaml 加入 `market: "TWO"`。若不確定，留空讓系統自動偵測。

**Q：市場掃描在假日執行結果為空？**
A：TWSE / TPEX API 在休市日不提供當日資料。建議在交易日收盤後（15:00 後）執行。

**Q：STOP LOSS 訊號代表什麼？**
A：系統偵測到你的持倉虧損超過設定門檻（預設 -7% 且技術面惡化，或 -15% 無條件觸發）。這是建議，最終決定仍由你判斷。

**Q：評分和 BFP 條件哪個更重要？**
A：系統同時使用兩者，取較保守的結果。BFP 條件是質性判斷（幾個條件成立），評分是量化細節（每個指標的強度）。

**Q：如何設定自己的停損線？**
A：目前停損由 ATR（平均真實波幅）和布林通道下軌動態計算。若想自訂，可修改 `internal/scanner/scorer.go` 的 `priceTargets` 函式。

**Q：消息面（News）會不會影響 BUY / WATCH / SELL 或飆股分數？**
A：不會。Phase 1 為純 Shadow Mode，只做顯示 / 記錄 / 保存。關閉時（預設 `enable_news: false`）報告與行為與過去完全一致。

**Q：TWETQ 抓不到東西（`fetched=0`）正常嗎？**
A：正常。TWETQ 的 `/podcast` 為 JS 渲染，多半降級為空；抓取失敗會被隔離，不影響掃描完成。主要消息來源是 SocialWorkerDaily。

**Q：為什麼用 `--date` 指定過去日期時看不到當天的新聞？**
A：這是**防 look-ahead** 的刻意設計——歷史日期執行不會即時抓取當前新聞，以免用今天的消息假裝是過去看到的。要看歷史消息請讀當時寫出的 `reports/news_YYYYMMDD_HHMMSS.json` 快照。

**Q：Yahoo Finance 速率限制怎麼辦？**
A：調高 `configs/config.yaml` 中的 `request_delay_ms`（建議 500–1000ms），或降低 `concurrency`（建議 3–5）。

---

## 免責聲明

本工具僅供技術分析研究使用，**不構成投資建議**。

股票投資有風險，進場前請做好風險管理，設定停損。

過去績效不代表未來結果。
