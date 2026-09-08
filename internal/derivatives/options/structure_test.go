package options_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/options"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// TestPCRIsAStructureAndNeverADirection.
//
// §10.8: open interest says how many calls and puts are OPEN. It does not say who is long or
// short — the same OI is consistent with buyers or with writers dominating, and the feed
// carries no long/short identity for it. So M5 describes structure and produces no validated
// direction.
func TestPCRIsAStructureAndNeverADirection(t *testing.T) {
	for _, c := range []struct {
		name      string
		callOI    float64
		putOI     float64
		wantLabel options.StructureLabel
	}{
		{"puts dominate", 1000, 2000, options.StructurePutOIDominant},
		{"calls dominate", 2000, 1000, options.StructureCallOIDominant},
		{"balanced at parity", 1000, 1000, options.StructureBalanced},
		{"balanced at the top of the band", 1000, 1100, options.StructureBalanced},
		{"balanced at the bottom of the band", 1100, 1000, options.StructureBalanced},
		{"just outside the band", 1000, 1101, options.StructurePutOIDominant},
	} {
		t.Run(c.name, func(t *testing.T) {
			rows := pair(provider.SessionDay, "202609", 46000,
				fptr(1), fptr(1), fptr(c.callOI), fptr(c.putOI))
			obs := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay), rows)
			if got := obs.OI.Structure.Label; got != c.wantLabel {
				v, _ := obs.OI.Value()
				t.Errorf("ratio %v labelled %s, want %s", v, got, c.wantLabel)
			}
		})
	}
}

// TestNoDirectionWordReachesAReader walks the whole serialised view for the two words §10.8
// forbids, in every field, not only the label.
func TestNoDirectionWordReachesAReader(t *testing.T) {
	rows := fixtureRows(t)
	obs := mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows)
	view, err := options.BuildMetricView(options.MetricOIPCR, obs, nil, policy())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(b))
	for _, forbidden := range []string{"bullish", "bearish", "看多", "看空"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("the serialised view contains %q; M5 produces no validated direction", forbidden)
		}
	}
	for _, label := range options.StructureLabels {
		if strings.Contains(string(label), "BULL") || strings.Contains(string(label), "BEAR") {
			t.Errorf("the structure vocabulary contains %q", label)
		}
	}
}

// TestEveryTraditionalReadingCarriesItsOpposite.
func TestEveryTraditionalReadingCarriesItsOpposite(t *testing.T) {
	for _, c := range []struct {
		callOI, putOI float64
	}{{1000, 2000}, {2000, 1000}, {1000, 1000}} {
		rows := pair(provider.SessionDay, "202609", 46000,
			fptr(1), fptr(1), fptr(c.callOI), fptr(c.putOI))
		obs := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay), rows)
		s := obs.OI.Structure
		if len(s.Interpretations) == 0 {
			t.Fatalf("%s carries no interpretation at all", s.Label)
		}
		for _, i := range s.Interpretations {
			if i.Contrary == "" {
				t.Errorf("%s: a reading with no contrary reading beside it", s.Label)
			}
			if i.Type != options.InterpretationHeuristic {
				t.Errorf("%s: interpretation type %q, want HEURISTIC", s.Label, i.Type)
			}
			if i.Status != options.ValidationShadow {
				t.Errorf("%s: validation status %q, want SHADOW", s.Label, i.Status)
			}
		}
	}
}

// TestAnAbsentRatioHasNoStructureAndIsNotBalanced.
func TestAnAbsentRatioHasNoStructureAndIsNotBalanced(t *testing.T) {
	night := mustObserve(t, req(options.FamilyAll, provider.SessionAfterHours), fixtureRows(t))
	s := night.OI.Structure
	if s.Label != options.StructureUndetermined {
		t.Errorf("an absent open-interest ratio is labelled %s", s.Label)
	}
	if s.Label == options.StructureBalanced {
		t.Error("BALANCED is a reading; the absence of one must not render as it")
	}
	if len(s.Interpretations) != 0 {
		t.Error("an absent ratio carries interpretations of nothing")
	}
}

