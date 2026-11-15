// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"path/filepath"
	"slices"

	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// maxExamples bounds the notes a warning names.
const maxExamples = 5

// Volumes reads the text of a book as the import queue reads the file of a volume (OB4.2): the chapters and their plain
// words, or the code of the refusal of the queue.
type Volumes interface {
	Check(fileName string, content []byte) Volume
}

// Volume is a text as the queue reads it: the title of the book and its chapters, or the refusal.
type Volume struct {
	Refusal  string
	Title    string
	Chapters []VolumeChapter
}

// VolumeChapter is a chapter the import makes and the signs of its plain words; Paragraphs are the plain words of its
// paragraphs and Markdown their italic and bold, as the update of a book compares them (OK0.4).
type VolumeChapter struct {
	Title      string
	Characters int
	Paragraphs []string
	Markdown   []string
}

// Helper names who reads the books of a saga for the app.
type Helper string

const (
	HelperNone Helper = "none"
	HelperOwn  Helper = "own"
	// HelperAccount is the helper «Аккаунт CanonKeeper» (specs/2026-11-13-desk-account-ai.md): the books are read on the
	// models of the site by the tier.
	HelperAccount Helper = "account"
)

// Reading is what the reading of a saga waits for (OB4.4): the helper of the app and the consent of the author.
type Reading struct {
	Helper  Helper
	Consent bool
}

// Saga is what a preview reads of the saga: what the book calls names and its reading.
type Saga interface {
	BookNames
	Reading(ctx context.Context, projectID string) (Reading, error)
}

// PreviewInput is the choice of a vault the author would load.
type PreviewInput struct {
	ActorUserID string
	ProjectID   string
	Choice      obsidian.Choice
}

// FolderState is what a folder of a book is to the saga (specs/2026-11-14-desk-obsidian-books.md OK0.3, OK0.4): new,
// linked to a book — Changed when «Обновить из файла» of the book by the glued text would change it, InTrash when the book
// lies in «Недавно удалённые» — or Queued, its volume in work of the queue QueueID.
type FolderState struct {
	Linked  bool
	BookID  string
	Title   string
	Changed bool
	InTrash bool
	Queued  bool
	QueueID string
}

// BookWarning is a warning of a book with the notes it names.
type BookWarning struct {
	Code  obsidian.Warning
	Notes []string
}

// BookPreview is a folder as the book the import makes of it (OB4.2).
type BookPreview struct {
	Folder      string
	Title       string
	Notes       []obsidian.GluedNote
	Chapters    []VolumeChapter
	Characters  int
	Refusal     string
	Warnings    []BookWarning
	Dropped     obsidian.Dropped
	Skipped     obsidian.Skipped
	State       FolderState
	Fingerprint string
	// Glued is the text the folder of the book glues into: what a load or an update uploads.
	Glued obsidian.Glued
	// volume is the chapters of the glue with their paragraphs, as the plan compares them.
	volume []VolumeChapter
}

// RecordPreview is a record a note is to give, with its state (OB4.3; specs/2026-11-15-desk-obsidian-notes.md ON1.2).
type RecordPreview struct {
	obsidian.Record
	obsidian.NoteState
	Folder string
}

// FolderPrint is a folder of materials with the fingerprint of the records of its notes: a load of the records names it
// (specs/2026-11-15-desk-obsidian-notes.md ON1.1).
type FolderPrint struct {
	Folder      string
	Fingerprint string
}

// MaterialsPreview is what the folders of materials are to give, with the fingerprint of the records of each folder.
type MaterialsPreview struct {
	Records []RecordPreview
	Skipped obsidian.Skipped
	Folders []FolderPrint
}

// Preview is previewObsidianImport (OB4): the books, the records, what is skipped, and what the reading needs to say.
type Preview struct {
	Books     []BookPreview
	Materials MaterialsPreview
	// NewChapters and NewCharacters are the chapters of new and changed books and their signs: what the helper reads.
	NewChapters   int
	NewCharacters int
	Reading       Reading
}

// PreviewImport reads the chosen folders of the vault of a saga and says what a load would give, writing nothing (OB4):
// the choice is checked before anything is read, the notes are counted before one is read, every note within its time,
// every folder of a book glued and read by the code of the queue, every note of materials as a record compared with the
// saga.
type PreviewImport struct {
	Vaults  Vaults
	Files   Files
	Links   Links
	Plans   Plans
	Saga    Saga
	Volumes Volumes
	Plain   obsidian.Plain
	Gate    *Gate
	Limits  Limits
}

