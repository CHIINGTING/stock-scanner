# SPEC R11 — AI Analysis Layer（OpenAI，shadow-only）

> **命名注意**：本文件的 `R11` 是 **release 編號**。它與 Market Dashboard regime 決策表裡的
> rule id `R0`…`R11`（`internal/market/model/structure.go`）是**完全無關的兩套編號**，
> 只是字串碰巧相同。grep `R11` 時兩者都會命中。
>
> 編號依據：`docs/` 現有最大的 SPEC 編號是 `SPEC_R10_2_CANDLESTICK_SHADOW.md`，故本次為 R11。
> （Market Dashboard 依核准決策刻意不佔 R 編號，採領域導向命名，不影響此序列。）

Status: **實作完成**。Date: 2026-08-10。

---

## 1. 目的與邊界

把 scanner **已經算完**的證據，交給 OpenAI 轉成好讀的多空解讀。它是一層
**解釋層（Explanation / Interpretation Layer）**，不是訊號來源。

### 1.1 紅線

AI **絕對不得**影響下列任何一項：

```
technical score / institutional score / market score / Market Regime / Stage
BUY / WATCH / SELL / WatchAction / ranking / recommendation / position sizing
```

實作上由三件事保證，而非靠自律：

1. `WatchlistEntry.AI` 是**專屬欄位**，與 `ShadowSignals` 分開——後者是 C6b guardrail
   scoring 唯一會讀的結構，AI 不在其中，因此**在型別上就進不了計分路徑**。
2. `AttachAI` 是 **post-pass**：在 `computeRocket` 之後執行，此時分數、action、排序都已定案。
3. 有測試枚舉「AI 成功 / 失敗 / 停用」三種情況，斷言 entry 的 score、action、排序**位元相同**。

### 1.2 Fail-open（最重要的一條）

```
scanner → deterministic result → AI attempt
                                   ├─ 成功 → 掛上分析
                                   └─ 失敗 → 標記 unavailable
                                 → report 照常產生
```

以下任一情況，掃描與報告都必須正常完成：

```
沒有 OPENAI_API_KEY ／ timeout ／ 429 ／ 5xx ／ invalid JSON ／ AI disabled
```

AI 失敗**永遠不會**讓 scanner 或 CI workflow 失敗。

---

## 2. Secret 與 Config

### 2.1 Token 只從環境變數取得

```bash
export OPENAI_API_KEY="sk-..."
```

**禁止**寫進 YAML / repo / 原始碼 / test fixture / HTML report；**禁止** log。
client 只在送出請求時把它放進 `Authorization` header，並且從不回寫任何結構。
`ai.Config` **沒有 APIKey 欄位**——這樣它連被序列化進報告或快照的可能性都沒有。

### 2.2 Model 走既有 config 系統

沿用 `internal/scanner.Config` 既有的兩層開關 + 巢狀 struct 風格（與 `Institution`、
`News`、`KDJ` 相同），**沒有另造設定框架**：

```yaml
scanner:
  enable_ai: false          # 總開關，預設 false
  show_ai: false            # 顯示開關，預設 false
  ai:
    model: "gpt-5.6-luna"
    timeout_sec: 30
    max_stocks: 12
    temperature: 0.2
    base_url: ""            # 留空 = OpenAI 官方；僅供測試覆寫
```

`enable_ai=false`（預設）時 analyzer 完全不執行，`WatchlistEntry.AI` 恆為 nil，
輸出與現況位元相同。

---

## 3. 提供給模型的證據

只送**已經 derived 的結構化欄位**，不送原始價格序列，也**不重算任何 scanner 已有的指標**：

```
symbol / name / price
stage（RocketStage）/ watch_action / rocket_score / explosion_prob
technical_score / best_four_point
ma20 / ma60 / ma120 距離（%）
volume_ratio
consolidation（型態 + 天數）
sector / sector_flow / sector_stage
institutional：外資 / 投信 / 自營（若 enable_institution 開啟）
market_regime / market_score（若快照存在）
risk_label / risk_warning / reasons（scanner 既有）
```

