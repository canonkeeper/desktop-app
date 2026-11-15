// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
)

// PurposeProgram is the purpose of a reading of a program of the author through the bridge (specs/2026-11-15-desk-mcp.md
// MC4.2): no Job, no thread, no helper and no host.
const PurposeProgram = "mcp"

// ProgramRead is the record of one reading of a program of the author the door of the bridge writes before its answer goes
// (MC4.2): its number, its time, the author it reads as — checked in the transaction of the record, not written —, its saga
// (none for sagas), the name the program gave itself, the bytes of the page it gets, the number of the page and of the pages,
// and what of the saga the answer named. The second and last writer of the journal: the gateway writes a Sending.
type ProgramRead struct {
	ID          string
	At          time.Time
	ActorUserID string
	ProjectID   string
	Client      string
	Bytes       int64
	Page        int
	Pages       int
	Named       disclosure.Named
}

// ErrSagaGone is a reading of a saga that is no more the author's or went to «Недавно удалённые» before its record lay: no
// record, the refusal of a saga that is not there.
var ErrSagaGone = errors.New("model journal: the saga of the reading is not the author's")
