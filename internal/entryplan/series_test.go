package entryplan_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ── EP-2: the regime series, and the mixtures it must make unexpressible ──────────────
//
// The measurement behind every assertion in this file is on entryplan.RegimeSource: on
// 2026-08-31 a live snapshot and a cache replay of the SAME SESSION agreed on all ten price
// metrics to the last decimal and differed on all eight breadth metrics, with posture UNKNOWN
// on both sides. A series that mixes the two archives is not a noisier history, it is a
// history of two different universes reported as one.

func liveSession(date string, regime model.Regime, score *float64) entryplan.RegimeSession {
	return entryplan.RegimeSession{
		Date:    date,
		Source:  entryplan.RegimeSourceLive,
		Regime:  regime,
		Posture: entryplan.PostureEvidence{Status: entryplan.Available, Posture: model.PostureSupportive},
		Score:   score,
	}
}

func replaySession(date string, regime model.Regime) entryplan.RegimeSession {
	return entryplan.RegimeSession{
		Date:    date,
		Source:  entryplan.RegimeSourceReplay,
		Regime:  regime,
		Posture: entryplan.UnavailablePosture(),
		// Score deliberately absent. See RegimeSession.Score.
	}
}

// ── the zero source ───────────────────────────────────────────────────────────────────

// RegimeSource("") IS NOT LIVE, AND IT IS NOT REPLAY.
//
// A zero value quietly read as LIVE is how a replay series ends up labelled market history —
// and the label is the only thing that says whether the breadth numbers in it are
// reproducible.
func TestTheZeroRegimeSourceIsNeitherLiveNorReplay(t *testing.T) {
	var zero entryplan.RegimeSource
	if zero != "" {
		t.Fatalf("the zero RegimeSource is %q — this test is checking the wrong value", zero)
	}
	if zero.Valid() {
		t.Error("RegimeSource(\"\").Valid() is true — \"\" is a caller who did not say, not a " +
			"source")
	}
	if zero == entryplan.RegimeSourceLive || zero == entryplan.RegimeSourceReplay {
		t.Fatal("the zero source has become one of the two archives")
	}
	for _, bad := range []entryplan.RegimeSource{"", "live", "LIVE ", "CACHE", "BACKTEST", "REPLAYED"} {
		if bad.Valid() {
			t.Errorf("%q accepted as a source", bad)
		}
	}

	// It cannot construct a series either — with rows, or without them.
	if _, err := entryplan.NewRegimeSeries("", nil); err == nil {
		t.Error("NewRegimeSeries(\"\", nil) succeeded — an unlabelled series is a series whose " +
			"breadth nobody can interpret")
	}
	if _, err := entryplan.NewRegimeSeries("", []entryplan.RegimeSession{
		liveSession("2026-08-31", model.RegimeBull, f(58)),
	}); err == nil {
		t.Error("an empty source was accepted and would have been inferred from the rows — " +
			"inferring it is how a mixed slice becomes a series with whichever label came first")
	}

	// And a SESSION with no source is refused, so the rows cannot smuggle one in.
	s := liveSession("2026-08-31", model.RegimeBull, f(58))
	s.Source = ""
	if err := s.Validate(); err == nil {
		t.Error("a session with no source validated")
	}
	if _, err := entryplan.NewLiveRegimeSeries([]entryplan.RegimeSession{s}); err == nil {
		t.Error("a sourceless session was admitted to a LIVE series — the zero value was " +
			"coerced to LIVE, which is exactly the default this type exists to refuse")
	}

	// The zero SERIES is not usable, which is what stops `var s RegimeSeries` from reading as
	// an empty live history.
	var series entryplan.RegimeSeries
	if series.Usable() {
		t.Error("the zero RegimeSeries reports Usable()")
	}
	if series.Source() != "" || series.Len() != 0 || series.Sessions() != nil {
		t.Error("the zero RegimeSeries is not empty")
	}
}

// ── replay: nil score stays nil ───────────────────────────────────────────────────────

