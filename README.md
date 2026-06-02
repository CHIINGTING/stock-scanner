# stock-scanner

每日收盤後自動掃描台股，產生含評分、交易建議與價格目標的三分頁 HTML 報告。

## 三分頁報告

| 頁面 | 說明 |
|------|------|
| 📊 市場掃描 | 全市場評分 Top N，依分數排序 |
| 💼 Portfolio | 持股分析：成本、損益、建議操作 |
| 👁 Watchlist | 觀察股分析：建議進場時機 |

## 六段式建議

| 建議 | 說明 |
|------|------|
| **STRONG BUY** | 多指標共振，強力進場訊號 |
| **BUY** | 技術面偏多，可考慮建倉 |
| **WATCH** | 有潛力但尚未確認，持續觀察 |
| **HOLD** | 中性，維持現況 |
| **REDUCE** | 偏空或持倉獲利已高，考慮減倉 |
| **SELL** | 技術面轉弱或虧損擴大，執行停損 |

## 評分系統（0–100）

| 指標 | 滿分 | 邏輯 |
|------|------|------|
| RSI(14) | 25 | <30 超賣得高分，>70 超買扣分 |
| MA20 趨勢 | 25 | 連續上揚天數越多得分越高 |
| KDJ | 25 | 黃金交叉最高，死亡交叉大扣分 |
| 成交量比 | 15 | 爆量得分，縮量扣分 |
| 布林通道 | 10 | 收斂突破 / 擴張方向 |

### Portfolio 額外調整

- 浮盈 ≥ 30% 且技術偏弱 → 自動升級為 **REDUCE**
- 虧損 ≥ 20% 且無轉強訊號 → 自動升級為 **SELL**

## 價格目標計算（ATR 基礎）

```
進場價 = 當日收盤
停損   = max(布林下軌 − 0.5×ATR, 進場 − 2×ATR)
目標1  = max(進場 + 2×風險, 布林上軌)
目標2  = 進場 + 3.5×風險
```

## 使用方式

```bash
# 1. 安裝依賴
go mod tidy

# 2. 設定持股清單（參考 stocks.yaml）
cp stocks.yaml my_stocks.yaml
# 編輯 my_stocks.yaml

# 3. 只分析 Portfolio + Watchlist（快速，< 1 分鐘）
make run-fast

# 4. 完整市場掃描 + Portfolio（~30 分鐘）
make run

# 5. 指定日期
make run-date DATE=2025-12-31

# 6. 指定自訂清單
make run-stocks STOCKS=my_stocks.yaml
```

報告輸出：`reports/report_YYYYMMDD.html`

## stocks.yaml 格式

```yaml
portfolio:
  - code: "5483"
    name: "中美晶"    # 選填，留空則用 Yahoo 名稱
    cost: 165         # 成本價（元）
    shares: 2000      # 持股張數（股）

watchlist:
  - code: "2337"
    name: "旺宏"
  - code: "2408"
```

## 專案結構

```
stock-scanner/
├── cmd/scanner/main.go              # 程式入口
├── internal/
│   ├── fetcher/
│   │   ├── types.go                 # Candle, StockData
│   │   ├── fetcher.go               # 並行抓取（market / portfolio / watchlist）
│   │   ├── twse.go                  # TWSE 上市股票清單
│   │   ├── yahoo.go                 # Yahoo Finance 日線 OHLCV
│   │   └── portfolio.go             # 讀取 stocks.yaml
│   ├── indicator/
│   │   ├── ma.go                    # SMA、MA20 趨勢標籤
│   │   ├── kdj.go                   # KDJ 隨機指標
│   │   ├── bollinger.go             # 布林通道
│   │   ├── volume.go                # 量比
│   │   ├── rsi.go                   # RSI（Wilder 平滑法）
│   │   ├── atr.go                   # ATR（平均真實範圍）
│   │   └── calculator.go            # 整合計算入口
│   ├── scanner/
│   │   ├── types.go                 # Action, StockAnalysis
│   │   ├── scorer.go                # 評分引擎 + 價格目標
│   │   └── scanner.go               # 市場掃描 / Portfolio / Watchlist
│   └── report/
│       └── report.go                # 三分頁 HTML + 終端摘要
├── stocks.yaml                      # 持股與觀察清單
├── configs/config.yaml              # 參數設定
└── Makefile
```

## 資料來源

- **股票清單**：[TWSE Open API](https://openapi.twse.com.tw/)
- **歷史 OHLCV**：Yahoo Finance（`{code}.TW`，6 個月日線）

## 注意事項

- 本工具**僅供研究參考，非投資建議**。
- Yahoo Finance 有請求速率限制，`request_delay_ms` 建議 ≥ 200ms。
- 若 TWSE API 在非交易日返回空資料，市場掃描會無結果，可加 `--no-market` 跳過。
