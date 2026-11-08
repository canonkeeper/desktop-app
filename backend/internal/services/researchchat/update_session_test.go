package researchchat

import (
	"context"
	"errors"
	"strings"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type updateSessionStoreForTest struct {
	input UpdateSessionInput
	calls int
}

func (store *updateSessionStoreForTest) UpdateSessionForActor(_ context.Context, input UpdateSessionInput) (researchdomain.Session, error) {
	store.input = input
	store.calls++
	session := researchdomain.Session{ID: input.ChatID}
	if input.Title != nil {
		session.Title = *input.Title
	}
	return session, nil
}

func TestUpdateSessionTrimsAndCountsUnicodeRunes(t *testing.T) {
	store := &updateSessionStoreForTest{}
	useCase := NewUpdateSession(store)
	title := "  Новое название  "
	renamed, err := useCase.Execute(context.Background(), UpdateSessionInput{
		Actor: researchdomain.Actor{UserID: "actor-1"}, ChatID: "chat-1", Title: &title,
	})
	if err != nil || renamed.Title != "Новое название" || *store.input.Title != "Новое название" {
		t.Fatalf("Execute() = %#v, %v; input=%#v", renamed, err, store.input)
	}

	for name, test := range map[string]struct {
		title string
		want  error
	}{
		"empty":    {title: " \n\t ", want: researchdomain.ErrTitleEmpty},
		"too long": {title: strings.Repeat("я", 121), want: researchdomain.ErrTitleTooLong},
	} {
		t.Run(name, func(t *testing.T) {
			before := store.calls
			_, err := useCase.Execute(context.Background(), UpdateSessionInput{
				Actor: researchdomain.Actor{UserID: "actor-1"}, ChatID: "chat-1", Title: &test.title,
			})
			if !errors.Is(err, test.want) || store.calls != before {
				t.Fatalf("Execute() error/calls = %v/%d, want %v/%d", err, store.calls, test.want, before)
			}
		})
	}
}

// A change without a title changes nothing and is refused: the toggle «Рисунки» is gone with the drawings of the
// chat (specs/2026-11-02-image-workshop.md IW7.1).
func TestUpdateSessionRefusesAChangeWithoutATitle(t *testing.T) {
	store := &updateSessionStoreForTest{}
	useCase := NewUpdateSession(store)
	if _, err := useCase.Execute(context.Background(), UpdateSessionInput{
		Actor: researchdomain.Actor{UserID: "actor-1"}, ChatID: "chat-1",
	}); !errors.Is(err, researchdomain.ErrValidation) || store.calls != 0 {
		t.Fatalf("an empty change = %v, calls %d", err, store.calls)
	}
}
