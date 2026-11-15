// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"log"
	"time"

	"canonkeeper/backend/internal/domain/obsidian"
)

// FolderLinked is a folder another live book of the saga holds (obsidian.folder_linked).
type FolderLinked struct {
	Folder string
	BookID string
}

func (FolderLinked) Error() string { return "obsidian: another book holds the folder" }

// BookFolderInput is linkBookToObsidianFolder: a book of the saga and the folder of its vault, from its root.
type BookFolderInput struct {
	ActorUserID string
	ProjectID   string
	BookID      string
	Folder      string
}

// LinkBookFolder is linkBookToObsidianFolder (specs/2026-11-14-desk-obsidian-books.md OK2.3): a live book of the saga is
// linked with a folder of its vault without a load — «это та же книга». The folder is none another live book holds, lies in
// none and holds none (the rule of a choice, OB4.1), and it is a folder of the vault now, read under the gate; the book has
// one link, the new one in the place of the one before.
type LinkBookFolder struct {
	Vaults Vaults
	Files  Files
	Links  BookLinks
	Shelf  Shelf
	Gate   *Gate
	Limits Limits
	Logger *log.Logger
	Now    func() time.Time
}

func (link LinkBookFolder) Execute(ctx context.Context, input BookFolderInput) error {
	linked, err := link.Vaults.Vault(ctx, input.ActorUserID, input.ProjectID, AccessWrite)
	if err != nil {
		return err
	}
	if linked.Path == "" {
		return ErrNotLinked
	}
	folder, err := obsidian.Relative(input.Folder)
	if err != nil {
		return err
	}
	operation, err := enter(ctx, link.Gate, input.ProjectID)
	if err != nil {
		return err
	}
	defer operation.end()
	folders, err := freeFolders(operation, link.Limits, link.Links, link.Shelf, link.Logger, input.ActorUserID, input.ProjectID)
	if err != nil {
		return err
	}
	var others []string
	for _, held := range folders {
		if held.Book != nil && held.Book.BookID == input.BookID {
			continue
		}
		if held.Book != nil && held.Folder == folder {
			return FolderLinked{Folder: folder, BookID: held.Book.BookID}
		}
		others = append(others, held.Folder)
	}
	if _, err := (obsidian.Choice{Books: []string{folder}}).Check(others); err != nil {
		return err
	}
	if _, err := readIn(operation, link.Limits.Read, func(ctx context.Context) (struct{}, error) {
		vault, err := link.Files.Open(linked.Path)
		if err != nil {
			return struct{}{}, VaultMissing{Reason: obsidian.ReasonOf(err)}
		}
		defer vault.Close()
		if err := vault.Folder(ctx, folder); err != nil {
			return struct{}{}, FolderMissing{Folder: folder}
		}
		return struct{}{}, nil
	}); err != nil {
		return err
	}
	writes, cancel := operation.writes(link.Limits.Write)
	defer cancel()
	return link.Links.LinkBook(writes, input.ProjectID, input.BookID, folder, link.now())
}

func (link LinkBookFolder) now() time.Time {
	if link.Now == nil {
		return time.Now()
	}
	return link.Now()
}

// UnlinkBookFolder is unlinkBookFromObsidianFolder (OK2.3): the book — live or in «Недавно удалённые», whose folder would
// otherwise answer in_trash for seven days — is linked with no folder any more. The book and the vault stay as they are.
type UnlinkBookFolder struct {
	Vaults Vaults
	Links  BookLinks
}

func (unlink UnlinkBookFolder) Execute(ctx context.Context, input BookFolderInput) error {
	linked, err := unlink.Vaults.Vault(ctx, input.ActorUserID, input.ProjectID, AccessWrite)
	if err != nil {
		return err
	}
	if linked.Path == "" {
		return ErrNotLinked
	}
	return unlink.Links.UnlinkBook(ctx, input.ProjectID, input.BookID)
}
