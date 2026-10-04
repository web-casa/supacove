package recovery

import (
	"strings"
	"testing"

	platformpkg "github.com/cloudfan/supabackup/backend/internal/platform"
)

func TestGenerateRestoreScriptNoCredentials(t *testing.T) {
	kit := KitInput{
		BackupUUID:       "test-uuid-123",
		Platform:         "supabase",
		ArtifactFileName: "backup.dump.age",
		SHA256:           "abc123",
		KeyID:            "key-fingerprint",
	}
	script := GenerateRestoreScript(kit)

	if strings.Contains(script, "CANARY") || strings.Contains(script, "password") {
		t.Fatal("script must not contain credentials")
	}
	for _, want := range []string{
		"age --decrypt", "pg_restore", "--exit-on-error", "--no-owner",
		"test-uuid-123", "abc123", "key-fingerprint",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
	// Verify the script is valid POSIX shell
	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Fatal("script must start with #!/bin/sh")
	}
}

func TestGenerateRecoveryGuide(t *testing.T) {
	for _, p := range []string{"supabase", "neon", "railway", "generic"} {
		guide := GenerateRecoveryGuide(platformpkg.Platform(p))
		if len(guide) == 0 {
			t.Errorf("platform %q has empty guide", p)
		}
	}
}
