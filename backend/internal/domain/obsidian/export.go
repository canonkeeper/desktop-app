// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The export of the map of a saga and of the notes of its chapters into a folder of its vault
// (specs/2026-11-15-desk-obsidian-export.md): the names of its files, the fingerprint that says whether the author changed
// one, its manifest, the references themselves and the plan of a run. Nothing here touches a file.

// The codes of the refusals of an export (Data).
const (
	CodeMapNotReady              = "obsidian.map_not_ready"
	CodeExportDirUnsafe          = "obsidian.export_dir_unsafe"
	CodeExportManifestUnreadable = "obsidian.export_manifest_unreadable"
)

// The folders and the files of an export (OE1.3, OE1.5, OE2.1, OE2.8).
const (
	// ExportRoot is the folder of the vault the first export of a saga takes its folder in.
	ExportRoot = "CanonKeeper"
	// ExportMark is the folder of an export the app keeps for itself: hidden from Obsidian and from the walk of the bridge.
	ExportMark = ".canonkeeper"
	// ExportManifest is the manifest of an export, from its folder: a folder that holds it is a folder of an export (OE1.9).
	ExportManifest = ExportMark + "/manifest.json"
	// ExportTmp is the folder of the temporary files of an export, on the same volume as its references.
	ExportTmp = ExportMark + "/tmp"
	// ExportDeleted is the folder of the export a reference the run takes away goes to (OE2.5, OE2.7).
	ExportDeleted = wordDeleted
	// ExportIndexKey is the key of the index of a saga, the file Сага.md.
	ExportIndexKey = "saga"
	// ExportFormat is the version of the format of the manifest.
	ExportFormat = 1
	// MaxExportNameRunes is the longest name of a file or of a folder of an export, without .md (OE2.6).
	MaxExportNameRunes = 120
	// MaxExportNameBytes bounds the same name in bytes of UTF-8: a file system keeps 255 bytes of a name, and a name in
	// _Удалено/ takes « 2026-11-15 (999).md» after it.
	MaxExportNameBytes = 200
	// maxNumbered bounds « (N)» a name takes before a run gives up the name.
	maxNumbered = 999
)

// ExportKind is what a reference is: the index of the saga, a node of the map or a chapter.
type ExportKind string

const (
	ExportIndex   ExportKind = "index"
	ExportNode    ExportKind = "node"
	ExportChapter ExportKind = "chapter"
)

// ExportSkip is why a reference that lies on the disk is not read, replaced or moved (OE1.3, OE1.7, OE1.8): a closed list.
type ExportSkip string

const (
	ExportCloudOnly  ExportSkip = "cloud_only"
	ExportBusy       ExportSkip = "busy"
	ExportUnreadable ExportSkip = "unreadable"
	ExportLink       ExportSkip = "link"
)

// MapNotReady is an export the map of the saga does not let through now (obsidian.map_not_ready): it is being updated —
// the Job of the cards is queued —, or the helper has read no book yet.
type MapNotReady struct{ Reason MapReason }

func (MapNotReady) Error() string { return "obsidian: the map of the saga is not ready" }

// MapReason is why the map is not ready.
type MapReason string

const (
	MapUpdating MapReason = "updating"
	MapNoCards  MapReason = "no_cards"
)

var (
	// ErrExportDirUnsafe is a path to the folder of an export with a link or a file on it (obsidian.export_dir_unsafe).
	ErrExportDirUnsafe = errors.New("obsidian: the path to the folder of the export is not a folder")
	// ErrExportManifestUnreadable is the manifest of the folder of the saga that does not read
	// (obsidian.export_manifest_unreadable).
	ErrExportManifestUnreadable = errors.New("obsidian: the manifest of the export does not read")
)

// forbiddenNameSigns are the signs no name of a file of an export keeps (OE2.6): those of Windows and those of the links of
// Obsidian.
const forbiddenNameSigns = `\/:*?"<>|#^[]`

