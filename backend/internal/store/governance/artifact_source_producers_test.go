package governancestore

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactSourceProducersDoNotBypassSharedWriter(t *testing.T) {
	t.Parallel()

	err := filepath.WalkDir("../..", func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "sqlc" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(filename) != ".go" || strings.HasSuffix(filename, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), "INSERT INTO artifact_source_owners") ||
			strings.Contains(string(source), "INSERT INTO artifact_source_refs") {
			t.Errorf("%s writes artifact-source rows directly; use ArtifactSourcesStore", filename)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk current artifact producers: %v", err)
	}
}
