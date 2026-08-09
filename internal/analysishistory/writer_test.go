package analysishistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ratio := 12.5
	b20 := 17.8
	snap := Snapshot{
		Date: "2026-07-15",
		Stocks: []StockData{{
			Code: "2330", Action: "BUY", Score: 82,
			Institution: &InstitutionPayload{
				Foreign: LegPayload{Today: -3200, Sum5d: -8400, ConsecutiveSellDays: 3, Transition: "BUY_TO_SELL", NetBuyRatio: &ratio, StreakComplete: true, DataComplete: true},
			},
			Bias: &BiasPayload{Bias20: &b20, Risk: "HIGH"},
		}},
	}
	path, err := Write(dir, snap)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if path != filepath.Join(dir, "2026-07-15.json") {
		t.Errorf("path = %s", path)
	}
	b, _ := os.ReadFile(path)
	var got Snapshot
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("schema version not stamped: %d", got.SchemaVersion)
	}
	if len(got.Stocks) != 1 || got.Stocks[0].Code != "2330" {
		t.Fatalf("stocks mismatch: %+v", got.Stocks)
	}
	f := got.Stocks[0].Institution.Foreign
	if f.NetBuyRatio == nil || *f.NetBuyRatio != 12.5 {
		t.Errorf("ratio not preserved: %v", f.NetBuyRatio)
	}
	if got.Stocks[0].Bias.Bias20 == nil || *got.Stocks[0].Bias.Bias20 != 17.8 {
		t.Errorf("bias20 not preserved")
	}
}

func TestWriteMissingDate(t *testing.T) {
	if _, err := Write(t.TempDir(), Snapshot{}); err == nil {
		t.Fatal("expected error for missing date")
	}
}
