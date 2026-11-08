package app

import (
	"context"

	"canonkeeper/backend/internal/jobs"
)

type userModelJobTargetResolver interface {
	jobs.TargetAccessResolver
	jobs.TargetCancelResolver
}

type jobTargetResolver struct {
	indexing jobs.TargetAccessResolver
	chat     userModelJobTargetResolver
}

func newJobTargetResolver(indexing jobs.TargetAccessResolver, chat userModelJobTargetResolver) *jobTargetResolver {
	if indexing == nil || chat == nil {
		panic("app: job target resolvers are required")
	}
	return &jobTargetResolver{indexing: indexing, chat: chat}
}

func (resolver *jobTargetResolver) CanReadJobTarget(
	ctx context.Context,
	actor jobs.Actor,
	subject jobs.Subject,
) (bool, error) {
	switch subject.(type) {
	case jobs.CanonTextIndexSubject, jobs.ChapterCardsSubject, jobs.SagaLayerSubject:
		return resolver.indexing.CanReadJobTarget(ctx, actor, subject)
	case jobs.ResearchChatTurnSubject:
		return resolver.chat.CanReadJobTarget(ctx, actor, subject)
	default:
		return false, nil
	}
}

func (resolver *jobTargetResolver) CancelJobTarget(
	ctx context.Context,
	input jobs.TargetCancelInput,
) (jobs.CancelResult, error) {
	switch input.Job.Kind {
	case jobs.KindResearchChatTurn:
		return resolver.chat.CancelJobTarget(ctx, input)
	default:
		return jobs.CancelResult{}, jobs.ErrNotFound
	}
}

var (
	_ jobs.TargetAccessResolver = (*jobTargetResolver)(nil)
	_ jobs.TargetCancelResolver = (*jobTargetResolver)(nil)
)
