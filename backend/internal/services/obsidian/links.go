// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"time"
)

// BookChoice is the choice of a book kept with its link (specs/2026-11-14-desk-obsidian-books.md OK1.3, OK2.1): the paths
// left out under its folder and the folders inside it chosen otherwise at its load — the glue of an update leaves them out
// as the load did.
type BookChoice struct {
	Excluded []string
	Nested   []string
}

// LinkedBook is a book of the saga linked with a folder of the vault.
type LinkedBook struct {
	BookID string
	Title  string
	Choice BookChoice
	// LoadedAt is the end of the last successful Job of the volume of the link or of an update from the vault; nil for none.
	LoadedAt *time.Time
}

// HeldFolder is a folder of the vault by the rule of reading of the links (OK0.3): Book is the live book of its latest link
// that acts; without one, Trash is a book of it in «Недавно удалённые», then Queue the queue whose volume of it is in work.
// Others are the live books of the folder whose links act while another holds it (OK2.6). A folder of inert links only is
// not among the folders.
type HeldFolder struct {
	Folder string
	Book   *LinkedBook
	Trash  *LinkedBook
	Queue  string
	Others []LinkedBook
}

// VolumeLink is the link of a volume of a queue the bridge made with its folder (OK1.3): Fingerprint is the SHA-256 of the
// glued text the volume carries.
type VolumeLink struct {
	Folder      string
	ItemID      string
	Fingerprint string
	Choice      BookChoice
}

// Draft is a queue of the bridge that never started — in uploading or draft, made under the key of the step «queue» of a
// load — with the author who made it (OK1.4).
type Draft struct {
	ProjectID   string
	QueueID     string
	ActorUserID string
}

// BookLinks keep the links of the books of a saga with the folders of its vault (obsidian_books, OK0.2): what holds a
// folder is one rule of reading (OK0.3), which Books of Links reads too.
type BookLinks interface {
	Links
	// Folders are the folders of the saga by the rule of reading.
	Folders(ctx context.Context, projectID string) ([]HeldFolder, error)
	// AddVolumes keeps the links of the volumes of a queue with their folders in one transaction.
	AddVolumes(ctx context.Context, projectID string, volumes []VolumeLink, at time.Time) error
	// LinkBook links a live book with a folder without a load; whatever links the book had go (OK2.3).
	LinkBook(ctx context.Context, projectID, bookID, folder string, at time.Time) error
	// UnlinkBook removes the links of a book, live or in «Недавно удалённые» (OK2.3); no link is no error.
	UnlinkBook(ctx context.Context, projectID, bookID string) error
	// Drafts are the drafts of the bridge of the saga, of every saga for "" (OK1.4).
	Drafts(ctx context.Context, projectID string) ([]Draft, error)
}

// Plans say whether «Обновить из файла» of a live book by the chapters of a text would change it — the plan the update
// writes by (OK0.4, OK2.2).
type Plans interface {
	Changes(ctx context.Context, projectID, bookID string, chapters []VolumeChapter) (bool, error)
}

// holderOf is the folder of the book a saga holds, if it holds one.
func holderOf(folders []HeldFolder, bookID string) (HeldFolder, bool) {
	for _, folder := range folders {
		if folder.Book != nil && folder.Book.BookID == bookID {
			return folder, true
		}
	}
	return HeldFolder{}, false
}

// booksOf are the folders as the look and the preview read them: one record a folder.
func booksOf(folders []HeldFolder) []BookFolder {
	books := make([]BookFolder, 0, len(folders))
	for _, folder := range folders {
		switch {
		case folder.Book != nil:
			books = append(books, BookFolder{Folder: folder.Folder, BookID: folder.Book.BookID, BookTitle: folder.Book.Title})
		case folder.Trash != nil:
			books = append(books, BookFolder{Folder: folder.Folder, BookID: folder.Trash.BookID, BookTitle: folder.Trash.Title, InTrash: true})
		case folder.Queue != "":
			books = append(books, BookFolder{Folder: folder.Folder, Queued: true, QueueID: folder.Queue})
		}
	}
	return books
}

// BooksOfFolders are the folders the books hold, one record a folder (Links.Books), by the rule of reading of Folders.
func BooksOfFolders(ctx context.Context, links BookLinks, projectID string) ([]BookFolder, error) {
	folders, err := links.Folders(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return booksOf(folders), nil
}

// ApplyKeyPrefix begins the key of every update of a book the bridge applies (OK2.5): the end of the last one is when the
// book was last loaded from the vault (OK2.6).
const ApplyKeyPrefix = "obsidian-apply-"

// QueueKeyPrefix begins the key of every queue the bridge makes (the step «queue» of a load, DerivedKey): it names the drafts
// of the bridge, with the links of their volumes or without them (OK1.4).
const QueueKeyPrefix = "obsidian-queue-"