實際欄位以 repo 現有資料為準；缺的就不送，**不補值**。

---

## 4. Output schema

輸出格式由 Responses API 的 **Structured Outputs** 保證：request 帶
`text.format = {"type":"json_schema", "name":…, "schema":…, "strict":true}`，
API 端就會強制回覆符合 schema。**不用 regex 從自然語言硬拆**：

```json
{
  "summary": "…",
  "bull_case": ["…"],
  "bear_case": ["…"],
  "risk_flags": ["…"],
  "confidence": 0.76
}
```

JSON parse 或 schema validation 失敗 → **AI unavailable**，不做任何 fallback 推論。
`confidence` 是**模型對自己解讀的把握**，明確**不是**交易信心，報告文案會寫明。

---

## 5. Prompt 原則

System instruction 明確聲明：

```
You are analyzing evidence produced by a deterministic stock scanner.
Do not generate trading instructions.
Do not override the scanner's signal.
Do not invent missing financial data.
Only interpret the supplied evidence.
Clearly distinguish bullish and bearish evidence.
```

另外禁止：假裝知道未提供的新聞、自行補財報／法人數字、推測即時股價、查網路。

---

## 6. 成本控制

```
one stock → max one request
```

- 只分析**候選股票**：`WatchAction` 屬於可操作訊號（`PREPARE_ENTRY` /
  `BREAKOUT_BUY` / `PULLBACK_BUY`），依 `RocketScore` 由高到低取前 `max_stocks` 檔。
- 同一次 run 內以 symbol 去重，不會重複請求。
- 沿用 scanner 既有的候選概念，不另建一套過濾。

---

## 7. Report

飆股候選卡片新增 **⑭ AI 分析** 區塊，沿用既有 `wl-sec` / `wl-note` 樣式，不重新設計版面。
`show_ai=false` 或 AI 不可用時**整段不 render**（與 R8-4/R10-2 的處理一致）。
區塊固定帶一行 shadow 標註：不影響掃描分數與買賣訊號。

---

## 8. 架構

```
internal/ai/
    model.go      Config / Evidence / Analysis / Status（零外部相依）
    client.go     OpenAI Responses API over net/http
    prompt.go     system instruction + evidence 序列化
    analyzer.go   候選挑選、去重、逐檔分析、fail-open

internal/scanner/ai_attach.go   post-pass，沿用既有 Attach* 慣例
```

API 用 **OpenAI 官方 Responses API**（`POST /v1/responses`）——官方對新專案推薦的介面。
與舊 Chat Completions 有兩處關鍵差異，實作都已對齊：

1. **輸出 schema 放在 `text.format`**（`type: "json_schema"` + `strict: true`），
   不是 `response_format`。Responses API 不認得後者，誤用會靜默失去保證。
2. **回覆是 typed output 陣列**，不是單一 choice。`reasoning` / `function_call` /
   `message` 是不同 item type 且順序不保證，因此文字要**依 type 走訪**取出
   （`output[].content[].type == "output_text"`），不能取 `output[0]`。
   `refusal` part 會被辨識為「模型拒答」，與「沒有回覆」分開處理；
   `status == "incomplete"` 視為失敗（截斷的 JSON 仍可能合法但內容殘缺）。

HTTP 只用 `net/http`，**未新增任何相依**（`go.mod` 仍只有 `yaml.v3` 與 `x/text`）。

`temperature` 以指標序列化：config 設 `0` 時整個欄位不送出，作為「模型不接受
temperature」時的逃生口。

---

## 9. 已知限制

1. 成功路徑無法在無 key 的環境端到端驗證。request / response 結構依官方
   Responses API 與 Structured Outputs 文件實作，解析由 `httptest` fixture 覆蓋
   （含 reasoning item 前置、refusal、incomplete、多段 output_text 串接）。
2. `confidence` 未經校準，且模型自評本來就不可靠——僅作閱讀輔助。
3. 第一版不做跨 run 的持久化快取；同一 run 內以 symbol 去重已足夠。