// A REPLAY HAS NO SCORE, AND 0 IS NOT ONE.
//
// model.RegimeReplay was given no Score field at all for this reason: model.Snapshot.Validate
// requires Score ∈ [0,100], so a replay filling it would have had to write 0 and "persist
// 'maximum bearishness' for every replayed day". The pointer here carries the same fact, and
// this test follows it all the way through the projection a consumer actually reads.
func TestAReplayScoreIsNilAndStaysNilThroughProjection(t *testing.T) {
	r := replaySession("2026-08-31", model.RegimeBull)
	if r.Score != nil {
		t.Fatal("the replay fixture already carries a score")
	}

	// one row
	if got := r.Project(); got.Score != nil {
		t.Errorf("Project() produced score %v from a nil one — a substituted 0 would read as "+
			"the most bearish possible session, on every replayed day", *got.Score)
	}

	// the whole series, which is what a study walks
	series, err := entryplan.NewReplayRegimeSeries([]entryplan.RegimeSession{
		replaySession("2026-08-27", model.RegimeSideways),
		replaySession("2026-08-28", model.RegimeBull),
		replaySession("2026-08-31", model.RegimeBullPullback),
	})
	if err != nil {
		t.Fatalf("a well-formed replay series was refused: %v", err)
	}
	projected := series.Project()
	if len(projected) != 3 {
		t.Fatalf("projected %d rows, want 3", len(projected))
	}
	for _, p := range projected {
		if p.Score != nil {
			t.Errorf("%s: projected score = %v, want nil", p.Date, *p.Score)
		}
		if p.Source != entryplan.RegimeSourceReplay {
			t.Errorf("%s: projection lost the source (%q)", p.Date, p.Source)
		}
	}

	// NON-VACUITY: a LIVE score must survive the same projection, or "nil stays nil" is
	// indistinguishable from "the projection drops every score".
	live := liveSession("2026-08-31", model.RegimeBull, f(58.5))
	got := live.Project()
	if got.Score == nil {
		t.Fatal("a live score was dropped by Project() — the nil assertions above prove nothing")
	}
	if *got.Score != 58.5 {
		t.Errorf("live score projected as %v, want 58.5", *got.Score)
	}
	// And it is COPIED, not aliased: a caller moving its own float must not move the
	// projection.
	score := 58.5
	live.Score = &score
	got = live.Project()
	score = 1
	if *got.Score != 58.5 {
		t.Errorf("the projection aliases the caller's float: %v", *got.Score)
	}

	// A ZERO score is a real observation and must be preserved as one. This is the other half
	// of MISSING ≠ ZERO: refusing to invent a 0 is not the same as refusing to record one.
	zero := liveSession("2026-08-31", model.RegimeBear, f(0))
	if p := zero.Project(); p.Score == nil || *p.Score != 0 {
		t.Errorf("an observed score of 0 was projected as %v — 0 and nil are different facts "+
			"in both directions", p.Score)
	}

	// A replay that DOES carry a score is refused at the door.
	bad := replaySession("2026-08-31", model.RegimeBull)
	bad.Score = f(0)
	if err := bad.Validate(); err == nil {
		t.Error("a replay session carrying score 0 validated — that is the exact fabrication " +
			"model.RegimeReplay was shaped to prevent")
	}
	bad.Score = f(58)
	if err := bad.Validate(); err == nil {
		t.Error("a replay session carrying a plausible score validated")
	}
	if _, err := entryplan.NewReplayRegimeSeries([]entryplan.RegimeSession{bad}); err == nil {
		t.Error("a scored replay session was admitted to a series")
	}
}

// ── posture: absence is a contract, not a comment ─────────────────────────────────────

