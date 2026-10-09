package recovery

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	platformpkg "github.com/web-casa/supacove/backend/internal/platform"
)

// supaTOC is a table of contents as `pg_restore --list` prints it for a
// Supabase archive (entries taken from a real one, 2026-10-08 drill).
const supaTOC = `;
; Archive created at 2026-10-08 05:40:00 UTC
;     dbname: postgres
;
17; 2615 16607 SCHEMA - graphql_public supabase_admin
6; 3079 16636 EXTENSION - pg_graphql 
2; 3079 16389 EXTENSION - uuid-ossp 
5; 3079 16658 EXTENSION - supabase_vault 
284; 1259 17001 TABLE public notes postgres
3901; 0 17001 TABLE DATA public notes postgres
4041; 0 0 ACL graphql_public FUNCTION graphql("operationName" text, query text, variables jsonb, extensions jsonb) supabase_admin
4042; 0 0 ACL public TABLE notes postgres
`

type supaOpt struct {
	toc        string // table of contents the pg_restore stub prints (default supaTOC)
	profileEnv string // SUPABACKUP_PROFILE override ("" = kit default)
	platform   platformpkg.Platform
	super      string // rolsuper answer, default "t"
	available  string // pg_available_extensions rows
}

// runSupa runs a kit against stubs that answer by QUERY text rather than by
// invocation order, and returns the combined output, the argv log and the
// use-list pg_restore was handed (empty when it never got that far).
func runSupa(t *testing.T, o supaOpt) (out, argv, useList string, err error) {
	t.Helper()
	dir, binDir := t.TempDir(), t.TempDir()
	logPath := filepath.Join(dir, "argv.log")
	keptList := filepath.Join(dir, "use.list.kept")
	stub := func(name, body string) {
		if werr := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); werr != nil {
			t.Fatal(werr)
		}
	}
	if o.super == "" {
		o.super = "t"
	}
	if o.toc == "" {
		o.toc = supaTOC
	}
	if o.available == "" {
		o.available = "plpgsql\npg_graphql\nuuid-ossp\nsupabase_vault"
	}
	logLine := fmt.Sprintf(`printf 'TOOL:%%s ARGV:[%%s]\n' "${0##*/}" "$*" >> '%s'`, logPath)
	stub("age", "#!/bin/sh\n"+logLine+`
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "-o" ] && out="$a"
  prev="$a"
done
printf 'FAKE' > "$out"
`)
	stub("pg_restore", "#!/bin/sh\n"+logLine+`
for a in "$@"; do
  case "$a" in
    --list) cat <<'TOC'
`+o.toc+`TOC
      exit 0 ;;
    --use-list=*) cat "${a#--use-list=}" > '`+keptList+`' ;;
  esac
done
exit 0
`)
	stub("psql", "#!/bin/sh\n"+logLine+`
case "$*" in
  *rolsuper*) echo '`+o.super+`' ;;
  *pg_available_extensions*) cat <<'EXT'
`+o.available+`
EXT
    ;;
  *) n=$(cat '`+dir+`/.count' 2>/dev/null || echo 0)
     echo 1 > '`+dir+`/.count'
     if [ "$n" = "0" ]; then echo 0; else echo 5; fi ;;
esac
`)
	kit := testKit()
	stub("sha256sum", "#!/bin/sh\necho '"+kit.SHA256+"'\n")
	for _, name := range []string{"mkdir", "rm", "chmod", "cut", "cat"} {
		stub(name, "#!/bin/sh\nexec /usr/bin/"+name+" \"$@\"\n")
	}
	stub("mktemp", "#!/bin/sh\nd=\""+dir+"/work\"\nmkdir \"$d\"\necho \"$d\"\n")

	if o.platform != "" {
		kit.Platform = o.platform
	}
	script := GenerateRestoreScriptWithTables(kit, 5)
	if werr := os.WriteFile(filepath.Join(dir, "restore.sh"), []byte(script), 0o700); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, testEnc), []byte("AGE"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	cmd := exec.Command("sh", filepath.Join(dir, "restore.sh"), testTarget, testEnc)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + binDir, testEnv, "PGPASSWORD=sup3rsecret"}
	if o.profileEnv != "" {
		cmd.Env = append(cmd.Env, "SUPABACKUP_PROFILE="+o.profileEnv)
	}
	b, err := cmd.CombinedOutput()
	lb, _ := os.ReadFile(logPath)
	ub, _ := os.ReadFile(keptList)
	return string(b), string(lb), string(ub), err
}

// TestSupabaseProfileHappyPath: a Supabase kit restores from an edited
// table of contents — the known-bad grant is gone, everything else stays —
// and still runs pg_restore with --exit-on-error.
func TestSupabaseProfileHappyPath(t *testing.T) {
	out, argv, useList, err := runSupa(t, supaOpt{platform: platformpkg.Supabase})
	if err != nil {
		t.Fatalf("supabase profile failed: %v\n%s", err, out)
	}
	if strings.Contains(useList, "ACL graphql_public FUNCTION graphql(") {
		t.Fatalf("the known-bad grant must be left out of the use-list:\n%s", useList)
	}
	for _, keep := range []string{"ACL public TABLE notes", "EXTENSION - pg_graphql", "TABLE DATA public notes", "; Archive created"} {
		if !strings.Contains(useList, keep) {
			t.Fatalf("use-list lost %q:\n%s", keep, useList)
		}
	}
	if !strings.Contains(argv, "--exit-on-error --no-owner --use-list=") {
		t.Fatalf("pg_restore must stay strict and use the list: %s", argv)
	}
	if !strings.Contains(out, "1 archive entries left out") {
		t.Fatalf("the script must say how many entries it left out:\n%s", out)
	}
	if strings.Contains(argv, "sup3rsecret") {
		t.Fatalf("password leaked into tool argv: %s", argv)
	}
}

