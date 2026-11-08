package governancestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readStoreSQLFile(t *testing.T, path ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", "..", ".."}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer(
		"public.", "",
		"timestamp with time zone", "timestamptz",
		"timestamp without time zone", "timestamp",
	).Replace(string(raw))
}

func normalizeSQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}
