// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
)

// MaterialsInput is importObsidianMaterials (specs/2026-11-15-desk-obsidian-notes.md ON1): the choice the author saw in the
// preview, the fingerprint of the records of each folder of materials by its folder as the request names it, what he saw of
// the record of each note by its path, his decisions, and the request with its key.
type MaterialsInput struct {
	ActorUserID  string
	ProjectID    string
	Choice       obsidian.Choice
	Fingerprints map[string]string
	Seen         map[string]string
	Decisions    obsidian.Decisions
	Key          string
	Proof        auth.SessionWriteProof
}

// MaxNames bounds the names a group of the answer of a load of records names (ON1.8).
const MaxNames = 50

// NoteGroup is a group of the records of a load: how many, and the names of the first MaxNames.
type NoteGroup struct {
	Count int      `json:"count"`
	Names []string `json:"names"`
}

func (group *NoteGroup) add(name string) {
	group.Count++
	if len(group.Names) < MaxNames {
		group.Names = append(group.Names, name)
	}
}

// KeptGroups are the records a load left as they were (ON1.8): by a name another record keeps or another note gets, by
// an edit in the app, by a change since the preview.
type KeptGroups struct {
	Taken   NoteGroup `json:"taken"`
	Edited  NoteGroup `json:"edited"`
	Changed NoteGroup `json:"changed"`
}

// MaterialsLoaded is the answer of a load of the records of notes (ON1.8), the same for a replay of its key.
type MaterialsLoaded struct {
	Created   NoteGroup        `json:"created"`
	Updated   NoteGroup        `json:"updated"`
	Replaced  NoteGroup        `json:"replaced"`
	Unchanged NoteGroup        `json:"unchanged"`
	Kept      KeptGroups       `json:"kept"`
	Skipped   obsidian.Skipped `json:"skipped"`
}

// Add counts the record of a note in the group of its outcome.
func (loaded *MaterialsLoaded) Add(outcome obsidian.NoteOutcome, name string) {
	group := map[obsidian.NoteOutcome]*NoteGroup{
		obsidian.OutcomeCreated: &loaded.Created, obsidian.OutcomeUpdated: &loaded.Updated, obsidian.OutcomeReplaced: &loaded.Replaced,
		obsidian.OutcomeUnchanged: &loaded.Unchanged, obsidian.OutcomeKeptTaken: &loaded.Kept.Taken,
		obsidian.OutcomeKeptEdited: &loaded.Kept.Edited, obsidian.OutcomeKeptChanged: &loaded.Kept.Changed,
	}[outcome]
	group.add(name)
}

// NotesWrite is the one transaction of a load of the records of notes (ON1.5): the author and the proof of his session, the
// key of the request, the records of the notes, the names the book has by their keys, what the author saw and decided, and
// what of the notes gave no record.
type NotesWrite struct {
	ActorUserID string
	ProjectID   string
	Proof       auth.SessionWriteProof
	Key         ReplayKey
	Records     []obsidian.Record
	Book        map[string]string
	Seen        map[string]string
	Decisions   obsidian.Decisions
	Skipped     obsidian.Skipped
}

// Notes keep the records of the notes of a saga (ON1.5, ON1.8): the answers of the loads by their keys, and the one
// transaction of a load — the states again by the exact keys of the names with the rows locked, the records by the owner of
// the records, the links of the notes and the answer of the key.
type Notes interface {
	// MaterialsReplay is the answer kept for the key: nil for none, ErrReplayConflict for the key of another request.
	MaterialsReplay(ctx context.Context, key ReplayKey) (*MaterialsLoaded, error)
	WriteNotes(ctx context.Context, write NotesWrite) (MaterialsLoaded, error)
}

// BookNames are the names the book of a saga has by their keys.
type BookNames interface {
	BookNames(ctx context.Context, projectID string, keys []string) (map[string]string, error)
}