func (preview PreviewImport) Execute(ctx context.Context, input PreviewInput) (Preview, error) {
	linked, err := preview.Vaults.Vault(ctx, input.ActorUserID, input.ProjectID, AccessWrite)
	if err != nil {
		return Preview{}, err
	}
	if linked.Path == "" {
		return Preview{}, ErrNotLinked
	}
	books, err := preview.Links.Books(ctx, input.ProjectID)
	if err != nil {
		return Preview{}, err
	}
	folders := make([]string, 0, len(books))
	for _, book := range books {
		folders = append(folders, book.Folder)
	}
	choice, err := input.Choice.Check(folders)
	if err != nil {
		return Preview{}, err
	}
	// OB0.1: the reading, the glue of the books and the records go within the time of the operation, in its place.
	return gated(ctx, preview.Gate, input.ProjectID, preview.Limits.Read, func(ctx context.Context) (Preview, error) {
		read, err := preview.read(ctx, linked.Path, choice, choice.Folders())
		if err != nil {
			return Preview{}, err
		}
		result := Preview{}
		byFolder := map[string]BookFolder{}
		for _, book := range books {
			byFolder[book.Folder] = book
		}
		for _, folder := range choice.Books {
			if err := ctx.Err(); err != nil {
				return Preview{}, err
			}
			book := preview.book(folder, nameOf(folder, linked.Path), read.notes[folder], read.skipped[folder])
			if book.State, err = preview.state(ctx, input.ProjectID, byFolder, book); err != nil {
				return Preview{}, err
			}
			if !book.State.Linked && !book.State.Queued || book.State.Changed {
				result.NewChapters += len(book.Chapters)
				result.NewCharacters += book.Characters
			}
			result.Books = append(result.Books, book)
		}
		if result.Materials, err = preview.materials(ctx, input.ProjectID, choice, read); err != nil {
			return Preview{}, err
		}
		if result.Reading, err = preview.Saga.Reading(ctx, input.ProjectID); err != nil {
			return Preview{}, err
		}
		return result, nil
	})
}

// nameOf is the name a folder gives its book when no note names it: its own, the name of the folder of the vault for its root.
func nameOf(folder, vaultPath string) string {
	if folder == obsidian.Root {
		return filepath.Base(vaultPath)
	}
	return obsidian.Base(folder)
}

// state is what the folder of a book is to the saga (OK0.3, OK0.4): a folder a live book holds changed when «Обновить из
// файла» of the book by the glued text would change it — the plan of the update against the chapters of the book, not a
// remembered fingerprint, so the return of an update, an edit of a chapter in the app and an update from a file are heard.
func (preview PreviewImport) state(ctx context.Context, projectID string, folders map[string]BookFolder, book BookPreview) (FolderState, error) {
	link, ok := folders[book.Folder]
	switch {
	case !ok:
		return FolderState{}, nil
	case link.Queued:
		return FolderState{Queued: true, QueueID: link.QueueID}, nil
	}
	state := FolderState{Linked: true, BookID: link.BookID, Title: link.BookTitle, InTrash: link.InTrash}
	if link.InTrash || book.Refusal != "" {
		return state, nil
	}
	changed, err := preview.Plans.Changes(ctx, projectID, link.BookID, book.volume)
	state.Changed = changed
	return state, err
}

// gathered are the notes of each chosen folder and what of it was not read, by the reasons.
type gathered struct {
	notes   map[string][]obsidian.Note
	skipped map[string]obsidian.Skipped
}

