package ai

import (
	"context"
	"sort"
	"time"
)

// Analyzer runs the shadow explanation pass over a run's candidates.
//
// It NEVER returns an error. Every failure — no key, timeout, 429, 5xx, garbage output —
// becomes an Analysis with a non-OK Status, so a caller cannot accidentally propagate an AI
// problem into the scan. That is the fail-open contract stated in the package doc.
type Analyzer struct {
	Cfg    Config
	Client *Client
	// Logf is optional. It is called with progress lines that deliberately contain no
	// prompt text and no secret — symbol, model, latency, status, tokens only.
	Logf func(format string, args ...any)
}

// NewAnalyzer builds an Analyzer from config.
func NewAnalyzer(cfg Config, logf func(string, ...any)) *Analyzer {
	cfg = cfg.Defaulted()
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Analyzer{Cfg: cfg, Client: NewClient(cfg.BaseURL, cfg.Timeout()), Logf: logf}
}

// Candidate pairs one stock's evidence with the rank used to decide whether it is worth
// spending a request on.
type Candidate struct {
	Evidence Evidence
	// Actionable marks the scanner's own "this is a live candidate" judgement. Only
	// actionable stocks are analysed — the cost gate rides on the scanner's existing notion
	// of a candidate rather than inventing a second one.
	Actionable bool
	// Rank orders actionable candidates; higher is analysed first (RocketScore in practice).
	Rank int
}

// Analyze returns one Analysis per input symbol, keyed by symbol.
//
// Cost control, in order:
//  1. non-actionable candidates are SKIPPED without a request;
//  2. the rest are sorted by Rank and truncated to MaxStocks;
//  3. duplicate symbols within the run collapse to a single request.
//
// One stock therefore costs at most one request, and a run costs at most MaxStocks.
func (a *Analyzer) Analyze(ctx context.Context, enabled bool, candidates []Candidate) map[string]*Analysis {
	out := make(map[string]*Analysis, len(candidates))

	// Deduplicate first so a repeated symbol cannot consume two slots of the budget.
	seen := make(map[string]bool, len(candidates))
	uniq := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		sym := c.Evidence.Symbol
		if sym == "" || seen[sym] {
			continue
		}
		seen[sym] = true
		uniq = append(uniq, c)
	}

	if !enabled {
		for _, c := range uniq {
			out[c.Evidence.Symbol] = unavailable(c.Evidence.Symbol, StatusDisabled, "AI 分析未啟用")
		}
		return out
	}
	// Checked once per run, not once per stock: without a key every request would 401, and
	// issuing them anyway would waste time and fill the log with noise.
	if !HasAPIKey() {
		a.Logf("ai: %s not set — skipping AI analysis (scan continues normally)", apiKeyEnv)
		for _, c := range uniq {
			out[c.Evidence.Symbol] = unavailable(c.Evidence.Symbol, StatusNoKey, "未設定 OPENAI_API_KEY")
		}
		return out
	}

	var pool []Candidate
	for _, c := range uniq {
		if c.Actionable {
			pool = append(pool, c)
			continue
		}
		out[c.Evidence.Symbol] = unavailable(c.Evidence.Symbol, StatusSkipped, "非可操作候選，未送出分析")
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].Rank > pool[j].Rank })

	limit := a.Cfg.Defaulted().MaxStocks
	if len(pool) > limit {
		for _, c := range pool[limit:] {
			out[c.Evidence.Symbol] = unavailable(c.Evidence.Symbol, StatusSkipped,
				"超出本次分析檔數上限")
		}
		pool = pool[:limit]
	}

	a.Logf("ai: analysing %d candidates with model %s", len(pool), a.Cfg.Model)
	for _, c := range pool {
		out[c.Evidence.Symbol] = a.analyzeOne(ctx, c.Evidence)
	}
	return out
}

// analyzeOne performs a single request. It is the only place a failure is converted into a
// status, and it never panics or returns an error.
func (a *Analyzer) analyzeOne(ctx context.Context, e Evidence) *Analysis {
	user, err := buildUserMessage(e)
	if err != nil {
		return unavailable(e.Symbol, StatusError, "無法組出分析輸入")
	}

	start := time.Now()
	comp, err := a.Client.Complete(ctx, a.Cfg.Model, systemInstruction, user, a.Cfg.Temperature)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		// The error text comes from client.go, which is careful to exclude the key and the
		// request body. Log it as-is.
		a.Logf("ai: %s failed after %dms: %v", e.Symbol, latency, err)
		return unavailable(e.Symbol, StatusError, err.Error())
	}

	res := parseOutput(e.Symbol, a.Cfg.Model, comp.content, comp.tokens, latency)
	a.Logf("ai: %s model=%s latency=%dms status=%s tokens=%d",
		e.Symbol, a.Cfg.Model, latency, res.Status, comp.tokens)
	return res
}
