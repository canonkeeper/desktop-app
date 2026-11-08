package researchchat

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	service "canonkeeper/backend/internal/services/researchchat"
)

type panickingReadService struct{ readServiceStub }

func (panickingReadService) Search(context.Context, service.ToolContext, service.SearchInput) (string, error) {
	var hits map[string]int
	hits["SECRET_QUERY"]++
	return "", nil
}

func TestToolPanicBecomesRetryableOutcomeAndIsLogged(t *testing.T) {
	var output bytes.Buffer
	handler := NewHandler(panickingReadService{}, memoryStub{})
	handler.logger = log.New(&output, "", 0)

	result, err := handler.Handle(context.Background(), call("search", `{"queries":["вьюжница"]}`))

	if err != nil {
		t.Fatalf("Handle() error = %v, want a typed outcome", err)
	}
	got := failure(t, result)
	if result.Success || got.Status != "error" || got.Type != "tool_failed" ||
		len(got.NextValidActions) != 1 || got.NextValidActions[0] != "retry_same_tool" {
		t.Fatalf("panic outcome = %+v", got)
	}
	line := output.String()
	if !strings.Contains(line, `"event":"tool_panic"`) || !strings.Contains(line, `"tool":"saga.search"`) ||
		!strings.Contains(line, "assignment to entry in nil map") || !strings.Contains(line, "goroutine") {
		t.Fatalf("tool panic log = %q", line)
	}
	if strings.Contains(line, "вьюжница") {
		t.Fatalf("tool panic log leaked the call arguments: %q", line)
	}
}

type failingReadService struct{ readServiceStub }

func (failingReadService) Read(context.Context, service.ToolContext, service.ReadInput) (string, error) {
	return "", errors.New("ERROR: deadlock detected (SQLSTATE 40P01)")
}

// An unexpected failure stays retryable for the model and leaves a line for the operator: a
// read_failed outcome without a log hid the deadlocks of parallel calls on Codex 0.165.
func TestUnexpectedToolFailureIsRetryableAndLogged(t *testing.T) {
	var output bytes.Buffer
	handler := NewHandler(failingReadService{}, memoryStub{})
	handler.logger = log.New(&output, "", 0)

	result, err := handler.Handle(context.Background(), call("read", `{"chapter":"вьюжница"}`))

	if err != nil {
		t.Fatalf("Handle() error = %v, want a typed outcome", err)
	}
	if got := failure(t, result); result.Success || got.Type != "read_failed" || got.NextValidActions[0] != "retry_same_tool" {
		t.Fatalf("failure outcome = %+v", got)
	}
	line := output.String()
	if !strings.Contains(line, `"event":"tool_failed"`) || !strings.Contains(line, `"tool":"saga.read"`) ||
		!strings.Contains(line, "SQLSTATE 40P01") {
		t.Fatalf("tool failure log = %q", line)
	}
	if strings.Contains(line, "вьюжница") {
		t.Fatalf("tool failure log leaked the call arguments: %q", line)
	}
}
