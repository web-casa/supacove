package recovery

import (
	"os"
	"testing"

	platformpkg "github.com/cloudfan/supabackup/backend/internal/platform"
)

func TestDumpScriptForDebug(t *testing.T) {
	s := GenerateRestoreScript(KitInput{
		BackupUUID: "job-42", Platform: platformpkg.Generic,
		ArtifactFileName: "x.dump.age",
		SHA256:           "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		KeyID:            "k",
	})
	if err := os.WriteFile("/tmp/kitdbg/restore.sh", []byte(s), 0o700); err != nil {
		t.Fatal(err)
	}
}
