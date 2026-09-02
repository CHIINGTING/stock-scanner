package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PromptSetVersion identifies the instruction set and output contract this package sends.
//
// It is stored beside every persisted analysis so that "the model changed its mind" stays
// distinguishable from "we changed the question". Bump it whenever the system prompt, the
// evidence contract or the expected output shape changes — never for an unrelated edit.
const PromptSetVersion = "r11-v1"

// systemInstruction frames the model as an interpreter of someone else's work.
//
// The constraints are here rather than in the user message on purpose: they must apply to
// every request regardless of what evidence a particular stock produced. The two families of
// rule are (a) do not take over the scanner's job, and (b) do not fill in data you were not
// given — the second matters more in practice, because a model asked about a stock will
// otherwise happily supply plausible earnings, institutional flows, or news it has no access
// to, and that fabrication is indistinguishable from analysis in the rendered report.
const systemInstruction = `You are analyzing evidence produced by a deterministic stock scanner.

Do not generate trading instructions.
Do not override the scanner's signal.
Do not invent missing financial data.
Only interpret the supplied evidence.
Clearly distinguish bullish and bearish evidence.

Hard constraints:
- You have NO access to news, earnings reports, analyst opinion, or live quotes. Do not
  reference any of them, and do not imply you know them.
- Do not supply institutional figures, fundamentals, or prices that are absent from the
  evidence. If a field is missing, say the evidence does not cover it.
- Do not browse or claim to have looked anything up.
- Do not restate the scanner's action (BUY/WATCH/SELL) as your own recommendation, and do
  not argue for or against it. Explain what the evidence shows; the decision is not yours.
- "confidence" is how well the supplied evidence supports YOUR READING. It is not a
  probability that the trade works, and it is not a trading confidence.

The reply shape is enforced by a strict JSON schema, so it does not need describing here.
What the fields MEAN is up to you to respect:
- summary: two or three sentences in Traditional Chinese.
- bull_case / bear_case / risk_flags: short bullets, Traditional Chinese. Every bullet must
  trace back to a field in the supplied evidence. If the evidence is too thin to argue a
  side, return an empty array for that side rather than inventing one.
- confidence: a number between 0 and 1.`

// buildUserMessage serialises one stock's evidence.
//
// The evidence goes over as JSON rather than prose so the model cannot mistake narrative
// framing for a hint about the answer, and so an absent field is visibly absent instead of
// being described in a sentence that implies a value.
func buildUserMessage(e Evidence) (string, error) {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return "", fmt.Errorf("ai: encode evidence: %w", err)
	}
	var sb strings.Builder
	sb.WriteString("以下是掃描器針對這一檔股票已經計算完成的證據。只根據這些欄位做解讀，")
	sb.WriteString("缺少的欄位就當作沒有資訊，不要自行補上。\n")
	// The two status fields exist precisely so the model can tell "we could not look" from
	// "we looked and it was unremarkable". Saying so here is what makes them do any work —
	// without this line UNAVAILABLE is just an unexplained string.
	sb.WriteString("market_data_status 或 institutional_status 若為 UNAVAILABLE，代表該層資料")
	sb.WriteString("這次取不到，**不是**代表大盤中性或法人沒有動作。這種情況請明說該面向無法判斷，")
	sb.WriteString("不要用其他欄位推測它。\n\n")
	sb.Write(b)
	return sb.String(), nil
}

// parseOutput turns the model's reply into an Analysis.
//
// The Responses API enforces the schema server-side (text.format, strict:true), so a
// well-formed run cannot get here with the wrong shape. This is still a real decode rather
// than a formality: it is the last line of defence if the endpoint is ever pointed elsewhere,
// and it is what turns a schema-valid but empty summary into "unavailable".
//
// Strict by design: a payload that is not a usable JSON object becomes BAD_OUTPUT. There is
// no regex salvage path and no fallback that would let a malformed reply be reinterpreted as
// a trading view — an unreadable answer is simply an unavailable one.
func parseOutput(symbol, model, raw string, tokens int, latencyMs int64) *Analysis {
	raw = strings.TrimSpace(raw)
	// Some models wrap JSON in a fenced block even in JSON mode. Unwrapping a fence is
	// tolerated because it does not change the payload; anything beyond that is not.
	if strings.HasPrefix(raw, "```") {
		if i := strings.Index(raw, "\n"); i >= 0 {
			raw = raw[i+1:]
		}
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
		raw = strings.TrimSpace(raw)
	}

	var out modelOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return unavailable(symbol, StatusBadOutput, "模型回應不是合法 JSON")
	}
	if strings.TrimSpace(out.Summary) == "" {
		return unavailable(symbol, StatusBadOutput, "模型回應缺少 summary")
	}
	// A confidence outside [0,1] means the model ignored the contract; clamp rather than
	// reject, since the reading itself may still be fine and confidence is advisory.
	conf := out.Confidence
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	return &Analysis{
		Symbol:     symbol,
		Status:     StatusOK,
		Summary:    strings.TrimSpace(out.Summary),
		BullCase:   trimAll(out.BullCase),
		BearCase:   trimAll(out.BearCase),
		RiskFlags:  trimAll(out.RiskFlags),
		Confidence: conf,
		Model:      model,
		LatencyMs:  latencyMs,
		Tokens:     tokens,
	}
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
