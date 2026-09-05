package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/deep-huang/stock-scanner/internal/ai"
)

// The health judge: the ONE place a language model is involved, and the narrowest possible
// place it could be.
//
// # What it is allowed to do
//
// It receives evidence this package has already finished computing, and it writes prose about
// it. That is the entire job. Every number on the page — price, EPS, revenue, margins, P/E,
// the P/E percentile, the target prices, the upside, the institutional nets — was produced by
// a deterministic path and was already final BEFORE this file ran. The judge cannot alter one,
// because it is handed rendered strings and returns a struct with nowhere to put a number.
//
// # Two mechanisms, because the instruction alone is not a control
//
// A prompt saying "do not state figures" is a request. These are the enforcement:
//
//	the schema   strict json_schema with additionalProperties:false and every field required.
//	             The reply has no numeric field at all — confidence is an ENUM, not a score —
//	             so there is no slot a figure could land in.
//	the scrubber prose is the remaining channel: a model can always write "目標價 1,100 元"
//	             inside a sentence. proseIsClean rejects any reply whose prose carries a
//	             digit, and the request tells the model plainly to use 月線 / 季線 rather than
//	             20 日 / 60 日 so it has a way to comply.
//
// The digit ban is blunt and it costs something real: the judge cannot say "20 日均線", and a
// genuinely harmless sentence can be rejected for containing a year. That is the intended
// trade. A rejected reply costs one AI section; a laundered figure — a number the model
// invented, wearing the page's authority — costs the reader money.
//
// # What the scrubber does NOT catch, stated plainly
//
// Chinese numerals go straight through: 「目標價約一千一百元」 contains no digit. So does any
// spelled-out figure in any language. The scrubber is defence in depth, NOT the guarantee.
//
// The guarantee is structural and does not depend on the scrubber at all: the schema has no
// numeric field, the AI block is merged after every figure is already final, and no code path
// reads AI output back into a value. A model that writes 一千一百 has written prose — wrong,
// possibly embarrassing prose, but prose. It has not changed a number on this page, and it
// has no route by which it could.
//
// # It cannot fail the request
//
// No key, feature off, timeout, 429, 5xx, unparseable body, schema violation, dirty prose:
// every one of them returns an AIBlock carrying a status and a reason. None returns an error.
// The health check is answered from deterministic evidence whether or not a model was reachable.

// JudgeConfig is the AI block of the health-check config.
//
// It has NO API key field, deliberately and for the same reason internal/ai has none: the
// token is read from the environment at request time and never stored on a struct, so it
// cannot reach a report, a snapshot, a JSON response or a log line by accident.
type JudgeConfig struct {
	// Enabled gates the whole judge. False → StatusDisabled, which is a decision, not a
	// data fact, and is reported as such.
	Enabled bool `yaml:"enabled"`
	// Model is the OpenAI model id. It is configuration, not a secret.
	Model string `yaml:"model"`
	// TimeoutSec bounds one request. Zero → 30.
	TimeoutSec int `yaml:"timeout_sec"`
	// Temperature is passed through; low keeps the reading close to the evidence.
	Temperature float64 `yaml:"temperature"`
	// BaseURL overrides the API root so tests can point at httptest. It is not a gateway
	// hook, and no company-internal endpoint belongs here.
	BaseURL string `yaml:"base_url"`
}

// Defaulted fills zero values, matching the convention used across this repo's configs.
func (c JudgeConfig) Defaulted() JudgeConfig {
	if c.Model == "" {
		c.Model = "gpt-5.6-luna"
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 30
	}
	if c.Temperature < 0 {
		c.Temperature = 0
	}
	return c
}

// Timeout is the per-request bound.
func (c JudgeConfig) Timeout() time.Duration {
	return time.Duration(c.Defaulted().TimeoutSec) * time.Second
}

// Confidence is how sure the model is. It is an ENUM rather than a score because a number
// here would be the one numeric field on the reply, and the first thing a UI would render
// beside real figures as though it were one of them.
const (
	ConfidenceLow    = "LOW"
	ConfidenceMedium = "MEDIUM"
	ConfidenceHigh   = "HIGH"
)

