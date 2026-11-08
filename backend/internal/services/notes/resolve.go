package notes

import (
	"context"

	agent "canonkeeper/backend/internal/domain/agent"
	timeline "canonkeeper/backend/internal/domain/agenttimeline"
	domain "canonkeeper/backend/internal/domain/notes"
)

type ChatBinding struct {
	SourceView   string `json:"sourceView"`
	ProjectID    string `json:"projectId"`
	RootThreadID string `json:"threadId"`
	JobID        string `json:"jobId"`
	TurnID       string `json:"turnId"`
	Epoch        int64  `json:"epoch"`
}
type SourceContext struct {
	Resolution domain.Resolution
	Chat       *ChatBinding
}
type ResolveRepository interface {
	ReadSource(context.Context, string, string) (SourceContext, error)
}
type NativeReader interface {
	ThreadItems(context.Context, agent.Flow, string) ([]timeline.RawItem, error)
}
type Resolve struct {
	Repository ResolveRepository
	Native     NativeReader
}

func (useCase Resolve) Execute(ctx context.Context, userID, noteID string) (domain.Resolution, error) {
	unavailable := domain.Resolution{State: "unavailable"}
	if !domain.ValidID(userID) || !domain.ValidID(noteID) {
		return unavailable, domain.ErrNotFound
	}
	before, err := useCase.Repository.ReadSource(ctx, userID, noteID)
	if err != nil {
		return unavailable, err
	}
	if before.Chat == nil {
		return before.Resolution, nil
	}
	items, err := useCase.Native.ThreadItems(ctx, agent.FlowResearchChat, before.Chat.RootThreadID)
	if err != nil {
		return unavailable, domain.ErrUnavailable
	}
	snapshot, err := timeline.Project(before.Chat.JobID, items)
	if err != nil {
		return unavailable, domain.ErrUnavailable
	}
	found := false
	source := before.Resolution.Source
	for _, event := range snapshot.Events {
		if event.Role == timeline.RoleMain && event.Kind == timeline.EventAssistant &&
			event.TurnID == source.ProviderTurnID && event.ItemID == source.ProviderItemID {
			found = true
			break
		}
	}
	after, err := useCase.Repository.ReadSource(ctx, userID, noteID)
	if err != nil {
		return unavailable, err
	}
	if !found || after.Chat == nil || *after.Chat != *before.Chat {
		return unavailable, nil
	}
	return after.Resolution, nil
}
