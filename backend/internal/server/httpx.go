package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cloudfan/supabackup/backend/internal/api"
)

func fsSub(fsys fs.FS, dir string) (fs.FS, error) { return fs.Sub(fsys, dir) }

func fsStat(fsys fs.FS, name string) (fs.FileInfo, error) { return fs.Stat(fsys, name) }

func errorsAs(err error, target any) bool { return errors.As(err, target) }

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.Error{Code: code, Message: msg})
}

// originParts strictly parses an Origin header value into
// (scheme, host-lowercased, effective port). Anything that is not exactly
// scheme://host[:port] is rejected: no userinfo, path, query, fragment, empty
// port, or multi-value headers (review P1-01).
func originParts(origin string) (scheme, host, port string, err error) {
	if origin == "" || origin == "null" || strings.Contains(origin, ",") {
		return "", "", "", errors.New("empty, null, or repeated origin")
	}
	u, err := url.Parse(origin)
	if err != nil {
		return "", "", "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", "", "", errors.New("origin must be scheme://host[:port]")
	}
	h, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		// No port given: use the scheme default.
		h, p = u.Host, ""
	}
	h = strings.Trim(h, "[]")
	h = strings.ToLower(h)
	if ip := net.ParseIP(h); ip != nil {
		h = ip.String() // canonicalize IPv6
	} else if h == "" {
		return "", "", "", errors.New("empty origin host")
	}
	return strings.ToLower(u.Scheme), h, effectivePort(strings.ToLower(u.Scheme), h, p), nil
}

func effectivePort(scheme, host, port string) string {
	if port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return port // invalid port cannot match anything
		}
		return port
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// requestScheme reports the scheme of the direct connection. Proxied
// deployments must set SB_PUBLIC_ORIGIN rather than trusting
// X-Forwarded-Proto, which clients can also send (review P1-01).
func requestScheme(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

func writeEmpty(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}