// AIBlock is the model's reading. Every field is prose.
type AIBlock struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Model is which model spoke, recorded so a later reader can tell whether a change in
	// tone came from the evidence or from a model swap.
	Model string `json:"model,omitempty"`

	Summary    string   `json:"summary,omitempty"`
	BullPoints []string `json:"bull_points,omitempty"`
	BearPoints []string `json:"bear_points,omitempty"`
	RiskNotes  []string `json:"risk_notes,omitempty"`
	// Invalidators are what would overturn the reading — the question a page owes anyone it
	// has just given a view to.
	Invalidators []string `json:"invalidators,omitempty"`
	// DataGaps is what the model could not judge because the evidence was missing. It exists
	// so "the model did not mention the fundamentals" and "there were no fundamentals" stay
	// distinguishable.
	DataGaps   []string `json:"data_gaps,omitempty"`
	Confidence string   `json:"confidence,omitempty"`

	// Disclaimer states the boundary on the page itself, not only in a doc.
	Disclaimer string `json:"disclaimer,omitempty"`
}

const judgeDisclaimer = "本段為 AI 對既有 evidence 的文字解讀，不產生也不修改任何數字；" +
	"頁面上的所有數值皆由程式在呼叫模型之前計算完成。"

// judgeSchema is the strict reply shape.
//
// There is no numeric property anywhere in it. That is the point: strict mode forbids
// additional properties, so a figure has no field to arrive in.
func judgeSchema() map[string]any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":      map[string]any{"type": "string"},
			"bull_points":  strArray,
			"bear_points":  strArray,
			"risk_notes":   strArray,
			"invalidators": strArray,
			"data_gaps":    strArray,
			"confidence": map[string]any{
				"type": "string",
				"enum": []string{ConfidenceLow, ConfidenceMedium, ConfidenceHigh},
			},
		},
		"required": []string{"summary", "bull_points", "bear_points", "risk_notes",
			"invalidators", "data_gaps", "confidence"},
		"additionalProperties": false,
	}
}

type judgeReply struct {
	Summary      string   `json:"summary"`
	BullPoints   []string `json:"bull_points"`
	BearPoints   []string `json:"bear_points"`
	RiskNotes    []string `json:"risk_notes"`
	Invalidators []string `json:"invalidators"`
	DataGaps     []string `json:"data_gaps"`
	Confidence   string   `json:"confidence"`
}

const judgeInstruction = `你是一位台股研究員，正在為一份「個股健檢」頁面撰寫文字判讀。

【最重要的規則】
你不得產生、猜測或補完任何金融數字。頁面上的每一個數值——股價、EPS、營收、年增率、
毛利率、營益率、本益比、股價淨值比、殖利率、法人買賣超、技術指標、目標價、上檔空間、
安全邊際、財報日期——都已經由程式計算完畢，你看到的就是最終結果。你的工作是「解釋」，
不是「提供資料」。

因此你的回覆中「不得出現任何阿拉伯數字」。
需要指涉期間時請用文字：月線、季線、半年線、年線、上一季、去年同期、近期、本季。
不要寫「20 日均線」，寫「月線」。不要寫「本益比 18 倍」，寫「本益比位於歷史區間偏低處」。
不要重述數字，指出它代表什麼。

【證據的完整性】
標示為 INSUFFICIENT_DATA / UNAVAILABLE / NOT_IMPLEMENTED / DISABLED 的欄位代表「沒有資料」，
不代表「數值為零」，也不代表「表現不好」。不得就這些欄位做出方向性判斷，
請把它們列進 data_gaps。

【新聞】
只能談 evidence 中確實出現的新聞。不得提及任何未列出的事件、傳聞或消息。

【進場評估】
頁面上的進場評估結論由程式規則決定，已經寫在證據裡。你可以解釋它、指出它的前提，
但不得改變它，也不得在文字中給出不同的結論。

【估值樣本品質】
「估值樣本品質」（HIGH／MEDIUM／LOW／INSUFFICIENT）同樣由程式的確定性規則判定，
規則版本已寫在證據裡。你可以引用它來說明「目標價可以計算，但歷史估值樣本涵蓋率偏低」，
但不得改變這個等級、不得自行重算、不得提出不同的門檻或自己的信心度判斷。
它評估的是「資料證據的充分程度」，不是「本益比適不適合用來評價這家公司」。

【本益比估值模型適用性】
「本益比估值模型適用性」（SUITABLE／CONDITIONAL／WEAK／UNSUITABLE／INSUFFICIENT_DATA）
同樣由程式的確定性規則判定，規則版本與理由代碼都已寫在證據裡。
你可以引用它來解釋，例如「目標價可以計算，但歷史本益比只在近期部分期間存在，
因此這個以本益比為基礎的目標價不宜視為高信心的長期估值錨」。

但你不得：改變這個判定、自行重新計算、自行提出新的本益比門檻、
自行產生每股盈餘或目標價、自行改用其他估值模型（例如股價淨值比、EV/EBITDA）、
或建議系統改用其他模型。本 repo 目前只實作本益比一種估值模型，
提出替代模型的數字就是憑空捏造。

UNSUITABLE 的意思只有一個：「以目前證據，本益比不適合作為主要估值錨」。
它不代表公司不好、不代表股價偏貴、不代表高風險、也不代表賣出。不得作這些延伸。

【欄位】
summary       一段話總結這檔股票目前的處境。
bull_points   支持買進的理由，每點一句，必須對應到 evidence 中的事實。
bear_points   不支持買進的理由，同上。
risk_notes    需要留意的風險。
invalidators  什麼情況會推翻上述判讀。
data_gaps     因為缺資料而無法判斷的部分。
confidence    LOW / MEDIUM / HIGH —— 依「證據完整度」而非「看多程度」給。

沒有內容的陣列請回傳空陣列，不要編造內容來填滿。`

