package entryplanbacktest

import (
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// TestAnUnavailableRegimeIsNeverSidewaysOrUnknown is the machine pin on EP-9 §5's rule. The
// two substitutions it forbids are the two that would make a regime-segmented table lie:
// SIDEWAYS is a verdict ("we looked, no edge") and UNKNOWN is a measurement outcome ("the
// regime layer ran and the data could not support a call"). "No archive covers this session"
// is neither.
func TestAnUnavailableRegimeIsNeverSidewaysOrUnknown(t *testing.T) {
	for _, r := range []model.Regime{model.RegimeSideways, model.RegimeUnknown, model.RegimeBull} {
		got, ok := RegimeUnavailable.Regime(r)
		if ok {
			t.Fatalf("UNAVAILABLE provenance produced regime %q — an absent archive must not "+
				"be spelled with a regime", got)
		}
		if got != "" {
			t.Fatalf("UNAVAILABLE provenance produced %q, want \"\"", got)
		}
	}
	// And the two provenances that DO carry a regime carry it verbatim, including UNKNOWN,
	// which is a real row: internal/entryplan/series.go:141-143.
	for _, p := range []RegimeProvenance{RegimeArchived, RegimeReplayedPIT} {
		for _, r := range []model.Regime{model.RegimeBull, model.RegimeSideways, model.RegimeUnknown} {
			got, ok := p.Regime(r)
			if !ok || got != r {
				t.Fatalf("%s.Regime(%q) = (%q,%v), want (%q,true)", p, r, got, ok, r)
			}
		}
		if _, ok := p.Regime("NOT_A_REGIME"); ok {
			t.Fatalf("%s accepted a string that is not a regime", p)
		}
	}
}

func TestRegimeProvenanceIsThreeNamedValues(t *testing.T) {
	for _, p := range []RegimeProvenance{RegimeArchived, RegimeReplayedPIT, RegimeUnavailable} {
		if !p.Valid() || p == "" {
			t.Fatalf("%q is not a defined regime provenance", p)
		}
	}
	if RegimeProvenance("").Valid() || RegimeProvenance("LIVE").Valid() {
		t.Fatal("an undefined regime provenance passed Valid")
	}
	// Wire values, pinned.
	if RegimeArchived != "ARCHIVED" || RegimeReplayedPIT != "REPLAYED_PIT" || RegimeUnavailable != "UNAVAILABLE" {
		t.Fatalf("regime provenance serializes as %q/%q/%q", RegimeArchived, RegimeReplayedPIT, RegimeUnavailable)
	}
	// ARCHIVED and REPLAYED_PIT are not the same archive, and entryplan already refuses to
	// blend them. Keeping the two provenances distinct is that refusal restated here.
	if !entryplan.RegimeSourceLive.Valid() || !entryplan.RegimeSourceReplay.Valid() {
		t.Fatal("entryplan's own sources are not valid — the mapping below is meaningless")
	}
	if entryplan.RegimeSourceLive == entryplan.RegimeSourceReplay {
		t.Fatal("entryplan's LIVE and REPLAY sources collapsed into one")
	}
}

func TestValuationProvenanceKeepsUnsuitableApartFromUnavailable(t *testing.T) {
	if ValuationAvailableUnsuitable == ValuationUnavailableHistorically {
		t.Fatal("'screened out' and 'could not be looked up' are the same value")
	}
	if ValuationAvailableUnsuitable.PermitsCeiling() {
		t.Fatal("an UNSUITABLE valuation permits the Target2 ceiling")
	}
	if ValuationUnavailableHistorically.PermitsCeiling() {
		t.Fatal("a valuation that could not be reconstructed permits the Target2 ceiling — the " +
			"study would be restoring a cap the plan could not have had")
	}
	if !ValuationReproducibleSuitable.PermitsCeiling() {
		t.Fatal("a reproducible suitable valuation does not permit the ceiling")
	}
	for _, v := range []ValuationProvenance{ValuationReproducibleSuitable,
		ValuationAvailableUnsuitable, ValuationUnavailableHistorically} {
		if !v.Valid() || v == "" {
			t.Fatalf("%q is not a defined valuation provenance", v)
		}
	}
	if ValuationProvenance("").Valid() || ValuationProvenance("AVAILABLE").Valid() {
		t.Fatal("an undefined valuation provenance passed Valid")
	}
}

func TestValidateProvenanceNamesTheOffendingSide(t *testing.T) {
	if err := ValidateProvenance(RegimeArchived, ValuationReproducibleSuitable); err != nil {
		t.Fatalf("a well-formed pair was refused: %v", err)
	}
	if err := ValidateProvenance("LIVE", ValuationReproducibleSuitable); err == nil ||
		!strings.Contains(err.Error(), "regime provenance") {
		t.Fatalf("regime side: %v", err)
	}
	if err := ValidateProvenance(RegimeArchived, "AVAILABLE"); err == nil ||
		!strings.Contains(err.Error(), "valuation provenance") {
		t.Fatalf("valuation side: %v", err)
	}
}

// ── Run metadata ──────────────────────────────────────────────────────────────────────────

func goodMetadata() RunMetadata {
	return RunMetadata{
		MethodologyVersion: MethodologyVersion,
		BaselineCommit:     BaselineCommit,
		RuleVersion:        entryplan.RuleVersion,
		DatasetFrom:        "2025-03-07",
		DatasetTo:          "2026-08-31",
		SymbolsLoaded:      1995,
		SymbolsObserved:    120,
		Arm:                ArmZoneLimit,
		WaitSessions:       5,
		Observations:       10,
		Exclusions:         4,
		ExclusionsByReason: map[string]int{"SESSION_UNAVAILABLE": 1, "ADJUSTMENT_IN_WINDOW": 3},
		RegimeProvenanceCounts: map[RegimeProvenance]int{
			RegimeReplayedPIT: 7, RegimeUnavailable: 3,
		},
		ValuationProvenanceCounts: map[ValuationProvenance]int{
			ValuationUnavailableHistorically: 10,
		},
		GeneratedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestRunMetadataAcceptsAWellFormedHeader(t *testing.T) {
	if err := goodMetadata().Validate(); err != nil {
		t.Fatalf("a well-formed header was refused: %v", err)
	}
}

func TestRunMetadataRefusesAHeaderThatCannotBeAudited(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*RunMetadata)
		want string
	}{
		{"no methodology", func(m *RunMetadata) { m.MethodologyVersion = "" }, "MethodologyVersion"},
		{"no baseline", func(m *RunMetadata) { m.BaselineCommit = "" }, "BaselineCommit"},
		{"no rule version", func(m *RunMetadata) { m.RuleVersion = "" }, "RuleVersion"},
		{"bad from", func(m *RunMetadata) { m.DatasetFrom = "2025/03/07" }, "dataset_from"},
		{"bad to", func(m *RunMetadata) { m.DatasetTo = "" }, "dataset_to"},
		{"backwards range", func(m *RunMetadata) { m.DatasetFrom, m.DatasetTo = m.DatasetTo, m.DatasetFrom }, "backwards"},
		{"bad arm", func(m *RunMetadata) { m.Arm = "SIGNAL_CLOSE" }, "execution arm"},
		{"bad wait", func(m *RunMetadata) { m.WaitSessions = 0 }, "wait window"},
		{"more observed than loaded", func(m *RunMetadata) { m.SymbolsObserved = m.SymbolsLoaded + 1 }, "out of"},
		{"negative count", func(m *RunMetadata) { m.Observations = -1 }, "negative count"},
		{"unattributed exclusions", func(m *RunMetadata) { m.Exclusions = 9 }, "unattributed exclusion"},
		{"regime census short", func(m *RunMetadata) { m.RegimeProvenanceCounts[RegimeUnavailable] = 2 }, "regime provenance census covers"},
		{"regime census undefined key", func(m *RunMetadata) { m.RegimeProvenanceCounts = map[RegimeProvenance]int{"LIVE": 10} }, "regime provenance census names"},
		{"valuation census short", func(m *RunMetadata) { m.ValuationProvenanceCounts[ValuationUnavailableHistorically] = 9 }, "valuation provenance census covers"},
		{"valuation census undefined key", func(m *RunMetadata) {
			m.ValuationProvenanceCounts = map[ValuationProvenance]int{"AVAILABLE": 10}
		}, "valuation provenance census names"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := goodMetadata()
			c.mut(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("accepted %+v", m)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestResultsMayNotBeWrittenIntoTheProductionStores pins EP-9 §26/§27: the SQLite research
// store (which owns the production `outcomes` table) and the production archives are refused
// as output destinations.
func TestResultsMayNotBeWrittenIntoTheProductionStores(t *testing.T) {
	forbidden := []string{
		"data/research",
		"data/research/ep9",
		"./data/research/r13.db",
		"data/market",
		"data/market_replay",
		"data/analysis_history",
		"data/valuation/2026-09-06",
		"data/fundamental",
		"",
	}
	for _, d := range forbidden {
		if err := ValidateOutputDir(d); err == nil {
			t.Fatalf("%q was accepted as an output directory", d)
		}
	}
	for _, d := range []string{DefaultOutputDir, "reports/entryplanbacktest/2026-09-15", "/tmp/ep9"} {
		if err := ValidateOutputDir(d); err != nil {
			t.Fatalf("%q was refused: %v", d, err)
		}
	}
	if DefaultOutputDir != "reports/entryplanbacktest" {
		t.Fatalf("DefaultOutputDir is %q; the .gitignore rule that keeps runtime artifacts out "+
			"of version control is `reports/*/`, which requires a subdirectory", DefaultOutputDir)
	}
}

func TestTheBaselineCommitIsAFullSHA(t *testing.T) {
	if len(BaselineCommit) != 40 {
		t.Fatalf("BaselineCommit %q is %d characters, want a 40-character SHA — an abbreviated "+
			"hash can become ambiguous as the repo grows", BaselineCommit, len(BaselineCommit))
	}
	for _, r := range BaselineCommit {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("BaselineCommit %q contains %q", BaselineCommit, r)
		}
	}
	if MethodologyVersion == "" {
		t.Fatal("MethodologyVersion is empty")
	}
	if MethodologyVersion == entryplan.RuleVersion {
		t.Fatal("the methodology version and the entryplan rule version are the same string — " +
			"a row carrying one stamp could not say whether the strategy or the measurement moved")
	}
}
