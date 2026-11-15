// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

// vaults is the link of the sagas of a test.
type vaults struct {
	mu    sync.Mutex
	paths map[string]string
}

func (store *vaults) Vault(_ context.Context, actor, project string, access service.Access) (service.Linked, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	switch actor {
	case "reader":
		if access == service.AccessWrite {
			return service.Linked{}, service.ErrForbidden
		}
	case "stranger":
		return service.Linked{}, service.ErrNotFound
	}
	return service.Linked{Title: "Северный предел", Path: store.paths[project]}, nil
}

func (store *vaults) Link(_ context.Context, project, vaultPath string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.paths[project] = vaultPath
	return nil
}

func (store *vaults) Unlink(_ context.Context, _, project string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.paths, project)
	return nil
}

// write puts a note of the example vault «Северный предел» under its own temporary directory.
func write(t *testing.T, root, name, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exampleVault is a vault of the invented saga «Северный предел»: two books, characters, places, templates and a diary.
func exampleVault(t *testing.T) string {
	t.Helper()
	vault := filepath.Join(t.TempDir(), "Северный предел")
	for name, text := range map[string]string{
		"Книга 1/_book.md":             "---\nbook: Застава\n---\n[[01 Пролог]]\n[[02 Переправа]]\n",
		"Книга 1/01 Пролог.md":         "Ветер с перевала нёс снег. Ольга закрыла ставни.",
		"Книга 1/02 Переправа.md":      "Мирон ждал у переправы до темноты.",
		"Книга 2/Глава 1.md":           "Весной застава опустела.",
		"Персонажи/Ольга Сватова.md":   "---\ntype: персонаж\n---\nСтаршая на заставе.",
		"Места/Перевал.md":             "Перевал закрыт зимой.",
		"Шаблоны/Глава.md":             "<% tp.file.title %>",
		"Дневник/2026-11-13.md":        "Сегодня писал главу.",
		"Вложения/карта.png":           "png",
		".obsidian/workspace.json":     "{}",
		"Книга 1/Черновики/Отрывок.md": "Отрывок без места.",
		"Книга 1/Карта.excalidraw.md":  "Switch to EXCALIDRAW VIEW",
	} {
		write(t, vault, name, text)
	}
	return vault
}

func scanOf(store *vaults, files service.Files) service.Scan {
	return service.Scan{
		Vaults: store, Files: files, Links: service.NoLinks{}, Gate: service.NewGate(),
		Limits: service.DefaultLimits,
	}
}

// specs/2026-11-13-desk-obsidian.md OB2.1, OB2.2: the look over the vault of the example gives the folders with notes and
// their counts, what it skipped by its reasons, and the hints of the folders of the first two levels.
func TestTheLookOverTheVaultOfTheExample(t *testing.T) {
	store := &vaults{paths: map[string]string{"saga": exampleVault(t)}}
	scanned, err := scanOf(store, obsidianvault.Files{}).Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "saga"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, folder := range scanned.Folders {
		hint := "-"
		if folder.Hint != nil {
			hint = string(folder.Hint.Kind)
			if folder.Hint.Material != "" {
				hint += ":" + string(folder.Hint.Material)
			}
		}
		got[folder.Path] = fmt.Sprintf("%s %d+%d %v %s", folder.Name, folder.Notes, folder.Below, folder.Skipped, hint)
	}
	want := map[string]string{
		obsidian.Root:       "Северный предел 0+9 map[hidden:1] -",
		"Книга 1":           "Книга 1 3+1 map[service:1] book",
		"Книга 1/Черновики": "Черновики 1+0 map[] skip",
		"Книга 2":           "Книга 2 1+0 map[] skip",
		"Персонажи":         "Персонажи 1+0 map[] materials:" + string(authormaterials.KindCharacter),
		"Места":             "Места 1+0 map[] materials:" + string(authormaterials.KindPlace),
		"Шаблоны":           "Шаблоны 1+0 map[] skip",
		"Дневник":           "Дневник 1+0 map[] skip",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tree = %v", got)
	}
	if scanned.Truncated || scanned.Entries != 20 {
		t.Fatalf("truncated %v entries %d", scanned.Truncated, scanned.Entries)
	}

	// OB2.2, OB2.3: a folder linked to a book of the saga (part A2) names its book and is a book by its hint.
	scan := scanOf(store, obsidianvault.Files{})
	scan.Links = links{books: []service.BookFolder{{Folder: "Книга 2", BookID: "b-2", BookTitle: "Весна"}}}
	linked, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "saga"})
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range linked.Folders {
		if folder.Path == "Книга 2" && (folder.Book == nil || folder.Book.BookTitle != "Весна" || folder.Hint.Kind != obsidian.HintBook) {
			t.Fatalf("the linked folder = %+v", folder)
		}
		if folder.Path != "Книга 2" && folder.Book != nil {
			t.Fatalf("%s names a book", folder.Path)
		}
	}
}

