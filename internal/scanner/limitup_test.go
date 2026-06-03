package scanner

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

func bar(open, high, low, close float64, vol int64) fetcher.Candle {
	return fetcher.Candle{
		Date: time.Now(), Open: open, High: high, Low: low, Close: close, Volume: vol,
	}
}

func TestDetectLimitStatus(t *testing.T) {
	tests := []struct {
		name     string
		prevPrev fetcher.Candle // candles[n-3] (only used by distribution case)
		prev     fetcher.Candle // candles[n-2]
		today    fetcher.Candle // candles[n-1]
		volRatio float64
		want     string
	}{
		{
			name:     "locked limit-up low volume → not penalized",
			prev:     bar(100, 100, 100, 100, 5000),
			today:    bar(110, 110, 109, 110, 800), // +10%, closed at high, volume shrank
			volRatio: 0.4,
			want:     LimitLockedLowVol,
		},
		{
			name:     "一字漲停 (open=high=low=close) tiny volume",
			prev:     bar(50, 50, 50, 50, 8000),
			today:    bar(55, 55, 55, 55, 300), // +10% locked flat, minimal turnover
			volRatio: 0.2,
			want:     LimitLockedLowVol,
		},
		{
			name:     "limit-up opened then sold off on big volume → failed",
			prev:     bar(100, 100, 100, 100, 5000),
			today:    bar(108, 110, 102, 103, 30000), // touched +10% high, closed far below high, heavy volume
			volRatio: 3.0,
			want:     LimitUpFailed,
		},
		{
			name:     "distribution: prev locked limit-up, today down on volume",
			prevPrev: bar(100, 100, 100, 100, 5000),
			prev:     bar(110, 110, 110, 110, 600), // prev day locked +10%
			today:    bar(109, 109, 100, 101, 25000), // today down, big volume
			volRatio: 2.5,
			want:     LimitDistribution,
		},
		{
			name:     "normal up day with volume → no special status",
			prev:     bar(100, 100, 100, 100, 5000),
			today:    bar(101, 104, 100, 103, 6000), // +3%, not near limit
			volRatio: 1.2,
			want:     "",
		},
		{
			name:     "strong up day +9% but volume NOT shrunk → not locked-low-vol",
			prev:     bar(100, 100, 100, 100, 5000),
			today:    bar(105, 109, 104, 109, 9000), // +9%, closed at high, but volRatio high
			volRatio: 1.8,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candles := []fetcher.Candle{tt.prev, tt.today}
			if tt.prevPrev.Close > 0 {
				candles = []fetcher.Candle{tt.prevPrev, tt.prev, tt.today}
			}
			got, note := detectLimitStatus(candles, tt.volRatio)
			if got != tt.want {
				t.Errorf("detectLimitStatus = %q, want %q", got, tt.want)
			}
			if got != "" && note == "" {
				t.Errorf("status %q returned without an interpretation note", got)
			}
		})
	}
}

// TestLockedLimitUpNotPenalized verifies the volume engine does NOT mark a
// locked limit-up as weak: volume checkpoint passes and volume score is positive.
func TestLockedLimitUpNotPenalized(t *testing.T) {
	closes := []float64{100, 110}
	vols := []float64{5000, 800}
	ind := indicator.Result{VolumeMA: []float64{2000, 2000}} // ratio 800/2000 = 0.4 (量縮)

	normal := analyzeVolume(closes, vols, ind, "", "")
	locked := analyzeVolume(closes, vols, ind, LimitLockedLowVol, "🔒 test")

	if locked.score <= normal.score {
		t.Errorf("locked limit-up should not be penalized: locked=%d normal=%d", locked.score, normal.score)
	}
	if locked.signal != "漲停鎖量" {
		t.Errorf("expected 漲停鎖量 signal, got %q", locked.signal)
	}
}
