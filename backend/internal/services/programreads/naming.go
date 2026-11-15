// SPDX-License-Identifier: AGPL-3.0-only

package programreads

import (
	"context"
	"sync"

	"canonkeeper/backend/internal/domain/disclosure"
)

// Naming is what the readings of one call of a program named (specs/2026-11-15-desk-mcp.md MC4.2): the port of the naming of
// the chat (researchchat.Naming) kept in the memory of the call — nothing is written until the record of the reading, which
// takes Named once the answer is cut into its pages. A handler is built for every call with a fresh one.
type Naming struct {
	mu    sync.Mutex
	named disclosure.Named
}

// NewNaming is the naming of one call.
func NewNaming() *Naming { return &Naming{} }

// Name keeps what a tool named; the thread and the saga are the call's.
func (naming *Naming) Name(_ context.Context, _, _ string, named disclosure.Named) (string, error) {
	naming.mu.Lock()
	defer naming.mu.Unlock()
	naming.named = disclosure.Merge(naming.named, named)
	return "", nil
}

// Withdraw takes nothing back: a call of a program has no correction.
func (naming *Naming) Withdraw(context.Context, string) error { return nil }

// Named is what the call named.
func (naming *Naming) Named() disclosure.Named {
	naming.mu.Lock()
	defer naming.mu.Unlock()
	return disclosure.Merge(naming.named)
}
