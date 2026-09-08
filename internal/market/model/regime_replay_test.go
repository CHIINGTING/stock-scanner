package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ── the reflection allowlist ──────────────────────────────────────────────────────────
//
// RegimeReplay deliberately has NO Score, Confidence, Evidence or Flags field. That is not
// an oversight to be tidied up later: a point-in-time replay has no multi-source evidence,
// so any number in those slots would be invented. Score 0 in particular would read as
// "maximum bearishness" on every replayed day.
//
// This is asserted with an ALLOWLIST rather than a denylist, following
// internal/healthcheck/judge_test.go. A denylist that only looked for numeric fields once
// let a string field through there; an allowlist fails on ANY new field — including
// "score_note", "mkt_score" or a nested one — and forces a human to look at it.
//
// The walk is recursive, so a field added to StructureView or ReplayExtent trips it too.
// That is intended: StructureView is shared with the live Snapshot, and a new field arriving
// from that side needs the same review before it flows into replayed history.

// replayFields is every json key reachable from RegimeReplay, qualified by its owning type.
var replayFields = []string{
	"RegimeReplay.kind",
	"RegimeReplay.replay_date",
	"RegimeReplay.replayed_regime",
	"RegimeReplay.rule_id",
	"RegimeReplay.structure",
	"RegimeReplay.reasons",
	"RegimeReplay.caveats",
	"RegimeReplay.replayed_at",
	"RegimeReplay.replay_schema_version",
	"RegimeReplay.input_extent",

	"StructureView.benchmark",
	"StructureView.structure",
	"StructureView.breadth_quality",
	"StructureView.institutional_posture",
	"StructureView.trend_intact",
	"StructureView.price_resilient",
	"StructureView.orderly_correction",
	"StructureView.failed_highs",
	"StructureView.metrics",

	"ReplayExtent.benchmark_symbol",
	"ReplayExtent.benchmark_bars",
	"ReplayExtent.benchmark_first_date",
	"ReplayExtent.benchmark_last_date",
	"ReplayExtent.breadth_index",
	"ReplayExtent.breadth_first_date",
	"ReplayExtent.breadth_last_date",
	"ReplayExtent.breadth_universe",
	"ReplayExtent.breadth_symbols_loaded",
}

// replayGoFields is the same allowlist over GO FIELD NAMES, which is a different set: a
// field tagged `json:"-"` has no json key at all, so the tag allowlist above cannot see it.
// That gap was real: a Score float64 field tagged `json:"-"` passed every test here.
var replayGoFields = []string{
	"RegimeReplay.Kind",
	"RegimeReplay.Date",
	"RegimeReplay.Regime",
	"RegimeReplay.RuleID",
	"RegimeReplay.Structure",
	"RegimeReplay.Reasons",
	"RegimeReplay.Caveats",
	"RegimeReplay.ReplayedAt",
	"RegimeReplay.SchemaVer",
	"RegimeReplay.InputExtent",

	"StructureView.Benchmark",
	"StructureView.Structure",
	"StructureView.Breadth",
	"StructureView.Posture",
	"StructureView.TrendIntact",
	"StructureView.PriceResilient",
	"StructureView.OrderlyCorrection",
	"StructureView.FailedHighs",
	"StructureView.Metrics",

	"ReplayExtent.BenchmarkSymbol",
	"ReplayExtent.BenchmarkBars",
	"ReplayExtent.BenchmarkFirst",
	"ReplayExtent.BenchmarkLast",
	"ReplayExtent.BreadthIndex",
	"ReplayExtent.BreadthFirst",
	"ReplayExtent.BreadthLast",
	"ReplayExtent.BreadthUniverse",
	"ReplayExtent.BreadthSymbolsLoaded",
}

