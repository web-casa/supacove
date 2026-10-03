package pgclient

import (
	"context"
	"errors"
	"strings"
	"syscall"
)

// Classified attaches an error class to an underlying failure.
type Classified struct {
	Class ErrClass
	Err   error
}

func (e *Classified) Error() string { return e.Err.Error() }
func (e *Classified) Unwrap() error { return e.Err }

// ClassifyError maps a connection/query failure to an error class using
// structured errors first (syscall, context), then libpq/pgx message
// patterns (round-1 review P2-05).
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return &Classified{Class: ClassUnknown, Err: err}
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return &Classified{Class: ClassDisk, Err: err}
	}

	msg := strings.ToLower(err.Error())
	cls := ClassUnknown
	switch {
	case strings.Contains(msg, "password authentication failed"),
		strings.Contains(msg, "no password supplied"),
		strings.Contains(msg, "authentication failed"),
		strings.Contains(msg, "28p01"):
		cls = ClassAuth
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "connection timed out"),
		strings.Contains(msg, "network unreachable"),
		strings.Contains(msg, "host is unreachable"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "dial tcp"),
		strings.Contains(msg, "no route to host"):
		cls = ClassNetwork
	case strings.Contains(msg, "permission denied"),
		strings.Contains(msg, "42501"),
		strings.Contains(msg, "privilege"):
		cls = ClassPermission
	case strings.Contains(msg, "no such file or directory") && strings.Contains(msg, ".sock"):
		cls = ClassNetwork
	case strings.Contains(msg, "unsupported ssl"),
		strings.Contains(msg, "ssl is not enabled"),
		strings.Contains(msg, "certificate verify failed"),
		strings.Contains(msg, "tlsv1"):
		cls = ClassNetwork
	}
	if cls == ClassUnknown {
		return err
	}
	return &Classified{Class: cls, Err: err}
}
