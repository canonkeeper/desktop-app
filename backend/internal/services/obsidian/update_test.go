// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

type updateCase struct {
	vault  string
	links  *bookLinks
	shelf  *queueShelf
	plans  plans
	update service.UpdateBook
	link   service.LinkBookFolder
}

// newUpdate is a saga whose «Книга 1» came from its folder with the folder of its characters inside it and a folder of
// drafts left out.
func newUpdate(t *testing.T) *updateCase {
	t.Helper()
	vault := filepath.Join(t.TempDir(), "Северный предел")
	for name, text := range map[string]string{
		"Книга 1/01 Застава.md":        "Ольга вышла к заставе на рассвете.",
		"Книга 1/02 Переправа.md":      "Мирон ждал у переправы.",
		"Книга 1/Персонажи/Ольга.md":   "Старшая на заставе.",
		"Книга 1/Черновики/Обрывок.md": "Обрывок без места.",
		"Книга 2/01 Порт.md":           "Судно вошло в порт.",
	} {
		write(t, vault, name, text)
	}
	test := &updateCase{vault: vault, shelf: &queueShelf{}, plans: plans{"b-1": true, "b-2": true}}
	test.links = &bookLinks{folders: []service.HeldFolder{{
		Folder: "Книга 1",
		Book: &service.LinkedBook{BookID: "b-1", Title: "Застава", Choice: service.BookChoice{
			Excluded: []string{"Книга 1/Черновики"}, Nested: []string{"Книга 1/Персонажи"},
		}},
	}}}
	vaults := &vaults{paths: map[string]string{"saga": vault}}
	logger := log.New(io.Discard, "", 0)
	test.update = service.UpdateBook{
		Vaults: vaults, Files: obsidianvault.Files{}, Links: test.links, Plans: test.plans, Volumes: volumes{}, Shelf: test.shelf,
		Gate: service.NewGate(), Limits: service.DefaultLimits, Logger: logger,
	}
	test.link = service.LinkBookFolder{
		Vaults: vaults, Files: obsidianvault.Files{}, Links: test.links, Shelf: test.shelf, Gate: service.NewGate(),
		Limits: service.DefaultLimits, Logger: logger,
	}
	return test
}