// TestSupabaseProfileNeedsSuperuser: refused BEFORE decryption.
func TestSupabaseProfileNeedsSuperuser(t *testing.T) {
	out, argv, _, err := runSupa(t, supaOpt{platform: platformpkg.Supabase, super: "f"})
	mustFail(t, err, "non-superuser target")
	if !strings.Contains(out, "SUPERUSER") {
		t.Fatalf("missing guidance:\n%s", out)
	}
	if strings.Contains(argv, "TOOL:age") || strings.Contains(argv, "TOOL:pg_restore") {
		t.Fatalf("nothing may be decrypted or restored before the role check: %s", argv)
	}
}

// TestSupabaseProfileMissingExtension: named, and nothing is written.
func TestSupabaseProfileMissingExtension(t *testing.T) {
	out, argv, _, err := runSupa(t, supaOpt{platform: platformpkg.Supabase, available: "plpgsql\nuuid-ossp"})
	mustFail(t, err, "extensions unavailable on the target")
	if !strings.Contains(out, "pg_graphql") || !strings.Contains(out, "supabase_vault") {
		t.Fatalf("missing extensions must be named:\n%s", out)
	}
	if strings.Contains(out, " uuid-ossp") {
		t.Fatalf("an available extension was reported missing:\n%s", out)
	}
	if strings.Contains(argv, "--use-list") {
		t.Fatalf("pg_restore must not write anything when extensions are missing: %s", argv)
	}
}

// TestProfileOverride: the environment switches a kit either way, and an
// unknown value is refused.
func TestProfileOverride(t *testing.T) {
	_, argv, _, err := runSupa(t, supaOpt{platform: platformpkg.Supabase, profileEnv: "generic"})
	if err != nil || strings.Contains(argv, "--use-list") || strings.Contains(argv, "rolsuper") {
		t.Fatalf("generic override must take the plain path (err=%v): %s", err, argv)
	}
	_, argv, _, err = runSupa(t, supaOpt{platform: platformpkg.Generic, profileEnv: "supabase"})
	if err != nil || !strings.Contains(argv, "--use-list") {
		t.Fatalf("supabase override must take the supabase path (err=%v): %s", err, argv)
	}
	out, argv, _, err := runSupa(t, supaOpt{profileEnv: "nonsense"})
	mustFail(t, err, "unknown profile")
	if !strings.Contains(out, "SUPABACKUP_PROFILE") || strings.Contains(argv, "TOOL:") {
		t.Fatalf("unknown profile must be refused before any tool runs:\n%s\n%s", out, argv)
	}
}

// TestGenericKitUnchanged: a generic kit never takes the Supabase path.
func TestGenericKitUnchanged(t *testing.T) {
	_, argv, _, err := runSupa(t, supaOpt{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(argv, "--list") || strings.Contains(argv, "--use-list") || strings.Contains(argv, "rolsuper") {
		t.Fatalf("generic kit ran Supabase-only steps: %s", argv)
	}
}

// TestSupabaseProfileFilterIsExact: object names are data from the backed-up
// database and must never steer the filter or the extension check. Only the
// one known grant is left out, and only when its function is absent.
func TestSupabaseProfileFilterIsExact(t *testing.T) {
	const gql = `graphql("operationName" text, query text, variables jsonb, extensions jsonb)`
	spoofed := `;
6; 3079 16636 EXTENSION - pg_graphql 
300; 1259 17100 TABLE public orders EXTENSION - missing_extension postgres
301; 2606 17101 CONSTRAINT public orders x ACL graphql_public FUNCTION ` + gql + ` postgres
4050; 0 0 ACL graphql_public FUNCTION graphql(integer) app_owner
4051; 0 0 ACL public FUNCTION ` + gql + ` app_owner
4041; 0 0 ACL graphql_public FUNCTION ` + gql + ` supabase_admin
`
	out, _, useList, err := runSupa(t, supaOpt{platform: platformpkg.Supabase, toc: spoofed})
	if err != nil {
		t.Fatalf("a table NAMED like an extension record must not fail the preflight: %v\n%s", err, out)
	}
	for _, keep := range []string{
		"TABLE public orders EXTENSION - missing_extension",
		"CONSTRAINT public orders x ACL graphql_public FUNCTION",
		"ACL graphql_public FUNCTION graphql(integer)",
		"4051; 0 0 ACL public FUNCTION",
	} {
		if !strings.Contains(useList, keep) {
			t.Fatalf("filter dropped a legitimate entry (%q):\n%s", keep, useList)
		}
	}
	if strings.Contains(useList, "4041; ") || !strings.Contains(out, "1 archive entries left out") {
		t.Fatalf("exactly the known grant must be left out:\n%s\n%s", out, useList)
	}

	// The function is IN the archive: its grant is an ordinary one.
	withFn := spoofed + "900; 1255 17200 FUNCTION graphql_public " + gql + " supabase_admin\n"
	out, _, useList, err = runSupa(t, supaOpt{platform: platformpkg.Supabase, toc: withFn})
	if err != nil || !strings.Contains(useList, "4041; ") || !strings.Contains(out, "0 archive entries left out") {
		t.Fatalf("grant must stay when its function is archived (err=%v):\n%s\n%s", err, out, useList)
	}
}
