// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// maxExportFolders bounds « (N)» the folder of a first export of a saga takes (OE1.5).
const maxExportFolders = 99

// ExportInput is exportObsidianSaga (specs/2026-11-15-desk-obsidian-export.md OE1): the word of the author to move what he
// changed into _Удалено/ and write the fresh references, and the request with its key and the proof of its session.
type ExportInput struct {
	ActorUserID    string
	ProjectID      string
	ReplaceChanged bool
	Key            string
	Proof          auth.SessionWriteProof
}

// Export is exportObsidianSaga (OE1): the map of the saga and the notes of its chapters go into the folder of its export,
// a file a reference, with links to each other. The one transaction of its start first; then, in the place of the saga,
// the answer of its key, the map and the cards read as the pages read them — a map behind its cards refuses with the Job of
// the cards queued, before the vault is touched —; within the time of the reading the folder of the export, its manifest
// and the references the run replaces or moves; then the writes, each within a time of its own and each written into the
// manifest as soon as it returned. No model is called and no record of what went out is written: nothing goes out.
type Export struct {
	Exports Exports
	Saga    SagaMap
	Writers Writers
	Gate    *Gate
	Limits  Limits
	Now     func() time.Time
	// Failpoint stops a run for a test — "looked" once its reading returned within its time, "planned" once the plan is made,
	// "written" once a reference is written and the manifest names it —; nil in a build.
	Failpoint func(point string) error
}

func (export Export) Execute(ctx context.Context, input ExportInput) (Exported, error) {
	start, err := export.Exports.BeginExport(ctx, input.ActorUserID, input.ProjectID, input.Proof)
	if err != nil {
		return Exported{}, err
	}
	operation, err := enter(ctx, export.Gate, input.ProjectID)
	if err != nil {
		return Exported{}, err
	}
	defer operation.end()
	key := ReplayKey{ActorUserID: input.ActorUserID, ProjectID: input.ProjectID, Key: input.Key, Request: exportRequestHash(input.ReplaceChanged)}
	// OE1.1: the key keeps the answer of an export that ended; a replay of it writes nothing.
	if replayed, err := export.Exports.ExportReplay(ctx, key); err != nil || replayed != nil {
		if err != nil {
			return Exported{}, err
		}
		return *replayed, nil
	}
	saga, err := export.saga(ctx, input, start.Title)
	if err != nil {
		return Exported{}, err
	}
	run, err := readInOr(operation, export.Limits.Read, func(ctx context.Context) (*exportRun, error) {
		return export.look(ctx, start, saga, input.ReplaceChanged)
	}, func(late *exportRun) {
		// A reading that answered once its caller heard obsidian.slow gives back the vault and the choice of a folder.
		_ = late.writer.Close()
		late.free()
	})
	if err != nil {
		return Exported{}, err
	}
	writes, cancel := operation.writes(export.Limits.Write)
	defer cancel()
	// OE1.5: the folder a first export chose is kept before the first file.
	if run.chose {
		if err := export.Exports.KeepExportDir(writes, input.ProjectID, start.VaultPath, run.dir); err != nil {
			_ = run.writer.Close()
			run.free()
			return Exported{}, err
		}
	}
	if err := export.fail("planned"); err != nil {
		_ = run.writer.Close()
		run.free()
		return Exported{}, err
	}
	return export.write(writes, run, start, key, input.ProjectID)
}

// exportRequestHash is the SHA-256 of what an export asks: the word of the author or none.
func exportRequestHash(replaceChanged bool) string {
	sum := sha256.Sum256([]byte(map[bool]string{true: "replace", false: "keep"}[replaceChanged]))
	return hex.EncodeToString(sum[:])
}

func (export Export) fail(point string) error {
	if export.Failpoint == nil {
		return nil
	}
	return export.Failpoint(point)
}

func (export Export) now() time.Time {
	if export.Now == nil {
		return time.Now().UTC()
	}
	return export.Now().UTC()
}

