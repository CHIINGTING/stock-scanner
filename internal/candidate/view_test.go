package candidate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// okEvidence is one fully-resolved candidate.
func okEvidence(sym, code, note string, score int, action scanner.WatchAction) Evidence {
	// Derive the market from the symbol rather than hardcoding it, or a .TWO fixture would
	// silently become .TW and an order assertion would fail for the wrong reason.
	market := "TW"
	if strings.HasSuffix(sym, ".TWO") {
		market = "TWO"
	}
	e := &scanner.WatchlistEntry{
		RocketScore: score, RocketStage: scanner.StagePreBreakout,
		WatchAction: action, ExplosionProb: "HIGH",
	}
	e.A = scanner.StockAnalysis{Symbol: code, Name: "測試", Close: 123.456}
	return Evidence{
		Candidate: Candidate{Symbol: sym, Code: code, Market: market, Note: note},
		Status:    StatusOK, Entry: e,
		Market:            LoadMarketContext("", nil, ""),
		InstitutionStatus: Disabled, NewsStatus: NotWired,
	}
}

// The view must carry the evidence through unchanged — it projects, it does not decide.
func TestViewProjectsWithoutRecomputing(t *testing.T) {
	ev := okEvidence("2330.TW", "2330", "AI semiconductor", 71, scanner.ActWait)
	v := BuildViews([]Evidence{ev})[0]

	if v.Symbol != "2330.TW" || v.Name != "測試" {
		t.Errorf("identity = %q / %q", v.Symbol, v.Name)
	}
	if v.Note != "AI semiconductor" {
		t.Errorf("note = %q — user metadata must survive verbatim", v.Note)
	}
	if v.RocketScore != 71 {
		t.Errorf("RocketScore = %d, want the scanner's 71", v.RocketScore)
	}
	if v.Action != string(scanner.ActWait) {
		t.Errorf("action = %q", v.Action)
	}
	if v.Stage != string(scanner.StagePreBreakout) {
		t.Errorf("stage = %q", v.Stage)
	}
	if v.ExplosionProb != "HIGH" {
		t.Errorf("prob = %q", v.ExplosionProb)
	}
	// Formatting matches the rest of the repo's price rendering rather than dumping a float.
	if v.Price != "123.46" {
		t.Errorf("price = %q, want two decimals", v.Price)
	}

	// The source entry must be untouched by the projection.
	if ev.Entry.RocketScore != 71 || ev.Entry.WatchAction != scanner.ActWait {
		t.Error("building the view mutated the scanner entry")
	}
}

// Availability must survive projection. A missing block must never render as a value.
func TestViewKeepsAvailabilityDistinct(t *testing.T) {
	ev := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	ev.InstitutionStatus = Unavailable
	v := BuildViews([]Evidence{ev})[0]

	byName := map[string]Block{}
	for _, b := range v.Blocks {
		byName[b.Name] = b
	}

	if byName["Institution"].Status != Unavailable {
		t.Errorf("institution = %q, want UNAVAILABLE", byName["Institution"].Status)
	}
	if byName["Institution"].Detail != "" {
		t.Errorf("an unavailable block carried a value: %q", byName["Institution"].Detail)
	}
	if byName["Market"].Status != Unavailable {
		t.Errorf("market = %q", byName["Market"].Status)
	}
	if byName["Market"].Detail != "" {
		t.Errorf("an unavailable market carried %q — a reader would take that as a regime",
			byName["Market"].Detail)
	}

	// DISABLED must project as DISABLED, never collapsed into UNAVAILABLE: one is a
	// decision, the other is a fact about the data.
	ev.InstitutionStatus = Disabled
	v2 := BuildViews([]Evidence{ev})[0]
	for _, b := range v2.Blocks {
		if b.Name == "Institution" && b.Status != Disabled {
			t.Errorf("a disabled feature projected as %q", b.Status)
		}
	}
}

