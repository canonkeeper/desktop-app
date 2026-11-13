// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	service "canonkeeper/backend/internal/services/desktop"
)

// usageLimit is how long getDesktopState waits for the count of the data directory (specs/2026-11-13-desk-settings.md
// ST2.2): a saga of many books and the cluster are many files, and the page asks again while it counts.
const usageLimit = 2 * time.Second

// usageFresh is how long a count that ended answers a request without a new count: the page asks again while the server
// counts, and a count that ended between two of its requests is the place it waits for.
const usageFresh = 30 * time.Second

// appState is getDesktopState of this run: the version the build names, the data directory and its copies.
func appState(data DataDir, version string) service.ReadAppState {
	return service.ReadAppState{
		Version: version,
		DataDir: data.Root,
		Usage:   &usageMeter{root: data.Root, limit: usageLimit, count: placeOf, now: time.Now},
		Backups: backupList{directory: data.Backups()},
		Network: networkUses,
	}
}

// networkUses is where the app goes in the network in this build and its settings (ST4.3, DT11.4): the open app goes
// nowhere; a slice that makes the app go somewhere adds its host and what for here.
func networkUses() []service.NetworkUse {
	return nil
}

// usageMeter counts the place of the data directory in the background: a request starts a count unless one goes on or
// one ended less than usageFresh ago, waits for it no longer than limit and answers the last known place meanwhile (ST2.2).
type usageMeter struct {
	root  string
	limit time.Duration
	count func(root string) int64
	now   func() time.Time

	mu      sync.Mutex
	bytes   int64
	known   bool
	counted time.Time
	running chan struct{}
}

func (meter *usageMeter) Measure(ctx context.Context) service.DiskUsage {
	meter.mu.Lock()
	if meter.running == nil && meter.known && meter.now().Sub(meter.counted) < usageFresh {
		defer meter.mu.Unlock()
		return service.DiskUsage{Bytes: meter.bytes, Known: true}
	}
	done := meter.running
	if done == nil {
		done = make(chan struct{})
		meter.running = done
		go meter.run(done)
	}
	meter.mu.Unlock()
	timer := time.NewTimer(meter.limit)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	meter.mu.Lock()
	defer meter.mu.Unlock()
	return service.DiskUsage{Bytes: meter.bytes, Known: meter.known, Counting: meter.running == done}
}

func (meter *usageMeter) run(done chan struct{}) {
	bytes := meter.count(meter.root)
	meter.mu.Lock()
	meter.bytes, meter.known, meter.counted, meter.running = bytes, true, meter.now(), nil
	meter.mu.Unlock()
	close(done)
}

// placeOf is the sum of the sizes of the regular files under root. Links are neither followed nor counted — the home
// of Codex links to the profile of the bundle — and what cannot be read is left out.
func placeOf(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// backupVersionLength bounds what the version of a copy may be: a version of the app, not a text.
const backupVersionLength = 64

// backupList lists the complete copies of backups/ (ST2.1, ST2.3): the newest first by the time in their names, as
// many as the start keeps.
type backupList struct{ directory string }

func (list backupList) List() ([]service.Backup, error) {
	entries, err := os.ReadDir(list.directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var copies []service.Backup
	for _, entry := range entries {
		path := filepath.Join(list.directory, entry.Name())
		_, made, ok := backupOf(entry.Name())
		if !ok || !entry.IsDir() || !backupFinished(path) {
			continue
		}
		at, err := time.Parse(backupTime, made)
		if err != nil {
			continue
		}
		copies = append(copies, service.Backup{Made: at.UTC(), Version: versionOf(path)})
	}
	slices.SortFunc(copies, func(a, b service.Backup) int { return cmp.Compare(b.Made.UnixNano(), a.Made.UnixNano()) })
	return copies[:min(len(copies), backupsKept)], nil
}

// versionOf reads the version a copy holds; a copy without one, or with something that is no version, names none.
func versionOf(copy string) string {
	raw, err := os.ReadFile(filepath.Join(copy, backupVersion))
	if err != nil || len(raw) > backupVersionLength+1 {
		return ""
	}
	version := strings.TrimSpace(string(raw))
	if version == "" || strings.ContainsFunc(version, func(r rune) bool { return r < '!' || r > '~' }) {
		return ""
	}
	return version
}
