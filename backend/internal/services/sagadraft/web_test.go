package sagadraft

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/sagadraft"
)

var (
	webWrite = Write{Actor: Actor{UserID: "user", ProjectID: "project"}, Proof: auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}}
	webNow   = func() time.Time { return time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC) }
)

type webStore struct {
	created []CreateCommand
	changes []domain.Change
}

func (store *webStore) CreateChapterDraft(_ context.Context, command CreateCommand) (domain.Draft, error) {
	store.created = append(store.created, command)
	return domain.Draft{ID: "draft"}, nil
}

func (store *webStore) UpdateChapterDraft(_ context.Context, _ Write, _ string, _ int64, change domain.Change) (domain.Draft, error) {
	store.changes = append(store.changes, change)
	return domain.Draft{}, nil
}

// R7.2: a text is checked before it is kept and is kept as it is typed.
func TestCreateAndUpdateCheckWhatIsKept(t *testing.T) {
	store := &webStore{}
	create := Create{Store: store, Now: webNow}
	request := Request{Key: "key-12345", Method: "POST", Path: "/p"}
	for name, input := range map[string]CreateInput{
		"unknown kind":     {Kind: "fact"},
		"what to happen":   {Kind: "skeleton", Text: "встреча"},
		"long idea":        {Kind: "sketch", Idea: strings.Repeat("т", domain.MaxIdeaRunes+1)},
		"long title":       {Kind: "sketch", Title: strings.Repeat("т", domain.MaxTitleRunes+1)},
		"long text":        {Kind: "sketch", Text: strings.Repeat("т", domain.MaxChapterRunes+1)},
		"zero byte":        {Kind: "sketch", Text: "а\x00б"},
		"bad encoding":     {Kind: "sketch", Text: "\xff"},
		"chapter, no book": {Kind: "sketch", Place: domain.Place{AfterChapterID: "chapter"}},
	} {
		if _, err := create.Execute(context.Background(), webWrite, input, request); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	const typed = "  набросок\n\n"
	if _, err := create.Execute(context.Background(), webWrite, CreateInput{Kind: "sketch", Title: "  Глава  ", Idea: " замысел ", Text: typed}, request); err != nil {
		t.Fatal(err)
	}
	if command := store.created[0]; command.Title != "Глава" || command.Idea != " замысел " || command.Text != typed || command.Kind != domain.KindSketch {
		t.Fatalf("kept %+v", command)
	}
	update := Update{Store: store}
	if _, err := update.Execute(context.Background(), webWrite, "draft", 1, domain.Change{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("an empty edit: %v", err)
	}
	if _, err := update.Execute(context.Background(), webWrite, "draft", 0, domain.Change{Text: strPtr("т")}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("no revision: %v", err)
	}
	if _, err := update.Execute(context.Background(), webWrite, "draft", 1, domain.Change{Text: strPtr(strings.Repeat("т", domain.MaxChapterRunes+1))}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a long text: %v", err)
	}
	if _, err := update.Execute(context.Background(), webWrite, "draft", 1, domain.Change{Idea: strPtr(strings.Repeat("т", domain.MaxIdeaRunes+1))}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a long idea: %v", err)
	}
	// specs/2026-11-02-writer-workshop.md, W2.3: the marks of what the author did not write are closed and not nested.
	if _, err := create.Execute(context.Background(), webWrite, CreateInput{Kind: "sketch", Text: "а ⟦б"}, request); !errors.Is(err, domain.ErrInvalidMarks) {
		t.Fatalf("a draft with broken marks: %v", err)
	}
	if _, err := update.Execute(context.Background(), webWrite, "draft", 1, domain.Change{Text: strPtr("⟦а ⟦б⟧⟧")}); !errors.Is(err, domain.ErrInvalidMarks) {
		t.Fatalf("an edit with nested marks: %v", err)
	}
	title := "  Новая  "
	if _, err := update.Execute(context.Background(), webWrite, "draft", 1, domain.Change{Title: &title}); err != nil || *store.changes[0].Title != "Новая" {
		t.Fatalf("a title: %v, %+v", err, store.changes)
	}
}

func strPtr(value string) *string { return &value }

// A cursor survives its way through the page and a broken one is refused.
func TestListCursor(t *testing.T) {
	at := webNow()
	store := listStub{page: domain.Page{Drafts: []domain.Summary{{ID: "a", CreatedAt: at}, {ID: "b", CreatedAt: at}}, HasMore: true, Total: 5}}
	view, err := List{Store: store}.Execute(context.Background(), webWrite.Actor, "", 0, "")
	if err != nil || view.NextCursor == "" || view.Total != 5 {
		t.Fatalf("view = %+v, %v", view, err)
	}
	cursor, err := decodeCursor(view.NextCursor)
	if err != nil || cursor.ID != "b" || !cursor.CreatedAt.Equal(at) {
		t.Fatalf("cursor = %+v, %v", cursor, err)
	}
	if _, err := (List{Store: store}).Execute(context.Background(), webWrite.Actor, "%%%", 0, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a broken cursor: %v", err)
	}
	if _, err := (List{Store: store}).Execute(context.Background(), webWrite.Actor, "", maxPageSize+1, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a long page: %v", err)
	}
}

type listStub struct{ page domain.Page }

func (stub listStub) ListChapterDrafts(context.Context, Actor, *domain.Cursor, int, string) (domain.Page, error) {
	return stub.page, nil
}
