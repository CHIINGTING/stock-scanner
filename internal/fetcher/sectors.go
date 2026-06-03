package fetcher

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// SectorStockEntry is one member stock of a sector in sectors.yaml.
type SectorStockEntry struct {
	Code   string `yaml:"code"`
	Name   string `yaml:"name"`
	Market string `yaml:"market"` // "TW" | "TWO" | "" (auto-detect)
}

// SectorDef is one sector (族群) and its member stocks.
type SectorDef struct {
	Name   string             `yaml:"name"`
	Stocks []SectorStockEntry `yaml:"stocks"`
}

// SectorList is the top-level structure of sectors.yaml.
type SectorList struct {
	Sectors []SectorDef `yaml:"sectors"`
}

// LoadSectorList reads and parses sectors.yaml.
func LoadSectorList(path string) (*SectorList, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var sl SectorList
	if err := yaml.Unmarshal(data, &sl); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &sl, nil
}

// UniqueInfos returns the de-duplicated set of member stocks across all sectors.
// A single stock may belong to multiple sectors; it is fetched only once.
func (sl *SectorList) UniqueInfos() []StockInfo {
	seen := make(map[string]bool)
	var infos []StockInfo
	for _, sec := range sl.Sectors {
		for _, st := range sec.Stocks {
			if st.Code == "" || seen[st.Code] {
				continue
			}
			seen[st.Code] = true
			infos = append(infos, StockInfo{Symbol: st.Code, Name: st.Name, Market: st.Market})
		}
	}
	return infos
}

// FetchSectorStocks downloads OHLCV data for the given (de-duplicated) member stocks.
func (f *Fetcher) FetchSectorStocks(infos []StockInfo) ([]StockData, error) {
	return f.fetchBatch(infos, "sector")
}