// A zero institutional net IS a reading and must be shown as available — with the number,
// because the status beside it removes the ambiguity.
func TestViewZeroFlowIsAvailable(t *testing.T) {
	ev := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	ev.InstitutionStatus = Available
	ev.Entry.Institution = &institution.StockChipView{Code: "2330", AsOfDate: "2026-08-31"}

	v := BuildViews([]Evidence{ev})[0]
	var inst Block
	for _, b := range v.Blocks {
		if b.Name == "Institution" {
			inst = b
		}
	}
	if inst.Status != Available {
		t.Fatalf("a real snapshot with net 0 projected as %q", inst.Status)
	}
	if !strings.Contains(inst.Detail, "0") {
		t.Errorf("the zero reading was dropped: %q", inst.Detail)
	}

	// And it must be distinguishable from the unavailable case.
	ev2 := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	ev2.InstitutionStatus = Unavailable
	v2 := BuildViews([]Evidence{ev2})[0]
	for _, b := range v2.Blocks {
		if b.Name == "Institution" && b.Status == inst.Status {
			t.Error("zero flow and absent data projected identically")
		}
	}
}

// A market with a real score of zero is AVAILABLE; a missing one is not.
func TestViewMarketZeroVersusMissing(t *testing.T) {
	zero := 0.0
	ev := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	ev.Market = LoadMarketContext("SIDEWAYS", &zero, "2026-08-31")

	v := BuildViews([]Evidence{ev})[0]
	for _, b := range v.Blocks {
		if b.Name != "Market" {
			continue
		}
		if b.Status != Available {
			t.Errorf("a real market with score 0 projected as %q", b.Status)
		}
		if !strings.Contains(b.Detail, "SIDEWAYS") {
			t.Errorf("regime lost: %q", b.Detail)
		}
	}
}

// Data quality is completeness, and INSUFFICIENT is reserved for a stock with no data at all.
func TestDataQualityIsCompletenessOnly(t *testing.T) {
	// Nothing resolved.
	bad := Evidence{
		Candidate: Candidate{Symbol: "9999.TW", Code: "9999", Market: "TW"},
		Status:    StatusFetchError, Reason: "no price data",
		Market: LoadMarketContext("", nil, ""), InstitutionStatus: Unavailable,
	}
	if got := BuildViews([]Evidence{bad})[0].DataQuality; got != QualityInsufficient {
		t.Errorf("unfetched candidate → %q, want INSUFFICIENT", got)
	}

	// Resolved, but some blocks off/absent.
	partial := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	if got := BuildViews([]Evidence{partial})[0].DataQuality; got != QualityPartial {
		t.Errorf("partly-available candidate → %q, want PARTIAL", got)
	}

	// A high-scoring stock with poor data is still PARTIAL — quality is about EVIDENCE.
	high := okEvidence("2330.TW", "2330", "", 99, scanner.ActBreakoutBuy)
	if got := BuildViews([]Evidence{high})[0].DataQuality; got != QualityPartial {
		t.Errorf("a 99-score stock with thin evidence → %q; data quality must not track the "+
			"stock's attractiveness", got)
	}
}

// Order is the human's. Nothing may re-sort it — least of all by the scanner's score.
func TestViewPreservesCandidateOrder(t *testing.T) {
	ev := []Evidence{
		okEvidence("2327.TW", "2327", "first", 10, scanner.ActWait),
		okEvidence("2330.TW", "2330", "second", 99, scanner.ActBreakoutBuy), // highest score
		okEvidence("6488.TWO", "6488", "third", 50, scanner.ActWait),
	}
	views := BuildViews(ev)
	want := []string{"2327.TW", "2330.TW", "6488.TWO"}
	for i, w := range want {
		if views[i].Symbol != w {
			t.Fatalf("position %d = %s, want %s — the view re-sorted (probably by score)",
				i, views[i].Symbol, w)
		}
	}
}

