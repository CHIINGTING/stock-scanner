package healthcheck

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/deep-huang/stock-scanner/internal/ai"
)

// ── fixtures ──────────────────────────────────────────────────────────────────────────

// fakeOpenAI serves the Responses API shape. It is the ONLY thing stubbed: the judge's
// client, schema, transport, decoding, scrubber and status mapping all run for real.
//
// No test in this file reaches the real API. There is no path here that could.
func fakeOpenAI(t *testing.T, handler func(w http.ResponseWriter, body []byte)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		handler(w, b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// reply serialises a judge reply into the envelope the Responses API returns.
func reply(w http.ResponseWriter, v judgeReply) {
	inner, _ := json.Marshal(v)
	env := map[string]any{
		"status": "completed",
		"output": []map[string]any{{
			"type": "message",
			"content": []map[string]any{{
				"type": "output_text", "text": string(inner),
			}},
		}},
		"usage": map[string]any{"total_tokens": 100},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(env)
}

func cleanReply() judgeReply {
	return judgeReply{
		Summary:      "價格位於月線之上，法人近期偏多，但評價已不便宜。",
		BullPoints:   []string{"趨勢向上", "法人連續買超"},
		BearPoints:   []string{"本益比位於歷史區間偏高處"},
		RiskNotes:    []string{"大盤轉弱時回檔風險較高"},
		Invalidators: []string{"跌破月線且法人轉為賣超"},
		DataGaps:     []string{"缺少單季每股盈餘"},
		Confidence:   ConfidenceMedium,
	}
}

// judgeAt builds a judge pointed at a fake server.
//
// The environment gets a DUMMY key: ai.Client reads OPENAI_API_KEY itself, so overriding
// HasKey alone would leave every request failing on a missing key and the tests below would
// pass for the wrong reason. t.Setenv restores the old value when the test ends, and the
// value is nonsense pointed at httptest — no test in this file can reach the real API.
func judgeAt(t *testing.T, url string) *Judge {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key-not-a-real-credential")
	j := NewJudge(JudgeConfig{Enabled: true, Model: "test-model", BaseURL: url, TimeoutSec: 5})
	j.HasKey = func() bool { return true }
	return j
}

func sampleHealth() *Health {
	return &Health{
		Identity: Identity{Symbol: "2330", Name: "台積電", Market: "TWSE"},
		AsOf:     "2026-09-04",
		Price:    PriceBlock{Status: StatusAvailable, Close: Num(759, "TWD", 2, "test")},
	}
}

// ── the AI produces no financial facts (§0, §19) ──────────────────────────────────────

// The reply schema has no numeric field at all, so there is no slot a figure could arrive in.
func TestJudgeSchemaHasNoNumericField(t *testing.T) {
	schema := judgeSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("no properties")
	}
	forbidden := []string{"current_price", "eps", "target_price", "pe", "pe_ratio",
		"revenue", "institution_net_buy", "price", "upside", "margin_of_safety"}
	for _, name := range forbidden {
		if _, exists := props[name]; exists {
			t.Errorf("the reply schema carries a %q field — the model could supply it", name)
		}
	}
	for name, spec := range props {
		m := spec.(map[string]any)
		switch m["type"] {
		case "number", "integer":
			t.Errorf("%q is numeric; the model must not return figures", name)
		case "string", "array":
		default:
			t.Errorf("%q has unexpected type %v", name, m["type"])
		}
	}
	// confidence is an enum precisely so it is not a score.
	conf := props["confidence"].(map[string]any)
	if conf["enum"] == nil {
		t.Errorf("confidence is not an enum — a numeric confidence would render beside real figures")
	}
	// strict mode requires both of these, and they are what stop an invented field.
	if schema["additionalProperties"] != false {
		t.Errorf("additionalProperties must be false")
	}
	req, _ := schema["required"].([]string)
	if len(req) != len(props) {
		t.Errorf("required lists %d of %d properties; strict mode needs all", len(req), len(props))
	}
}

// Prose is the one channel a figure could still travel through. A reply carrying a digit is
// discarded whole rather than shown.
func TestProseCarryingDigitsIsRejected(t *testing.T) {
	cases := map[string]judgeReply{
		"summary": func() judgeReply { r := cleanReply(); r.Summary = "目標價約 1,100 元"; return r }(),
		"bull":    func() judgeReply { r := cleanReply(); r.BullPoints = []string{"EPS 達 22.5 元"}; return r }(),
		"bear":    func() judgeReply { r := cleanReply(); r.BearPoints = []string{"本益比 18 倍偏高"}; return r }(),
		"risk":    func() judgeReply { r := cleanReply(); r.RiskNotes = []string{"外資賣超 5000 張"}; return r }(),
		"invalid": func() judgeReply { r := cleanReply(); r.Invalidators = []string{"跌破 700 元"}; return r }(),
		"gaps":    func() judgeReply { r := cleanReply(); r.DataGaps = []string{"缺 4 季資料"}; return r }(),
		// A model told not to write numerals will sometimes reach for the full-width forms.
		"fullwidth": func() judgeReply { r := cleanReply(); r.Summary = "本益比為１８倍"; return r }(),
	}
	for name, rep := range cases {
		srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) { reply(w, rep) })
		got := judgeAt(t, srv.URL).Assess(context.Background(), sampleHealth())

		if got.Status == StatusAvailable {
			t.Errorf("%s: a reply containing a figure was accepted: %+v", name, got)
		}
		if got.Summary != "" || len(got.BullPoints) != 0 || len(got.BearPoints) != 0 {
			t.Errorf("%s: prose survived rejection: %+v", name, got)
		}
		if got.Reason == "" {
			t.Errorf("%s: rejected with no reason", name)
		}
	}
}

