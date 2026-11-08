package notes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	domain "canonkeeper/backend/internal/domain/notes"
	service "canonkeeper/backend/internal/services/notes"
	"canonkeeper/backend/internal/testutil"
)

func TestTitlesPartialUpdatesReplayAndFilteredSummaries(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_notes_query")
	actors := seedAccounts(t, db)
	repo := New(db)
	ctx := context.Background()
	create := service.Create{Repository: repo}
	update := service.Update{Repository: repo}
	list := service.List{Repository: repo}
	body := "Начало %_ " + strings.Repeat("Ж", 300) + " НЕВИДИМЫЙ ХВОСТ"
	input := service.CreateInput{Title: "  РУНА  ", Body: body}
	note, err := create.Execute(ctx, actors[0], input, "title-replay-key", "POST", "/personal-notes")
	if err != nil || note.Title != "РУНА" {
		t.Fatalf("title create=%#v,%v", note, err)
	}
	input.Title = "РУНА"
	replay, err := create.Execute(ctx, actors[0], input, "title-replay-key", "POST", "/personal-notes")
	if err != nil || replay.ID != note.ID {
		t.Fatalf("normalized replay=%#v,%v", replay, err)
	}
	input.Title = "Different"
	if _, err = create.Execute(ctx, actors[0], input, "title-replay-key", "POST", "/personal-notes"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("title changed replay=%v", err)
	}
	other, err := create.Execute(ctx, actors[0], service.CreateInput{Body: "Начало ABC"}, "other-title-key", "POST", "/personal-notes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = create.Execute(ctx, actors[1], service.CreateInput{Title: "РУНА", Body: "Other account"}, "foreign-title-key", "POST", "/personal-notes"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"руна", "%_", "невидимый хвост"} {
		page, err := list.Execute(ctx, actors[0].UserID, service.ListInput{Query: query, Limit: 1})
		if err != nil || page.Meta.TotalCount != 1 || len(page.Data) != 1 || page.Data[0].ID != note.ID || page.Meta.HasMore {
			t.Fatalf("filtered summary query=%q result=%#v err=%v", query, page, err)
		}
		entry := page.Data[0]
		if entry.Title != "РУНА" || entry.SourceKind != "free" || utf8.RuneCountInString(entry.Preview) != 240 {
			t.Fatalf("summary=%#v", entry)
		}
		raw, _ := json.Marshal(entry)
		for _, forbidden := range []string{body, "НЕВИДИМЫЙ ХВОСТ", `"body":`, `"locator":`, `"source":`, `"kind":`, `"type":`, `"canEdit":`} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("summary contains full detail %q", forbidden)
			}
		}
	}
	first, err := list.Execute(ctx, actors[0].UserID, service.ListInput{Limit: 1})
	if err != nil || first.Meta.TotalCount != 2 || first.Meta.NextCursor == nil {
		t.Fatalf("first=%#v,%v", first, err)
	}
	next, err := list.Execute(ctx, actors[0].UserID, service.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	if err != nil || next.Meta.TotalCount != 2 || len(next.Data) != 1 || next.Data[0].ID == first.Data[0].ID || next.Meta.HasMore {
		t.Fatalf("next=%#v,%v", next, err)
	}
	for _, filters := range []service.ListInput{{Query: "changed"}, {Source: "chat"}, {ProjectID: testID(90)}} {
		filters.Cursor = *first.Meta.NextCursor
		if _, err = list.Execute(ctx, actors[0].UserID, filters); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("cursor filter rebinding=%v", err)
		}
	}
	if _, err = list.Execute(ctx, actors[1].UserID, service.ListInput{Cursor: *first.Meta.NextCursor}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("cursor account rebinding=%v", err)
	}
	for _, filters := range []service.ListInput{{Query: "missing"}, {Source: "chapter"}, {ProjectID: testID(90), BookID: testID(91)}} {
		page, err := list.Execute(ctx, actors[0].UserID, filters)
		if err != nil || page.Meta.TotalCount != 0 || len(page.Data) != 0 || page.Meta.HasMore || page.Meta.NextCursor != nil {
			t.Fatalf("empty filtered count=%#v,%v", page, err)
		}
	}
	changed, err := update.Execute(ctx, actors[0], note.ID, service.UpdateInput{Title: testText("Новое"), ExpectedRevision: 1})
	if err != nil || changed.Body != body || changed.Title != "Новое" || changed.Revision != 2 {
		t.Fatalf("title-only=%#v,%v", changed, err)
	}
	changed, err = update.Execute(ctx, actors[0], note.ID, service.UpdateInput{Body: testText("Changed body"), ExpectedRevision: 2})
	if err != nil || changed.Title != "Новое" || changed.Revision != 3 {
		t.Fatalf("omitted title=%#v,%v", changed, err)
	}
	changed, err = update.Execute(ctx, actors[0], note.ID, service.UpdateInput{Title: testText("  "), ExpectedRevision: 3})
	if err != nil || changed.Title != "" || changed.Body != "Changed body" {
		t.Fatalf("clear title=%#v,%v", changed, err)
	}
	if _, err = repo.Get(ctx, actors[1].UserID, other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("detail ACL=%v", err)
	}
}
