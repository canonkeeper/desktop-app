// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"cmp"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	codexadapter "canonkeeper/backend/internal/adapters/codex"
)

// The copy before the migrations of a new version (specs/2026-11-12-desk-process.md PR3): backups/pre-<the last
// applied migration>-<UTC time>/ holds db.dump — pg_dump -Fc of the database of the product by the program of the
// bundle — and the threads of the chats, codex/home without the profile of each flow and codex/sqlite of the data
// directory; the mark complete is written last. pg_restore brings the database back and the threads are copied back;
// files/ is not copied, the migrations of the schema do not touch it.
const (
	backupPrefix   = "pre-"
	backupDump     = "db.dump"
	backupComplete = "complete"
	backupTime     = "20060102T150405.000Z"
	// backupsKept copies stay after the migrations (PR3.2).
	backupsKept = 2
	// dumpTimeout bounds pg_dump: a saga of many books is a few hundred megabytes.
	dumpTimeout = 30 * time.Minute
	// passFile holds the password of the administrator for pg_dump while it runs: the password is never in the
	// arguments or the environment of a process (DA3.2).
	passFile = "pg.pass"
)

// backupVersion holds the version of the app that made the copy (specs/2026-11-13-desk-settings.md ST2.3).
const backupVersion = "version"

// The failed steps of the start the shell tells apart (PR3.3, PR3.4).
var errNewerSchema = errors.New("desktop: the database holds a migration newer than the program")

type backupFailed struct{ err error }

func (failure backupFailed) Error() string {
	return "desktop: the copy before the migrations: " + failure.err.Error()
}
func (failure backupFailed) Unwrap() error { return failure.err }

// migrationFailed is a migration that failed after the copy named backup was made: the migrations run in one
// transaction, so the database is as it was before the start.
type migrationFailed struct {
	backup string
	err    error
}

func (failure migrationFailed) Error() string {
	return "desktop: apply migrations: " + failure.err.Error()
}
func (failure migrationFailed) Unwrap() error { return failure.err }

// backupOf reads a name of backups/: pre-<number>-<time>; ok is false for anything else lying there.
func backupOf(name string) (schema int64, made string, ok bool) {
	rest, found := strings.CutPrefix(name, backupPrefix)
	if !found {
		return 0, "", false
	}
	number, made, found := strings.Cut(rest, "-")
	if !found {
		return 0, "", false
	}
	schema, err := strconv.ParseInt(number, 10, 64)
	if err != nil || schema < 0 {
		return 0, "", false
	}
	if _, err := time.Parse(backupTime, made); err != nil {
		return 0, "", false
	}
	return schema, made, true
}

// backupFinished says whether the copy of the directory got its mark.
func backupFinished(directory string) bool {
	info, err := os.Stat(filepath.Join(directory, backupComplete))
	return err == nil && info.Mode().IsRegular()
}

// removeUnfinishedBackups removes the copies a stop left without their mark (PR3.2); whatever else lies in backups/
// stays.
func removeUnfinishedBackups(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	var failed error
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if _, _, ok := backupOf(entry.Name()); ok && entry.IsDir() && !backupFinished(path) {
			failed = errors.Join(failed, os.RemoveAll(path))
		}
	}
	return failed
}

// pruneBackups keeps keep complete copies (PR3.2, DT13.6): current — the copy of this start, which a fatal migrate
// names — and the newest others by the time in their names; it removes the older ones. current stays whatever its time:
// a clock set back gives it an older one.
func pruneBackups(directory string, keep int, current string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	type copyOf struct {
		name   string
		schema int64
		made   string
	}
	var found []copyOf
	for _, entry := range entries {
		schema, made, ok := backupOf(entry.Name())
		if ok && entry.IsDir() && backupFinished(filepath.Join(directory, entry.Name())) {
			found = append(found, copyOf{entry.Name(), schema, made})
		}
	}
	// The copy of this start goes first, then the others from the newest.
	rank := func(found copyOf) int {
		if found.name == current {
			return 0
		}
		return 1
	}
	slices.SortFunc(found, func(a, b copyOf) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(b.made, a.made), cmp.Compare(b.schema, a.schema))
	})
	var failed error
	for _, old := range found[min(keep, len(found)):] {
		failed = errors.Join(failed, os.RemoveAll(filepath.Join(directory, old.name)))
	}
	return failed
}

