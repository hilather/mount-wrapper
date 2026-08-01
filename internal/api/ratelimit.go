package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultDestructiveMinInterval is the minimum gap between successful admits
// for the same client key on a destructive HTTP action (purge, unmount-all,
// rescan flood protection).
const DefaultDestructiveMinInterval = 2 * time.Second

// actionLimiter is a simple per-key min-interval gate for destructive API POSTs.
// It is not a full token bucket; one admit resets the wait for that key+action.
type actionLimiter struct {
	mu       sync.Mutex
	minGap   time.Duration
	last     map[string]time.Time
	now      func() time.Time
	maxKeys  int
}

func newActionLimiter(minGap time.Duration) *actionLimiter {
	if minGap <= 0 {
		minGap = DefaultDestructiveMinInterval
	}
	return &actionLimiter{
		minGap:  minGap,
		last:    make(map[string]time.Time),
		now:     time.Now,
		maxKeys: 4096,
	}
}

// allow reports whether the action may proceed for key. On allow, records now.
func (l *actionLimiter) allow(key string) bool {
	if l == nil {
		return true
	}
	if key == "" {
		key = "unknown"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if prev, ok := l.last[key]; ok {
		if now.Sub(prev) < l.minGap {
			return false
		}
	}
	// Bound map growth (simple drop-all when huge; rare under loopback UI).
	if len(l.last) >= l.maxKeys {
		l.last = make(map[string]time.Time, 64)
	}
	l.last[key] = now
	return true
}

// clientKey derives a rate-limit key from the request (RemoteAddr host).
func clientKey(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "unknown"
	}
	return host
}

func writeRateLimited(w http.ResponseWriter) {
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error": "rate limited; retry after a short delay",
		"code":  "RATE_LIMITED",
	})
}
