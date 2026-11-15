// SPDX-License-Identifier: AGPL-3.0-only

// Package obsidian holds the rules of the bridge of the desktop app with an Obsidian vault
// (specs/2026-11-13-desk-obsidian.md): what of a vault is read and what is skipped and why, the names and the paths a
// request may name, the header of a note and its cleaning, how a folder of notes becomes one text of Markdown the
// import of a book reads, how a note becomes a record of the materials of the author, and the hints of the folders.
// Nothing here touches a file, the database or HTTP.
package obsidian

import "errors"

// The codes of the refusals of the bridge (Data).
const (
	CodeNotLinked         = "obsidian.not_linked"
	CodeVaultMissing      = "obsidian.vault_missing"
	CodeBusy              = "obsidian.busy"
	CodeSlow              = "obsidian.slow"
	CodeTooLarge          = "obsidian.too_large"
	CodeFoldersOverlap    = "obsidian.folders_overlap"
	CodeChoiceUnavailable = "obsidian.choice_unavailable"
	// The codes of the writes of part A2 (specs/2026-11-14-desk-obsidian-books.md, Data).
	CodeQueueBusy       = "obsidian.queue_busy"
	CodeFolderChanged   = "obsidian.folder_changed"
	CodeFolderMissing   = "obsidian.folder_missing"
	CodeFolderLinked    = "obsidian.folder_linked"
	CodeBookNotLinked   = "obsidian.book_not_linked"
	CodeNotesUnread     = "obsidian.notes_unread"
	CodeNothingToApply  = "obsidian.nothing_to_apply"
	CodeLoadInterrupted = "obsidian.load_interrupted"
)

// The limits of a vault (OB0.1, OB2.1, OB3.1, OB4.1).
const (
	// MaxEntries and MaxDepth bound the look over a vault: past them the tree is cut, not refused.
	MaxEntries = 200_000
	MaxDepth   = 32
	// MaxNotes and MaxSelectedBytes bound what a preview reads of the chosen folders.
	MaxNotes         = 20_000
	MaxSelectedBytes = 200 << 20
	// MaxNoteBytes is the largest note that is read; a larger one is skipped and counted.
	MaxNoteBytes = 10 << 20
	// MaxFrontLines is how far the end of the header of a note may stand from its start.
	MaxFrontLines = 500
	// MaxTitleRunes is the longest title of a book or of a chapter the import takes as a heading.
	MaxTitleRunes = 120
)

// Reason is why a chosen folder is not linked (OB1.3).
type Reason string

const (
	ReasonDataDir    Reason = "data_dir"
	ReasonTooWide    Reason = "too_wide"
	ReasonNotFound   Reason = "not_found"
	ReasonNotFolder  Reason = "not_folder"
	ReasonUnreadable Reason = "unreadable"
)

// Skip is why an entry of a vault or a note is not read (OB0.2, OB2.1, OB3.1, OB3.4).
type Skip string

const (
	// SkipLink is a symbolic link, a junction or anything that is neither a regular file nor a directory.
	SkipLink Skip = "link"
	// SkipHidden is a name on a dot and, on Windows, a hidden or a system entry.
	SkipHidden Skip = "hidden"
	// SkipCloudOnly is a file that lives only in the cloud: reading it would download it.
	SkipCloudOnly Skip = "cloud_only"
	// SkipNotNote is a file that is not a note: no .md at the end of its name.
	SkipNotNote  Skip = "not_note"
	SkipEncoding Skip = "encoding"
	SkipTooBig   Skip = "too_big"
	SkipEmpty    Skip = "empty"
	// SkipService is a note of a plugin: a drawing of Excalidraw, a board of Kanban, a template.
	SkipService    Skip = "service"
	SkipNoName     Skip = "no_name"
	SkipLongName   Skip = "long_name"
	SkipUnreadable Skip = "unreadable"
)

// Skipped counts what is skipped by its reason.
type Skipped map[Skip]int

// Add counts the skips of other into skipped.
func (skipped Skipped) Add(other Skipped) {
	for skip, count := range other {
		skipped[skip] += count
	}
}

// Warning names what a preview says of a book or of a record (OB4.2, OB4.3).
type Warning string

const (
	WarningChaptersDiffer  Warning = "chapters_differ"
	WarningBookInNote      Warning = "book_in_note"
	WarningOrderByName     Warning = "order_by_name"
	WarningTitleCut        Warning = "title_cut"
	WarningListNote        Warning = "list_note"
	WarningSeveralSections Warning = "several_sections"
)

// ErrPath is a path of a request that is not a path of a vault this bridge reads (OB0.3).
var ErrPath = errors.New("obsidian: not a path of the vault")

// FolderError is a vault, or a folder of it, that does not open as a folder the bridge reads, by its reason (OB1.3). It
// names no path: an error of the file system is turned into it before anything logs it (OB0.4).
type FolderError struct{ Reason Reason }

func (err FolderError) Error() string {
	return "obsidian: the folder does not open: " + string(err.Reason)
}

// ReasonOf is the reason of an error of a folder; any other error is a folder that does not read.
func ReasonOf(err error) Reason {
	var folder FolderError
	if errors.As(err, &folder) {
		return folder.Reason
	}
	return ReasonUnreadable
}

// Overlap is a choice of folders where one book holds another or a book holds a folder linked to a book (OB4.1).
type Overlap struct {
	Outer, Inner string
}

func (Overlap) Error() string { return "obsidian: the folders overlap" }

// TooLarge is a choice that holds more notes or more bytes than a preview reads, or more entries than a look walks (OB4.1):
// the walk stopped past MaxEntries, and Notes and Bytes are then what it counted until it stopped.
type TooLarge struct {
	Notes, Bytes, Entries int64
}

func (TooLarge) Error() string { return "obsidian: the chosen folders are too large" }
