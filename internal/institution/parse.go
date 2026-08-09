package institution

import (
	"strconv"
	"strings"
)

// parseShares parses one source cell (comma-thousands string, optional leading '-',
// literal "0" for a real zero) into 股. ok=false when the cell is empty or non-numeric —
// live verification found ZERO empty cells on either source, so ok=false marks a
// genuinely malformed row, never a legitimate zero (which parses fine as 0, true).
func parseShares(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.ReplaceAll(s, ",", "")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ptr returns a pointer to v (for the optional Buy/Sell legs).
func ptr(v int64) *int64 { return &v }

// isFourDigitCode is a BASIC SYNTAX PREFILTER ONLY — NOT an ordinary-stock
// classification (§24). A 4-digit numeric code is not necessarily a common stock
// (e.g. 9103 = TDR passes this). Authoritative ordinary-stock membership is decided at
// attach time by intersecting with the scanner's existing universe. This prefilter only
// keeps the snapshot lean by dropping obvious non-4-digit securities (ETF/ETN/權證/
// 債券ETF/TDR with 5–6 digit or alpha-suffixed codes).
func isFourDigitCode(code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 4 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// reconciled reports whether all three §7 reconciliation checks pass on this record:
//  1. ForeignTotal.Net == ForeignExclDealer.Net + ForeignDealer.Net
//  2. DealerTotal.Net  == DealerSelf.Net + DealerHedge.Net
//  3. ForeignTotal.Net + Trust.Net + DealerTotal.Net == OfficialTotalNet (when present)
//
// On TPEx (positional parsing) checks 1–2 also validate the column mapping is intact.
func (c StockChip) reconciled() bool {
	if c.ForeignTotal.Net != c.ForeignExclDealer.Net+c.ForeignDealer.Net {
		return false
	}
	if c.DealerTotal.Net != c.DealerSelf.Net+c.DealerHedge.Net {
		return false
	}
	if c.OfficialTotalNet != nil {
		if c.ForeignTotal.Net+c.Trust.Net+c.DealerTotal.Net != *c.OfficialTotalNet {
			return false
		}
	}
	return true
}
