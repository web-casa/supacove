package agekey

import (
	"errors"
	"strings"
	"testing"
)

// countingWrite records every Write; failingWrite fails from its Nth write
// on. age streams header/payload chunks during io.Copy and performs its
// FINAL flush inside Close — so failing exactly the last write isolates
// "is the Close error honored?" from copy-path failures.
type countingWrite struct{ n int }

func (c *countingWrite) Write(p []byte) (int, error) { c.n++; return len(p), nil }

type failingWrite struct {
	failAt int
	n      int
}

var errInjectedFlush = errors.New("injected disk full on final flush")

func (f *failingWrite) Write(p []byte) (int, error) {
	f.n++
	if f.n >= f.failAt {
		return 0, errInjectedFlush
	}
	return len(p), nil
}

// Mutation acceptance (quality plan): ignoring aw.Close() must turn this
// test red — the final flush's error is the only signal that the archive's
// last chunk never landed.
func TestEncryptStreamSurfacesCloseFailure(t *testing.T) {
	_, recipient, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("payload-data", 8)

	var probe countingWrite
	if err := EncryptStream(recipient, strings.NewReader(payload), &probe); err != nil {
		t.Fatal(err)
	}
	if probe.n < 2 {
		t.Fatalf("unexpected write sequence length %d", probe.n)
	}

	w := &failingWrite{failAt: probe.n} // the final write is Close's flush
	err = EncryptStream(recipient, strings.NewReader(payload), w)
	if !errors.Is(err, errInjectedFlush) {
		t.Fatalf("Close-flush failure not surfaced: err = %v (writes reached %d of %d)",
			err, w.n, probe.n)
	}
}
