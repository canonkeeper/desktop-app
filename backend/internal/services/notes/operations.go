package notes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/notes"
)

type Write struct {
	UserID string
	Proof  auth.SessionWriteProof
}
type CreateInput struct {
	Title  string         `json:"title,omitempty"`
	Body   string         `json:"body"`
	Source *domain.Source `json:"source,omitempty"`
}
type CreateCommand struct {
	Write
	CreateInput
	ScopeHash   string
	KeyHash     string
	RequestHash string
}
type CreateRepository interface {
	Create(context.Context, CreateCommand) (domain.Note, error)
}
type Create struct{ Repository CreateRepository }

func (useCase Create) Execute(ctx context.Context, actor Write, input CreateInput, key, method, path string) (domain.Note, error) {
	input.Title = strings.TrimSpace(input.Title)
	if !domain.ValidID(actor.UserID) || !actor.Proof.Valid() || domain.ValidateThought(input.Body, input.Source != nil) != nil ||
		domain.ValidateTitle(input.Title) != nil ||
		utf8.RuneCountInString(key) < 8 || utf8.RuneCountInString(key) > 128 || !utf8.ValidString(key) ||
		(input.Source != nil && input.Source.Validate() != nil) {
		return domain.Note{}, domain.ErrValidation
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return domain.Note{}, err
	}
	return useCase.Repository.Create(ctx, CreateCommand{Write: actor, CreateInput: input,
		ScopeHash: digest(strings.Join([]string{actor.UserID, method, path}, "\x00")),
		KeyHash:   digest(key), RequestHash: digest(string(raw))})
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type GetRepository interface {
	Get(context.Context, string, string) (domain.Note, error)
}
type Get struct{ Repository GetRepository }

func (useCase Get) Execute(ctx context.Context, userID, noteID string) (domain.Note, error) {
	if !domain.ValidID(userID) || !domain.ValidID(noteID) {
		return domain.Note{}, domain.ErrNotFound
	}
	return useCase.Repository.Get(ctx, userID, noteID)
}

type UpdateInput struct {
	Body             *string `json:"body,omitempty"`
	Title            *string `json:"title,omitempty"`
	ExpectedRevision int64   `json:"expectedRevision"`
}
type UpdateRepository interface {
	Update(context.Context, Write, string, UpdateInput) (domain.Note, error)
}
type Update struct{ Repository UpdateRepository }

func (useCase Update) Execute(ctx context.Context, actor Write, noteID string, input UpdateInput) (domain.Note, error) {
	if !domain.ValidID(actor.UserID) || !domain.ValidID(noteID) {
		return domain.Note{}, domain.ErrNotFound
	}
	// A blank thought is refused by the repository when the note keeps no quote: only the stored note knows (NT1.2).
	if !actor.Proof.Valid() || input.ExpectedRevision < 1 || (input.Body == nil && input.Title == nil) ||
		(input.Body != nil && domain.ValidateThought(*input.Body, true) != nil) {
		return domain.Note{}, domain.ErrValidation
	}
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if domain.ValidateTitle(title) != nil {
			return domain.Note{}, domain.ErrValidation
		}
		input.Title = &title
	}
	return useCase.Repository.Update(ctx, actor, noteID, input)
}

type DeleteRepository interface {
	Delete(context.Context, Write, string, int64) error
}
type Delete struct{ Repository DeleteRepository }

func (useCase Delete) Execute(ctx context.Context, actor Write, noteID string, revision int64) error {
	if !domain.ValidID(actor.UserID) || !domain.ValidID(noteID) {
		return domain.ErrNotFound
	}
	if !actor.Proof.Valid() || revision < 1 {
		return domain.ErrValidation
	}
	return useCase.Repository.Delete(ctx, actor, noteID, revision)
}
