// Package ratelimit is a small fixed-window limiter for independent edge
// protections (payment callbacks, OIDC token exchange, product token issuance).
package ratelimit

import (
	"sync"
	"time"
)

type entry struct {
	start time.Time
	count int
}

type Limiter struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	items  map[string]entry
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, items: make(map[string]entry)}
}

// Allow reports whether key may perform another action. The returned duration
// is the wait before the current window resets (zero when allowed).
func (l *Limiter) Allow(key string, now time.Time) (bool, time.Duration) {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.items[key]
	if !ok || now.Sub(e.start) >= l.window {
		l.items[key] = entry{start: now, count: 1}
		if len(l.items) > 10000 {
			for k, v := range l.items {
				if now.Sub(v.start) >= l.window {
					delete(l.items, k)
				}
			}
		}
		return true, 0
	}
	if e.count >= l.limit {
		return false, l.window - now.Sub(e.start)
	}
	e.count++
	l.items[key] = e
	return true, 0
}