// backup copies the database and the threads before the migrations (PR3.1) and names the copy. A copy that fails is
// removed and the error says why; the database is not changed either way.
func (cluster *Cluster) backup(ctx context.Context, schema int64, now time.Time) (string, error) {
	name := backupPrefix + strconv.FormatInt(schema, 10) + "-" + now.UTC().Format(backupTime)
	directory := filepath.Join(cluster.data.Backups(), name)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return "", err
	}
	if err := cluster.copyInto(ctx, directory); err != nil {
		_ = os.RemoveAll(directory)
		return "", err
	}
	return name, nil
}

func (cluster *Cluster) copyInto(ctx context.Context, directory string) error {
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	passwords := filepath.Join(cluster.data.Root, passFile)
	if err := os.WriteFile(passwords, []byte("*:*:*:"+adminRole+":"+cluster.secrets.Admin+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(passwords)
	if err := os.Chmod(passwords, 0o600); err != nil {
		return err
	}
	if err := cluster.run(ctx, dumpTimeout, []string{"PGPASSFILE=" + passwords}, "pg_dump", "-Fc", "-w",
		"-h", cluster.host, "-p", strconv.Itoa(cluster.port), "-U", adminRole, "-d", databaseName,
		"-f", filepath.Join(directory, backupDump)); err != nil {
		return err
	}
	if err := copyTree(cluster.data.CodexHome(), filepath.Join(directory, "codex", "home"), profileOfAFlow); err != nil {
		return err
	}
	if err := copyTree(cluster.data.CodexSQLite(), filepath.Join(directory, "codex", "sqlite"), nil); err != nil {
		return err
	}
	if err := cluster.writeVersion(directory); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, backupComplete), nil, 0o600)
}

// writeVersion names the version of the app in its copy; a program that does not know its version names none.
func (cluster *Cluster) writeVersion(directory string) error {
	if cluster.version == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(directory, backupVersion), []byte(cluster.version+"\n"), 0o600)
}

// profileOfAFlow says whether a path of codex/home, <flow>/<name>, is the program's in the home of its flow
// (specs/2026-11-13-desk-home.md HM1.5): an entry of the profile, which every start copies from the bundle again, or the
// staging of that copy (codex.HomeEntry). The copy before the migrations leaves them out: the threads are the author's,
// the profile is the bundle's.
func profileOfAFlow(relative string) bool {
	flow, name, found := strings.Cut(filepath.ToSlash(relative), "/")
	return found && flow != "" && !strings.Contains(name, "/") && codexadapter.HomeEntry(name)
}

// copyTree copies the directories and regular files of source into target, private to the user: 0700 and 0600, but for
// the paths leave names, when it names any. Links are not copied. A source Codex has not made yet is an empty copy.
func copyTree(source, target string, leave func(relative string) bool) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(source); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if leave != nil && leave(relative) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		destination := filepath.Join(target, relative)
		switch {
		case entry.IsDir():
			return os.MkdirAll(destination, 0o700)
		case entry.Type().IsRegular():
			return copyFile(path, destination)
		default:
			return nil
		}
	})
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// bootstrapFailure is the fatal of a failed Bootstrap: its reason and, after a failed migration, the name of the copy.
func bootstrapFailure(err error) ControlMessage {
	message := ControlMessage{Type: messageFatal, Reason: reasonDatabase}
	var migration migrationFailed
	switch {
	case errors.Is(err, errNewerSchema):
		message.Reason = reasonNewerSchema
	case errors.As(err, &backupFailed{}):
		message.Reason = reasonBackup
	case errors.As(err, &migration):
		message.Reason, message.Backup = reasonMigrate, migration.backup
	}
	return message
}
