// SPDX-License-Identifier: AGPL-3.0-only

package obsidianvault

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

var _ service.Writers = Writers{}

// The bounds of the writes of an export.
const (
	// maxManifestBytes is the largest manifest a run reads: thousands of references, a few hundred bytes each.
	maxManifestBytes = 16 << 20
	// holdWait is how long a write waits out a hold of Windows — Obsidian or a scanner that has the file open — as long as the
	// go command waits (cmd/internal/robustio).
	holdWait = 2 * time.Second
	dirMode  = 0o755
	// noteMode is the mode of a note of the author (OE1.3).
	noteMode = 0o644
)

// Writers open the vaults of the author for an export (specs/2026-11-15-desk-obsidian-export.md OE1.3).
type Writers struct{}

// OpenWriter opens the folder of a vault for one export, as Files.Open opens it for a reading.
func (Writers) OpenWriter(vaultPath string) (service.VaultWriter, error) {
	root, err := openVault(vaultPath)
	if err != nil {
		return nil, err
	}
	return &Writer{vault: root, holds: windowsHolds}, nil
}

// Writer writes the folder of an export of a vault (OE1.3): the vault and the folder of the export are roots, and every path
// goes name after name through Lstat, a link never followed — os.Root itself follows the links that stay inside it. Nothing
// is written out of the folder of the export, and no error names a path (OB0.4).
type Writer struct {
	vault  *os.Root
	export *os.Root
	holds  waiter
}

// Close closes the roots.
func (writer *Writer) Close() error {
	if writer.export != nil {
		_ = writer.export.Close()
	}
	return writer.vault.Close()
}

// Folder says what a folder of the vault is: missing, a folder, or a file or a link at its name; a link or a file on the way
// to it is obsidian.ErrExportDirUnsafe.
func (writer *Writer) Folder(_ context.Context, dir string) (service.ExportFolder, error) {
	names := strings.Split(dir, "/")
	current := "."
	for index, name := range names {
		child := path.Join(current, name)
		info, err := writer.vault.Lstat(child)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return service.ExportFolderMissing, nil
		case err != nil:
			return 0, obsidian.ErrExportDirUnsafe
		}
		if found := foundOf(name, info.Mode(), attributes(info), 0); !found.Dir || found.Link {
			if index == len(names)-1 {
				return service.ExportFolderTaken, nil
			}
			return 0, obsidian.ErrExportDirUnsafe
		}
		current = child
	}
	if writer.bare(current) {
		return service.ExportFolderBare, nil
	}
	return service.ExportFolderPresent, nil
}

// bare says a folder of the vault holds nothing but the folder of the app, a folder without a manifest.
func (writer *Writer) bare(dir string) bool {
	folder, err := writer.vault.Open(dir)
	if err != nil {
		return false
	}
	defer folder.Close()
	entries, err := folder.ReadDir(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != obsidian.ExportMark || entry.Type() != fs.ModeDir {
			return false
		}
		if _, err := writer.vault.Lstat(path.Join(dir, obsidian.ExportManifest)); !errors.Is(err, fs.ErrNotExist) {
			return false
		}
	}
	return true
}

// Manifest reads the manifest of a folder of the vault the walk saw as a folder.
func (writer *Writer) Manifest(_ context.Context, dir string) ([]byte, bool, error) {
	mark := path.Join(dir, obsidian.ExportMark)
	info, err := writer.vault.Lstat(mark)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0:
		return nil, false, obsidian.ErrExportManifestUnreadable
	}
	data, skip := writer.readFile(writer.vault, path.Join(dir, obsidian.ExportManifest), maxManifestBytes)
	switch skip {
	case "":
		return data, true, nil
	case skipMissing:
		return nil, false, nil
	}
	return nil, false, obsidian.ErrExportManifestUnreadable
}

