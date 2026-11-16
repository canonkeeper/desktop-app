// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// specs/2026-11-16-desk-restore-orphans.md RO1.4: a first start killed while its initdb makes the cluster — PG_VERSION
// written, the cluster not yet made — does not stop the next start: it makes the cluster again and is ready, and nothing
// half made is left beside the data.
func TestAFirstStartKilledInItsInitdbLeavesTheNextStartReady(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	first := beginBackend(t, binary, ControlMessage{
		Type: messageStart, DataDir: data, Resources: resources, SecretsStore: "file", Version: versionOfProgram(binary),
	})
	made := ""
	for deadline := desktoptest.Deadline(60 * time.Second); made == ""; time.Sleep(time.Millisecond) {
		for _, place := range []string{"pg", "pg" + creatingSuffix} {
			if _, err := os.Stat(filepath.Join(data, place, "PG_VERSION")); err == nil {
				made = place
			}
		}
		if made == "" && time.Now().After(deadline) {
			t.Fatal("the initdb of the first start never wrote PG_VERSION")
		}
	}
	if err := first.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.command.Wait()
	_, control := os.Stat(filepath.Join(data, made, "global", "pg_control"))
	t.Logf("killed with PG_VERSION in %s; pg_control: %v", made, control)

	next := startBackend(t, binary, resources, data, nil)
	opened(t, next).newSaga("Северный предел")
	next.stop()
	if _, err := os.Lstat(filepath.Join(data, "pg"+creatingSuffix)); !os.IsNotExist(err) {
		t.Fatalf("a cluster half made is left beside the data: %v", err)
	}
}