// files whose walk waits for the test, or for ever.
type stalled struct {
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (files stalled) Open(string) (service.Vault, error)   { return files, nil }
func (files stalled) Folder(context.Context, string) error { return nil }
func (files stalled) Walk(ctx context.Context, _ string, _ service.Visit) error {
	files.entered <- struct{}{}
	<-files.release
	return nil
}
func (files stalled) Read(context.Context, string) ([]byte, obsidian.Skip) {
	<-files.release
	return nil, ""
}
func (files stalled) Close() error {
	files.closed <- struct{}{}
	return nil
}

// specs/2026-11-13-desk-obsidian.md OB0.1: one operation of a saga at a time — a second look of a saga is busy while the
// process runs only one — and two operations in the process; a look past its time is obsidian.slow at once, and while the
// file system still holds its reading its place stays taken: a vault that does not answer holds no more readings than the
// gate lets through. The root is closed once the operation ends.
func TestTheGateAndTheTimeOfALook(t *testing.T) {
	store := &vaults{paths: map[string]string{"one": "/vault/one", "two": "/vault/two", "three": "/vault/three"}}
	files := stalled{entered: make(chan struct{}, 3), release: make(chan struct{}), closed: make(chan struct{}, 3)}
	scan := scanOf(store, files)
	scan.Limits.Scan = time.Minute
	results := make(chan error, 2)
	look := func(project string) {
		go func() {
			_, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: project})
			results <- err
		}()
		<-files.entered
	}
	var busy service.Busy
	look("one")
	if _, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "one"}); !errors.As(err, &busy) || busy.RetryAfter <= 0 {
		t.Fatalf("a second look of the saga while the process runs one: %v", err)
	}
	look("two")
	if _, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "three"}); !errors.As(err, &busy) {
		t.Fatalf("a third operation of the process: %v", err)
	}
	close(files.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
		<-files.closed
	}

	slow := stalled{entered: make(chan struct{}, 1), release: make(chan struct{}), closed: make(chan struct{}, 1)}
	scan = scanOf(store, slow)
	scan.Limits.Scan = 50 * time.Millisecond
	started := time.Now()
	if _, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "one"}); !errors.Is(err, service.ErrSlow) {
		t.Fatalf("a look past its time: %v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the slow look held its caller")
	}
	<-slow.entered
	scan.Limits.Scan = time.Second
	if _, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "one"}); !errors.As(err, &busy) {
		t.Fatalf("a look of the saga while the file system holds the slow one: %v", err)
	}
	close(slow.release)
	<-slow.closed
	deadline := time.Now().Add(5 * time.Second)
	for {
		go func() {
			_, _ = scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "one"})
		}()
		select {
		case <-slow.entered:
			return
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the slow look kept the place of the saga once its reading ended")
		}
	}
}

// specs/2026-11-13-desk-obsidian.md OB0.1, OB1.5: a saga without a vault is obsidian.not_linked, a vault that does not open
// now obsidian.vault_missing by its reason; the look needs the right to change the books of the saga.
func TestALookNeedsAVaultThatOpens(t *testing.T) {
	store := &vaults{paths: map[string]string{"gone": filepath.Join(t.TempDir(), "Нет")}}
	scan := scanOf(store, obsidianvault.Files{})
	if _, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "none"}); !errors.Is(err, service.ErrNotLinked) {
		t.Fatalf("no vault: %v", err)
	}
	var missing service.VaultMissing
	if _, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "author", ProjectID: "gone"}); !errors.As(err, &missing) || missing.Reason != obsidian.ReasonNotFound {
		t.Fatalf("a vault that is gone: %v", err)
	}
	if _, err := scan.Execute(context.Background(), service.ScanInput{ActorUserID: "reader", ProjectID: "gone"}); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a reader: %v", err)
	}
}
