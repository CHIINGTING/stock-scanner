package model

import (
	"encoding/json"
	"testing"
)

// The central invariant of the whole design: missing data must never be scoreable, and must
// never look like a neutral reading.
func TestMissingEvidenceIsNotNeutral(t *testing.T) {
	e := MissingEvidence(EvidenceMargin, "TWSE MI_MARGN timeout")

	if e.Available {
		t.Error("missing evidence must not be Available")
	}
	if e.Direction != DirUnknown {
		t.Errorf("Direction = %q, want UNKNOWN (never NEUTRAL)", e.Direction)
	}
	if e.Direction == DirNeutral {
		t.Fatal("missing data was recorded as a neutral reading")
	}
	if e.Strength != StrengthUnknown {
		t.Errorf("Strength = %q, want UNKNOWN", e.Strength)
	}
	if e.Usable() {
		t.Error("missing evidence must be excluded from scoring")
	}
	if e.Reason == "" {
		t.Error("missing evidence must record why")
	}
	if e.Confidence != 0 {
		t.Errorf("missing evidence confidence = %v, want 0", e.Confidence)
	}
}

func TestUsableRequiresBothAvailableAndKnownDirection(t *testing.T) {
	cases := []struct {
		name string
		e    Evidence
		want bool
	}{
		{"normal bullish", Evidence{Source: "x", Direction: DirBullish, Available: true}, true},
		{"neutral counts", Evidence{Source: "x", Direction: DirNeutral, Available: true}, true},
		{"unavailable", Evidence{Source: "x", Direction: DirBullish, Available: false}, false},
		// Available with UNKNOWN direction is a bug; it must not silently score as neutral.
		{"available but unknown", Evidence{Source: "x", Direction: DirUnknown, Available: true}, false},
		{"undefined direction", Evidence{Source: "x", Direction: Direction("SIDEWAYS"), Available: true}, false},
	}
	for _, c := range cases {
		if got := c.e.Usable(); got != c.want {
			t.Errorf("%s: Usable() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUsableEvidenceFilters(t *testing.T) {
	list := []Evidence{
		{Source: EvidencePrice, Direction: DirBullish, Available: true},
		MissingEvidence(EvidenceCash, "down"),
		{Source: EvidenceMargin, Direction: DirBearish, Available: true},
		{Source: EvidenceFutures, Direction: DirUnknown, Available: true},
	}
	got := UsableEvidence(list)
	if len(got) != 2 {
		t.Fatalf("got %d usable, want 2", len(got))
	}
	if got[0].Source != EvidencePrice || got[1].Source != EvidenceMargin {
		t.Errorf("wrong evidence survived: %v", got)
	}
}

func TestDirectionSignAndValidity(t *testing.T) {
	if DirBullish.Sign() != 1 || DirBearish.Sign() != -1 || DirNeutral.Sign() != 0 {
		t.Error("direction signs are wrong")
	}
	// UNKNOWN has no meaningful sign; callers must filter it out before summing.
	if DirUnknown.Sign() != 0 {
		t.Error("UNKNOWN must not contribute a sign")
	}
	for _, d := range []Direction{DirBullish, DirNeutral, DirBearish, DirUnknown} {
		if !d.Valid() {
			t.Errorf("%q should be valid", d)
		}
	}
	if Direction("BULL").Valid() {
		t.Error("undefined direction accepted")
	}
	for _, s := range []Strength{StrengthWeak, StrengthModerate, StrengthStrong, StrengthExtreme, StrengthUnknown} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if Strength("HUGE").Valid() {
		t.Error("undefined strength accepted")
	}
}

// A metric of 0 is a legitimate observation (e.g. zero net foreign buying), so callers must
// be able to distinguish it from "not recorded".
func TestMetricDistinguishesZeroFromAbsent(t *testing.T) {
	e := Evidence{Metrics: map[string]float64{"net_1d": 0}}
	if v, ok := e.Metric("net_1d"); !ok || v != 0 {
		t.Errorf("recorded zero metric lost: v=%v ok=%v", v, ok)
	}
	if _, ok := e.Metric("net_5d"); ok {
		t.Error("absent metric reported as present")
	}
	var empty Evidence
	if _, ok := empty.Metric("anything"); ok {
		t.Error("nil metrics map reported a value")
	}
}

func TestEvidenceJSONRoundTrip(t *testing.T) {
	in := Evidence{
		Source: EvidenceFutures, Direction: DirBearish, Strength: StrengthExtreme,
		Confidence: 0.72, Summary: "外資淨空 87,911 口",
		Metrics:   map[string]float64{"net_oi": -87911, "net_oi_change_1d": 1472},
		Available: true,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Evidence
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Source != in.Source || out.Direction != in.Direction || out.Strength != in.Strength {
		t.Errorf("round trip changed the verdict: %+v", out)
	}
	if v, _ := out.Metric("net_oi"); v != -87911 {
		t.Errorf("metric lost in round trip: %v", v)
	}
}