// A REPLAY'S POSTURE IS UNAVAILABLE, AND NEVER NEUTRAL.
//
// The three states are kept apart on purpose:
//
//	UNAVAILABLE        a replay — no producer exists, so there is nothing to wait for
//	INSUFFICIENT_DATA  a live day where fewer than two of the three inputs arrived
//	AVAILABLE          a measured stance
//
// structure.go's own words for why the second must not be NEUTRAL: "with one data point there
// is no posture, and calling that NEUTRAL would let a single outage read as calm". The first
// must not be NEUTRAL for a stronger reason still — no outage even happened.
func TestAReplayPostureIsUnavailableAndNeverNeutral(t *testing.T) {
	r := replaySession("2026-08-31", model.RegimeBull)
	if r.Posture.Status != entryplan.Unavailable {
		t.Errorf("replay posture status = %q, want UNAVAILABLE", r.Posture.Status)
	}
	if r.Posture.Posture != model.PostureUnknown {
		t.Errorf("replay posture = %q, want UNKNOWN", r.Posture.Posture)
	}
	if r.Posture.Usable() {
		t.Error("a replay posture reports Usable()")
	}
	if got := r.Project().Posture; got.Status != entryplan.Unavailable ||
		got.Posture != model.PostureUnknown {
		t.Errorf("projection changed the replay posture to %+v — the absence must survive the "+
			"trip, or a future consumer will find a stance where there was none", got)
	}

	// Every way of dressing the absence up as a stance is refused.
	for _, p := range []entryplan.PostureEvidence{
		{Status: entryplan.Unavailable, Posture: model.PostureNeutral},
		{Status: entryplan.InsufficientData, Posture: model.PostureNeutral},
		{Status: entryplan.Unavailable, Posture: model.PostureSupportive},
		{Status: entryplan.Available, Posture: model.PostureUnknown},
		{Status: entryplan.Available, Posture: ""},
		{Status: "", Posture: model.PostureUnknown},
		{Status: entryplan.Unavailable, Posture: "CALM"},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("PostureEvidence%+v validated", p)
		}
	}
	// And the two legitimate absences do validate, so the guard is not simply refusing
	// everything.
	for _, p := range []entryplan.PostureEvidence{
		entryplan.UnavailablePosture(),
		entryplan.UnmeasuredPosture(),
		{Status: entryplan.Available, Posture: model.PostureNeutral},
		{Status: entryplan.Available, Posture: model.PostureDeteriorating},
	} {
		if err := p.Validate(); err != nil {
			t.Errorf("PostureEvidence%+v was refused: %v", p, err)
		}
	}
	if entryplan.UnavailablePosture().Status == entryplan.UnmeasuredPosture().Status {
		t.Error("a replay's missing posture and a live day's unmeasured one have collapsed " +
			"into one status — only one of them could ever be filled by re-running anything")
	}

	// A replay claiming the LIVE kind of absence is still refused: INSUFFICIENT_DATA says a
	// producer exists and the evidence did not reach, which is not true of any replay.
	bad := replaySession("2026-08-31", model.RegimeBull)
	bad.Posture = entryplan.UnmeasuredPosture()
	if err := bad.Validate(); err == nil {
		t.Error("a replay session reported INSUFFICIENT_DATA posture and validated")
	}
	bad.Posture = entryplan.PostureEvidence{Status: entryplan.Available, Posture: model.PostureNeutral}
	if err := bad.Validate(); err == nil {
		t.Error("a replay session claimed a NEUTRAL stance and validated — this is the " +
			"invariant model.RegimeReplay.Validate enforces on disk, and it must hold in the " +
			"type a policy reads")
	}
	if _, err := entryplan.NewReplayRegimeSeries([]entryplan.RegimeSession{bad}); err == nil {
		t.Error("a posture-claiming replay session was admitted to a series")
	}
}

// The POLICY LAYER does not compensate for the missing posture. It cannot: PolicyFor takes
// (regime, semantic), so the same regime yields the same policy whether the posture behind it
// was measured, unmeasured or structurally absent.
//
// Asserted rather than assumed, because "policy v1 does not use posture" is only safe while
// nothing quietly supplies a default on the way in.
func TestThePolicyLayerNeitherReadsNorInventsAPosture(t *testing.T) {
	var postures []reflect.Type
	walkTypes(reflect.TypeOf(entryplan.PolicyResult{}), map[reflect.Type]bool{}, func(ty reflect.Type) {
		if ty == reflect.TypeOf(model.InstitutionalPosture("")) ||
			ty == reflect.TypeOf(entryplan.PostureEvidence{}) {
			postures = append(postures, ty)
		}
	})
	if len(postures) != 0 {
		t.Errorf("PolicyResult reaches %v — EP2-v1's policy is a function of (regime, "+
			"semantic) only, and a posture it can see is a posture it can be swayed by", postures)
	}

	// Same regime, three different posture situations, one answer.
	live := liveSession("2026-08-31", model.RegimeDistribution, f(41))
	unmeasured := live
	unmeasured.Posture = entryplan.UnmeasuredPosture()
	unmeasured.Score = nil
	replay := replaySession("2026-08-31", model.RegimeDistribution)

	want := entryplan.PolicyFor(model.RegimeDistribution, entryplan.EntrySemanticPullback)
	for _, s := range []entryplan.RegimeSession{live, unmeasured, replay} {
		got := entryplan.PolicyFor(s.Project().Regime.Regime, entryplan.EntrySemanticPullback)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s posture %q produced a different policy answer: %+v vs %+v",
				s.Source, s.Posture.Status, got, want)
		}
		// Nothing filled the gap in on the way through, either.
		if p := s.Project().Posture; p.Status != s.Posture.Status || p.Posture != s.Posture.Posture {
			t.Errorf("%s: the projection rewrote the posture from %+v to %+v",
				s.Source, s.Posture, p)
		}
	}
}

