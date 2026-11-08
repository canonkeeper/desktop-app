package indexing

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/jobs"
)

type CanonJobTargetAccessStore interface {
	CanReadCanonTextIndexTarget(context.Context, string, jobs.CanonTextIndexSubject) (bool, error)
	CanReadChapterCardsTarget(context.Context, string, jobs.ChapterCardsSubject) (bool, error)
}

type JobTargetAccessResolver struct {
	store CanonJobTargetAccessStore
}

func NewJobTargetAccessResolver(store CanonJobTargetAccessStore) *JobTargetAccessResolver {
	if store == nil {
		panic("indexing: CanonJobTargetAccessStore is required")
	}
	return &JobTargetAccessResolver{store: store}
}

func (resolver *JobTargetAccessResolver) CanReadJobTarget(
	ctx context.Context,
	actor jobs.Actor,
	subject jobs.Subject,
) (bool, error) {
	actor.UserID = strings.TrimSpace(actor.UserID)
	if actor.UserID == "" {
		return false, nil
	}
	switch typed := subject.(type) {
	case jobs.CanonTextIndexSubject:
		return resolver.store.CanReadCanonTextIndexTarget(ctx, actor.UserID, typed)
	case jobs.ChapterCardsSubject:
		return resolver.store.CanReadChapterCardsTarget(ctx, actor.UserID, typed)
	case jobs.SagaLayerSubject:
		// The layer is derived from the cards of the same text version: who sees those sees it.
		return resolver.store.CanReadChapterCardsTarget(ctx, actor.UserID, jobs.ChapterCardsSubject{
			Type: jobs.KindChapterCards, ProjectID: typed.ProjectID, SourceView: typed.SourceView,
		})
	default:
		return false, nil
	}
}

var _ jobs.TargetAccessResolver = (*JobTargetAccessResolver)(nil)