// saga reads what the export writes, as the pages read it (OE1.6, OE2.3): the map — a saga without cards and a map behind its
// cards refuse, the second with the Job of the cards queued by the read of the map —, the card of every node of the map and
// the chapters of the published text with their fresh cards. A node of the map without a card is a map that changed meanwhile.
func (export Export) saga(ctx context.Context, input ExportInput, title string) (obsidian.ExportSaga, error) {
	document, err := export.Saga.Map(ctx, input.ActorUserID, input.ProjectID)
	switch {
	case err != nil:
		return obsidian.ExportSaga{}, err
	case document.Coverage.WithCards == 0:
		return obsidian.ExportSaga{}, obsidian.MapNotReady{Reason: obsidian.MapNoCards}
	case document.Building:
		return obsidian.ExportSaga{}, obsidian.MapNotReady{Reason: obsidian.MapUpdating}
	}
	nodes := make(map[string]sagamemory.MapCardDocument, len(document.Nodes))
	names := map[string]string{}
	for _, node := range document.Nodes {
		card, err := export.Saga.Node(ctx, input.ActorUserID, input.ProjectID, node.Key)
		if errors.Is(err, ErrNodeGone) {
			return obsidian.ExportSaga{}, obsidian.MapNotReady{Reason: obsidian.MapUpdating}
		}
		if err != nil {
			return obsidian.ExportSaga{}, err
		}
		nodes[node.Key] = card
		for _, name := range append([]string{node.Name}, node.Aliases...) {
			if key := sagamemory.GlossaryKey(name); key != "" && names[key] == "" {
				names[key] = node.Key
			}
		}
	}
	version, cards, err := export.Saga.Chapters(ctx, input.ProjectID)
	if err != nil {
		return obsidian.ExportSaga{}, err
	}
	return obsidian.ExportSaga{
		ID: input.ProjectID, Title: title, Map: document, Nodes: nodes, Version: version, Cards: cards.Cards,
		// A name of a card leads to the node of its entry of the glossary, the keys of the map; a record of the author the
		// book does not know, by the names of the map.
		NodeOf: func(name string) (string, bool) {
			if cards.Glossary != nil {
				if entry, ok := cards.Glossary.Lookup(name); ok {
					if _, drawn := nodes[sagamemory.EntryKey(entry)]; drawn {
						return sagamemory.EntryKey(entry), true
					}
				}
			}
			key, ok := names[sagamemory.GlossaryKey(name)]
			return key, ok
		},
	}, nil
}

// exportRun is what the reading of a run gave: the vault open for the export, the folder of the export, whether it is on the
// disk and whether the run chose it, the manifest, the folders of the books and the plan.
type exportRun struct {
	writer   VaultWriter
	dir      string
	present  bool
	chose    bool
	manifest obsidian.Manifest
	books    []obsidian.ManifestBook
	plan     []obsidian.ExportAction
	// free gives back the choice of the folder of a first export once the folder holds its manifest, or the run ends before.
	free func()
}

