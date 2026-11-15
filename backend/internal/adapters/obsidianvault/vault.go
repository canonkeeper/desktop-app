// SPDX-License-Identifier: AGPL-3.0-only

// Package obsidianvault reads a vault of Obsidian of the author — a folder outside the data directory of the app — for
// the bridge (specs/2026-11-13-desk-obsidian.md OB0.2): through the root of the folder, opened for one operation; the
// entries of its directories by their own type, never a link followed; a note opened without waiting, checked to be the
// file the look saw, read up to the size of a note. Nothing is written, and no error names a path (OB0.4).
package obsidianvault

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

var _ service.Files = Files{}

// readBatch is how many entries of a directory are read at once: the look checks the end of its time between batches.
const readBatch = 256

// Files opens the vaults of the author.
type Files struct{}

// Open opens the folder of a vault for one operation (OB1.3): it exists, it is a folder, it opens as a root and its
// entries read; otherwise obsidian.FolderError by the reason.
func (Files) Open(vaultPath string) (service.Vault, error) {
	if !filepath.IsAbs(vaultPath) {
		return nil, obsidian.FolderError{Reason: obsidian.ReasonNotFound}
	}
	info, err := os.Stat(vaultPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, obsidian.FolderError{Reason: obsidian.ReasonNotFound}
	case err != nil:
		return nil, obsidian.FolderError{Reason: obsidian.ReasonUnreadable}
	case !info.IsDir():
		return nil, obsidian.FolderError{Reason: obsidian.ReasonNotFolder}
	}
	root, err := os.OpenRoot(vaultPath)
	if err != nil {
		return nil, obsidian.FolderError{Reason: obsidian.ReasonUnreadable}
	}
	dir, err := root.Open(".")
	if err == nil {
		_, err = dir.ReadDir(1)
		_ = dir.Close()
		if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		_ = root.Close()
		return nil, obsidian.FolderError{Reason: obsidian.ReasonUnreadable}
	}
	return &Vault{root: root, disk: map[string]string{}}, nil
}

// Vault is a vault open for one operation.
type Vault struct {
	root *os.Root
	mu   sync.Mutex
	// disk are the paths on the disk of the paths in NFC whose names the disk keeps otherwise — in NFD, from macOS.
	disk map[string]string
}

// Close closes the root: no handle of the vault outlives its operation.
func (vault *Vault) Close() error { return vault.root.Close() }

// Folder checks a folder a request names (OB0.3): every name of its path is a folder of the vault the look enters — no
// link, no hidden entry. Otherwise obsidian.ErrPath.
func (vault *Vault) Folder(_ context.Context, folder string) error {
	_, err := vault.folder(folder)
	return err
}

func (vault *Vault) folder(folder string) (string, error) {
	if folder == obsidian.Root {
		return ".", nil
	}
	disk, found, err := vault.resolve(folder)
	if err != nil || !found.Dir || found.Link || found.Hidden {
		return "", obsidian.ErrPath
	}
	return disk, nil
}

// resolve finds a path of the vault on the disk, name after name, each folder on the way checked to be a folder without a
// link; it gives the last entry as the disk names it.
func (vault *Vault) resolve(relative string) (string, obsidian.Found, error) {
	vault.mu.Lock()
	known, ok := vault.disk[relative]
	vault.mu.Unlock()
	names := strings.Split(relative, "/")
	disk := "."
	if ok {
		disk, names = known, nil
	}
	var found obsidian.Found
	for index, name := range names {
		child, err := vault.child(disk, name)
		if err != nil {
			return "", obsidian.Found{}, err
		}
		info, err := vault.root.Lstat(child)
		if err != nil {
			return "", obsidian.Found{}, obsidian.ErrPath
		}
		found = foundOf(name, info.Mode(), attributes(info), info.Size())
		if index < len(names)-1 && (!found.Dir || found.Link || found.Hidden) {
			return "", obsidian.Found{}, obsidian.ErrPath
		}
		disk = child
	}
	if ok {
		info, err := vault.root.Lstat(disk)
		if err != nil {
			return "", obsidian.Found{}, obsidian.ErrPath
		}
		found = foundOf(path.Base(relative), info.Mode(), attributes(info), info.Size())
	}
	return disk, found, nil
}

// child is the entry of a directory whose name in NFC is name: the name itself, or the one the directory keeps in another
// form.
func (vault *Vault) child(dir, name string) (string, error) {
	exact := path.Join(dir, name)
	if _, err := vault.root.Lstat(exact); err == nil {
		return exact, nil
	}
	file, err := vault.root.Open(dir)
	if err != nil {
		return "", obsidian.ErrPath
	}
	defer file.Close()
	for {
		entries, err := file.ReadDir(readBatch)
		for _, entry := range entries {
			if obsidian.NFC(entry.Name()) == name {
				return path.Join(dir, entry.Name()), nil
			}
		}
		if err != nil {
			return "", obsidian.ErrPath
		}
	}
}

