// Package limiter provides a small in-memory fixed-window rate limiter for
// login attempts (dev-plan P1-06: login rate limiting without Redis).
package limiter

import (
	"sync"
	"time"
)

const (
	window        = time.Minute
	maxPerWindow  = 10
	lockoutAfter  = 5 // consecutive failures
	lockoutPeriod = time.Minute
)

// Limiter is safe for concurrent use.
type Limiter struct {
	mu       sync.Mutex
	attempts map[string]*attempt
	now      func() time.Time
}

type attempt struct {
	windowStart time.Time
	count       int
	failures    int
	lockedUntil time.Time
}

func New() *Limiter {
	return &Limiter{attempts: make(map[string]*attempt), now: time.Now}
}

// Allow reports whether a login attempt for key (usually client IP) may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(key)
	now := l.now()
	if now.Before(a.lockedUntil) {
		return false
	}
	if now.Sub(a.windowStart) >= window {
		a.windowStart = now
		a.count = 0
	}
	if a.count >= maxPerWindow {
		return false
	}
	a.count++
	return true
}

// Fail records a failed attempt; returns true if this failure triggered a lockout.
func (l *Limiter) Fail(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(key)
	a.failures++
	if a.failures >= lockoutAfter {
		a.lockedUntil = l.now().Add(lockoutPeriod)
		a.failures = 0
		return true
	}
	return false
}

// Reset clears failure state after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a, ok := l.attempts[key]; ok {
		a.failures = 0
		a.lockedUntil = time.Time{}
	}
}

func (l *Limiter) get(key string) *attempt {
	a, ok := l.attempts[key]
	if !ok {
		a = &attempt{}
		l.attempts[key] = a
	}
	// Opportunistic cleanup to bound memory.
	if len(l.attempts) > 10_000 {
		now := l.now()
		for k, v := range l.attempts {
			if now.Sub(v.windowStart) > 10*window && now.After(v.lockedUntil) {
				delete(l.attempts, k)
			}
		}
	}
	return a
}
