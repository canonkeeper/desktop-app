// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	service "canonkeeper/backend/internal/services/desktop"
)

// specs/2026-11-13-desk-settings.md ST2.2: the place the data take is the sum of the regular files under the data
// directory; a link — to a file or to a directory outside — is not followed and not counted.
func TestThePlaceOfTheDataCountsNoLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links of the test are symbolic links of Unix")
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{filepath.Join(data, "pg", "base"), filepath.Join(data, "codex", "home"), outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(data, "pg", "base", "1"), strings.Repeat("a", 1000))
	writeFile(t, filepath.Join(data, "pg", "base", "2"), strings.Repeat("b", 24))
	writeFile(t, filepath.Join(data, "settings.json"), `{"port":21000}`)
	writeFile(t, filepath.Join(outside, "big"), strings.Repeat("c", 1<<20))
	if err := os.Symlink(outside, filepath.Join(data, "codex", "home", "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "big"), filepath.Join(data, "big")); err != nil {
		t.Fatal(err)
	}
	if got, want := placeOf(data), int64(1000+24+len(`{"port":21000}`)); got != want {
		t.Fatalf("the place of the data = %d, want %d", got, want)
	}
}

// ST2.2: a request waits for the count no longer than its limit; until the count ends it answers the last known place
// with the mark «считается», and once the count ended — the new place. A count that ended a moment ago answers the next
// request without a new one, so the page that asks again while the server counts gets the place it waited for.
func TestTheCountOfThePlaceAnswersWithinItsLimit(t *testing.T) {
	if usageLimit != 2*time.Second {
		t.Fatalf("the limit of a request = %s, want 2s", usageLimit)
	}
	release := make(chan struct{})
	var counts atomic.Int64
	clock := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}
	meter := &usageMeter{root: "/data", limit: 50 * time.Millisecond, now: now, count: func(root string) int64 {
		if root != "/data" {
			t.Errorf("the count walks %s", root)
		}
		<-release
		return counts.Add(1) * 4096
	}}
	// The count is held until release: a request that waited for it would never answer, so an answer here is the limit
	// at work, whatever the load of the machine.
	first := meter.Measure(context.Background())
	if first != (service.DiskUsage{Counting: true}) {
		t.Fatalf("the first request = %+v, want counting with no place known", first)
	}
	// A second request while the count goes on waits for the same count; no second one starts.
	if second := meter.Measure(context.Background()); second != (service.DiskUsage{Counting: true}) {
		t.Fatalf("a request during the count = %+v", second)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		meter.mu.Lock()
		done := meter.running == nil
		meter.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The count ended between two requests: the next one answers it, without a new count.
	if got := meter.Measure(context.Background()); got != (service.DiskUsage{Bytes: 4096, Known: true}) || counts.Load() != 1 {
		t.Fatalf("a request after the count = %+v after %d counts, want the place counted", got, counts.Load())
	}
	// Later the place is counted anew.
	clockMu.Lock()
	clock = clock.Add(usageFresh)
	clockMu.Unlock()
	if got := meter.Measure(context.Background()); got != (service.DiskUsage{Bytes: 8192, Known: true}) || counts.Load() != 2 {
		t.Fatalf("a later request = %+v after %d counts, want a new count", got, counts.Load())
	}
}

// ST2.1, ST2.3: the copies before an update are the complete ones, the newest first and no more than two; each says
// when it was made and the version of the app that made it — a copy made before the version was written down says none.
func TestTheCopiesBeforeAnUpdateNameTheirTimeAndVersion(t *testing.T) {
	backups := t.TempDir()
	makeCopy(t, backups, "pre-139-20261102T080000.000Z", true)
	makeCopy(t, backups, "pre-140-20261112T091500.250Z", true)
	writeFile(t, filepath.Join(backups, "pre-140-20261112T091500.250Z", backupVersion), "1.1.4\n")
	makeCopy(t, backups, "pre-138-20261020T080000.000Z", true)
	writeFile(t, filepath.Join(backups, "pre-138-20261020T080000.000Z", backupVersion), "1.1.2\n")
	makeCopy(t, backups, "pre-141-20261113T080000.000Z", false)
	writeFile(t, filepath.Join(backups, "notes.txt"), "the author's")

	copies, err := backupList{directory: backups}.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []service.Backup{
		{Made: time.Date(2026, 11, 12, 9, 15, 0, 250_000_000, time.UTC), Version: "1.1.4"},
		{Made: time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)},
	}
	if len(copies) != len(want) || !copies[0].Made.Equal(want[0].Made) || copies[0].Version != want[0].Version ||
		!copies[1].Made.Equal(want[1].Made) || copies[1].Version != want[1].Version {
		t.Fatalf("the copies = %+v, want %+v", copies, want)
	}
	none, err := backupList{directory: filepath.Join(backups, "missing")}.List()
	if err != nil || len(none) != 0 {
		t.Fatalf("no backups directory = %+v, %v", none, err)
	}
}

// ST2.3: the copy of an update holds the version of the app that made it; a program that does not know its version
// writes none.
func TestTheCopyOfAnUpdateHoldsTheVersionThatMadeIt(t *testing.T) {
	for _, version := range []string{"1.1.4", ""} {
		directory := t.TempDir()
		cluster := &Cluster{data: DataDir{Root: t.TempDir()}, version: version}
		if err := cluster.writeVersion(directory); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(directory, backupVersion))
		switch {
		case version == "" && !os.IsNotExist(err):
			t.Fatalf("a program without a version wrote %q, %v", raw, err)
		case version != "" && (err != nil || string(raw) != version+"\n"):
			t.Fatalf("the version of the copy = %q, %v", raw, err)
		}
	}
}

// ST4.3: the open app goes nowhere in the network until a slice adds an address to the list.
func TestTheOpenAppGoesNowhereInTheNetwork(t *testing.T) {
	if uses := networkUses(); len(uses) != 0 {
		t.Fatalf("the network of the app = %+v", uses)
	}
}
