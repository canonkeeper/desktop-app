// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"path/filepath"

	"canonkeeper/backend/internal/domain/obsidian"
)

// ScanInput asks for the look over the vault of a saga.
type ScanInput struct {
	ActorUserID string
	ProjectID   string
}

// ScannedFolder is a folder of the tree with the hint of its kind and the book it is linked to, if any.
type ScannedFolder struct {
	obsidian.Folder
	Depth int
	Hint  *obsidian.Hint
	Book  *BookFolder
}

// Scanned is the tree of a vault (OB2.1): the folders with notes, what was skipped by its reasons, and whether the look
// stopped at a limit with the entries it counted.
type Scanned struct {
	Folders   []ScannedFolder
	Truncated bool
	Entries   int
	Skipped   obsidian.Skipped
}

// Scan is scanObsidianVault (OB2): the tree of the vault of a saga, its entries counted without a note read, within the
// time of a look and the gate of the process. It writes nothing and gives no text of a note.
type Scan struct {
	Vaults Vaults
	Files  Files
	Links  Links
	Gate   *Gate
	Limits Limits
}

func (scan Scan) Execute(ctx context.Context, input ScanInput) (Scanned, error) {
	linked, err := scan.Vaults.Vault(ctx, input.ActorUserID, input.ProjectID, AccessWrite)
	if err != nil {
		return Scanned{}, err
	}
	if linked.Path == "" {
		return Scanned{}, ErrNotLinked
	}
	books, err := scan.Links.Books(ctx, input.ProjectID)
	if err != nil {
		return Scanned{}, err
	}
	tree, err := gated(ctx, scan.Gate, input.ProjectID, scan.Limits.Scan, func(ctx context.Context) (*obsidian.Tree, error) {
		vault, err := scan.Files.Open(linked.Path)
		if err != nil {
			return nil, VaultMissing{Reason: obsidian.ReasonOf(err)}
		}
		defer vault.Close()
		tree := obsidian.NewTree(filepath.Base(linked.Path))
		return tree, vault.Walk(ctx, obsidian.Root, tree.Add)
	})
	if err != nil {
		return Scanned{}, err
	}
	byFolder := map[string]*BookFolder{}
	for index := range books {
		byFolder[books[index].Folder] = &books[index]
	}
	scanned := Scanned{Truncated: tree.Truncated, Entries: tree.Entries(), Skipped: tree.Skipped}
	for _, folder := range tree.Folders() {
		depth := obsidian.Depth(folder.Path)
		book := byFolder[folder.Path]
		scanned.Folders = append(scanned.Folders, ScannedFolder{
			Folder: folder, Depth: depth, Book: book,
			Hint: obsidian.HintOf(folder.Name, depth, folder.BookNote, book != nil),
		})
	}
	return scanned, nil
}