// The clean path keeps every field, and what it keeps still contains no digits.
func TestCleanProseIsAccepted(t *testing.T) {
	srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) { reply(w, cleanReply()) })
	got := judgeAt(t, srv.URL).Assess(context.Background(), sampleHealth())

	if got.Status != StatusAvailable {
		t.Fatalf("status = %s (%s)", got.Status, got.Reason)
	}
	if got.Summary == "" || len(got.BullPoints) != 2 || len(got.Invalidators) != 1 {
		t.Fatalf("fields lost in translation: %+v", got)
	}
	if got.Confidence != ConfidenceMedium {
		t.Fatalf("confidence = %q", got.Confidence)
	}
	if got.Model != "test-model" {
		t.Fatalf("model = %q, want it recorded", got.Model)
	}
	if got.Disclaimer == "" {
		t.Fatalf("no disclaimer — the boundary must be stated on the page, not only in a doc")
	}
	// Belt and braces: whatever reached the block is digit-free.
	for _, s := range append([]string{got.Summary}, concat(got.BullPoints, got.BearPoints,
		got.RiskNotes, got.Invalidators, got.DataGaps)...) {
		for _, ch := range s {
			if unicode.IsDigit(ch) {
				t.Fatalf("a digit reached the block: %q", s)
			}
		}
	}
}

func concat(ss ...[]string) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s...)
	}
	return out
}

// An out-of-vocabulary confidence is refused rather than shown.
func TestUnknownConfidenceIsRejected(t *testing.T) {
	r := cleanReply()
	r.Confidence = "VERY_HIGH"
	srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) { reply(w, r) })
	got := judgeAt(t, srv.URL).Assess(context.Background(), sampleHealth())
	if got.Status == StatusAvailable {
		t.Fatalf("accepted an unknown confidence: %+v", got)
	}
}

// ── fail-open (§20) ───────────────────────────────────────────────────────────────────

