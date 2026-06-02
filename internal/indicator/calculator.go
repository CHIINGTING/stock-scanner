package indicator

import "github.com/deep-huang/stock-scanner/internal/fetcher"

// Config holds indicator calculation parameters.
type Config struct {
	KDJKPeriod      int     `yaml:"k_period"`
	KDJDSmooth      int     `yaml:"d_smooth"`
	KDJJSmooth      int     `yaml:"j_smooth"`
	BollingerPeriod int     `yaml:"period"`
	BollingerStdDev float64 `yaml:"std_dev"`
}

// Result holds all computed indicator time series for a single stock.
type Result struct {
	MA20     []float64
	VolumeMA []float64
	KDJ      KDJResult
	BB       BollingerResult
	RSI      []float64
	ATR      []float64
}

// Calculate computes all indicators from a stock's candle history.
func Calculate(candles []fetcher.Candle, cfg Config) Result {
	n := len(candles)
	closes := make([]float64, n)
	highs := make([]float64, n)
	lows := make([]float64, n)
	volumes := make([]int64, n)

	for i, c := range candles {
		closes[i] = c.Close
		highs[i] = c.High
		lows[i] = c.Low
		volumes[i] = c.Volume
	}

	kPeriod := cfg.KDJKPeriod
	if kPeriod == 0 {
		kPeriod = 9
	}
	dSmooth := cfg.KDJDSmooth
	if dSmooth == 0 {
		dSmooth = 3
	}
	jSmooth := cfg.KDJJSmooth
	if jSmooth == 0 {
		jSmooth = 3
	}
	bbPeriod := cfg.BollingerPeriod
	if bbPeriod == 0 {
		bbPeriod = 20
	}
	bbStd := cfg.BollingerStdDev
	if bbStd == 0 {
		bbStd = 2.0
	}

	return Result{
		MA20:     SMA(closes, 20),
		VolumeMA: SMA(VolumesToFloat64(volumes), 20),
		KDJ:      KDJ(highs, lows, closes, kPeriod, dSmooth, jSmooth),
		BB:       Bollinger(closes, bbPeriod, bbStd),
		RSI:      RSI(closes, 14),
		ATR:      ATR(highs, lows, closes, 14),
	}
}
