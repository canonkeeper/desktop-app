// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"canonkeeper/backend/internal/adapters/tempfiles"
	coverservice "canonkeeper/backend/internal/services/library"
	sagaarchiveservice "canonkeeper/backend/internal/services/sagaarchive"
)

// SagaArchive is what the saga file asks of the build (specs/2026-11-13-desk-sagafile.md): who makes its files —
// «canonkeeper.ru» or «CanonKeeper <version>» — and how many drawings of a saga it does not carry; a build without drawings
// leaves Drawings nil.
type SagaArchive struct {
	CreatedBy string
	Drawings  sagaarchiveservice.Drawings
}

// sagaArchiveExports is the constructor of the save of a saga into its file (specs/2026-11-16-desk-site-saga.md TR1.1): the
// snapshot, the covers, the threads, the temporary files and the clock of the process, and one gate of the process for every
// save it builds — so «one save of an author at a time, four for the process, ten an hour» (SF2.4) holds for getSagaArchive
// and for the transfer of a saga to the app together.
func sagaArchiveExports(snapshot sagaarchiveservice.SnapshotStore, covers sagaarchiveservice.CoverObjects, threads sagaarchiveservice.Threads, temp sagaarchiveservice.TempFiles, now func() time.Time) func(SagaArchive) *sagaarchiveservice.ExportSaga {
	gate := sagaarchiveservice.NewGate(now)
	return func(archive SagaArchive) *sagaarchiveservice.ExportSaga {
		return &sagaarchiveservice.ExportSaga{
			Snapshot: snapshot, Covers: covers, Threads: threads, Temp: temp, Gate: gate,
			Drawings: archive.Drawings, CreatedBy: archive.CreatedBy, Now: now,
		}
	}
}

// sagaArchiveDirectory is the named directory of the temporary files of the saga file under the files of the build; its
// start clears it whole (SF2.6).
const sagaArchiveDirectory = "saga-archives"

// openSagaFiles opens the directory of the temporary files of the saga file and clears it: a process that fell left its
// files there (SF2.6) and the covers of an open it did not finish (SF3.9).
func openSagaFiles(ctx context.Context, storageDir string, stray func(context.Context, []string) ([]string, error), covers coverservice.CoverObjectStore) (*tempfiles.Directory, error) {
	directory, err := tempfiles.Open(filepath.Join(storageDir, sagaArchiveDirectory))
	if err != nil {
		return nil, errors.New("api: saga file directory unavailable")
	}
	marked, err := directory.Marked()
	if err != nil {
		return nil, errors.New("api: saga file directory unreadable")
	}
	unnamed, err := stray(ctx, marked)
	if err != nil {
		return nil, err
	}
	for _, key := range unnamed {
		if err := covers.Remove(ctx, key); err != nil {
			return nil, err
		}
	}
	if err := directory.Clear(); err != nil {
		return nil, errors.New("api: saga file directory cannot be cleared")
	}
	return directory, nil
}

// tempFilesPort gives the use cases the temporary files of the directory.
type tempFilesPort struct{ directory *tempfiles.Directory }

func (port tempFilesPort) Create() (sagaarchiveservice.TempFile, error) {
	if port.directory == nil {
		return nil, errors.New("api: no saga file directory")
	}
	return port.directory.Create()
}

// marksPort lists the covers an open puts among the files of the build in the directory of the saga file.
type marksPort struct{ directory *tempfiles.Directory }

func (port marksPort) Mark(keys []string) (sagaarchiveservice.Mark, error) {
	if port.directory == nil {
		return nil, errors.New("api: no saga file directory")
	}
	return port.directory.Mark(keys)
}

// SagaArchiveTemp is the temporary files of the saga file for the open a build mounts.
func SagaArchiveTemp(core Core) sagaarchiveservice.TempFiles { return tempFilesPort{core.SagaFiles} }
