package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Splitting one response into one response per trading session.
//
// This is the transformation §10.2b forces on M3 and it is not a convenience. options_oi_by_strike
// is keyed (snapshot_id, product, expiry_code, strike, call_put) with NO session column — the
// session lives on the SNAPSHOT — so a response carrying both sessions has to become two
// snapshots or it cannot be stored at all.
//
// The three plausible shortcuts each destroy data, and all three are silent:
//
//	one COMBINED snapshot         → 126 of the fixture's 277 rows collide on the UNIQUE index.
//	                                With INSERT OR IGNORE that is 126 rows lost and a success
//	                                message; the 2,868 night rows of the live response likewise.
//	sum the two sessions           → §3.1's volume rule needs them separately, and OI must NOT
//	                                be summed the same way volume is
//	keep only 一般                  → volume comes out at 214,161 against the official 356,219
//
// So the response is partitioned before anything else looks at it, and each partition is
// parsed, hashed, archived and stored as an independent observation.

// SessionPart is one session's slice of a response.
//
// Body is a JSON array of the ORIGINAL element bytes, in the original order. It is not
// re-serialised from a decoded form: the partition is what gets hashed into content_hash and
// stored as the snapshot payload, and re-encoding would make the hash a property of Go's
// marshaller rather than of what the exchange said.
type SessionPart struct {
	Session string // DAY | AFTER_HOURS
	Body    []byte
	Rows    int
}

// SplitBySession partitions a response by its own TradingSession column.
//
// By the feed's field, never by the clock at fetch time — the exchange's convention is the
// opposite of the intuitive one (the 盤後 rows printed in day D's report were traded the
// EVENING BEFORE D), and reading the field means R15 never has to reason about which calendar
// day a night session spans. Both partitions therefore carry the SAME trading_date: D. The
// night rows are not backdated, and doing so would put them in a snapshot no reader of D
// would look at.
//
// Structural failures are ErrStructure and produce no partitions at all, which is the point
// of routing every response through rawRows first: an HTML error page or an empty body must
// reach the caller as ERROR, never as "two sessions with nothing in them".
//
// A row whose TradingSession is unrecognised is a REJECTED row (§6.2), returned and counted
// rather than dropped or guessed into a session: that column decides which snapshot the row
// belongs to, so an unrecognised value leaves the row with no place to be stored.
//
// Ordering is deterministic — DAY before AFTER_HOURS — so a content hash does not depend on
// map iteration.
func SplitBySession(source string, body []byte) ([]SessionPart, []RejectedRow, error) {
	rows, err := rawRows(source, body, []string{"TradingSession"})
	if err != nil {
		return nil, nil, err
	}
	// Decoded a second time, as raw elements, so each row's ORIGINAL bytes survive into its
	// partition. rawRows' map[string]string is what classifies; this is what is stored.
	var elems []json.RawMessage
	if err := json.Unmarshal(body, &elems); err != nil {
		return nil, nil, fmt.Errorf("%w: %s: undecodable envelope: %v", ErrStructure, source, err)
	}
	if len(elems) != len(rows) {
		return nil, nil, fmt.Errorf("%w: %s: %d elements but %d rows decoded",
			ErrStructure, source, len(elems), len(rows))
	}

	var rejected []RejectedRow
	buckets := map[string][]json.RawMessage{}
	for i, r := range rows {
		session, err := NormalizeSession(r["TradingSession"])
		if err != nil {
			rejected = append(rejected, RejectedRow{
				Index: i, Field: "TradingSession", Token: r["TradingSession"],
				Reason: err.Error(),
			})
			continue
		}
		buckets[session] = append(buckets[session], elems[i])
	}

	var out []SessionPart
	for _, session := range []string{SessionDay, SessionAfterHours} {
		part := buckets[session]
		if len(part) == 0 {
			// Not an error and not an empty snapshot. A response can legitimately carry one
			// session — the night rows are published later — and the caller records the
			// absent one as NOT_PUBLISHED, which is a different claim from "we stored zero
			// rows for it".
			continue
		}
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range part {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(e)
		}
		buf.WriteByte(']')
		out = append(out, SessionPart{Session: session, Body: buf.Bytes(), Rows: len(part)})
	}
	return out, rejected, nil
}