// walkFields collects "Type.jsontag" for every field reachable from rt, and the Go field
// names alongside. time.Time is a leaf: it marshals as a string and its internals are not
// part of our shape.
//
// A field tagged `json:"-"` is recorded in goNames and NOT in tags, and it is still WALKED.
// That asymmetry is the whole point and it used to be wrong: `continue` fired before goNames
// was filled, so a Score float64 tagged `json:"-"` was invisible to both allowlist tests
// and the entire package stayed green — while the comment on
// TestRegimeReplayHasNoScoreOrEvidenceSlot claimed exactly that case was covered. A field
// that is not on the wire is still settable in Go, which is enough to corrupt a replay in
// memory and enough to be read back by any in-process consumer.
func walkFields(rt reflect.Type, tags map[string]bool, goNames map[string]bool, seen map[reflect.Type]bool) {
	for rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Slice ||
		rt.Kind() == reflect.Array || rt.Kind() == reflect.Map {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct || seen[rt] {
		return
	}
	if rt == reflect.TypeOf(time.Time{}) {
		return
	}
	seen[rt] = true
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" {
			tag = f.Name // no tag → encoding/json uses the Go name, so that IS the key
		}
		goNames[rt.Name()+"."+f.Name] = true
		if tag == "-" {
			// Not on the wire, so it has no json key to allowlist — but it IS a field, so it
			// is already in goNames above, and its own fields still need walking.
			walkFields(f.Type, tags, goNames, seen)
			continue
		}
		tags[rt.Name()+"."+tag] = true
		walkFields(f.Type, tags, goNames, seen)
	}
}

func TestRegimeReplayCarriesExactlyTheKnownFields(t *testing.T) {
	tags := map[string]bool{}
	goNames := map[string]bool{}
	walkFields(reflect.TypeOf(RegimeReplay{}), tags, goNames, map[reflect.Type]bool{})

	allowed := map[string]bool{}
	for _, f := range replayFields {
		allowed[f] = true
		if !tags[f] {
			t.Errorf("RegimeReplay lost the %q field", f)
		}
	}
	for got := range tags {
		if !allowed[got] {
			t.Errorf("RegimeReplay reaches an undeclared %q field. A replay has no "+
				"multi-source evidence, so any new slot risks being filled with an invented "+
				"number; declare it in replayFields only after deciding it can be produced "+
				"honestly from .cache alone", got)
		}
	}
}

// The Go-field half of the allowlist. Not redundant with the test above: a field tagged
// `json:"-"` produces NO json key, so the tag allowlist is structurally incapable of seeing
// it — which is exactly how a `Score float64` slot got past this file once.
func TestRegimeReplayCarriesExactlyTheKnownGoFields(t *testing.T) {
	tags := map[string]bool{}
	goNames := map[string]bool{}
	walkFields(reflect.TypeOf(RegimeReplay{}), tags, goNames, map[reflect.Type]bool{})

	allowed := map[string]bool{}
	for _, f := range replayGoFields {
		allowed[f] = true
		if !goNames[f] {
			t.Errorf("RegimeReplay lost the Go field %q", f)
		}
	}
	for got := range goNames {
		if !allowed[got] {
			t.Errorf("RegimeReplay reaches an undeclared Go field %q. Being off the wire is "+
				"not a reason to skip review: it is still settable in Go and still readable "+
				"by any in-process consumer, so it can still carry an invented number. "+
				"Declare it in replayGoFields once you have decided it can be produced "+
				"honestly from .cache alone", got)
		}
	}
}

// The meta-test for walkFields itself.
//
// The two allowlist tests are only as good as the walk that feeds them, and the walk had a
// hole: `if tag == "-" { continue }` ran BEFORE goNames was filled, so a hidden field was
// absent from both maps and every assertion in this file passed. Rather than trusting the
// fix by inspection, this constructs the exact shape the mutation had and requires the walk
// to report it. If someone reintroduces the early `continue`, this fails and the message
// says what stopped working.
func TestWalkFieldsSeesFieldsHiddenFromJSON(t *testing.T) {
	type nested struct {
		Deep string `json:"deep"`
	}
	type hidden struct {
		Visible string  `json:"visible"`
		Score   float64 `json:"-"` // the mutation that survived
		Inner   nested  `json:"-"` // and its own fields must still be walked
	}

	tags := map[string]bool{}
	goNames := map[string]bool{}
	walkFields(reflect.TypeOf(hidden{}), tags, goNames, map[reflect.Type]bool{})

	if !goNames["hidden.Score"] {
		t.Error(`a field tagged json:"-" was invisible to walkFields. That is the hole that ` +
			`let a Score slot into RegimeReplay with the whole package green: the Go field ` +
			`is still settable and still readable in process, so it must be walked`)
	}
	if tags["hidden.-"] || tags["hidden.Score"] {
		t.Errorf(`a field tagged json:"-" must not contribute a json key: %v`, tags)
	}
	if !tags["hidden.visible"] || !goNames["hidden.Visible"] {
		t.Errorf("the ordinary field went missing: tags=%v goNames=%v", tags, goNames)
	}
	// Skipping the field must not skip its subtree either.
	if !tags["nested.deep"] || !goNames["nested.Deep"] {
		t.Errorf(`the subtree under a json:"-" field was not walked: tags=%v`, tags)
	}
}

