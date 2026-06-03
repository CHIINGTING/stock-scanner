package fetcher

import "testing"

func TestUniqueInfosDedup(t *testing.T) {
	sl := &SectorList{
		Sectors: []SectorDef{
			{Name: "PCB", Stocks: []SectorStockEntry{
				{Code: "3037", Name: "欣興"},
				{Code: "8046", Name: "南電"},
			}},
			{Name: "ABF", Stocks: []SectorStockEntry{
				{Code: "3037", Name: "欣興"}, // duplicate across sectors
				{Code: "3189", Name: "景碩"},
			}},
			{Name: "Empty", Stocks: []SectorStockEntry{
				{Code: "", Name: "no code"}, // skipped
			}},
		},
	}

	infos := sl.UniqueInfos()
	if len(infos) != 3 {
		t.Fatalf("expected 3 unique infos, got %d: %+v", len(infos), infos)
	}
	seen := map[string]bool{}
	for _, in := range infos {
		if seen[in.Symbol] {
			t.Errorf("duplicate symbol %q in UniqueInfos", in.Symbol)
		}
		seen[in.Symbol] = true
	}
	for _, want := range []string{"3037", "8046", "3189"} {
		if !seen[want] {
			t.Errorf("missing expected code %q", want)
		}
	}
}
