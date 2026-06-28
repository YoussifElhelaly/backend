package proxy

import (
	"os"
	"strings"
	"sync/atomic"
)

// Pool holds a list of proxy URLs and dishes them out round-robin.
// Loaded once at startup from PROXY_POOL env var (comma-separated).
// Empty pool = no auto-assignment; manual per-session proxy_url still works.
var pool []string
var counter atomic.Uint64

func init() {
	raw := os.Getenv("PROXY_POOL")
	if raw == "" {
		return
	}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			pool = append(pool, p)
		}
	}
}

// Next returns the next proxy URL from the pool (round-robin).
// Returns "" if the pool is empty.
func Next() string {
	if len(pool) == 0 {
		return ""
	}
	idx := counter.Add(1) - 1
	return pool[idx%uint64(len(pool))]
}

// Len returns the number of proxies in the pool.
func Len() int { return len(pool) }