// ExportName is a name of a file or of a folder of an export, without .md (OE2.6): the signs of Windows and of the links of
// Obsidian and the control signs are spaces, the spaces one, NFC, no dot and no space at the edges, not longer than
// MaxExportNameRunes nor MaxExportNameBytes; a name whose part before the first dot is a name of a device of Windows starts with «_»; a name that
// leaves nothing is «Без имени».
func ExportName(name string) string {
	name = strings.Map(func(sign rune) rune {
		if unicode.IsControl(sign) || sign == '\uFEFF' || strings.ContainsRune(forbiddenNameSigns, sign) {
			return ' '
		}
		return sign
	}, NFC(name))
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, " ")
	}
	name = trimEdges(strings.Join(strings.Fields(name), " "))
	if runes := []rune(name); len(runes) > MaxExportNameRunes || len(name) > MaxExportNameBytes {
		runes = runes[:min(len(runes), MaxExportNameRunes)]
		for len(string(runes)) > MaxExportNameBytes {
			runes = runes[:len(runes)-1]
		}
		name = trimEdges(string(runes))
	}
	if name == "" {
		name = wordNoName
	}
	if deviceName(name) {
		name = "_" + name
	}
	return name
}

// trimEdges takes the dots and the spaces off the edges of a name: Windows drops them at the end, a dot at the start hides it.
func trimEdges(name string) string {
	return strings.Trim(name, " .")
}

// deviceName says a name is one Windows keeps for a device, whatever follows its first dot.
func deviceName(name string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	stem = strings.TrimRight(stem, " ")
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9'
}

// Numbered is a name with « (n)» after it; the first is the name itself.
func Numbered(name string, n int) string {
	if n <= 1 {
		return name
	}
	return fmt.Sprintf("%s (%d)", name, n)
}

// FreeName is the first of name, «name (2)», «name (3)»… that taken does not hold; "" when none up to maxNumbered is free.
func FreeName(name string, taken func(string) bool) string {
	for n := 1; n <= maxNumbered; n++ {
		if candidate := Numbered(name, n); !taken(candidate) {
			return candidate
		}
	}
	return ""
}

// DeletedNames are the names a file moved into _Удалено/ tries in their order (OE2.5, OE2.7): its own, then with the day,
// then with the day and « (2)», « (3)»…; stem is its name without .md.
func DeletedNames(stem string, day time.Time) []string {
	names := []string{stem + ".md"}
	dated := stem + " " + day.Format(time.DateOnly)
	for n := 1; n <= maxNumbered; n++ {
		names = append(names, Numbered(dated, n)+".md")
	}
	return names
}

// FoldKey is a path as a file system that ignores the case compares it: two names of one folder that differ only in their
// case are one file on Windows (OE2.6).
func FoldKey(path string) string {
	return strings.ToLower(NFC(path))
}

// wikiTarget is a link of the wiki with its target and its caption, an embed too.
var wikiTarget = regexp.MustCompile(`\[\[[^\[\]|\n]*\|([^\[\]\n]*)\]\]`)