// TestVolumeIsNotLabelledWithAnOpenInterestStructure: the vocabulary describes HELD exposure.
func TestVolumeIsNotLabelledWithAnOpenInterestStructure(t *testing.T) {
	obs := mustObserve(t, req(options.FamilyAll, provider.SessionCombined), fixtureRows(t))
	if got := obs.Volume.Structure.Label; got != options.StructureNotApplicable {
		t.Errorf("the volume PCR is labelled %s; PUT_OI_DOMINANT is a claim about open "+
			"interest and a volume figure cannot support it", got)
	}
	if len(obs.Volume.Structure.Interpretations) != 0 {
		t.Error("the volume PCR carries open-interest interpretations")
	}
}

// TestTheStatusVocabularyIsASupersetOfSixNotARival.
func TestTheStatusVocabularyIsASupersetOfSixNotARival(t *testing.T) {
	for _, s := range institutional.SourceStatuses {
		got, err := options.FromSourceStatus(s)
		if err != nil {
			t.Errorf("%s does not map into the ratio vocabulary: %v", s, err)
			continue
		}
		if !got.IsSourceStatus() || got.IsDerived() {
			t.Errorf("%s maps to %s, which does not report as a source status", s, got)
		}
	}
	for _, s := range options.DerivedRatioStatuses {
		if s.IsSourceStatus() {
			t.Errorf("%s reports as a source status and could be written into a data-health "+
				"record, which describes a fetch that succeeded as one that failed", s)
		}
		if !s.Valid() {
			t.Errorf("%s is not in the closed set", s)
		}
	}
	if _, err := options.FromSourceStatus(institutional.StatusInsufficientHistory); err == nil {
		t.Error("a DERIVED status was accepted as a fetch outcome")
	}
	if options.RatioPartial.Numeric() {
		t.Error("PARTIAL renders as a number; R14 spent a milestone on INSUFFICIENT_DATA " +
			"rendered as 0")
	}
	if !options.RatioAvailable.Numeric() {
		t.Error("AVAILABLE does not render as a number")
	}
}

// TestTheIdentityIsTheContractsAndNotTheLabel.
func TestTheIdentityIsTheContractsAndNotTheLabel(t *testing.T) {
	a := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay),
		pair(provider.SessionDay, "202609", 46000, fptr(1), fptr(1), fptr(10), fptr(10)))
	b := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay),
		pair(provider.SessionDay, "202610", 46000, fptr(1), fptr(1), fptr(10), fptr(10)))

	if a.OI.Identity.Same(b.OI.Identity) {
		t.Fatal("two aggregates over different expiries report the same identity — the key " +
			"is the family label rather than the contracts")
	}
	added, removed := b.OI.Identity.Diff(a.OI.Identity)
	if len(added) != 1 || added[0] != "202610" || len(removed) != 1 || removed[0] != "202609" {
		t.Errorf("diff added %v removed %v", added, removed)
	}
	if a.OI.Identity.Rule != options.RolloverRule {
		t.Errorf("identity rule %q", a.OI.Identity.Rule)
	}
}

// TestDaysToExpiryIsNeverGuessed: it comes from a supplied settlement date or it is absent.
func TestDaysToExpiryIsNeverGuessed(t *testing.T) {
	r := req(options.FamilyMonthly, provider.SessionDay)
	r.SettlementDates = map[string]string{"202609": "2026-09-16"}
	rows := append(
		pair(provider.SessionDay, "202609", 46000, fptr(1), fptr(1), fptr(10), fptr(10)),
		pair(provider.SessionDay, "202610", 46000, fptr(1), fptr(1), fptr(10), fptr(10))...)
	obs := mustObserve(t, r, rows)

	found := map[string]*int{}
	for _, d := range obs.OI.Identity.Distances {
		found[d.ExpiryCode] = d.Days
	}
	if found["202609"] == nil || *found["202609"] != 12 {
		t.Errorf("202609 distance = %v, want 12 days", found["202609"])
	}
	if found["202610"] != nil {
		t.Errorf("202610 reports %v days with no settlement date supplied; TAIFEX defers a "+
			"settlement when the third Wednesday is a holiday, so a date-shaped guess is wrong "+
			"on exactly the days it matters", *found["202610"])
	}
}
