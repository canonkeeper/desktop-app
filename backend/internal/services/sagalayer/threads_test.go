package sagalayer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/modelthread"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// threadedLayerModel keeps the thread of each call of a step with its input, the checks running in parallel.
type threadedLayerModel struct {
	*layerModel
	mu    sync.Mutex
	calls map[string][]threadedCall
}

type threadedCall struct {
	parts []string
	input string
}

func (model *threadedLayerModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	parts, _ := modelthread.Of(ctx)
	model.mu.Lock()
	if model.calls == nil {
		model.calls = map[string][]threadedCall{}
	}
	model.calls[stepName(instructions)] = append(model.calls[stepName(instructions)], threadedCall{parts: parts, input: input})
	model.mu.Unlock()
	return model.layerModel.Complete(ctx, instructions, input)
}

// specs/2026-11-17-model-cache.md MC1.3: a book goes under the thread of its step and its number in the Job, the reduction
// and «что упущено» under the thread of their step, and a check of a record under the thread of its step and the SHA-256 of
// the text of the record.
func TestTheStepsOfTheLayerGoUnderTheThreadsOfTheirJob(t *testing.T) {
	model := &threadedLayerModel{layerModel: newLayerModel()}
	version := layerVersion("h1")
	store := &layerStore{stamps: stampsOf(version)}
	build := NewBuildLayer(sagamemorytest.NewSaga(version, nil, layerCards()), store, model, &layerUsage{}, "test-model", 4, nil)
	if _, err := build.RunForClaim(jobs.WithJobID(context.Background(), layerJobID), layerClaim, layerSubject); err != nil {
		t.Fatal(err)
	}
	books := map[string]bool{}
	for _, call := range model.calls["map"] {
		number := strings.TrimPrefix(strings.SplitN(call.input, "\n", 2)[0], "ТОМ ")
		if want := []string{"layer", layerJobID, "layer_book", number}; !slices.Equal(call.parts, want) {
			t.Fatalf("a book went under %q, want %q", call.parts, want)
		}
		books[number] = true
	}
	if len(books) != 2 {
		t.Fatalf("the books went under %d threads", len(books))
	}
	for step, name := range map[string]string{"reduce": "layer_reduce", "gap": "layer_gap"} {
		if calls := model.calls[step]; len(calls) != 1 || !slices.Equal(calls[0].parts, []string{"layer", layerJobID, name}) {
			t.Fatalf("%s went under %+v", step, calls)
		}
	}
	checks := model.calls["verify"]
	if len(checks) == 0 {
		t.Fatal("no check was asked")
	}
	for _, check := range checks {
		record := strings.SplitN(strings.TrimPrefix(check.input, "ЗАПИСЬ: "), " (возникла ", 2)[0]
		sum := sha256.Sum256([]byte(record))
		if want := []string{"layer", layerJobID, "layer_verify", hex.EncodeToString(sum[:])}; !slices.Equal(check.parts, want) {
			t.Fatalf("the check of %q went under %q, want %q", record, check.parts, want)
		}
	}
}
