package model

// Evidence is the ONLY thing an analyzer is allowed to produce. Analyzers never touch the
// Market Score directly: they emit normalized evidence, and the engine (M7) decides what
// that evidence is worth. This keeps "what the data says" separate from "what we score it".
//
// MISSING is not NEUTRAL. When a source is unavailable the analyzer returns
// MissingEvidence(...) — Available=false, Direction=UNKNOWN — and the engine EXCLUDES it
// from the weighted average rather than feeding it 50. A market with no margin data is not
// a market with neutral margin.

// Direction is the sign of what one analyzer sees. UNKNOWN is a first-class value, not a
// placeholder: it means "we could not tell", which is different from NEUTRAL ("we looked
// and it is balanced").
type Direction string

const (
	DirBullish Direction = "BULLISH"
	DirNeutral Direction = "NEUTRAL"
	DirBearish Direction = "BEARISH"
	DirUnknown Direction = "UNKNOWN"
)

// Valid reports whether d is one of the four defined values (guards against typos when
// evidence is decoded from an old snapshot).
func (d Direction) Valid() bool {
	switch d {
	case DirBullish, DirNeutral, DirBearish, DirUnknown:
		return true
	}
	return false
}

// Sign maps a direction onto -1/0/+1 for the agreement calculation in the confidence
// engine (M7). UNKNOWN has no sign and must be filtered out before calling this.
func (d Direction) Sign() float64 {
	switch d {
	case DirBullish:
		return 1
	case DirBearish:
		return -1
	}
	return 0
}

// Strength is how emphatic the reading is. It is deliberately coarse — four buckets, not a
// continuous number — because the underlying thresholds are uncalibrated hypotheses and a
// float would imply precision we do not have.
type Strength string

const (
	StrengthWeak     Strength = "WEAK"
	StrengthModerate Strength = "MODERATE"
	StrengthStrong   Strength = "STRONG"
	StrengthExtreme  Strength = "EXTREME"
	StrengthUnknown  Strength = "UNKNOWN"
)

func (s Strength) Valid() bool {
	switch s {
	case StrengthWeak, StrengthModerate, StrengthStrong, StrengthExtreme, StrengthUnknown:
		return true
	}
	return false
}

// Evidence source identifiers. Stable strings — they are persisted in every snapshot and
// used to answer "which analyzer was historically useful", so renaming one silently breaks
// historical comparison.
const (
	EvidencePrice   = "Price"
	EvidenceBreadth = "Breadth"
	EvidenceFutures = "ForeignFutures"
	EvidenceCash    = "ForeignCash"
	EvidenceMargin  = "Margin"
)

// Evidence is one analyzer's normalized reading.
//
// Metrics holds the raw numbers that produced the judgment. It is persisted verbatim so a
// future validation run can re-derive a different verdict from the SAME observation
// without re-fetching anything (the same trick internal/r6backtest uses with .cache).
// Metric keys are part of the stored schema: add freely, never rename or repurpose.
type Evidence struct {
	Source     string             `json:"source"`
	Direction  Direction          `json:"direction"`
	Strength   Strength           `json:"strength"`
	Confidence float64            `json:"confidence"` // 0..1, this analyzer's own conviction
	Summary    string             `json:"summary"`    // one-line Chinese justification
	Metrics    map[string]float64 `json:"metrics,omitempty"`

	// Available=false means the source was missing/failed. Direction and Strength are then
	// UNKNOWN and the engine must skip this evidence entirely.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"` // why it is unavailable
}

// MissingEvidence builds the canonical "we have no data" evidence. Every analyzer must use
// this instead of returning a NEUTRAL evidence with zero metrics.
func MissingEvidence(source, reason string) Evidence {
	return Evidence{
		Source:    source,
		Direction: DirUnknown,
		Strength:  StrengthUnknown,
		Available: false,
		Reason:    reason,
	}
}

// Usable reports whether the engine may include this evidence in scoring. Both conditions
// are required: an evidence that is Available but UNKNOWN-directioned is a bug, and this
// keeps it out of the average instead of silently counting as neutral.
func (e Evidence) Usable() bool {
	return e.Available && e.Direction != DirUnknown && e.Direction.Valid()
}

// Metric returns a metric and whether it was recorded. Callers must branch on ok rather
// than treating a missing metric as 0 — 0 is a legitimate value for most of these.
func (e Evidence) Metric(key string) (float64, bool) {
	if e.Metrics == nil {
		return 0, false
	}
	v, ok := e.Metrics[key]
	return v, ok
}

// UsableEvidence filters a slice down to the evidence the engine may score.
func UsableEvidence(list []Evidence) []Evidence {
	out := make([]Evidence, 0, len(list))
	for _, e := range list {
		if e.Usable() {
			out = append(out, e)
		}
	}
	return out
}
