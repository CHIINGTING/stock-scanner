package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// TAIFEX historical backfill — the ONLY place Big5 exists in this codebase.
//
// The daily JSON feed has no date parameter, so the 252-day history the futures analyzer
// needs for its crowding percentile cannot come from it. The CSV download does accept a date
// range (verified), but it is Big5-encoded, which is why golang.org/x/text appears in
// go.mod at all.
//
// Containment is deliberate and load-bearing:
//   - decoding happens here and nowhere else;
//   - the output is the same model.FuturesOIData the JSON path produces, so nothing
//     downstream can tell which source a day came from;
//   - no analyzer, engine, or model file imports x/text.
//
// Once a backfill has run, the archive carries the history and this path is not needed again
// until someone wants to extend the window further back.

const taifexBackfillURL = "https://www.taifex.com.tw/cht/3/futContractsDateDown"

// BackfillDay is one historical session as recovered from the CSV.
type BackfillDay struct {
	Date    string // YYYY-MM-DD
	Futures *model.FuturesOIData
}

// TAIFEXBackfill downloads a date range of institutional futures positions.
type TAIFEXBackfill struct {
	Client  *http.Client
	BaseURL string // test override
}

func NewTAIFEXBackfill(timeout time.Duration) *TAIFEXBackfill {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &TAIFEXBackfill{Client: &http.Client{Timeout: timeout}}
}

// Range fetches [from, to] inclusive (both YYYY-MM-DD) and returns one entry per session
// that carried a 臺股期貨 / 外資及陸資 row, ascending by date.
func (b *TAIFEXBackfill) Range(ctx context.Context, from, to string) ([]BackfillDay, error) {
	fromT, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, fmt.Errorf("market: bad from date %q: %w", from, err)
	}
	toT, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, fmt.Errorf("market: bad to date %q: %w", to, err)
	}
	if toT.Before(fromT) {
		return nil, fmt.Errorf("market: backfill range ends before it starts (%s → %s)", from, to)
	}

	endpoint := b.BaseURL
	if endpoint == "" {
		endpoint = taifexBackfillURL
	}
	form := url.Values{}
	form.Set("queryStartDate", fromT.Format("2006/01/02"))
	form.Set("queryEndDate", toT.Format("2006/01/02"))
	form.Set("commodityId", "TXF")

	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; stock-scanner/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST %s: HTTP %d", endpoint, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	return ParseTAIFEXBackfillCSV(raw)
}

// ParseTAIFEXBackfillCSV decodes the Big5 CSV and extracts the foreign 臺股期貨 rows.
//
// Exported so tests drive it from a committed Big5 fixture — the encoding handling is the
// risky part and it must be tested without the network.
func ParseTAIFEXBackfillCSV(raw []byte) ([]BackfillDay, error) {
	text, err := decodeBig5(raw)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("TAIFEX backfill: response has no rows")
	}

	// Column positions of the 15-field contract (verified against the live download).
	const (
		colDate     = 0
		colContract = 1
		colItem     = 2
		colLongOI   = 9
		colShortOI  = 11
		colNetOI    = 13
		colCount    = 15
	)

	byDate := map[string]*model.FuturesOIData{}
	var order []string
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) < colCount {
			continue
		}
		if strings.TrimSpace(f[colContract]) != contractTXF {
			continue
		}
		d, err := time.Parse("2006/01/02", strings.TrimSpace(f[colDate]))
		if err != nil {
			continue
		}
		iso := d.Format("2006-01-02")
		item := strings.TrimSpace(f[colItem])
		net, err := parseOI(f[colNetOI])
		if err != nil {
			return nil, fmt.Errorf("TAIFEX backfill %s %s: net OI: %w", iso, item, err)
		}
		cur, ok := byDate[iso]
		if !ok {
			cur = &model.FuturesOIData{Contract: contractTXF, Item: itemForeign, OtherItems: map[string]float64{}}
			byDate[iso] = cur
			order = append(order, iso)
		}
		if item != itemForeign {
			cur.OtherItems[item] = net
			continue
		}
		long, err1 := parseOI(f[colLongOI])
		short, err2 := parseOI(f[colShortOI])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("TAIFEX backfill %s: 外資 long/short unparsable", iso)
		}
		cur.LongOI, cur.ShortOI, cur.NetOI = long, short, net
	}

	out := make([]BackfillDay, 0, len(order))
	for _, iso := range order {
		f := byDate[iso]
		// A day whose foreign row never appeared carries no usable position; skip it rather
		// than emit a zero that would read as "flat".
		if f.LongOI == 0 && f.ShortOI == 0 && f.NetOI == 0 {
			continue
		}
		out = append(out, BackfillDay{Date: iso, Futures: f})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("TAIFEX backfill: no %s/%s rows found — layout changed", contractTXF, itemForeign)
	}
	sortBackfill(out)
	return out, nil
}

func sortBackfill(days []BackfillDay) {
	for i := 1; i < len(days); i++ {
		for j := i; j > 0 && days[j].Date < days[j-1].Date; j-- {
			days[j], days[j-1] = days[j-1], days[j]
		}
	}
}

// decodeBig5 converts the CSV bytes to UTF-8. This function is the containment boundary:
// x/text is imported here and in no other file of the feature.
func decodeBig5(raw []byte) (string, error) {
	r := transform.NewReader(bytes.NewReader(raw), traditionalchinese.Big5.NewDecoder())
	out, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("TAIFEX backfill: Big5 decode: %w", err)
	}
	return string(out), nil
}