// Open makes dir the folder of the export (OE1.3, OE1.5): the folders on the way are made one by one, each checked to be a
// folder without a link, and the folder is opened as a root of its own, checked to be the folder the walk saw.
func (writer *Writer) Open(_ context.Context, dir string) error {
	current := "."
	var seen fs.FileInfo
	for name := range strings.SplitSeq(dir, "/") {
		child := path.Join(current, name)
		info, err := makeDir(writer.vault, child)
		if err != nil {
			return obsidian.ErrExportDirUnsafe
		}
		seen, current = info, child
	}
	export, err := writer.vault.OpenRoot(current)
	if err != nil {
		return obsidian.ErrExportDirUnsafe
	}
	opened, err := export.Stat(".")
	if err != nil || !os.SameFile(seen, opened) {
		_ = export.Close()
		return obsidian.ErrExportDirUnsafe
	}
	writer.export = export
	return nil
}

// makeDir makes a folder of a root where none is and gives it; a link or a file at its name is an error.
func makeDir(root *os.Root, dir string) (fs.FileInfo, error) {
	info, err := root.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := root.Mkdir(dir, dirMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err = root.Lstat(dir)
	}
	if err != nil {
		return nil, err
	}
	if found := foundOf(path.Base(dir), info.Mode(), attributes(info), 0); !found.Dir || found.Link {
		return nil, obsidian.ErrExportDirUnsafe
	}
	return info, nil
}

// parent checks the folders on the way to a path of the export, name after name — make makes the missing ones —, and says
// why a file there is not reached: missing for a folder that is not, link for a link or a file in the way.
func (writer *Writer) parent(file string, make bool) obsidian.ExportSkip {
	dir := path.Dir(file)
	if dir == "." {
		return ""
	}
	current := "."
	for name := range strings.SplitSeq(dir, "/") {
		child := path.Join(current, name)
		if make {
			if _, err := makeDir(writer.export, child); err != nil {
				return obsidian.ExportLink
			}
		} else {
			info, err := writer.export.Lstat(child)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return skipMissing
			case err != nil:
				return obsidian.ExportUnreadable
			}
			if found := foundOf(name, info.Mode(), attributes(info), 0); !found.Dir || found.Link {
				return obsidian.ExportLink
			}
		}
		current = child
	}
	return ""
}

// skipMissing is no file at a path: not a reason of the answer, the file is just not there.
const skipMissing obsidian.ExportSkip = "missing"

// Stat says whether a file lies at a path of the export and what of it does not read.
func (writer *Writer) Stat(_ context.Context, file string) (bool, obsidian.ExportSkip) {
	if skip := writer.parent(file, false); skip == skipMissing {
		return false, ""
	} else if skip != "" {
		return true, skip
	}
	info, err := writer.export.Lstat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, ""
	case err != nil:
		return true, obsidian.ExportUnreadable
	}
	return true, fileSkip(foundOf(path.Base(file), info.Mode(), attributes(info), info.Size()))
}

// fileSkip is why an entry is not read as a reference.
func fileSkip(found obsidian.Found) obsidian.ExportSkip {
	switch {
	case found.Link || found.Dir || !found.Regular:
		return obsidian.ExportLink
	case found.CloudOnly:
		return obsidian.ExportCloudOnly
	case found.Size > obsidian.MaxNoteBytes:
		return obsidian.ExportUnreadable
	}
	return ""
}

// Read reads a reference of the export (OE1.7): a file on the disk, no link, up to the size of a note, opened without
// waiting and checked to be the file Lstat saw; a file Windows holds is waited out a moment, then busy.
func (writer *Writer) Read(_ context.Context, file string) ([]byte, obsidian.ExportSkip) {
	if skip := writer.parent(file, false); skip == skipMissing {
		return nil, obsidian.ExportUnreadable
	} else if skip != "" {
		return nil, skip
	}
	data, skip := writer.readFile(writer.export, file, obsidian.MaxNoteBytes)
	if skip == skipMissing {
		return nil, obsidian.ExportUnreadable
	}
	return data, skip
}

