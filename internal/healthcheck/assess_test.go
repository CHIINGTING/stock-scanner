package healthcheck

import (
	"strings"
	"testing"
)

// hb builds a Health with the fields the assessment reads, so a test states exactly the
// situation it is about.
type hb struct {
	priceStatus Status
	percentile  *float64
	margin      *float64
	risk        string
	limit       string
	action      string
}

func (o hb) build() *Health {
	// Default to the scanner's real "no risk" marker rather than "": a fixture that used the
	// empty string would not exercise the production shape at all, which is how the HAZARD
	// bug survived its own test suite.
	if o.risk == "" {
		o.risk = RiskNone
	}
	h := &Health{
		Price:     PriceBlock{Status: StatusAvailable, Close: Num(100, "TWD", 2, "t")},
		Technical: TechnicalBlock{RiskLabel: o.risk, LimitStatus: o.limit, ScannerAction: o.action},
		Valuation: ValuationBlock{Historical: HistoricalPEBlock{
			CurrentPercentile: Missing(StatusInsufficientData, "樣本不足")}},
		Target: TargetPriceBlock{
			MarginOfSafety: Missing(StatusInsufficientData, "沒有目標價"),
			Reason:         "樣本不足"},
		DataQuality: DataQuality{Level: QualityPartial},
	}
	if o.priceStatus != "" {
		h.Price.Status = o.priceStatus
		if o.priceStatus != StatusAvailable {
			h.Price.Close = Missing(o.priceStatus, "沒有價格")
			h.Price.Reason = "沒有價格"
		}
	}
	if o.percentile != nil {
		h.Valuation.Historical.CurrentPercentile = Num(*o.percentile, "%", 0, "t")
	}
	if o.margin != nil {
		h.Target.MarginOfSafety = Num(*o.margin, "%", 1, "t")
	}
	return h
}

func fp(v float64) *float64 { return &v }

// ── the verdicts ──────────────────────────────────────────────────────────────────────

func TestAssessmentVerdicts(t *testing.T) {
	cases := []struct {
		name string
		in   hb
		want string
		rule string
	}{
		{"no price at all", hb{priceStatus: StatusUnavailable},
			AssessInsufficientData, "NO_PRICE"},

		// The scanner writes "—" when there is NO risk. Reading "not empty" as "has a hazard"
		// fired on every stock that has a watchlist entry — which is every stock this page can
		// check — and made HIGH_RISK the only reachable verdict.
		{"no risk at all (the scanner's own '—')",
			hb{risk: RiskNone, limit: "", margin: fp(40), action: "BUY"},
			AssessAttractive, "VALUE_AND_SETUP"},
		{"empty risk label", hb{risk: "", margin: fp(40), action: "BUY"},
			AssessAttractive, "VALUE_AND_SETUP"},

		{"pattern failed", hb{risk: "跌破支撐", margin: fp(50), action: "BUY"},
			AssessHighRisk, "HAZARD"},
		{"overextended", hb{risk: "追高", margin: fp(50), action: "BUY"},
			AssessHighRisk, "HAZARD"},
		{"limit-up distribution", hb{limit: "DISTRIBUTION_AFTER_LIMIT_UP", margin: fp(50), action: "BUY"},
			AssessHighRisk, "HAZARD"},
		{"limit-up failed", hb{limit: "LIMIT_UP_FAILED", margin: fp(50), action: "BUY"},
			AssessHighRisk, "HAZARD"},

		// The scanner documents LOCKED_LIMIT_UP_LOW_VOLUME as 中性偏多 and excludes it from
		// its own negative set. Reading it as a hazard here would contradict the source.
		{"locked limit-up, low volume is NOT a hazard",
			hb{limit: "LOCKED_LIMIT_UP_LOW_VOLUME", margin: fp(50), action: "BUY"},
			AssessAttractive, "VALUE_AND_SETUP"},

		// A caution is real, but it is not "untouchable regardless of price": it costs the
		// stock ATTRACTIVE, not everything.
		{"caution blocks attractive", hb{risk: "量不足", margin: fp(50), action: "BUY"},
			AssessAcceptable, "DEFAULT"},
		{"caution with a thin margin", hb{risk: "上影/收弱", margin: fp(3), action: "BUY"},
			AssessAcceptable, "DEFAULT"},

		{"expensive and no upside", hb{percentile: fp(92), margin: fp(-12), action: "WATCH"},
			AssessOvervalued, "EXPENSIVE"},
		// A high percentile ALONE is a stock that re-rated, not a reason to stay out.
		{"expensive but still has upside", hb{percentile: fp(92), margin: fp(30), action: "BUY"},
			AssessAttractive, "VALUE_AND_SETUP"},
		// Negative margin at a normal percentile is not OVERVALUED, it is just no entry.
		{"cheap-ish but no upside", hb{percentile: fp(30), margin: fp(-5), action: "WATCH"},
			AssessWait, "NO_UPSIDE"},

		{"no target price", hb{action: "BUY"}, AssessWait, "NO_MARGIN"},

		{"value and setup", hb{margin: fp(22), action: "BUY"},
			AssessAttractive, "VALUE_AND_SETUP"},
		{"value, no setup", hb{margin: fp(22), action: "WATCH"},
			AssessWait, "VALUE_NO_SETUP"},
		{"thin margin with a BUY", hb{margin: fp(5), action: "BUY"},
			AssessAcceptable, "DEFAULT"},
	}
	for _, tc := range cases {
		got := assess(tc.in.build())
		if got.Verdict != tc.want {
			t.Errorf("%s: verdict = %s, want %s (decided by %s)", tc.name, got.Verdict,
				tc.want, got.DecidedBy)
		}
		if got.DecidedBy != tc.rule {
			t.Errorf("%s: decided by %s, want %s", tc.name, got.DecidedBy, tc.rule)
		}
		if got.Reason == "" {
			t.Errorf("%s: no reason", tc.name)
		}
	}
}

