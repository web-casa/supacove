package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	metricNameRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelPairRe  = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"$`)
)

// The hand-written exposition text must satisfy the Prometheus format on
// every line — a malformed scrape silently drops families in real
// deployments (quality plan: /metrics goes through a real parser; this is
// the in-repo parser gate, the docker promtool target is the external one).
// splitLabelPairs splits a label set on commas that are OUTSIDE quoted
// values (a label value may legally contain an escaped comma).
func splitLabelPairs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && inQuotes:
			cur.WriteRune(r)
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// parseSample splits "name[{labels}] value" by hand: a label value may
// legally contain braces, quotes, commas and spaces once escaped, so no
// single regex is trustworthy here (review Q8).
func parseSample(line string) (name, labels, value string, ok bool) {
	// value is the final whitespace-separated token; our emitter never
	// writes timestamps.
	sp := strings.LastIndex(line, " ")
	if sp <= 0 {
		return "", "", "", false
	}
	value = line[sp+1:]
	head := line[:sp]
	if i := strings.IndexByte(head, '{'); i >= 0 {
		if !strings.HasSuffix(head, "}") {
			return "", "", "", false
		}
		name = head[:i]
		labels = head[i+1 : len(head)-1]
	} else {
		name = head
	}
	if name == "" || !metricNameRe.MatchString(name) {
		return "", "", "", false
	}
	return name, labels, value, true
}

func TestMetricsExpositionFormat(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	// A database name with quote/backslash/newline chars locks label-value
	// escaping: raw values here would break the exposition grammar AND leak
	// unescaped admin input into every scraper.
	var oddDB int64
	if err := env.store.DB.QueryRow(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		 VALUES ('we"ird\name' || char(10) || 'x', 'generic', '', 'x', 0, 0) RETURNING id`).Scan(&oddDB); err != nil {
		t.Fatal(err)
	}
	// The database label only appears once a SUCCESSFUL job exists for it:
	// without one the fixture never reaches the exposition text.
	if _, err := env.store.DB.Exec(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at, finished_at)
		VALUES (?, 'succeeded', 0, 0, 1, 2)`, oddDB); err != nil {
		t.Fatal(err)
	}

	resp, err := env.client.Get(env.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d", resp.StatusCode)
	}
	bodyBytes, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		t.Fatalf("read scrape: %v", rerr)
	}
	body := string(bodyBytes)

	// Optional capture for the external promtool target.
	if path := os.Getenv("SB_METRICS_DUMP"); path != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	families := map[string]string{} // metric -> TYPE
	helps := map[string]bool{}      // HELP seen once per metric
	seen := map[string]bool{}       // name{labels} samples, dup detection
	var lastFamily string

	for i, line := range strings.Split(body, "\n") {
		if line == "" {
			continue
		}
		where := fmt.Sprintf("line %d %q", i+1, line)
		switch {
		case strings.HasPrefix(line, "# HELP "):
			fields := strings.SplitN(line, " ", 4)
			if len(fields) < 3 || !metricNameRe.MatchString(fields[2]) {
				t.Fatalf("%s: bad HELP", where)
			}
			if helps[fields[2]] {
				t.Fatalf("%s: duplicate HELP", where)
			}
			helps[fields[2]] = true
			lastFamily = fields[2]
		case strings.HasPrefix(line, "# TYPE "):
			fields := strings.SplitN(line, " ", 4)
			if len(fields) != 4 {
				t.Fatalf("%s: bad TYPE", where)
			}
			switch fields[3] {
			case "gauge", "counter", "summary", "histogram", "untyped":
			default:
				t.Fatalf("%s: unknown type %q", where, fields[3])
			}
			if families[fields[2]] != "" {
				t.Fatalf("%s: duplicate TYPE", where)
			}
			families[fields[2]] = fields[3]
			lastFamily = fields[2]
		case strings.HasPrefix(line, "#"):
			t.Fatalf("%s: unknown comment kind", where)
		default:
			name, labels, value, ok := parseSample(line)
			if !ok {
				t.Fatalf("%s: unparsable sample line", where)
			}
			if _, ferr := strconv.ParseFloat(value, 64); ferr != nil {
				t.Fatalf("%s: value %q not a float", where, value)
			}
			if name != lastFamily {
				t.Fatalf("%s: sample outside its HELP/TYPE family (want %s)", where, lastFamily)
			}
			if labels != "" {
				for _, pair := range splitLabelPairs(labels) {
					lm := labelPairRe.FindStringSubmatch(pair)
					if lm == nil {
						t.Fatalf("%s: bad label pair %q", where, pair)
					}
					// Escaped value must round-trip: an unescaped raw quote
					// or newline inside the value is a grammar violation.
					v := lm[2]
					// The capture grammar only admits escaped forms; verify
					// they round-trip to a coherent value and that no escape
					// sequence is malformed.
					if strings.Contains(v, "\\") {
						unescaped := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n").Replace(v)
						if unescaped == v {
							t.Fatalf("%s: backslash without escape sequence", where)
						}
					}
				}
			}
			key := name + "{" + labels + "}"
			if seen[key] {
				t.Fatalf("%s: duplicate sample %s", where, key)
			}
			seen[key] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("no samples scraped")
	}
	// The odd database name MUST appear as a label value — without this the
	// escaping assertions above would be vacuous (review Q8).
	found := false
	for key := range seen {
		if strings.HasPrefix(key, "supabackup_last_success_timestamp{") && strings.Contains(key, `\"`) {
			found = true
		}
	}
	if !found {
		t.Error("the odd database-name label never reached the exposition text")
	}
	// Every family carries both HELP and TYPE.
	for name := range families {
		if !helps[name] {
			t.Errorf("family %s has TYPE but no HELP", name)
		}
	}
}