// Every AI failure mode leaves a status and a reason, and none returns an error.
func TestEveryAIFailureIsDegradedNotFatal(t *testing.T) {
	slow := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
		time.Sleep(300 * time.Millisecond)
		reply(w, cleanReply())
	})
	cases := []struct {
		name  string
		judge func() *Judge
	}{
		{"disabled", func() *Judge {
			j := NewJudge(JudgeConfig{Enabled: false})
			j.HasKey = func() bool { return true }
			return j
		}},
		{"no api key", func() *Judge {
			j := NewJudge(JudgeConfig{Enabled: true, BaseURL: "http://127.0.0.1:1"})
			j.HasKey = func() bool { return false }
			return j
		}},
		{"nil judge", func() *Judge { return nil }},
		{"connection refused", func() *Judge { return judgeAt(t, "http://127.0.0.1:1") }},
		{"timeout", func() *Judge {
			j := judgeAt(t, slow.URL)
			j.Cfg.TimeoutSec = 1
			j.Client = ai.NewClient(slow.URL, 20*time.Millisecond)
			return j
		}},
		{"rate limited", func() *Judge {
			srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"message":"rate limit","type":"rate_limit_error"}}`))
			})
			return judgeAt(t, srv.URL)
		}},
		{"server error", func() *Judge {
			srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
				w.WriteHeader(http.StatusInternalServerError)
			})
			return judgeAt(t, srv.URL)
		}},
		{"unauthorized", func() *Judge {
			srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"bad key","type":"invalid_request_error"}}`))
			})
			return judgeAt(t, srv.URL)
		}},
		{"garbage body", func() *Judge {
			srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
				_, _ = w.Write([]byte("this is not json"))
			})
			return judgeAt(t, srv.URL)
		}},
		{"valid envelope, unparseable payload", func() *Judge {
			srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
				env := map[string]any{"status": "completed", "output": []map[string]any{{
					"type": "message",
					"content": []map[string]any{{
						"type": "output_text", "text": "{{{not json",
					}},
				}}}
				_ = json.NewEncoder(w).Encode(env)
			})
			return judgeAt(t, srv.URL)
		}},
		{"truncated response", func() *Judge {
			srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
				env := map[string]any{
					"status":             "incomplete",
					"incomplete_details": map[string]any{"reason": "max_output_tokens"},
				}
				_ = json.NewEncoder(w).Encode(env)
			})
			return judgeAt(t, srv.URL)
		}},
	}

	for _, tc := range cases {
		got := tc.judge().Assess(context.Background(), sampleHealth())
		if got.Status == StatusAvailable {
			t.Errorf("%s: reported AVAILABLE: %+v", tc.name, got)
		}
		if got.Reason == "" {
			t.Errorf("%s: no reason given", tc.name)
		}
		if got.Summary != "" || len(got.BullPoints) != 0 {
			t.Errorf("%s: prose present on a failed reading: %+v", tc.name, got)
		}
	}
}

// The whole health endpoint still answers when the model is unreachable, and every
// deterministic figure is byte-identical to a run with AI switched off.
//
// This is the §20 acceptance criterion: AI down, dashboard still works.
func TestHealthIsUnchangedWhenTheModelIsDown(t *testing.T) {
	// ONE service, so both answers are built from the same archives. Two services would sit
	// in different temp directories, and those paths appear in the reason strings — the
	// comparison below would then fail for a reason that has nothing to do with the model.
	s, root := fundService(t, "2026-09-04")
	writeSessions(t, filepath.Join(root, "valuation"), 25, "2026-09-04", 18)
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-08-20", year: 2026, quarter: 2, eps: f64(22.5),
		revenue: 1000, gross: 500, operating: 400, net: 300,
	})

	s.Judge = judgeAt(t, "http://127.0.0.1:1")
	down := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	s.Judge = nil
	off := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})

	if down.AI.Status == StatusAvailable {
		t.Fatalf("the unreachable model reported AVAILABLE")
	}
	if off.AI.Status != StatusDisabled {
		t.Fatalf("a nil judge = %s, want DISABLED", off.AI.Status)
	}

	// Everything except the AI block must be identical. Comparing the serialised forms
	// catches a field that a hand-written comparison would forget to check.
	down.AI, off.AI = AIBlock{}, AIBlock{}
	down.GeneratedAt, off.GeneratedAt = time.Time{}, time.Time{}
	a, _ := json.Marshal(down)
	b, _ := json.Marshal(off)
	if string(a) != string(b) {
		t.Fatalf("the deterministic answer changed with the model's availability:\n%s\n%s", a, b)
	}
	// And it is a real answer, not an empty one that happens to match.
	if down.Target.Status != StatusAvailable {
		t.Fatalf("the fixture produced no target, so this proves nothing: %s", down.Target.Reason)
	}
	if _, ok := down.Fundamental.EPS.Cumulative.Float(); !ok {
		t.Fatalf("the fixture produced no EPS, so this proves nothing")
	}
}

