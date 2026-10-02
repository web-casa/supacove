package db

import (
	"errors"
	"testing"
)

func TestAdvisoryLockBlocksSecondInstance(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	defer s1.Close()

	s2, err := Open(dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second open: want ErrLocked, got %v", err)
	}
	if s2 != nil {
		s2.Close()
	}

	// After the first instance closes, the OS releases the lock: reopen works.
	s1.Close()
	s3, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	s3.Close()
}
