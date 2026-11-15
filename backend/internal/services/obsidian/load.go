// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/quota"
)

// The outcomes of a folder of a load (specs/2026-11-14-desk-obsidian-books.md OK1.2).
const (
	OutcomeQueued  = "queued"
	OutcomeInTrash = "in_trash"
	OutcomeRefused = "refused"
)

// LoadInput is importObsidianBooks: the choice the author saw in the preview, the fingerprint of the glue of each folder
// of a book by its folder as the request names it, and the request with its key.
type LoadInput struct {
	ActorUserID  string
	ProjectID    string
	Choice       obsidian.Choice
	Fingerprints map[string]string
	Key          string
	Method       string
	Path         string
	Proof        auth.SessionWriteProof
}

// FolderOutcome is what a load did with a folder of a book (OK1.2): queued in the queue QueueID, linked to its live book,
// in_trash with its book in «Недавно удалённые», or refused with the code of the refusal of the queue.
type FolderOutcome struct {
	Folder  string `json:"folder"`
	Outcome string `json:"outcome"`
	BookID  string `json:"bookId,omitempty"`
	Title   string `json:"title,omitempty"`
	QueueID string `json:"queueId,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

// Loaded is the answer of a load: the queue it started, if any, and the outcome of each folder in the order of the request;
// Failure is the code of a failure after the queue was made, which the replay of the key answers too.
type Loaded struct {
	Queue   *QueueRef       `json:"queue,omitempty"`
	Folders []FolderOutcome `json:"folders"`
	Failure string          `json:"failure,omitempty"`
}

// QueueBusy is a load refused because the saga has a live queue of the volumes (obsidian.queue_busy): nothing is written.
type QueueBusy struct{ Queue QueueRef }

func (QueueBusy) Error() string { return "obsidian: the saga has a live queue of the volumes" }

// FolderChanged are the folders whose glue is not the one the request names (obsidian.folder_changed): the author sees the
// preview again; nothing is written.
type FolderChanged struct{ Folders []string }

func (FolderChanged) Error() string { return "obsidian: a folder changed since its preview" }

// FoldersGone are folders a reading names that are no folders of the vault — obsidian.ErrPath —: a preview was asked a path
// the vault has not, a load or an update reads a folder gone since.
type FoldersGone struct{ Folders []string }

func (FoldersGone) Error() string { return "obsidian: a chosen folder is not a folder of the vault" }

func (FoldersGone) Is(target error) bool { return target == obsidian.ErrPath }

// sincePreview is a failure of the reading of a load as the load says it: a folder its preview read and that is gone from the
// vault since — renamed or removed — changed since the preview (obsidian.folder_changed), it is no path the request may not
// name.
func sincePreview(err error) error {
	var gone FoldersGone
	if errors.As(err, &gone) {
		return FolderChanged{Folders: gone.Folders}
	}
	return err
}

// LoadFailed is a load that failed after it made its queue (OK1.3): its draft is canceled, and the replay of its key says
// the same code.
type LoadFailed struct{ Code string }

func (LoadFailed) Error() string { return "obsidian: the load failed after its queue was made" }

// ErrLoadInterrupted is a key whose load made its queue and has no kept answer — the process fell in the middle and its
// draft was canceled (obsidian.load_interrupted): a new load needs a new key.
var ErrLoadInterrupted = errors.New("obsidian: the load of the key was interrupted")

// ErrFingerprint is a folder of a book of the request without the fingerprint of its glue.
var ErrFingerprint = errors.New("obsidian: a folder of a book has no fingerprint")

// ImportBooks is importObsidianBooks (OK1): the books of the folders the author saw in the preview go onto the shelf by the
// queue of the volumes of the core. Within the place of the saga: the answer kept for the key, the drafts of the bridge
// canceled, the choice checked; within the time of the reading: each folder glued again by the code of the preview, its
// fingerprint compared with the request and its volume checked by the code of the queue; then the writes to their end —
// the queue first, the links of its volumes, their files, its start — and the answer kept for the key.
type ImportBooks struct {
	Vaults  Vaults
	Files   Files
	Links   BookLinks
	Volumes Volumes
	Shelf   Shelf
	Replays Replays
	Gate    *Gate
	Limits  Limits
	Logger  *log.Logger
	Now     func() time.Time
	// Failpoint stops a load at a point of its writes, for a test of what a failure leaves; nil in a build.
	Failpoint func(point string) error
}

func (load ImportBooks) Execute(ctx context.Context, input LoadInput) (Loaded, error) {
	linked, err := load.Vaults.Vault(ctx, input.ActorUserID, input.ProjectID, AccessWrite)
	if err != nil {
		return Loaded{}, err
	}
	if linked.Path == "" {
		return Loaded{}, ErrNotLinked
	}
	choice, fingerprints, err := checkedRequest(input)
	if err != nil {
		return Loaded{}, err
	}
	operation, err := enter(ctx, load.Gate, input.ProjectID)
	if err != nil {
		return Loaded{}, err
	}
	defer operation.end()
	write := Write{
		ActorUserID: input.ActorUserID, ProjectID: input.ProjectID, Key: input.Key, Method: input.Method, Path: input.Path, Proof: input.Proof,
	}
	key := ReplayKey{ActorUserID: input.ActorUserID, ProjectID: input.ProjectID, Key: input.Key, Request: requestHash(choice, fingerprints)}
	// OK1.6: the answer of the key comes before the vault is read — after a success it is the same when the folder changed.
	if replayed, err := load.Replays.Replay(ctx, key); err != nil || replayed != nil {
		return replayOf(replayed, err)
	}
	queueWrite := write.step("queue")
	if made, err := load.Shelf.QueueOfKey(ctx, input.ActorUserID, input.ProjectID, queueWrite.Key); err != nil {
		return Loaded{}, err
	} else if made {
		return Loaded{}, ErrLoadInterrupted
	}
	if err := cancelDraftsIn(operation, load.Limits, load.Links, load.Shelf, load.Logger, input.ProjectID); err != nil {
		return Loaded{}, err
	}
	folders, err := load.Links.Folders(ctx, input.ProjectID)
	if err != nil {
		return Loaded{}, err
	}
	held := make([]string, 0, len(folders))
	for _, folder := range folders {
		held = append(held, folder.Folder)
	}
	if _, err := choice.Check(held); err != nil {
		return Loaded{}, err
	}
	reader := PreviewImport{Files: load.Files, Volumes: load.Volumes, Limits: load.Limits}
	books, err := readIn(operation, load.Limits.Read, func(ctx context.Context) ([]BookPreview, error) {
		read, err := reader.read(ctx, linked.Path, choice, choice.Books)
		if err != nil {
			return nil, sincePreview(err)
		}
		var books []BookPreview
		var changed []string
		for _, folder := range choice.Books {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			book := reader.book(folder, nameOf(folder, linked.Path), read.notes[folder], read.skipped[folder])
			if book.Fingerprint != fingerprints[folder] {
				changed = append(changed, folder)
			}
			books = append(books, book)
		}
		if len(changed) > 0 {
			return nil, FolderChanged{Folders: changed}
		}
		return books, nil
	})
	if err != nil {
		return Loaded{}, err
	}
	writes, cancel := operation.writes(load.Limits.Write)
	defer cancel()
	return load.write(writes, write, key, choice, books, folders)
}

// checkedRequest is the choice of the request by the rule of a request (OB0.3) with the fingerprint of each folder of a book.
func checkedRequest(input LoadInput) (obsidian.Choice, map[string]string, error) {
	choice, err := input.Choice.Check(nil)
	if err != nil {
		return obsidian.Choice{}, nil, err
	}
	fingerprints := map[string]string{}
	for folder, fingerprint := range input.Fingerprints {
		relative, err := obsidian.Relative(folder)
		if err != nil {
			return obsidian.Choice{}, nil, err
		}
		fingerprints[relative] = fingerprint
	}
	for _, folder := range choice.Books {
		if fingerprints[folder] == "" {
			return obsidian.Choice{}, nil, ErrFingerprint
		}
	}
	return choice, fingerprints, nil
}

// requestHash is the SHA-256 of what a load asks: its choice and the fingerprints of its books.
func requestHash(choice obsidian.Choice, fingerprints map[string]string) string {
	type book struct{ Folder, Fingerprint string }
	request := struct {
		Books     []book
		Materials []obsidian.MaterialFolder
		Exclude   []string
	}{Materials: choice.Materials, Exclude: slices.Sorted(slices.Values(choice.Exclude))}
	for _, folder := range choice.Books {
		request.Books = append(request.Books, book{Folder: folder, Fingerprint: fingerprints[folder]})
	}
	encoded, _ := json.Marshal(request)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func replayOf(replayed *Loaded, err error) (Loaded, error) {
	if err != nil {
		return Loaded{}, err
	}
	if replayed.Failure != "" {
		return Loaded{}, LoadFailed{Code: replayed.Failure}
	}
	return *replayed, nil
}

// write is the writes of a load (OK1.2, OK1.3): the outcome of each folder by the rule of reading; nothing to queue — no
// queue; the queue is the first write, and a live queue of the saga refuses it with nothing written; a failure after it
// cancels the draft and keeps its code for the key. The answer — with a queue, without one (nothing to queue, the limit of
// the books of the saga) or of a failure — is kept for the key (OK1.6).
func (load ImportBooks) write(ctx context.Context, write Write, key ReplayKey, choice obsidian.Choice, books []BookPreview, folders []HeldFolder) (Loaded, error) {
	byFolder := map[string]HeldFolder{}
	for _, folder := range folders {
		byFolder[folder.Folder] = folder
	}
	loaded := Loaded{Folders: make([]FolderOutcome, len(books))}
	var volumes []QueueVolume
	var queued []int
	for index, book := range books {
		outcome := FolderOutcome{Folder: book.Folder}
		held := byFolder[book.Folder]
		switch {
		case held.Book != nil:
			outcome.Outcome, outcome.BookID, outcome.Title = OutcomeLinked, held.Book.BookID, held.Book.Title
		case held.Trash != nil:
			outcome.Outcome, outcome.BookID, outcome.Title = OutcomeInTrash, held.Trash.BookID, held.Trash.Title
		case held.Queue != "":
			outcome.Outcome, outcome.QueueID = OutcomeQueued, held.Queue
		case book.Refusal != "":
			outcome.Outcome, outcome.Refusal = OutcomeRefused, book.Refusal
		default:
			volumes = append(volumes, QueueVolume{Folder: book.Folder, FileName: book.Glued.FileName, Content: []byte(book.Glued.Text)})
			queued = append(queued, index)
		}
		loaded.Folders[index] = outcome
	}
	if len(volumes) == 0 {
		return load.keep(ctx, key, loaded), nil
	}
	queueID, items, err := load.Shelf.CreateQueue(ctx, write.step("queue"), volumes)
	if errors.Is(err, ErrQueueExists) {
		live, ok, err := load.Shelf.LiveQueue(ctx, write.ActorUserID, write.ProjectID)
		if err != nil {
			return Loaded{}, err
		}
		if !ok {
			return Loaded{}, ErrConflict
		}
		return Loaded{}, QueueBusy{Queue: live}
	}
	if exceeded, ok := quota.AsExceeded(err); ok {
		for _, index := range queued {
			loaded.Folders[index] = FolderOutcome{Folder: books[index].Folder, Outcome: OutcomeRefused, Refusal: exceeded.Code()}
		}
		return load.keep(ctx, key, loaded), nil
	}
	if err != nil {
		return Loaded{}, err
	}
	queue, err := load.fill(ctx, write, queueID, items, choice, volumes, books, queued)
	if err != nil {
		if canceled := load.Shelf.CancelDraft(context.WithoutCancel(ctx), Draft{ProjectID: write.ProjectID, QueueID: queueID, ActorUserID: write.ActorUserID}); canceled != nil {
			load.Logger.Print(`{"event":"obsidian_draft_not_canceled"}`)
		}
		failed := LoadFailed{Code: failureCode(err)}
		load.keep(ctx, key, Loaded{Failure: failed.Code})
		return Loaded{}, failed
	}
	loaded.Queue = &queue
	for _, index := range queued {
		loaded.Folders[index] = FolderOutcome{Folder: books[index].Folder, Outcome: OutcomeQueued, QueueID: queue.ID}
	}
	return load.keep(ctx, key, loaded), nil
}

// keep keeps the answer of a load for its key (OK1.6) and gives it back; an answer the store did not keep is in the log.
func (load ImportBooks) keep(ctx context.Context, key ReplayKey, loaded Loaded) Loaded {
	if err := load.Replays.Keep(context.WithoutCancel(ctx), key, loaded); err != nil {
		load.Logger.Print(`{"event":"obsidian_load_answer_not_kept"}`)
	}
	return loaded
}

// fill writes the links of the volumes of the queue with their folders, their files and the start of the queue.
func (load ImportBooks) fill(ctx context.Context, write Write, queueID string, items []string, choice obsidian.Choice, volumes []QueueVolume, books []BookPreview, queued []int) (QueueRef, error) {
	if len(items) != len(volumes) {
		return QueueRef{}, ErrConflict
	}
	if err := load.point("after_queue"); err != nil {
		return QueueRef{}, err
	}
	links := make([]VolumeLink, len(volumes))
	for index, volume := range volumes {
		links[index] = VolumeLink{
			Folder: volume.Folder, ItemID: items[index], Fingerprint: books[queued[index]].Fingerprint, Choice: choiceOf(choice, volume.Folder),
		}
	}
	if err := load.Links.AddVolumes(ctx, write.ProjectID, links, load.now()); err != nil {
		return QueueRef{}, err
	}
	if err := load.point("after_links"); err != nil {
		return QueueRef{}, err
	}
	var version int64
	for index, volume := range volumes {
		uploaded, err := load.Shelf.Upload(ctx, write.step("volume", items[index]), queueID, items[index], volume)
		if err != nil {
			return QueueRef{}, err
		}
		version = uploaded
		if err := load.point("after_upload"); err != nil {
			return QueueRef{}, err
		}
	}
	return load.Shelf.Start(ctx, write.step("start"), queueID, version)
}

// choiceOf is the choice of the book of a folder (OK1.3): what the author left out under it and the folders inside it the
// choice takes otherwise.
func choiceOf(choice obsidian.Choice, folder string) BookChoice {
	var kept BookChoice
	for _, excluded := range choice.Exclude {
		if obsidian.Within(folder, excluded) {
			kept.Excluded = append(kept.Excluded, excluded)
		}
	}
	for _, other := range choice.Folders() {
		if other != folder && obsidian.Within(folder, other) {
			kept.Nested = append(kept.Nested, other)
		}
	}
	return kept
}

// cancelDraftsIn cancels the drafts of the bridge of the saga of an operation before it looks whether the saga is busy
// (OK1.4): a draft left by a load that fell would hold the saga and its folders. The cancel is a write: it goes to its end.
func cancelDraftsIn(operation *writing, limits Limits, links BookLinks, shelf Shelf, logger *log.Logger, projectID string) error {
	ctx, cancel := operation.writes(limits.Write)
	defer cancel()
	return CancelDrafts(ctx, links, shelf, logger, projectID)
}

// CancelDrafts cancels the drafts of the bridge of a saga, of every saga for "" (OK1.4): at the start of the backend and
// before a load or an update looks whether the saga is busy. A queue the author started from the shelf is no draft.
func CancelDrafts(ctx context.Context, links BookLinks, shelf Shelf, logger *log.Logger, projectID string) error {
	drafts, err := links.Drafts(ctx, projectID)
	if err != nil {
		return err
	}
	for _, draft := range drafts {
		if err := shelf.CancelDraft(ctx, draft); err != nil {
			return err
		}
	}
	if len(drafts) > 0 {
		logger.Printf(`{"event":"obsidian_drafts_canceled","count":%d}`, len(drafts))
	}
	return nil
}

func (load ImportBooks) point(name string) error {
	if load.Failpoint == nil {
		return nil
	}
	return load.Failpoint(name)
}

func (load ImportBooks) now() time.Time {
	if load.Now == nil {
		return time.Now()
	}
	return load.Now()
}

// failureCode is the code of the failure of a write after the queue was made, as the problem of the request says it.
func failureCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return "auth.session_required"
	case errors.Is(err, auth.ErrEmailNotVerified):
		return "auth.email_unverified"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrNotLinked):
		return obsidian.CodeNotLinked
	}
	if exceeded, ok := quota.AsExceeded(err); ok {
		return exceeded.Code()
	}
	return "server.error"
}