// ── ordering, uniqueness, source consistency ──────────────────────────────────────────

// A DUPLICATE DATE IS AN ERROR, not a last-write-wins.
//
// Two regime rows for one session means two runs disagreed, or one archive was read twice.
// There is no defensible winner between them, so picking one would publish the choice as
// market history — and the loser would be unrecoverable afterwards.
func TestASeriesRefusesDuplicateDatesRatherThanPickingAWinner(t *testing.T) {
	dup := []entryplan.RegimeSession{
		liveSession("2026-08-27", model.RegimeBull, f(60)),
		liveSession("2026-08-31", model.RegimeBull, f(58)),
		// same session, a different call — exactly the case a de-duplicating loader would
		// resolve silently
		liveSession("2026-08-31", model.RegimeBear, f(21)),
	}
	series, err := entryplan.NewLiveRegimeSeries(dup)
	if err == nil {
		t.Fatalf("a series with two 2026-08-31 rows was accepted with %d sessions — "+
			"last-write-wins turns a data integrity failure into a verdict", series.Len())
	}
	if !strings.Contains(err.Error(), "2026-08-31") {
		t.Errorf("the duplicate error does not name the session: %v", err)
	}
	if series.Usable() {
		t.Error("a series was returned alongside the error")
	}

	// An identical repeated row is refused too: "the same day twice" is the integrity failure,
	// whether or not the two rows agree.
	same := liveSession("2026-08-31", model.RegimeBull, f(58))
	if _, err := entryplan.NewLiveRegimeSeries([]entryplan.RegimeSession{same, same}); err == nil {
		t.Error("the same session repeated verbatim was accepted")
	}
}

// Out of order is an error, and it is NOT SORTED. Sorting repairs a caller who lost track of
// the order, and a series whose order was repaired is a series whose provenance is unknown.
func TestASeriesRefusesDisorderRatherThanSortingIt(t *testing.T) {
	descending := []entryplan.RegimeSession{
		liveSession("2026-08-31", model.RegimeBull, f(58)),
		liveSession("2026-08-27", model.RegimeBull, f(60)),
	}
	if s, err := entryplan.NewLiveRegimeSeries(descending); err == nil {
		got := s.Sessions()
		t.Fatalf("a descending series was accepted and returned %s, %s — the order was "+
			"repaired instead of reported", got[0].Date, got[1].Date)
	}

	// The ascending version of the same rows IS accepted, and comes back ascending.
	ascending := []entryplan.RegimeSession{
		liveSession("2026-08-25", model.RegimeSideways, f(50)),
		liveSession("2026-08-27", model.RegimeBull, f(60)),
		liveSession("2026-08-31", model.RegimeBullPullback, f(58)),
	}
	s, err := entryplan.NewLiveRegimeSeries(ascending)
	if err != nil {
		t.Fatalf("an ascending series was refused: %v", err)
	}
	want := []string{"2026-08-25", "2026-08-27", "2026-08-31"}
	for n, sess := range s.Sessions() {
		if sess.Date != want[n] {
			t.Errorf("session %d = %s, want %s", n, sess.Date, want[n])
		}
	}
	// Deterministic across calls, and the projection keeps the same order.
	if !reflect.DeepEqual(s.Sessions(), s.Sessions()) {
		t.Error("Sessions() is not deterministic")
	}
	for n, p := range s.Project() {
		if p.Date != want[n] {
			t.Errorf("projection %d = %s, want %s", n, p.Date, want[n])
		}
	}
	// Sessions() hands out a COPY: a caller reordering the result must not reorder the series.
	got := s.Sessions()
	got[0], got[2] = got[2], got[0]
	if s.Sessions()[0].Date != "2026-08-25" {
		t.Error("Sessions() exposes the backing array, so the invariants hold only until " +
			"somebody uses them")
	}
	if at, ok := s.At(1); !ok || at.Date != "2026-08-27" {
		t.Errorf("At(1) = %+v, %v", at, ok)
	}
	for _, n := range []int{-1, 3, 99} {
		if _, ok := s.At(n); ok {
			t.Errorf("At(%d) claims to have a session", n)
		}
	}
}

