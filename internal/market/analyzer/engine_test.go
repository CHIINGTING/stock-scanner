package analyzer

import (
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

var wts = map[string]float64{
	model.EvidencePrice: 20, model.EvidenceBreadth: 15,
	model.EvidenceCash: 25, model.EvidenceFutures: 20, model.EvidenceMargin: 20,
}

func e(src string, d model.Direction, s model.Strength, conf float64) model.Evidence {
	return model.Evidence{Source: src, Direction: d, Strength: s, Confidence: conf, Available: true}
}

func TestScoreDirectionAndRange(t *testing.T) {
	allBull := []model.Evidence{
		e(model.EvidencePrice, model.DirBullish, model.StrengthExtreme, 1),
		e(model.EvidenceBreadth, model.DirBullish, model.StrengthExtreme, 1),
		e(model.EvidenceCash, model.DirBullish, model.StrengthExtreme, 1),
	}
	allBear := []model.Evidence{
		e(model.EvidencePrice, model.DirBearish, model.StrengthExtreme, 1),
		e(model.EvidenceBreadth, model.DirBearish, model.StrengthExtreme, 1),
		e(model.EvidenceCash, model.DirBearish, model.StrengthExtreme, 1),
	}
	neutral := []model.Evidence{e(model.EvidencePrice, model.DirNeutral, model.StrengthWeak, 1)}

	if s := Score(allBull, wts).Score; s <= 80 {
		t.Errorf("unanimous extreme bullish should be high, got %v", s)
	}
	if s := Score(allBear, wts).Score; s >= 20 {
		t.Errorf("unanimous extreme bearish should be low, got %v", s)
	}
	if s := Score(neutral, wts).Score; s != 50 {
		t.Errorf("neutral evidence should score 50, got %v", s)
	}
	for _, set := range [][]model.Evidence{allBull, allBear, neutral} {
		if s := Score(set, wts).Score; s < 0 || s > 100 {
			t.Errorf("score out of range: %v", s)
		}
	}
}

// The central contract, restated at the engine level: a missing source is EXCLUDED, not
// scored as 50. Otherwise an outage would drag every reading toward the middle and look
// like a calming market.
func TestScoreExcludesMissingRatherThanNeutralising(t *testing.T) {
	bullOnly := []model.Evidence{e(model.EvidenceCash, model.DirBullish, model.StrengthStrong, 1)}
	withMissing := append([]model.Evidence{}, bullOnly...)
	withMissing = append(withMissing,
		model.MissingEvidence(model.EvidenceMargin, "TWSE 500"),
		model.MissingEvidence(model.EvidenceFutures, "timeout"))

	a, b := Score(bullOnly, wts), Score(withMissing, wts)
	if a.Score != b.Score {
		t.Errorf("missing evidence changed the score: %v → %v", a.Score, b.Score)
	}
	if b.UsedWeight != 25 {
		t.Errorf("UsedWeight = %v, want only the usable 25", b.UsedWeight)
	}
	if b.TotalWeight <= b.UsedWeight {
		t.Error("TotalWeight must record the intended base so the gap is visible")
	}
	// Nothing usable at all → 50, but with UsedWeight 0 so it cannot be read as measured.
	empty := Score([]model.Evidence{model.MissingEvidence(model.EvidencePrice, "x")}, wts)
	if empty.Score != 50 || empty.UsedWeight != 0 {
		t.Errorf("empty case: score=%v used=%v", empty.Score, empty.UsedWeight)
	}
}

// An analyzer's own confidence scales its pull, so "I can barely see this" moves the number
// less than a full-history reading.
func TestScoreRespectsEvidenceConfidence(t *testing.T) {
	strong := []model.Evidence{
		e(model.EvidenceCash, model.DirBullish, model.StrengthStrong, 1.0),
		e(model.EvidenceMargin, model.DirBearish, model.StrengthStrong, 1.0),
	}
	weakBear := []model.Evidence{
		e(model.EvidenceCash, model.DirBullish, model.StrengthStrong, 1.0),
		e(model.EvidenceMargin, model.DirBearish, model.StrengthStrong, 0.2),
	}
	if Score(weakBear, wts).Score <= Score(strong, wts).Score {
		t.Error("a low-confidence bearish input should pull the score down less")
	}
}

func TestScoreContributionsExplainTheNumber(t *testing.T) {
	r := Score([]model.Evidence{
		e(model.EvidenceCash, model.DirBearish, model.StrengthExtreme, 1),
		e(model.EvidencePrice, model.DirBullish, model.StrengthWeak, 1),
	}, wts)
	if len(r.Contributions) != 2 {
		t.Fatalf("want 2 contributions, got %d", len(r.Contributions))
	}
	// Ordered by absolute pull so the dominant driver is first.
	if math.Abs(r.Contributions[0].Pull) < math.Abs(r.Contributions[1].Pull) {
		t.Error("contributions must be ordered by influence")
	}
	if r.Contributions[0].Source != model.EvidenceCash {
		t.Errorf("the extreme cash reading should dominate, got %s", r.Contributions[0].Source)
	}
}

// ── confidence ───────────────────────────────────────────────────────────────────────

func TestConfidenceFactorsAreSeparable(t *testing.T) {
	agree := []model.Evidence{
		e(model.EvidencePrice, model.DirBullish, model.StrengthStrong, 1),
		e(model.EvidenceBreadth, model.DirBullish, model.StrengthStrong, 1),
		e(model.EvidenceCash, model.DirBullish, model.StrengthStrong, 1),
		e(model.EvidenceFutures, model.DirBullish, model.StrengthStrong, 1),
		e(model.EvidenceMargin, model.DirBullish, model.StrengthStrong, 1),
	}
	split := []model.Evidence{
		e(model.EvidencePrice, model.DirBullish, model.StrengthStrong, 1),
		e(model.EvidenceBreadth, model.DirBearish, model.StrengthStrong, 1),
		e(model.EvidenceCash, model.DirNeutral, model.StrengthWeak, 1),
		e(model.EvidenceFutures, model.DirBearish, model.StrengthStrong, 1),
		e(model.EvidenceMargin, model.DirBullish, model.StrengthStrong, 1),
	}
	a, b := Confidence(agree, wts), Confidence(split, wts)

	if a.Completeness != 1 {
		t.Errorf("all five present should be complete, got %v", a.Completeness)
	}
	if a.Agreement <= b.Agreement {
		t.Errorf("unanimity should out-agree a split panel: %v vs %v", a.Agreement, b.Agreement)
	}
	if a.Confidence <= b.Confidence {
		t.Errorf("unanimous evidence should be more trustworthy: %v vs %v", a.Confidence, b.Confidence)
	}
	if len(b.Caveats) == 0 {
		t.Error("a split panel should say why confidence fell")
	}
	// The product must actually be the product of its reported factors — an unexplainable
	// number is exactly what this design forbids.
	want := a.Completeness * a.Agreement * a.Conviction
	if math.Abs(a.Confidence-math.Floor(want*100)/100) > 0.02 {
		t.Errorf("confidence %v does not match its factors %v×%v×%v",
			a.Confidence, a.Completeness, a.Agreement, a.Conviction)
	}
}

func TestConfidenceCapsOnThinEvidence(t *testing.T) {
	// Only price arrived: 20 of 100 weight, but it is unanimous with itself.
	thin := []model.Evidence{
		e(model.EvidencePrice, model.DirBullish, model.StrengthExtreme, 1),
		model.MissingEvidence(model.EvidenceBreadth, "x"),
		model.MissingEvidence(model.EvidenceCash, "x"),
		model.MissingEvidence(model.EvidenceFutures, "x"),
		model.MissingEvidence(model.EvidenceMargin, "x"),
	}
	c := Confidence(thin, wts)
	if c.Agreement < 0.99 {
		t.Errorf("a single source trivially agrees with itself: %v", c.Agreement)
	}
	if c.Confidence > 0.5 {
		t.Errorf("a day built on 20%% of the evidence must be capped, got %v", c.Confidence)
	}
	if len(c.Caveats) == 0 {
		t.Error("the cap must be explained")
	}
	// Nothing at all → zero, not a middling number.
	none := Confidence([]model.Evidence{model.MissingEvidence(model.EvidencePrice, "x")}, wts)
	if none.Confidence != 0 {
		t.Errorf("no usable evidence should be confidence 0, got %v", none.Confidence)
	}
}

func TestConfidenceAvoidsFakePrecision(t *testing.T) {
	c := Confidence([]model.Evidence{
		e(model.EvidencePrice, model.DirBullish, model.StrengthStrong, 0.83),
		e(model.EvidenceBreadth, model.DirBullish, model.StrengthModerate, 0.77),
		e(model.EvidenceCash, model.DirBullish, model.StrengthStrong, 0.91),
		e(model.EvidenceFutures, model.DirBullish, model.StrengthWeak, 0.66),
		e(model.EvidenceMargin, model.DirNeutral, model.StrengthWeak, 0.55),
	}, wts)
	if c.Confidence*100 != math.Floor(c.Confidence*100) {
		t.Errorf("confidence should carry at most two decimals, got %v", c.Confidence)
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		t.Errorf("out of range: %v", c.Confidence)
	}
}

// Determinism: the same evidence must always give the same numbers, whatever order it
// arrives in — no map iteration leaking into the result.
func TestEngineIsDeterministic(t *testing.T) {
	set := []model.Evidence{
		e(model.EvidencePrice, model.DirBullish, model.StrengthStrong, 0.8),
		e(model.EvidenceBreadth, model.DirBearish, model.StrengthModerate, 0.9),
		e(model.EvidenceCash, model.DirBullish, model.StrengthWeak, 0.7),
	}
	s0, c0 := Score(set, wts), Confidence(set, wts)
	for i := 0; i < 50; i++ {
		if Score(set, wts).Score != s0.Score {
			t.Fatal("score is not deterministic")
		}
		if Confidence(set, wts).Confidence != c0.Confidence {
			t.Fatal("confidence is not deterministic")
		}
	}
	// Reordering the inputs must not change the aggregate.
	rev := []model.Evidence{set[2], set[1], set[0]}
	if Score(rev, wts).Score != s0.Score {
		t.Error("score depends on evidence order")
	}
}

func TestMissingAndConflictingAreReported(t *testing.T) {
	set := []model.Evidence{
		e(model.EvidencePrice, model.DirBullish, model.StrengthStrong, 1),
		e(model.EvidenceBreadth, model.DirBullish, model.StrengthStrong, 1),
		e(model.EvidenceCash, model.DirBearish, model.StrengthStrong, 1),
		model.MissingEvidence(model.EvidenceMargin, "TWSE MI_MARGN timeout"),
	}
	missing := MissingSources(set)
	if len(missing) != 1 || missing[0] == "" {
		t.Fatalf("missing sources not reported: %v", missing)
	}
	if want := "TWSE MI_MARGN timeout"; !contains(missing[0], want) {
		t.Errorf("the analyzer's own reason must survive: %q", missing[0])
	}
	conflict := ConflictingSources(set, wts)
	if len(conflict) != 1 {
		t.Fatalf("want the one dissenting source, got %v", conflict)
	}
	if !contains(conflict[0], model.EvidenceCash) {
		t.Errorf("wrong dissenter: %v", conflict)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// "The evidence contradicts itself" and "there is no evidence" are different states and must
// not report the same confidence. Two directly opposed sources drive agreement to ~0 and the
// product truncates to 0.00, which is exactly what the empty case returns.
func TestConflictingEvidenceIsDistinguishableFromNoEvidence(t *testing.T) {
	opposed := Confidence([]model.Evidence{
		e(model.EvidencePrice, model.DirBullish, model.StrengthStrong, 1),
		e(model.EvidenceBreadth, model.DirBearish, model.StrengthExtreme, 1),
	}, wts)
	none := Confidence([]model.Evidence{
		model.MissingEvidence(model.EvidencePrice, "x"),
		model.MissingEvidence(model.EvidenceBreadth, "x"),
	}, wts)

	if none.Confidence != 0 {
		t.Errorf("no usable evidence must be exactly 0, got %v", none.Confidence)
	}
	if opposed.Confidence <= 0 {
		t.Errorf("contradictory-but-present evidence must not report 0, got %v", opposed.Confidence)
	}
	if opposed.Confidence == none.Confidence {
		t.Error("the two states are indistinguishable")
	}
	// It should still be a very low number — this is a floor, not a rescue.
	if opposed.Confidence > 0.1 {
		t.Errorf("contradictory evidence should still be near the floor, got %v", opposed.Confidence)
	}
}