// The thresholds are boundaries, and a rule that fires at 14.9% but not 15.0% would be a
// different rule than the one documented.
func TestAssessmentThresholdsAreExact(t *testing.T) {
	atAttractive := assess(hb{margin: fp(MarginAttractive), action: "BUY"}.build())
	if atAttractive.Verdict != AssessAttractive {
		t.Errorf("margin exactly %.0f with a BUY = %s, want ATTRACTIVE",
			MarginAttractive, atAttractive.Verdict)
	}
	justUnder := assess(hb{margin: fp(MarginAttractive - 0.1), action: "BUY"}.build())
	if justUnder.Verdict == AssessAttractive {
		t.Errorf("margin just under the threshold was still ATTRACTIVE")
	}

	atExpensive := assess(hb{percentile: fp(PercentileExpensive), margin: fp(-1)}.build())
	if atExpensive.Verdict != AssessOvervalued {
		t.Errorf("percentile exactly %.0f with negative margin = %s, want OVERVALUED",
			PercentileExpensive, atExpensive.Verdict)
	}
	justBelow := assess(hb{percentile: fp(PercentileExpensive - 0.1), margin: fp(-1)}.build())
	if justBelow.Verdict == AssessOvervalued {
		t.Errorf("percentile just below the threshold was still OVERVALUED")
	}
	// Zero margin is the ACCEPTABLE boundary, not a negative.
	atZero := assess(hb{margin: fp(0), action: "WATCH"}.build())
	if atZero.DecidedBy != "VALUE_NO_SETUP" {
		t.Errorf("a zero margin decided by %s, want VALUE_NO_SETUP", atZero.DecidedBy)
	}
}

// ── the properties that make it an assessment rather than a score ─────────────────────

// Every rule is reported whether or not it fired, so a reader sees what was checked.
func TestEveryRuleIsReportedAndTraceable(t *testing.T) {
	got := assess(hb{margin: fp(22), action: "BUY"}.build())
	if len(got.Rules) == 0 {
		t.Fatal("no rules reported")
	}
	var fired int
	for _, r := range got.Rules {
		if r.ID == "" || r.Description == "" {
			t.Errorf("a rule has no id or description: %+v", r)
		}
		if r.Fired {
			fired++
			if r.Detail == "" && r.ID != "NO_PRICE" && r.ID != "NO_MARGIN" {
				t.Errorf("%s fired with no evidence detail", r.ID)
			}
		}
	}
	if fired != 1 {
		t.Errorf("%d rules fired; the first match must decide", fired)
	}
	// The threshold that was applied has to be visible, not inferred.
	last := got.Rules[len(got.Rules)-1]
	if !strings.Contains(last.Description, "15") {
		t.Errorf("the deciding rule does not state its threshold: %q", last.Description)
	}
	if got.Inputs["margin_of_safety"] == "" || got.Inputs["scanner_action"] == "" {
		t.Errorf("the inputs the rules read are not reported: %+v", got.Inputs)
	}
}

// The vocabulary must not overlap the scanner's, or a reader will treat one as the other.
func TestVocabularyIsSeparateFromTheScanners(t *testing.T) {
	scanner := map[string]bool{"BUY": true, "WATCH": true, "SELL": true, "HOLD": true}
	for _, v := range []string{AssessAttractive, AssessAcceptable, AssessWait,
		AssessOvervalued, AssessHighRisk, AssessInsufficientData} {
		if scanner[v] {
			t.Errorf("%q is a scanner action; the vocabularies must not overlap", v)
		}
	}
	got := assess(hb{margin: fp(22), action: "BUY"}.build())
	if got.Note == "" || !strings.Contains(got.Note, "掃描器") {
		t.Errorf("the block does not state its boundary against the scanner: %q", got.Note)
	}
}