// ErrMaterialsFingerprint is a folder of materials of the request without the fingerprint of its records, or a request
// without a folder of materials.
var ErrMaterialsFingerprint = errors.New("obsidian: a folder of materials has no fingerprint")

// ErrMaterialsSeen is a request without what the author saw of the record of a note of its folders (ON1.3): a load writes by
// what the author saw of every record, so a request that leaves one out is not one.
var ErrMaterialsSeen = errors.New("obsidian: a record of the load has no seen")

// ImportMaterials is importObsidianMaterials (ON1): the records of the notes of the folders of materials the author saw in
// the preview go into «Ваши материалы», without a model, a row of an import, a Job or an upload of the day. Within the place
// of the saga: the answer kept for the key, the choice checked as the preview checks it; within the time of the reading: the
// notes of the folders of materials read again by the code of the preview; then the fingerprints of the records of each
// folder compared with the request and the one transaction of the writes to its end.
type ImportMaterials struct {
	Vaults Vaults
	Files  Files
	Links  Links
	Notes  Notes
	Book   BookNames
	Plain  obsidian.Plain
	Gate   *Gate
	Limits Limits
}

func (load ImportMaterials) Execute(ctx context.Context, input MaterialsInput) (MaterialsLoaded, error) {
	linked, err := load.Vaults.Vault(ctx, input.ActorUserID, input.ProjectID, AccessWrite)
	if err != nil {
		return MaterialsLoaded{}, err
	}
	if linked.Path == "" {
		return MaterialsLoaded{}, ErrNotLinked
	}
	choice, fingerprints, seen, decisions, err := checkedMaterials(input)
	if err != nil {
		return MaterialsLoaded{}, err
	}
	operation, err := enter(ctx, load.Gate, input.ProjectID)
	if err != nil {
		return MaterialsLoaded{}, err
	}
	defer operation.end()
	key := ReplayKey{
		ActorUserID: input.ActorUserID, ProjectID: input.ProjectID, Key: input.Key,
		Request: materialsRequestHash(choice, fingerprints, seen, decisions),
	}
	// ON1.8: the answer of the key comes before the vault is read.
	if replayed, err := load.Notes.MaterialsReplay(ctx, key); err != nil || replayed != nil {
		if err != nil {
			return MaterialsLoaded{}, err
		}
		return *replayed, nil
	}
	books, err := load.Links.Books(ctx, input.ProjectID)
	if err != nil {
		return MaterialsLoaded{}, err
	}
	held := make([]string, 0, len(books))
	for _, book := range books {
		held = append(held, book.Folder)
	}
	if _, err := choice.Check(held); err != nil {
		return MaterialsLoaded{}, err
	}
	reader := PreviewImport{Files: load.Files, Limits: load.Limits}
	read, err := readIn(operation, load.Limits.Read, func(ctx context.Context) (gathered, error) {
		read, err := reader.read(ctx, linked.Path, choice, materialPaths(choice))
		return read, sincePreview(err)
	})
	if err != nil {
		return MaterialsLoaded{}, err
	}
	records, folders, skipped := recordsOf(choice, read, load.Plain)
	var changed []string
	for _, folder := range folderPrints(choice, records, folders) {
		if folder.Fingerprint != fingerprints[folder.Folder] {
			changed = append(changed, folder.Folder)
		}
	}
	if len(changed) > 0 {
		return MaterialsLoaded{}, FolderChanged{Folders: changed}
	}
	// ON1.3: what the author saw of every record of the folders the fingerprints vouch for, before the saga is read.
	for _, record := range records {
		if _, ok := seen[record.Path]; !ok {
			return MaterialsLoaded{}, ErrMaterialsSeen
		}
	}
	// OK0.1: the writes go to their end on their own context, which neither the time of the reading nor a caller who went
	// away ends.
	writes, cancel := operation.writes(load.Limits.Write)
	defer cancel()
	names, _, _ := namesOf(records)
	book, err := load.Book.BookNames(writes, input.ProjectID, names)
	if err != nil {
		return MaterialsLoaded{}, err
	}
	return load.Notes.WriteNotes(writes, NotesWrite{
		ActorUserID: input.ActorUserID, ProjectID: input.ProjectID, Proof: input.Proof, Key: key, Records: records, Book: book,
		Seen: seen, Decisions: decisions, Skipped: skipped,
	})
}

