package entryplan

import (
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ── EP-2: regime SERIES, and the one thing it must make unexpressible ─────────────────
//
// A study that walks the regime day by day — a policy A/B, a stop-profile sweep, any
// backtest arm — reads a SERIES. This file is the type and the invariants of that series.
// It deliberately does NOT read one from disk: see the loader note below.

// RegimeSource is WHERE a regime observation came from, and it has NO USABLE ZERO VALUE.
//
// # Why this is a required field rather than a nice-to-have
//
// Measured, not assumed. On 2026-08-31, the one session both archives cover, a live snapshot
// and a cache replay agreed on ALL TEN price metrics to the last decimal and differed on ALL
// EIGHT breadth metrics (breadth_above_ma20 46.65 vs 50.56, +3.91pp; advancing_ratio 22.39 vs
// 30.60, +8.21pp), while institutional posture was UNKNOWN on both sides. The cause is
// provider/cache.go's Universe(), which globs .cache/*.json: membership is "whatever is in the
// cache today", so it carries survivorship bias and is not reproducible over time.
//
// BreadthQuality feeds analyzer rules R3/R6/R7/R8/R9, and 50.56 sits 4.44pp from
// BreadthHealthyPct — the same order of magnitude as the gap. So on a day nearer a threshold
// the two sources produce DIFFERENT REGIMES for the same session, and a series that mixes them
// reports the mixture as market history.
//
// The zero value "" is therefore refused rather than defaulted to LIVE. Defaulting would
// assert the very fact the field exists to establish, and it would do it for the caller who
// did not know there was a choice — which is exactly the caller who most needs to be told.
type RegimeSource string

const (
	// RegimeSourceLive — recorded by the live pipeline on the session itself. Has a Market
	// Score and can see institutional posture.
	RegimeSourceLive RegimeSource = "LIVE"
	// RegimeSourceReplay — replayed point-in-time from the local price cache
	// (model.RegimeReplay). Has NO score and NO posture, ever, and its breadth is measured
	// over the cache's current membership.
	RegimeSourceReplay RegimeSource = "REPLAY"
)

// Valid reports whether a source names one of the two archives.
//
// "" IS NOT A SOURCE. TestTheZeroRegimeSourceIsNeitherLiveNorReplay holds it, because a zero value quietly
// read as LIVE is how a replay series ends up labelled as market history.
func (s RegimeSource) Valid() bool {
	return s == RegimeSourceLive || s == RegimeSourceReplay
}

// PostureEvidence is institutional posture WITH ITS OWN AVAILABILITY, and the wrapper is the
// point of the type.
//
// # Why not just model.PostureUnknown
//
// Because model.PostureUnknown already means something else. structure.go: posture is UNKNOWN
// "when fewer than two of the three are available — with one data point there is no posture,
// and calling that NEUTRAL would let a single outage read as calm". That is a LIVE MEASUREMENT
// OUTCOME: somebody looked, and the futures/cash/margin evidence did not reach.
//
// A replay's absence is a different fact. Nothing produces posture retroactively — there is no
// historical producer at all, so there is nothing to wait for and no outage to blame. In this
// package's own vocabulary that is UNAVAILABLE, not INSUFFICIENT_DATA (see Availability), and
// collapsing the two would lose the only distinction that tells a reader whether re-running the
// pipeline could ever fill the gap.
//
// So the two absences are carried on two fields:
//
//	Status  UNAVAILABLE        no producer exists — a replay, always
//	Status  INSUFFICIENT_DATA  a producer exists and the evidence did not reach — a live day
//	Status  AVAILABLE          a stance was measured: SUPPORTIVE / NEUTRAL / DETERIORATING
//
// and Posture is model.PostureUnknown in both absent cases. NEVER PostureNeutral. That is the
// same invariant model.RegimeReplay.Validate enforces on disk ("a replay has no institutional
// evidence and must be UNKNOWN"), restated in the type a policy reads, so it survives the trip.
//
// # The policy layer does not read it, and must not fill it
//
// EP2-v1's PolicyFor takes (regime, semantic) and nothing else, so no policy can be swayed by
// a posture that is not there. What matters is that the ABSENCE stays absent: a future item
// that starts reading posture must find UNAVAILABLE and refuse, not find NEUTRAL and relax.
type PostureEvidence struct {
	Status  Availability               `json:"status"`
	Posture model.InstitutionalPosture `json:"posture"`
}

// Usable reports whether an actual institutional stance is in hand.
func (p PostureEvidence) Usable() bool {
	return p.Status.OK() && p.Posture.Valid() && p.Posture != model.PostureUnknown
}

// UnavailablePosture is the posture evidence a REPLAY must carry. Provided as a constructor so
// no caller has to remember which of the two absences a replay is.
func UnavailablePosture() PostureEvidence {
	return PostureEvidence{Status: Unavailable, Posture: model.PostureUnknown}
}

// UnmeasuredPosture is the posture evidence a LIVE day carries when fewer than two of the
// three institutional inputs were available — structure.go's own definition of UNKNOWN.
func UnmeasuredPosture() PostureEvidence {
	return PostureEvidence{Status: InsufficientData, Posture: model.PostureUnknown}
}

// Validate rejects the shapes that would let an absent posture be read as a stance.
func (p PostureEvidence) Validate() error {
	if !p.Posture.Valid() {
		return fmt.Errorf("entryplan: posture %q is not an InstitutionalPosture", p.Posture)
	}
	switch p.Status {
	case Available:
		if p.Posture == model.PostureUnknown {
			return fmt.Errorf("entryplan: posture is AVAILABLE and UNKNOWN — a measured " +
				"stance is SUPPORTIVE, NEUTRAL or DETERIORATING; UNKNOWN is the absence of one")
		}
	case Unavailable, InsufficientData:
		if p.Posture != model.PostureUnknown {
			return fmt.Errorf("entryplan: posture status %q carries the stance %q — an absent "+
				"posture must not be spelled with a stance, least of all NEUTRAL",
				p.Status, p.Posture)
		}
	default:
		return fmt.Errorf("entryplan: posture status %q is not an availability", p.Status)
	}
	return nil
}

// RegimeSession is ONE session's regime observation from ONE source.
//
// Every field that a source may not have is optional IN THE TYPE, not filled with a
// plausible-looking value: see Score.
type RegimeSession struct {
	// Date is the session, YYYY-MM-DD. The series is keyed on it, one row per session.
	Date string `json:"date"`
	// Source is which archive this row came from. Required; "" is refused.
	Source RegimeSource `json:"source"`
	// Regime is the call for that session. model.RegimeUnknown is ALLOWED here — a day the
	// regime layer could not call is a real row, and dropping it would silently close the gap
	// in the series.
	Regime model.Regime `json:"regime"`

	// Posture is the institutional stance, with its own availability. See PostureEvidence.
	Posture PostureEvidence `json:"posture"`

	// Score is the Market Score, 0-100.
	//
	// A POINTER, and REPLAY ROWS MUST LEAVE IT NIL. nil means "this source has no such
	// evidence"; 0 would mean "maximum bearishness was observed". model.RegimeReplay was
	// given no Score field at all for precisely this reason — model.Snapshot.Validate
	// requires Score ∈ [0,100], so a replay filling it would have had to write 0 and
	// "persist 'maximum bearishness' for every replayed day — the MISSING → ZERO failure
	// this codebase exists to avoid".
	//
	// Nothing in this package may substitute a value for it, and Project() must carry the nil
	// through untouched.
	Score *float64 `json:"score,omitempty"`
}

// Validate reports whether one session row can be trusted.
//
// The REPLAY clauses are a DATA CONTRACT, not documentation. model.RegimeReplay.Validate
// already refuses a replay that claims a posture; without the same refusal here, a replay
// re-materialised into this type could acquire one on the way and no test on the archive would
// see it.
func (s RegimeSession) Validate() error {
	if s.Date == "" {
		return fmt.Errorf("entryplan: regime session has no date")
	}
	if _, err := time.Parse(asOfLayout, s.Date); err != nil {
		return fmt.Errorf("entryplan: regime session date %q is not YYYY-MM-DD", s.Date)
	}
	if !s.Source.Valid() {
		return fmt.Errorf("entryplan: regime session %s has source %q — \"\" is not a source, "+
			"and LIVE and REPLAY are not interchangeable", s.Date, s.Source)
	}
	if !s.Regime.Valid() {
		return fmt.Errorf("entryplan: regime session %s has undefined regime %q", s.Date, s.Regime)
	}
	if err := s.Posture.Validate(); err != nil {
		return fmt.Errorf("entryplan: regime session %s: %w", s.Date, err)
	}
	if s.Score != nil && !finiteScore(*s.Score) {
		return fmt.Errorf("entryplan: regime session %s has score %v, want 0-100", s.Date, *s.Score)
	}
	if s.Source == RegimeSourceReplay {
		// A replay has no score, and 0 is not a stand-in for one.
		if s.Score != nil {
			return fmt.Errorf("entryplan: replay session %s carries score %v — a replay has no "+
				"multi-source evidence to score, and writing one (0 least of all) fabricates it",
				s.Date, *s.Score)
		}
		// A replay has no posture, and the absence is UNAVAILABLE: no producer exists, so
		// there is nothing to wait for. INSUFFICIENT_DATA would claim a re-run could fill it.
		if s.Posture.Status != Unavailable {
			return fmt.Errorf("entryplan: replay session %s reports posture status %q — a "+
				"replay has no institutional producer at all, so its posture is UNAVAILABLE",
				s.Date, s.Posture.Status)
		}
	}
	return nil
}

// finiteScore is the Market Score guard, matching model.Snapshot.Validate's 0-100 range.
func finiteScore(v float64) bool { return finite(v) && v >= 0 && v <= 100 }

// SessionEvidence is one session PROJECTED into the evidence shapes a Snapshot carries.
//
// It exists so the trip from a stored regime row to a plan input has ONE implementation that
// can be tested for what it must not do: invent a score, invent a posture, or forget which
// archive the row came from.
type SessionEvidence struct {
	Date   string       `json:"date"`
	Source RegimeSource `json:"source"`

	Regime  RegimeEvidence  `json:"regime"`
	Posture PostureEvidence `json:"posture"`

	// Score is COPIED, and nil STAYS nil. See RegimeSession.Score.
	Score *float64 `json:"score,omitempty"`
}

// Project turns a session into plan evidence.
//
// PURE, and it copies every pointer: a caller mutating its own float afterwards cannot change
// an already-returned projection, the same property ComputePlan has.
//
// The regime status is read the same three ways ComputePlan reads it, and THE UNKNOWN READING
// IS IDENTICAL in both — model.RegimeUnknown is INSUFFICIENT_DATA here and there, never
// UNAVAILABLE, which is the one row this package exists to protect:
//
//	a called regime      → AVAILABLE
//	model.RegimeUnknown  → INSUFFICIENT_DATA  (it WAS computed; the data could not support it)
//	not a regime at all  → UNAVAILABLE        (nothing computed this)
//
// The two readings are NOT equivalent on the third row, and this doc does not claim they
// "cannot drift", because they already have: for a string that is not a regime at all this
// returns UNAVAILABLE, while ComputePlan's regime row reports INSUFFICIENT_DATA whenever the
// caller labelled that evidence AVAILABLE. No test compares the two. The reading here is the
// defensible one — nothing computed a non-regime string — and reconciling them is doc.go's
// TODO(J-1), not a claim to be made in a comment before the test exists.
func (s RegimeSession) Project() SessionEvidence {
	out := SessionEvidence{
		Date:    s.Date,
		Source:  s.Source,
		Posture: s.Posture,
		Regime:  RegimeEvidence{Status: Unavailable, Regime: s.Regime},
	}
	switch {
	case s.Regime == model.RegimeUnknown:
		out.Regime.Status = InsufficientData
	case s.Regime.Valid():
		out.Regime.Status = Available
	}
	// NO SUBSTITUTION. A nil score projects to a nil score; there is no `if score == nil {
	// score = 0 }` here and there must never be one.
	if s.Score != nil {
		v := *s.Score
		out.Score = &v
	}
	return out
}

// RegimeSeries is an ascending, single-source, one-row-per-session run of regime observations.
//
// # Its fields are unexported ON PURPOSE
//
// The invariants below are not advice, and a struct literal is how advice gets bypassed. With
// unexported fields the ONLY way to obtain a series is through a constructor that validated it,
// so "this series is ascending, unique and single-source" is a property of every value of the
// type rather than of the ones somebody remembered to check.
//
// # Why there is no LoadRegimeSeries(dirs ...string)
//
// A variadic or two-directory loader makes MIXING SOURCES EXPRESSIBLE AT THE API, and the
// 2026-08-31 measurement in RegimeSource's doc is what a mixed series produces: identical
// prices, breadth off by 3.91pp, and a regime that can differ on any day near a threshold. The
// constructors therefore take the source as a REQUIRED, SINGLE argument, and a session whose
// own Source disagrees with it is an error rather than a coercion.
//
// There is also NO FALLBACK. "Live is missing, read the replay" is the one line that would
// turn every guarantee here into a coin flip, and it is not implemented at any level: not in
// the constructors, and not in the loader that does not exist yet.
//
// # And why there is no loader here AT ALL
//
// internal/entryplan is a pure domain package: no filesystem, enforced by
// architecture_test.go's wholesale `os.` ban over every production file, not by a promise. A
// loader in this package would make that test red the moment it opened a directory — correctly,
// because a domain type that can read a directory is no longer a domain type.
//
// So EP-2 ships the TYPE AND THE INVARIANTS, and the reading of bytes is a separate ADAPTER
// work item that lives outside this package (alongside market/service's replay_store.go, which
// already owns the file layout). That adapter's signature is constrained in advance by the
// paragraph above: LoadLiveRegimeSeries / LoadReplayRegimeSeries, or one Load taking a
// RegimeSource — never a dirs... and never a fallback. It hands its rows to NewRegimeSeries,
// which is the only way to get a valid series, so the invariants apply to loaded data without
// this package ever having touched a disk.
type RegimeSeries struct {
	source   RegimeSource
	sessions []RegimeSession
}

// NewRegimeSeries validates a run of sessions and returns the series, or an error naming the
// first violation.
//
// The caller MUST NAME THE SOURCE. It is not inferred from the rows, because inferring it from
// the rows is how a mixed slice becomes a series with whichever label came first.
//
// # What it refuses, and what it does NOT repair
//
//	source ""                  not a source; never defaulted to LIVE
//	a session from another source   a mixed series, refused rather than filtered
//	an unparsable date         refused; a row not keyed to a session is not a row
//	the same date twice        AN ERROR. Two regime rows for one session is a data integrity
//	                           failure — two runs disagreeing, or one archive read twice — and
//	                           last-write-wins would pick a winner and publish the choice as
//	                           history. There is no defensible winner, so there is no winner.
//	descending / out of order  refused, and NOT SORTED. Sorting repairs a caller who lost
//	                           track of the order, and a series whose order was repaired is a
//	                           series whose provenance is now unknown.
//
// An EMPTY series (zero sessions, valid source) is allowed: "this source has no rows in this
// window" is a fact, and refusing it would push callers into inventing one.
func NewRegimeSeries(source RegimeSource, sessions []RegimeSession) (RegimeSeries, error) {
	if !source.Valid() {
		return RegimeSeries{}, fmt.Errorf("entryplan: regime series source %q is not LIVE or "+
			"REPLAY — a series must say which archive it is, and \"\" is not an answer", source)
	}

	// COPIED, so the caller cannot mutate the series out from under its own invariants after
	// they were checked.
	out := RegimeSeries{source: source, sessions: make([]RegimeSession, 0, len(sessions))}

	var prev time.Time
	for n, s := range sessions {
		if s.Source != source {
			return RegimeSeries{}, fmt.Errorf("entryplan: %s series contains a %q session at "+
				"index %d (%s) — LIVE and REPLAY measure breadth over different universes and "+
				"must not be mixed", source, s.Source, n, s.Date)
		}
		if err := s.Validate(); err != nil {
			return RegimeSeries{}, err
		}
		// Parsed above by Validate; the error cannot fire here, and it is still checked
		// rather than discarded.
		at, err := time.Parse(asOfLayout, s.Date)
		if err != nil {
			return RegimeSeries{}, fmt.Errorf("entryplan: regime session date %q is not "+
				"YYYY-MM-DD", s.Date)
		}
		if n > 0 {
			if at.Equal(prev) {
				return RegimeSeries{}, fmt.Errorf("entryplan: %s series has two sessions dated "+
					"%s — one session is one row, and choosing a winner between two "+
					"disagreeing rows would publish the choice as market history", source, s.Date)
			}
			if at.Before(prev) {
				return RegimeSeries{}, fmt.Errorf("entryplan: %s series is not ascending at "+
					"index %d (%s follows %s) — the order is not repaired here, because a "+
					"repaired order hides which run produced it", source, n, s.Date,
					prev.Format(asOfLayout))
			}
		}
		prev = at
		out.sessions = append(out.sessions, s)
	}
	return out, nil
}

// NewLiveRegimeSeries and NewReplayRegimeSeries are the two named entry points, mirroring the
// LoadLive / LoadReplay shape the future adapter is required to use. Naming the source in the
// FUNCTION removes the last way to pass the wrong one.
func NewLiveRegimeSeries(sessions []RegimeSession) (RegimeSeries, error) {
	return NewRegimeSeries(RegimeSourceLive, sessions)
}

func NewReplayRegimeSeries(sessions []RegimeSession) (RegimeSeries, error) {
	return NewRegimeSeries(RegimeSourceReplay, sessions)
}

// Source is the one archive every row in this series came from. "" only for the zero value,
// which no constructor returns and which Usable() rejects.
func (s RegimeSeries) Source() RegimeSource { return s.source }

// Usable reports whether this is a series at all, rather than a `var s RegimeSeries`.
func (s RegimeSeries) Usable() bool { return s.source.Valid() }

// Len is how many sessions the series holds.
func (s RegimeSeries) Len() int { return len(s.sessions) }

// Sessions returns the sessions in ASCENDING date order.
//
// A COPY. Handing out the backing slice would let a caller reorder or duplicate the rows the
// constructor just validated, which would make the invariants true only until someone used
// them.
func (s RegimeSeries) Sessions() []RegimeSession {
	if len(s.sessions) == 0 {
		return nil
	}
	out := make([]RegimeSession, len(s.sessions))
	copy(out, s.sessions)
	return out
}

// At returns the n-th session, ascending. ok == false for an out-of-range index, so a caller
// cannot panic its way into a zero session and read it as a market state.
func (s RegimeSeries) At(n int) (RegimeSession, bool) {
	if n < 0 || n >= len(s.sessions) {
		return RegimeSession{}, false
	}
	return s.sessions[n], true
}

// Project maps the whole series into plan evidence, ascending, nil scores intact.
func (s RegimeSeries) Project() []SessionEvidence {
	if len(s.sessions) == 0 {
		return nil
	}
	out := make([]SessionEvidence, 0, len(s.sessions))
	for _, session := range s.sessions {
		out = append(out, session.Project())
	}
	return out
}
