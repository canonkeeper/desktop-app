// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"slices"
	"strings"

	"canonkeeper/backend/internal/domain/authormaterials"
)

// BookNote is the note that makes its folder a book and names it (OB2.2, OB3.3).
const BookNote = "_book.md"

// HintKind is what a folder looks like to the look over a vault (OB2.2): only a hint, the author chooses.
type HintKind string

const (
	HintBook      HintKind = "book"
	HintMaterials HintKind = "materials"
	HintSkip      HintKind = "skip"
)

// Hint is the hint of a folder of the first two levels; Material is the kind of the records of a folder of materials.
type Hint struct {
	Kind     HintKind
	Material authormaterials.Kind
}

// materialFolders are the names of the folders of materials and the kind of their records, in Russian and in English.
var materialFolders = map[string]authormaterials.Kind{
	"персонажи": authormaterials.KindCharacter, "герои": authormaterials.KindCharacter, "characters": authormaterials.KindCharacter,
	"места": authormaterials.KindPlace, "locations": authormaterials.KindPlace,
	"навыки": authormaterials.KindSkill, "skills": authormaterials.KindSkill,
	"предметы": authormaterials.KindItem, "items": authormaterials.KindItem,
	"фракции": authormaterials.KindFaction, "factions": authormaterials.KindFaction,
}

// HintOf is the hint of a folder by its name and its depth: a folder with the note _book.md or linked to a book of the
// saga is a book; the folders of characters, places, skills, items and factions are materials of their kind; the
// rest — templates, a diary, attachments and every other folder — is not taken. Below the second level there is no hint.
func HintOf(name string, depth int, bookNote, linked bool) *Hint {
	if depth < 1 || depth > 2 {
		return nil
	}
	if bookNote || linked {
		return &Hint{Kind: HintBook}
	}
	if kind, ok := materialFolders[strings.ToLower(NFC(strings.TrimSpace(name)))]; ok {
		return &Hint{Kind: HintMaterials, Material: kind}
	}
	return &Hint{Kind: HintSkip}
}

// Folder is a folder of the tree of a vault.
type Folder struct {
	Path string
	Name string
	// Notes are the notes in the folder itself, Below those of its folders.
	Notes, Below int
	// Skipped are its own entries that are not read, by their reasons.
	Skipped Skipped
	// BookNote says the folder holds _book.md.
	BookNote bool
}

// Tree is the look over a vault as its entries come (OB2.1): the folders and their notes, counted without a note read,
// within MaxEntries entries and MaxDepth levels.
type Tree struct {
	folders map[string]*Folder
	entries int
	// Truncated says the look stopped at a limit: the tree is what it saw.
	Truncated bool
	Skipped   Skipped
}

// NewTree begins the tree of a vault whose root folder is named rootName.
func NewTree(rootName string) *Tree {
	return &Tree{
		folders: map[string]*Folder{Root: {Path: Root, Name: NFC(rootName), Skipped: Skipped{}}},
		Skipped: Skipped{},
	}
}

// Entries is how many entries the look counted.
func (tree *Tree) Entries() int { return tree.entries }

// Add counts an entry of the folder dir and says whether the look goes into it — a folder within the depth — and whether
// it stops: the limit of entries is reached.
func (tree *Tree) Add(dir string, found Found) (enter, stop bool) {
	if tree.entries >= MaxEntries {
		tree.Truncated = true
		return false, true
	}
	tree.entries++
	parent := tree.folders[dir]
	if parent == nil {
		return false, false
	}
	found.Name = NFC(found.Name)
	kind, skip := Classify(found)
	switch kind {
	case KindSkipped:
		parent.Skipped[skip]++
		tree.Skipped[skip]++
		return false, false
	case KindNote:
		parent.Notes++
		if strings.EqualFold(found.Name, BookNote) {
			parent.BookNote = true
		}
		return false, false
	}
	folder := Join(dir, found.Name)
	if Depth(folder) > MaxDepth {
		tree.Truncated = true
		return false, false
	}
	tree.folders[folder] = &Folder{Path: folder, Name: found.Name, Skipped: Skipped{}}
	return true, false
}

// Folders are the folders that hold notes, themselves or below, in the natural order of their paths, the root first.
func (tree *Tree) Folders() []Folder {
	for folderPath, folder := range tree.folders {
		if folder.Notes == 0 || folderPath == Root {
			continue
		}
		for parent := parentOf(folderPath); ; parent = parentOf(parent) {
			if above := tree.folders[parent]; above != nil {
				above.Below += folder.Notes
			}
			if parent == Root {
				break
			}
		}
	}
	var folders []Folder
	for _, folder := range tree.folders {
		if folder.Notes+folder.Below > 0 {
			folders = append(folders, *folder)
		}
	}
	slices.SortFunc(folders, func(left, right Folder) int {
		switch {
		case left.Path == right.Path:
			return 0
		case left.Path == Root:
			return -1
		case right.Path == Root:
			return 1
		case NaturalLess(left.Path, right.Path):
			return -1
		}
		return 1
	})
	return folders
}

// parentOf is the folder a path lies in.
func parentOf(value string) string {
	index := strings.LastIndexByte(value, '/')
	if index < 0 {
		return Root
	}
	return value[:index]
}