// SkipAI turns the section off for one request without touching config, and changes nothing
// else about the answer.
func TestSkipAIDisablesOnlyTheAISection(t *testing.T) {
	srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) { reply(w, cleanReply()) })
	s, _ := valService(t, "2026-09-04")
	s.Judge = judgeAt(t, srv.URL)

	withAI := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	skipped := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04", SkipAI: true})

	if withAI.AI.Status != StatusAvailable {
		t.Fatalf("baseline AI status = %s (%s)", withAI.AI.Status, withAI.AI.Reason)
	}
	if skipped.AI.Status != StatusDisabled {
		t.Fatalf("SkipAI gave %s, want DISABLED", skipped.AI.Status)
	}
	if skipped.AI.Summary != "" {
		t.Fatalf("SkipAI still returned prose")
	}
	withAI.AI, skipped.AI = AIBlock{}, AIBlock{}
	withAI.GeneratedAt, skipped.GeneratedAt = time.Time{}, time.Time{}
	a, _ := json.Marshal(withAI)
	b, _ := json.Marshal(skipped)
	if string(a) != string(b) {
		t.Fatalf("SkipAI changed something other than the AI section")
	}
}

// ── what is sent, and what is not ─────────────────────────────────────────────────────

// The prompt carries rendered Display strings, so the model sees INSUFFICIENT_DATA exactly
// where a reader would — never a zero standing in for a missing figure.
func TestPromptShowsMissingDataAsStatusNotZero(t *testing.T) {
	h := sampleHealth()
	h.Valuation = ValuationBlock{
		Status:     StatusInsufficientData,
		TrailingPE: Missing(StatusInsufficientData, "樣本不足"),
		Historical: HistoricalPEBlock{
			Status:            StatusInsufficientData,
			Summary:           "INSUFFICIENT_DATA（1 筆，需 20）",
			CurrentPercentile: Missing(StatusInsufficientData, "樣本不足"),
		},
	}
	h.Fundamental = FundamentalBlock{
		Status: StatusUnavailable,
		EPS: EPSBlock{
			Cumulative: Missing(StatusUnavailable, "無封存"),
			Quarter:    Missing(StatusInsufficientData, "需前期"),
			TTM:        Missing(StatusInsufficientData, "需四季"),
			Forward:    Missing(StatusNotImplemented, "無授權來源"),
		},
	}
	p := buildJudgePrompt(h)

	for _, want := range []string{"INSUFFICIENT_DATA", "UNAVAILABLE", "NOT_IMPLEMENTED"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt never shows %s", want)
		}
	}
	if strings.Contains(p, "本益比 0") || strings.Contains(p, "：0.00") {
		t.Errorf("a missing figure was rendered as a zero in the prompt:\n%s", p)
	}
	// The instruction has to tell the model what those words mean, or it will read them as
	// bad news rather than as absent data.
	if !strings.Contains(judgeInstruction, "INSUFFICIENT_DATA") ||
		!strings.Contains(judgeInstruction, "不代表") {
		t.Errorf("the instruction does not explain that a missing figure is not a zero")
	}
}

// The instruction forbids inventing news, and the prompt says so explicitly when there is
// none — the model must not fill an empty section.
func TestPromptSaysWhenThereIsNoNews(t *testing.T) {
	p := buildJudgePrompt(sampleHealth())
	if !strings.Contains(p, "不得提及任何未列出的消息") {
		t.Errorf("an empty news section does not warn against inventing one:\n%s", p)
	}
	if !strings.Contains(judgeInstruction, "只能談 evidence 中確實出現的新聞") {
		t.Errorf("the instruction does not constrain news to the evidence")
	}
}

// ── security (§24) ────────────────────────────────────────────────────────────────────

