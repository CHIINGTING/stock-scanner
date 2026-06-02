package fetcher

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dataCache provides two-tier (memory + file) TTL caching for OHLCV stock data.
//
// Key design decisions:
//   - Memory cache is checked first (O(1), no I/O).
//   - File cache persists across runs so a second invocation within the TTL
//     window (e.g., run-fast after run) reuses already-fetched data.
//   - File writes happen in a goroutine; failures are logged, not fatal.
type dataCache struct {
	dir string
	ttl time.Duration

	mu  sync.RWMutex
	mem map[string]cacheRecord // key → record
}

type cacheRecord struct {
	FetchedAt time.Time `json:"fetched_at"`
	Data      StockData `json:"data"`
}

func newDataCache(dir string, ttl time.Duration) *dataCache {
	if dir == "" {
		dir = ".cache"
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("cache: cannot create dir %s: %v", dir, err)
	}
	return &dataCache{dir: dir, ttl: ttl, mem: make(map[string]cacheRecord)}
}

// cacheKey converts a ticker like "2330.TW" to a safe filename stem.
func cacheKey(ticker string) string {
	return strings.ReplaceAll(ticker, ".", "_")
}

func (c *dataCache) filePath(ticker string) string {
	return filepath.Join(c.dir, cacheKey(ticker)+".json")
}

// Get returns cached StockData if fresh (within TTL). Checks memory first, then file.
func (c *dataCache) Get(ticker string) (*StockData, bool) {
	// ── memory ──────────────────────────────────────────────────────────────
	c.mu.RLock()
	rec, ok := c.mem[ticker]
	c.mu.RUnlock()

	if ok {
		if time.Since(rec.FetchedAt) < c.ttl {
			d := rec.Data
			return &d, true
		}
		// stale in memory – fall through to file check
	}

	// ── file ────────────────────────────────────────────────────────────────
	b, err := os.ReadFile(c.filePath(ticker))
	if err != nil {
		return nil, false
	}
	var r cacheRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, false
	}
	if time.Since(r.FetchedAt) >= c.ttl {
		return nil, false // stale on disk
	}

	// promote to memory
	c.mu.Lock()
	c.mem[ticker] = r
	c.mu.Unlock()

	return &r.Data, true
}

// Set stores data in memory and asynchronously writes to disk.
func (c *dataCache) Set(ticker string, data StockData) {
	rec := cacheRecord{FetchedAt: time.Now(), Data: data}

	c.mu.Lock()
	c.mem[ticker] = rec
	c.mu.Unlock()

	go func() {
		b, err := json.Marshal(rec)
		if err != nil {
			return
		}
		if err := os.WriteFile(c.filePath(ticker), b, 0o644); err != nil {
			log.Printf("cache: write %s: %v", ticker, err)
		}
	}()
}

// ──────────────────────────────────────────────────────────────────────────────
// EOF / connection-error cooldown
//
// After a network failure (EOF, connection reset, etc.) we refuse to contact
// the same ticker for EOFCooldownMin minutes. This prevents hammering Yahoo
// Finance with requests that will fail anyway, and avoids triggering further
// rate-limiting.
// ──────────────────────────────────────────────────────────────────────────────

// eofCooldownStore wraps a sync.Map for per-ticker cooldown tracking.
type eofCooldownStore struct {
	m sync.Map // ticker → time.Time (do not retry until)
}

func (s *eofCooldownStore) IsActive(ticker string) (bool, time.Time) {
	v, ok := s.m.Load(ticker)
	if !ok {
		return false, time.Time{}
	}
	until := v.(time.Time)
	if time.Now().Before(until) {
		return true, until
	}
	// expired — clean up
	s.m.Delete(ticker)
	return false, time.Time{}
}

func (s *eofCooldownStore) Set(ticker string, dur time.Duration) {
	until := time.Now().Add(dur)
	s.m.Store(ticker, until)
	log.Printf("cooldown: %s locked for %.0fs (until %s)", ticker, dur.Seconds(), until.Format("15:04:05"))
}
