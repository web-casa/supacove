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
	maxKeys       = 10_000
)

// Limiter is safe for concurrent use.
type Limiter struct {
	mu       sync.Mutex
	attempts map[string]*attempt
	now      func() time.Time
}

type attempt struct {
	windowStart time.Time
	lastSeen    time.Time
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
	a.lastSeen = now
	return true
}

// Fail records a failed attempt; returns true if this failure triggered a lockout.
func (l *Limiter) Fail(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(key)
	a.lastSeen = l.now()
	a.failures++
	if a.failures >= lockoutAfter {
		a.lockedUntil = a.lastSeen.Add(lockoutPeriod)
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

// get returns the entry for key, creating one if capacity allows. Cleanup
// runs BEFORE insertion (review P2-03: a just-created entry with a zero time
// must never be its own cleanup victim), and a full table recycles its oldest
// slot so accounting stays bounded instead of silently dropping a new key.
func (l *Limiter) get(key string) *attempt {
	now := l.now()
	if a, ok := l.attempts[key]; ok {
		return a
	}
	if len(l.attempts) >= maxKeys {
		l.evict(now)
		if len(l.attempts) >= maxKeys {
			l.evictOldest()
		}
	}
	a := &attempt{windowStart: now, lastSeen: now}
	l.attempts[key] = a
	return a
}

// evict drops entries idle for ten windows. Called with the lock held.
func (l *Limiter) evict(now time.Time) {
	for k, v := range l.attempts {
		if now.Sub(v.lastSeen) > 10*window && now.After(v.lockedUntil) {
			delete(l.attempts, k)
		}
	}
}

// evictOldest unconditionally recycles the least recently used slot.
func (l *Limiter) evictOldest() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, v := range l.attempts {
		if first || v.lastSeen.Before(oldest) {
			oldestKey, oldest, first = k, v.lastSeen, false
		}
	}
	if !first {
		delete(l.attempts, oldestKey)
	}
}
