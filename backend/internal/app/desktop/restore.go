// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// The return of the data from a copy (specs/2026-11-13-desk-upgrade.md UG4): a start of the backend the shell asks for
// from the failure of data a newer program opened. The backend brings back the copy it names as the candidate — the
// shell names none. The cluster and the homes of Codex are restored beside the data (<part>.restoring); only once that
// went well do the parts of the author move into <data>/before-restore-<UTC>/ and the restored ones take their place.
// The mark of the return is written before anything is restored: a start that finds it — whatever stopped the return —
// first puts every moved part back, removes what was half restored and takes the mark away, so the author never finds a
// saga empty or half restored. A folder «до возврата» is never removed by the app; every return leaves its own. files/
// (covers, pictures) stays as it is: the copy does not hold it.
const (
	restoreMark     = "restoring"
	restoringSuffix = ".restoring"
	beforePrefix    = "before-restore-"
	// restoreMargin is the place the restored cluster may take beyond the cluster of the author: its log and its first
	// write-ahead files.
	restoreMargin = 64 << 20
	// restoreTimeout bounds pg_restore as dumpTimeout bounds pg_dump.
	restoreTimeout = dumpTimeout
	// undoPatience is how long the removal of a half restored part keeps trying: a program of PostgreSQL a killed return
	// started — initdb, pg_restore — may still write into it a moment, and on Windows hold its files.
	undoPatience = 30 * time.Second
)

// restoreParts are the parts of the data directory a return brings back, relative to it: the cluster, the threads of the
// chats and their database (the copy holds codex/home and codex/sqlite).
var restoreParts = []string{"pg", filepath.Join("codex", "home"), filepath.Join("codex", "sqlite")}

// baselineRoles are the roles without login the first migration creates (backend/db/migrations/0001_baseline.sql): the
// dump of one database names them as owners and in its grants, and initdb does not make them. A test holds the list to
// the migrations.
var baselineRoles = []string{"canonkeeper_governance_owner", "canonkeeper_worker_discovery", "canonkeeper_product_runtime"}

// restoreFailed is a return that failed after its checks: the data are as they were.
type restoreFailed struct {
	reason string
	copy   *BackupCopy
	needed int64
	err    error
}

func (failure restoreFailed) Error() string {
	return "desktop: the return of the copy: " + failure.err.Error()
}
func (failure restoreFailed) Unwrap() error { return failure.err }

// fatal is the message of the failed return.
func (failure restoreFailed) fatal() ControlMessage {
	return ControlMessage{Type: messageFatal, Reason: failure.reason, Copy: failure.copy, Needed: failure.needed}
}

// restoreRecord is the mark of a return: the copy it brings back and the folder the parts of the author move into.
type restoreRecord struct {
	Copy   string `json:"copy"`
	Before string `json:"before"`
}

// restoring is the place a part is restored into before it takes its place.
func restoring(data DataDir, part string) string {
	return filepath.Join(data.Root, part+restoringSuffix)
}

// undoRestore puts back what a return an earlier start did not end moved away, removes what it half restored and takes
// its mark away (UG4.2). It runs before the cluster starts; without a mark it only removes half restored parts. The
// cluster a killed return left running in its directory is stopped first: it holds the socket of the cluster and, on
// Windows, its files. A stop that fails does not stop the undo: the server initdb of a killed return runs alone cannot be
// stopped by pg_ctl, and it ends by itself once its directory is gone — the removal tries again meanwhile.
func (cluster *Cluster) undoRestore(ctx context.Context) error {
	data := cluster.data
	staging := *cluster
	staging.home = restoring(data, "pg")
	_ = staging.stopLeftover(ctx)
	mark := filepath.Join(data.Root, restoreMark)
	raw, err := os.ReadFile(mark)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil {
		var record restoreRecord
		if json.Unmarshal(raw, &record) != nil || !plainBefore(record.Before) {
			return errors.New("desktop: the mark of a return names no folder of a return")
		}
		before := filepath.Join(data.Root, record.Before)
		for _, part := range restoreParts {
			moved := filepath.Join(before, part)
			if _, err := os.Lstat(moved); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return err
			}
			// The part of the author was moved away: what stands in its place is restored or half restored.
			if err := os.RemoveAll(filepath.Join(data.Root, part)); err != nil {
				return err
			}
			if err := os.Rename(moved, filepath.Join(data.Root, part)); err != nil {
				return err
			}
		}
		// The folder of this return holds nothing now; a folder that is not empty stays.
		_ = os.Remove(filepath.Join(before, "codex"))
		_ = os.Remove(before)
	}
	for _, part := range restoreParts {
		if err := removePatiently(restoring(data, part), undoPatience); err != nil {
			return err
		}
	}
	if err := os.Remove(mark); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// removePatiently removes path whole, trying again until patience passed.
