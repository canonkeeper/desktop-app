// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Root is the path of the folder of the vault itself.
const Root = "."

// NFC is a name as the bridge keeps it (OB0.2): a vault moved from macOS names its files in NFD, and the same name must
// give the same path, the same order and the same fingerprint on every computer.
func NFC(name string) string {
	return norm.NFC.String(name)
}

// IsNoteName says a file is a note: its name ends with .md in any case and has something before it.
func IsNoteName(name string) bool {
	return len(name) > len(".md") && strings.EqualFold(name[len(name)-len(".md"):], ".md")
}

// isServiceName says a note is a drawing of Excalidraw by its name.
func isServiceName(name string) bool {
	const drawing = ".excalidraw.md"
	return len(name) >= len(drawing) && strings.EqualFold(name[len(name)-len(drawing):], drawing)
}

// isHiddenName says a name starts with a dot: Obsidian reads no such folder or file (.obsidian, .trash).
func isHiddenName(name string) bool {
	return strings.HasPrefix(name, ".")
}

// NoteStem is the name of a note without .md.
func NoteStem(name string) string {
	if IsNoteName(name) {
		return name[:len(name)-len(".md")]
	}
	return name
}

// Relative is a path of a folder or of a note a request names, as the bridge reads it (OB0.3): relative to the root of
// the vault, with "/" between its names, in NFC and otherwise as it is — a space at the edge of a name is a part of the
// name; no name of it is empty, ".", ".." or on a dot, and it holds no backslash, colon or control sign — a path no
// system reads as another. The root of the vault is ".".
func Relative(value string) (string, error) {
	value = NFC(value)
	if value == "" || value == Root {
		return Root, nil
	}
	if !utf8.ValidString(value) || !fs.ValidPath(value) || strings.ContainsAny(value, `\:`) ||
		strings.ContainsFunc(value, unicode.IsControl) {
		return "", ErrPath
	}
	for name := range strings.SplitSeq(value, "/") {
		if isHiddenName(name) {
			return "", ErrPath
		}
	}
	return value, nil
}

// Within says child is folder itself or lies in it.
func Within(folder, child string) bool {
	return folder == Root || child == folder || strings.HasPrefix(child, folder+"/")
}

// Depth is how deep a folder lies: the root 0, its folders 1.
func Depth(folder string) int {
	if folder == Root {
		return 0
	}
	return strings.Count(folder, "/") + 1
}

// Join is the path of a name in a folder.
func Join(folder, name string) string {
	if folder == Root {
		return name
	}
	return folder + "/" + name
}

// Base is the last name of a path.
func Base(value string) string {
	return path.Base(value)
}

// NaturalLess orders paths as a person reads them (OB3.3): name after name, a run of digits by its number — «Глава 2»
// before «Глава 10» — the letters without regard to their case, then by the case, then by the bytes.
func NaturalLess(left, right string) bool {
	leftNames, rightNames := strings.Split(left, "/"), strings.Split(right, "/")
	for index := 0; index < len(leftNames) && index < len(rightNames); index++ {
		if order := naturalCompare(leftNames[index], rightNames[index]); order != 0 {
			return order < 0
		}
	}
	return len(leftNames) < len(rightNames)
}

func naturalCompare(left, right string) int {
	if order := naturalChunks(strings.ToLower(left), strings.ToLower(right)); order != 0 {
		return order
	}
	if order := naturalChunks(left, right); order != 0 {
		return order
	}
	return strings.Compare(left, right)
}

func naturalChunks(left, right string) int {
	for left != "" && right != "" {
		leftChunk, leftDigits := nextChunk(left)
		rightChunk, rightDigits := nextChunk(right)
		left, right = left[len(leftChunk):], right[len(rightChunk):]
		if leftDigits && rightDigits {
			a, b := strings.TrimLeft(leftChunk, "0"), strings.TrimLeft(rightChunk, "0")
			if len(a) != len(b) {
				return compareInts(len(a), len(b))
			}
			if order := strings.Compare(a, b); order != 0 {
				return order
			}
			continue
		}
		if order := strings.Compare(leftChunk, rightChunk); order != 0 {
			return order
		}
	}
	return compareInts(len(left), len(right))
}

func nextChunk(text string) (string, bool) {
	digits := text[0] >= '0' && text[0] <= '9'
	end := 1
	for end < len(text) && (text[end] >= '0' && text[end] <= '9') == digits {
		end++
	}
	return text[:end], digits
}

func compareInts(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}

// Found is an entry of a directory of a vault as the file system names it, before anything of it is read.
type Found struct {
	Name string
	// Dir and Regular are the type of the entry itself, its links not followed; Link is a symbolic link, a junction or
	// another point of a reparse.
	Dir, Regular, Link bool
	// Hidden is the attribute hidden or system of Windows; CloudOnly a file whose data are not on the disk.
	Hidden, CloudOnly bool
	Size              int64
}

// Kind is what an entry is to the bridge.
type Kind uint8

const (
	KindSkipped Kind = iota
	KindFolder
	KindNote
)

// readableName says a name of an entry is one a request can name (Relative): valid UTF-8, without a control sign, a
// backslash or a colon — a name Obsidian itself does not give.
func readableName(name string) bool {
	return utf8.ValidString(name) && !strings.ContainsAny(name, `\:`) && !strings.ContainsFunc(name, unicode.IsControl)
}

// Classify says what an entry is (OB0.2, OB2.1): a link and anything that is neither a file nor a directory is skipped as
// a link, a name on a dot and a hidden or system entry as hidden, a name no request can name as unreadable; a file only
// in the cloud, a file that is not a note, a drawing of Excalidraw and a note over the size of a note are skipped by
// their reasons; a directory is entered.
func Classify(found Found) (Kind, Skip) {
	switch {
	case found.Link || !found.Dir && !found.Regular:
		return KindSkipped, SkipLink
	case isHiddenName(found.Name) || found.Hidden:
		return KindSkipped, SkipHidden
	case !readableName(found.Name):
		return KindSkipped, SkipUnreadable
	case found.Dir:
		return KindFolder, ""
	case found.CloudOnly:
		return KindSkipped, SkipCloudOnly
	case !IsNoteName(found.Name):
		return KindSkipped, SkipNotNote
	case isServiceName(found.Name):
		return KindSkipped, SkipService
	case found.Size > MaxNoteBytes:
		return KindSkipped, SkipTooBig
	}
	return KindNote, ""
}
