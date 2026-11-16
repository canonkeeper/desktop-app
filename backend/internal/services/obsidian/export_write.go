// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"errors"
	"path"
	"slices"
	"sync"
	"time"

	"canonkeeper/backend/internal/domain/obsidian"
)

// write is the writes of a run (OE1.2–OE1.4, OE2.5, OE2.7, OE2.8) on the context of the writes: the folder made when it is not
// there, the temporary files of the runs before removed and the manifest kept before the first reference, so the folder is a
// folder of an export from its first file; then the references that went leave and the others are written, each within a
// time of its own and named by the manifest as soon as it returned. A write past its time or a failure ends the run with what
// was done; the place of the saga stays held until a write that did not answer returns, and it still names what it wrote. The
// run ends — complete, its time on the link and its answer kept for its key — only while the link is the one it started with.
func (export Export) write(ctx context.Context, run *exportRun, start ExportStart, key ReplayKey, projectID string) (Exported, error) {
	now := export.now()
	result := Exported{Folder: run.dir, At: now, Skipped: map[obsidian.ExportSkip]ExportGroup{}}
	keeper := &exportKeeper{writer: run.writer, manifest: run.manifest}
	keeper.manifest.Format, keeper.manifest.At, keeper.manifest.Complete, keeper.manifest.Books = obsidian.ExportFormat, now, false, run.books
	steps := &exportSteps{writer: run.writer, limit: export.Limits.Note}
	defer steps.end()
	if _, err := steps.run(ctx, func(ctx context.Context) (exportOutcome, error) {
		// The folder holds its manifest once this step returned — or the run ends —: the choice of a first folder is free.
		defer run.free()
		if !run.present {
			if err := run.writer.Open(ctx, run.dir); err != nil {
				return exportOutcome{}, err
			}
		}
		if err := run.writer.Clean(ctx); err != nil {
			return exportOutcome{}, err
		}
		// OE2.8: every write of the plan is named before the first rename.
		keeper.manifest = obsidian.Intend(keeper.manifest, run.plan)
		return exportOutcome{}, keeper.keep(ctx)
	}); err != nil {
		if errors.Is(err, obsidian.ErrExportDirUnsafe) {
			return Exported{}, err
		}
		return result, nil
	}
	for _, action := range run.plan {
		switch action.Step {
		case obsidian.StepUnchanged, obsidian.StepStay:
			result.Unchanged.add(action.File.Path)
			continue
		case obsidian.StepKeep:
			result.Kept.add(action.File.Path)
			continue
		case obsidian.StepSkip:
			result.skip(action.Skip, action.File.Path)
			continue
		case obsidian.StepForget:
			keeper.drop(action.File.ID())
			continue
		}
		outcome, err := steps.run(ctx, func(ctx context.Context) (exportOutcome, error) {
			return export.act(ctx, run.writer, keeper, action, now)
		})
		if err != nil {
			// A step that answered with a failure counts what it did before it; one past its time counts nothing yet.
			outcome.count(&result)
			return result, nil
		}
		outcome.count(&result)
		if err := export.fail("written"); err != nil {
			return Exported{}, err
		}
	}
	if linked, err := export.Exports.Linked(ctx, projectID, start.VaultPath, run.dir); err != nil || !linked {
		return result, nil
	}
	if _, err := steps.run(ctx, func(ctx context.Context) (exportOutcome, error) {
		keeper.manifest.Complete = true
		return exportOutcome{}, keeper.keep(ctx)
	}); err != nil {
		return result, nil
	}
	if ended, err := export.Exports.EndExport(ctx, projectID, start.VaultPath, run.dir, now); err != nil || !ended {
		return result, nil
	}
	result.Complete = true
	// A key that did not keep its answer only repeats a run that changes nothing.
	_ = export.Exports.KeepExport(ctx, key, result)
	return result, nil
}

