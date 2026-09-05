package fetcher

import (
	"fmt"
	"os"
	"path/filepath"
)

// Choosing a cache directory, and refusing one that is reserved.
//
// This package WRITES what it fetches: FetchSectorStocks → fetchYahoo → dataCache.Set →
// os.WriteFile. Which directory a Fetcher was built with is therefore a CORRECTNESS property,
// not a performance one.
//
// The concrete hazard: a tool pointed at the scan's own cache_dir, run during trading hours,
// writes that morning's HALF-FORMED daily bar into .cache/2330_TW.json with a fresh
// timestamp. A scan started inside cache_ttl_min then reads it back as the last completed
// session, and every moving average, RSI and ATR built on it is wrong — arriving in a BUY.
// That is not hypothetical; it happened, and the file's timestamp proved it.
//
// The guard lives here rather than in each caller because a second copy is a second thing to
// keep in step, and the first version of it was already wrong in two ways a copy would have
// duplicated: it compared strings only (so a case variant on APFS walked straight through),
// and it treated a blank configured directory as "no collision possible" when a blank one
// resolves to exactly the directory being protected.

// CacheChoice is a resolved cache decision: where to write, and what it was checked against.
type CacheChoice struct {
	Dir string
	// Reserved is the directory the choice was refused from — reported so a caller can say
	// which one it is protecting rather than restating a literal.
	Reserved string
}

// ResolveCacheDir picks a cache directory and refuses the reserved one.
//
// Both defaults are applied HERE rather than by the caller, because both are load-bearing:
//
//	requested == ""   the caller's own fallback, never this package's default
//	reserved  == ""   DefaultCacheDir — a config that omits cache_dir resolves to it, so
//	                  treating blank as "nothing to protect" reopens the hole for exactly
//	                  the configurations most likely to hit it
func ResolveCacheDir(requested, reserved, fallback string) (CacheChoice, error) {
	if requested == "" {
		requested = fallback
	}
	if requested == "" {
		return CacheChoice{}, fmt.Errorf("fetcher: no cache directory and no fallback given")
	}
	if reserved == "" {
		reserved = DefaultCacheDir
	}
	if SameDir(requested, reserved) {
		return CacheChoice{}, fmt.Errorf(
			"cache directory %q is the reserved one (%q).\n"+
				"internal/fetcher writes what it fetches, so sharing this directory would let "+
				"an intraday run put a half-formed daily bar into the series the next scan "+
				"reads. Choose a different directory.", requested, reserved)
	}
	return CacheChoice{Dir: requested, Reserved: reserved}, nil
}

// SameDir reports whether two paths name the same directory.
//
// Three tests, widest last:
//
//	text     cleaned then made absolute — ".cache", "./.cache", "foo/../.cache"
//	link     EvalSymlinks — a symlink pointing at the other directory
//	identity os.SameFile on the stat results — everything the string comparison cannot see
//
// The identity check is the one that matters. APFS is case-insensitive by default, so on
// macOS ".CACHE" opens the same directory as ".cache" while comparing unequal as text, and
// EvalSymlinks does not help — it succeeds for both spellings and returns each unchanged.
// os.SameFile compares device and inode, closing that along with hard links and bind mounts.
//
// A path that does not exist yet cannot be stat'd, which is normal on a first run; the string
// tests still apply, and a directory that does not exist cannot be the one being written to.
func SameDir(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	aa, errA := filepath.Abs(ca)
	ab, errB := filepath.Abs(cb)
	if errA == nil && errB == nil {
		if aa == ab {
			return true
		}
		if ra, err1 := filepath.EvalSymlinks(aa); err1 == nil {
			if rb, err2 := filepath.EvalSymlinks(ab); err2 == nil && ra == rb {
				return true
			}
		}
	}
	sa, err1 := os.Stat(ca)
	sb, err2 := os.Stat(cb)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}
