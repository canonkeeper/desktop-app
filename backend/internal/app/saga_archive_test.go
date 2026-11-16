// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	sagaarchiveservice "canonkeeper/backend/internal/services/sagaarchive"
)

// The saves the constructor of the core builds — getSagaArchive with the saga file of the build, the transfer of a saga to
// the app with the same or another — go through one gate (specs/2026-11-16-desk-site-saga.md TR1.1): a save of an author
// that runs keeps his second one out, whichever builds it.
func TestTheSavesOfTheCoreGoThroughOneGate(t *testing.T) {
	drawings := sagaarchiveservice.Drawings(func(context.Context, string) (int, error) { return 0, nil })
	exports := sagaArchiveExports(nil, nil, nil, nil, func() time.Time { return time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC) })
	site := exports(SagaArchive{CreatedBy: "canonkeeper.example.test", Drawings: drawings})
	transfer := exports(SagaArchive{CreatedBy: "canonkeeper.example.test", Drawings: drawings})
	if site.CreatedBy != "canonkeeper.example.test" || site.Drawings == nil || site.Now == nil {
		t.Fatalf("the save carries what the build tells: %+v", site)
	}
	release, _, ok := site.Gate.Enter("author")
	if !ok {
		t.Fatal("the first save of an author goes in")
	}
	if _, wait, ok := transfer.Gate.Enter("author"); ok || wait <= 0 {
		t.Fatalf("the second save of the author waits, whichever builds it: ok=%v wait=%v", ok, wait)
	}
	release()
	again, _, ok := transfer.Gate.Enter("author")
	if !ok {
		t.Fatal("the place given back lets the next save in")
	}
	again()
}
