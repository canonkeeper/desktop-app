package deletion

import (
	"reflect"
	"strings"
	"testing"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

func TestMergeRootCandidatesDropsRepeatedSelectorsAndThreads(t *testing.T) {
	const projectID = "10000000-0000-4000-8000-000000000001"
	chat := func(chatID, threadID string) deletiondomain.RootCandidate {
		return deletiondomain.RootCandidate{ProjectID: projectID, Selector: deletiondomain.RootSelector{
			Flow: agentdomain.FlowResearchChat, CorrelationID: chatID, ThreadID: threadID,
		}}
	}
	got, err := mergeRootCandidates(projectID, []deletiondomain.RootCandidate{
		chat("chat-1", "root-chat"), chat("chat-2", ""), chat("chat-1", "root-chat"), chat("chat-3", "root-chat"), chat("chat-2", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []deletiondomain.RootCandidate{chat("chat-1", "root-chat"), chat("chat-2", "")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selectors = %#v, want %#v", got, want)
	}
	if _, err := mergeRootCandidates(projectID, []deletiondomain.RootCandidate{chat("", "root-chat")}); err == nil {
		t.Fatal("a root without its chat correlation was accepted")
	}
}

func TestReleasedChatRootUsesConfirmedCorrelationDelete(t *testing.T) {
	candidate := deletiondomain.RootCandidate{
		ProjectID: "10000000-0000-4000-8000-000000000001",
		Selector:  deletiondomain.RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "30000000-0000-4000-8000-000000000001"},
	}
	rootID, err := releasedRootBindingID(candidate, "root-chat")
	if err != nil || rootID != "root-chat" {
		t.Fatalf("released root = %q, %v", rootID, err)
	}
}

func TestPermissionLossRootSelectorsUseDurableUserCorrelation(t *testing.T) {
	if strings.Count(permissionLossRootSelectorsSQL, "job.creator_user_id = $2") != 1 ||
		strings.Contains(permissionLossRootSelectorsSQL, "JOIN job_model_bindings") {
		t.Fatal("permission-loss roots are not selected by durable user Job correlation")
	}
}

func TestDeletionRootParamsPreserveExactProductScope(t *testing.T) {
	const (
		projectID   = "10000000-0000-4000-8000-000000000001"
		bookID      = "20000000-0000-4000-8000-000000000001"
		chatID      = "30000000-0000-4000-8000-000000000001"
		tombstoneID = "40000000-0000-4000-8000-000000000001"
	)
	project, err := governance.ProjectDeletionVector(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	book, err := governance.BookDeletionVector(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1, BookID: bookID, BookGeneration: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := governance.ChatDeletionVector(projectID, chatID, 7)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	deadline, err := governance.PresentPurgeDeadline(now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, kind, bookID, chatID string
		epoch                      int64
		selection                  governance.DeletionSelectedVector
		mode                       governance.PurgeMode
		deadline                   governance.PurgeDeadlineArm
	}{
		{name: "project", kind: "project", selection: project, mode: governance.PurgeModeDerivedGeneration, deadline: deadline},
		{name: "book", kind: "book", bookID: bookID, selection: book, mode: governance.PurgeModeDerivedGeneration, deadline: deadline},
		{name: "chat", kind: "chat", chatID: chatID, epoch: 7, selection: chat, mode: governance.PurgeModeChatEpoch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tombstone := deletiondomain.Tombstone{
				ProjectID: projectID,
				Identity: governance.DeletionTombstoneInput{
					TombstoneID: tombstoneID, Origin: governance.DeletionOriginGovernedOnline,
					Mode: test.mode, SelectedVector: test.selection, FencedAt: now, Deadline: test.deadline,
				},
				Status: deletiondomain.TombstoneProcessing,
			}
			params, err := deletionRootParams(tombstone)
			if err != nil {
				t.Fatal(err)
			}
			if params.projectID.String() != projectID || params.selectedKind != test.kind ||
				params.bookID.String() != test.bookID || params.chatSessionID.String() != test.chatID ||
				params.chatLifecycleEpoch.Int64 != test.epoch || params.purgeMode != string(test.mode) {
				t.Fatalf("root params = %#v", params)
			}
		})
	}
}