// checkedMaterials is the choice of the request by the rule of a request (OB0.3) with the fingerprint of the records of each
// folder of materials, what the author saw and what he decided by the paths of the notes — every path in NFC, as a preview
// names it.
func checkedMaterials(input MaterialsInput) (obsidian.Choice, map[string]string, map[string]string, obsidian.Decisions, error) {
	choice, err := input.Choice.Check(nil)
	if err != nil {
		return obsidian.Choice{}, nil, nil, obsidian.Decisions{}, err
	}
	if len(choice.Materials) == 0 {
		return obsidian.Choice{}, nil, nil, obsidian.Decisions{}, ErrMaterialsFingerprint
	}
	fingerprints, err := byNFC(input.Fingerprints)
	if err != nil {
		return obsidian.Choice{}, nil, nil, obsidian.Decisions{}, err
	}
	for _, folder := range choice.Materials {
		if fingerprints[folder.Path] == "" {
			return obsidian.Choice{}, nil, nil, obsidian.Decisions{}, ErrMaterialsFingerprint
		}
	}
	seen, err := byNFC(input.Seen)
	if err != nil {
		return obsidian.Choice{}, nil, nil, obsidian.Decisions{}, err
	}
	notes, err := byNFC(input.Decisions.Notes)
	if err != nil {
		return obsidian.Choice{}, nil, nil, obsidian.Decisions{}, err
	}
	return choice, fingerprints, seen, obsidian.Decisions{Notes: notes, States: input.Decisions.States}, nil
}

// byNFC keys the values of a request by their paths relative to the root of the vault in NFC; two paths of one folder or
// note are no request.
func byNFC[V any](values map[string]V) (map[string]V, error) {
	keyed := make(map[string]V, len(values))
	for path, value := range values {
		relative, err := obsidian.Relative(path)
		if err != nil {
			return nil, err
		}
		if _, twice := keyed[relative]; twice {
			return nil, obsidian.ErrPath
		}
		keyed[relative] = value
	}
	return keyed, nil
}

// materialPaths are the folders of materials of a choice: the folders a load of records reads.
func materialPaths(choice obsidian.Choice) []string {
	paths := make([]string, 0, len(choice.Materials))
	for _, folder := range choice.Materials {
		paths = append(paths, folder.Path)
	}
	return paths
}

// materialsRequestHash is the SHA-256 of what a load of records asks: its choice, the fingerprints of its folders of
// materials, what the author saw and what he decided.
func materialsRequestHash(choice obsidian.Choice, fingerprints, seen map[string]string, decisions obsidian.Decisions) string {
	type pair struct{ Path, Value string }
	sorted := func(values map[string]string) []pair {
		pairs := make([]pair, 0, len(values))
		for _, path := range slices.Sorted(maps.Keys(values)) {
			pairs = append(pairs, pair{Path: path, Value: values[path]})
		}
		return pairs
	}
	asText := func(values map[string]obsidian.Decision) map[string]string {
		text := map[string]string{}
		for path, decision := range values {
			text[path] = string(decision)
		}
		return text
	}
	request := struct {
		Books, Exclude []string
		Materials      []obsidian.MaterialFolder
		Fingerprints   []pair
		Seen           []pair
		Notes, States  []pair
	}{
		Books: slices.Sorted(slices.Values(choice.Books)), Exclude: slices.Sorted(slices.Values(choice.Exclude)), Materials: choice.Materials,
		Fingerprints: sorted(fingerprints), Seen: sorted(seen), Notes: sorted(asText(decisions.Notes)), States: sorted(asText(decisions.States)),
	}
	encoded, _ := json.Marshal(request)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
