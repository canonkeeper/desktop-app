package codex

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// profile writes a profile of a flow as the bundle holds it: the three files, skills/chat/SKILL.md and, withAgents,
// agents/reader.toml; word marks the version of the bundle.
func profile(t *testing.T, directory, word string, withAgents bool) string {
	t.Helper()
	write := func(name, text string) {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md"} {
		write(name, name+" "+word+"\n")
	}
	write(filepath.Join("skills", "chat", "SKILL.md"), "skill "+word+"\n")
	if withAgents {
		write(filepath.Join("agents", "reader.toml"), "agent "+word+"\n")
	}
	return directory
}

// files are the regular files under a directory by their relative paths with their text; a link or another kind of file
// fails the test.
func files(t *testing.T, root string) map[string]string {
	t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		switch {
		case entry.IsDir():
			return nil
		case !entry.Type().IsRegular():
			t.Fatalf("%s in the home is %v, not a file", relative, entry.Type())
		}
		raw, err := os.ReadFile(path)
		found[filepath.ToSlash(relative)] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// theCopyOf checks that the five entries of the home are the profile of the bundle, copied: no link among them or
// inside them, the files of the bundle and nothing else.
func theCopyOf(t *testing.T, home, bundle string) {
	t.Helper()
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md", "agents", "skills"} {
		want, wantErr := os.Lstat(filepath.Join(bundle, name))
		got, err := os.Lstat(filepath.Join(home, name))
		if errors.Is(wantErr, fs.ErrNotExist) {
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s the bundle has not stays in the home: %v", name, err)
			}
			continue
		}
		if err != nil || got.Mode()&fs.ModeSymlink != 0 || got.IsDir() != want.IsDir() {
			t.Fatalf("%s in the home = %v, %v; want a copy of the bundle's", name, got, err)
		}
		if !got.IsDir() {
			raw, _ := os.ReadFile(filepath.Join(home, name))
			wantRaw, _ := os.ReadFile(filepath.Join(bundle, name))
			if string(raw) != string(wantRaw) {
				t.Fatalf("%s in the home = %q; want %q", name, raw, wantRaw)
			}
			continue
		}
		if got, want := files(t, filepath.Join(home, name)), files(t, filepath.Join(bundle, name)); !maps.Equal(got, want) {
			t.Fatalf("%s in the home = %v; want %v", name, got, want)
		}
	}
}

