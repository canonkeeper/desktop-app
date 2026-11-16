// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"slices"
	"strings"
)

// The plan of a run of an export (OE1.4, OE2.5–OE2.8): what the manifest names, what the run wants and what the disk holds
// give what each reference becomes. A reference of the app is replaced only while the disk holds what the manifest says
// was written; one the author changed is left as it is, or with his word moved into _Удалено/ — never lost.

// DiskFile is what a run saw of a path of the manifest: no file there; a file that does not read, by the reason; or the
// fingerprint of what it read and the SHA-256 of its bytes.
type DiskFile struct {
	Missing bool
	Skip    ExportSkip
	Print   string
	Sum     string
}

// ExportStep is what a run does with a reference.
type ExportStep string

const (
	// StepCreate writes a new reference — a new key, or a reference whose file went — where no file is.
	StepCreate ExportStep = "create"
	// StepUpdate replaces a reference of the app whose content changed.
	StepUpdate ExportStep = "update"
	// StepUnchanged leaves a reference whose content did not change: it is neither read nor written.
	StepUnchanged ExportStep = "unchanged"
	// StepStay leaves the reference of a chapter without a fresh card as it is (OE2.7).
	StepStay ExportStep = "stay"
	// StepKeep leaves a reference the author changed, by his word not given: in its place, named (OE2.5, OE2.7).
	StepKeep ExportStep = "keep"
	// StepReplace moves a reference the author changed into _Удалено/ by his word and writes the fresh one (OE2.5).
	StepReplace ExportStep = "replace"
	// StepMove moves the reference of a node or a chapter that went into _Удалено/ (OE2.7).
	StepMove ExportStep = "move"
	// StepForget drops from the manifest a reference that went whose file went too.
	StepForget ExportStep = "forget"
	// StepSkip leaves a reference that did not read, by its reason (OE1.7).
	StepSkip ExportStep = "skip"
)

// ExportAction is a step of a run: the reference as the manifest names it (the path where it lies), and for a write its
// text; Expect is the fingerprint the disk must hold right before a replacement or a move (OE1.4).
type ExportAction struct {
	Step   ExportStep
	File   ManifestFile
	Text   []byte
	Expect string
	Skip   ExportSkip
}

// Wanted is a reference a run writes with its path, from the folder of the export, and its fresh text.
type Wanted struct {
	ID   FileID
	Path string
	Text []byte
}

// ExportReads are the paths of the manifest a run reads (OE2.5): a reference that went, and a reference whose fresh text is
// not what the manifest says was written — a reference whose content did not change is not read. missing are the paths the
// disk holds no file at.
func ExportReads(manifest Manifest, wanted []Wanted, stay []FileID, missing map[string]bool) []string {
	fresh := map[FileID]Wanted{}
	for _, file := range wanted {
		fresh[file.ID] = file
	}
	var reads []string
	for _, file := range manifest.Files {
		if missing[file.Path] || slices.Contains(stay, file.ID()) {
			continue
		}
		if wanted, ok := fresh[file.ID()]; ok && ExportSum(wanted.Text) == file.Sum {
			continue
		}
		reads = append(reads, file.Path)
	}
	return reads
}

// Gone are the references of the manifest a run does not write any more: their node is off the map or their chapter out
// of the saga. A chapter without a fresh card stays.
func Gone(manifest Manifest, files []ExportFile, stay []FileID) []ManifestFile {
	wanted := map[FileID]bool{}
	for _, file := range files {
		wanted[file.ID] = true
	}
	var gone []ManifestFile
	for _, file := range manifest.Files {
		if !wanted[file.ID()] && !slices.Contains(stay, file.ID()) {
			gone = append(gone, file)
		}
	}
	return gone
}

// Moves says whether a reference that went leaves its place in a run: not changed by the author, or changed and his word
// given; a reference that did not read stays.
func Moves(file ManifestFile, disk DiskFile, replaceChanged bool) bool {
	return !disk.Missing && disk.Skip == "" && (disk.Print == file.Print || replaceChanged)
}