// ExportPrint is the fingerprint of the text of a reference (OE2.5): SHA-256 of the text without the mark of the byte
// order, its lines ended by "\n", and from each link of the wiki with a caption only its caption kept as a link —
// «[[цель|подпись]]» counts as «[[подпись]]». A synchronizer that changes the ends of the lines and Obsidian that rewrites
// the targets of the links when a note beside is renamed change no fingerprint; a change of a word, of a caption or a link
// that became text does.
func ExportPrint(text []byte) string {
	value := strings.TrimPrefix(string(text), "\uFEFF")
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	value = wikiTarget.ReplaceAllString(value, "[[$1]]")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// ExportSum is the SHA-256 of the bytes of a text as they are: the manifest keeps it for what a run wrote, so a fresh text
// equal to it is neither read nor written again.
func ExportSum(text []byte) string {
	sum := sha256.Sum256(text)
	return hex.EncodeToString(sum[:])
}

// FileID names a reference: its kind and its key — the key of a node, the id of a chapter, ExportIndexKey.
type FileID struct {
	Kind ExportKind
	Key  string
}

// Manifest is the manifest of the folder of an export (OE2.8): whose folder it is, when its last run went, whether that run
// ended, the folder of each book and, by file, its key, its kind, its path from the folder of the export, the fingerprint
// of what was written and the SHA-256 of its bytes.
type Manifest struct {
	Format   int            `json:"format"`
	Saga     string         `json:"saga"`
	At       time.Time      `json:"at"`
	Complete bool           `json:"complete"`
	Books    []ManifestBook `json:"books"`
	Files    []ManifestFile `json:"files"`
}

// ManifestBook is the folder of the chapters of a book, from the folder of the export (OE2.1).
type ManifestBook struct {
	Book   string `json:"book"`
	Folder string `json:"folder"`
}

// ManifestFile is a reference the manifest names: what was written at its path, and the text a run is renaming into it.
type ManifestFile struct {
	Kind  ExportKind `json:"kind"`
	Key   string     `json:"key"`
	Path  string     `json:"path"`
	Print string     `json:"print,omitempty"`
	Sum   string     `json:"sha256,omitempty"`
	// Writing is the text a run renames into the path, named before the rename (OE2.8); nil once the rename is said.
	Writing *ManifestWrite `json:"writing,omitempty"`
}

// ManifestWrite is a text by its fingerprint and the SHA-256 of its bytes.
type ManifestWrite struct {
	Print string `json:"print"`
	Sum   string `json:"sha256"`
}

// ID is the name of the reference.
func (file ManifestFile) ID() FileID { return FileID{Kind: file.Kind, Key: file.Key} }

// ReadManifest reads the bytes of a manifest: a manifest of another format, without a saga, with a path that is not a
// path of a file of the folder of an export, or with a reference or a path named twice does not read.
func ReadManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&manifest); err != nil || decoder.More() {
		return Manifest{}, ErrExportManifestUnreadable
	}
	if manifest.Format != ExportFormat || strings.TrimSpace(manifest.Saga) == "" {
		return Manifest{}, ErrExportManifestUnreadable
	}
	ids, paths := map[FileID]bool{}, map[string]bool{}
	for _, file := range manifest.Files {
		switch file.Kind {
		case ExportIndex, ExportNode, ExportChapter:
		default:
			return Manifest{}, ErrExportManifestUnreadable
		}
		if file.Key == "" || ids[file.ID()] || !ExportPath(file.Path) || paths[FoldKey(file.Path)] || !IsNoteName(file.Path) {
			return Manifest{}, ErrExportManifestUnreadable
		}
		// A reference is what was written, what a run is writing, or both — a print without its sum is neither.
		written, writing := file.Print != "" && file.Sum != "", file.Writing != nil && file.Writing.Print != "" && file.Writing.Sum != ""
		if !written && !writing || (file.Print == "") != (file.Sum == "") || file.Writing != nil && !writing {
			return Manifest{}, ErrExportManifestUnreadable
		}
		ids[file.ID()], paths[FoldKey(file.Path)] = true, true
	}
	books := map[string]bool{}
	for _, book := range manifest.Books {
		if book.Book == "" || books[book.Book] || !ExportPath(book.Folder) {
			return Manifest{}, ErrExportManifestUnreadable
		}
		books[book.Book] = true
	}
	return manifest, nil
}

// Encode is the bytes of the manifest.
func (manifest Manifest) Encode() []byte {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

// ExportPath says a path is one a file of the folder of an export may have, from that folder (OE1.3): names joined by "/",
// valid UTF-8 without a backslash, a colon or a control sign, none empty, ".", ".." or hidden — never the folder of the app
// or a path out of the folder.
func ExportPath(value string) bool {
	if value == "" || !utf8.ValidString(value) || !fs.ValidPath(value) || value == "." || strings.ContainsAny(value, `\:`) ||
		strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	for name := range strings.SplitSeq(value, "/") {
		if isHiddenName(name) {
			return false
		}
	}
	return true
}