// specs/2026-11-13-desk-home.md HM1.2, HM1.3: the copy puts the profile of the bundle into the home at every start —
// over a file of an earlier copy, over the links an earlier build left and over a tree with a file the bundle no longer
// has — removes the agents the bundle has not, leaves the threads and the state of Codex as they are, and after it no
// entry of the home is a link or holds one. A bundle that moved is copied from its new place.
func TestTheCopiedHomeIsTheProfileOfTheBundleWhereverItLies(t *testing.T) {
	first := profile(t, filepath.Join(t.TempDir(), "mount-1", "codex-homes", "research-chat"), "first", true)
	home := filepath.Join(t.TempDir(), "codex", "home", "research-chat")
	if err := os.MkdirAll(filepath.Join(home, "sessions", "2026", "11"), 0o700); err != nil {
		t.Fatal(err)
	}
	thread := filepath.Join(home, "sessions", "2026", "11", "rollout.jsonl")
	if err := os.WriteFile(thread, []byte(`{"type":"turn"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "model-catalog.json"), []byte("an earlier copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "agents", "gone"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agents", "gone", "old.toml"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// The links of an earlier build into a bundle that is no longer there.
		gone := filepath.Join(t.TempDir(), "mount-0", "codex-homes", "research-chat")
		for _, name := range []string{"config.toml", "skills"} {
			if err := os.Symlink(filepath.Join(gone, name), filepath.Join(home, name)); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := CopyHome(home, first); err != nil {
		t.Fatal(err)
	}
	theCopyOf(t, home, first)
	if err := CopyHome(home, first); err != nil {
		t.Fatalf("the next start with the same bundle: %v", err)
	}
	theCopyOf(t, home, first)

	// The bundle is mounted at a new place and the old one is gone: the home is copied from the new one.
	second := profile(t, filepath.Join(t.TempDir(), "mount-2", "codex-homes", "research-chat"), "second", false)
	if err := os.RemoveAll(filepath.Dir(filepath.Dir(first))); err != nil {
		t.Fatal(err)
	}
	if err := CopyHome(home, second); err != nil {
		t.Fatalf("the start from the moved bundle: %v", err)
	}
	theCopyOf(t, home, second)
	if raw, err := os.ReadFile(thread); err != nil || string(raw) != `{"type":"turn"}` {
		t.Fatalf("the thread after the copies = %q, %v", raw, err)
	}
	if entries, _ := filepath.Glob(filepath.Join(home, ".*")); len(entries) != 0 {
		t.Fatalf("the copy left %v in the home", entries)
	}
	for _, name := range []string{"config.toml", "skills"} {
		if !HomeEntry(name) {
			t.Fatalf("%s is not an entry of the program", name)
		}
	}
	if HomeEntry("sessions") || HomeEntry("state_5.sqlite") {
		t.Fatal("the threads of the author are taken for the program's")
	}
}

// HM1.2: a copy that fails half-way leaves the entry it was copying as it was, not half of the new one, and a stop that
// left the staging of a copy in the home does not stop the next start, which removes it.
func TestACopyThatFailsLeavesTheEntryAsItWas(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("an unreadable file needs the permissions of a user")
	}
	first := profile(t, filepath.Join(t.TempDir(), "first"), "first", false)
	home := filepath.Join(t.TempDir(), "home")
	if err := CopyHome(home, first); err != nil {
		t.Fatal(err)
	}
	second := profile(t, filepath.Join(t.TempDir(), "second"), "second", false)
	unreadable := filepath.Join(second, "skills", "z-unreadable.md")
	if err := os.WriteFile(unreadable, []byte("hidden\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := CopyHome(home, second); err == nil {
		t.Fatal("a bundle with an unreadable file was copied")
	}
	if got := files(t, filepath.Join(home, "skills")); !maps.Equal(got, map[string]string{"chat/SKILL.md": "skill first\n"}) {
		t.Fatalf("skills after the failed copy = %v", got)
	}
	if entries, _ := filepath.Glob(filepath.Join(home, ".*")); len(entries) != 0 {
		t.Fatalf("the failed copy left %v in the home", entries)
	}

	if err := os.Remove(unreadable); err != nil {
		t.Fatal(err)
	}
	// A stop in the middle of a copy left its staging.
	left := filepath.Join(home, ".canonkeeper-stage-0001", "skills")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CopyHome(home, second); err != nil {
		t.Fatalf("the start after a stop in the middle of a copy: %v", err)
	}
	theCopyOf(t, home, second)
	if entries, _ := filepath.Glob(filepath.Join(home, ".*")); len(entries) != 0 {
		t.Fatalf("the staging of the stopped copy stays: %v", entries)
	}
}

// HM1 on Windows: a scanner that reads what the copy has just written holds the file for a moment, and a rename of it or
// of its tree fails with ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION; the copy waits such a hold out, as the go command
// does (cmd/internal/robustio), and stops at once on any other failure — a home without agents does not wait at every
// start.
func TestARenameOfTheHomeWaitsOutAHoldThatPasses(t *testing.T) {
	held := errors.New("held by another process")
	isHeld := func(err error) bool { return errors.Is(err, held) }
	calls := 0
	if err := waitOut(func() error {
		calls++
		if calls < 4 {
			return held
		}
		return nil
	}, isHeld); err != nil || calls != 4 {
		t.Fatalf("a hold that passes = %v after %d calls; want nil after 4", err, calls)
	}
	calls = 0
	if err := waitOut(func() error { calls++; return fs.ErrNotExist }, isHeld); !errors.Is(err, fs.ErrNotExist) || calls != 1 {
		t.Fatalf("a failure that is not a hold = %v after %d calls; want it at once", err, calls)
	}
	calls = 0
	if err := waitOut(func() error { calls++; return held }, isHeld); !errors.Is(err, held) || calls < 2 {
		t.Fatalf("a hold that stays = %v after %d calls; want the hold after the wait", err, calls)
	}
	missing := os.Rename(filepath.Join(t.TempDir(), "agents"), filepath.Join(t.TempDir(), "aside"))
	if missing == nil || heldByAnother(missing) || heldByAnother(nil) {
		t.Fatalf("a missing entry (%v) is taken for a hold", missing)
	}
}

// HM1.3: a link inside an entry of the bundle is not of our bundle — the copy stops rather than put a link into the home;
// the path to the bundle may go through links.
func TestTheCopyTakesNoLinkFromTheBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a link needs a privilege on Windows")
	}
	real := profile(t, filepath.Join(t.TempDir(), "real"), "linked", false)
	through := filepath.Join(t.TempDir(), "through")
	if err := os.Symlink(real, through); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := CopyHome(home, through); err != nil {
		t.Fatalf("a bundle reached through a link: %v", err)
	}
	theCopyOf(t, home, real)

	if err := os.Symlink(filepath.Join(real, "AGENTS.md"), filepath.Join(real, "skills", "chat", "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := CopyHome(home, real); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a link inside the bundle = %v; want ErrInvalid", err)
	}
	if got := files(t, filepath.Join(home, "skills")); !maps.Equal(got, map[string]string{"chat/SKILL.md": "skill linked\n"}) {
		t.Fatalf("skills after the refused copy = %v", got)
	}
}

// HM1.1: the copy checks its homes as the links do.
func TestTheCopyRefusesTheHomesTheLinksRefuse(t *testing.T) {
	bundle := profile(t, filepath.Join(t.TempDir(), "bundle"), "first", false)
	for _, homes := range [][2]string{
		{"relative/home", bundle}, {filepath.Join(t.TempDir(), "home"), "relative/bundle"}, {bundle, bundle},
		{filepath.Join(t.TempDir(), "home"), filepath.Join(t.TempDir(), "missing")},
	} {
		if err := CopyHome(homes[0], homes[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("CopyHome(%q, %q) = %v; want ErrInvalid", homes[0], homes[1], err)
		}
	}
}

// HM1.1: the client copies the home when its build says so and links it otherwise.
func TestTheClientCopiesTheHomeWhenTheBuildSaysSo(t *testing.T) {
	bundle := profile(t, filepath.Join(t.TempDir(), "bundle"), "first", true)
	for _, copies := range []bool{false, true} {
		home := filepath.Join(t.TempDir(), "home")
		client, err := NewClient(t.Context(), ClientConfig{
			Transport: newFakeTransport(), CWD: "/safe/canonkeeper",
			RepositoryHome: bundle, CodexHome: home, HomeCopies: copies,
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		info, err := os.Lstat(filepath.Join(home, "config.toml"))
		if err != nil || (info.Mode()&fs.ModeSymlink != 0) == copies {
			t.Fatalf("config.toml with copies %v = %v, %v", copies, info, err)
		}
		if copies {
			theCopyOf(t, home, bundle)
		}
	}
	if !slices.Equal(homeEntries, []string{"config.toml", "model-catalog.json", "AGENTS.md", "agents", "skills"}) {
		t.Fatalf("the entries of the home = %v", homeEntries)
	}
}

// HM1.2: the data of the author may lie under a path with the characters of a pattern — a bracket in the name of a
// user or a folder; the copy finds the staging a stop left there by its name and starts.
func TestTheCopyStartsInAHomeWhosePathHoldsAPattern(t *testing.T) {
	bundle := profile(t, filepath.Join(t.TempDir(), "bundle"), "first", false)
	for _, folder := range []string{"Автор [черновики", "Name [Work]", "звёзды * и ?"} {
		home := filepath.Join(t.TempDir(), folder, "codex", "home", "research-chat")
		left := filepath.Join(home, ".canonkeeper-stage-0001", "skills")
		if err := os.MkdirAll(left, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := CopyHome(home, bundle); err != nil {
			t.Fatalf("the copy under %q: %v", folder, err)
		}
		theCopyOf(t, home, bundle)
		if _, err := os.Stat(filepath.Dir(left)); !os.IsNotExist(err) {
			t.Fatalf("the staging under %q stays: %v", folder, err)
		}
	}
}