// And the end-to-end version: the same hidden Score, checked through the forbidden-substring
// test, so BOTH guards are known to catch it.
func TestForbiddenSlotsAreCaughtEvenWhenHiddenFromJSON(t *testing.T) {
	type hidden struct {
		Score float64 `json:"-"`
	}
	tags := map[string]bool{}
	goNames := map[string]bool{}
	walkFields(reflect.TypeOf(hidden{}), tags, goNames, map[reflect.Type]bool{})

	found := false
	for name := range goNames {
		if strings.Contains(strings.ToLower(name), "score") {
			found = true
		}
	}
	if !found {
		t.Error(`the substring check cannot see a json:"-" Score field, so ` +
			"TestRegimeReplayHasNoScoreOrEvidenceSlot's own comment would be false")
	}
}

// The allowlist above is the real guard. This names the four specific slots the design
// argument turns on, so the failure message says WHY when someone adds one of them back —
// an allowlist violation alone would not explain it.
func TestRegimeReplayHasNoScoreOrEvidenceSlot(t *testing.T) {
	forbidden := []string{"score", "confidence", "evidence", "flags"}

	tags := map[string]bool{}
	goNames := map[string]bool{}
	walkFields(reflect.TypeOf(RegimeReplay{}), tags, goNames, map[reflect.Type]bool{})

	// Both the json keys and the Go field names: a field tagged `json:"-"` or renamed on the
	// wire would still be settable in Go, which is enough to corrupt a replay in memory.
	check := func(kind string, set map[string]bool) {
		for name := range set {
			short := strings.ToLower(name[strings.Index(name, ".")+1:])
			for _, bad := range forbidden {
				if strings.Contains(short, bad) {
					t.Errorf("RegimeReplay %s %q contains %q. A replay cannot produce a "+
						"score/confidence/evidence read: there is no historical producer for "+
						"institutional posture, futures OI, foreign cash flow or margin. A "+
						"zero here would persist as maximum bearishness on every replayed day",
						kind, name, bad)
				}
			}
		}
	}
	check("json key", tags)
	check("field", goNames)

	// And the marshalled form, because that is what a downstream reader actually sees.
	b, err := json.Marshal(sampleReplay("2026-08-31"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, bad := range forbidden {
		for k := range generic {
			if strings.Contains(strings.ToLower(k), bad) {
				t.Errorf("a marshalled replay carries top-level key %q containing %q", k, bad)
			}
		}
	}
	// The keys a snapshot reader would look for must be absent, not merely renamed.
	for _, k := range []string{"date", "regime", "schema_version", "score", "confidence"} {
		if _, ok := generic[k]; ok {
			t.Errorf("a marshalled replay carries the snapshot key %q — it could be read as "+
				"a live snapshot", k)
		}
	}
}

// ── fixtures ──────────────────────────────────────────────────────────────────────────

func sampleReplay(date string) *RegimeReplay {
	r := NewRegimeReplay(date, time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC))
	r.Regime = RegimeBullPullback
	r.RuleID = RulePullbackOrderly
	r.Structure = StructureView{
		Benchmark: Benchmark0050, Structure: StructureUptrend,
		Breadth: BreadthCooling, Posture: PostureUnknown,
		TrendIntact: true, OrderlyCorrection: true,
		Metrics: map[string]float64{MetricBreadthMA20: 47.5},
	}
	r.Reasons = []string{"結構 UPTREND，廣度 COOLING"}
	r.InputExtent = ReplayExtent{
		BenchmarkSymbol: string(Benchmark0050),
		BenchmarkBars:   400,
		BenchmarkFirst:  "2024-09-02",
		BenchmarkLast:   date,
		BreadthIndex:    380,
		BreadthFirst:    "2024-09-02",
		BreadthLast:     date,
		BreadthUniverse: 1780,

		BreadthSymbolsLoaded: 1974,
	}
	return r
}

// ── Validate ──────────────────────────────────────────────────────────────────────────

func TestRegimeReplayValidateAcceptsAHonestReplay(t *testing.T) {
	if err := sampleReplay("2026-08-31").Validate(); err != nil {
		t.Fatalf("a well-formed replay was rejected: %v", err)
	}
}

// The defining invariant: a replay that claims to know institutional posture is claiming to
// be something it cannot be.
func TestRegimeReplayValidateRejectsAKnownPosture(t *testing.T) {
	for _, p := range []InstitutionalPosture{PostureSupportive, PostureNeutral, PostureDeteriorating} {
		r := sampleReplay("2026-08-31")
		r.Structure.Posture = p
		err := r.Validate()
		if err == nil {
			t.Fatalf("posture %s was accepted on a replay — nothing reconstructs "+
				"institutional evidence retroactively", p)
		}
		if !strings.Contains(err.Error(), "UNKNOWN") {
			t.Errorf("posture %s rejected for the wrong reason: %v", p, err)
		}
	}
	// The one legal value still passes.
	r := sampleReplay("2026-08-31")
	r.Structure.Posture = PostureUnknown
	if err := r.Validate(); err != nil {
		t.Fatalf("PostureUnknown must be accepted: %v", err)
	}
}

func TestRegimeReplayValidateRejectsMalformedRecords(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*RegimeReplay)
		want string
	}{
		{"nil kind", func(r *RegimeReplay) { r.Kind = "" }, "kind"},
		{"snapshot kind", func(r *RegimeReplay) { r.Kind = "MARKET_SNAPSHOT" }, "kind"},
		{"no date", func(r *RegimeReplay) { r.Date = "" }, "replay_date"},
		{"bad date", func(r *RegimeReplay) { r.Date = "2026/08/31" }, "YYYY-MM-DD"},
		{"no schema", func(r *RegimeReplay) { r.SchemaVer = 0 }, "schema version"},
		{"no timestamp", func(r *RegimeReplay) { r.ReplayedAt = time.Time{} }, "replayed_at"},
		{"undefined regime", func(r *RegimeReplay) { r.Regime = "MELTUP" }, "undefined regime"},
		{"undefined structure", func(r *RegimeReplay) { r.Structure.Structure = "SIDEWAYS" }, "structural state"},
		{"undefined breadth", func(r *RegimeReplay) { r.Structure.Breadth = "OK" }, "structural state"},
		{"undefined benchmark", func(r *RegimeReplay) { r.Structure.Benchmark = "0056" }, "benchmark"},
		{"regime without rule", func(r *RegimeReplay) { r.RuleID = "" }, "no rule id"},
		{"caveat stripped", func(r *RegimeReplay) { r.Caveats = []string{"看起來還行"} }, CaveatReplayCode},
		{"no caveats at all", func(r *RegimeReplay) { r.Caveats = nil }, CaveatReplayCode},
		// The universe caveat is required exactly as strictly as the posture one. Keeping only
		// the posture caveat is the realistic mistake — it is the one that was written first,
		// and on the single day both archives cover it is posture that AGREES while breadth
		// differs by 3.91pp. So a row that discloses only posture is the misleading case, not
		// a partially-correct one.
		{"only the posture caveat", func(r *RegimeReplay) {
			r.Caveats = []string{CaveatReplayPostureUnknown}
		}, CaveatReplayUniverseCode},
		{"only the universe caveat", func(r *RegimeReplay) {
			r.Caveats = []string{CaveatReplayUniverseAsCached}
		}, CaveatReplayCode},
		{"no bars", func(r *RegimeReplay) { r.InputExtent.BenchmarkBars = 0 }, "no benchmark bars"},
		{"benchmark saw the future", func(r *RegimeReplay) {
			r.InputExtent.BenchmarkLast = "2026-09-05"
		}, "not point-in-time"},
		{"breadth saw the future", func(r *RegimeReplay) {
			r.InputExtent.BreadthLast = "2026-09-05"
		}, "not point-in-time"},
		{"negative breadth index", func(r *RegimeReplay) { r.InputExtent.BreadthIndex = -1 }, "breadth index"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := sampleReplay("2026-08-31")
			tc.mut(r)
			err := r.Validate()
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
	var nilReplay *RegimeReplay
	if err := nilReplay.Validate(); err == nil {
		t.Error("a nil replay must not validate")
	}
}

// UNKNOWN is a legal regime for a replayed day (warm-up, or a date the breadth axis reaches
// with too few measurable stocks) and must not need a rule id.
func TestRegimeReplayValidateAllowsUnknownRegime(t *testing.T) {
	r := NewRegimeReplay("2026-08-31", time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC))
	r.Structure.Benchmark = Benchmark0050
	r.RuleID = ""
	r.InputExtent = ReplayExtent{
		BenchmarkBars: 121, BenchmarkLast: "2026-08-31", BreadthLast: "2026-08-31",
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("an UNKNOWN replayed day must be storable: %v", err)
	}
}

