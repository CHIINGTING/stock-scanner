package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// No test in this file ever reaches the real OpenAI API. Everything goes through httptest,
// so the suite stays deterministic and an outage (or a missing key) can never turn it red.

func withKey(t *testing.T, v string) {
	t.Helper()
	t.Setenv(apiKeyEnv, v)
}

// respOK returns a well-formed Responses API reply carrying `content`.
//
// The output array deliberately leads with a reasoning item: a reasoning-capable model puts
// one there, and any parser that reads output[0] would pick it up instead of the message.
func respOK(content string, tokens int) string {
	b, _ := json.Marshal(map[string]any{
		"id":     "resp_test",
		"object": "response",
		"status": "completed",
		"output": []any{
			map[string]any{"type": "reasoning", "id": "rs_test", "summary": []any{}},
			map[string]any{
				"type": "message", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": content}},
			},
		},
		"usage": map[string]int{"input_tokens": 100, "output_tokens": 50, "total_tokens": tokens},
	})
	return string(b)
}

const goodOutput = `{"summary":"整理末端量能轉強。","bull_case":["站上 MA20"],"bear_case":["族群資金外流"],"risk_flags":["整理天數偏短"],"confidence":0.72}`

// ── Config ───────────────────────────────────────────────────────────────────────────

func TestConfigDefaults(t *testing.T) {
	c := Config{}.Defaulted()
	if c.Model == "" {
		t.Error("a model must always be resolved so a bare config still runs")
	}
	if c.TimeoutSec <= 0 || c.MaxStocks <= 0 {
		t.Errorf("timeout/max must default to something usable: %+v", c)
	}
	if c.Timeout() != time.Duration(c.TimeoutSec)*time.Second {
		t.Error("Timeout() disagrees with TimeoutSec")
	}
	// An explicit model is never overridden.
	if got := (Config{Model: "gpt-4.1-mini"}).Defaulted().Model; got != "gpt-4.1-mini" {
		t.Errorf("explicit model overwritten: %s", got)
	}
}

// The token must never be reachable from config — that is what keeps it out of reports,
// snapshots and logs by construction rather than by discipline.
func TestConfigCarriesNoAPIKey(t *testing.T) {
	b, err := json.Marshal(Config{Model: "m", BaseURL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ToLower(string(b))
	for _, bad := range []string{"key", "token", "secret", "authorization"} {
		if strings.Contains(s, bad) {
			t.Errorf("Config serialises something that looks like a secret field: %s", b)
		}
	}
}

func TestHasAPIKey(t *testing.T) {
	withKey(t, "")
	if HasAPIKey() {
		t.Error("empty env must read as no key")
	}
	withKey(t, "   ")
	if HasAPIKey() {
		t.Error("whitespace-only env must read as no key")
	}
	withKey(t, "unit-test-key-not-a-secret")
	if !HasAPIKey() {
		t.Error("a set key must be detected")
	}
}

// ── Client ───────────────────────────────────────────────────────────────────────────

func TestClientSuccess(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	var gotAuth, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(respOK(goodOutput, 321)))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	comp, err := c.Complete(context.Background(), "gpt-5.6-luna", "sys", "user", 0.2)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if comp.content != goodOutput || comp.tokens != 321 {
		t.Errorf("content/tokens wrong: %q %d", comp.content, comp.tokens)
	}
	if gotPath != responsesPath {
		t.Errorf("path = %s, want %s", gotPath, responsesPath)
	}
	if gotAuth != "Bearer unit-test-key-not-a-secret" {
		t.Errorf("auth header wrong: %q", gotAuth)
	}
	// The schema must ride in text.format — the Responses API ignores response_format, so
	// sending the old field would silently drop the guarantee and leave prose to scrape.
	if strings.Contains(gotBody, "response_format") {
		t.Errorf("request still carries the Chat Completions field: %s", gotBody)
	}
	for _, want := range []string{`"text"`, `"format"`, "json_schema", `"strict":true`,
		"additionalProperties", "bull_case", "risk_flags"} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("structured-output request is missing %q: %s", want, gotBody)
		}
	}
	// System text goes in instructions; the evidence goes in input as a user message.
	if !strings.Contains(gotBody, `"instructions":"sys"`) {
		t.Errorf("system text not sent as instructions: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"input"`) || !strings.Contains(gotBody, `"role":"user"`) {
		t.Errorf("evidence not sent as a user input message: %s", gotBody)
	}
}