// AssignPaths gives every reference of a run its path from the folder of the export (OE2.1, OE2.6): the path the manifest
// keeps for its key, else the name of its first export in its folder — the folder of its book for a chapter — with « (2)»,
// « (3)» while the path is held, by another new reference of the run (the case ignored) or by occupied. occupied says what
// the disk and the manifest hold at a path after the references that went left: the run frees their names first (OE1.4).
// The folder of a new book is named by its title, « (2)» after a book of the same title, in the order of the books; the
// manifest keeps the folder of a book it knows.
func AssignPaths(manifest Manifest, files []ExportFile, books []ExportBook, occupied func(string) bool) (map[FileID]string, []ManifestBook) {
	folders := map[string]string{}
	taken := map[string]bool{}
	for _, book := range manifest.Books {
		folders[book.Book] = book.Folder
		taken[FoldKey(book.Folder)] = true
	}
	kept := map[FileID]string{}
	for _, file := range manifest.Files {
		kept[file.ID()] = file.Path
	}
	used := map[string]bool{}
	for _, file := range files {
		if file.ID.Kind == ExportChapter {
			used[file.Book] = true
		}
	}
	for _, file := range manifest.Files {
		if file.Kind == ExportChapter {
			for book, folder := range folders {
				if strings.HasPrefix(file.Path, folder+"/") {
					used[book] = true
				}
			}
		}
	}
	var result []ManifestBook
	for _, book := range books {
		if !used[book.ID] {
			continue
		}
		if _, ok := folders[book.ID]; !ok {
			name := FreeName(wordChapters+"/"+ExportName(book.Title), func(candidate string) bool { return taken[FoldKey(candidate)] })
			folders[book.ID] = name
			taken[FoldKey(name)] = true
		}
		result = append(result, ManifestBook{Book: book.ID, Folder: folders[book.ID]})
	}
	for _, book := range manifest.Books {
		if used[book.Book] && !slices.ContainsFunc(result, func(known ManifestBook) bool { return known.Book == book.Book }) {
			result = append(result, book)
		}
	}
	paths := map[FileID]string{}
	fresh := map[string]bool{}
	for _, file := range files {
		if path, ok := kept[file.ID]; ok {
			paths[file.ID] = path
			continue
		}
		folder := file.Folder
		if file.ID.Kind == ExportChapter {
			folder = folders[file.Book]
		}
		stem := FreeName(Join(folderOrRoot(folder), file.Name), func(candidate string) bool {
			path := candidate + ".md"
			return fresh[FoldKey(path)] || occupied(path)
		})
		if stem == "" {
			continue
		}
		paths[file.ID] = stem + ".md"
		fresh[FoldKey(stem+".md")] = true
	}
	return paths, result
}

// folderOrRoot is a folder of the export for Join: "" is its root.
func folderOrRoot(folder string) string {
	if folder == "" {
		return Root
	}
	return folder
}

// PlanExport is the plan of a run (OE1.4, OE2.5, OE2.7): for each reference of the manifest and each new one, what the run
// does, by the fresh texts, what the disk holds at the paths it read and the word of the author to replace what he changed.
// The references that went leave first, so a name they free goes to a new reference without « (2)» (OE1.4).
func PlanExport(manifest Manifest, wanted []Wanted, stay []FileID, disk map[string]DiskFile, replaceChanged bool) []ExportAction {
	fresh := map[FileID]Wanted{}
	for _, file := range wanted {
		fresh[file.ID] = file
	}
	var moves, writes []ExportAction
	known := map[FileID]bool{}
	for _, file := range manifest.Files {
		known[file.ID()] = true
		seen := disk[file.Path]
		wanted, ok := fresh[file.ID()]
		switch {
		case slices.Contains(stay, file.ID()):
			writes = append(writes, ExportAction{Step: StepStay, File: file})
		case !ok && seen.Missing:
			moves = append(moves, ExportAction{Step: StepForget, File: file})
		case !ok && seen.Skip != "":
			moves = append(moves, ExportAction{Step: StepSkip, File: file, Skip: seen.Skip})
		case !ok && Moves(file, seen, replaceChanged):
			moves = append(moves, ExportAction{Step: StepMove, File: file, Expect: seen.Print})
		case !ok:
			moves = append(moves, ExportAction{Step: StepKeep, File: file})
		case seen.Missing:
			writes = append(writes, ExportAction{Step: StepCreate, File: written(file, wanted), Text: wanted.Text})
		case ExportSum(wanted.Text) == file.Sum:
			writes = append(writes, ExportAction{Step: StepUnchanged, File: file})
		case seen.Skip != "":
			writes = append(writes, ExportAction{Step: StepSkip, File: file, Skip: seen.Skip})
		case seen.Print == file.Print:
			writes = append(writes, ExportAction{Step: StepUpdate, File: written(file, wanted), Text: wanted.Text, Expect: seen.Print})
		case replaceChanged:
			writes = append(writes, ExportAction{Step: StepReplace, File: written(file, wanted), Text: wanted.Text, Expect: seen.Print})
		default:
			writes = append(writes, ExportAction{Step: StepKeep, File: file})
		}
	}
	for _, file := range wanted {
		if known[file.ID] {
			continue
		}
		entry := ManifestFile{Kind: file.ID.Kind, Key: file.ID.Key, Path: file.Path}
		writes = append(writes, ExportAction{Step: StepCreate, File: written(entry, file), Text: file.Text})
	}
	return append(moves, writes...)
}

// written is the entry of the manifest for a reference once its fresh text is written.
func written(file ManifestFile, wanted Wanted) ManifestFile {
	file.Print, file.Sum = ExportPrint(wanted.Text), ExportSum(wanted.Text)
	if file.Path == "" {
		file.Path = wanted.Path
	}
	return file
}

