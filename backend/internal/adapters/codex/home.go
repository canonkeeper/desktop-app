package codex

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// homeEntries are the declarative entries of the writable home of a flow: the profile of the flow, codex-homes/<flow>
// of the tree or of the bundle. Every start of a client puts them into the home again; the rest of the home — the
// threads and the state of Codex — is Codex's.
var homeEntries = []string{"config.toml", "model-catalog.json", "AGENTS.md", "agents", "skills"}

// stagingPrefix names the directory a copy of the profile writes into before it puts an entry in its place, and where
// it puts the entry it replaces; a stop in the middle leaves it, and the next copy removes it.
const stagingPrefix = ".canonkeeper-stage-"

// HomeEntry says whether a name of the writable home of a flow is the program's: an entry of the profile, which every
// start puts there again, or the staging of a copy. The copy of the data before the migrations of the app leaves them
// out (specs/2026-11-13-desk-home.md HM1.5).
func HomeEntry(name string) bool {
	for _, entry := range homeEntries {
		if name == entry {
			return true
		}
	}
	return strings.HasPrefix(name, stagingPrefix)
}

// checkHomes is what both ways of the home require — two different absolute paths and the profile a directory — and
// makes the writable home.
func checkHomes(runtimeHome, repositoryHome string) error {
	if !filepath.IsAbs(runtimeHome) || !filepath.IsAbs(repositoryHome) || runtimeHome == repositoryHome {
		return ErrInvalid
	}
	if info, err := os.Stat(repositoryHome); err != nil || !info.IsDir() {
		return errors.Join(ErrInvalid, err)
	}
	return os.MkdirAll(runtimeHome, 0o700)
}

// PrepareHome is the way of the site: the home links every entry to the profile of the tree it runs from, and a link
// that points elsewhere — a home of another tree — is ErrConflict.
func PrepareHome(runtimeHome, repositoryHome string) error {
	if err := checkHomes(runtimeHome, repositoryHome); err != nil {
		return err
	}
	for _, name := range homeEntries {
		target := filepath.Join(repositoryHome, name)
		link := filepath.Join(runtimeHome, name)
		if _, err := os.Stat(target); err != nil {
			if name == "agents" && errors.Is(err, os.ErrNotExist) {
				if _, linkErr := os.Lstat(link); errors.Is(linkErr, os.ErrNotExist) {
					continue
				} else if linkErr == nil {
					return ErrConflict
				} else {
					return linkErr
				}
			}
			return err
		}
		current, err := os.Readlink(link)
		switch {
		case err == nil && current == target:
			continue
		case err == nil:
			return ErrConflict
		case !errors.Is(err, os.ErrNotExist):
			if _, statErr := os.Lstat(link); statErr == nil {
				return ErrConflict
			}
			return err
		}
		if err := os.Symlink(target, link); err != nil {
			return err
		}
	}
	return nil
}

// CopyHome is the way of the app (specs/2026-11-13-desk-home.md HM1): a link needs a privilege on Windows, and the
// bundle of an AppImage lies at a new place every run, so every start copies each entry of the profile into the home —
// written into the staging first, then put in place of the entry, which the staging takes and removes. Under the name
// of an entry there is never half a copy: a file goes over a file in one rename, any other entry steps aside first, and
// a stop between the two renames leaves no entry, which the next start copies — Codex starts only after the copy. An
// entry the profile has not (agents) leaves the home; whatever else lies there stays. Every rename waits out a scanner
// of Windows that holds a file (renameInHome). The copy needs no sync: every start writes it again.
func CopyHome(runtimeHome, repositoryHome string) error {
	if err := checkHomes(runtimeHome, repositoryHome); err != nil {
		return err
	}
	// The staging a stopped copy left; read by name, not by a pattern: the path of the home may hold [ or *. A staging
	// holds nothing Codex reads, so one that cannot be removed now — a file a scanner of Windows holds — does not stop
	// the start: the next start removes it.
	present, err := os.ReadDir(runtimeHome)
	if err != nil {
		return err
	}
	for _, left := range present {
		if strings.HasPrefix(left.Name(), stagingPrefix) {
			_ = os.RemoveAll(filepath.Join(runtimeHome, left.Name()))
		}
	}
	staging, err := os.MkdirTemp(runtimeHome, stagingPrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	for _, name := range homeEntries {
		entry, aside := filepath.Join(runtimeHome, name), filepath.Join(staging, "replaced-"+name)
		source, err := os.Stat(filepath.Join(repositoryHome, name))
		if name == "agents" && errors.Is(err, fs.ErrNotExist) {
			if err := renameInHome(entry, aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		staged := filepath.Join(staging, name)
		if err := copyEntry(filepath.Join(repositoryHome, name), source, staged); err != nil {
			return err
		}
		current, err := os.Lstat(entry)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil && !(current.Mode().IsRegular() && source.Mode().IsRegular()) {
			if err := renameInHome(entry, aside); err != nil {
				return err
			}
		}
		if err := renameInHome(staged, entry); err != nil {
			return err
		}
	}
	return nil
}

// holdWait is how long a rename of the home waits out a hold, as long as the go command waits (cmd/internal/robustio).
const holdWait = 2 * time.Second

// renameInHome is os.Rename that waits out a hold of Windows: a scanner reads the files the copy has just written, and
// a rename of them or of their tree fails until it lets them go (golang.org/issue/31247). Any other failure is returned
// at once.
func renameInHome(from, to string) error {
	return waitOut(func() error { return os.Rename(from, to) }, heldByAnother)
}

// waitOut runs op again while it fails with an error held says passes, for at most holdWait, and returns its last
// failure.
func waitOut(op func() error, held func(error) bool) error {
	deadline := time.Now().Add(holdWait)
	for pause := time.Millisecond; ; pause *= 2 {
		err := op()
		left := time.Until(deadline)
		if err == nil || !held(err) || left <= 0 {
			return err
		}
		time.Sleep(min(pause, left))
	}
}

// copyEntry copies an entry of the profile, a file or a tree, into target, private to the user: directories 0700 and
// files 0600. The path to the profile may go through links; inside an entry only directories and files are of our
// bundle, and anything else stops the copy with ErrInvalid rather than bring a link into the home.
func copyEntry(source string, info fs.FileInfo, target string) error {
	if info.Mode().IsRegular() {
		return copyHomeFile(source, target)
	}
	root, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		switch {
		case entry.IsDir():
			return os.Mkdir(destination, 0o700)
		case entry.Type().IsRegular():
			return copyHomeFile(path, destination)
		default:
			return fmt.Errorf("%w: %s of the Codex profile is not a file or a directory", ErrInvalid, path)
		}
	})
}

func copyHomeFile(source, target string) error {
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