// read walks the chosen folders without a link followed, counts their entries and notes against the limits of a preview
// before one is read — a walk past the entries of a look is obsidian.too_large, not a book cut short —, and reads each
// note within its time. The notes of a folder are the notes it owns (obsidian.Choice.Owner): a chosen folder inside it
// and what the author left out of it are not among them. A note that does not read is counted by its reason.
//
// walk are the folders whose notes are read: every chosen folder for a preview, the folder of a book for its load or update.
func (preview PreviewImport) read(ctx context.Context, vaultPath string, choice obsidian.Choice, walk []string) (gathered, error) {
	vault, err := preview.Files.Open(vaultPath)
	if err != nil {
		return gathered{}, VaultMissing{Reason: obsidian.ReasonOf(err)}
	}
	defer vault.Close()
	chosen := choice.Folders()
	var gone []string
	for _, folder := range walk {
		if err := vault.Folder(ctx, folder); err != nil {
			gone = append(gone, folder)
		}
	}
	if len(gone) > 0 {
		return gathered{}, FoldersGone{Folders: gone}
	}
	type found struct{ folder, path string }
	var notes []found
	skipped := map[string]obsidian.Skipped{}
	var count, bytes, entries int64
	for _, folder := range walk {
		skipped[folder] = obsidian.Skipped{}
		err := vault.Walk(ctx, folder, func(dir string, entry obsidian.Found) (bool, bool) {
			entries++
			if entries > obsidian.MaxEntries {
				return false, true
			}
			entry.Name = obsidian.NFC(entry.Name)
			path := obsidian.Join(dir, entry.Name)
			if owner, _, ok := choice.Owner(path); !ok || owner != folder {
				return false, false
			}
			kind, skip := obsidian.Classify(entry)
			switch {
			case kind == obsidian.KindFolder:
				return !slices.Contains(chosen, path), false
			case kind == obsidian.KindSkipped:
				skipped[folder][skip]++
			default:
				count, bytes = count+1, bytes+entry.Size
				notes = append(notes, found{folder: folder, path: path})
			}
			return false, false
		})
		if err != nil {
			return gathered{}, err
		}
	}
	if count > obsidian.MaxNotes || bytes > obsidian.MaxSelectedBytes || entries > obsidian.MaxEntries {
		return gathered{}, obsidian.TooLarge{Notes: count, Bytes: bytes, Entries: entries}
	}
	read := gathered{notes: map[string][]obsidian.Note{}, skipped: skipped}
	for _, note := range notes {
		data, skip := within(ctx, preview.Limits.Note, func(ctx context.Context) ([]byte, error) {
			data, skip := vault.Read(ctx, note.path)
			if skip != "" {
				return nil, skipError(skip)
			}
			return data, nil
		})
		if skip != nil {
			if reason, ok := skip.(skipError); ok {
				skipped[note.folder][obsidian.Skip(reason)]++
				continue
			}
			return gathered{}, skip
		}
		parsed, reason := obsidian.ReadNote(note.path, data)
		if reason != "" {
			skipped[note.folder][reason]++
			continue
		}
		read.notes[note.folder] = append(read.notes[note.folder], parsed)
	}
	return read, nil
}

// skipError carries the reason a note did not read through within.
type skipError obsidian.Skip

func (skipError) Error() string { return "obsidian: the note does not read" }

// book glues the notes of a folder and reads the text as the queue reads a volume.
func (preview PreviewImport) book(folder, name string, notes []obsidian.Note, skipped obsidian.Skipped) BookPreview {
	glued := obsidian.Glue(folder, name, notes)
	volume := preview.Volumes.Check(glued.FileName, []byte(glued.Text))
	book := BookPreview{
		Folder: folder, Title: glued.Title, Notes: glued.Notes, Chapters: volume.Chapters, Refusal: volume.Refusal,
		Dropped: glued.Dropped, Skipped: obsidian.Skipped{}, Fingerprint: glued.Fingerprint, Glued: glued, volume: volume.Chapters,
	}
	// OB4.2: the book is named as the import names it.
	if volume.Refusal == "" && volume.Title != "" {
		book.Title = volume.Title
	}
	book.Skipped.Add(skipped)
	book.Skipped.Add(glued.Skipped)
	titles := make([]string, 0, len(volume.Chapters))
	for _, chapter := range volume.Chapters {
		titles = append(titles, chapter.Title)
		book.Characters += chapter.Characters
	}
	if volume.Refusal == "" {
		if differ := obsidian.ChaptersDiffer(glued.Notes, titles); len(differ) > 0 {
			book.Warnings = append(book.Warnings, BookWarning{Code: obsidian.WarningChaptersDiffer, Notes: examples(differ)})
		}
	}
	var inNote []string
	for _, note := range glued.Notes {
		if note.InNote {
			inNote = append(inNote, note.Path)
		}
	}
	if len(inNote) > 0 {
		book.Warnings = append(book.Warnings, BookWarning{Code: obsidian.WarningBookInNote, Notes: examples(inNote)})
	}
	if glued.OrderByName {
		book.Warnings = append(book.Warnings, BookWarning{Code: obsidian.WarningOrderByName})
	}
	if glued.TitleCut || len(glued.Cut) > 0 {
		book.Warnings = append(book.Warnings, BookWarning{Code: obsidian.WarningTitleCut, Notes: examples(glued.Cut)})
	}
	return book
}

