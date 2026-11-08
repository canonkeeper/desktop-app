package app

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

func TestJobTargetResolverDispatchesClosedKinds(t *testing.T) {
	indexing := &jobTargetResolverStub{readable: true}
	chat := &userModelJobTargetResolverStub{jobTargetResolverStub: jobTargetResolverStub{readable: true}}
	resolver := newJobTargetResolver(indexing, chat)
	actor := jobs.Actor{UserID: "actor"}

	indexSubject := jobs.CanonTextIndexSubject{
		Type: jobs.KindCanonTextIndex, ProjectID: "project", SourceView: governance.SourceViewDraft,
		CatalogRevision: 1,
	}
	if readable, err := resolver.CanReadJobTarget(context.Background(), actor, indexSubject); err != nil || !readable {
		t.Fatalf("index access = (%v, %v)", readable, err)
	}
	chatSubject := jobs.ResearchChatTurnSubject{
		Type: jobs.KindResearchChatTurn, ProjectID: "project", ChatID: "chat", TurnID: "turn",
		SourceView: governance.SourceViewDraft, Scope: "project",
	}
	if readable, err := resolver.CanReadJobTarget(context.Background(), actor, chatSubject); err != nil || !readable {
		t.Fatalf("chat access = (%v, %v)", readable, err)
	}
	cardsSubject := jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: "project", SourceView: governance.SourceViewDraft}
	if readable, err := resolver.CanReadJobTarget(context.Background(), actor, cardsSubject); err != nil || !readable {
		t.Fatalf("cards access = (%v, %v)", readable, err)
	}
	if indexing.readCalls != 2 || chat.readCalls != 1 {
		t.Fatalf("read calls = indexing:%d chat:%d", indexing.readCalls, chat.readCalls)
	}

	job := jobs.Job{Kind: jobs.KindResearchChatTurn}
	if _, err := resolver.CancelJobTarget(context.Background(), jobs.TargetCancelInput{Job: job}); err != nil {
		t.Fatal(err)
	}
	if chat.cancelCalls != 1 {
		t.Fatalf("chat cancel calls = %d", chat.cancelCalls)
	}
	if _, err := resolver.CancelJobTarget(context.Background(), jobs.TargetCancelInput{
		Job: jobs.Job{Kind: jobs.KindChapterCards},
	}); err != jobs.ErrNotFound {
		t.Fatalf("non-chat cancel error = %v", err)
	}
}

func TestJobTargetResolverRequiresIndexingAndChat(t *testing.T) {
	for _, test := range []struct {
		name     string
		indexing jobs.TargetAccessResolver
		chat     userModelJobTargetResolver
	}{
		{name: "indexing", chat: &userModelJobTargetResolverStub{}},
		{name: "chat", indexing: &jobTargetResolverStub{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("newJobTargetResolver accepted missing %s resolver", test.name)
				}
			}()
			_ = newJobTargetResolver(test.indexing, test.chat)
		})
	}
}

type jobTargetResolverStub struct {
	readable  bool
	readCalls int
}

func (stub *jobTargetResolverStub) CanReadJobTarget(
	context.Context,
	jobs.Actor,
	jobs.Subject,
) (bool, error) {
	stub.readCalls++
	return stub.readable, nil
}

type userModelJobTargetResolverStub struct {
	jobTargetResolverStub
	cancelCalls int
}

func (stub *userModelJobTargetResolverStub) CancelJobTarget(
	context.Context,
	jobs.TargetCancelInput,
) (jobs.CancelResult, error) {
	stub.cancelCalls++
	return jobs.CancelResult{}, nil
}
