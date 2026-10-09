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
	// Only \, \" and \n are legal escapes in the Prometheus text format;
	// anything else (Go %q's \t, \r, \xNN) must fail the gate.
	labelPairRe = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\\n]|\\["\\n])*)"$`)
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
		 VALUES ('we"ird\name' || char(10) || char(9) || 'x', 'generic', '', 'x', 0, 0) RETURNING id`).Scan(&oddDB); err != nil {
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
	// The odd database name MUST appear AND decode back to the exact stored
	// string — without the equality check, escaped-but-wrong values pass
	// (GLM review round 5: the old check only looked for \" to exist).
	const oddName = "we\"ird\\name\n\tx"
	found := false
	for key := range seen {
		if !strings.HasPrefix(key, "supacove_last_success_timestamp{") {
			continue
		}
		labels := strings.TrimSuffix(strings.TrimPrefix(key, "supacove_last_success_timestamp{"), "}")
		v := labelPairRe.FindStringSubmatch(labels)
		if v == nil {
			continue
		}
		decoded := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n").Replace(v[2])
		if decoded == oddName {
			found = true
		}
	}
	if !found {
		t.Errorf("the odd database name never round-tripped through the exposition (want %q)", oddName)
	}
	// Every family carries both HELP and TYPE.
	for name := range families {
		if !helps[name] {
			t.Errorf("family %s has TYPE but no HELP", name)
		}
	}
}

