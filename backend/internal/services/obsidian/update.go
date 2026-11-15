// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"errors"
	"log"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/jobs"
)

var (
	// ErrBookNotLinked is a book no folder of the vault is held by (obsidian.book_not_linked).
	ErrBookNotLinked = errors.New("obsidian: the book is not linked with a folder")
	// ErrNothingToApply is an update whose plan changes nothing in the book (obsidian.nothing_to_apply): the update of the
	// core would still take the place of «Вернуть» of the update before.
	ErrNothingToApply = errors.New("obsidian: the update changes nothing")
)

// FolderMissing is the folder of a link, or of a request, that is not a folder of the vault now (obsidian.folder_missing):
// the book and its link stay as they are.
type FolderMissing struct{ Folder string }

func (FolderMissing) Error() string { return "obsidian: the folder is not in the vault" }

// NotesUnread are the notes of the folder of a book that did not read now for a reason that passes — only in the cloud, a
// read that failed — by their reasons (obsidian.notes_unread): applied, each would be a chapter the update removes.
type NotesUnread struct{ Unread obsidian.Skipped }

func (NotesUnread) Error() string { return "obsidian: notes of the folder did not read" }

// ImportRefused is the import of the core refusing the Job of an update, its error as the core says it.
type ImportRefused struct{ Err error }

func (refused ImportRefused) Error() string {
	return "obsidian: the import refused the update: " + refused.Err.Error()
}
func (refused ImportRefused) Unwrap() error { return refused.Err }

// passing are the reasons a note may read the next time: a pass of the update would remove its chapter (OK2.2).
var passing = []obsidian.Skip{obsidian.SkipCloudOnly, obsidian.SkipUnreadable}

// UpdateInput is updateBookFromObsidian (specs/2026-11-14-desk-obsidian-books.md OK2): its preview, or its application with
// the fingerprint of the glue its preview gave and, for notes that did not read, the word of the author that they go.
type UpdateInput struct {
	ActorUserID   string
	ProjectID     string
	BookID        string
	Apply         bool
	Expected      string
	ConfirmUnread bool
	Key           string
	Method        string
	Path          string
	Proof         auth.SessionWriteProof
}

// Updated is the Job of the import the update started; a preview gives the fingerprint of the glue and the report of the
// reading of the folder beside it.
type Updated struct {
	Job         jobs.Job
	Fingerprint string
	Book        *BookPreview
}

// UpdateBook is updateBookFromObsidian (OK2.1, OK2.2, OK2.4, OK2.5): the folder of a linked book glued now by the choice of
// its link and given to «Обновить из файла» of the core — its preview, or its application. Within the place of the saga:
// the Job of the key of an application, the drafts of the bridge canceled, the live queue of the saga, the link; within
// the time of the reading only the glue; then the checks of an application and the start of the Job of the core on a
// context of its own.
type UpdateBook struct {
	Vaults  Vaults
	Files   Files
	Links   BookLinks
	Plans   Plans
	Volumes Volumes
	Shelf   Shelf
	Gate    *Gate
	Limits  Limits
	Logger  *log.Logger
}

func (update UpdateBook) Execute(ctx context.Context, input UpdateInput) (Updated, error) {
	linked, err := update.Vaults.Vault(ctx, input.ActorUserID, input.ProjectID, AccessWrite)
	if err != nil {
		return Updated{}, err
	}
	if linked.Path == "" {
		return Updated{}, ErrNotLinked
	}
	operation, err := enter(ctx, update.Gate, input.ProjectID)
	if err != nil {
		return Updated{}, err
	}
	defer operation.end()
	write := Write{
		ActorUserID: input.ActorUserID, ProjectID: input.ProjectID, Key: input.Key, Method: input.Method, Path: input.Path, Proof: input.Proof,
	}
	// OK2.5: an application answers the Job of its key before the folder is glued — a lost answer and an edit of the folder
	// give the same Job, not folder_changed.
	if input.Apply {
		if job, ok, err := update.Shelf.JobOfKey(ctx, write.step("apply")); err != nil || ok {
			return Updated{Job: job}, err
		}
	}
	held, err := heldFolderOf(operation, update.Limits, update.Links, update.Shelf, update.Logger, input.ActorUserID, input.ProjectID, input.BookID)
	if err != nil {
		return Updated{}, err
	}
	reader := PreviewImport{Files: update.Files, Volumes: update.Volumes, Limits: update.Limits}
	folder := held.folder.Folder
	book, err := readIn(operation, update.Limits.Read, func(ctx context.Context) (BookPreview, error) {
		read, err := reader.read(ctx, linked.Path, held.choice, []string{folder})
		if errors.Is(err, obsidian.ErrPath) {
			return BookPreview{}, FolderMissing{Folder: folder}
		}
		if err != nil {
			return BookPreview{}, err
		}
		return reader.book(folder, nameOf(folder, linked.Path), read.notes[folder], read.skipped[folder]), nil
	})
	if err != nil {
		return Updated{}, err
	}
	book.State = FolderState{Linked: true, BookID: held.folder.Book.BookID, Title: held.folder.Book.Title}
	if book.Refusal == "" {
		if book.State.Changed, err = update.Plans.Changes(ctx, input.ProjectID, input.BookID, book.volume); err != nil {
			return Updated{}, err
		}
	}
	step := write.step("preview", book.Fingerprint)
	if input.Apply {
		if err := applicable(input, book); err != nil {
			return Updated{}, err
		}
		step = write.step("apply")
	}
	writes, cancel := operation.writes(update.Limits.Write)
	defer cancel()
	job, err := update.Shelf.ImportJob(writes, step, UpdateJob{
		BookID: input.BookID, FileName: book.Glued.FileName, Content: []byte(book.Glued.Text), Apply: input.Apply,
	})
	if err != nil {
		return Updated{}, ImportRefused{Err: err}
	}
	if input.Apply {
		return Updated{Job: job}, nil
	}
	return Updated{Job: job, Fingerprint: book.Fingerprint, Book: &book}, nil
}

