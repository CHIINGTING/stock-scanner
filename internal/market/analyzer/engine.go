package analyzer

import (
	"fmt"
	"math"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// The Market Score and Confidence engines (M7).
//
// ARCHITECTURAL INVARIANT: neither of these feeds the regime. DecideRegime takes a
// StructureView; it has no score parameter and no confidence parameter. Score answers "how
// bullish is the weight of evidence", regime answers "what shape is the market in", and they
// are allowed to disagree — a score of 67 sitting on a BULL_PULLBACK is a legitimate reading,
// not a bug. Keeping them in separate functions with no shared argument is what makes the
// forbidden coupling impossible rather than merely discouraged.

// ScoreResult is the 0..100 aggregate plus everything needed to explain it.
type ScoreResult struct {
	Score float64
	// Contributions is per-evidence detail, ordered by descending absolute pull on the
	// score, so "why 67" is answerable from the snapshot alone.
	Contributions []ScoreContribution
	// UsedWeight / TotalWeight show how much of the intended evidence base actually arrived.
	UsedWeight, TotalWeight float64
}

type ScoreContribution struct {
	Source    string  `json:"source"`
	SubScore  float64 `json:"sub_score"` // 0..100
	Weight    float64 `json:"weight"`    // configured weight
	Effective float64 `json:"effective"` // weight × evidence confidence
	Pull      float64 `json:"pull"`      // signed distance from neutral, weighted
}

// subScore maps one evidence onto 0..100 where 50 is neutral.
//
// Deliberately a small fixed table rather than a formula: the strength buckets are coarse
// because the thresholds behind them are uncalibrated, and inventing a continuous curve on
// top of four buckets would manufacture precision the inputs do not have.
func subScore(e model.Evidence) float64 {
	base := map[model.Strength]float64{
		model.StrengthWeak: 10, model.StrengthModerate: 20,
		model.StrengthStrong: 32, model.StrengthExtreme: 42,
	}[e.Strength]
	switch e.Direction {
	case model.DirBullish:
		return 50 + base
	case model.DirBearish:
		return 50 - base
	}
	return 50
}

// Score aggregates usable evidence into 0..100.
//
// Two properties matter more than the arithmetic:
//
//   - MISSING evidence is EXCLUDED, never scored as 50. A market with no margin data is not
//     a market with neutral margin, and the remaining weights renormalize so a partial day
//     degrades toward "less certain" rather than toward the middle.
//   - Each evidence's own confidence scales its weight. An analyzer that says "I can see
//     this but only barely" should move the number less than one with a full history behind
//     it, without needing a separate override mechanism.
//
// With nothing usable the score is 50 — but that is reported alongside UsedWeight 0, and the
// confidence engine drives the day's confidence to its floor, so the neutral number can
// never be mistaken for a measured neutral market.
func Score(evidence []model.Evidence, weights map[string]float64) ScoreResult {
	out := ScoreResult{}
	for _, e := range evidence {
		w := weights[e.Source]
		out.TotalWeight += w
		if !e.Usable() || w <= 0 {
			continue
		}
		conf := e.Confidence
		if conf <= 0 {
			conf = 1 // an analyzer that did not state one is taken at face value
		}
		eff := w * conf
		sub := subScore(e)
		out.UsedWeight += eff
		out.Contributions = append(out.Contributions, ScoreContribution{
			Source: e.Source, SubScore: round2(sub), Weight: w,
			Effective: round2(eff), Pull: round2((sub - 50) * eff),
		})
	}
	if out.UsedWeight <= 0 {
		out.Score = 50
		return out
	}
	var num float64
	for _, c := range out.Contributions {
		num += c.SubScore * c.Effective
	}
	out.Score = round2(num / out.UsedWeight)
	sort.SliceStable(out.Contributions, func(i, j int) bool {
		return math.Abs(out.Contributions[i].Pull) > math.Abs(out.Contributions[j].Pull)
	})
	return out
}

// ConfidenceResult is the day's confidence with its three factors kept separate, because a
// single number that cannot be decomposed is not explainable.
type ConfidenceResult struct {
	Confidence   float64 // 0..1
	Completeness float64 // share of intended weight that arrived
	Agreement    float64 // how much the usable evidence points the same way
	Conviction   float64 // how emphatic that evidence is
	Caveats      []string
}

// Confidence measures evidence QUALITY, not market direction.
//
//	confidence = completeness × agreement × conviction
//
// Each factor is independently computable and independently reportable, so "why 0.30" always
// has an answer. The multiplication is deliberate: any one factor collapsing should collapse
// the result, because complete-but-contradictory evidence and unanimous-but-absent evidence
// are both untrustworthy for different reasons.
//
// Precision is capped on purpose. The value is truncated to two decimals, and when less than
// 60% of the intended weight arrived it is additionally capped at 0.5 — a day built on half
// the evidence must not be able to report high confidence no matter how neatly the surviving
// half agrees.
func Confidence(evidence []model.Evidence, weights map[string]float64) ConfidenceResult {
	var total, arrived float64
	usable := make([]model.Evidence, 0, len(evidence))
	for _, e := range evidence {
		w := weights[e.Source]
		total += w
		if e.Usable() && w > 0 {
			arrived += w
			usable = append(usable, e)
		}
	}
	out := ConfidenceResult{Completeness: 1, Agreement: 1, Conviction: 1}
	if total > 0 {
		out.Completeness = arrived / total
	}
	if len(usable) == 0 {
		out.Confidence, out.Agreement, out.Conviction = 0, 0, 0
		out.Caveats = append(out.Caveats, "沒有任何可用證據，本日信心為 0")
		return out
	}

	// AGREEMENT — weighted dispersion of direction around its own mean, normalised so that
	// unanimity is 1 and a perfectly split panel approaches 0.
	var wsum, mean float64
	for _, e := range usable {
		w := weights[e.Source]
		wsum += w
		mean += e.Direction.Sign() * w
	}
	mean /= wsum
	var variance float64
	for _, e := range usable {
		d := e.Direction.Sign() - mean
		variance += d * d * weights[e.Source]
	}
	variance /= wsum
	out.Agreement = 1 - math.Min(1, math.Sqrt(variance))

	// CONVICTION — how emphatic the surviving evidence is, blended with each analyzer's own
	// stated confidence.
	strengthWeight := map[model.Strength]float64{
		model.StrengthWeak: 0.5, model.StrengthModerate: 0.7,
		model.StrengthStrong: 0.9, model.StrengthExtreme: 1.0,
	}
	var conv float64
	for _, e := range usable {
		s := strengthWeight[e.Strength]
		if s == 0 {
			s = 0.5
		}
		c := e.Confidence
		if c <= 0 {
			c = 1
		}
		conv += s * c * weights[e.Source]
	}
	out.Conviction = conv / wsum

	out.Confidence = out.Completeness * out.Agreement * out.Conviction

	if out.Completeness < 0.6 {
		if out.Confidence > 0.5 {
			out.Confidence = 0.5
		}
		out.Caveats = append(out.Caveats,
			fmt.Sprintf("僅 %.0f%% 的證據權重可用，信心上限壓在 0.5", out.Completeness*100))
	}
	if out.Agreement < 0.6 {
		out.Caveats = append(out.Caveats, "證據方向分歧，信心下修")
	}
	// Truncate rather than round: this is a quality measure built on uncalibrated
	// thresholds, and 0.84 should not become 0.8400000001 or imply three-digit accuracy.
	out.Confidence = math.Floor(out.Confidence*100) / 100

	// A floor, so that "the evidence contradicts itself" never reports the same number as
	// "there is no evidence". Two directly opposed sources drive agreement to ~0.01, and the
	// product then truncates to 0.00 — indistinguishable from the empty case above, which
	// returns exactly 0. Those are different states and the archive has to keep them apart,
	// the same way MISSING is kept apart from NEUTRAL everywhere else in this package.
	if out.Confidence <= 0 {
		out.Confidence = 0.01
	}
	out.Completeness = round2(out.Completeness)
	out.Agreement = round2(out.Agreement)
	out.Conviction = round2(out.Conviction)
	return out
}

// MissingSources lists the evidence that did not arrive, with each analyzer's stated reason.
// Persisted so a later validation run can ask "which inputs were unavailable that day"
// without re-deriving anything.
func MissingSources(evidence []model.Evidence) []string {
	var out []string
	for _, e := range evidence {
		if !e.Usable() {
			reason := e.Reason
			if reason == "" {
				reason = "未說明"
			}
			out = append(out, fmt.Sprintf("%s：%s", e.Source, reason))
		}
	}
	return out
}

// ConflictingSources reports evidence pointing against the weighted majority. Kept separate
// from the confidence number so a snapshot can answer "which evidence disagreed", not merely
// "how much disagreement there was".
func ConflictingSources(evidence []model.Evidence, weights map[string]float64) []string {
	usable := model.UsableEvidence(evidence)
	if len(usable) < 2 {
		return nil
	}
	var mean, wsum float64
	for _, e := range usable {
		w := weights[e.Source]
		wsum += w
		mean += e.Direction.Sign() * w
	}
	if wsum == 0 {
		return nil
	}
	mean /= wsum
	if math.Abs(mean) < 0.15 {
		return nil // no majority to disagree with
	}
	var out []string
	for _, e := range usable {
		s := e.Direction.Sign()
		if s != 0 && math.Signbit(s) != math.Signbit(mean) {
			out = append(out, fmt.Sprintf("%s（%s）", e.Source, e.Direction))
		}
	}
	return out
}
