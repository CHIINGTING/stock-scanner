package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validSnapshot() *Snapshot {
	s := NewSnapshot("2026-08-07", time.Date(2026, 8, 7, 15, 30, 0, 0, time.UTC))
	s.Score = 67
	s.Confidence = 0.72
	s.Regime = RegimeBullPullback
	s.RuleID = RulePullbackOrderly
	s.Structure = StructureView{
		Benchmark: Benchmark0050, Structure: StructureUptrend,
		Breadth: BreadthCooling, Posture: PostureNeutral,
		TrendIntact: true, OrderlyCorrection: true,
	}
	s.Evidence = []Evidence{
		{Source: EvidencePrice, Direction: DirBullish, Strength: StrengthModerate, Confidence: 0.8, Available: true},
		MissingEvidence(EvidenceMargin, "MI_MARGN timeout"),
	}
	return s
}

// A fresh snapshot must start UNKNOWN, not at the zero value: "" is a regime nobody defined
// and would serialize into the archive as an undecodable state.
func TestNewSnapshotStartsUnknown(t *testing.T) {
	s := NewSnapshot("2026-08-07", time.Now())
	if s.Regime != RegimeUnknown {
		t.Errorf("Regime = %q, want UNKNOWN", s.Regime)
	}
	if s.Structure.Structure != StructureUnknown || s.Structure.Breadth != BreadthUnknown ||
		s.Structure.Posture != PostureUnknown {
		t.Error("structural layers must start UNKNOWN")
	}
	if s.RuleID != RuleUnknown {
		t.Errorf("RuleID = %q, want %q", s.RuleID, RuleUnknown)
	}
	if s.SchemaVer != SnapshotSchemaVersion {
		t.Errorf("SchemaVer = %d, want %d", s.SchemaVer, SnapshotSchemaVersion)
	}
	// An untouched snapshot must be writable — a day where everything failed is still a
	// legitimate archive entry, recorded as UNKNOWN.
	if err := s.Validate(); err != nil {
		t.Errorf("a fresh UNKNOWN snapshot should be valid: %v", err)
	}
}