// A date that is not a session date is refused: a row not keyed to a session is not a row.
func TestASeriesRefusesUnparsableDates(t *testing.T) {
	for _, date := range []string{"", "yesterday", "2026-8-31", "31/08/2026", "2026-13-01", "2026-08-32"} {
		s := liveSession(date, model.RegimeBull, f(58))
		if err := s.Validate(); err == nil {
			t.Errorf("session date %q validated", date)
		}
		if _, err := entryplan.NewLiveRegimeSeries([]entryplan.RegimeSession{s}); err == nil {
			t.Errorf("a series containing the date %q was accepted", date)
		}
	}
	// A regime of UNKNOWN is NOT an error: a day the regime layer could not call is a real
	// row, and dropping it would silently close the gap in the series.
	ok := liveSession("2026-08-31", model.RegimeUnknown, nil)
	if err := ok.Validate(); err != nil {
		t.Errorf("an UNKNOWN regime row was refused: %v — UNKNOWN is a state, and a series "+
			"with the unknown days removed is a series that reports coverage it did not have",
			err)
	}
	if got := ok.Project().Regime; got.Status != entryplan.InsufficientData {
		t.Errorf("an UNKNOWN regime projected as %q, want INSUFFICIENT_DATA: it WAS computed, "+
			"and the data could not support a call", got.Status)
	}
	if got := ok.Project().Regime; got.Usable() {
		t.Error("an UNKNOWN regime projected as a usable regime call")
	}
	// A regime string no rule can produce IS an error.
	bad := liveSession("2026-08-31", "MELT_UP", nil)
	if err := bad.Validate(); err == nil {
		t.Error("an undefined regime validated")
	}
}

// A SERIES CARRIES ONE SOURCE, and a row from the other archive is refused rather than
// filtered — filtering would hand back a shorter series that looks complete.
func TestASeriesRefusesAMixtureOfSources(t *testing.T) {
	mixed := []entryplan.RegimeSession{
		liveSession("2026-08-27", model.RegimeBull, f(60)),
		replaySession("2026-08-31", model.RegimeBull),
	}
	if s, err := entryplan.NewLiveRegimeSeries(mixed); err == nil {
		t.Fatalf("a LIVE series accepted a REPLAY row (%d sessions) — the two measure breadth "+
			"over different universes", s.Len())
	}
	if _, err := entryplan.NewReplayRegimeSeries(mixed); err == nil {
		t.Fatal("a REPLAY series accepted a LIVE row")
	}
	// Each source accepts its own rows.
	if _, err := entryplan.NewLiveRegimeSeries(mixed[:1]); err != nil {
		t.Errorf("a LIVE series refused a LIVE row: %v", err)
	}
	if _, err := entryplan.NewReplayRegimeSeries(mixed[1:]); err != nil {
		t.Errorf("a REPLAY series refused a REPLAY row: %v", err)
	}
	// The named constructors and the explicit-source one agree.
	a, err1 := entryplan.NewReplayRegimeSeries(mixed[1:])
	b, err2 := entryplan.NewRegimeSeries(entryplan.RegimeSourceReplay, mixed[1:])
	if err1 != nil || err2 != nil || !reflect.DeepEqual(a, b) {
		t.Errorf("NewReplayRegimeSeries and NewRegimeSeries(REPLAY, ...) disagree: %v / %v", err1, err2)
	}
	if a.Source() != entryplan.RegimeSourceReplay {
		t.Errorf("series source = %q", a.Source())
	}

	// An EMPTY series is legal, for a named source: "this archive has no rows in this window"
	// is a fact, and refusing it pushes callers into inventing one.
	empty, err := entryplan.NewLiveRegimeSeries(nil)
	if err != nil {
		t.Errorf("an empty LIVE series was refused: %v", err)
	}
	if !empty.Usable() || empty.Len() != 0 || empty.Project() != nil {
		t.Errorf("the empty series is malformed: usable=%v len=%d", empty.Usable(), empty.Len())
	}

	// The constructor copies its input: mutating the caller's slice afterwards must not reach
	// inside the validated series.
	rows := []entryplan.RegimeSession{liveSession("2026-08-31", model.RegimeBull, f(58))}
	s, err := entryplan.NewLiveRegimeSeries(rows)
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Regime = model.RegimeBear
	rows[0].Date = "1999-01-01"
	if got, _ := s.At(0); got.Regime != model.RegimeBull || got.Date != "2026-08-31" {
		t.Errorf("the series aliases the caller's slice: %+v", got)
	}
}

