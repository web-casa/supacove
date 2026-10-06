package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

func sha256Of(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func osWriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func osStat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