// look is the reading of a run (OE1.2, OE1.5, OE2.5, OE2.6): the folder of the export, its manifest, what lies at its paths,
// a reference the author moved found by its key, the references that went and the ones whose content changed read — no
// other file; then the paths, the fresh texts and the plan. It writes nothing.
func (export Export) look(ctx context.Context, start ExportStart, saga obsidian.ExportSaga, replaceChanged bool) (_ *exportRun, result error) {
	writer, err := export.Writers.OpenWriter(start.VaultPath)
	if err != nil {
		return nil, VaultMissing{Reason: obsidian.ReasonOf(err)}
	}
	run := &exportRun{writer: writer, free: func() {}}
	defer func() {
		if result != nil {
			_ = writer.Close()
			run.free()
		}
	}()
	if err := export.folder(ctx, run, start, saga.ID); err != nil {
		return nil, err
	}
	files, stay := saga.Files(), saga.Stay()
	disk := map[string]obsidian.DiskFile{}
	occupied := func(string) bool { return false }
	var slow error
	if run.present {
		if err := within1(ctx, export.Limits.Note, func(ctx context.Context) error { return writer.Open(ctx, run.dir) }); err != nil {
			return nil, err
		}
		if err := export.seen(ctx, run, files, stay, disk); err != nil {
			return nil, err
		}
		staying, moving := map[string]bool{}, map[string]bool{}
		for _, file := range run.manifest.Files {
			staying[obsidian.FoldKey(file.Path)] = true
		}
		for _, file := range obsidian.Gone(run.manifest, files, stay) {
			if seen := disk[file.Path]; seen.Missing || obsidian.Moves(file, seen, replaceChanged) {
				delete(staying, obsidian.FoldKey(file.Path))
				moving[obsidian.FoldKey(file.Path)] = true
			}
		}
		occupied = func(path string) bool {
			switch key := obsidian.FoldKey(path); {
			case staying[key]:
				return true
			case moving[key]:
				return false
			}
			exists, _, err := stat(ctx, writer, export.Limits.Note, path)
			if err != nil && slow == nil {
				slow = err
			}
			return exists || err != nil
		}
	}
	paths, books := obsidian.AssignPaths(run.manifest, files, saga.Books(), occupied)
	if slow != nil {
		return nil, slow
	}
	run.books = books
	links := obsidian.ExportPaths{Folder: run.dir, Files: map[obsidian.FileID]string{}}
	for id, path := range paths {
		links.Files[id] = path
	}
	for _, file := range run.manifest.Files {
		for _, id := range stay {
			if file.ID() == id {
				links.Files[id] = file.Path
			}
		}
	}
	var wanted []obsidian.Wanted
	for _, file := range files {
		if path, ok := paths[file.ID]; ok {
			wanted = append(wanted, obsidian.Wanted{ID: file.ID, Path: path, Text: saga.Text(file.ID, links)})
		}
	}
	if run.present {
		missing := map[string]bool{}
		for path, seen := range disk {
			missing[path] = seen.Missing
		}
		for _, path := range obsidian.ExportReads(run.manifest, wanted, stay, missing) {
			if _, known := disk[path]; known {
				continue
			}
			if disk[path], err = read(ctx, writer, export.Limits.Note, path); err != nil {
				return nil, err
			}
		}
	}
	run.plan = obsidian.PlanExport(run.manifest, wanted, stay, disk, replaceChanged)
	// A reading that ended past its time gives nothing: its vault closes with it.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := export.fail("looked"); err != nil {
		return nil, err
	}
	return run, nil
}