// remember keeps the path on the disk of an entry whose name is not in NFC.
func (vault *Vault) remember(relative, disk, name string) {
	if obsidian.NFC(name) == name {
		return
	}
	vault.mu.Lock()
	vault.disk[relative] = disk
	vault.mu.Unlock()
}

// Walk visits the entries under folder, folder after folder, until visit stops it or ctx ends (OB0.2, OB2.1): each entry
// by its own type, a link never followed; a folder is entered when visit says so, and only once it opened as the same
// folder the look saw. A folder below that does not open is not entered; the folder the walk starts from is an error.
func (vault *Vault) Walk(ctx context.Context, folder string, visit service.Visit) error {
	start, err := vault.folder(folder)
	if err != nil {
		return err
	}
	type pending struct{ relative, disk string }
	stack := []pending{{relative: folder, disk: start}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		dir, err := vault.openDir(current.disk)
		if err != nil {
			if current.disk == start {
				return err
			}
			continue
		}
		stop := false
		for !stop {
			entries, readErr := dir.ReadDir(readBatch)
			for _, entry := range entries {
				found := vault.found(current.disk, entry)
				enter, halt := visit(current.relative, found)
				if halt {
					stop = true
					break
				}
				relative := obsidian.Join(current.relative, obsidian.NFC(entry.Name()))
				disk := path.Join(current.disk, entry.Name())
				vault.remember(relative, disk, entry.Name())
				if enter {
					stack = append(stack, pending{relative: relative, disk: disk})
				}
			}
			if readErr != nil || ctx.Err() != nil {
				break
			}
		}
		_ = dir.Close()
		if stop {
			return nil
		}
	}
	return ctx.Err()
}

// openDir opens a folder the look saw and checks it is that folder still, not a link put in its place.
func (vault *Vault) openDir(disk string) (*os.File, error) {
	seen, err := vault.root.Lstat(disk)
	if err != nil || !foundOf(path.Base(disk), seen.Mode(), attributes(seen), 0).Dir {
		return nil, obsidian.ErrPath
	}
	dir, err := vault.root.Open(disk)
	if err != nil {
		return nil, obsidian.ErrPath
	}
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(seen, opened) {
		_ = dir.Close()
		return nil, obsidian.ErrPath
	}
	return dir, nil
}

// found is an entry of a directory as the bridge sees it; its information is read only when it is needed — the size of a
// note, the attributes of Windows. An entry whose information does not read is an entry that is not read.
func (vault *Vault) found(dir string, entry fs.DirEntry) obsidian.Found {
	mode := entry.Type()
	if !entryInfo(mode, obsidian.IsNoteName(entry.Name())) {
		return foundOf(entry.Name(), mode, 0, 0)
	}
	info, err := entry.Info()
	if err != nil {
		return obsidian.Found{Name: entry.Name(), Link: true}
	}
	return foundOf(entry.Name(), info.Mode(), attributes(info), info.Size())
}

// Read reads a note (OB0.2, OB3.1): a file on the disk, no link, up to the size of a note; opened without waiting and
// checked to be the file the look saw. A note that does not read is skipped by its reason, never an error with its path.
func (vault *Vault) Read(_ context.Context, notePath string) ([]byte, obsidian.Skip) {
	disk, found, err := vault.resolve(notePath)
	if err != nil {
		return nil, obsidian.SkipUnreadable
	}
	if skip := noteSkip(found); skip != "" {
		return nil, skip
	}
	seen, err := vault.root.Lstat(disk)
	if err != nil {
		return nil, obsidian.SkipUnreadable
	}
	file, err := vault.root.OpenFile(disk, readFlags, 0)
	if err != nil {
		return nil, obsidian.SkipUnreadable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(seen, opened) {
		return nil, obsidian.SkipLink
	}
	if skip := noteSkip(foundOf(found.Name, opened.Mode(), attributes(opened), opened.Size())); skip != "" {
		return nil, skip
	}
	data, err := io.ReadAll(io.LimitReader(file, obsidian.MaxNoteBytes+1))
	if err != nil {
		return nil, obsidian.SkipUnreadable
	}
	if len(data) > obsidian.MaxNoteBytes {
		return nil, obsidian.SkipTooBig
	}
	return data, ""
}

// noteSkip is why an entry is not read as a note.
func noteSkip(found obsidian.Found) obsidian.Skip {
	switch {
	case found.Link || found.Dir || !found.Regular:
		return obsidian.SkipLink
	case found.Hidden:
		return obsidian.SkipHidden
	case found.CloudOnly:
		return obsidian.SkipCloudOnly
	case found.Size > obsidian.MaxNoteBytes:
		return obsidian.SkipTooBig
	case !note(found):
		return obsidian.SkipLink
	}
	return ""
}
