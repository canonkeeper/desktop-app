package httpapi

import "testing"

func TestAgentTimelineLinkUsesExactProjectAndJobOnly(t *testing.T) {
	got := AgentTimelineLink("10000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002")
	want := "/api/v1/projects/10000000-0000-4000-8000-000000000001/jobs/20000000-0000-4000-8000-000000000002/agent-timeline"
	if got == nil || *got != want {
		t.Fatalf("AgentTimelineLink() = %#v, want %q", got, want)
	}
	if AgentTimelineLink("", "job") != nil || AgentTimelineLink("project", "") != nil {
		t.Fatal("empty identity produced a timeline link")
	}
}