func TestValidateRejectsCorruptSnapshots(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Snapshot)
		want string
	}{
		{"no date", func(s *Snapshot) { s.Date = "" }, "no date"},
		{"bad date", func(s *Snapshot) { s.Date = "07/08/2026" }, "YYYY-MM-DD"},
		{"no schema", func(s *Snapshot) { s.SchemaVer = 0 }, "schema version"},
		{"undefined regime", func(s *Snapshot) { s.Regime = Regime("Bull Market") }, "undefined regime"},
		{"score high", func(s *Snapshot) { s.Score = 101 }, "score"},
		{"score low", func(s *Snapshot) { s.Score = -1 }, "score"},
		{"confidence high", func(s *Snapshot) { s.Confidence = 1.5 }, "confidence"},
		{"undefined structure", func(s *Snapshot) { s.Structure.Structure = PrimaryStructure("UP") }, "structural state"},
		{"regime without rule", func(s *Snapshot) { s.RuleID = "" }, "no rule id"},
		{"evidence without source", func(s *Snapshot) { s.Evidence[0].Source = "" }, "no source"},
		{"evidence bad direction", func(s *Snapshot) { s.Evidence[0].Direction = Direction("UP") }, "direction/strength"},
		{"evidence confidence range", func(s *Snapshot) { s.Evidence[0].Confidence = 2 }, "confidence"},
	}
	for _, c := range cases {
		s := validSnapshot()
		c.mut(s)
		err := s.Validate()
		if err == nil {
			t.Errorf("%s: expected rejection", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
	if err := (*Snapshot)(nil).Validate(); err == nil {
		t.Error("nil snapshot must be rejected")
	}
}

// The invariant that keeps the archive honest: an unavailable evidence recorded with a real
// direction would let a future validation run count an outage as a neutral market reading.
func TestValidateRejectsMissingEvidenceThatLooksMeasured(t *testing.T) {
	s := validSnapshot()
	s.Evidence[1].Direction = DirNeutral // unavailable, but claims a reading
	err := s.Validate()
	if err == nil {
		t.Fatal("unavailable evidence with a NEUTRAL direction must be rejected")
	}
	if !strings.Contains(err.Error(), "unavailable but not UNKNOWN") {
		t.Errorf("unexpected error: %v", err)
	}

	s = validSnapshot()
	s.Evidence[1].Strength = StrengthWeak
	if err := s.Validate(); err == nil {
		t.Fatal("unavailable evidence with a strength must be rejected")
	}
}

// A day where every source failed is a valid record — UNKNOWN is a state, not an error.
func TestUnknownDayIsArchivable(t *testing.T) {
	s := NewSnapshot("2026-08-07", time.Now())
	s.Evidence = []Evidence{
		MissingEvidence(EvidencePrice, "insufficient history"),
		MissingEvidence(EvidenceCash, "TWSE 500"),
	}
	s.Caveats = []string{"所有來源皆不可用"}
	if err := s.Validate(); err != nil {
		t.Fatalf("an all-missing day must still be archivable: %v", err)
	}
	if len(s.UsableEvidence()) != 0 {
		t.Error("nothing should be scoreable on an all-missing day")
	}
}

func TestApplyDecisionCopiesVerdictAndProvenance(t *testing.T) {
	s := NewSnapshot("2026-08-07", time.Now())
	s.ApplyDecision(RegimeDecision{
		Regime: RegimeDistribution,
		RuleID: RuleDistBreadth,
		View: StructureView{
			Benchmark: Benchmark0050, Structure: StructureUptrend,
			Breadth: BreadthNarrowing, Posture: PostureDeteriorating,
		},
		Reasons: []string{"廣度背離"},
		Caveats: []string{"門檻未校準"},
	})
	if s.Regime != RegimeDistribution || s.RuleID != RuleDistBreadth {
		t.Errorf("verdict not applied: %s / %s", s.Regime, s.RuleID)
	}
	if s.Structure.Breadth != BreadthNarrowing {
		t.Error("structural view not applied")
	}
	if len(s.Reasons) != 1 || len(s.Caveats) != 1 {
		t.Error("reasons/caveats not carried over")
	}
	if err := s.Validate(); err != nil {
		t.Errorf("applied decision should stay valid: %v", err)
	}
}

// The archive's whole purpose is offline re-derivation, so the raw observation and every
// analyzer metric must survive a persist/reload cycle intact.
func TestSnapshotRoundTripPreservesRawAndMetrics(t *testing.T) {
	s := validSnapshot()
	s.Raw = RawSnapshot{
		Date:    "2026-08-07",
		Cash:    &CashData{ForeignBuy: 310339947885, ForeignSell: 351055691675, ForeignNet: -40715743790},
		Futures: &FuturesOIData{Contract: "臺股期貨", Item: "外資及陸資", LongOI: 8321, ShortOI: 96232, NetOI: -87911},
		Sources: []SourceMeta{{Name: "BFI82U", Status: SourceOK, AsOfDate: "2026-08-07"}},
	}
	s.Structure.Metrics = map[string]float64{MetricBreadthMA20: 41.5, MetricDDFromHigh60: 1.2}
	s.Evidence[0].Metrics = map[string]float64{MetricRet20: -2.4}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var out Snapshot
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("round-tripped snapshot invalid: %v", err)
	}
	if out.Raw.Futures == nil || out.Raw.Futures.NetOI != -87911 {
		t.Error("raw futures observation lost")
	}
	if out.Raw.Cash == nil || out.Raw.Cash.ForeignNet != -40715743790 {
		t.Error("raw cash observation lost")
	}
	if v, ok := out.Structure.Metric(MetricBreadthMA20); !ok || v != 41.5 {
		t.Errorf("structure metric lost: %v %v", v, ok)
	}
	if v, ok := out.Evidence[0].Metric(MetricRet20); !ok || v != -2.4 {
		t.Errorf("evidence metric lost: %v %v", v, ok)
	}
	if out.Raw.Breadth != nil || out.Raw.Margin != nil {
		t.Error("missing datasets must stay nil after reload")
	}
	// The unavailable evidence must come back still unavailable.
	if out.Evidence[1].Usable() {
		t.Error("missing evidence became usable after a round trip")
	}
	if out.SchemaVer != SnapshotSchemaVersion {
		t.Errorf("schema version lost: %d", out.SchemaVer)
	}
}

func TestUsableEvidenceOnSnapshot(t *testing.T) {
	s := validSnapshot()
	if got := len(s.UsableEvidence()); got != 1 {
		t.Errorf("usable evidence = %d, want 1 (the missing one must be excluded)", got)
	}
}