func removePatiently(path string, patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		err := os.RemoveAll(path)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// plainBefore says whether name is the name of a folder of a return: before-restore-<time>.
func plainBefore(name string) bool {
	made, ok := strings.CutPrefix(name, beforePrefix)
	if !ok {
		return false
	}
	_, err := time.Parse(backupTime, made)
	return err == nil
}

// BeforeRestore is a folder «до возврата» an earlier return left: when it was made, local time, and its size (UG4.4).
type BeforeRestore struct {
	MadeAt string `json:"madeAt"`
	Bytes  int64  `json:"bytes"`
}

// beforeRestores are the folders «до возврата» of the data directory, the newest first.
func beforeRestores(data DataDir) []BeforeRestore {
	entries, err := os.ReadDir(data.Root)
	if err != nil {
		return nil
	}
	type found struct {
		made   time.Time
		before BeforeRestore
	}
	var folders []found
	for _, entry := range entries {
		made, ok := strings.CutPrefix(entry.Name(), beforePrefix)
		at, err := time.Parse(backupTime, made)
		if !ok || err != nil || !entry.IsDir() {
			continue
		}
		path := filepath.Join(data.Root, entry.Name())
		folders = append(folders, found{at, BeforeRestore{MadeAt: at.In(time.Local).Format(time.RFC3339), Bytes: placeOf(path)}})
	}
	slices.SortFunc(folders, func(a, b found) int { return b.made.Compare(a.made) })
	befores := make([]BeforeRestore, len(folders))
	for index, folder := range folders {
		befores[index] = folder.before
	}
	return befores
}

// restore brings the data back from the copy the backend names as the candidate (UG4.1–UG4.3, UG4.6); free says how much
// place the file system of the data directory still has. A failure says why and leaves the data as they were; nil is a
// return that went well.
func (cluster *Cluster) restore(ctx context.Context, step func(ControlMessage), free func(string) (uint64, error), now time.Time) *restoreFailed {
	data := cluster.data
	if err := cluster.stopLeftover(ctx); err != nil {
		return &restoreFailed{reason: reasonRestore, err: err}
	}
	major, err := cluster.bundleMajor(ctx)
	if err != nil {
		return &restoreFailed{reason: reasonRestore, err: err}
	}
	known, err := storedb.LastKnownMigration()
	if err != nil {
		return &restoreFailed{reason: reasonRestore, err: err}
	}
	candidate := restoreCandidate(data.Backups(), known, major)
	if candidate == nil {
		return &restoreFailed{reason: reasonRestore, err: errors.New("no copy to bring back")}
	}
	copyPath := filepath.Join(data.Backups(), candidate.Name)
	// The copy is read before anything changes: the archive of the database and the homes of Codex.
	if err := cluster.tool(ctx, "pg_restore", "--list", filepath.Join(copyPath, backupDump)); err != nil {
		return &restoreFailed{reason: reasonRestoreCopy, copy: candidate, err: err}
	}
	for _, part := range []string{"home", "sqlite"} {
		if info, err := os.Lstat(filepath.Join(copyPath, "codex", part)); err != nil || !info.IsDir() {
			return &restoreFailed{reason: reasonRestoreCopy, copy: candidate, err: fmt.Errorf("the copy has no codex/%s", part)}
		}
	}
	needed := placeOf(data.Cluster()) + placeOf(filepath.Join(copyPath, "codex")) + restoreMargin
	available, err := free(data.Root)
	if err != nil {
		return &restoreFailed{reason: reasonRestore, copy: candidate, err: err}
	}
	if uint64(needed) > available {
		return &restoreFailed{reason: reasonRestoreSpace, copy: candidate, needed: needed - int64(available), err: errors.New("no place on the disk")}
	}

	// The mark goes first: from here on, whatever stops the return, the next start puts everything back.
	before := beforePrefix + now.UTC().Format(backupTime)
	record, _ := json.Marshal(restoreRecord{Copy: candidate.Name, Before: before})
	if err := replaceFile(filepath.Join(data.Root, restoreMark), record); err != nil {
		return &restoreFailed{reason: reasonRestore, copy: candidate, err: err}
	}
	step(ControlMessage{Type: messageStep, Step: stepRestore, Copy: candidate})
	failed := func(reason string, err error) *restoreFailed {
		if undone := cluster.undoRestore(ctx); undone != nil {
			err = errors.Join(err, undone)
		}
		return &restoreFailed{reason: reason, copy: candidate, err: err}
	}
	for _, part := range restoreParts {
		if err := os.RemoveAll(restoring(data, part)); err != nil {
			return failed(reasonRestore, err)
		}
	}
	if err := cluster.restoreDatabase(ctx, copyPath); err != nil {
		return failed(reasonRestore, err)
	}
	for _, part := range []string{"home", "sqlite"} {
		if err := copyTree(filepath.Join(copyPath, "codex", part), restoring(data, filepath.Join("codex", part)), nil); err != nil {
			return failed(reasonRestore, err)
		}
	}
	// The restored parts take the places of the parts of the author, which move into the folder «до возврата».
	if err := os.MkdirAll(filepath.Join(data.Root, before, "codex"), 0o700); err != nil {
		return failed(reasonRestore, err)
	}
	for _, part := range restoreParts {
		if err := os.Rename(filepath.Join(data.Root, part), filepath.Join(data.Root, before, part)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return failed(reasonRestore, err)
		}
		if err := os.Rename(restoring(data, part), filepath.Join(data.Root, part)); err != nil {
			return failed(reasonRestore, err)
		}
	}
	if err := os.Remove(filepath.Join(data.Root, restoreMark)); err != nil {
		return failed(reasonRestore, err)
	}
	return nil
}

// restoreDatabase restores the dump of the copy into a new cluster beside the cluster of the author (UG4.3): initdb with
// the parameters and the password of the standard creation, the roles of the first migration and of Bootstrap, an empty
// database, then pg_restore of the bundle as the administrator, stopping at the first error, with the owners and the
// grants of the dump. The cluster is stopped before the restored directory takes its place.
func (cluster *Cluster) restoreDatabase(ctx context.Context, copyPath string) (err error) {
	staging := *cluster
	staging.home = restoring(cluster.data, "pg")
	staging.fallback = ""
	if err := staging.initialize(ctx); err != nil {
		return err
	}
	if err := staging.ensureSettings(); err != nil {
		return err
	}
	if err := staging.listen(); err != nil {
		return err
	}
	logFile := filepath.Join(cluster.data.Logs(), "postgres-restore-"+time.Now().UTC().Format("2006-01-02")+".log")
	if err := staging.tool(ctx, "pg_ctl", "start", "-w", "-t", "60", "-D", staging.directory(), "-l", logFile); err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), stopClusterTime)
		defer cancel()
		err = errors.Join(err, staging.Stop(stopCtx))
	}()
	admin, err := pgx.Connect(ctx, staging.url(adminRole, staging.secrets.Admin, "postgres", nil))
	if err != nil {
		return fmt.Errorf("connect the administrator: %w", err)
	}
	defer admin.Close(context.Background())
	for _, role := range baselineRoles {
		if _, err := admin.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN"); err != nil {
			return err
		}
	}
	if err := staging.loginRoles(ctx, admin); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+databaseName); err != nil {
		return err
	}
	passwords := filepath.Join(cluster.data.Root, passFile)
	if err := os.WriteFile(passwords, []byte("*:*:*:"+adminRole+":"+staging.secrets.Admin+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(passwords)
	return staging.run(ctx, restoreTimeout, []string{"PGPASSFILE=" + passwords}, "pg_restore", "--exit-on-error", "-w",
		"-h", staging.host, "-p", strconv.Itoa(staging.port), "-U", adminRole, "-d", databaseName,
		filepath.Join(copyPath, backupDump))
}