// Judge asks a model to read finished evidence.
type Judge struct {
	Cfg    JudgeConfig
	Client *ai.Client
	// HasKey reports whether a key is available. Injectable so a test can exercise the
	// no-key path without touching the process environment.
	HasKey func() bool
}

// NewJudge builds a judge from config.
func NewJudge(cfg JudgeConfig) *Judge {
	cfg = cfg.Defaulted()
	return &Judge{
		Cfg:    cfg,
		Client: ai.NewClient(cfg.BaseURL, cfg.Timeout()),
		HasKey: ai.HasAPIKey,
	}
}

// Assess returns the model's reading of a finished Health.
//
// It NEVER returns an error. Every failure is a status on the block, because the health check
// is answered from deterministic evidence and an unreachable model must not take that away.
func (j *Judge) Assess(ctx context.Context, h *Health) AIBlock {
	if j == nil || !j.Cfg.Enabled {
		return AIBlock{Status: StatusDisabled, Reason: "AI 判讀未啟用（config: ai.enabled）"}
	}
	hasKey := j.HasKey
	if hasKey == nil {
		hasKey = ai.HasAPIKey
	}
	if !hasKey() {
		// Named without its value, and the value is never read into a message anywhere here.
		return AIBlock{Status: StatusUnavailable, Reason: "環境變數 OPENAI_API_KEY 未設定"}
	}
	if j.Client == nil {
		return AIBlock{Status: StatusUnavailable, Reason: "AI client 未初始化"}
	}
	if h == nil {
		return AIBlock{Status: StatusUnavailable, Reason: "沒有可判讀的 evidence"}
	}

	cfg := j.Cfg.Defaulted()
	reqCtx, cancel := context.WithTimeout(ctx, cfg.Timeout())
	defer cancel()

	raw, _, err := j.Client.CompleteSchema(reqCtx, cfg.Model, judgeInstruction,
		buildJudgePrompt(h), cfg.Temperature, "stock_health_reading", judgeSchema())
	if err != nil {
		// err carries the URL and the API's own message, never the key or the header — see
		// the contract on ai.Complete. The prompt is not included either: it is long, and
		// logging it wholesale is how evidence ends up somewhere it was not meant to go.
		return AIBlock{Status: StatusUnavailable, Model: cfg.Model,
			Reason: "AI 判讀失敗：" + err.Error()}
	}

	var reply judgeReply
	if err := json.Unmarshal([]byte(raw), &reply); err != nil {
		return AIBlock{Status: StatusUnavailable, Model: cfg.Model,
			Reason: "AI 回覆不是預期的結構"}
	}
	if bad, ok := proseIsClean(reply); !ok {
		// The model wrote a figure into its prose. Whether it copied one from the evidence
		// or invented it cannot be told apart from here, and a page that cannot tell must
		// not print it.
		return AIBlock{Status: StatusUnavailable, Model: cfg.Model,
			Reason: "AI 回覆的文字中出現數字，已整段捨棄（" + bad + "）"}
	}
	if !validConfidence(reply.Confidence) {
		return AIBlock{Status: StatusUnavailable, Model: cfg.Model,
			Reason: "AI 回覆的 confidence 不是允許的值"}
	}
	// Strict mode guarantees the FIELDS exist, not that they say anything: "" and [] are both
	// valid, so a reply of nothing at all passes the schema, the scrubber and the confidence
	// check. An AVAILABLE block with no prose in it is worse than an honest refusal — the
	// page would show a heading, a confidence and blank space, which reads as "the model
	// looked and had no concerns".
	if strings.TrimSpace(reply.Summary) == "" {
		return AIBlock{Status: StatusUnavailable, Model: cfg.Model,
			Reason: "AI 回覆沒有內容"}
	}

	return AIBlock{
		Status:       StatusAvailable,
		Model:        cfg.Model,
		Summary:      strings.TrimSpace(reply.Summary),
		BullPoints:   trimAll(reply.BullPoints),
		BearPoints:   trimAll(reply.BearPoints),
		RiskNotes:    trimAll(reply.RiskNotes),
		Invalidators: trimAll(reply.Invalidators),
		DataGaps:     trimAll(reply.DataGaps),
		Confidence:   reply.Confidence,
		Disclaimer:   judgeDisclaimer,
	}
}

