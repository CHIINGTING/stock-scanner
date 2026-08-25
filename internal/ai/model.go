// Package ai is the R11 shadow-only explanation layer: it hands evidence the scanner has
// ALREADY computed to an LLM and gets back a readable bull/bear reading.
//
// It is an interpretation layer, never a signal source. Nothing here may influence the
// technical score, institutional score, market score, Market Regime, Stage, BUY/WATCH/SELL,
// ranking, recommendation or position sizing. Three things enforce that rather than trusting
// a comment:
//
//   - the result lands on a DEDICATED WatchlistEntry field, deliberately outside
//     ShadowSignals — which is the only structure the C6b guardrail scoring reads, so this
//     data cannot reach the scoring path even by mistake;
//   - AttachAI is a post-pass, run after scores, actions and sort order are already final;
//   - a test enumerates AI success / failure / disabled and asserts the entries are
//     byte-identical in every scored field.
//
// FAIL-OPEN is the other half of the contract. Missing key, timeout, 429, 5xx, malformed
// JSON, or the feature being off must all leave the scan and the report completing normally.
// The analyzer therefore never returns an error to its caller — it returns an Analysis whose
// Status says what went wrong.
package ai

import "time"

// Status records how one stock's analysis ended. It is persisted with the result so a report
// (or a later reader) can distinguish "the model said nothing useful" from "we never asked".
type Status string

const (
	StatusOK        Status = "OK"
	StatusDisabled  Status = "DISABLED"   // enable_ai is false
	StatusNoKey     Status = "NO_API_KEY" // OPENAI_API_KEY unset
	StatusSkipped   Status = "SKIPPED"    // not a candidate, or beyond max_stocks
	StatusError     Status = "ERROR"      // transport, non-2xx, timeout
	StatusBadOutput Status = "BAD_OUTPUT" // 2xx but the payload was not usable
)

// Config is the AI block of the scanner config. It deliberately has NO API key field: the
// token comes from the environment at request time and is never stored on a struct, so it
// cannot be serialised into a report, a snapshot, or a log line by accident.
type Config struct {
	// Model is the OpenAI model id, e.g. "gpt-5.6-luna".
	Model string `yaml:"model"`
	// TimeoutSec bounds one request. Zero → 30.
	TimeoutSec int `yaml:"timeout_sec"`
	// MaxStocks caps how many stocks are analysed per run — the cost gate. Zero → 12.
	MaxStocks int `yaml:"max_stocks"`
	// Temperature is passed through; low values keep the reading close to the evidence.
	Temperature float64 `yaml:"temperature"`
	// BaseURL overrides the API root. Empty → the official endpoint. It exists so tests can
	// point at httptest; it is not a gateway hook.
	BaseURL string `yaml:"base_url"`
}

// Defaulted fills zero values with the documented defaults, matching the convention used by
// the market and institution configs.
func (c Config) Defaulted() Config {
	if c.Model == "" {
		c.Model = "gpt-5.6-luna"
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 30
	}
	if c.MaxStocks <= 0 {
		c.MaxStocks = 12
	}
	if c.Temperature < 0 {
		c.Temperature = 0
	}
	return c
}

// Timeout is the per-request duration.
func (c Config) Timeout() time.Duration { return time.Duration(c.Defaulted().TimeoutSec) * time.Second }

// Evidence is what gets sent to the model: fields the scanner ALREADY derived, never raw
// price series and never anything recomputed here.
//
// Every field is optional. A value the scanner did not produce is simply absent from the
// prompt — the model is instructed not to invent it, and this layer never fills a gap.
type Evidence struct {
	Symbol string  `json:"symbol"`
	Name   string  `json:"name,omitempty"`
	Price  float64 `json:"price,omitempty"`

	Stage         string `json:"stage,omitempty"`        // RocketStage
	WatchAction   string `json:"watch_action,omitempty"` // scanner's own signal
	RocketScore   int    `json:"rocket_score,omitempty"`
	ExplosionProb string `json:"explosion_probability,omitempty"`

	TechnicalScore int `json:"technical_score,omitempty"`
	BestFourPoint  int `json:"best_four_point,omitempty"`

	// Only MA20 is carried: it is the only moving average StockAnalysis holds. Adding
	// MA60/MA120 here would require deriving them in the AI layer, which this feature is
	// explicitly not allowed to do.
	MA20DistancePct   float64 `json:"ma20_distance_pct,omitempty"`
	MA20Trend         string  `json:"ma20_trend,omitempty"`
	VolumeRatio       float64 `json:"volume_ratio,omitempty"`
	PriceVolumeSignal string  `json:"price_volume_signal,omitempty"`

	// The magnitude of the session's move. Without these the model sees "價漲量縮" for both
	// a +0.2% drift and a +7.5% surge, and cannot tell them apart. PriceChangeATR says how
	// big that move was FOR THIS STOCK.
	PriceChangePct   float64 `json:"price_change_pct,omitempty"`
	PriceChangeATR   float64 `json:"price_change_atr,omitempty"`
	PriceMove        string  `json:"price_move,omitempty"`
	PriceVolumeState string  `json:"price_volume_state,omitempty"`

	Consolidation string `json:"consolidation,omitempty"`

	Sector      string `json:"sector,omitempty"`
	SectorFlow  string `json:"sector_flow,omitempty"`
	SectorStage string `json:"sector_stage,omitempty"`

	// Institutional lines, present only when the institution layer is enabled and had data.
	Institutional []string `json:"institutional,omitempty"`

	MarketRegime string  `json:"market_regime,omitempty"`
	MarketScore  float64 `json:"market_score,omitempty"`

	RiskLabel   string   `json:"risk_label,omitempty"`
	RiskWarning string   `json:"risk_warning,omitempty"`
	Reasons     []string `json:"reasons,omitempty"`
}

// Analysis is one stock's shadow reading.
//
// Confidence is the MODEL'S confidence in its own interpretation of the supplied evidence.
// It is explicitly NOT a trading confidence and must never be presented or used as one.
type Analysis struct {
	Symbol     string   `json:"symbol"`
	Status     Status   `json:"status"`
	Summary    string   `json:"summary,omitempty"`
	BullCase   []string `json:"bull_case,omitempty"`
	BearCase   []string `json:"bear_case,omitempty"`
	RiskFlags  []string `json:"risk_flags,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`

	Model     string `json:"model,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	// Reason explains a non-OK status in one line, for the report and the log.
	Reason string `json:"reason,omitempty"`
	// Tokens is usage when the API reported it; 0 otherwise.
	Tokens int `json:"tokens,omitempty"`
}

// OK reports whether this analysis carries a usable reading.
func (a *Analysis) OK() bool { return a != nil && a.Status == StatusOK && a.Summary != "" }

// unavailable builds a non-OK analysis. Every failure path goes through here so no caller
// can accidentally return a half-populated success.
func unavailable(symbol string, st Status, reason string) *Analysis {
	return &Analysis{Symbol: symbol, Status: st, Reason: reason}
}

// modelOutput is the JSON contract the model is asked to produce. Parsing is strict: an
// unusable payload becomes BAD_OUTPUT rather than being salvaged with regex, and there is
// deliberately no field here that could be read as a buy/sell instruction.
type modelOutput struct {
	Summary    string   `json:"summary"`
	BullCase   []string `json:"bull_case"`
	BearCase   []string `json:"bear_case"`
	RiskFlags  []string `json:"risk_flags"`
	Confidence float64  `json:"confidence"`
}
