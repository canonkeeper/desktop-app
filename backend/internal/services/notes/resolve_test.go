package notes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"canonkeeper/backend/internal/auth"
	agent "canonkeeper/backend/internal/domain/agent"
	timeline "canonkeeper/backend/internal/domain/agenttimeline"
	domain "canonkeeper/backend/internal/domain/notes"
)

const testUser = "00000000-0000-4000-8000-000000000001"
const testNote = "00000000-0000-4000-8000-000000000002"

type sourceRepository struct {
	contexts []SourceContext
	calls    int
}

func (repo *sourceRepository) ReadSource(context.Context, string, string) (SourceContext, error) {
	result := repo.contexts[min(repo.calls, len(repo.contexts)-1)]
	repo.calls++
	return result, nil
}

type nativeReader struct {
	items []timeline.RawItem
	err   error
	calls int
}

func (native *nativeReader) ThreadItems(_ context.Context, flow agent.Flow, thread string) ([]timeline.RawItem, error) {
	native.calls++
	if flow != agent.FlowResearchChat || thread != "root" {
		return nil, errors.New("wrong binding")
	}
	return native.items, native.err
}
func chatContext() SourceContext {
	return SourceContext{
		Chat: &ChatBinding{ProjectID: testUser, RootThreadID: "root", TurnID: "product-old", JobID: "job-old", Epoch: 1, SourceView: "published"},
		Resolution: domain.Resolution{State: "current", Source: &domain.ResolvedSource{Source: domain.Source{
			Kind: "chat", ChatID: testUser, ProviderTurnID: "old-turn", ProviderItemID: "old-item", Quote: "unfinished visible copy"},
			ProjectID: testUser, SourceView: "published"}},
	}
}
func assistant(turn, item, text string, role timeline.Role) timeline.RawItem {
	raw, _ := json.Marshal(map[string]string{"id": item, "type": "agentMessage", "text": text})
	return timeline.RawItem{Role: role, TurnID: turn, TurnStatus: timeline.TurnStatusCompleted, Item: raw}
}
func TestResolveExactHistoricalAssistantAndRevalidate(t *testing.T) {
	current := chatContext()
	for _, test := range []struct {
		name      string
		items     []timeline.RawItem
		after     SourceContext
		nativeErr error
		want      string
		wantErr   error
	}{
		{"old turn", []timeline.RawItem{assistant("old-turn", "old-item", "completed later", timeline.RoleMain), assistant("new-turn", "new-item", "latest", timeline.RoleMain)}, current, nil, "current", nil},
		{"wrong turn", []timeline.RawItem{assistant("new-turn", "old-item", "wrong", timeline.RoleMain)}, current, nil, "unavailable", nil},
		{"wrong role", []timeline.RawItem{assistant("old-turn", "old-item", "private", timeline.RoleSourceEvidence)}, current, nil, "unavailable", nil},
		{"revoked while native read", []timeline.RawItem{assistant("old-turn", "old-item", "old", timeline.RoleMain)}, SourceContext{Resolution: domain.Resolution{State: "unavailable"}}, nil, "unavailable", nil},
		{"provider outage", nil, current, errors.New("provider offline"), "unavailable", domain.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &sourceRepository{contexts: []SourceContext{current, test.after}}
			native := &nativeReader{items: test.items, err: test.nativeErr}
			value, err := (Resolve{Repository: repo, Native: native}).Execute(context.Background(), testUser, testNote)
			if value.State != test.want || !errors.Is(err, test.wantErr) {
				t.Fatalf("resolve = %#v, %v", value, err)
			}
			if value.State == "unavailable" && value.Source != nil {
				t.Fatal("unavailable exposed source")
			}
			if err == nil && repo.calls != 2 {
				t.Fatal("access was not checked after native read")
			}
		})
	}
}

type createRepository struct{ command CreateCommand }

func (repo *createRepository) Create(_ context.Context, command CreateCommand) (domain.Note, error) {
	repo.command = command
	return domain.Note{Body: command.Body, Source: command.Source}, nil
}
func TestCreateAcceptsActorCopyDuringStreamingAndHashesImmutableSource(t *testing.T) {
	repo := &createRepository{}
	actor := Write{UserID: testUser, Proof: auth.SessionWriteProof{SessionHash: "proof", CSRFSecretHash: "proof"}}
	source := chatContext().Resolution.Source.Source
	value, err := (Create{Repository: repo}).Execute(context.Background(), actor, CreateInput{Body: "edited note", Source: &source}, "request-key", "POST", "/personal-notes")
	if err != nil || value.Body != "edited note" || value.Source.Quote != "unfinished visible copy" {
		t.Fatalf("create = %#v, %v", value, err)
	}
	firstHash := repo.command.RequestHash
	source.ProviderItemID = "another-item"
	_, err = (Create{Repository: repo}).Execute(context.Background(), actor, CreateInput{Body: "edited note", Source: &source}, "request-key", "POST", "/personal-notes")
	if err != nil || firstHash == repo.command.RequestHash {
		t.Fatal("replay fingerprint omitted source identity")
	}
}