func TestClientHTTPErrors(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	cases := []struct {
		name string
		code int
		body string
		want string
	}{
		{"401", http.StatusUnauthorized, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`, "Incorrect API key"},
		{"429", http.StatusTooManyRequests, `{"error":{"message":"Rate limit reached","type":"rate_limit_error"}}`, "Rate limit"},
		{"500", http.StatusInternalServerError, `{"error":{"message":"server error","type":"server_error"}}`, "server error"},
		{"502 no envelope", http.StatusBadGateway, `<html>gateway</html>`, "HTTP 502"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.code)
			w.Write([]byte(tc.body))
		}))
		c := NewClient(srv.URL, 5*time.Second)
		_, err := c.Complete(context.Background(), "m", "s", "u", 0)
		srv.Close()
		if err == nil {
			t.Errorf("%s: expected an error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err, tc.want)
		}
		// No error string may leak the key.
		if strings.Contains(err.Error(), "unit-test-key-not-a-secret") {
			t.Errorf("%s: the API key leaked into the error: %v", tc.name, err)
		}
	}
}

func TestClientTimeoutAndCancellation(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(respOK(goodOutput, 1)))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, 30*time.Millisecond).
		Complete(context.Background(), "m", "s", "u", 0); err == nil {
		t.Error("a client timeout must surface as an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewClient(srv.URL, 5*time.Second).Complete(ctx, "m", "s", "u", 0); err == nil {
		t.Error("a cancelled context must surface as an error")
	}
}

func TestClientMalformedAndEmptyResponses(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	for _, body := range []string{
		`{not json`,
		`{"status":"completed","output":[]}`,
		`{"status":"completed","output":[{"type":"reasoning","id":"rs_1"}]}`,
		`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"   "}]}]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		_, err := NewClient(srv.URL, 5*time.Second).Complete(context.Background(), "m", "s", "u", 0)
		srv.Close()
		if err == nil {
			t.Errorf("body %q should have failed", body)
		}
	}
}

// The text lives in a typed output array, so it must be located by type. Indexing output[0]
// would grab a reasoning item the moment a reasoning-capable model is configured.
func TestOutputTextWalksTypedItems(t *testing.T) {
	got, err := outputText([]outputItem{
		{Type: "reasoning"},
		{Type: "function_call"},
		{Type: "message", Role: "assistant", Content: []outputContent{
			{Type: "output_text", Text: `{"summary":`},
			{Type: "output_text", Text: `"ok"}`},
		}},
	})
	if err != nil || got != `{"summary":"ok"}` {
		t.Errorf("got %q, %v — text parts should concatenate in order", got, err)
	}

	// A refusal is a different fact from an empty reply and must say so.
	_, err = outputText([]outputItem{{Type: "message", Content: []outputContent{
		{Type: "refusal", Refusal: "I cannot assist with that."},
	}}})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("a refusal must surface as a refusal, got %v", err)
	}

	if _, err := outputText(nil); err == nil {
		t.Error("no output at all must be an error")
	}
}

// A truncated reply can still be valid JSON while missing half the reading, so an incomplete
// status is a failure rather than something to salvage.
func TestClientRejectsIncompleteResponse(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},
			"output":[{"type":"message","role":"assistant",
			"content":[{"type":"output_text","text":"{\"summary\":\"half\"}"}]}]}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, 5*time.Second).Complete(context.Background(), "m", "s", "u", 0)
	if err == nil || !strings.Contains(err.Error(), "max_output_tokens") {
		t.Errorf("incomplete response must fail with its reason, got %v", err)
	}
}

