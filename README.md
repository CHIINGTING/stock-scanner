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
13. [AI 解讀（AI Shadow Layer）](#ai-解讀ai-shadow-layer)
14. [區間策略回測面板（backtest.html）](#區間策略回測面板backtesthtml)
15. [設定檔與所有開關（config.yaml）](#設定檔與所有開關configyaml)
16. [常見問題](#常見問題)

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

## AI 解讀（AI Shadow Layer）

> **AI 分析是選用的，而且是 shadow-only：它不影響任何確定性計算的分數，也不影響
> BUY／WATCH／SELL 訊號。**

掃描器本身完全不含 AI——分數、階段、訊號、排序都由固定規則算出，同樣的輸入永遠得到
同樣的輸出。這一層做的是**另一件事**：把掃描器**已經算完**的證據（階段、飆股分數、
量價、整理天數、族群流向、法人籌碼、風險標籤）交給 OpenAI，換回一段人話的多空解讀。

它**不會**拿到原始價格序列，**不會**重算任何指標，**不會**產生買賣建議。報告 ⑭ 區塊
就是它唯一的出口。

走的是 OpenAI 官方 **Responses API**（`POST /v1/responses`），輸出格式由 Structured
Outputs（`text.format` 的 `json_schema` + `strict`）在 API 端強制保證，不是靠 prompt
拜託模型、更不是用 regex 從自然語言拆欄位。只用 Go 標準庫 `net/http`，沒有引入 SDK。

### 啟用方式

**第一步：把 API token 放進環境變數。** token 只從 `OPENAI_API_KEY` 讀取，程式不會、
也不能從設定檔或任何 repo 內的檔案取得它：

```bash
export OPENAI_API_KEY="sk-..."
```

> **絕對不要**把 token 寫進 `config.yaml`、原始碼、測試資料或報告——設定結構裡刻意
> 沒有任何可以存放 token 的欄位，`internal/scanner` 也有測試會擋下 configs 內的憑證。

**第二步：在 `configs/config.yaml` 開啟開關（預設全關），模型也寫在設定檔裡：**

```yaml
scanner:
  enable_ai: true      # 呼叫 OpenAI 做解讀
  show_ai: true        # 報告顯示 ⑭ AI 分析
  ai:
    model: "gpt-5.6-luna"
    timeout_sec: 30
    max_stocks: 12     # 每次執行最多分析幾檔（成本上限）
    temperature: 0.2
```

| 組合 | 行為 |
|------|------|
| `enable_ai: false`（預設） | 完全不呼叫 OpenAI，`WatchlistEntry.AI` 為 nil，輸出與沒有這功能時一致 |
| `enable_ai: true, show_ai: false` | 有呼叫、有記錄，但報告不顯示（觀察模式） |
| `enable_ai: true, show_ai: true` | 呼叫 + 顯示 ⑭ |
| `enable_ai: false, show_ai: true` | 不呼叫，報告也沒有東西可顯示 |

### 成本

一檔股票最多送出一次請求，而且只送「可操作候選」（PREPARE／BREAKOUT_BUY／
PULLBACK_BUY／WATCH_CLOSELY），再依飆股分數由高到低截到 `max_stocks`。WAIT 與
出場類 action 不會花錢。

### 失敗時會怎樣

**一律 fail-open。** 沒設 `OPENAI_API_KEY`、逾時、429、5xx、回應不是合法 JSON——
掃描與報告都照常完成，只是該檔的 ⑭ 區塊不出現。AI 失敗不會讓排程失敗。

模型的 `confidence` 是它對**自己那段解讀**的把握，不是交易信心，也不是勝率。

---

## 區間策略回測面板（backtest.html）

盤後報告回答「今天能不能買」。這個面板回答另一個問題：**如果我當初在某天買進、某天賣出，
不同的加碼／停利紀律，事後看分別會是什麼結果？**

```bash
make backtest          # 全部快取標的、完整歷史 → backtest.html（約 5 MB）
make backtest-light    # 只嵌最近 250 個交易日 → 檔案約一半大
```

**它也掛在每日報告的「🔬 回測洞察」分頁裡**（`show_backtest_insights: true` 時）：
該分頁的內容就是這個面板。按「在此展開面板」才會用 iframe 載入 `backtest.html`，
沒展開就完全不下載，所以每日報告的體積與載入速度不受影響；也可以「在新分頁開啟」用整頁版。

> 線上要用得到，`backtest.html` 必須跟報告一起 commit 進 repo（面板會依所在層級自動解析
> `backtest.html` / `../backtest.html`，所以根目錄的 `index.html` 與 `reports/` 底下的歸檔都指得到）。

產生的 `backtest.html` 是**單一自帶檔案**：價格資料直接嵌在頁面裡，不連網、不吃 CDN，
用瀏覽器直接開（或放上 GitHub Pages `/backtest.html`）就能用。資料來源是 `.cache/`
既有的價格快取，所以價格「跟上一次掃描一樣新」——想要最新價，先跑一次 `make run` 再產生面板。

### 面板上能調什麼

| 參數 | 說明 |
| --- | --- |
| 標的 / 買進日 / 賣出日 | 代號或中文名皆可；日期可用「近 1 月 / 3 月 / 6 月 / 1 年 / 全部」快速鍵 |
| 初始本金 | 第一天單筆進場的錢 |
| 定期定額加碼 | 每次金額 × 次數，每月幾號扣款（遇假日順延） |
| 預留現金 | 等「自區間最高收盤回檔 X%」時一次全數投入 |
| 對照標的 | 疊在走勢圖上，以第一天 = 100 正規化（預設 0050） |
| 交易成本 | 手續費 0.1425%（可打折、最低 20 元、買賣各收）+ 賣出證交稅 0.3% |

### 六種策略

| 策略 | 打法 |
| --- | --- |
| 基準 | 第一天全押、抱到期末（所有比較的分母） |
| 一 | 定期定額加碼，全程不賣 |
| 二 | 單筆進場 + 預留現金等回檔狙擊（掃 3%～30% 門檻） |
| 三 | 定期定額 + 回檔狙擊（雙資金流，預留現金不會被定期定額偷用） |
| 四 | 停利 + 回檔接回（單筆資金，掃「停利 × 接回」12×6 組參數矩陣） |
| 五 | 定期定額 + 停利接回（空手時新資金先進現金池） |
| 六 | 停利落袋，出場後不再進場（**不參加排名**——期末是現金，跟全程在市場的策略比報酬率不對等） |

### 讀這個面板要注意

- 所有進出都以**當日收盤價**成交，沒有盤中價、沒有跳空與滑價。
- **預留現金即使一路沒觸發，也算在總投入裡**——不然把錢閒置的規則會在報酬率上佔便宜。
- 報酬率一律是「損益 ÷ 總投入」，**不是時間加權**。有定期定額時後面才投入的錢待得比較短，
  所以策略一的報酬率天生比單筆保守，不能直接拿來當「策略優劣」。
- 「冠軍」是**事後**從上百組參數挑出來的最佳解，實戰不可能每次選中。冠軍只小勝基準時，
  選一個你執行得下去的規則，長期勝率反而更高。
- 預設價格是**原始收盤價**（未還原除權息）。長區間、高股息標的的報酬會被低估，
  想改用還原價加 `-adjusted`。

### 常用參數

```bash
go run ./cmd/backtest-panel -h                     # 全部參數
go run ./cmd/backtest-panel -days 250              # 只嵌最近 250 個交易日
go run ./cmd/backtest-panel -only 2330,2317,0050   # 只嵌指定標的（檔案極小）
go run ./cmd/backtest-panel -adjusted              # 用還原收盤價
go run ./cmd/backtest-panel -archive reports       # 另存一份 reports/backtest_YYYYMMDD.html
go run ./cmd/backtest-panel -capital 300000 -reserve 200000 -add 20000 -add-count 12
```

> `-days` 砍掉的是**嵌進頁面的歷史長度**，均線也跟著受影響：低於約 120 天時，
> 走勢圖的 M60 會因為算不出完整 60 日均值而整條消失。想縮檔案又要保留均線，
> 用 `-days 250` 比 `-days 60` 合理。

> 策略引擎在 `internal/segbacktest/engine.js`（純函式、無 DOM），由
> `internal/segbacktest/engine_script_test.go` 用系統內建的 `jsc` 實際跑過驗證，
> 所以雖然計算在瀏覽器端，規則仍然有單元測試守著。

---

## 設定檔與所有開關（config.yaml）

所有行為集中在 `configs/config.yaml`。多數進階功能採**雙層開關**：

- **`enable_*`**：是否**計算 / 抓取**該功能（關 → 完全不算，對既有輸出零影響）。
- **`show_*`**：是否在報告**顯示**（純顯示，不影響分數 / 排序 / 動作）。
- 另有一個 **master flag** `enable_signal_guardrail_scoring`：唯有它為 `true` 時，
  RS / 新高 / VCP / MomentumFlow 這些 shadow 訊號才會**真的影響**飆股分數；
  否則即使各自 `enable` 也只是 shadow-only（只計算、不改分數，即「觀察模式」）。

四種組合的意義一致（以 `X` 代表功能名）：

| 組合 | 行為 |
|------|------|
| `enable_X: false`（預設） | 完全不計算，欄位為 nil，輸出與沒有這個功能時一致 |
| `enable_X: true, show_X: false` | **有計算、有記錄**（evidence / 快照 / agent 可見），報告不顯示 —— 純觀察模式 |
| `enable_X: true, show_X: true` | 計算 + 顯示 |
| `enable_X: false, show_X: true` | 不計算，報告也沒有東西可顯示 |

> **最後一列只有三對會在啟動時擋下來。** `scanner.Config.Validate()`
> （`internal/scanner/candlestick_attach.go`）目前只強制
> `show_candlestick → enable_candlestick`、
> `show_technical_indicators → enable_technical_indicators` 與
> `show_entry_plan → enable_entry_plan` 這三對，
> 違反時直接 fatal。**其餘的 `show_*` 並不會被檢查**，設成 `true` 而
> `enable_*` 為 `false` 只會安靜地渲染出一個空區塊 —— 那讀起來像「沒有東西可看」，
> 而不是「沒有人去看」。新增雙層開關時要記得自己加進那個 validator，它不會自動繼承。

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
| `show_backtest_insights` | 顯示 | 多一個「🔬 回測洞察」分頁，內容為[區間策略回測面板](#區間策略回測面板backtesthtml)（互動），不影響分數 |

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

### scanner — 進場計畫 EntryPlan（EP-6，Shadow Only；報告區塊 ⑲ 由 EP-8 整合）

「這檔是否值得持有」由掃描器先回答完，之後才輪到「要在什麼價位進場」。EntryPlan 是後者：一個
純運算的 domain 套件（`internal/entryplan`，無網路 / 無檔案 / 無時鐘），由橋接檔
`internal/scanner/entryplan_attach.go` 把掃描結果投影過去，再把算出來的 plan 掛回
`WatchlistEntry.EntryPlan`。它跑在排序**之後**，而且沒有任何東西會把它讀回去。

EntryPlan **不是**第二套「何時買」的分類器：進場語意直接沿用既有的 `WatchAction`
（`PULLBACK_BUY` → PULLBACK、`BREAKOUT_BUY` → BREAKOUT），橋接不重新判斷。
`Action = BUY` 也**不等於** `BUY_NOW` —— 後者還要求進場語意已確定、市場 regime 的政策允許
這種進場、有合法區間、現價落在區間內（或政策明示允許追價且未越過上限）。
另外（EP-6D，使用者決定）：只要 `WatchAction` 已給出進場語意（`PULLBACK_BUY` / `BREAKOUT_BUY`），
而掃描器的主要建議 `Action` 為 `SELL`、`REDUCE`、`TAKE PROFIT` 或 `STOP LOSS`，plan 一律是
`NO_VALID_ENTRY`（決策表新的第 S1 條，理由碼 `STATUS_THESIS_CONTRADICTED`），而且**不產生任何
進場價位**：沒有進場區間、追價上限、失效價、目標價、風險報酬比，`EntryTrace` 的區間 / 追價 /
停損 / 目標計算紀錄也是空的（這些步驟根本不執行）—— 不論現價、regime 或區間為何，所以也不會出現
「賣出」旁邊寫著「等拉回、在 X 買」。plan 裡仍保留的數字只有**市場觀測值**（現價、ATR、MA20 /
MA60 / 底部低點 / 突破價、前一日收盤、20 日均量、還原日齡、估值投影），它們是證據，不是建議。沒有進場語意的股票不受影響（仍是
`INSUFFICIENT_DATA`）。`Action` 為空或未知值時則只拿掉 `BUY_NOW`（改為 `INSUFFICIENT_DATA`，
`STATUS_THESIS_UNCLASSIFIED`），其他狀態與價格不變。決策規則版本目前為 `EP6G-v1`；EP-6G 讓既有
BASE 估值上限在 production EntryPlan 可達，因此屬於可觀察的序列化語意變更（先前的 sell-side
規則版本為 `EP6D-v1`）。之後
在同一個尚未發布的版本內又三度修改規則（併入 `TAKE PROFIT` / `STOP LOSS`、擴大為「不公布價格」、
連 `EntryTrace` 的計算紀錄也不產生），
都未再升版，因為當時沒有任何 plan 被存檔（EP-7 的持久化是之後才實作的）。

| 變數 | 說明 |
|------|------|
| `enable_entry_plan` | 計算並 attach `WatchlistEntry.EntryPlan`（預設 false） |
| `show_entry_plan` | 報告 ⑲「進場計畫」區塊開關（EP-8，預設 false） |

`show_entry_plan: true` 而 `enable_entry_plan: false` 會在**啟動時直接失敗**。

**目前的實際狀態，請照字面讀：**

| 階段 | 狀態 |
|------|------|
| domain engine（`internal/entryplan`） | 已實作 |
| production 接線：`buildWatchlist → attachEntryPlan` | 已接線，**行為證明**：拿掉這個呼叫會讓 `cmd/scanner/entryplan_pipeline_test.go` 的行為測試轉紅（它讀的是回傳結果，不是原始碼文字） |
| production 接線：`main() → buildWatchlist` | **僅結構守衛**：只有 `TestMainCallsTheProductionWatchlistSeam` 看著它，而那是一條 AST 斷言（「main() 裡有這個呼叫」），不是行為證明。呼叫被搬到永遠不會執行的地方它照樣會過。兩層的守衛強度不同，請不要合併理解 |
| shadow 隔離（開 / 關的唯一差異是 EntryPlan 欄位） | 已用結構化 diff 證明，但範圍限定在 `buildWatchlist` 回傳的 `[]WatchlistEntry`、且只跑 offline fixture（`cmd/scanner/shadowdiff_test.go`）；**不涵蓋** report / HTML / analysis-history 這些下游產出。EP-6C 另外在「plan 真的帶價格」的情況下重跑同一份 diff（`TestEntryPlanRemainsShadowOnlyWhenPlansCarryPrices`），因為原本的 fixture 每一檔都是無價的 `INSUFFICIENT_DATA` |
| 證據投影：entry semantic（由既有 `WatchAction` 映射） | **已實作 + 已接線（EP-6C）**，涵蓋 7 個 `WatchAction` 的逐一歸屬；兩個出場動作（`TAKE_PROFIT` / `REMOVE_FROM_WATCHLIST`）明確投影為 UNAVAILABLE，不偽造進場語意 |
| 證據投影：市場 regime | **已實作 + 已接線（EP-6C）**，來源是 `cmd/market-fetch` 寫的 dashboard snapshot（與 AI 解讀共用同一次載入）。橋接本身不推導 regime；沒有 snapshot 就是 UNAVAILABLE，不會退化成中性盤 |
| 證據投影：前一交易日收盤 / 還原日齡 | **已實作 + 已接線（EP-6C）**，從該檔自己的 K 線序列讀取，且序列必須**收在分析日當天**才採用（避免 lookahead） |
| 證據投影：MA60 | **維持 UNAVAILABLE**。理由**不是**「沒人算 60 日均線」—— `internal/scanner` 內已有四處在算（`horizonhint.go:187`、`holdinghorizon.go:122`、`trendext.go:353`、`rotation.go:371`），其中 `horizonhint.go:187` 正是把它當**個股拉回支撐**在用。真正的事實較窄：**沒有任何 producer 把 MA60 這個「價位」publish 出來** —— `indicator.Result` 與 `StockAnalysis` 都沒有 MA60 欄位，四處都是就地算完丟掉（留下的是 setup 標籤 / 斜率 / 乖離 / 布林值）。橋接拒絕成為**第五處**：那會是第五套各自的 warm-up 與零值約定（現有四處的判法就不一致），也會把一個沒有 producer 背書的 level 送給 `entryplan.SelectCenter` 當 MA20 的同級候選。要補請先加在 `internal/indicator` + `StockAnalysis`，並與既有四處（尤其 `horizonhint.go:187`）對齊 warm-up / 零值約定（獨立的 Work Item，尚未編號 —— 指標層；不是 EP-7，EP-7 是持久化） |
| 證據投影：估值上限 | **已實作 + 已接線（EP-6G）**。`cmd/scanner` 在 `loadResearchViews(asOf)` 之後，以同一分析日、同一 K 線序列呼叫純函式 `valuation.BuildEntryPlanEvidence`；只把 availability、BASE target、suitability、asOf 投影進 scanner bridge，scanner 不依賴 healthcheck。只有既有 `ComputeTargetPrice` 的 **BASE** 情境可夾限 Target2；可夾限與否由 EP-4 的 `ValuationSuitability.PermitsCeiling`（`internal/entryplan/input.go`）決定：**除 `UNSUITABLE` 以外的已知代碼**都可夾限，也就是 `SUITABLE`、`CONDITIONAL`、`WEAK`、`INSUFFICIENT_DATA`；`UNSUITABLE`、空值與未知代碼不夾限（不是「只有 SUITABLE 才夾限」）。例如 P/E 連續、觀察窗足夠但沒有可見財報時，`valuation.ClassifySuitability` 給的是 `CONDITIONAL`，仍會夾限；不產生區間、Target1、停損、追價上限或 BUY_NOW，也不改 status / R:R / scanner verdict。未載入、日期不符、非有限或不適用的證據不偽造成 0。S1 sell-side contradiction 仍是 `NO_VALID_ENTRY` 且不公布任何可執行價或 trace 目標；估值目標價在 S1 plan 的 `VALUATION_BASE_TARGET` 證據列也**不帶數值**（狀態 `UNAVAILABLE`、文字 `NOT_SCREENED`），整份 plan JSON 都不會出現該數字。歷史可重建性為 **PARTIAL**：僅限分析日當時已有 valuation archive、可見的 fundamental filing、ratio 對應日原始收盤與 market snapshot 的日期；缺任一項就保留 UNAVAILABLE / INSUFFICIENT_DATA，不能用今天資料回填。這是整合與 PIT 測試，不是 EP-9 回測驗證 |
| 「既有 BUY thesis」守衛：`SELL` / `REDUCE` / `TAKE PROFIT` / `STOP LOSS` → `NO_VALID_ENTRY` 且不產生進場價位（EP-6D） | **已實作 + 已接線**（橋接把 `A.Action` 投影成 `Snapshot.Thesis`；決策表 S1 在進場語意確定後判定，`ComputePlan` 對 S1 的 plan 不計算任何進場步驟，之後 invariant gate 照常檢查），**行為證明到 `AttachEntryPlan` 為止**（沒有另外透過 `buildWatchlist` 餵 SELL 股票的測試；`buildWatchlist → attachEntryPlan` 本身已有行為證明）：決策矩陣逐列測試（矛盾 → S1、未分類 → 只拿掉 BUY_NOW）、經 `ComputePlan` 的 zone / risk 案例矩陣（矛盾 → 無任何可下單價位）、橋接端對 types.go 宣告的 8 個 `Action` 加空值 / 未知值逐一測試；plan 不變式 `BUY_NOW_THESIS_NOT_CONTRADICTED` 與 `THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY`（總數 18 → 20）。S1 的 plan 不執行區間 / 追價 / 停損 / 目標步驟，`EntryTrace` 內這四段紀錄為空；另有測試把 plan 序列化成 JSON，確認這些進場數字都不出現（同一 fixture 不帶 thesis 時則都出現）。S1 plan 保留的是**市場觀察值**（現價、ATR、MA20 / MA60 / base low / pivot、前一交易日收盤、成交量、還原日齡），這些證據列照常帶數值；**估值目標價不是市場觀察而是策略目標**，所以 `VALUATION_BASE_TARGET` 列保留但不帶數值（`UNAVAILABLE` / `NOT_SCREENED`），suitability 列（文字、無數字）照舊。EP-6G 以 `TestSellSidePlanJSONCarriesNoValuationTarget` 對四個賣出類 Action 各自序列化整份 plan，確認植入的估值數字不出現（`BUY` 對照組則出現）。只在 `enable_entry_plan: true` 時生效 |
| `DecisionResult` 欄位 exact allowlist（EP-6D） | **已實作，僅結構守衛**：欄位集合必須剛好是 `Status` / `Rule` / `Reasons`，雙向比對；多加 `Confidence` 之類欄位會紅。證明的是型別形狀，不是運算 |
| architecture / purity 守衛（EP-6D） | **已實作，僅結構守衛（AST / `go list`，是早期警示，不是行為證明）**：`internal/entryplan` 的直接 import 必須剛好等於 allowlist、禁止依賴 `internal/valuation`、只有 `input.go` / `series.go` 可 import `time` 且只能用 `time.Parse` / `time.Time`；全 repo（`cmd/`、`internal/` 非測試檔）只有 `AttachEntryPlan`（寫入）、EP-7 的 `research.buildEntryPlanEvidence`（唯讀，只投影成 evidence 列）與 EP-8 的 `report.entryPlanView`（唯讀，只格式化成 ⑲ 顯示字串）這三個函式寫得出 `.EntryPlan`。看不到經由 reflection / `encoding/json` 的讀取 |
| 橋接不做 I/O（EP-6D） | **已實作，僅結構守衛**：`entryplan_attach.go` 的 import、對 `fetcher` / `model` / `math` 的 selector、以及每一個函式呼叫都必須在固定集合內（同檔宣告的函式、`len` / `float64`、少數依名稱比對的方法）。不做型別檢查、也不檢查被允許函式的內部 |
| status invariant 的 gate 行為（EP-6E） | **已補行為測試**：EP-5 的五條 status invariant（`STATUS_HAS_REASON` / `BUY_NOW_HAS_ZONE` / `BUY_NOW_PRICE_AUTHORISED` / `TOO_EXTENDED_ABOVE_CEILING` / `TOO_EXTENDED_KEEPS_ZONE`）先前只證明「會觸發」，沒證明 gate 觸發後做什麼 —— 從 `enforce.go` 的 `statusInvariants` 移除任一條時測試仍全綠。`internal/entryplan/statusgate_test.go` 對每條各植入「恰好一條」違規的 plan，斷言 gate 的完整輸出（撤回判定為 `INSUFFICIENT_DATA`、不撤價格、stamp `STATUS_WITHDRAWN`），並以合法對照組確認不被誤傷。突變驗證（runner 逐一核對目標檔 md5 已改變、改的是程式碼不是註解、可編譯、還原後 md5 相符）：五條成員移除、各條述詞弱化、以及 EP-6D 兩條 thesis invariant 的突變，共 32 個全數 KILLED。僅涵蓋手工植入的 plan；`ComputePlan` 本身不會產生這些違規 |
| 研究庫持久化（EP-7） | **已實作，reviewer 已核准（尚未 commit）**。不新增資料表、不改 schema（仍是 R13 schema version 2）：plan 以 evidence 列寫進既有的 `scan_runs → stock_snapshots → evidence`，category `entry_plan`，只在 `research.enabled` 與 `enable_entry_plan` 都開時才會有列。key 固定為 `ep_status`、`ep_rule_version`（取自 `Plan.RuleVersion`）、`ep_policy`（`EntryTrace.Policy.Name`，政策代碼文字）、`ep_zone_low` / `ep_zone_high`（`IdealEntry.Low/High`）、`ep_max_chase`、`ep_invalidation`、`ep_target_1`、`ep_target_2`（最終公布的 Target2，估值夾限後的值）、`ep_rr_ratio`（`RiskReward.Ratio`）。**缺值就是沒有這個 key**：欄位為 nil 時不寫 0、不寫空字串；`enable_entry_plan: false`（plan 為 nil）時完全沒有 `ep_*` 列；S1 賣出矛盾的 plan 只有 status / version / policy 三列、沒有任何價位列。不寫 `EntryTrace` 的計算數字、理由碼、caveats、confidence、`InvariantCheck`。**身分必須相符**：只有 `Plan.Symbol` 等於快照代號、且 `Plan.AsOf` 等於快照交易日時才寫入；任一不符就**零筆** `ep_*` 列並記 log（該快照與其他 evidence 照常寫入）。不改寫 `Plan.AsOf`、不改存到 K 棒日期、不把前一日的 plan 掛到今天的快照、不另造快照。`cmd/scanner` 的研究庫交易日是 `-date`（未給則為執行當天），plan 的 `AsOf` 則是該檔最後一根 K 棒日期，所以週末、假日、開盤前執行時通常沒有 `ep_*` 列——這是**刻意的**。**不做盤中 / 收盤判斷**：持久化不讀時鐘、不檢查是否收盤；盤中執行時若 plan 與快照同代號同日，就照樣寫入。這一層只是記錄器；EP-9 回測（尚未實作）要維持自己的「只用已完成 K 棒 / T+1」契約，這裡的決定不放寬它。測試：`TestEntryPlanAsOfMismatchPersistsNoEntryPlanRows`、`TestEntryPlanSymbolMismatchPersistsNoEntryPlanRows`、`TestEntryPlanSameIdentityPlanIsPersistedWithoutSessionGating`（皆走真實 `RecordScan` 與 SQLite）；「沒有時鐘」另有 AST 結構守衛 `TestEntryPlanPersistenceHasNoClockOrSessionReference`，只看 `entryplan_evidence.go` 與 `RecordScan` 的 watchlist 迴圈、以名稱比對，不是行為證明。結構守衛（`internal/research/entryplan_evidence_guard_test.go`，AST / `go list`）確認沒有 production 程式讀回這些 key；行為測試只證明寫入不改變快照、scanner decision 與其他 evidence。寫入不代表回測或驗證過 |
| 報告呈現 ⑲ 進場計畫（EP-8） | **已整合，待 review（尚未 commit）**。`show_entry_plan: true` 時，**只有 plan 公布「買方進場型態」的股票**（`ENTRY_SEMANTIC` 證據列為 `AVAILABLE` 且為 `PULLBACK` / `BREAKOUT`）的觀察清單卡片多一個「⑲ 進場計畫（Shadow Only，不改變 BUY／WATCH／SELL）」區塊；plan 為 nil 的股票不顯示該區塊，不偽造 plan。**出場動作**（`WatchAction` 為 `TAKE_PROFIT` / `REMOVE_FROM_WATCHLIST`，橋接刻意不給進場型態）以及掃描器已回答但未指明型態的股票（`PREPARE_ENTRY` / `WATCH_CLOSELY` / `WAIT`，證據列 `INSUFFICIENT_DATA` + `UNKNOWN`）**都不顯示 ⑲**（使用者決定，解決 `entryplan_attach.go` 的 EP-8 TODO）。這是**純顯示層**規則：橋接照常投影、`EntryPlan` 不變成 nil、EP-7 的 `ep_*` 列照常寫入（`TestEntryPlanExitActionEntryStillPersistsItsRows` 走真實 `RecordScan`）。反之，EP-6D/6G 的**賣出矛盾（S1）**是另一回事：掃描器已給買方型態、主要 `Action` 為賣出類，⑲ 仍顯示 `NO_VALID_ENTRY` 與 `—`。內容全部取自已算好的 `WatchlistEntry.EntryPlan`：狀態碼（6 種 canonical 狀態原碼照印，另附中文說明；未知碼明示「未知」）、理想進場區 `Low – High`、追價上限、想法失效價、Target1、Target2（只印最終公布值；被撤回就是 `—`，不回填 trace 的原始值或估值目標價）、R:R（只印公布的 `Ratio`）、Policy（`EntryTrace.Policy.Name`）、RuleVersion、Confidence、理由碼（依 plan 原順序；未對應的碼照印原碼）、caveats。缺值一律 `—`，不印 0。「現價位置」標籤是唯一在報告端推導的東西，只供顯示：現價取自 plan 自己的 `CURRENT_PRICE` 證據列（不退回 `A.Close`），比較方式與 `DecideStatus` 相同（`decide.go:626-627`、`636`、`652`，上下緣含、等於追價上限不算超過）。賣出類 `Action` 的 plan 在 ⑲ 只顯示 `NO_VALID_ENTRY` 與 `—`（⑰ 估值區塊照舊）。**不是回測驗證**、不影響 BUY／WATCH／SELL、分數、排序或 `EntryStatus`；舊版「④ 價位計畫」的進場區／停損價／停利區與表格欄位**不變**（收斂是 EP-10）。**呈現覆蓋率（presentation coverage）**：依本節下方 EP-6C／EP-6D 的一次性實測，當時 `.cache` 的 1,993 檔裡只有約 **11 檔**具備進場語意（其餘 1,982 檔的 `WatchAction` 是 `WAIT` / `WATCH_CLOSELY` / `PREPARE_ENTRY` / `TAKE_PROFIT` / `REMOVE_FROM_WATCHLIST`），所以 ⑲ 大約也只會顯示這 11 檔。這個數字是**從該次量測推得**、EP-8 未另行重量，且與那張表一樣**無法用 `go test` 重現、沒有任何測試守著**，`.cache` 或掃描器設定一變就會漂移。它衡量的是「有多少檔會顯示這個區塊」，**不是資料完整度**（缺的不是 MA60 或估值，而是掃描器根本沒指出拉回買或突破買），**更不是策略有效性**（EP-9 回測驗證尚未進行）。**不得為了把這個數字做高而放寬顯示規則**：沒有進場想法的股票顯示 `INSUFFICIENT_DATA` 區塊，等於把「還沒有進場想法」講成「想法有了但證據不足」，那是兩件事。證據：`internal/report/report_entryplan_test.go` 的輸出測試（含同一份掃描結果 OFF / ON 產生 HTML、剝除 ⑲ 區塊與其樣式後逐位元組相同，以及渲染前後 plan 深度比對）；報告端不呼叫 `ComputePlan` / `DecideStatus` / 價位步驟函式的 AST 守衛 `report_entryplan_guard_test.go` 是**僅結構守衛**；`main.go` 的旗標接線也只有 AST 守衛。突變稽核 `docs/EP8_MUTATION_AUDIT.md`（`go run ./scripts/ep8_mutation` 產生） |
| 進場建議是否可用於實際下單 | **尚未驗證**：EP-5 的決策表是 heuristic、沒有任何 backtest 佐證；EP-6E、EP-6F、EP-6G（估值證據投影）、EP-7（持久化）已實作並經 reviewer 核准（尚未 commit）；EP-8（報告呈現）已整合、待 review；EP-9（回測驗證）、EP-10（收斂）仍未完成 |

**EP-6C 之後打開 `enable_entry_plan` 會發生什麼（一次性實測，不是估計，也不是回測）**：把
`.cache` 現有的 1,993 檔（全市場當成觀察清單跑、單一日期）餵進同一條 attach —
（這張表量的是 **plan 的計算結果**，不是 ⑲ 的顯示範圍：即使某一列是 `INSUFFICIENT_DATA`，只要
該檔沒有進場語意，EP-8 就完全不顯示 ⑲——見上面那列的「呈現覆蓋率」。）

> ⚠️ 這張表**無法用 `go test` 重現，也沒有任何測試守著它**。它是一次性量測（依賴本機 `.cache`
> 的當時內容），不是 regression 的一部分。掃描器的分級規則、`.cache` 內容或 EP-5 決策表一變，
> 表上的數字就會漂移而**不會有任何測試轉紅** —— 要用它做決策前請重新量一次。

| 市場 regime | 有可執行價格 | 仍 `INSUFFICIENT_DATA` |
|------|------|------|
| 沒有 snapshot | 0 | 1,993 |
| `BULL` | 11（`BUY_NOW` 2 / `TOO_EXTENDED` 5 / `WAIT_PULLBACK` 4） | 1,982 |
| `SIDEWAYS` / `DISTRIBUTION` | 11（其中 9 檔 `WAIT_PULLBACK`，另 2 檔有價但狀態仍 `INSUFFICIENT_DATA`） | 1,984 |
| `BEAR` | 0（11 檔為 `NO_VALID_ENTRY` —— 這是「看過了，沒有邊」的結論，不是缺資料） | 1,982 |

**EP-6D 的量測（同樣是一次性實測，沒有測試守著）**：量的是「帶 thesis」與「thesis 視為不矛盾」
兩種決策逐檔比對後的差異；絕對數字依賴當時的 `.cache`。

- **較早的 `.cache`（上表那一份）**：用 `configs/config.yaml` 的 scanner 設定，上表每一格數字
  完全不變，7 種 regime 狀態下差異都是 0 檔（`PULLBACK_BUY` 的 11 檔 `Action` 都是 `HOLD` / `WATCH`）。
- **`.cache` 於 2026-09-14 10:04 前後被重新抓取之後**（非 EP-6D 所為）：上表已漂移 —— 同一設定下
  `BULL` 變成 `BUY_NOW` 2 / `TOO_EXTENDED` 2 / `WAIT_PULLBACK` 4，`BEAR` 變成 `NO_VALID_ENTRY` 8；
  但「帶 thesis」與「不矛盾」的差異在 7 種 regime 狀態下**仍是 0 檔**。
- **這條守衛不是空轉**：同一份新 `.cache` 改用空的 scanner 設定（所有選配層關閉）時，有 9 檔
  帶進場語意（`PULLBACK_BUY` / `BREAKOUT_BUY`）的股票 `Action` 是 `SELL`（2221、2880、4198、4908、6173、6885）或 `REDUCE`
  （2033、2883、6620）。它們在無 snapshot / `BULL` / `BULL_PULLBACK` / `SIDEWAYS` / `DISTRIBUTION` /
  `UNKNOWN` 下全部變成 `NO_VALID_ENTRY`（`BULL` 下原本是 `BUY_NOW` 5、`TOO_EXTENDED` 3、
  `WAIT_PULLBACK` 1），`BEAR` 下狀態本來就是 `NO_VALID_ENTRY`、改由 S1 判定；其餘股票不動。
  這 9 檔的 plan 序列化成 JSON 後，不含任何原本（thesis 視為不矛盾時）的區間、追價上限、失效價或
  目標價數字（剛好等於某個保留的市場觀測值者除外），四段步驟紀錄皆為空。資料中沒有任何一檔是 `TAKE PROFIT` / `STOP LOSS`。

剩下 1,982 檔缺的**不是** MA60 也不是估值，而是**進場語意**：掃描器對它們給的
`WatchAction` 是 `WAIT` / `WATCH_CLOSELY` / `PREPARE_ENTRY` / `TAKE_PROFIT` /
`REMOVE_FROM_WATCHLIST`，本來就沒有指出要拉回買還是突破買。這是正確輸出，不是待調參數。

打開它今天的意義，仍然只是讓 plan 以 shadow evidence 的身分存在；它保證不會動到任何既有輸出
（Score / Action / 飆股分數 / watch_action / 排序 / 停損 / 既有 shadow 欄位）。只有再打開
`show_entry_plan` 時，使用者才會在報告 ⑲ 看到它，而那仍是未經回測驗證的規劃證據，不是下單指令。

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