// ── CLI rendering ─────────────────────────────────────────────────────────────────────

func render(t *testing.T, ev []Evidence) string {
	t.Helper()
	var buf bytes.Buffer
	RenderText(&buf, BuildViews(ev))
	return buf.String()
}

// A failed candidate must appear, and must NOT print a price, action or score — a "0.00"
// would be read as a real price by anyone skimming.
func TestRenderFailedCandidateShowsNoFakeValues(t *testing.T) {
	out := render(t, []Evidence{{
		Candidate: Candidate{Symbol: "9999.TW", Code: "9999", Market: "TW", Note: "gone"},
		Status:    StatusFetchError, Reason: "no price data for 9999.TW",
		Market: LoadMarketContext("", nil, ""), InstitutionStatus: Unavailable,
	}})

	if !strings.Contains(out, "9999.TW") {
		t.Error("the failed candidate vanished from the output")
	}
	if !strings.Contains(out, string(StatusFetchError)) {
		t.Error("the failure status is not shown")
	}
	if !strings.Contains(out, "no price data") {
		t.Error("the reason is not shown")
	}
	for _, forbidden := range []string{"Price:", "RocketScore:", "Action:"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("%q was printed for a candidate with no data", forbidden)
		}
	}
}

// Every scanner value must be labelled as the scanner's, so nobody reads it as a research
// verdict this command reached.
func TestRenderLabelsScannerEvidence(t *testing.T) {
	out := render(t, []Evidence{okEvidence("2330.TW", "2330", "note here", 71, scanner.ActWait)})

	for _, field := range []string{"Action:", "Stage:", "RocketScore:"} {
		i := strings.Index(out, field)
		if i < 0 {
			t.Fatalf("%q missing", field)
		}
		line := out[i:]
		if j := strings.IndexByte(line, '\n'); j >= 0 {
			line = line[:j]
		}
		if !strings.Contains(line, "(scanner)") {
			t.Errorf("%q is not labelled as scanner evidence: %q", field, line)
		}
	}
	if strings.Contains(out, "Candidate BUY") || strings.Contains(out, "Rating") {
		t.Error("the renderer produced a candidate-level verdict")
	}
	if !strings.Contains(out, "note here") {
		t.Error("the note was dropped")
	}
}

// A partial failure must not cost the rest of the run.
func TestRenderKeepsEveryCandidate(t *testing.T) {
	out := render(t, []Evidence{
		okEvidence("2327.TW", "2327", "", 40, scanner.ActWait),
		{Candidate: Candidate{Symbol: "9999.TW", Code: "9999", Market: "TW"},
			Status: StatusFetchError, Reason: "boom",
			Market: LoadMarketContext("", nil, ""), InstitutionStatus: Unavailable},
		okEvidence("2330.TW", "2330", "", 60, scanner.ActWait),
	})

	for _, sym := range []string{"2327.TW", "9999.TW", "2330.TW"} {
		if !strings.Contains(out, sym) {
			t.Errorf("%s is missing — one failure must not remove the others", sym)
		}
	}
	// And in the original order.
	i1, i2, i3 := strings.Index(out, "2327.TW"), strings.Index(out, "9999.TW"), strings.Index(out, "2330.TW")
	if !(i1 < i2 && i2 < i3) {
		t.Errorf("order changed: 2327@%d 9999@%d 2330@%d", i1, i2, i3)
	}
	if !strings.Contains(out, "3 candidates.") {
		t.Error("the total does not count every candidate")
	}
}

// An empty universe is a valid state, and must never fall back to the scanner's watchlist.
func TestRenderEmpty(t *testing.T) {
	out := render(t, nil)
	if !strings.Contains(out, "No candidates") {
		t.Errorf("empty output = %q", out)
	}
	if strings.Contains(out, "watchlist") {
		t.Error("the empty case mentioned the watchlist")
	}
}