// readFile reads a file of a root, its link not followed, up to limit bytes.
func (writer *Writer) readFile(root *os.Root, file string, limit int64) ([]byte, obsidian.ExportSkip) {
	seen, err := root.Lstat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, skipMissing
	case err != nil:
		return nil, obsidian.ExportUnreadable
	}
	if skip := fileSkip(foundOf(path.Base(file), seen.Mode(), attributes(seen), 0)); skip != "" {
		return nil, skip
	}
	if seen.Size() > limit {
		return nil, obsidian.ExportUnreadable
	}
	var opened *os.File
	err = writer.holds.checked(nil, func() error {
		var err error
		opened, err = root.OpenFile(file, readFlags, 0)
		return err
	})
	switch {
	case err != nil && writer.holds.held(err):
		return nil, obsidian.ExportBusy
	case err != nil:
		return nil, obsidian.ExportUnreadable
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil || !os.SameFile(seen, info) {
		return nil, obsidian.ExportLink
	}
	data, err := io.ReadAll(io.LimitReader(opened, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, obsidian.ExportUnreadable
	}
	return data, ""
}

// waiter waits out a hold of Windows — Obsidian, a scanner or a synchronizer that has the file open and lets it go in a
// moment: held says a failure is one, wait bounds the waiting, now and sleep are its clock.
type waiter struct {
	held  func(error) bool
	wait  time.Duration
	now   func() time.Time
	sleep func(time.Duration)
}

// windowsHolds waits a hold of Windows out for holdWait; outside Windows no failure is a hold.
var windowsHolds = waiter{held: heldByAnother, wait: holdWait, now: time.Now, sleep: time.Sleep}

// checked runs check, then op, and both again while op fails by a hold, for at most the wait; it returns the failure of check,
// or the last failure of op. check goes before every try (OE1.4): the author may save the file while the app waits its hold
// out — that is why another program holds it —, and a try after the wait must not put a reference over what he saved.
func (holds waiter) checked(check, op func() error) error {
	deadline := holds.now().Add(holds.wait)
	for pause := time.Millisecond; ; pause *= 2 {
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		err := op()
		left := deadline.Sub(holds.now())
		if err == nil || !holds.held(err) || left <= 0 {
			return err
		}
		holds.sleep(min(pause, left))
	}
}

// Notes are the paths of the notes of the folder of the export (OE2.6): the folder of the app, hidden entries and _Удалено/
// left out, no link followed, within the entries a look walks.
func (writer *Writer) Notes(ctx context.Context) ([]string, error) {
	var notes []string
	entries := 0
	stack := []string{"."}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		file, err := writer.export.Open(dir)
		if err != nil {
			continue
		}
		for {
			batch, readErr := file.ReadDir(readBatch)
			for _, entry := range batch {
				entries++
				name := entry.Name()
				if isHidden(name) || dir == "." && name == obsidian.ExportDeleted {
					continue
				}
				child := path.Join(dir, name)
				switch {
				case entry.Type().IsDir():
					stack = append(stack, child)
				case entry.Type().IsRegular() && obsidian.IsNoteName(name) && obsidian.ExportPath(child):
					notes = append(notes, child)
				}
			}
			if readErr != nil || entries > obsidian.MaxEntries || ctx.Err() != nil {
				break
			}
		}
		_ = file.Close()
		if entries > obsidian.MaxEntries {
			break
		}
	}
	return notes, ctx.Err()
}

// isHidden says a name starts with a dot: Obsidian reads no such entry.
func isHidden(name string) bool { return strings.HasPrefix(name, ".") }

// Clean removes the temporary files of the runs before (OE1.3): the folder of the app must be a folder, not a link.
func (writer *Writer) Clean(context.Context) error {
	info, err := writer.export.Lstat(obsidian.ExportMark)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !info.IsDir() || info.Mode()&fs.ModeSymlink != 0:
		return obsidian.ErrExportDirUnsafe
	}
	if _, err := writer.export.Lstat(obsidian.ExportTmp); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return writer.export.RemoveAll(obsidian.ExportTmp)
}

// temp writes a text into a new temporary file of the folder of the app and gives its path.
func (writer *Writer) temp(text []byte) (string, error) {
	for _, dir := range []string{obsidian.ExportMark, obsidian.ExportTmp} {
		if _, err := makeDir(writer.export, dir); err != nil {
			return "", obsidian.ErrExportDirUnsafe
		}
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	name := path.Join(obsidian.ExportTmp, hex.EncodeToString(random[:])+".tmp")
	file, err := writer.export.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, noteMode)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(text); err != nil {
		_ = file.Close()
		_ = writer.export.Remove(name)
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = writer.export.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = writer.export.Remove(name)
		return "", err
	}
	return name, nil
}