// The key is read from the environment inside the client and never stored on a config or a
// judge, so it cannot be serialised into a response, a snapshot or a log line.
func TestNoAPIKeyFieldExistsToLeak(t *testing.T) {
	// Config carries no key: if a field is ever added, this fails at compile time when the
	// literal below stops matching, and at run time on the marshalled shape.
	b, err := json.Marshal(JudgeConfig{Enabled: true, Model: "m", BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	blob := strings.ToLower(string(b))
	for _, bad := range []string{"key", "token", "secret", "authorization", "bearer"} {
		if strings.Contains(blob, bad) {
			t.Fatalf("JudgeConfig serialises something called %q: %s", bad, b)
		}
	}
	// The block sent to a browser must not carry one either.
	ab, _ := json.Marshal(AIBlock{Status: StatusAvailable, Model: "m", Summary: "x"})
	blob = strings.ToLower(string(ab))
	for _, bad := range []string{"key", "token", "secret", "authorization", "bearer"} {
		if strings.Contains(blob, bad) {
			t.Fatalf("AIBlock serialises something called %q: %s", bad, ab)
		}
	}
}

// A failed call must not put the prompt, the key or the Authorization header into the reason
// string, which is rendered on the page and may be logged.
func TestFailureReasonLeaksNothing(t *testing.T) {
	srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided: sk-abc123","type":"invalid_request_error"}}`))
	})
	got := judgeAt(t, srv.URL).Assess(context.Background(), sampleHealth())
	if got.Status == StatusAvailable {
		t.Fatal("a 401 was accepted")
	}
	lower := strings.ToLower(got.Reason)
	for _, bad := range []string{"bearer", "authorization"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("the reason names %q: %s", bad, got.Reason)
		}
	}
	// The prompt is long and carries the whole evidence set; it must not ride along.
	if strings.Contains(got.Reason, "【個股】") || strings.Contains(got.Reason, "【目標價】") {
		t.Fatalf("the prompt was embedded in the failure reason: %s", got.Reason)
	}
	// The no-key path names the variable but can never carry its value.
	j := NewJudge(JudgeConfig{Enabled: true})
	j.HasKey = func() bool { return false }
	nokey := j.Assess(context.Background(), sampleHealth())
	if !strings.Contains(nokey.Reason, "OPENAI_API_KEY") {
		t.Fatalf("the no-key reason does not say which variable is missing: %s", nokey.Reason)
	}
}

// The request goes to the configured base URL and nowhere else — in particular, never to a
// company-internal gateway.
func TestRequestGoesOnlyToTheConfiguredEndpoint(t *testing.T) {
	var path string
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		sawAuth = r.Header.Get("Authorization") != ""
		b, _ := io.ReadAll(r.Body)
		// The evidence must reach the model as the USER message, with the rules as
		// instructions — not smuggled into a field the schema does not describe.
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		if req["model"] != "test-model" {
			t.Errorf("model = %v", req["model"])
		}
		if _, ok := req["text"]; !ok {
			t.Errorf("no structured-output config was sent; the reply would be unconstrained")
		}
		reply(w, cleanReply())
	}))
	defer srv.Close()

	got := judgeAt(t, srv.URL).Assess(context.Background(), sampleHealth())
	if got.Status != StatusAvailable {
		t.Fatalf("status = %s (%s)", got.Status, got.Reason)
	}
	if path != "/v1/responses" {
		t.Fatalf("path = %q", path)
	}
	if !sawAuth {
		t.Fatalf("no Authorization header was sent, so the key never left the environment")
	}
}

// ── the judge cannot change a number ──────────────────────────────────────────────────

// A model that returns prose cannot alter any figure, because the figures are final before
// it is called. Asserted end-to-end: the same request with a model that "speaks" and one that
// does not must differ ONLY in the AI block.
func TestModelOutputNeverReachesAFigure(t *testing.T) {
	srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) { reply(w, cleanReply()) })

	s, vdir := valService(t, "2026-09-04")
	writeSessions(t, vdir, 25, "2026-09-04", 18)

	s.Judge = judgeAt(t, srv.URL)
	spoke := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	s.Judge = nil
	silent := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})

	if spoke.AI.Status != StatusAvailable {
		t.Fatalf("the talking judge failed: %s", spoke.AI.Reason)
	}
	spoke.AI, silent.AI = AIBlock{}, AIBlock{}
	spoke.GeneratedAt, silent.GeneratedAt = time.Time{}, time.Time{}
	a, _ := json.Marshal(spoke)
	b, _ := json.Marshal(silent)
	if string(a) != string(b) {
		t.Fatalf("a figure moved when the model spoke:\n%s\n%s", a, b)
	}
}

// The two nil guards that were implemented but untested.
func TestNilInputsAreRefusedNotPanicked(t *testing.T) {
	srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) { reply(w, cleanReply()) })

	if got := judgeAt(t, srv.URL).Assess(context.Background(), nil); got.Status == StatusAvailable {
		t.Fatalf("a nil Health produced a reading: %+v", got)
	}

	j := judgeAt(t, srv.URL)
	j.Client = nil
	if got := j.Assess(context.Background(), sampleHealth()); got.Status == StatusAvailable {
		t.Fatalf("a nil client produced a reading: %+v", got)
	}
}

