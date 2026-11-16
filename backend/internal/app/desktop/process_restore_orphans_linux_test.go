// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// writingInitdb is an initdb that never makes the cluster of a return: it says its process in record and writes into the
// directory of that cluster — making it again when it is gone — until it is killed, five minutes at most. Any other
// cluster is made by the real initdb of the bundle.
func writingInitdb(resources, record string) string {
	return "#!/bin/sh\n" +
		"for arg; do [ \"$previous\" = -D ] && cluster=$arg; previous=$arg; done\n" +
		"case \"$cluster\" in *" + restoringSuffix + ")\n" +
		"  echo $$ > '" + filepath.Join(record, "initdb.pid") + "'\n" +
		"  i=0; while [ $i -lt 6000 ]; do mkdir -p \"$cluster\" && date > \"$cluster/written\"; sleep 0.05; i=$((i+1)); done\n" +
		"  exit 1;;\n" +
		"esac\n" +
		"exec '" + filepath.Join(resources, "postgres", "bin", "initdb") + "' \"$@\"\n"
}

// specs/2026-11-16-desk-restore-orphans.md RO1.1, RO1.2: a return killed while its initdb writes into the cluster it
// restores leaves no program of its own behind — the kernel takes it with the backend —, and the next start ends its undo
// only once nothing writes there: the data of the later program open whole, no half restored part is left and no program
// of the killed return runs.
func TestAKilledReturnLeavesNoProgramWritingIntoTheData(t *testing.T) {
	_, resources := desktopBinary(t)
	older, newer := returnPrograms(t)
	data := filepath.Join(t.TempDir(), "data")
	first := startBackend(t, older, resources, data, nil)
	opened(t, first).newSaga("Северный предел")
	first.stop()
	second := startBackend(t, newer, resources, data, nil)
	opened(t, second).newSaga("Южный перевал")
	second.stop()

	record := t.TempDir()
	bundle := bundleWithProgram(t, resources, "initdb", writingInitdb(resources, record))
	returning := beginBackend(t, older, restoreStart(older, bundle, data))
	if message := returning.receive(60 * time.Second); message.Type != messageStep || message.Step != "restore" {
		t.Fatalf("the return said %+v", message)
	}
	written := filepath.Join(data, "pg"+restoringSuffix, "written")
	initdb := 0
	deadline := desktoptest.Deadline(60 * time.Second)
	for {
		raw, _ := os.ReadFile(filepath.Join(record, "initdb.pid"))
		initdb, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		if _, err := os.Stat(written); err == nil && initdb > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the initdb of the return was never caught writing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A failed test leaves no program behind: what still writes into the data directory goes before the directory does.
	t.Cleanup(func() {
		if runs(initdb, bundle) {
			_ = syscall.Kill(initdb, syscall.SIGKILL)
		}
	})
	if err := returning.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = returning.command.Wait()

	next := startBackend(t, newer, resources, data, nil)
	if titles := opened(t, next).sagaTitles(); !reflect.DeepEqual(titles, []string{"Северный предел", "Южный перевал"}) {
		t.Fatalf("the next start found %v", titles)
	}
	next.stop()
	nothingHalfRestored(t, data)
	for deadline := desktoptest.Deadline(10 * time.Second); runs(initdb, bundle); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the initdb of the killed return still runs: %d", initdb)
		}
	}
}