// NewRegimeReplay must start in the UNKNOWN state with the caveat already attached — the
// zero value of Regime is "", a regime nobody defined.
func TestNewRegimeReplayStartsUnknownAndSelfDescribing(t *testing.T) {
	r := NewRegimeReplay("2026-08-31", time.Now())
	if r.Kind != ReplayKind {
		t.Errorf("Kind = %q, want %q", r.Kind, ReplayKind)
	}
	if r.Regime != RegimeUnknown || r.RuleID != RuleUnknown {
		t.Errorf("must start UNKNOWN/R0, got %s/%s", r.Regime, r.RuleID)
	}
	if r.Structure.Posture != PostureUnknown {
		t.Errorf("posture must start UNKNOWN, got %s", r.Structure.Posture)
	}
	if r.SchemaVer != ReplaySchemaVersion {
		t.Errorf("SchemaVer = %d, want %d", r.SchemaVer, ReplaySchemaVersion)
	}
	if !r.announcesItself() {
		t.Errorf("a new replay must already carry both mandatory caveats, got %v", r.Caveats)
	}
	for _, code := range []string{CaveatReplayCode, CaveatReplayUniverseCode} {
		if !r.carriesCaveat(code) {
			t.Errorf("a new replay must already carry the %s caveat, got %v", code, r.Caveats)
		}
	}
	if !strings.HasPrefix(CaveatReplayPostureUnknown, CaveatReplayCode) {
		t.Errorf("the posture caveat text must lead with the stable code")
	}
	if !strings.HasPrefix(CaveatReplayUniverseAsCached, CaveatReplayUniverseCode) {
		t.Errorf("the universe caveat text must lead with the stable code")
	}
	if CaveatReplayCode == CaveatReplayUniverseCode {
		t.Error("the two caveat codes must be distinguishable")
	}
}

