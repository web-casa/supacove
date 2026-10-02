package limiter

import (
	"fmt"
	"testing"
	"time"
)

func TestLimiterLockoutFlow(t *testing.T) {
	l := New()
	key := "1.2.3.4"
	for i := 0; i < lockoutAfter-1; i++ {
		if !l.Allow(key) {
			t.Fatalf("attempt %d must be allowed", i)
		}
		l.Fail(key)
	}
	if l.Allow(key) {
		l.Fail(key)
	} else {
		t.Fatal("5th attempt should still be allowed")
	}
	// After 5 consecutive failures the lockout is active — even good credentials.
	if l.Allow(key) {
		t.Fatal("locked-out key must be rejected")
	}
	l.Reset(key)
	if !l.Allow(key) {
		t.Fatal("Reset must clear the lockout")
	}
}

// TestLimiterCapacityDoesNotDropNewAttempt pins the review P2-03 fix: a table
// at capacity must not drop (and thereby untrack) a newly created entry.
func TestLimiterCapacityDoesNotDropNewAttempt(t *testing.T) {
	l := New()
	// Fill the table to its hard limit with recently-seen keys.
	for i := 0; i < maxKeys; i++ {
		k := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		if !l.Allow(k) {
			t.Fatalf("seed key %d must be allowed", i)
		}
	}
	// One more key must still be tracked: its per-window quota is enforced.
	fresh := "9.9.9.9"
	for i := 0; i < maxPerWindow; i++ {
		if !l.Allow(fresh) {
			t.Fatalf("fresh key attempt %d must be allowed", i)
		}
	}
	if l.Allow(fresh) {
		t.Fatal("fresh key over its window quota must be rejected (must stay tracked at capacity)")
	}
}

func TestLimiterWindowRollover(t *testing.T) {
	l := New()
	now := time.Now()
	l.now = func() time.Time { return now }
	key := "k"
	for i := 0; i < maxPerWindow; i++ {
		if !l.Allow(key) {
			t.Fatalf("attempt %d must be allowed", i)
		}
	}
	if l.Allow(key) {
		t.Fatal("over-window attempt must be rejected")
	}
	now = now.Add(window + time.Second) // window rolls over
	if !l.Allow(key) {
		t.Fatal("after the window rolls, the quota resets")
	}
}

func TestLimiterLockoutExpiry(t *testing.T) {
	l := New()
	now := time.Now()
	l.now = func() time.Time { return now }
	key := "k"
	for i := 0; i < lockoutAfter; i++ {
		if !l.Allow(key) {
			t.Fatalf("attempt %d must be allowed", i)
		}
		l.Fail(key)
	}
	if l.Allow(key) {
		t.Fatal("locked out")
	}
	now = now.Add(lockoutPeriod + time.Second)
	if !l.Allow(key) {
		t.Fatal("lockout must expire")
	}
}