// act does one step of a plan that touches the disk and names its outcome in the manifest.
func (export Export) act(ctx context.Context, writer VaultWriter, keeper *exportKeeper, action obsidian.ExportAction, now time.Time) (exportOutcome, error) {
	file := action.File
	switch action.Step {
	case obsidian.StepMove, obsidian.StepReplace:
		moved, err := writer.Move(ctx, file.Path, obsidian.DeletedNames(obsidian.NoteStem(path.Base(file.Path)), now), action.Expect)
		var skipped ExportSkipped
		switch {
		case errors.Is(err, ErrChangedMeanwhile):
			keeper.unwrite(file.ID())
			return exportOutcome{kept: file.Path}, nil
		case errors.As(err, &skipped):
			keeper.unwrite(file.ID())
			return exportOutcome{skip: skipped.Skip, path: file.Path}, nil
		case errors.Is(err, ErrGoneMeanwhile):
			if action.Step == obsidian.StepMove {
				keeper.drop(file.ID())
				return exportOutcome{}, keeper.keep(ctx)
			}
		case err != nil:
			return exportOutcome{}, err
		}
		if action.Step == obsidian.StepMove {
			keeper.drop(file.ID())
			return exportOutcome{moved: moved}, keeper.keep(ctx)
		}
		written, err := export.create(ctx, writer, keeper, file, action.Text)
		if err != nil {
			return exportOutcome{moved: moved}, err
		}
		return exportOutcome{moved: moved, updated: written}, nil
	case obsidian.StepUpdate:
		err := writer.Write(ctx, file.Path, action.Text, action.Expect)
		var skipped ExportSkipped
		switch {
		case errors.Is(err, ErrChangedMeanwhile):
			keeper.unwrite(file.ID())
			return exportOutcome{kept: file.Path}, nil
		case errors.As(err, &skipped):
			keeper.unwrite(file.ID())
			return exportOutcome{skip: skipped.Skip, path: file.Path}, nil
		case err != nil:
			return exportOutcome{}, err
		}
		keeper.set(file)
		return exportOutcome{updated: file.Path}, keeper.keep(ctx)
	case obsidian.StepCreate:
		written, err := export.create(ctx, writer, keeper, file, action.Text)
		var skipped ExportSkipped
		if errors.As(err, &skipped) {
			keeper.unwrite(file.ID())
			return exportOutcome{skip: skipped.Skip, path: file.Path}, nil
		}
		return exportOutcome{created: written}, err
	}
	return exportOutcome{}, nil
}

// create writes a new reference (OE1.4): a file that lies at its path right before the rename stays where it is, and the
// reference takes the next free name of its folder; the manifest names where it went.
func (export Export) create(ctx context.Context, writer VaultWriter, keeper *exportKeeper, file obsidian.ManifestFile, text []byte) (string, error) {
	dir, stem := path.Dir(file.Path), obsidian.NoteStem(path.Base(file.Path))
	if dir == "." {
		dir = obsidian.Root
	}
	tried := map[string]bool{}
	target := file.Path
	for target != "" {
		file.Path = target
		err := ErrOccupied
		// A reference the run did not move away — changed by the author meanwhile, or not read — still holds the path.
		if obsidian.Free(keeper.manifest, file.ID(), target) {
			// OE2.8: the manifest names the write before its rename, once and on a free path.
			if !keeper.intends(file.ID(), target) {
				keeper.set(obsidian.Pending(obsidian.ExportAction{File: file}))
				if err := keeper.keep(ctx); err != nil {
					return "", err
				}
			}
			if err = writer.Write(ctx, target, text, ""); err == nil {
				keeper.set(file)
				return target, keeper.keep(ctx)
			}
		}
		if !errors.Is(err, ErrOccupied) {
			return "", err
		}
		tried[obsidian.FoldKey(target)] = true
		next := obsidian.FreeName(obsidian.Join(dir, stem), func(candidate string) bool {
			key := obsidian.FoldKey(candidate + ".md")
			return tried[key] || keeper.holds(key)
		})
		target = ""
		if next != "" {
			target = next + ".md"
		}
	}
	return "", ExportSkipped{Skip: obsidian.ExportBusy}
}