// scrapeSamples fetches /metrics and returns the parsed samples
// (name{labels} -> value) and HELP lines (name -> line).
func scrapeSamples(t *testing.T, env *testEnv) (map[string]string, map[string]string) {
	t.Helper()
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
	samples := map[string]string{}
	helps := map[string]string{}
	for _, line := range strings.Split(string(bodyBytes), "\n") {
		if strings.HasPrefix(line, "# HELP ") {
			fields := strings.SplitN(line, " ", 4)
			if len(fields) >= 3 {
				helps[fields[2]] = line
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, value, ok := parseSample(line)
		if !ok {
			t.Fatalf("unparsable sample line %q", line)
		}
		samples[name+"{"+labels+"}"] = value
	}
	return samples, helps
}

// TestMetricRenameAliasParity pins the supabackup->supacove rename
// transition guarantee with REAL data in every family: each supacove_
// sample has a same-valued supabackup_ twin and vice versa (except the two
// pre-rename shortcut gauges, which stay legacy-only), every legacy family
// announces its deprecation, and the shortcut gauges get no twins.
func TestMetricRenameAliasParity(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	// Seed every SQL-backed family: two databases (one backed up
	// successfully, one never), one failed job, so jobs / verification /
	// last_success_timestamp / databases_protection all carry samples.
	seed := func(name string) int64 {
		var id int64
		if err := env.store.DB.QueryRow(
			`INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
			 VALUES (?, 'generic', '', 'x', 0, 0) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	baked, fresh := seed("parity-backed"), seed("parity-fresh")
	for _, q := range []string{
		`INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at, finished_at)
		 VALUES (?, 'succeeded', 0, 0, 1, 2)`,
		`INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at, finished_at)
		 VALUES (?, 'failed', 0, 0, 1, 2)`,
	} {
		if _, err := env.store.DB.Exec(q, baked); err != nil {
			t.Fatal(err)
		}
	}
	_ = fresh

	samples, helps := scrapeSamples(t, env)

	mustFamilies := []string{
		"supacove_uptime_seconds", "supacove_go_goroutines", "supacove_heap_alloc_bytes",
		"supacove_jobs", "supacove_remote_commits", "supacove_remote_upload_failures",
		"supacove_verification", "supacove_last_success_timestamp", "supacove_staging_bytes",
		"supacove_outbox_pending", "supacove_outbox_dead", "supacove_databases_protection",
	}
	for _, fam := range mustFamilies {
		if !anySample(samples, fam) {
			t.Errorf("family %s has no samples (seed incomplete?)", fam)
		}
	}
	for key, value := range samples {
		name := key[:strings.IndexByte(key, '{')]
		if strings.HasPrefix(name, "supacove_") {
			legacy := "supabackup_" + strings.TrimPrefix(name, "supacove_") + key[strings.IndexByte(key, '{'):]
			if lv, ok := samples[legacy]; !ok {
				t.Errorf("sample %s has no legacy alias", key)
			} else if lv != value {
				t.Errorf("alias value diverges for %s: legacy %s vs current %s", key, lv, value)
			}
		}
	}
	for key := range samples {
		name := key[:strings.IndexByte(key, '{')]
		if !strings.HasPrefix(name, "supabackup_") {
			continue
		}
		if name == "supabackup_jobs_succeeded" || name == "supabackup_jobs_failed" {
			continue
		}
		current := "supacove_" + strings.TrimPrefix(name, "supabackup_") + key[strings.IndexByte(key, '{'):]
		if _, ok := samples[current]; !ok {
			t.Errorf("legacy-only sample %s (current twin missing)", key)
		}
	}
	for name, help := range helps {
		if strings.HasPrefix(name, "supabackup_") && !strings.Contains(help, "DEPRECATED") {
			t.Errorf("legacy family %s does not announce deprecation: %s", name, help)
		}
	}
	for _, name := range []string{"supabackup_jobs_succeeded", "supabackup_jobs_failed"} {
		if _, ok := helps[name]; !ok {
			t.Errorf("pre-rename shortcut %s missing", name)
		}
		if anySample(samples, "supacove_"+strings.TrimPrefix(name, "supabackup_")) {
			t.Errorf("shortcut %s must not get a supacove_ twin", name)
		}
	}
}

func anySample(samples map[string]string, family string) bool {
	for key := range samples {
		if strings.HasPrefix(key, family+"{") {
			return true
		}
	}
	return false
}

// TestMetricAliasesOnCollectorFailure pins the failure contract: a failing
// collector surfaces under BOTH names in supacove_scrape_errors. A staging
// failure still emits staging_bytes (0) in both generations — only the
// scrape_errors marker distinguishes it — while a SQL collector failure
// (jobs table gone) omits every dependent family from BOTH generations: a
// query outage must never appear as a credible zero in either.
func TestMetricAliasesOnCollectorFailure(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)
	// One job so the (healthy) jobs family actually carries samples.
	var dbID int64
	if err := env.store.DB.QueryRow(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		 VALUES ('failure-probe', 'generic', '', 'x', 0, 0) RETURNING id`).Scan(&dbID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB.Exec(
		`INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at, finished_at)
		 VALUES (?, 'succeeded', 0, 0, 1, 2)`, dbID); err != nil {
		t.Fatal(err)
	}
	// Point the staging walk at a path that cannot exist: the staging
	// collector fails, every SQL collector stays healthy.
	env.srv.SetRunner(nil, "/nonexistent-supacove-staging-parity-test")

	samples, _ := scrapeSamples(t, env)
	if v, ok := samples[`supacove_scrape_errors{collector="staging"}`]; !ok || v != "1" {
		t.Errorf("supacove_scrape_errors{collector=\"staging\"} missing or wrong: %q", v)
	}
	if v, ok := samples[`supabackup_scrape_errors{collector="staging"}`]; !ok || v != "1" {
		t.Errorf("supabackup_scrape_errors{collector=\"staging\"} missing or wrong: %q", v)
	}
	// The jobs collector stayed healthy: its family must be present under
	// BOTH names (absence is the contract only for a FAILED collector).
	if !anySample(samples, "supacove_jobs") || !anySample(samples, "supabackup_jobs") {
		t.Errorf("jobs family must stay present under both names with a healthy collector")
	}
	if !anySample(samples, "supacove_uptime_seconds") || !anySample(samples, "supabackup_uptime_seconds") {
		t.Errorf("uptime must not depend on any collector")
	}

	// SQL collector failure: hide the jobs table; every jobs-dependent
	// family must vanish from BOTH generations, and the failure itself must
	// be marked under both scrape_errors names.
	env2 := newTestEnv(t)
	env2.bootstrapAdmin(t)
	var sqlDB int64
	if err := env2.store.DB.QueryRow(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		 VALUES ('failure-sql', 'generic', '', 'x', 0, 0) RETURNING id`).Scan(&sqlDB); err != nil {
		t.Fatal(err)
	}
	if _, err := env2.store.DB.Exec(
		`INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at, finished_at)
		 VALUES (?, 'succeeded', 0, 0, 1, 2)`, sqlDB); err != nil {
		t.Fatal(err)
	}
	if _, err := env2.store.DB.Exec(`ALTER TABLE jobs RENAME TO jobs_hidden`); err != nil {
		t.Fatal(err)
	}
	samples2, _ := scrapeSamples(t, env2)
	for _, fam := range []string{
		"jobs", "verification", "last_success_timestamp",
		"remote_commits", "remote_upload_failures", "databases_protection",
	} {
		if anySample(samples2, "supacove_"+fam) || anySample(samples2, "supabackup_"+fam) {
			t.Errorf("family %s must be absent from both generations when its collector fails", fam)
		}
	}
	for _, name := range []string{"supacove_scrape_errors", "supabackup_scrape_errors"} {
		if v, ok := samples2[name+`{collector="jobs"}`]; !ok || v != "1" {
			t.Errorf("%s{collector=\"jobs\"} missing or wrong: %q", name, v)
		}
	}
	if !anySample(samples2, "supacove_outbox_pending") || !anySample(samples2, "supabackup_outbox_pending") {
		t.Errorf("the outbox collector did not touch jobs and must stay healthy")
	}
	if !anySample(samples2, "supacove_uptime_seconds") || !anySample(samples2, "supabackup_uptime_seconds") {
		t.Errorf("uptime must not depend on any collector")
	}
}
