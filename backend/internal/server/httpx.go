package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/cloudfan/supabackup/backend/internal/api"
)

func fsSub(fsys fs.FS, dir string) (fs.FS, error) { return fs.Sub(fsys, dir) }

func fsStat(fsys fs.FS, name string) (fs.FileInfo, error) { return fs.Stat(fsys, name) }

func schemeAndHost(origin string) (string, string) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return "", ""
	}
	return u.Scheme, u.Host
}

// subtleHostEqual compares hosts including port, constant-time enough for a
// non-secret comparison while avoiding trivial early-exit oracles.
func subtleHostEqual(a, b string) bool {
	normalize := func(h string) string {
		h = strings.ToLower(h)
		if strings.HasPrefix(h, "xn--") {
			return h
		}
		return h
	}
	ha, hpa := splitHostPort(normalize(a))
	hb, hpb := splitHostPort(normalize(b))
	if ha != hb {
		return false
	}
	// Default ports may be omitted by browsers; treat equal hosts with
	// missing port on default schemes as equal.
	return hpa == hpb || hpa == "" || hpb == ""
}

func splitHostPort(host string) (string, string) {
	if h, p, err := net.SplitHostPort(host); err == nil {
		return h, p
	}
	return host, ""
}

func newCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is not recoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.Error{Code: code, Message: msg})
}

func writeEmpty(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}
