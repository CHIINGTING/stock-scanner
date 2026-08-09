package scanner

import (
	"math"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func fptr(v float64) *float64 { return &v }

func heatAt(score float64) *SectorHeatView {
	return &SectorHeatView{
		Status: SectorHeatOK, Heat: score,
		Breadth: score, Participation: score, VolumeConfirmation: score,
		ValidMemberCount: 8, MemberCount: 8,
	}
}

func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// ── State machine ────────────────────────────────────────────────────────────────────

func TestTrendStateTable(t *testing.T) {
	cases := []struct {
		name string
		in   trendExtInput
		want TrendExtState
	}{
		{
			// Trend up on both averages, location normal, group behind it.
			name: "trend confirmed",
			in: trendExtInput{
				MA20Slope: fptr(0.30), MA60Slope: fptr(0.10),
				Bias20: fptr(6), BiasPct: fptr(55), Heat: heatAt(75),
			},
			want: TrendConfirmed,
		},
		{
			// A technical pullback inside an uptrend. Descriptive only — validation did not
			// support it as a superior entry (see docs/R12_VALIDATION_RESULTS.md §2).
			name: "pullback in uptrend",
			in: trendExtInput{
				MA20Slope: fptr(0.25), MA60Slope: fptr(0.12),
				Bias20: fptr(1.2), BiasPct: fptr(45), Heat: heatAt(60),
			},
			want: PullbackInUptrend,
		},
		{
			// Stretched for THIS stock, in a hot sector — the do-not-chase cell. It must
			// beat TREND_CONFIRMED, which every other condition here would also satisfy.
			name: "extended beats confirmation",
			in: trendExtInput{
				MA20Slope: fptr(0.9), MA60Slope: fptr(0.4),
				Bias20: fptr(18), BiasPct: fptr(97), Heat: heatAt(90),
			},
			want: TrendExtended,
		},
		{
			name: "sector divergence",
			in: trendExtInput{
				MA20Slope: fptr(0.35), MA60Slope: fptr(0.2),
				Bias20: fptr(8), BiasPct: fptr(60), Heat: heatAt(20),
			},
			want: SectorDivergence,
		},
		{
			// Group leads, MA60 has not turned yet.
			name: "sector confirmed",
			in: trendExtInput{
				MA20Slope: fptr(0.35), MA60Slope: fptr(-0.5),
				Bias20: fptr(7), BiasPct: fptr(60), Heat: heatAt(80),
			},
			want: SectorConfirmed,
		},
		{
			name: "weakening",
			in: trendExtInput{
				MA20Slope: fptr(-0.30), MA60Slope: fptr(-0.15),
				Bias20: fptr(-6), BiasPct: fptr(10), Heat: heatAt(15),
			},
			want: TrendWeakening,
		},
		{
			name: "flat slope is neutral",
			in: trendExtInput{
				MA20Slope: fptr(0.01), MA60Slope: fptr(0),
				Bias20: fptr(0.5), BiasPct: fptr(50), Heat: heatAt(50),
			},
			want: TrendExtNeutral,
		},
		{
			// A bounce inside a falling MA20 is NOT weakness-confirmed (price is above the
			// average) and NOT an uptrend cell either.
			name: "rebound inside downtrend",
			in: trendExtInput{
				MA20Slope: fptr(-0.30), MA60Slope: fptr(-0.2),
				Bias20: fptr(4), BiasPct: fptr(60), Heat: heatAt(30),
			},
			want: TrendExtNeutral,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeTrendExtension(tc.in); got.State != tc.want {
				t.Errorf("state = %s, want %s", got.State, tc.want)
			}
		})
	}
}

// EXTENDED must not read as a stronger buy — the hotter and steeper it gets, the more firmly
// it stays EXTENDED rather than escalating into a confirmation.
func TestExtendedNeverBecomesConfirmation(t *testing.T) {
	for _, heat := range []float64{40, 65, 85, 100} {
		for _, slope := range []float64{0.2, 1.0, 3.0} {
			in := trendExtInput{
				MA20Slope: fptr(slope), MA60Slope: fptr(slope / 2),
				Bias20: fptr(25), BiasPct: fptr(99), Heat: heatAt(heat),
			}
			if got := computeTrendExtension(in).State; got != TrendExtended {
				t.Errorf("heat=%v slope=%v → %s, want EXTENDED", heat, slope, got)
			}
		}
	}
}

func TestTrendInsufficientData(t *testing.T) {
	cases := map[string]trendExtInput{
		"no slope": {Bias20: fptr(3), Heat: heatAt(60)},
		"no bias":  {MA20Slope: fptr(0.3), Heat: heatAt(60)},
		"neither":  {Heat: heatAt(60)},
	}
	for name, in := range cases {
		v := computeTrendExtension(in)
		if v.State != TrendExtInsufficient {
			t.Errorf("%s: state = %s, want INSUFFICIENT_DATA", name, v.State)
		}
		if v.Computed() {
			t.Errorf("%s: Computed() must be false", name)
		}
	}
}

