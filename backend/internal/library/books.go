package library

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/projectaccess"
	"canonkeeper/backend/internal/projects"
)

var coverColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

type StringPatch struct {
	Present bool
	Value   *string
}

type UpdateBookInput struct {
	Title       StringPatch
	Subtitle    StringPatch
	CoverColor  StringPatch
	AfterBookID OrderPlacement
}

func (s *Service) CreateBook(ctx context.Context, actor Actor, projectID string, input BookInput) (Book, error) {
	if err := s.requireMutation(ctx, actor, projectID); err != nil {
		return Book{}, err
	}
	title, err := validateRequiredTitle(input.Title, maxBookTitleLength)
	if err != nil {
		return Book{}, err
	}
	subtitle, err := validateOptionalText(input.Subtitle, maxBookSubtitleLength)
	if err != nil {
		return Book{}, err
	}
	coverColor, err := validateCoverColor(input.CoverColor)
	if err != nil {
		return Book{}, err
	}
	placement, anchorBook, err := s.validateBookPlacement(ctx, projectID, "", input.AfterBookID)
	if err != nil {
		return Book{}, err
	}
	order := 1
	if !placement.Present {
		order, err = s.store.NextBookOrder(ctx, projectID)
		if err != nil {
			return Book{}, err
		}
	} else if anchorBook != nil {
		order = anchorBook.Order + 1
	}
	if order < 1 {
		return Book{}, ErrValidation
	}
	displayNumber := strings.TrimSpace(input.DisplayNumber)
	if displayNumber == "" {
		displayNumber = strconv.Itoa(order)
	}
	now := s.clock.Now()
	return s.store.CreateBook(ctx, CreateBookStoreInput{
		ID:            s.ids.NewBookID(),
		ProjectID:     projectID,
		Title:         title,
		Subtitle:      subtitle,
		CoverColor:    coverColor,
		Order:         order,
		DisplayNumber: displayNumber,
		CreatedAt:     now,
		AfterBookID:   placement,
	})
}

// ListBooks is one page of the books of a saga: active in shelf order, deleted newest first; the next
// page goes after its last book (specs/2026-11-07-chapter-list-paging.md, P1.3).
func (s *Service) ListBooks(ctx context.Context, actor Actor, projectID string, page Page) (BookPage, error) {
	state := page.State
	if state == "" {
		state = SourceListActive
	}
	switch state {
	case SourceListActive:
		if err := s.requireRead(ctx, actor, projectID); err != nil {
			return BookPage{}, err
		}
	case SourceListDeleted:
		if err := s.requireMutation(ctx, actor, projectID); err != nil {
			return BookPage{}, err
		}
	default:
		return BookPage{}, ErrValidation
	}
	scope := listScope{kind: booksListKind, state: state, parentID: projectID, actorUserID: actor.UserID}
	limit, after, err := listPage(page, scope)
	if err != nil {
		return BookPage{}, err
	}
	books, err := s.store.ListBooks(ctx, ListBooksStoreInput{
		ActorUserID: actor.UserID,
		ProjectID:   projectID,
		State:       state,
		Limit:       limit + 1,
		After:       after,
	})
	if err != nil {
		return BookPage{}, err
	}
	books, meta, err := pageOf(books, limit, scope, bookPosition)
	if err != nil {
		return BookPage{}, err
	}
	return BookPage{Data: books, Meta: meta}, nil
}

func (s *Service) GetBook(ctx context.Context, actor Actor, bookID string) (Book, error) {
	access, identity, err := s.resolveActiveBook(ctx, actor, bookID)
	if err != nil {
		return Book{}, err
	}
	if !projects.CanReadProject(access.ProjectRole) {
		return Book{}, ErrForbidden
	}
	return s.store.GetBook(ctx, access.ProjectID, identity.BookID)
}

func (s *Service) UpdateBook(ctx context.Context, actor Actor, bookID string, input UpdateBookInput) (Book, error) {
	access, identity, err := s.resolveActiveBook(ctx, actor, bookID)
	if err != nil {
		return Book{}, err
	}
	if !projects.CanMutateProject(access.ProjectRole) {
		return Book{}, ErrForbidden
	}
	book, err := s.store.GetBook(ctx, access.ProjectID, identity.BookID)
	if err != nil {
		return Book{}, err
	}
	title := book.Title
	if input.Title.Present {
		if input.Title.Value == nil {
			return Book{}, ErrValidation
		}
		title, err = validateRequiredTitle(*input.Title.Value, maxBookTitleLength)
		if err != nil {
			return Book{}, err
		}
	}
	subtitle := book.Subtitle
	if input.Subtitle.Present {
		subtitle, err = validateOptionalText(input.Subtitle.Value, maxBookSubtitleLength)
		if err != nil {
			return Book{}, err
		}
	}
	coverColor := book.CoverColor
	if input.CoverColor.Present {
		coverColor, err = validateCoverColor(input.CoverColor.Value)
		if err != nil {
			return Book{}, err
		}
	}
	placement, _, err := s.validateBookPlacement(ctx, access.ProjectID, bookID, input.AfterBookID)
	if err != nil {
		return Book{}, err
	}
	updated, err := s.store.UpdateBook(ctx, UpdateBookStoreInput{
		ProjectID:   access.ProjectID,
		BookID:      bookID,
		Title:       title,
		Subtitle:    subtitle,
		CoverColor:  coverColor,
		UpdatedAt:   s.clock.Now(),
		AfterBookID: placement,
	})
	if err == nil && placement.Present && s.reordered != nil {
		s.reordered(context.WithoutCancel(ctx), access.ProjectID)
	}
	return updated, err
}