func (test *updateCase) input(apply bool, expected string) service.UpdateInput {
	return service.UpdateInput{
		ActorUserID: "author", ProjectID: "saga", BookID: "b-1", Apply: apply, Expected: expected, Key: "update-key-0001",
		Method: "POST", Path: "/projects/saga/obsidian/books/b-1/update", Proof: auth.SessionWriteProof{},
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK2.1, OK2.5: the bridge gives «Обновить из файла» of the core the folder of the
// book glued now by the choice of its link — the folder of materials inside it and the notes left out are no chapters —:
// the bytes and the name of the glue, the mode, the book, the key of the step and the request; a preview answers the
// fingerprint and the report beside its Job, an application its Job, and the replay of an application its Job before the
// folder is glued.
func TestAnUpdateGivesTheCoreTheGlueOfTheFolder(t *testing.T) {
	test := newUpdate(t)
	updated, err := test.update.Execute(context.Background(), test.input(false, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(test.shelf.imports) != 1 || updated.Book == nil || updated.Job.ID != "job-a" {
		t.Fatalf("the preview = %+v, imports %+v", updated, test.shelf.imports)
	}
	call := test.shelf.imports[0]
	glued := updated.Book.Glued
	if call.update.Apply || call.update.BookID != "b-1" || string(call.update.Content) != glued.Text || call.update.FileName != glued.FileName ||
		call.write.Key != service.DerivedKey("update-key-0001", "preview", updated.Fingerprint) || call.write.Method != "POST" ||
		call.write.Path != "/projects/saga/obsidian/books/b-1/update" || updated.Fingerprint != glued.Fingerprint {
		t.Fatalf("the import of the preview = %+v", call)
	}
	if titles := titlesOf(*updated.Book); !slices.Equal(titles, []string{"Застава", "Переправа"}) ||
		strings.Contains(glued.Text, "Старшая") || strings.Contains(glued.Text, "Обрывок") || !updated.Book.State.Changed {
		t.Fatalf("the glue of the update = %q, state %+v", titles, updated.Book.State)
	}

	applied, err := test.update.Execute(context.Background(), test.input(true, updated.Fingerprint))
	if err != nil || applied.Job.ID != "job-b" || applied.Book != nil || len(test.shelf.imports) != 2 || !test.shelf.imports[1].update.Apply ||
		test.shelf.imports[1].write.Key != service.DerivedKey("update-key-0001", "apply") ||
		!strings.HasPrefix(test.shelf.imports[1].write.Key, service.ApplyKeyPrefix) {
		t.Fatalf("the application = %+v, %v, imports %+v", applied, err, test.shelf.imports)
	}
	// OK2.5: the answer was lost and the folder changed: the replay answers the Job of the key.
	write(t, test.vault, "Книга 1/01 Застава.md", "Ольга вышла к заставе в полдень.")
	again, err := test.update.Execute(context.Background(), test.input(true, updated.Fingerprint))
	if err != nil || again.Job.ID != "job-b" || len(test.shelf.imports) != 2 {
		t.Fatalf("the replay of the application = %+v, %v", again, err)
	}
}

// cloudNotes are the files of a vault whose notes of a name live only in the cloud.
type cloudNotes struct {
	obsidianvault.Files
	name string
}

func (files cloudNotes) Open(vaultPath string) (service.Vault, error) {
	vault, err := files.Files.Open(vaultPath)
	if err != nil {
		return nil, err
	}
	return cloudVault{Vault: vault, name: files.name}, nil
}

type cloudVault struct {
	service.Vault
	name string
}

func (vault cloudVault) Read(ctx context.Context, notePath string) ([]byte, obsidian.Skip) {
	if strings.Contains(notePath, vault.name) {
		return nil, obsidian.SkipCloudOnly
	}
	return vault.Vault.Read(ctx, notePath)
}

// specs/2026-11-14-desk-obsidian-books.md OK2.2, OK2.4: an application starts no Job when the glue is not the one of its
// preview, when notes did not read for a passing reason and the author did not say they go, or when the plan changes
// nothing; within the time of the reading only the vault is read — a vault that does not answer is obsidian.slow and no Job
// starts then or after —; a live queue of the saga is queue_busy before the vault is read; a book no folder is held by is
// book_not_linked; the folder of the link gone is folder_missing.
func TestAnApplicationIsCheckedBeforeItsJob(t *testing.T) {
	test := newUpdate(t)
	preview, err := test.update.Execute(context.Background(), test.input(false, ""))
	if err != nil {
		t.Fatal(err)
	}
	write(t, test.vault, "Книга 1/03 Порт.md", "Судно вошло в порт.")
	var changed service.FolderChanged
	if _, err := test.update.Execute(context.Background(), test.input(true, preview.Fingerprint)); !errors.As(err, &changed) ||
		!slices.Equal(changed.Folders, []string{"Книга 1"}) || len(test.shelf.imports) != 1 {
		t.Fatalf("a folder changed since its preview = %v, imports %d", err, len(test.shelf.imports))
	}

	cloud := newUpdate(t)
	cloud.update.Files = cloudNotes{name: "Переправа"}
	shown, err := cloud.update.Execute(context.Background(), cloud.input(false, ""))
	if err != nil || shown.Book.Skipped[obsidian.SkipCloudOnly] != 1 {
		t.Fatalf("a preview with a note in the cloud = %+v, %v", shown.Book, err)
	}
	var unread service.NotesUnread
	if _, err := cloud.update.Execute(context.Background(), cloud.input(true, shown.Fingerprint)); !errors.As(err, &unread) ||
		unread.Unread[obsidian.SkipCloudOnly] != 1 || len(cloud.shelf.imports) != 1 {
		t.Fatalf("an application without the word of the author = %v", err)
	}
	confirmed := cloud.input(true, shown.Fingerprint)
	confirmed.ConfirmUnread = true
	if _, err := cloud.update.Execute(context.Background(), confirmed); err != nil || len(cloud.shelf.imports) != 2 {
		t.Fatalf("an application the author confirmed = %v", err)
	}

	same := newUpdate(t)
	same.update.Plans = plans{}
	preview, err = same.update.Execute(context.Background(), same.input(false, ""))
	if err != nil || preview.Book.State.Changed {
		t.Fatalf("a preview that changes nothing = %+v, %v", preview.Book, err)
	}
	if _, err := same.update.Execute(context.Background(), same.input(true, preview.Fingerprint)); !errors.Is(err, service.ErrNothingToApply) ||
		len(same.shelf.imports) != 1 {
		t.Fatalf("an application that changes nothing = %v", err)
	}

	stuck := newUpdate(t)
	release := make(chan struct{})
	stuck.update.Files = hangingNotes{Files: obsidianvault.Files{}, release: release}
	stuck.update.Limits.Read = 100 * time.Millisecond
	if _, err := stuck.update.Execute(context.Background(), stuck.input(true, preview.Fingerprint)); !errors.Is(err, service.ErrSlow) {
		t.Fatalf("a vault that does not answer = %v", err)
	}
	close(release)
	for {
		free, err := stuck.update.Gate.Enter("saga")
		if err == nil {
			free()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(stuck.shelf.seen()) != 0 {
		t.Fatalf("a slow vault started %v", stuck.shelf.seen())
	}

	busy := newUpdate(t)
	busy.shelf.live = &service.QueueRef{ID: "q-page", Status: "running"}
	busy.update.Files = hangingNotes{Files: obsidianvault.Files{}, release: make(chan struct{})}
	var queueBusy service.QueueBusy
	if _, err := busy.update.Execute(context.Background(), busy.input(false, "")); !errors.As(err, &queueBusy) || queueBusy.Queue.ID != "q-page" {
		t.Fatalf("an update beside a live queue = %v", err)
	}
	unlinked := busy.input(false, "")
	unlinked.BookID = "b-9"
	busy.shelf.live = nil
	if _, err := busy.update.Execute(context.Background(), unlinked); !errors.Is(err, service.ErrBookNotLinked) {
		t.Fatalf("an update of a book no folder is held by = %v", err)
	}

	gone := newUpdate(t)
	gone.links.folders[0].Folder = "Книга 9"
	var missing service.FolderMissing
	if _, err := gone.update.Execute(context.Background(), gone.input(false, "")); !errors.As(err, &missing) || missing.Folder != "Книга 9" ||
		len(gone.shelf.imports) != 0 {
		t.Fatalf("the folder of the link gone = %v", err)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK2.3, OK2.4: a book is linked with a folder that is in the vault, that no other
// live book holds, that lies in none and holds none; its own folder before does not stand in the way — the new link takes
// its place —; a folder whose book lies in «Недавно удалённые» may be taken; a live queue is queue_busy.
func TestALinkWithoutALoad(t *testing.T) {
	test := newUpdate(t)
	test.links.folders = append(test.links.folders,
		service.HeldFolder{Folder: "Книга 2", Book: &service.LinkedBook{BookID: "b-2", Title: "Переправа"}},
		service.HeldFolder{Folder: "Книга 3", Trash: &service.LinkedBook{BookID: "b-3", Title: "Порт"}},
	)
	input := service.BookFolderInput{ActorUserID: "author", ProjectID: "saga", BookID: "b-1", Folder: "Книга 2"}
	var linked service.FolderLinked
	if err := test.link.Execute(context.Background(), input); !errors.As(err, &linked) || linked.BookID != "b-2" {
		t.Fatalf("the folder of another live book = %v", err)
	}
	var overlap obsidian.Overlap
	for _, folder := range []string{"Книга 2/Часть", obsidian.Root, "Книга 3/Часть"} {
		input.Folder = folder
		if err := test.link.Execute(context.Background(), input); !errors.As(err, &overlap) {
			t.Fatalf("a folder %q that overlaps = %v", folder, err)
		}
	}
	input.Folder = "Книга 9"
	var missing service.FolderMissing
	if err := test.link.Execute(context.Background(), input); !errors.As(err, &missing) || missing.Folder != "Книга 9" {
		t.Fatalf("a folder not in the vault = %v", err)
	}
	input.Folder = "../Книга 1"
	if err := test.link.Execute(context.Background(), input); !errors.Is(err, obsidian.ErrPath) {
		t.Fatalf("a path out of the vault = %v", err)
	}
	write(t, test.vault, "Книга 1/Текст/01 Застава.md", "Ольга вышла к заставе.")
	write(t, test.vault, "Книга 3/01 Порт.md", "Судно вошло в порт.")
	for _, folder := range []string{"Книга 1/Текст", "Книга 3"} {
		input.Folder = folder
		if err := test.link.Execute(context.Background(), input); err != nil {
			t.Fatalf("a link with %q = %v", folder, err)
		}
	}
	test.shelf.live = &service.QueueRef{ID: "q-page", Status: "draft"}
	var busy service.QueueBusy
	if err := test.link.Execute(context.Background(), input); !errors.As(err, &busy) {
		t.Fatalf("a link beside a live queue = %v", err)
	}
}

// slowVaults are the files of a vault whose opening does not answer until released.
type slowVaults struct {
	obsidianvault.Files
	release chan struct{}
}

func (files slowVaults) Open(vaultPath string) (service.Vault, error) {
	<-files.release
	return files.Files.Open(vaultPath)
}

// specs/2026-11-14-desk-obsidian-books.md OK2.6: getObsidianVault names the live books that hold a folder, whether each
// folder is in the vault — looked at within the one check of the saga —, when it was last loaded, and the live books whose
// folder another holds; a vault that hangs leaves every folder unknown within the time of the check, and no second check
// runs while it hangs.
func TestTheVaultNamesItsBooksWithinItsOneCheck(t *testing.T) {
	test := newUpdate(t)
	loaded := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	test.links.folders = []service.HeldFolder{
		{Folder: "Книга 1", Book: &service.LinkedBook{BookID: "b-1", Title: "Застава", LoadedAt: &loaded},
			Others: []service.LinkedBook{{BookID: "b-4", Title: "Застава (прежняя)"}}},
		{Folder: "Книга 9", Book: &service.LinkedBook{BookID: "b-9", Title: "Пропавшая"}},
		{Folder: "Книга 3", Trash: &service.LinkedBook{BookID: "b-3", Title: "Порт"}},
	}
	read := service.ReadVault{
		Vaults: &vaults{paths: map[string]string{"saga": test.vault}}, Files: obsidianvault.Files{}, Links: test.links,
		Openings: service.NewOpenings(), Wait: service.AvailableTime,
	}
	state, err := read.Execute(context.Background(), "author", "saga")
	if err != nil || len(state.Books) != 2 || state.Books[0].Present == nil || !*state.Books[0].Present || state.Books[0].LoadedAt != &loaded ||
		state.Books[1].Present == nil || *state.Books[1].Present || len(state.Relink) != 1 || state.Relink[0].BookID != "b-4" {
		t.Fatalf("the vault = %+v, %v", state, err)
	}

	release := make(chan struct{})
	defer close(release)
	read.Files, read.Wait = slowVaults{release: release}, 50*time.Millisecond
	for range 2 {
		state, err = read.Execute(context.Background(), "author", "saga")
		if err != nil || state.Available != nil || state.Books[0].Present != nil || state.Books[1].Present != nil {
			t.Fatalf("a vault that hangs = %+v, %v", state, err)
		}
	}
}