// A reply that satisfies every structural check but says nothing is not a reading. Strict
// mode guarantees the fields exist, not that they carry content.
func TestEmptyReplyIsNotAReading(t *testing.T) {
	srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
		reply(w, judgeReply{Confidence: ConfidenceLow})
	})
	got := judgeAt(t, srv.URL).Assess(context.Background(), sampleHealth())
	if got.Status == StatusAvailable {
		t.Fatalf("an empty reply was reported as AVAILABLE: %+v", got)
	}
	if got.Reason == "" {
		t.Fatalf("no reason given")
	}
	// A whitespace-only summary is the same thing wearing a disguise.
	srv2 := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
		reply(w, judgeReply{Summary: "   \n  ", Confidence: ConfidenceLow})
	})
	if got := judgeAt(t, srv2.URL).Assess(context.Background(), sampleHealth()); got.Status == StatusAvailable {
		t.Fatalf("a whitespace summary was accepted: %+v", got)
	}
}

// The config the service holds and the config the judge holds must agree.
func TestServiceAndJudgeConfigAgree(t *testing.T) {
	cfg := Config{AI: JudgeConfig{Enabled: true}}.Defaulted()
	j := NewJudge(cfg.AI)
	if cfg.AI.Model == "" {
		t.Fatalf("Config.Defaulted left the AI model blank")
	}
	if cfg.AI.Model != j.Cfg.Model || cfg.AI.TimeoutSec != j.Cfg.TimeoutSec {
		t.Fatalf("service config %+v disagrees with judge config %+v", cfg.AI, j.Cfg)
	}
}

// The assessment is handed to the judge as read-only evidence, and the judge cannot change
// it. The instruction says so, and the ordering guarantees it: assess() runs before Assess().
func TestAssessmentIsGivenToTheJudgeAsReadOnly(t *testing.T) {
	h := sampleHealth()
	h.Assessment = assess(&Health{
		Price:     PriceBlock{Status: StatusAvailable, Close: Num(100, "TWD", 2, "t")},
		Technical: TechnicalBlock{ScannerAction: "BUY"},
		Target:    TargetPriceBlock{MarginOfSafety: Num(22, "%", 1, "t")},
		Valuation: ValuationBlock{Historical: HistoricalPEBlock{
			CurrentPercentile: Missing(StatusInsufficientData, "x")}},
	})
	p := buildJudgePrompt(h)

	if !strings.Contains(p, h.Assessment.Verdict) {
		t.Errorf("the prompt does not carry the verdict")
	}
	if !strings.Contains(p, "不得更動") && !strings.Contains(p, "不得改變") {
		t.Errorf("the prompt does not mark the verdict read-only:\n%s", p)
	}
	if !strings.Contains(judgeInstruction, "不得改變它") {
		t.Errorf("the instruction does not forbid the model reaching a different conclusion")
	}
	// The rule that decided it travels too, so the model explains the actual reasoning
	// rather than inventing a plausible one.
	if h.Assessment.DecidedBy != "" && !strings.Contains(p, h.Assessment.DecidedBy) {
		t.Errorf("the deciding rule is not in the prompt")
	}
}

// ── the model cannot supply a VERDICT either (FU-11) ──────────────────────────────────
//
// TestJudgeSchemaHasNoNumericField blocks figures. It does not block a CLASSIFICATION: a
// string field named "suitability" is neither numeric nor on its denylist, so adding one to
// the reply — and letting it overwrite the deterministic verdict when the model bothers to
// send it — passed the entire suite. Every deterministic conclusion this repo computes is now
// a word rather than a number (entry assessment, data quality, model suitability), so "no
// numeric field" has stopped being the same thing as "the model decides nothing".
//
// The fix is a denylist replaced by an ALLOWLIST. Any new field, of any type, fails these
// until someone deliberately adds it here — which is the moment to ask whether the model
// should be able to send it at all.

// replyFields is the complete set the reply may carry. Adding to it is a decision, not a
// detail: every one of these is prose the model wrote, and nothing else may enter.
var replyFields = []string{"summary", "bull_points", "bear_points", "risk_notes",
	"invalidators", "data_gaps", "confidence"}

func TestJudgeSchemaCarriesExactlyTheKnownFields(t *testing.T) {
	props, ok := judgeSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatal("no properties")
	}
	allowed := map[string]bool{}
	for _, f := range replyFields {
		allowed[f] = true
		if _, exists := props[f]; !exists {
			t.Errorf("the schema lost the %q field", f)
		}
	}
	for name := range props {
		if !allowed[name] {
			t.Errorf("the reply schema carries an undeclared %q field — the model could "+
				"supply it, and if it names a deterministic conclusion it could decide it",
				name)
		}
	}
}

