// SPDX-License-Identifier: AGPL-3.0-only

// Package obsidian is the bridge of the desktop app with a vault of Obsidian (specs/2026-11-13-desk-obsidian.md): the
// choice of the folder of a saga through the shell, the link and its removal, the look over the vault and the preview of
// what an import gives. Nothing is written into the saga and nothing into the vault; the only row it keeps is the link.
package obsidian

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/domain/obsidian"
)

var (
	// ErrNotFound is a saga that is not, or that the actor does not see.
	ErrNotFound = errors.New("obsidian: saga not found")
	// ErrForbidden is an actor whose role does not let him change the books of the saga.
	ErrForbidden = errors.New("obsidian: forbidden")
	// ErrNotLinked is a saga without a folder of a vault (obsidian.not_linked).
	ErrNotLinked = errors.New("obsidian: the saga is not linked")
	// ErrChoiceUnavailable is a choice of a folder without the shell to make it (obsidian.choice_unavailable).
	ErrChoiceUnavailable = errors.New("obsidian: no shell chooses a folder")
)

// VaultMissing is a linked vault that does not open now (obsidian.vault_missing), by its reason.
type VaultMissing struct{ Reason obsidian.Reason }

func (VaultMissing) Error() string { return "obsidian: the vault does not open" }

// Access is what an operation needs of the actor in the saga.
type Access int

const (
	// AccessRead is any member of the saga.
	AccessRead Access = iota
	// AccessWrite is a member who changes its books: an editor or above.
	AccessWrite
)

// Linked is a saga as the bridge sees it: its title and the path of its vault, "" without one.
type Linked struct {
	Title string
	Path  string
}

// Vaults keeps the link of a saga with the folder of its vault (obsidian_vaults; OB1.4, OB1.5).
type Vaults interface {
	// Vault reads the saga and its link for an actor with the access: ErrNotFound for a saga he does not see,
	// ErrForbidden for a role below the access.
	Vault(ctx context.Context, actorUserID, projectID string, access Access) (Linked, error)
	// Link keeps the path of the vault of a saga: a new row or a new path of the row (OB1.4).
	Link(ctx context.Context, projectID, vaultPath string) error
	// Unlink removes the link of a saga for an actor who changes its books (OB1.5); no row is no error.
	Unlink(ctx context.Context, actorUserID, projectID string) error
}

// BookFolder is a folder of the vault a book of the saga holds (specs/2026-11-14-desk-obsidian-books.md OK0.3, OK0.4): the
// path of the folder from the root of the vault, its book and its title. InTrash says the book lies in «Недавно удалённые»;
// Queued says no book holds it yet and its volume is in work of the queue QueueID.
type BookFolder struct {
	Folder    string
	BookID    string
	BookTitle string
	InTrash   bool
	Queued    bool
	QueueID   string
}

// Links are the links of the books and of the records of a saga with the folders and the notes of its vault: Books are the
// folders the books hold by the rule of reading of the store of the links (OK0.3), one a folder; Notes are what the states
// of the records of notes depend on (specs/2026-11-15-desk-obsidian-notes.md ON1.2) — the records whose names the keys are
// or that the notes are linked with, with their links, and how many records the saga keeps.
type Links interface {
	Books(ctx context.Context, projectID string) ([]BookFolder, error)
	Notes(ctx context.Context, projectID string, keys, notes []string) (obsidian.NoteFacts, error)
}

// NoLinks is the links of a saga of a test that keeps none: no book and no record is linked to the vault, no record kept.
type NoLinks struct{}

func (NoLinks) Books(context.Context, string) ([]BookFolder, error) { return nil, nil }
func (NoLinks) Notes(context.Context, string, []string, []string) (obsidian.NoteFacts, error) {
	return obsidian.NoteFacts{}, nil
}

// Visit is told of every entry of a folder the walk goes through and says whether the walk goes into it — a folder — and
// whether it stops.
type Visit func(dir string, found obsidian.Found) (enter, stop bool)

// Files opens the vaults of the author (OB0.2).
type Files interface {
	// Open opens the folder of a vault for one operation; a folder that does not open is obsidian.FolderError.
	Open(vaultPath string) (Vault, error)
}

// Vault is a vault open for one operation: the root of its folder, closed at its end.
type Vault interface {
	// Folder checks a folder a request names: every name of its path is a folder the walk enters, no link, no hidden
	// entry; otherwise obsidian.ErrPath.
	Folder(ctx context.Context, folder string) error
	// Walk visits the entries under folder, their links never followed, until visit stops it or ctx ends.
	Walk(ctx context.Context, folder string, visit Visit) error
	// Read reads a note; a note that does not read is skipped by its reason.
	Read(ctx context.Context, notePath string) ([]byte, obsidian.Skip)
	Close() error
}