// exportOutcome is what a step of a plan did: a path created, updated, moved into _Удалено/, kept, or skipped by its reason.
type exportOutcome struct {
	created, updated, moved, kept, path string
	skip                                obsidian.ExportSkip
}

func (outcome exportOutcome) count(result *Exported) {
	for _, item := range []struct {
		path  string
		group *ExportGroup
	}{{outcome.created, &result.Created}, {outcome.updated, &result.Updated}, {outcome.moved, &result.Moved}, {outcome.kept, &result.Kept}} {
		if item.path != "" {
			item.group.add(item.path)
		}
	}
	if outcome.skip != "" {
		result.skip(outcome.skip, outcome.path)
	}
}

// skip counts a reference skipped by its reason.
func (result *Exported) skip(reason obsidian.ExportSkip, path string) {
	group := result.Skipped[reason]
	group.add(path)
	result.Skipped[reason] = group
}

// exportKeeper is the manifest of a run as it goes (OE2.8): it names a reference as soon as its write returned.
type exportKeeper struct {
	writer   VaultWriter
	manifest obsidian.Manifest
}

func (keeper *exportKeeper) set(file obsidian.ManifestFile) {
	for index := range keeper.manifest.Files {
		if keeper.manifest.Files[index].ID() == file.ID() {
			keeper.manifest.Files[index] = file
			return
		}
	}
	keeper.manifest.Files = append(keeper.manifest.Files, file)
}

// unwrite says a write named before its rename did not happen: a new reference leaves the manifest, a replaced one keeps
// what was written before.
func (keeper *exportKeeper) unwrite(id obsidian.FileID) {
	for index := range keeper.manifest.Files {
		if file := &keeper.manifest.Files[index]; file.ID() == id && file.Writing != nil {
			if file.Print == "" {
				keeper.drop(id)
				return
			}
			file.Writing = nil
			return
		}
	}
}

func (keeper *exportKeeper) drop(id obsidian.FileID) {
	keeper.manifest.Files = slices.DeleteFunc(keeper.manifest.Files, func(file obsidian.ManifestFile) bool { return file.ID() == id })
}

// intends says the manifest names a write of the reference at the path.
func (keeper *exportKeeper) intends(id obsidian.FileID, path string) bool {
	return slices.ContainsFunc(keeper.manifest.Files, func(file obsidian.ManifestFile) bool {
		return file.ID() == id && file.Path == path && file.Writing != nil
	})
}

// holds says the manifest names a path, the case ignored.
func (keeper *exportKeeper) holds(key string) bool {
	return slices.ContainsFunc(keeper.manifest.Files, func(file obsidian.ManifestFile) bool { return obsidian.FoldKey(file.Path) == key })
}

func (keeper *exportKeeper) keep(ctx context.Context) error {
	return keeper.writer.Keep(ctx, keeper.manifest.Encode())
}

// exportSteps runs the steps of the writes one by one, each within the time of one file (OE1.2). A step past its time
// answers ErrSlow and goes on by itself: the place of the saga is held until it returns (within), and the vault stays open for
// it — the last of the run and the step close it, whichever ends later.
type exportSteps struct {
	writer   VaultWriter
	limit    time.Duration
	mu       sync.Mutex
	inFlight bool
	ended    bool
}

func (steps *exportSteps) run(ctx context.Context, work func(context.Context) (exportOutcome, error)) (exportOutcome, error) {
	steps.mu.Lock()
	steps.inFlight = true
	steps.mu.Unlock()
	return within(ctx, steps.limit, func(ctx context.Context) (exportOutcome, error) {
		outcome, err := work(ctx)
		steps.mu.Lock()
		defer steps.mu.Unlock()
		steps.inFlight = false
		if steps.ended {
			_ = steps.writer.Close()
		}
		return outcome, err
	})
}

// end says the run is over: the vault closes now, or when the step that did not answer returns.
func (steps *exportSteps) end() {
	steps.mu.Lock()
	defer steps.mu.Unlock()
	steps.ended = true
	if !steps.inFlight {
		_ = steps.writer.Close()
	}
}