func examples(notes []string) []string {
	return notes[:min(len(notes), maxExamples)]
}

// materials reads the notes of the folders of materials as records and compares them with the saga (OB4.3) by the one
// rule of the states of the records of notes (specs/2026-11-15-desk-obsidian-notes.md ON1.2) on the reads a load makes again:
// the records of their names and of their links, the names of the book. A choice past the records of a load or of a saga is
// obsidian.too_large (ON1.7).
func (preview PreviewImport) materials(ctx context.Context, projectID string, choice obsidian.Choice, read gathered) (MaterialsPreview, error) {
	result := MaterialsPreview{Skipped: obsidian.Skipped{}}
	records, folders, skipped := recordsOf(choice, read, preview.Plain)
	result.Skipped.Add(skipped)
	result.Folders = folderPrints(choice, records, folders)
	if len(records) == 0 {
		return result, nil
	}
	states, facts, err := preview.states(ctx, projectID, records)
	if err != nil {
		return MaterialsPreview{}, err
	}
	creates := 0
	for index, record := range records {
		if obsidian.Creates(states[index].State) {
			creates++
		}
		result.Records = append(result.Records, RecordPreview{Record: record, NoteState: states[index], Folder: folders[index]})
	}
	if err := obsidian.CheckRecords(len(records), facts.Total, creates); err != nil {
		return MaterialsPreview{}, err
	}
	return result, nil
}

// states are the states of the records of a batch by the facts of the saga and the names of its book.
func (preview PreviewImport) states(ctx context.Context, projectID string, records []obsidian.Record) ([]obsidian.NoteState, obsidian.NoteFacts, error) {
	names, paths, keys := namesOf(records)
	facts, err := preview.Links.Notes(ctx, projectID, keys, paths)
	if err != nil {
		return nil, obsidian.NoteFacts{}, err
	}
	book, err := preview.Saga.BookNames(ctx, projectID, names)
	if err != nil {
		return nil, obsidian.NoteFacts{}, err
	}
	return obsidian.NoteStates(records, facts, book, nil), facts, nil
}

// recordsOf are the records of the notes of the folders of materials in the order of the folders of the choice and of the
// paths of their notes, the folder of each, and what of them did not give a record, by the reasons.
func recordsOf(choice obsidian.Choice, read gathered, plain obsidian.Plain) ([]obsidian.Record, []string, obsidian.Skipped) {
	var records []obsidian.Record
	var folders []string
	skipped := obsidian.Skipped{}
	for _, folder := range choice.Materials {
		skipped.Add(read.skipped[folder.Path])
		notes := slices.Clone(read.notes[folder.Path])
		slices.SortFunc(notes, func(left, right obsidian.Note) int {
			switch {
			case obsidian.NaturalLess(left.Path, right.Path):
				return -1
			case obsidian.NaturalLess(right.Path, left.Path):
				return 1
			}
			return 0
		})
		for _, note := range notes {
			record, skip := obsidian.Material(note, folder.Kind, plain)
			if skip != "" {
				skipped[skip]++
				continue
			}
			records, folders = append(records, record), append(folders, folder.Path)
		}
	}
	return records, folders, skipped
}

// folderPrints are the fingerprints of the records of each folder of materials of the choice.
func folderPrints(choice obsidian.Choice, records []obsidian.Record, folders []string) []FolderPrint {
	prints := make([]FolderPrint, 0, len(choice.Materials))
	for _, folder := range choice.Materials {
		var own []obsidian.Record
		for index, record := range records {
			if folders[index] == folder.Path {
				own = append(own, record)
			}
		}
		prints = append(prints, FolderPrint{Folder: folder.Path, Fingerprint: obsidian.RecordsFingerprint(own)})
	}
	return prints
}

// namesOf are the names a book may know of the records — their names and their other names — by their keys, the paths of
// their notes and the keys of their names.
func namesOf(records []obsidian.Record) (names, paths, keys []string) {
	for _, record := range records {
		key := sagamemory.GlossaryKey(record.Draft.Name)
		paths, keys, names = append(paths, record.Path), append(keys, key), append(names, key)
		for _, alias := range record.Aliases {
			names = append(names, sagamemory.GlossaryKey(alias))
		}
	}
	return names, paths, keys
}
