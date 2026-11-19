// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"errors"
	"testing"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// gateOfTheTest is the gate of new turns: closed while an update is installed.
type gateOfTheTest struct{ closed bool }

func (gate gateOfTheTest) Admit() (func(), error) {
	if gate.closed {
		return nil, errors.New("the app is installing an update")
	}
	return func() {}, nil
}

type copiesOfTheTest struct{ removed int }

func (copies *copiesOfTheTest) Remove() error {
	copies.removed++
	return nil
}

// specs/2026-11-19-desk-trust.md TW3.5: the copies go only while no update is installed; while one is, nothing is removed.
func TestTheCopiesGoOnlyWhileNoUpdateIsInstalled(t *testing.T) {
	copies := &copiesOfTheTest{}
	if err := (DeleteBackups{Backups: copies, Gate: gateOfTheTest{closed: true}}).Execute(); !errors.Is(err, domain.ErrUpdating) || copies.removed != 0 {
		t.Fatalf("while an update is installed = %v, removed %d", err, copies.removed)
	}
	if err := (DeleteBackups{Backups: copies, Gate: gateOfTheTest{}}).Execute(); err != nil || copies.removed != 1 {
		t.Fatalf("without an update = %v, removed %d", err, copies.removed)
	}
}
