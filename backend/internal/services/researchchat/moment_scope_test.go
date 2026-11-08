package researchchat

import (
	"context"
	"strings"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// specs/2026-11-06-writing-agent.md WA3.3: the helper of a chapter in writing reads the saga only before the place of
// its draft — a chapter after it is refused with its reason, a search looks only before it — and the chat reads all.
func TestTheHelperOfAChapterReadsOnlyBeforeItsPlace(t *testing.T) {
	tools, repository := newTestTools(researchdomain.ScopeProject, "")
	repository.authority.Moment, repository.authority.MomentAfterChapterID = true, "c2"
	ctx := context.Background()
	_, err := tools.Read(ctx, rootCall, ReadInput{Chapter: "3"})
	if outcome, ok := AsToolOutcome(err); !ok || outcome.Type != OutcomeAfterPlace {
		t.Fatalf("read after the place = %v", err)
	}
	if _, err := tools.Read(ctx, rootCall, ReadInput{Chapter: "1"}); err != nil {
		t.Fatalf("read before the place = %v", err)
	}
	found, err := tools.Search(ctx, rootCall, SearchInput{Queries: []string{"вьюжница"}})
	if err != nil || !strings.Contains(found, "Ищу только в главах до места черновика") || strings.Contains(found, "гл. 3 ") {
		t.Fatalf("search = %q, %v", found, err)
	}
	listed, err := tools.Chapters(ctx, rootCall, ChaptersInput{})
	if err != nil || strings.Contains(listed, "Глава 3") {
		t.Fatalf("chapters = %q, %v", listed, err)
	}

	repository.authority.MomentAfterChapterID, repository.authority.MomentBookID = "", "b1"
	if empty, err := tools.Search(ctx, rootCall, SearchInput{Queries: []string{"вьюжница"}}); err != nil || !strings.Contains(empty, "До места черновика глав нет") {
		t.Fatalf("a place at the start of the saga = %q, %v", empty, err)
	}

	chat, _ := newTestTools(researchdomain.ScopeProject, "")
	if _, err := chat.Read(ctx, rootCall, ReadInput{Chapter: "3"}); err != nil {
		t.Fatalf("the chat reads all = %v", err)
	}
}
