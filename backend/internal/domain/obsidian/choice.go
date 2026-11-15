// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"slices"

	"canonkeeper/backend/internal/domain/authormaterials"
)

// MaterialFolder is a folder the author takes as materials, with the kind of its records.
type MaterialFolder struct {
	Path string
	Kind authormaterials.Kind
}

// Choice is what the author takes of a vault for a preview (OB4.1): folders as books, folders as materials, and folders
// and notes left out.
type Choice struct {
	Books     []string
	Materials []MaterialFolder
	Exclude   []string
}

// Check reads the paths of a choice by the rule of a request (OB0.3) and checks it before anything is read (OB4.1): no
// book holds another book or a folder linked to a book of the saga, and none of them holds a book. A folder named twice
// is named once.
func (choice Choice) Check(linked []string) (Choice, error) {
	var checked Choice
	seen := map[string]bool{}
	for _, value := range choice.Books {
		folder, err := Relative(value)
		if err != nil {
			return Choice{}, err
		}
		if !seen[folder] {
			seen[folder] = true
			checked.Books = append(checked.Books, folder)
		}
	}
	for _, material := range choice.Materials {
		folder, err := Relative(material.Path)
		if err != nil {
			return Choice{}, err
		}
		kind, ok := authormaterials.ParseKind(string(material.Kind))
		if !ok && material.Kind != "" {
			return Choice{}, ErrPath
		}
		if seen[folder] {
			return Choice{}, Overlap{Outer: folder, Inner: folder}
		}
		seen[folder] = true
		checked.Materials = append(checked.Materials, MaterialFolder{Path: folder, Kind: kind})
	}
	for _, value := range choice.Exclude {
		excluded, err := Relative(value)
		if err != nil {
			return Choice{}, err
		}
		if !slices.Contains(checked.Exclude, excluded) {
			checked.Exclude = append(checked.Exclude, excluded)
		}
	}
	for _, book := range checked.Books {
		for _, other := range checked.Books {
			if other != book && Within(book, other) {
				return Choice{}, Overlap{Outer: book, Inner: other}
			}
		}
		for _, folder := range linked {
			switch {
			case folder == book:
			case Within(book, folder):
				return Choice{}, Overlap{Outer: book, Inner: folder}
			case Within(folder, book):
				return Choice{}, Overlap{Outer: folder, Inner: book}
			}
		}
	}
	return checked, nil
}

// Owner is the chosen folder a note goes to (OB3.3): the deepest chosen folder that holds it — a folder of materials or
// another book inside a book takes its notes out of the book — or none when a folder or the note itself is left out
// deeper than it.
func (choice Choice) Owner(notePath string) (folder string, book bool, ok bool) {
	depth := -1
	consider := func(candidate string, isBook bool) {
		if Within(candidate, notePath) && candidate != notePath && Depth(candidate) > depth {
			folder, book, ok, depth = candidate, isBook, true, Depth(candidate)
		}
	}
	for _, candidate := range choice.Books {
		consider(candidate, true)
	}
	for _, material := range choice.Materials {
		consider(material.Path, false)
	}
	for _, excluded := range choice.Exclude {
		if excluded == notePath || Within(excluded, notePath) && Depth(excluded) >= depth {
			return "", false, false
		}
	}
	return folder, book, ok
}

// Folders are every folder of the choice, the books first.
func (choice Choice) Folders() []string {
	folders := slices.Clone(choice.Books)
	for _, material := range choice.Materials {
		folders = append(folders, material.Path)
	}
	return folders
}
