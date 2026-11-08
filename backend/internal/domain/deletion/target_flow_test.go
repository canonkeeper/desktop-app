package deletion

import (
	"errors"
	"testing"

	agentdomain "canonkeeper/backend/internal/domain/agent"
)

func TestRootSelectorRequiresClosedNativeFlow(t *testing.T) {
	valid := RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "chat"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid selector: %v", err)
	}
	for _, selector := range []RootSelector{
		{CorrelationID: "job"},
		{Flow: agentdomain.Flow("indexing"), CorrelationID: "job"},
	} {
		if err := selector.Validate(); !errors.Is(err, ErrInvalidTargetRequest) {
			t.Fatalf("selector %#v error = %v, want ErrInvalidTargetRequest", selector, err)
		}
	}
}
