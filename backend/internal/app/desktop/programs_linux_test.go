// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runs says whether the process pid lives and runs a program whose command line names mark: a zombie runs nothing, and a
// pid taken since by another program is not the test's.
func runs(pid int, mark string) bool {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	if fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:])); len(fields) == 0 || fields[0] == "Z" {
		return false
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	return err == nil && bytes.Contains(cmdline, []byte(mark))
}

// specs/2026-11-16-desk-restore-orphans.md RO1.1: a program of PostgreSQL whose context ends goes with what it started —
// as initdb with the postgres of its steps —, not alone: it leads its own process group, and the cancel kills the group.
func TestACanceledProgramEndsWithWhatItStarted(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	if err := os.MkdirAll(data.Logs(), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	started := filepath.Join(data.Root, "started")
	writeFile(t, filepath.Join(bin, "child"), "#!/bin/sh\ni=0; while [ $i -lt 600 ]; do sleep 1; i=$((i+1)); done\n")
	writeFile(t, filepath.Join(bin, "initdb"), "#!/bin/sh\n'"+filepath.Join(bin, "child")+"' &\necho $! > '"+started+"'\nwait\n")
	for _, name := range []string{"child", "initdb"} {
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child := make(chan int, 1)
	go func() {
		defer cancel()
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if raw, err := os.ReadFile(started); err == nil && bytes.HasSuffix(raw, []byte("\n")) {
				pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
				child <- pid
				return
			}
		}
		child <- 0
	}()
	if err := (&Cluster{bin: bin, data: data}).tool(ctx, "initdb"); err == nil {
		t.Fatal("a canceled program ended well")
	}
	pid := <-child
	if pid == 0 {
		t.Fatal("the program never started its child")
	}
	t.Cleanup(func() {
		if runs(pid, bin) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	for deadline := time.Now().Add(5 * time.Second); runs(pid, bin); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("what the canceled program started still runs: %d", pid)
		}
	}
}