func (s *Service) DeleteBook(ctx context.Context, actor Actor, bookID string) error {
	access, err := s.store.ResolveBookAccessForActor(ctx, actor.UserID, bookID, projectaccess.VisibilityActive)
	if err != nil {
		return err
	}
	identity, ok := access.Identity.(projectaccess.BookIdentity)
	if !ok || identity.BookID != bookID || access.ProjectID == "" || access.Visibility != projectaccess.VisibilityActive {
		return ErrNotFound
	}
	if !projects.CanMutateProject(access.ProjectRole) {
		return ErrForbidden
	}
	_, err = s.store.FenceBookDeletion(ctx, FenceBookDeletionInput{
		ActorUserID: actor.UserID,
		ProjectID:   access.ProjectID,
		BookID:      bookID,
		TombstoneID: s.ids.NewDeletionTombstoneID(),
		DeletedAt:   s.clock.Now().UTC().Truncate(time.Microsecond),
	})
	return err
}

func (s *Service) RestoreBook(ctx context.Context, actor Actor, bookID string) (Book, error) {
	access, err := s.store.ResolveBookAccessForActor(ctx, actor.UserID, bookID, projectaccess.VisibilityDeletedSource)
	if err != nil {
		return Book{}, err
	}
	identity, ok := access.Identity.(projectaccess.BookIdentity)
	if !ok || identity.BookID != bookID || access.ProjectID == "" || access.Visibility != projectaccess.VisibilityDeletedSource {
		return Book{}, ErrNotFound
	}
	if !projects.CanRestoreBookChapter(access.ProjectRole) {
		return Book{}, ErrForbidden
	}
	lifecycle, err := s.store.LoadDeletedBookLifecycle(ctx, access.ProjectID, bookID)
	if err != nil {
		return Book{}, err
	}
	switch lifecycle {
	case projectaccess.DeletedSourceRestorable:
	case projectaccess.DeletedSourceExpired, projectaccess.DeletedSourceTerminal, projectaccess.DeletedSourceParentDeleted:
		return Book{}, ErrConflict
	default:
		return Book{}, errors.New("library: invalid deleted book lifecycle")
	}
	return s.store.RestoreBook(ctx, RestoreBookStoreInput{
		ActorUserID: actor.UserID,
		ProjectID:   access.ProjectID,
		BookID:      bookID,
	})
}

func (s *Service) validateBookPlacement(ctx context.Context, projectID, movingBookID string, placement OrderPlacement) (OrderPlacement, *Book, error) {
	placement, err := normalizeOrderPlacement(placement)
	if err != nil || !placement.Present || placement.AfterID == nil {
		return placement, nil, err
	}
	if movingBookID != "" && *placement.AfterID == movingBookID {
		return OrderPlacement{}, nil, ErrValidation
	}
	anchor, err := s.store.GetBook(ctx, projectID, *placement.AfterID)
	if err != nil {
		return OrderPlacement{}, nil, err
	}
	if anchor.ProjectID != projectID {
		return OrderPlacement{}, nil, ErrNotFound
	}
	return placement, &anchor, nil
}

func (s *Service) resolveActiveBook(
	ctx context.Context,
	actor Actor,
	bookID string,
) (projectaccess.ResolvedResourceAccess, projectaccess.BookIdentity, error) {
	access, err := s.store.ResolveBookAccessForActor(ctx, actor.UserID, bookID, projectaccess.VisibilityActive)
	if err != nil {
		return projectaccess.ResolvedResourceAccess{}, projectaccess.BookIdentity{}, err
	}
	identity, ok := access.Identity.(projectaccess.BookIdentity)
	if !ok || identity.BookID != bookID || access.ProjectID == "" || access.Visibility != projectaccess.VisibilityActive {
		return projectaccess.ResolvedResourceAccess{}, projectaccess.BookIdentity{}, ErrNotFound
	}
	return access, identity, nil
}

func validateOptionalText(value *string, maxLength int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	if utf8.RuneCountInString(*value) > maxLength {
		return nil, ErrValidation
	}
	copied := *value
	return &copied, nil
}

func validateCoverColor(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	if !coverColorPattern.MatchString(*value) {
		return nil, ErrValidation
	}
	copied := *value
	return &copied, nil
}