// The same allowlist over the STRUCT, because the schema is what the model is asked for and
// the struct is what gets decoded. A field added to only one of them is the subtler bug.
func TestJudgeReplyStructCarriesExactlyTheKnownFields(t *testing.T) {
	rt := reflect.TypeOf(judgeReply{})
	allowed := map[string]bool{}
	for _, f := range replyFields {
		allowed[f] = true
	}
	seen := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		seen[tag] = true
		if !allowed[tag] {
			t.Errorf("judgeReply carries an undeclared %q field; a model reply could be "+
				"decoded into it", tag)
		}
	}
	for _, f := range replyFields {
		if !seen[f] {
			t.Errorf("judgeReply lost the %q field", f)
		}
	}
}

// And the end-to-end form: a model that ACTIVELY TRIES to set every deterministic conclusion
// must change none of them.
//
// The reply below is valid JSON carrying the seven real fields plus fabricated verdicts. It
// reaches the real decoder over the real transport, which is the only place the guarantee
// actually lives — the struct has no field for them, so they are dropped. If someone ever adds
// one, this fails whether or not the schema was updated to match.
func TestAModelVerdictCannotOverrideADeterministicOne(t *testing.T) {
	srv := fakeOpenAI(t, func(w http.ResponseWriter, _ []byte) {
		inner := map[string]any{
			"summary": "測試用的模型回覆。", "bull_points": []string{"a"},
			"bear_points": []string{"b"}, "risk_notes": []string{"c"},
			"invalidators": []string{"d"}, "data_gaps": []string{"e"},
			"confidence": ConfidenceMedium,
			// Everything the model must not be able to decide.
			"suitability":       "SUITABLE",
			"model_suitability": "SUITABLE",
			"quality":           "HIGH",
			"verdict":           "BUY",
			"assessment":        "OPPORTUNITY",
			"target_price":      9999.0,
			"eps":               123.4,
		}
		b, _ := json.Marshal(inner)
		env := map[string]any{"status": "completed", "output": []map[string]any{{
			"type": "message", "content": []map[string]any{{
				"type": "output_text", "text": string(b)}}}},
			"usage": map[string]any{"total_tokens": 100}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	})

	// A stock whose deterministic verdicts are the OPPOSITE of what the model claims: a
	// distribution too thin to be SUITABLE, so an override would be visible.
	s, vdir := valService(t, "2026-09-04")
	writeSessions(t, vdir, 25, "2026-09-04", 18)

	s.Judge = judgeAt(t, srv.URL)
	spoke := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	s.Judge = nil
	silent := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})

	if spoke.AI.Status != StatusAvailable {
		t.Fatalf("the talking judge failed: %s", spoke.AI.Reason)
	}
	// Named individually before the whole-document compare, so a failure says WHICH
	// conclusion moved rather than printing two large JSON blobs.
	if spoke.Valuation.ModelSuitability.Suitability != silent.Valuation.ModelSuitability.Suitability {
		t.Errorf("model suitability moved when the model spoke: %q → %q",
			silent.Valuation.ModelSuitability.Suitability,
			spoke.Valuation.ModelSuitability.Suitability)
	}
	if spoke.Valuation.Historical.Quality.Quality != silent.Valuation.Historical.Quality.Quality {
		t.Errorf("data quality moved: %q → %q", silent.Valuation.Historical.Quality.Quality,
			spoke.Valuation.Historical.Quality.Quality)
	}
	if spoke.Assessment.Verdict != silent.Assessment.Verdict {
		t.Errorf("the entry assessment moved: %q → %q", silent.Assessment.Verdict,
			spoke.Assessment.Verdict)
	}
	if spoke.Target.Status != silent.Target.Status {
		t.Errorf("the target status moved: %q → %q", silent.Target.Status, spoke.Target.Status)
	}

	spoke.AI, silent.AI = AIBlock{}, AIBlock{}
	spoke.GeneratedAt, silent.GeneratedAt = time.Time{}, time.Time{}
	a, _ := json.Marshal(spoke)
	b, _ := json.Marshal(silent)
	if string(a) != string(b) {
		t.Fatalf("something moved when the model spoke:\n%s\n%s", a, b)
	}
}
