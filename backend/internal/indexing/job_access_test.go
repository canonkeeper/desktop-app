package indexing

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

func TestJobTargetAccessResolverDispatchesCanonTargets(t *testing.T) {
	store := &canonJobTargetAccessStoreForTest{allowed: true}
	resolver := NewJobTargetAccessResolver(store)
	textSubject := jobs.CanonTextIndexSubject{
		Type: jobs.KindCanonTextIndex, ProjectID: "project-1",
		SourceView: governance.SourceViewDraft, CatalogRevision: 4,
	}

	allowed, err := resolver.CanReadJobTarget(
		context.Background(), jobs.Actor{UserID: " user-1 "}, textSubject,
	)
	if err != nil || !allowed {
		t.Fatalf("text CanReadJobTarget() = (%v, %v), want (true, nil)", allowed, err)
	}
	if len(store.textCalls) != 1 || store.textCalls[0].actorID != "user-1" || store.textCalls[0].subject != textSubject {
		t.Fatalf("text calls = %#v", store.textCalls)
	}

	cardsSubject := jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: "project-1", SourceView: governance.SourceViewDraft}
	allowed, err = resolver.CanReadJobTarget(context.Background(), jobs.Actor{UserID: "user-3"}, cardsSubject)
	if err != nil || !allowed || len(store.cardsCalls) != 1 || store.cardsCalls[0] != cardsSubject {
		t.Fatalf("cards CanReadJobTarget() = (%v, %v), calls %#v", allowed, err, store.cardsCalls)
	}
}

func TestJobTargetAccessResolverRejectsUnrelatedKindsAndEmptyActor(t *testing.T) {
	store := &canonJobTargetAccessStoreForTest{allowed: true}
	resolver := NewJobTargetAccessResolver(store)

	for _, test := range []struct {
		name    string
		actorID string
		subject jobs.Subject
	}{
		{
			name: "unrelated kind", actorID: "user-1",
			subject: jobs.ImportSubject{Type: jobs.KindImport, ID: "project-1", ProjectID: "project-1"},
		},
		{
			name: "empty actor", actorID: " ",
			subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: "project-1",
				SourceView: governance.SourceViewDraft, CatalogRevision: 1,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			allowed, err := resolver.CanReadJobTarget(
				context.Background(), jobs.Actor{UserID: test.actorID}, test.subject,
			)
			if err != nil || allowed {
				t.Fatalf("CanReadJobTarget() = (%v, %v), want (false, nil)", allowed, err)
			}
		})
	}
	if len(store.textCalls) != 0 || len(store.cardsCalls) != 0 {
		t.Fatalf("store calls = %#v / %#v, want none", store.textCalls, store.cardsCalls)
	}
}

type canonTextAccessCallForTest struct {
	actorID string
	subject jobs.CanonTextIndexSubject
}

type canonJobTargetAccessStoreForTest struct {
	allowed    bool
	textCalls  []canonTextAccessCallForTest
	cardsCalls []jobs.ChapterCardsSubject
}

func (store *canonJobTargetAccessStoreForTest) CanReadChapterCardsTarget(
	_ context.Context,
	_ string,
	subject jobs.ChapterCardsSubject,
) (bool, error) {
	store.cardsCalls = append(store.cardsCalls, subject)
	return store.allowed, nil
}

func (store *canonJobTargetAccessStoreForTest) CanReadCanonTextIndexTarget(
	_ context.Context,
	actorID string,
	subject jobs.CanonTextIndexSubject,
) (bool, error) {
	store.textCalls = append(store.textCalls, canonTextAccessCallForTest{actorID: actorID, subject: subject})
	return store.allowed, nil
}