// A missing MA60 is a young listing, not a bearish signal — the cells that need long-trend
// agreement must simply not fire, and the stock must still get a usable state.
func TestMissingMA60IsNotBearish(t *testing.T) {
	in := trendExtInput{MA20Slope: fptr(0.3), Bias20: fptr(1), BiasPct: fptr(40), Heat: heatAt(80)}
	v := computeTrendExtension(in)
	if v.State == TrendWeakening {
		t.Error("a missing MA60 must never produce WEAKENING")
	}
	if !v.Computed() {
		t.Error("a missing MA60 must not make the whole reading unavailable")
	}
	if hasCode(v.Codes, CodeMA60SlopeUp) || hasCode(v.Codes, CodeMA60SlopeDown) {
		t.Errorf("a missing MA60 must emit no MA60 code, got %v", v.Codes)
	}
}

// An UNKNOWN sector is not a weak sector. Missing data must never become divergence.
func TestUnknownSectorIsNotDivergence(t *testing.T) {
	for name, heat := range map[string]*SectorHeatView{
		"no sector": nil,
		"thin":      {Status: SectorHeatInsufficientData, ValidMemberCount: 2, MemberCount: 3},
	} {
		in := trendExtInput{
			MA20Slope: fptr(0.35), MA60Slope: fptr(0.2),
			Bias20: fptr(7), BiasPct: fptr(60), Heat: heat,
		}
		v := computeTrendExtension(in)
		if v.State == SectorDivergence {
			t.Errorf("%s: unknown sector must not be reported as divergence", name)
		}
		if !hasCode(v.Codes, CodeSectorHeatUnknown) {
			t.Errorf("%s: expected %s in %v", name, CodeSectorHeatUnknown, v.Codes)
		}
	}
}

// ── Extension test ───────────────────────────────────────────────────────────────────

// Extension is judged against the stock's OWN history, so the same raw BIAS is extended for
// a quiet name and normal for a volatile one.
func TestIsExtendedUsesPercentileFirst(t *testing.T) {
	quiet := trendExtInput{Bias20: fptr(8), BiasPct: fptr(96)}
	volatile := trendExtInput{Bias20: fptr(8), BiasPct: fptr(40)}
	if !isExtended(quiet) {
		t.Error("+8% in the 96th percentile of its own history is extended")
	}
	if isExtended(volatile) {
		t.Error("+8% in the 40th percentile is ordinary for that stock")
	}
}

// Without a percentile the fallback is the EXISTING R10-1 label — not a fresh set of numbers,
// which would make this the fifth definition of "extended" in the repo.
func TestIsExtendedFallsBackToExistingBiasRisk(t *testing.T) {
	for _, risk := range []string{BiasRiskHigh, BiasRiskExtreme} {
		if !isExtended(trendExtInput{Bias20: fptr(16), BiasRisk: risk}) {
			t.Errorf("risk %s should fall back to extended", risk)
		}
	}
	for _, risk := range []string{BiasRiskNormal, BiasRiskElevated, BiasRiskNA, ""} {
		if isExtended(trendExtInput{Bias20: fptr(11), BiasRisk: risk}) {
			t.Errorf("risk %q must not be treated as extended", risk)
		}
	}
}

// ── Evidence codes ───────────────────────────────────────────────────────────────────

func TestTrendEvidenceCodes(t *testing.T) {
	v := computeTrendExtension(trendExtInput{
		MA20Slope: fptr(0.4), MA60Slope: fptr(0.2),
		Bias20: fptr(0.8), BiasPct: fptr(50), Heat: heatAt(80),
	})
	for _, want := range []string{CodeMA20SlopeUp, CodeMA60SlopeUp, CodeSlopeAligned, CodeBias20NearMA} {
		if !hasCode(v.Codes, want) {
			t.Errorf("missing %s in %v", want, v.Codes)
		}
	}

	down := computeTrendExtension(trendExtInput{
		MA20Slope: fptr(-0.4), MA60Slope: fptr(-0.2),
		Bias20: fptr(3.5), BiasPct: fptr(60), Heat: heatAt(30),
	})
	if !hasCode(down.Codes, CodeReboundInDowntrend) {
		t.Errorf("price above a falling MA20 must emit %s, got %v", CodeReboundInDowntrend, down.Codes)
	}
	if !hasCode(down.Codes, CodeMA20SlopeDown) {
		t.Errorf("missing %s in %v", CodeMA20SlopeDown, down.Codes)
	}
}