// The universe caveat has to SAY the thing, not merely exist. Its whole reason for being is
// that the posture caveat made "replay ≠ live" sound like it was only about posture, so a
// reader who greps for the reason must find survivorship bias and breadth named explicitly.
func TestUniverseCaveatNamesTheBiasAndTheAffectedMetrics(t *testing.T) {
	for _, want := range []string{
		"REPLAYED_REGIME_UNIVERSE_AS_CACHED",
		"breadth",                // which metrics move
		"advancing_ratio",        // including this one, which moved most (+8.21pp)
		"survivorship",           // the name of the bias, so it is searchable
		"breadth_symbols_loaded", // where to look to see the membership actually used
	} {
		if !strings.Contains(CaveatReplayUniverseAsCached, want) {
			t.Errorf("the universe caveat never mentions %q: %s", want, CaveatReplayUniverseAsCached)
		}
	}
	// It must not read as a posture caveat: the two failure modes are independent and a
	// reader must be able to tell which one they are looking at.
	if strings.Contains(CaveatReplayUniverseAsCached, CaveatReplayCode) {
		t.Error("the universe caveat embeds the posture code — they must stay distinguishable")
	}
}

// The two archives version independently; coupling them would drag every snapshot reader
// into a replay-only change.
func TestReplaySchemaVersionIsIndependent(t *testing.T) {
	if ReplaySchemaVersion <= 0 {
		t.Fatalf("ReplaySchemaVersion = %d", ReplaySchemaVersion)
	}
	// Sanity, not a rule: they may hold the same number, they just must be separate consts.
	if reflect.ValueOf(ReplaySchemaVersion).Kind() != reflect.Int {
		t.Error("ReplaySchemaVersion must be an int constant")
	}
}