// A missing figure must never be read as a zero. An absent margin of safety is "we do not
// know", and reading it as 0 would silently make every unknown a WAIT via NO_UPSIDE.
func TestMissingFiguresAreNotReadAsZero(t *testing.T) {
	got := assess(hb{action: "BUY"}.build()) // no margin at all
	if got.DecidedBy != "NO_MARGIN" {
		t.Fatalf("an absent margin decided by %s, want NO_MARGIN", got.DecidedBy)
	}
	if got.Verdict != AssessWait {
		t.Fatalf("verdict = %s", got.Verdict)
	}
	// And an absent percentile must not make EXPENSIVE fire.
	neg := assess(hb{margin: fp(-30), action: "WATCH"}.build())
	if neg.DecidedBy == "EXPENSIVE" {
		t.Fatalf("EXPENSIVE fired with no percentile at all")
	}
}

// The verdict is stable: the same evidence always gives the same answer.
func TestAssessmentIsDeterministic(t *testing.T) {
	in := hb{percentile: fp(55), margin: fp(18), action: "BUY"}
	first := assess(in.build())
	for i := 0; i < 20; i++ {
		got := assess(in.build())
		if got.Verdict != first.Verdict || got.DecidedBy != first.DecidedBy {
			t.Fatalf("run %d gave %s/%s, want %s/%s", i, got.Verdict, got.DecidedBy,
				first.Verdict, first.DecidedBy)
		}
	}
}

// A nil Health is refused rather than panicking.
func TestNilHealthIsInsufficient(t *testing.T) {
	if got := assess(nil); got.Verdict != AssessInsufficientData {
		t.Fatalf("verdict = %s", got.Verdict)
	}
}

// The empty-string risk label is what the fixture used to default to, and it is NOT what
// production emits. This pins the real value so the bug cannot come back through a fixture.
func TestScannerNoRiskMarkerIsNotAHazard(t *testing.T) {
	if RiskNone == "" {
		t.Fatal("RiskNone must be the scanner's real marker, not the empty string")
	}
	got := assess(hb{risk: RiskNone, margin: fp(40), action: "BUY"}.build())
	if got.Verdict == AssessHighRisk {
		t.Fatalf("a stock with no risk label was judged HIGH_RISK: %+v", got)
	}
	// Every rule after HAZARD must be reachable, or the assessment has one outcome.
	reached := map[string]bool{}
	for _, tc := range []hb{
		{risk: RiskNone, margin: fp(40), action: "BUY"},
		{risk: RiskNone, margin: fp(5), action: "BUY"},
		{risk: RiskNone, margin: fp(20), action: "WATCH"},
		{risk: RiskNone, margin: fp(-5), action: "WATCH"},
		{risk: RiskNone, percentile: fp(95), margin: fp(-5), action: "WATCH"},
		{risk: RiskNone, action: "BUY"},
		{risk: "跌破支撐", margin: fp(40), action: "BUY"},
		{risk: RiskNone, priceStatus: StatusUnavailable},
	} {
		reached[assess(tc.build()).Verdict] = true
	}
	for _, want := range []string{AssessAttractive, AssessAcceptable, AssessWait,
		AssessOvervalued, AssessHighRisk, AssessInsufficientData} {
		if !reached[want] {
			t.Errorf("%s is unreachable in production shapes", want)
		}
	}
}

// The hazard sets must agree with internal/scanner's own severity distinction rather than
// reinterpreting it.
func TestHazardSetsMatchTheScannersOwnSemantics(t *testing.T) {
	// The scanner documents this one as 中性偏多 and excludes it from climaxDistribution.
	if hazardLimitStatuses["LOCKED_LIMIT_UP_LOW_VOLUME"] {
		t.Error("LOCKED_LIMIT_UP_LOW_VOLUME is 中性偏多 in internal/scanner; it is not a hazard")
	}
	for _, want := range []string{"LIMIT_UP_FAILED", "DISTRIBUTION_AFTER_LIMIT_UP"} {
		if !hazardLimitStatuses[want] {
			t.Errorf("%s is distribution in internal/scanner and must be a hazard", want)
		}
	}
	// Cautions are not hazards.
	for _, caution := range []string{"族群轉弱", "假突破", "量不足", "上影/收弱"} {
		if hazardRiskLabels[caution] {
			t.Errorf("%q is a caution, not a reason to refuse regardless of price", caution)
		}
		if !hasRisk(caution) {
			t.Errorf("%q should still register as a risk label", caution)
		}
	}
	if hasRisk(RiskNone) || hasRisk("") {
		t.Error("the no-risk marker must not register as a risk")
	}
}
