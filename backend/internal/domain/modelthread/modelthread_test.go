// SPDX-License-Identifier: AGPL-3.0-only

package modelthread

import (
	"context"
	"slices"
	"testing"
)

// specs/2026-11-17-model-cache.md MC1.1, MC1.3: the parts of a thread go with the context in their order; a thread with an
// empty part — a call outside a Job — is none, and the last thread put is the thread of the call.
func TestTheThreadOfACallIsItsPartsInTheirOrder(t *testing.T) {
	if _, ok := Of(context.Background()); ok {
		t.Fatal("a context without a thread has one")
	}
	ctx := With(context.Background(), "cards", "job-north", "card", "7")
	if parts, ok := Of(ctx); !ok || !slices.Equal(parts, []string{"cards", "job-north", "card", "7"}) {
		t.Fatalf("Of() = %q, %v", parts, ok)
	}
	if _, ok := Of(With(context.Background(), "cards", "", "card")); ok {
		t.Fatal("a thread with an empty part was kept")
	}
	if _, ok := Of(With(context.Background())); ok {
		t.Fatal("a thread without parts was kept")
	}
	inner := With(ctx, "proofread", "job-north", "2")
	if parts, _ := Of(inner); !slices.Equal(parts, []string{"proofread", "job-north", "2"}) {
		t.Fatalf("the inner thread = %q", parts)
	}
	parts, _ := Of(ctx)
	parts[0] = "changed"
	if again, _ := Of(ctx); again[0] != "cards" {
		t.Fatal("the parts of the context changed under a reader")
	}
}
