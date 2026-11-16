// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The ports of the export of the map of a saga and of the notes of its chapters into a folder of its vault
// (specs/2026-11-15-desk-obsidian-export.md).

// ExportStart is what the transaction that starts an export read (OE1.1): the title of the saga, the path of its vault and
// the folder of its export from the root of the vault, "" before the first.
type ExportStart struct {
	Title     string
	VaultPath string
	ExportDir string
}

// Exports keep the link of a saga with the folder of its export (obsidian_vaults) and the answers of the exports by their keys.
type Exports interface {
	// BeginExport is the one short transaction an export starts with (OE1.1): the saga alive, the proof of the session, the
	// role of an editor, the link of the vault.
	BeginExport(ctx context.Context, actorUserID, projectID string, proof auth.SessionWriteProof) (ExportStart, error)
	// KeepExportDir writes the folder a first export chose, before the first file (OE1.5); ErrNotLinked when the link went or
	// its path changed since the start.
	KeepExportDir(ctx context.Context, projectID, vaultPath, dir string) error
	// Linked says the link of the saga is the one the export started with (OE1.2): the same path of the vault and the folder
	// of this export — a link taken off and given again, even to the same path, keeps no folder.
	Linked(ctx context.Context, projectID, vaultPath, dir string) (bool, error)
	// EndExport writes the time of an export that ended, while the link is the one it started with; false when it is not.
	EndExport(ctx context.Context, projectID, vaultPath, dir string, at time.Time) (bool, error)
	// ExportReplay is the answer kept for the key: nil for none, ErrReplayConflict for the key of another request.
	ExportReplay(ctx context.Context, key ReplayKey) (*Exported, error)
	// KeepExport keeps the answer of an export that ended for its key (OE1.1).
	KeepExport(ctx context.Context, key ReplayKey, exported Exported) error
}

// ErrNodeGone is a node of the map whose card is not there: the map changed between the reads.
var ErrNodeGone = errors.New("obsidian: the card of a node of the map is not there")

// SagaMap reads what an export writes of a saga by the reads of the pages (OE1.6, OE2.3): the map as the page of the map
// reads it — the Job of the cards queued when it is behind them —, the card of a node as the page shows it, and the
// chapters of the published text with their fresh cards.
type SagaMap interface {
	Map(ctx context.Context, actorUserID, projectID string) (sagamemory.MapDocument, error)
	// Node is the card of a node of the map, ErrNodeGone for none.
	Node(ctx context.Context, actorUserID, projectID, key string) (sagamemory.MapCardDocument, error)
	Chapters(ctx context.Context, projectID string) (*sagamemory.Version, *sagamemory.CardSet, error)
}

// ExportFolder is what a folder of the vault the export would take is.
type ExportFolder int

const (
	// ExportFolderMissing is no folder there: the export makes it.
	ExportFolderMissing ExportFolder = iota
	// ExportFolderPresent is a folder there.
	ExportFolderPresent
	// ExportFolderTaken is a file or a link at the name of the folder.
	ExportFolderTaken
	// ExportFolderBare is a folder with nothing in it but the folder of the app without a manifest: what a first run cut
	// between the making of its folder and its first manifest leaves (OE2.8). Nothing of anyone is in it.
	ExportFolderBare
)

// Writers open the vaults of the author for an export.
type Writers interface {
	// OpenWriter opens the folder of a vault for one export; a folder that does not open is obsidian.FolderError.
	OpenWriter(vaultPath string) (VaultWriter, error)
}

var (
	// ErrOccupied is a path of a new reference a file holds right before its rename (OE1.4).
	ErrOccupied = errors.New("obsidian: a file holds the path")
	// ErrChangedMeanwhile is a file that is not what the plan read right before its replacement or its move (OE1.4).
	ErrChangedMeanwhile = errors.New("obsidian: the file changed since the plan read it")
	// ErrGoneMeanwhile is a file to move that went before its move.
	ErrGoneMeanwhile = errors.New("obsidian: the file went since the plan read it")
)

// ExportSkipped is a file a write leaves by its reason (OE1.7).
type ExportSkipped struct{ Skip obsidian.ExportSkip }

func (skipped ExportSkipped) Error() string {
	return "obsidian: the file is skipped: " + string(skipped.Skip)
}

// VaultWriter is a vault open for one export (OE1.3): every path goes name after name, no link followed, and nothing is
// written out of the folder of the export; no error names a path.
type VaultWriter interface {
	// Folder says what a folder of the vault is — missing, bare, a folder or taken —; obsidian.ErrExportDirUnsafe for a link or
	// a file on the way to it.
	Folder(ctx context.Context, dir string) (ExportFolder, error)
	// Manifest reads the manifest of a folder of the vault: false for none, obsidian.ErrExportManifestUnreadable for one
	// that does not read.
	Manifest(ctx context.Context, dir string) ([]byte, bool, error)
	// Open makes dir the folder of the export, the folders on the way made: obsidian.ErrExportDirUnsafe for a link or a file
	// on it. Every other method but Folder and Manifest goes under it, by paths from it.
	Open(ctx context.Context, dir string) error
	// Stat says whether a file lies at a path, and what of it does not read.
	Stat(ctx context.Context, path string) (exists bool, skip obsidian.ExportSkip)
	// Read reads a reference; a file that does not read is skipped by its reason.
	Read(ctx context.Context, path string) ([]byte, obsidian.ExportSkip)
	// Notes are the paths of the notes of the folder, the folder of the app and _Удалено/ left out.
	Notes(ctx context.Context) ([]string, error)
	// Clean removes the temporary files of the runs before (OE1.3).
	Clean(ctx context.Context) error
	// Write writes a text at a path through a temporary file renamed into its place (OE1.3, OE1.4): for a new reference
	// (expect "") ErrOccupied when a file lies there right before the rename; for a replacement ErrChangedMeanwhile when the
	// file there is not of the fingerprint expect; ExportSkipped for a file or a folder that does not let it through.
	Write(ctx context.Context, path string, text []byte, expect string) error
	// Move moves the file at a path, of the fingerprint expect, into _Удалено/ under the first free of names and gives its
	// new path (OE2.5, OE2.7): ErrChangedMeanwhile, ErrGoneMeanwhile or ExportSkipped otherwise.
	Move(ctx context.Context, path string, names []string, expect string) (string, error)
	// Keep writes the manifest through a temporary file renamed into its place (OE2.8).
	Keep(ctx context.Context, manifest []byte) error
	Close() error
}

// ExportGroup is a group of the references of an export (OE1.8): how many, and the paths of the first MaxNames from the
// folder of the export.
type ExportGroup struct {
	Count int      `json:"count"`
	Names []string `json:"names"`
}

func (group *ExportGroup) add(path string) {
	group.Count++
	if len(group.Names) < MaxNames {
		group.Names = append(group.Names, path)
	}
}

// Exported is the answer of an export (OE1.8), the same for a replay of the key of one that ended: the folder of the export
// from the root of the vault, the time, whether it ended, and the references by what became of them — created, updated,
// unchanged, moved into _Удалено/, not updated because the author changed them, skipped by the reasons.
type Exported struct {
	Folder    string                              `json:"folder"`
	At        time.Time                           `json:"at"`
	Complete  bool                                `json:"complete"`
	Created   ExportGroup                         `json:"created"`
	Updated   ExportGroup                         `json:"updated"`
	Unchanged ExportGroup                         `json:"unchanged"`
	Moved     ExportGroup                         `json:"moved"`
	Kept      ExportGroup                         `json:"kept"`
	Skipped   map[obsidian.ExportSkip]ExportGroup `json:"skipped"`
}