// Sector codes are produced once, by the sector layer, and carried through — not recomputed
// per stock with a second set of thresholds.
func TestTrendCarriesSectorCodes(t *testing.T) {
	h := heatAt(80)
	h.Codes = []string{CodeSectorHeatStrong, CodeSectorVolumeConfirm}
	v := computeTrendExtension(trendExtInput{
		MA20Slope: fptr(0.4), MA60Slope: fptr(0.2), Bias20: fptr(6), BiasPct: fptr(50), Heat: h,
	})
	for _, want := range h.Codes {
		if !hasCode(v.Codes, want) {
			t.Errorf("sector code %s was dropped: %v", want, v.Codes)
		}
	}
}

// ── Derivation from candles ──────────────────────────────────────────────────────────

func trendCandles(n int, drift float64) []fetcher.Candle {
	out := make([]fetcher.Candle, n)
	price := 100.0
	for i := range out {
		price *= 1 + drift
		out[i] = fetcher.Candle{
			Open: price, High: price * 1.01, Low: price * 0.99, Close: price, Volume: 1_000_000,
		}
	}
	return out
}

func TestBuildTrendExtInputFromCandles(t *testing.T) {
	in := buildTrendExtInput(trendCandles(400, 0.004), BiasRiskNormal, "半導體", heatAt(70))

	if in.MA20Slope == nil || in.MA60Slope == nil {
		t.Fatal("both slopes should compute from 400 bars")
	}
	if *in.MA20Slope <= 0 || *in.MA60Slope <= 0 {
		t.Errorf("a rising series must give positive slopes: %v %v", *in.MA20Slope, *in.MA60Slope)
	}
	if in.Bias20 == nil || in.Bias60 == nil || in.BiasPct == nil {
		t.Fatal("BIAS and its percentile should compute from 400 bars")
	}
	// BIAS60 must exceed BIAS20 in a steady uptrend: the slower average lags further behind.
	if *in.Bias60 <= *in.Bias20 {
		t.Errorf("BIAS60 (%v) should exceed BIAS20 (%v) in a steady uptrend", *in.Bias60, *in.Bias20)
	}
	if in.Sector != "半導體" || in.Heat == nil {
		t.Error("sector context was not carried through")
	}
}

// Short history must degrade field by field, never fabricate.
func TestBuildTrendExtInputShortHistory(t *testing.T) {
	in := buildTrendExtInput(trendCandles(30, 0.003), "", "", nil)
	if in.MA20Slope == nil {
		t.Error("30 bars is enough for an MA20 slope")
	}
	if in.MA60Slope != nil {
		t.Errorf("30 bars cannot yield an MA60 slope, got %v", *in.MA60Slope)
	}
	if in.Bias60 != nil {
		t.Errorf("30 bars cannot yield BIAS60, got %v", *in.Bias60)
	}
	if in.BiasPct != nil {
		t.Errorf("30 bars cannot yield a 252-day percentile, got %v", *in.BiasPct)
	}

	// No candles at all: everything unavailable, and the state says so.
	empty := buildTrendExtInput(nil, "", "", nil)
	if empty.MA20Slope != nil || empty.Bias20 != nil {
		t.Error("no candles must produce no values")
	}
	if computeTrendExtension(empty).State != TrendExtInsufficient {
		t.Error("no candles must yield INSUFFICIENT_DATA")
	}
}

// A degenerate series (all zeros) must not crash or produce NaN/Inf.
func TestBuildTrendExtInputZeroPrices(t *testing.T) {
	zeros := make([]fetcher.Candle, 300)
	in := buildTrendExtInput(zeros, "", "", nil)
	for name, p := range map[string]*float64{
		"MA20Slope": in.MA20Slope, "MA60Slope": in.MA60Slope,
		"Bias20": in.Bias20, "Bias60": in.Bias60, "BiasPct": in.BiasPct,
	} {
		if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0)) {
			t.Errorf("%s = %v — a zero MA must be unavailable, not NaN/Inf", name, *p)
		}
	}
	if computeTrendExtension(in).State != TrendExtInsufficient {
		t.Error("a zero-price series must be INSUFFICIENT_DATA")
	}
}

// Every state must have a distinct, non-empty name — the report renders these verbatim.
func TestTrendStateNamesAreDistinct(t *testing.T) {
	states := []TrendExtState{
		TrendConfirmed, PullbackInUptrend, TrendExtended, SectorConfirmed,
		SectorDivergence, TrendWeakening, TrendExtNeutral, TrendExtInsufficient,
	}
	seen := map[TrendExtState]bool{}
	for _, s := range states {
		if s == "" {
			t.Error("empty state constant")
		}
		if seen[s] {
			t.Errorf("duplicate state %s", s)
		}
		seen[s] = true
		if strings.ToUpper(string(s)) != string(s) {
			t.Errorf("state %s should be an upper-case code", s)
		}
	}
}
