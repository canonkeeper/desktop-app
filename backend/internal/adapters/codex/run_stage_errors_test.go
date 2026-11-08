package codex

import (
	"context"
	"errors"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

func TestInvalidRunRequestPreservesSafeStageCode(t *testing.T) {
	_, err := (&Client{}).Run(t.Context(), RunRequest{})
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.run_request_invalid" {
		t.Fatalf("invalid run error = %v, want safe runtime.run_request_invalid", err)
	}
}

func TestProjectCreateFailurePreservesSafeStageCode(t *testing.T) {
	transport := newFakeTransport()
	transport.projectCreateErr = errors.New("provider project detail")
	client := newTestClient(t, transport)

	_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "", false, new(string)))
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.project_create_failed" {
		t.Fatalf("project create error = %v, want safe runtime.project_create_failed", err)
	}
	if transport.methodCount("thread/list") != 0 || transport.methodCount("thread/start") != 0 {
		t.Fatalf("project create failure calls: thread/list=%d thread/start=%d", transport.methodCount("thread/list"), transport.methodCount("thread/start"))
	}
}

func TestThreadListFailurePreservesSafeStageCode(t *testing.T) {
	transport := newFakeTransport()
	transport.threadListErr = errors.New("provider thread list detail")
	client := newTestClient(t, transport)

	_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "", false, new(string)))
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.thread_list_failed" {
		t.Fatalf("thread list error = %v, want safe runtime.thread_list_failed", err)
	}
	if transport.methodCount("thread/list") != 1 || transport.methodCount("thread/start") != 0 {
		t.Fatalf("thread list failure calls: thread/list=%d thread/start=%d", transport.methodCount("thread/list"), transport.methodCount("thread/start"))
	}
}

func TestMultipleProjectRootsPreserveSafeCorrelationConflict(t *testing.T) {
	transport := newFakeTransport()
	transport.projects["job"] = "project-job"
	transport.addThread(testProtocolThread("root-one", "project-job", "", ""))
	transport.addThread(testProtocolThread("root-two", "project-job", "", ""))
	client := newTestClient(t, transport)

	_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "", false, new(string)))
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.root_correlation_conflict" {
		t.Fatalf("multiple roots error = %v, want safe runtime.root_correlation_conflict", err)
	}
}

func TestThreadStartFailurePreservesSafeStageCode(t *testing.T) {
	transport := newFakeTransport()
	transport.threadStartErr = errors.New("provider thread start detail")
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "", false, new(string))
	bindCalls := 0
	request.BindRoot = func(context.Context, string) error {
		bindCalls++
		return nil
	}

	_, err := client.Run(t.Context(), request)
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.thread_start_failed" {
		t.Fatalf("thread start error = %v, want safe runtime.thread_start_failed", err)
	}
	if !strings.Contains(err.Error(), "provider thread start detail") {
		t.Fatalf("thread start error = %q, want the app-server cause for the diagnostic log", err)
	}
	if bindCalls != 0 || transport.methodCount("thread/start") != 1 || transport.methodCount("turn/start") != 0 {
		t.Fatalf("thread start failure calls: bind=%d thread/start=%d turn/start=%d", bindCalls, transport.methodCount("thread/start"), transport.methodCount("turn/start"))
	}
}

func TestThreadResumeFailurePreservesSafeStageCode(t *testing.T) {
	transport := newFakeTransport()
	transport.threadResumeErr = errors.New("provider thread resume detail")
	transport.projects["job"] = "project-job"
	transport.addThread(testProtocolThread("root-existing", "project-job", "", ""))
	client := newTestClient(t, transport)

	_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "root-existing", false, new(string)))
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.thread_resume_failed" {
		t.Fatalf("thread resume error = %v, want safe runtime.thread_resume_failed", err)
	}
}

func TestThreadReadFailurePreservesSafeStageCode(t *testing.T) {
	transport := newFakeTransport()
	transport.threadReadErr = errors.New("provider thread read detail")
	transport.projects["job"] = "project-job"
	transport.addThread(testProtocolThread("root-existing", "project-job", "", ""))
	client := newTestClient(t, transport)

	_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "root-existing", false, new(string)))
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.thread_read_failed" {
		t.Fatalf("thread read error = %v, want safe runtime.thread_read_failed", err)
	}
}

func TestTurnListFailurePreservesSafeStageCode(t *testing.T) {
	transport := newFakeTransport()
	transport.turnListErr = errors.New("provider turn list detail")
	transport.projects["job"] = "project-job"
	transport.addThread(testProtocolThread("root-existing", "project-job", "", ""))
	client := newTestClient(t, transport)

	_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "root-existing", false, new(string)))
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.turn_list_failed" {
		t.Fatalf("turn list error = %v, want safe runtime.turn_list_failed", err)
	}
}

func TestTurnStartFailurePreservesSafeStageCode(t *testing.T) {
	transport := newFakeTransport()
	transport.turnStartErr = errors.New("provider turn start detail")
	client := newTestClient(t, transport)

	_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "", false, new(string)))
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.turn_start_failed" {
		t.Fatalf("turn start error = %v, want safe runtime.turn_start_failed", err)
	}
}

func TestBindRootFailurePreservesSafeStageCode(t *testing.T) {
	transport := newFakeTransport()
	client := newTestClient(t, transport)
	bindErr := errors.New("repository bind detail")
	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.BindRoot = func(context.Context, string) error { return bindErr }

	_, err := client.Run(t.Context(), request)
	var classified interface{ SafeErrorCode() string }
	if !errors.Is(err, bindErr) || !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.root_bind_failed" {
		t.Fatalf("root bind error = %v, want preserved cause and safe runtime.root_bind_failed", err)
	}
	if transport.methodCount("turn/start") != 0 {
		t.Fatalf("root bind failure started %d turns", transport.methodCount("turn/start"))
	}
}

func TestResultFromTurnKeepsNativeTurnErrorForDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name string
		info protocol.Optional[protocol.CodexErrorInfo]
		want string
	}{
		{name: "string info", info: protocol.Some(protocol.CodexErrorInfoServerOverloaded), want: "codexErrorInfo=serverOverloaded: upstream overloaded"},
		{name: "without info", want: "upstream overloaded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := resultFromTurn(protocol.Turn{
				ID: "turn-1", Status: protocol.TurnStatusFailed,
				Error: protocol.Some(protocol.TurnError{CodexErrorInfo: test.info, Message: "upstream overloaded"}),
			})
			if result.Status != agent.TurnFailed || result.Error != test.want {
				t.Fatalf("result = %+v, want failed with error %q", result, test.want)
			}
		})
	}
}