// folder finds the folder of the export (OE1.5): the folder the saga keeps — made anew when it is not there or bare, refused
// when its manifest does not read, chosen anew when it holds no manifest of this saga —, else CanonKeeper/<title of the
// saga>, « (2)», « (3)» past a folder of another saga, of another program or without a manifest of this saga.
func (export Export) folder(ctx context.Context, run *exportRun, start ExportStart, saga string) error {
	look := func(dir string) (ExportFolder, *obsidian.Manifest, error) {
		var state ExportFolder
		var manifest *obsidian.Manifest
		err := within1(ctx, export.Limits.Note, func(ctx context.Context) error {
			var err error
			if state, err = run.writer.Folder(ctx, dir); err != nil || state != ExportFolderPresent {
				return err
			}
			data, ok, err := run.writer.Manifest(ctx, dir)
			if err != nil || !ok {
				return err
			}
			read, err := obsidian.ReadManifest(data)
			if err != nil {
				return err
			}
			manifest = &read
			return nil
		})
		return state, manifest, err
	}
	// A missing folder and a bare one are made; a folder with this manifest is the saga's.
	free := func(state ExportFolder, manifest *obsidian.Manifest) bool {
		return state == ExportFolderMissing || state == ExportFolderBare || state == ExportFolderPresent && manifest != nil && manifest.Saga == saga
	}
	use := func(dir string, state ExportFolder, manifest *obsidian.Manifest) {
		run.dir, run.present, run.chose = dir, state == ExportFolderPresent, dir != start.ExportDir
		run.manifest = obsidian.Manifest{Format: obsidian.ExportFormat, Saga: saga}
		if manifest != nil {
			run.manifest = *manifest
		}
	}
	ours := func(state ExportFolder, manifest *obsidian.Manifest) bool {
		return state == ExportFolderPresent && manifest != nil && manifest.Saga == saga
	}
	// The folder the saga keeps with its manifest: nothing to choose, nothing to make.
	if start.ExportDir != "" {
		state, manifest, err := look(start.ExportDir)
		switch {
		case err != nil:
			return err
		case state == ExportFolderTaken:
			return obsidian.ErrExportDirUnsafe
		case ours(state, manifest):
			use(start.ExportDir, state, manifest)
			return nil
		}
	}
	// A choice: one in the process at a time, looked at anew under the lock, and held until the folder it makes holds its
	// manifest (the first step of the writes gives it back) — another saga of the same title looks at it after that and goes
	// past it. A choice that finds the folder of the saga with its manifest makes nothing and gives the lock back at once.
	release, err := export.Gate.choose(ctx)
	if err != nil {
		return err
	}
	pick := func(dir string, state ExportFolder, manifest *obsidian.Manifest) error {
		use(dir, state, manifest)
		if state == ExportFolderPresent {
			release()
		} else {
			run.free = release
		}
		return nil
	}
	if start.ExportDir != "" {
		// The folder the saga keeps is not there or bare: it is made anew, the export goes as a first one (OE1.5).
		state, manifest, err := look(start.ExportDir)
		switch {
		case err != nil:
			release()
			return err
		case state == ExportFolderTaken:
			release()
			return obsidian.ErrExportDirUnsafe
		case free(state, manifest):
			return pick(start.ExportDir, state, manifest)
		}
	}
	name := obsidian.ExportName(start.Title)
	for n := 1; n <= maxExportFolders; n++ {
		dir := obsidian.ExportRoot + "/" + obsidian.Numbered(name, n)
		state, manifest, err := look(dir)
		switch {
		case errors.Is(err, obsidian.ErrExportManifestUnreadable):
			continue
		case err != nil:
			release()
			return err
		case free(state, manifest):
			return pick(dir, state, manifest)
		}
	}
	release()
	return obsidian.ErrExportDirUnsafe
}

// seen is what lies at the paths of the manifest (OE1.7, OE2.6): a missing file is looked for among the notes of the folder the
// manifest does not name — the one whose header names this saga and this reference is the reference the author moved —; a
// reference that went is read, so the plan knows whether it moves.
func (export Export) seen(ctx context.Context, run *exportRun, files []obsidian.ExportFile, stay []obsidian.FileID, disk map[string]obsidian.DiskFile) error {
	var missing []int
	for index, file := range run.manifest.Files {
		exists, skip, err := stat(ctx, run.writer, export.Limits.Note, file.Path)
		switch {
		case err != nil:
			return err
		case !exists:
			missing = append(missing, index)
		case skip != "":
			disk[file.Path] = obsidian.DiskFile{Skip: skip}
		}
	}
	if len(missing) > 0 {
		headers, err := export.headers(ctx, run)
		if err != nil {
			return err
		}
		for _, index := range missing {
			file := &run.manifest.Files[index]
			if path, ok := obsidian.Relocated(run.manifest.Saga, file.ID(), headers); ok {
				file.Path = path
				continue
			}
			disk[file.Path] = obsidian.DiskFile{Missing: true}
		}
	}
	if err := export.settle(ctx, run, disk); err != nil {
		return err
	}
	for _, file := range obsidian.Gone(run.manifest, files, stay) {
		if _, known := disk[file.Path]; known {
			continue
		}
		seen, err := read(ctx, run.writer, export.Limits.Note, file.Path)
		if err != nil {
			return err
		}
		disk[file.Path] = seen
	}
	return nil
}