// temperature: 0 is the escape hatch for models that reject the parameter — it must drop the
// field entirely rather than sending an explicit zero.
func TestClientOmitsZeroTemperature(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Write([]byte(respOK(goodOutput, 1)))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 5*time.Second)

	if _, err := c.Complete(context.Background(), "m", "s", "u", 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "temperature") {
		t.Errorf("temperature 0 should be omitted: %s", body)
	}
	if _, err := c.Complete(context.Background(), "m", "s", "u", 0.2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"temperature":0.2`) {
		t.Errorf("a set temperature must be sent: %s", body)
	}
}

func TestClientRequiresKey(t *testing.T) {
	withKey(t, "")
	_, err := NewClient("http://127.0.0.1:1", time.Second).Complete(context.Background(), "m", "s", "u", 0)
	if err == nil || !strings.Contains(err.Error(), apiKeyEnv) {
		t.Errorf("a missing key must fail before any request, got %v", err)
	}
}

// ── Output parsing ───────────────────────────────────────────────────────────────────

func TestParseOutput(t *testing.T) {
	a := parseOutput("2330", "m", goodOutput, 100, 42)
	if !a.OK() {
		t.Fatalf("valid output should be OK: %+v", a)
	}
	if a.Summary == "" || len(a.BullCase) != 1 || len(a.BearCase) != 1 || len(a.RiskFlags) != 1 {
		t.Errorf("fields lost: %+v", a)
	}
	if a.Confidence != 0.72 || a.Tokens != 100 || a.LatencyMs != 42 {
		t.Errorf("metadata wrong: %+v", a)
	}
	// A fenced block is unwrapped — that does not change the payload.
	if !parseOutput("x", "m", "```json\n"+goodOutput+"\n```", 0, 0).OK() {
		t.Error("a fenced JSON block should still parse")
	}
}

// Malformed output becomes unavailable. There is deliberately no regex salvage and no
// fallback that could turn an unreadable reply into a trading view.
func TestParseOutputRejectsUnusable(t *testing.T) {
	for _, raw := range []string{
		`I think this stock looks bullish.`,
		`{"bull_case":["x"]}`, // no summary
		`{"summary":"   "}`,   // blank summary
		``,
	} {
		a := parseOutput("2330", "m", raw, 0, 0)
		if a.OK() {
			t.Errorf("%q should be unusable", raw)
		}
		if a.Status != StatusBadOutput {
			t.Errorf("%q → status %s, want BAD_OUTPUT", raw, a.Status)
		}
		if a.Summary != "" || len(a.BullCase) != 0 {
			t.Errorf("%q leaked partial content: %+v", raw, a)
		}
	}
	// An out-of-range confidence is clamped, not rejected — the reading may still be fine.
	a := parseOutput("x", "m", `{"summary":"s","confidence":9.5}`, 0, 0)
	if !a.OK() || a.Confidence != 1 {
		t.Errorf("confidence should clamp to 1, got %+v", a)
	}
}

// ── Analyzer ─────────────────────────────────────────────────────────────────────────

func cand(sym string, actionable bool, rank int) Candidate {
	return Candidate{Evidence: Evidence{Symbol: sym, Name: sym}, Actionable: actionable, Rank: rank}
}

func TestAnalyzerDisabledAndNoKey(t *testing.T) {
	a := NewAnalyzer(Config{}, nil)

	withKey(t, "unit-test-key-not-a-secret")
	got := a.Analyze(context.Background(), false, []Candidate{cand("2330", true, 90)})
	if got["2330"].Status != StatusDisabled || got["2330"].OK() {
		t.Errorf("disabled → %+v", got["2330"])
	}

	withKey(t, "")
	got = a.Analyze(context.Background(), true, []Candidate{cand("2330", true, 90)})
	if got["2330"].Status != StatusNoKey || got["2330"].OK() {
		t.Errorf("no key → %+v", got["2330"])
	}
}

// Cost control: non-candidates never cost a request, the budget truncates the rest, and a
// duplicate symbol cannot consume two slots.
func TestAnalyzerCostControl(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(respOK(goodOutput, 10)))
	}))
	defer srv.Close()

	a := NewAnalyzer(Config{BaseURL: srv.URL, MaxStocks: 2}, nil)
	got := a.Analyze(context.Background(), true, []Candidate{
		cand("AAA", true, 10),
		cand("BBB", true, 90),
		cand("CCC", true, 50),
		cand("DDD", false, 99), // not actionable
		cand("AAA", true, 10),  // duplicate
	})
	if calls != 2 {
		t.Errorf("made %d requests, want 2 (MaxStocks)", calls)
	}
	// Highest rank first: BBB and CCC get analysed, AAA is squeezed out.
	if !got["BBB"].OK() || !got["CCC"].OK() {
		t.Errorf("the two highest-ranked should be analysed: %+v %+v", got["BBB"], got["CCC"])
	}
	if got["AAA"].Status != StatusSkipped {
		t.Errorf("AAA should be skipped by the budget, got %s", got["AAA"].Status)
	}
	if got["DDD"].Status != StatusSkipped || got["DDD"].OK() {
		t.Errorf("a non-actionable candidate must never be sent: %+v", got["DDD"])
	}
	if len(got) != 4 {
		t.Errorf("duplicate symbol produced %d results, want 4 unique", len(got))
	}
}

// Every transport failure has to become a status, never an error the caller could propagate.
func TestAnalyzerNeverPropagatesFailure(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		want Status
	}{
		{"429", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }, StatusError},
		{"500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, StatusError},
		{"garbage", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{{{")) }, StatusError},
		{"prose", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(respOK("just some words", 5)))
		}, StatusBadOutput},
	} {
		srv := httptest.NewServer(tc.h)
		a := NewAnalyzer(Config{BaseURL: srv.URL}, nil)
		got := a.Analyze(context.Background(), true, []Candidate{cand("2330", true, 90)})
		srv.Close()

		res := got["2330"]
		if res == nil {
			t.Fatalf("%s: no result produced at all", tc.name)
		}
		if res.Status != tc.want {
			t.Errorf("%s: status %s, want %s", tc.name, res.Status, tc.want)
		}
		if res.OK() {
			t.Errorf("%s: a failure must not read as OK", tc.name)
		}
		if res.Reason == "" {
			t.Errorf("%s: a failure must say why", tc.name)
		}
	}
}

func TestAnalyzerSuccessShape(t *testing.T) {
	withKey(t, "unit-test-key-not-a-secret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(respOK(goodOutput, 250)))
	}))
	defer srv.Close()

	a := NewAnalyzer(Config{BaseURL: srv.URL, Model: "gpt-5.6-luna"}, nil)
	res := a.Analyze(context.Background(), true, []Candidate{cand("2330", true, 90)})["2330"]
	if !res.OK() {
		t.Fatalf("expected OK: %+v", res)
	}
	if res.Model != "gpt-5.6-luna" || res.Tokens != 250 {
		t.Errorf("metadata not recorded: %+v", res)
	}
}

// The prompt must state the constraints that stop the model taking over the scanner's job or
// inventing data it was never given.
func TestSystemInstructionStatesTheConstraints(t *testing.T) {
	for _, want := range []string{
		"Do not generate trading instructions",
		"Do not override the scanner's signal",
		"Do not invent missing financial data",
		"Only interpret the supplied evidence",
		"Clearly distinguish bullish and bearish evidence",
		"NO access to news",
		"Do not browse",
	} {
		if !strings.Contains(systemInstruction, want) {
			t.Errorf("system instruction is missing %q", want)
		}
	}
}

// The evidence sent must be exactly what the caller supplied — this layer never adds a field
// the scanner did not produce.
func TestUserMessageCarriesOnlySuppliedEvidence(t *testing.T) {
	msg, err := buildUserMessage(Evidence{Symbol: "2330", Stage: "PRE_BREAKOUT"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "2330") || !strings.Contains(msg, "PRE_BREAKOUT") {
		t.Errorf("supplied fields missing: %s", msg)
	}
	// Absent fields must not appear at all, so the model sees them as missing rather than
	// as a zero it might narrate.
	for _, absent := range []string{"market_regime", "institutional", "risk_label"} {
		if strings.Contains(msg, absent) {
			t.Errorf("unsupplied field %q leaked into the prompt", absent)
		}
	}
}