// applicable checks an application against what its preview showed (OK2.2): the glue is the one of the preview, no note
// that did not read for a passing reason goes without the word of the author, and the plan changes the book — an update
// that changes nothing would still take the place of «Вернуть» of the update before.
func applicable(input UpdateInput, book BookPreview) error {
	if book.Fingerprint != input.Expected {
		return FolderChanged{Folders: []string{book.Folder}}
	}
	unread := obsidian.Skipped{}
	for _, reason := range passing {
		if count := book.Skipped[reason]; count > 0 {
			unread[reason] = count
		}
	}
	if len(unread) > 0 && !input.ConfirmUnread {
		return NotesUnread{Unread: unread}
	}
	if book.Refusal == "" && !book.State.Changed {
		return ErrNothingToApply
	}
	return nil
}

// heldBook is the folder a live book holds and the choice the glue of an update takes of it.
type heldBook struct {
	folder HeldFolder
	choice obsidian.Choice
}

// heldFolderOf is the folder a book holds, read once the drafts of the bridge are canceled and the saga has no live queue
// (OK1.4, OK2.4): ErrBookNotLinked when no folder is held by it.
func heldFolderOf(operation *writing, limits Limits, links BookLinks, shelf Shelf, logger *log.Logger, actorUserID, projectID, bookID string) (heldBook, error) {
	folders, err := freeFolders(operation, limits, links, shelf, logger, actorUserID, projectID)
	if err != nil {
		return heldBook{}, err
	}
	held, ok := holderOf(folders, bookID)
	if !ok {
		return heldBook{}, ErrBookNotLinked
	}
	return heldBook{folder: held, choice: updateChoice(held, folders)}, nil
}

// freeFolders are the folders of the saga by the rule of reading once the drafts of the bridge are canceled, or QueueBusy
// for a saga with a live queue of the volumes (OK2.4): before the vault is read.
func freeFolders(operation *writing, limits Limits, links BookLinks, shelf Shelf, logger *log.Logger, actorUserID, projectID string) ([]HeldFolder, error) {
	if err := cancelDraftsIn(operation, limits, links, shelf, logger, projectID); err != nil {
		return nil, err
	}
	if queue, ok, err := shelf.LiveQueue(operation.ctx, actorUserID, projectID); err != nil {
		return nil, err
	} else if ok {
		return nil, QueueBusy{Queue: queue}
	}
	return links.Folders(operation.ctx, projectID)
}

// updateChoice is the choice the glue of an update takes (OK2.1): the folder of the book, the paths its link left out, and
// the folders inside it chosen otherwise — at its load, and the folders other books of the saga hold now.
func updateChoice(held HeldFolder, folders []HeldFolder) obsidian.Choice {
	choice := obsidian.Choice{Books: []string{held.Folder}, Exclude: held.Book.Choice.Excluded}
	nested := map[string]bool{}
	add := func(folder string) {
		if folder != held.Folder && obsidian.Within(held.Folder, folder) && !nested[folder] {
			nested[folder] = true
			choice.Materials = append(choice.Materials, obsidian.MaterialFolder{Path: folder})
		}
	}
	for _, folder := range held.Book.Choice.Nested {
		add(folder)
	}
	for _, other := range folders {
		add(other.Folder)
	}
	return choice
}