// Write writes a text at a path of the export through a temporary file renamed into its place (OE1.3, OE1.4). Right before
// the rename the path is looked at again: a new reference never takes the place of a file there, a replacement only of the
// file the plan read.
func (writer *Writer) Write(ctx context.Context, file string, text []byte, expect string) (result error) {
	if skip := writer.parent(file, true); skip != "" {
		return service.ExportSkipped{Skip: skip}
	}
	temporary, err := writer.temp(text)
	if err != nil {
		return err
	}
	defer func() {
		if result != nil {
			_ = writer.export.Remove(temporary)
		}
	}()
	// The path is looked at again before every try of the rename: a hold of Windows waited out is a time the author saves in.
	err = writer.holds.checked(func() error { return writer.vacant(ctx, file, expect) }, func() error { return writer.export.Rename(temporary, file) })
	switch {
	case err != nil && writer.holds.held(err):
		return service.ExportSkipped{Skip: obsidian.ExportBusy}
	case err != nil:
		return err
	}
	return nil
}

// vacant checks the path of a write right before its rename: nothing there for a new reference; for a replacement nothing,
// or the file of the fingerprint the plan read.
func (writer *Writer) vacant(ctx context.Context, file, expect string) error {
	exists, skip := writer.Stat(ctx, file)
	switch {
	case !exists:
		return nil
	case expect == "":
		return service.ErrOccupied
	case skip != "":
		return service.ExportSkipped{Skip: skip}
	}
	data, skip := writer.Read(ctx, file)
	if skip != "" {
		return service.ExportSkipped{Skip: skip}
	}
	if obsidian.ExportPrint(data) != expect {
		return service.ErrChangedMeanwhile
	}
	return nil
}

// Move moves a reference into _Удалено/ under the first free of names (OE2.5, OE2.7), once it is checked to be the file the
// plan read: a file the author changed meanwhile stays where it is. The check and the freedom of the name go again before
// every try of the rename a hold of Windows makes wait.
func (writer *Writer) Move(ctx context.Context, file string, names []string, expect string) (string, error) {
	same := func() error {
		exists, skip := writer.Stat(ctx, file)
		switch {
		case !exists:
			return service.ErrGoneMeanwhile
		case skip != "":
			return service.ExportSkipped{Skip: skip}
		}
		data, skip := writer.Read(ctx, file)
		if skip != "" {
			return service.ExportSkipped{Skip: skip}
		}
		if obsidian.ExportPrint(data) != expect {
			return service.ErrChangedMeanwhile
		}
		return nil
	}
	if err := same(); err != nil {
		return "", err
	}
	if _, err := makeDir(writer.export, obsidian.ExportDeleted); err != nil {
		return "", service.ExportSkipped{Skip: obsidian.ExportLink}
	}
	free := func(target string) bool {
		_, err := writer.export.Lstat(target)
		return errors.Is(err, fs.ErrNotExist)
	}
	for _, name := range names {
		target := path.Join(obsidian.ExportDeleted, name)
		if !free(target) {
			continue
		}
		err := writer.holds.checked(func() error {
			if err := same(); err != nil {
				return err
			}
			if !free(target) {
				return errNameTaken
			}
			return nil
		}, func() error { return writer.export.Rename(file, target) })
		switch {
		case errors.Is(err, errNameTaken):
			continue
		case err != nil && writer.holds.held(err):
			return "", service.ExportSkipped{Skip: obsidian.ExportBusy}
		case err != nil:
			return "", err
		}
		return target, nil
	}
	return "", service.ExportSkipped{Skip: obsidian.ExportBusy}
}

// errNameTaken is a name of _Удалено/ a file took while a move waited out a hold.
var errNameTaken = errors.New("obsidianvault: the name of _Удалено/ is taken")

// Keep writes the manifest through a temporary file renamed into its place (OE2.8).
func (writer *Writer) Keep(_ context.Context, manifest []byte) (result error) {
	temporary, err := writer.temp(manifest)
	if err != nil {
		return err
	}
	defer func() {
		if result != nil {
			_ = writer.export.Remove(temporary)
		}
	}()
	return writer.holds.checked(nil, func() error { return writer.export.Rename(temporary, obsidian.ExportManifest) })
}
