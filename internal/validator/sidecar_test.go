package validator

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/analysishistory"
)

func writeSidecar(t *testing.T, dir, date string, stocks ...analysishistory.StockData) {
	t.Helper()
	if _, err := analysishistory.Write(dir, analysishistory.Snapshot{Date: date, Stocks: stocks}); err != nil {
		t.Fatalf("write sidecar %s: %v", date, err)
	}
}

func TestSidecarLoadAndRecord(t *testing.T) {
	dir := t.TempDir()
	b20 := 17.8
	writeSidecar(t, dir, "2026-07-15",
		analysishistory.StockData{Code: "2330", Action: "BUY", Score: 82, Bias: &analysishistory.BiasPayload{Bias20: &b20, Risk: "HIGH"}},
	)
	sc := NewSidecarStore(dir)
	d := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	snap, ok := sc.Load(d)
	if !ok || len(snap.Stocks) != 1 {
		t.Fatalf("load failed: ok=%v snap=%+v", ok, snap)
	}
	rec, ok := sc.Record(d, "2330")
	if !ok || rec.Action != "BUY" || rec.Bias.Risk != "HIGH" {
		t.Fatalf("record wrong: ok=%v rec=%+v", ok, rec)
	}
	if _, ok := sc.Record(d, "9999"); ok {
		t.Errorf("unknown code should be absent")
	}
}

func TestSidecarMissing(t *testing.T) {
	sc := NewSidecarStore(t.TempDir())
	if _, ok := sc.Load(time.Now()); ok {
		t.Errorf("missing sidecar must be absent, not fatal")
	}
}