// settle settles the writes a run before named and did not say done (OE2.8): the file at the path of each is read — its
// fingerprint, and whether its header names this saga and this reference —, and the entry becomes what was written there or
// leaves the manifest.
func (export Export) settle(ctx context.Context, run *exportRun, disk map[string]obsidian.DiskFile) error {
	files := make([]obsidian.ManifestFile, 0, len(run.manifest.Files))
	for _, file := range run.manifest.Files {
		if file.Writing == nil {
			files = append(files, file)
			continue
		}
		seen, known := disk[file.Path]
		ours := false
		if !known {
			if err := within1(ctx, export.Limits.Note, func(ctx context.Context) error {
				exists, skip := run.writer.Stat(ctx, file.Path)
				switch {
				case !exists:
					seen = obsidian.DiskFile{Missing: true}
				case skip != "":
					seen = obsidian.DiskFile{Skip: skip}
				default:
					data, skip := run.writer.Read(ctx, file.Path)
					if skip != "" {
						seen = obsidian.DiskFile{Skip: skip}
						return nil
					}
					header, ok := obsidian.HeaderOf(file.Path, data)
					seen = obsidian.DiskFile{Print: obsidian.ExportPrint(data), Sum: obsidian.ExportSum(data)}
					ours = ok && header.Saga == run.manifest.Saga && header.ID == file.ID()
				}
				return nil
			}); err != nil {
				return err
			}
		}
		settled, ok := obsidian.Settle(file, seen, ours)
		if !ok {
			delete(disk, file.Path)
			continue
		}
		disk[settled.Path] = seen
		files = append(files, settled)
	}
	run.manifest.Files = files
	return nil
}

// headers are the headers of the notes of the folder of the export the manifest does not name.
func (export Export) headers(ctx context.Context, run *exportRun) ([]obsidian.ExportHeader, error) {
	named := map[string]bool{}
	for _, file := range run.manifest.Files {
		named[obsidian.FoldKey(file.Path)] = true
	}
	notes, err := run.writer.Notes(ctx)
	if err != nil {
		return nil, err
	}
	var headers []obsidian.ExportHeader
	for _, path := range notes {
		if named[obsidian.FoldKey(path)] {
			continue
		}
		var data []byte
		var skip obsidian.ExportSkip
		if err := within1(ctx, export.Limits.Note, func(ctx context.Context) error {
			data, skip = run.writer.Read(ctx, path)
			return nil
		}); err != nil {
			return nil, err
		}
		if skip != "" {
			continue
		}
		if header, ok := obsidian.HeaderOf(path, data); ok {
			headers = append(headers, header)
		}
	}
	return headers, nil
}

// stat says whether a file lies at a path and what of it does not read, within the time of one file.
func stat(ctx context.Context, writer VaultWriter, limit time.Duration, path string) (bool, obsidian.ExportSkip, error) {
	var exists bool
	var skip obsidian.ExportSkip
	err := within1(ctx, limit, func(ctx context.Context) error {
		exists, skip = writer.Stat(ctx, path)
		return nil
	})
	return exists, skip, err
}

// read is what a run sees of a reference it read, within the time of one file: its fingerprint, or why it did not read.
func read(ctx context.Context, writer VaultWriter, limit time.Duration, path string) (obsidian.DiskFile, error) {
	var seen obsidian.DiskFile
	err := within1(ctx, limit, func(ctx context.Context) error {
		exists, skip := writer.Stat(ctx, path)
		switch {
		case !exists:
			seen = obsidian.DiskFile{Missing: true}
		case skip != "":
			seen = obsidian.DiskFile{Skip: skip}
		default:
			data, skip := writer.Read(ctx, path)
			if skip != "" {
				seen = obsidian.DiskFile{Skip: skip}
			} else {
				seen = obsidian.DiskFile{Print: obsidian.ExportPrint(data), Sum: obsidian.ExportSum(data)}
			}
		}
		return nil
	})
	return seen, err
}

// within1 runs one operation on the files of the vault within its time (OB0.1): past it ErrSlow, and the place of the saga
// is held until the operation returns.
func within1(ctx context.Context, limit time.Duration, work func(context.Context) error) error {
	_, err := within(ctx, limit, func(ctx context.Context) (struct{}, error) { return struct{}{}, work(ctx) })
	return err
}