// ── mixing must be unexpressible, not merely refused ──────────────────────────────────

// The API-shape half of the argument, checked against the source.
//
// A refusal at runtime is worth having; an API through which the mixture cannot even be
// SPELLED is worth more. Two properties:
//
//  1. RegimeSeries has no exported fields, so no struct literal can assemble a series that
//     was never validated.
//  2. No constructor takes more than one source, and none takes a variadic list of anything
//     source-like — no LoadRegimeSeries(dirs ...string), no (liveDir, replayDir string).
//
// The loader itself is deliberately absent from this package: internal/entryplan is a pure
// domain package and architecture_test.go's wholesale `os.` ban over production files would
// (correctly) turn red the moment one opened a directory.
func TestMixingSourcesIsUnexpressibleInTheAPI(t *testing.T) {
	ty := reflect.TypeOf(entryplan.RegimeSeries{})
	for n := 0; n < ty.NumField(); n++ {
		if f := ty.Field(n); f.IsExported() {
			t.Errorf("RegimeSeries.%s is exported — a struct literal can now build a series "+
				"that skipped every invariant the constructor checks", f.Name)
		}
	}
	if ty.NumField() == 0 {
		t.Fatal("RegimeSeries has no fields at all — this test is inspecting the wrong type")
	}

	for _, fn := range []struct {
		name string
		v    any
	}{
		{"NewRegimeSeries", entryplan.NewRegimeSeries},
		{"NewLiveRegimeSeries", entryplan.NewLiveRegimeSeries},
		{"NewReplayRegimeSeries", entryplan.NewReplayRegimeSeries},
	} {
		f := reflect.TypeOf(fn.v)
		if f.IsVariadic() {
			t.Errorf("%s is variadic — a variadic constructor is how two directories, or two "+
				"sources, become one call", fn.name)
		}
		var sources int
		for n := 0; n < f.NumIn(); n++ {
			if f.In(n) == reflect.TypeOf(entryplan.RegimeSourceLive) {
				sources++
			}
		}
		if sources > 1 {
			t.Errorf("%s takes %d sources — one call must not be able to name both archives",
				fn.name, sources)
		}
	}

	// And the package ships no loader and no fallback. Source-scanned, because the argument
	// is about what CANNOT be written here, and a promise in a doc comment is not that.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for n, line := range strings.Split(string(src), "\n") {
			code := line
			if at := strings.Index(code, "//"); at >= 0 {
				code = code[:at]
			}
			if !strings.HasPrefix(strings.TrimSpace(code), "func ") {
				continue
			}
			if strings.Contains(code, "func Load") || strings.Contains(code, "dirs ...") {
				t.Errorf("%s:%d declares a loader in a pure domain package: %s",
					name, n+1, strings.TrimSpace(code))
			}
		}
	}
	if scanned < 6 {
		t.Fatalf("scanned only %d production files — the sweep is not seeing the package", scanned)
	}
}

// The projection must always say which archive it came from, and the zero session must not
// project a valid source.
//
// WHAT THIS TEST DOES NOT DO: it does not inspect SessionEvidence's FIELD SET. It used to claim
// that "SessionEvidence must not grow a field that lets a source be blended after the fact",
// and it checked nothing of the kind — a per-row `BreadthSource RegimeSource` hardcoded to LIVE
// inside Project() passes every assertion below. The field guard is doc.go's TODO(J-2). A
// claimed guard that does not exist is worse than an absent one, because it is the reason
// nobody writes the real one.
func TestEveryProjectionNamesItsArchive(t *testing.T) {
	for _, s := range []entryplan.RegimeSession{
		liveSession("2026-08-31", model.RegimeBull, f(58)),
		replaySession("2026-08-31", model.RegimeBull),
	} {
		p := s.Project()
		if p.Source != s.Source || !p.Source.Valid() {
			t.Errorf("projection of a %s row reports source %q", s.Source, p.Source)
		}
		if p.Date != s.Date {
			t.Errorf("projection rewrote the date: %s → %s", s.Date, p.Date)
		}
	}
	// A projection of a row with no source is still not a source.
	var bare entryplan.RegimeSession
	if p := bare.Project(); p.Source.Valid() {
		t.Errorf("the zero session projected the valid source %q", p.Source)
	}
}