// ExportHeader is what the header of a note of the folder of an export the manifest does not name says of it: whose saga, which
// reference.
type ExportHeader struct {
	Path string
	Saga string
	ID   FileID
}

// HeaderOf reads the keys of the app in the header of a note, false for a note without them.
func HeaderOf(notePath string, data []byte) (ExportHeader, bool) {
	note, skip := ReadNote(notePath, data)
	if skip != "" {
		return ExportHeader{}, false
	}
	saga, _ := note.Front.Get("canonkeeper-saga")
	key, _ := note.Front.Get("canonkeeper-key")
	kind, _ := note.Front.Get("canonkeeper-kind")
	if saga.Text == "" || key.Text == "" || kind.Text == "" {
		return ExportHeader{}, false
	}
	return ExportHeader{Path: notePath, Saga: saga.Text, ID: FileID{Kind: ExportKind(kind.Text), Key: key.Text}}, true
}

// Relocated is the path a reference whose file went from the path of the manifest lies at now (OE2.6): the one note of the
// folder of the export the manifest does not name whose header names this saga and this reference — the author renamed or
// moved it in the folder. None, or more than one, and the reference is written again at its path.
func Relocated(saga string, id FileID, headers []ExportHeader) (string, bool) {
	found := ""
	for _, header := range headers {
		if header.Saga != saga || header.ID != id {
			continue
		}
		if found != "" {
			return "", false
		}
		found = header.Path
	}
	return found, found != ""
}

// Intend is the manifest a run keeps before its first write (OE2.8): each reference the plan writes named at its path with
// the text it writes — a reference it replaces keeps what was written before, a new one has nothing written yet —, so a run
// cut between a rename and the record of it leaves the next run a file it knows as its own (Settle), never a file it takes
// for another's and gives « (2)». A new reference whose path another reference of the manifest still holds — one that went
// and frees it in this run (OE1.4) — is not named yet: the reference that went keeps its path until its move is said, so a
// cut before it leaves no file «чужим», and the run names the new one once the path is free (Free), right before its
// rename. The manifest never names a path twice.
func Intend(manifest Manifest, plan []ExportAction) Manifest {
	files := slices.Clone(manifest.Files)
	for _, action := range plan {
		switch action.Step {
		case StepCreate, StepUpdate, StepReplace:
		default:
			continue
		}
		pending := Pending(action)
		if !Free(Manifest{Files: files}, pending.ID(), pending.Path) {
			continue
		}
		if at := slices.IndexFunc(files, func(file ManifestFile) bool { return file.ID() == pending.ID() }); at >= 0 {
			if action.Step != StepCreate {
				pending.Print, pending.Sum = files[at].Print, files[at].Sum
			}
			files[at] = pending
			continue
		}
		files = append(files, pending)
	}
	manifest.Files = files
	return manifest
}

// Free says no reference of the manifest but id holds a path, the case ignored.
func Free(manifest Manifest, id FileID, path string) bool {
	return !slices.ContainsFunc(manifest.Files, func(file ManifestFile) bool { return file.ID() != id && FoldKey(file.Path) == FoldKey(path) })
}

// Pending is the entry of the manifest for a reference a run is about to write at the path of its action: the text named as
// the one being written, nothing yet as written.
func Pending(action ExportAction) ManifestFile {
	file := action.File
	file.Writing = &ManifestWrite{Print: file.Print, Sum: file.Sum}
	file.Print, file.Sum = "", ""
	return file
}

// Settle is a reference whose write a run named and did not say done (OE2.8), once the next run looked at its path: the
// bytes it was writing lie there — written. A file that reads as that text but is not its bytes — the write changed only the
// target of a link and its rename did not happen, or a synchronizer rewrote the ends of the lines — is that text with the
// bytes of the disk: the plan, which tells «what was written» by the SHA-256, writes the fresh text again, and no link stays
// pointing where the reference left. A reference it was replacing whose file is neither keeps what was written before — not
// replaced, or changed since by the author, which the plan tells by the fingerprint; a new reference whose file says it is
// this reference of this saga (ours) is the run's, changed since by the author. A new reference with no file there, or a file
// of someone else, is no reference of the manifest: false. A file that does not read keeps the entry as it is.
func Settle(file ManifestFile, disk DiskFile, ours bool) (ManifestFile, bool) {
	if file.Writing == nil || disk.Skip != "" {
		return file, true
	}
	writing := *file.Writing
	file.Writing = nil
	switch {
	case !disk.Missing && disk.Sum == writing.Sum, file.Print == "" && !disk.Missing && ours && disk.Print != writing.Print:
		file.Print, file.Sum = writing.Print, writing.Sum
		return file, true
	case !disk.Missing && disk.Print == writing.Print:
		file.Print, file.Sum = writing.Print, disk.Sum
		return file, true
	case file.Print != "":
		return file, true
	}
	return ManifestFile{}, false
}