func validConfidence(c string) bool {
	switch c {
	case ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
		return true
	}
	return false
}

func trimAll(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// proseIsClean reports whether every prose field is free of digits, and names the first
// offending field when it is not.
//
// Full-width digits count. A model asked not to write numerals will sometimes reach for
// ０-９ instead, and unicode.IsDigit covers both along with every other decimal script.
func proseIsClean(r judgeReply) (string, bool) {
	fields := []struct {
		name  string
		lines []string
	}{
		{"summary", []string{r.Summary}},
		{"bull_points", r.BullPoints},
		{"bear_points", r.BearPoints},
		{"risk_notes", r.RiskNotes},
		{"invalidators", r.Invalidators},
		{"data_gaps", r.DataGaps},
	}
	for _, f := range fields {
		for _, line := range f.lines {
			for _, ch := range line {
				if unicode.IsDigit(ch) {
					return f.name, false
				}
			}
		}
	}
	return "", true
}

// buildJudgePrompt renders the finished evidence as text.
//
// It sends Display strings, which are the same strings the page shows and are never bare
// zeros for missing values — so the model sees "INSUFFICIENT_DATA" where a reader would, and
// is told in the instruction what that means. Nothing here is a raw series, and nothing is
// recomputed on the way out.
func buildJudgePrompt(h *Health) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("【個股】%s %s（%s）", h.Identity.Symbol, h.Identity.Name, h.Identity.Market)
	if h.Identity.Industry != "" {
		w("產業：%s", h.Identity.Industry)
	}
	w("分析日：%s", h.AsOf)
	w("資料完整度：%s", h.DataQuality.Level)

	w("\n【價格與技術面】")
	w("收盤：%s（%s）", h.Price.Close.Display, dateOr(h.Price.BarDate, "無"))
	w("漲跌幅：%s", h.Price.ChangePct.Display)
	w("量能倍數：%s", h.Price.VolumeRatio.Display)
	w("月線：%s／乖離：%s", h.Technical.MA20.Display, h.Technical.MA20DistancePct.Display)
	w("RSI：%s", h.Technical.RSI.Display)
	if h.Technical.Trend.State != "" {
		w("趨勢狀態：%s", h.Technical.Trend.State)
	}
	// Omitted ENTIRELY when there is no risk label. "風險標籤：—" reads as a risk to a model
	// that has just been told to explain a verdict, and whatever it writes about it goes
	// straight onto the page.
	if hasRisk(h.Technical.RiskLabel) {
		w("風險標籤：%s %s", h.Technical.RiskLabel, strings.TrimSpace(h.Technical.RiskWarning))
	}
	w("掃描器判定（唯讀，不得更動）：%s／階段 %s", h.Technical.ScannerAction, h.Technical.Stage)

	w("\n【大盤】%s（%s）", h.Market.Regime, h.Market.Status)

	w("\n【基本面】狀態 %s", h.Fundamental.Status)
	w("月營收（%s）：%s／年增 %s／月增 %s", h.Fundamental.RevenuePeriod,
		h.Fundamental.Revenue.Display, h.Fundamental.RevenueYoY.Display,
		h.Fundamental.RevenueMoM.Display)
	w("毛利率 %s／營益率 %s／淨利率 %s", h.Fundamental.GrossMargin.Display,
		h.Fundamental.OperatingMargin.Display, h.Fundamental.NetMargin.Display)
	eps := h.Fundamental.EPS
	w("EPS（%s）：累計 %s／單季 %s／TTM %s／預估 %s", eps.Period,
		eps.Cumulative.Display, eps.Quarter.Display, eps.TTM.Display, eps.Forward.Display)
	w("註：%s", eps.Note)

	w("\n【評價】狀態 %s", h.Valuation.Status)
	w("本益比 %s／股價淨值比 %s／殖利率 %s", h.Valuation.TrailingPE.Display,
		h.Valuation.PBRatio.Display, h.Valuation.DividendYield.Display)
	w("歷史本益比分布：%s", h.Valuation.Historical.Summary)
	w("目前所在百分位：%s", h.Valuation.Historical.CurrentPercentile.Display)
	// Evidence quality, given to the model as a FINDING it may explain — never as a question
	// it may answer. The grade, its rule version and the counts behind it are all computed by
	// a pure function before this prompt is built; nothing the model returns is read back
	// into them.
	q := h.Valuation.Historical.Quality
	w("估值樣本品質：%s", q.Summary)
	for _, c := range q.Caveats {
		w("　- %s", c)
	}
	// Model suitability, given as a finding with its canonical reason codes. Like the grade
	// above, it is decided by a pure function before this prompt exists.
	ms := h.Valuation.ModelSuitability
	w("本益比估值模型適用性：%s", ms.Summary)
	if len(ms.Reasons) > 0 {
		w("　適用性理由代碼：%s", strings.Join(ms.Reasons, "、"))
	}
	for _, n := range ms.Notes {
		w("　- %s", n)
	}

	w("\n【目標價】狀態 %s（規則：%s）", h.Target.Status, h.Target.Rule)
	if h.Target.RuleDetail != "" {
		w("倍數依據：%s", h.Target.RuleDetail)
	}
	for _, sc := range h.Target.Scenarios {
		w("%s：倍數 %s → 目標價 %s（上檔 %s）", sc.Name, sc.PEMultiple.Display,
			sc.TargetPrice.Display, sc.UpsidePct.Display)
	}
	w("安全邊際：%s", h.Target.MarginOfSafety.Display)

	w("\n【法人】狀態 %s（當日紀錄完整：%t）", h.Institution.Status, h.Institution.TodayComplete)
	for _, leg := range h.Institution.Legs {
		w("%s：當日 %s／五日 %s／十日 %s", leg.Name, leg.TodayNet.Display,
			leg.Sum5D.Display, leg.Sum10D.Display)
	}

	w("\n【新聞】狀態 %s", h.News.Status)
	if len(h.News.Items) == 0 {
		w("（沒有任何新聞訊號，不得提及任何未列出的消息）")
	}
	for _, it := range h.News.Items {
		w("[%s/%s] %s", it.Level, it.Signal, it.Title)
	}

	w("\n【程式判定的進場評估（唯讀，不得更動或推翻）】")
	w("結論：%s —— %s", h.Assessment.Verdict, h.Assessment.Reason)
	if h.Assessment.DecidedBy != "" {
		w("依據規則：%s", h.Assessment.DecidedBy)
	}
	for _, r := range h.Assessment.Rules {
		if r.Fired {
			w("觸發：%s（%s）%s", r.ID, r.Description, r.Detail)
		}
	}
	w("你的工作是解釋這個結論為什麼成立、以及什麼情況會推翻它。不得提出不同的結論。")

	w("\n【資料缺口】")
	for _, blk := range h.DataQuality.Blocks {
		if blk.Status != StatusAvailable {
			w("%s：%s %s", blk.Name, blk.Status, blk.Detail)
		}
	}
	return b.String()
}
