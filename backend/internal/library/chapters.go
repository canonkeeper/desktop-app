package library

import (
	"context"
	"errors"
	"sort"
	"time"

	"canonkeeper/backend/internal/projectaccess"
	"canonkeeper/backend/internal/projects"
)

type ChapterContent struct {
	Chapter
	Paragraphs      []Paragraph
	WordCount       int
	ContentVariant  ParagraphVariant
	ContentRevision int64
	Navigation      ChapterNavigation
}

// ListChapters is one page of the chapters of a book: active in reading order, deleted newest first;
// the next page goes after its last chapter (specs/2026-11-07-chapter-list-paging.md, P1).
func (s *Service) ListChapters(ctx context.Context, actor Actor, bookID string, page Page) (ChapterPage, error) {
	access, err := s.store.ResolveBookAccessForActor(ctx, actor.UserID, bookID, projectaccess.VisibilityActive)
	if err != nil {
		return ChapterPage{}, err
	}
	identity, ok := access.Identity.(projectaccess.BookIdentity)
	if !ok || identity.BookID != bookID || access.ProjectID == "" || access.Visibility != projectaccess.VisibilityActive {
		return ChapterPage{}, ErrNotFound
	}
	state := page.State
	if state == "" {
		state = SourceListActive
	}
	switch state {
	case SourceListActive:
		if !projects.CanReadProject(access.ProjectRole) {
			return ChapterPage{}, ErrForbidden
		}
	case SourceListDeleted:
		if !projects.CanMutateProject(access.ProjectRole) {
			return ChapterPage{}, ErrForbidden
		}
	default:
		return ChapterPage{}, ErrValidation
	}
	scope := listScope{kind: chaptersListKind, state: state, parentID: bookID, actorUserID: actor.UserID}
	limit, after, err := listPage(page, scope)
	if err != nil {
		return ChapterPage{}, err
	}
	chapters, err := s.store.ListChapters(ctx, ListChaptersStoreInput{
		ActorUserID: actor.UserID,
		ProjectID:   access.ProjectID,
		BookID:      bookID,
		State:       state,
		Limit:       limit + 1,
		After:       after,
	})
	if err != nil {
		return ChapterPage{}, err
	}
	chapters, meta, err := pageOf(chapters, limit, scope, chapterPosition)
	if err != nil {
		return ChapterPage{}, err
	}
	summaries := make([]ChapterSummary, 0, len(chapters))
	for _, chapter := range chapters {
		if state == SourceListDeleted {
			summaries = append(summaries, ChapterSummary{Chapter: chapter, HasDraft: chapterHasDraft(chapter)})
			continue
		}
		paragraphs, err := s.store.ListChapterParagraphs(ctx, ListChapterParagraphsStoreInput{
			ProjectID: access.ProjectID,
			BookID:    bookID,
			ChapterID: chapter.ID,
			Revision:  chapter.Revision,
			Variant:   ParagraphVariantDraft,
		})
		if err != nil {
			return ChapterPage{}, err
		}
		content := chapterContent(ChapterRecord{Chapter: chapter}, paragraphs, ParagraphVariantDraft, chapter.Revision)
		summaries = append(summaries, ChapterSummary{
			Chapter:   content.Chapter,
			WordCount: content.WordCount,
			HasDraft:  chapterHasDraft(chapter),
		})
	}
	return ChapterPage{Data: summaries, Meta: meta}, nil
}

func (s *Service) GetChapter(ctx context.Context, actor Actor, chapterID string, variant ParagraphVariant) (ChapterContent, error) {
	access, identity, err := s.resolveActiveChapter(ctx, actor, chapterID)
	if err != nil {
		return ChapterContent{}, err
	}
	if !projects.CanReadProject(access.ProjectRole) {
		return ChapterContent{}, ErrForbidden
	}
	if variant != ParagraphVariantPublished && !projects.CanMutateProject(access.ProjectRole) {
		return ChapterContent{}, ErrNotFound
	}
	record, err := s.store.GetChapter(ctx, access.ProjectID, identity.BookID, identity.ChapterID)
	if err != nil {
		return ChapterContent{}, err
	}
	revision, selectedVariant, err := selectedChapterVariant(record.Chapter, variant)
	if err != nil {
		return ChapterContent{}, err
	}
	paragraphs, err := s.store.ListChapterParagraphs(ctx, ListChapterParagraphsStoreInput{
		ProjectID: access.ProjectID,
		BookID:    identity.BookID,
		ChapterID: chapterID,
		Revision:  revision,
		Variant:   selectedVariant,
	})
	if err != nil {
		return ChapterContent{}, err
	}
	return chapterContent(record, paragraphs, selectedVariant, revision), nil
}

func (s *Service) DeleteChapter(ctx context.Context, actor Actor, chapterID string) error {
	access, err := s.store.ResolveChapterAccessForActor(ctx, actor.UserID, chapterID, projectaccess.VisibilityActive)
	if err != nil {
		return err
	}
	identity, ok := access.Identity.(projectaccess.ChapterIdentity)
	if !ok || identity.ChapterID != chapterID || identity.BookID == "" || access.ProjectID == "" || access.Visibility != projectaccess.VisibilityActive {
		return ErrNotFound
	}
	if !projects.CanMutateProject(access.ProjectRole) {
		return ErrForbidden
	}
	_, err = s.store.FenceChapterDeletion(ctx, FenceChapterDeletionInput{
		ActorUserID: actor.UserID,
		ProjectID:   access.ProjectID,
		BookID:      identity.BookID,
		ChapterID:   chapterID,
		TombstoneID: s.ids.NewDeletionTombstoneID(),
		DeletedAt:   s.clock.Now().UTC().Truncate(time.Microsecond),
	})
	return err
}

func (s *Service) RestoreChapter(ctx context.Context, actor Actor, chapterID string) (Chapter, error) {
	access, err := s.store.ResolveChapterAccessForActor(ctx, actor.UserID, chapterID, projectaccess.VisibilityDeletedSource)
	if err != nil {
		return Chapter{}, err
	}
	identity, ok := access.Identity.(projectaccess.ChapterIdentity)
	if !ok || identity.ChapterID != chapterID || identity.BookID == "" || access.ProjectID == "" || access.Visibility != projectaccess.VisibilityDeletedSource {
		return Chapter{}, ErrNotFound
	}
	if !projects.CanRestoreBookChapter(access.ProjectRole) {
		return Chapter{}, ErrForbidden
	}
	lifecycle, err := s.store.LoadDeletedChapterLifecycle(ctx, access.ProjectID, identity.BookID, chapterID)
	if err != nil {
		return Chapter{}, err
	}
	switch lifecycle {
	case projectaccess.DeletedSourceRestorable:
	case projectaccess.DeletedSourceExpired, projectaccess.DeletedSourceTerminal, projectaccess.DeletedSourceParentDeleted:
		return Chapter{}, ErrConflict
	default:
		return Chapter{}, errors.New("library: invalid deleted chapter lifecycle")
	}
	return s.store.RestoreChapter(ctx, RestoreChapterStoreInput{
		ActorUserID: actor.UserID,
		ProjectID:   access.ProjectID,
		BookID:      identity.BookID,
		ChapterID:   chapterID,
	})
}

func (s *Service) resolveActiveChapter(
	ctx context.Context,
	actor Actor,
	chapterID string,
) (projectaccess.ResolvedResourceAccess, projectaccess.ChapterIdentity, error) {
	access, err := s.store.ResolveChapterAccessForActor(ctx, actor.UserID, chapterID, projectaccess.VisibilityActive)
	if err != nil {
		return projectaccess.ResolvedResourceAccess{}, projectaccess.ChapterIdentity{}, err
	}
	identity, ok := access.Identity.(projectaccess.ChapterIdentity)
	if !ok || identity.BookID == "" || identity.ChapterID != chapterID || access.ProjectID == "" || access.Visibility != projectaccess.VisibilityActive {
		return projectaccess.ResolvedResourceAccess{}, projectaccess.ChapterIdentity{}, ErrNotFound
	}
	return access, identity, nil
}

func chapterHasDraft(chapter Chapter) bool {
	publishedRevision := int64(0)
	if chapter.PublishedRevision != nil {
		publishedRevision = *chapter.PublishedRevision
	}
	return chapter.Revision > publishedRevision
}

func selectedChapterVariant(chapter Chapter, variant ParagraphVariant) (int64, ParagraphVariant, error) {
	switch variant {
	case "", ParagraphVariantDraft:
		return chapter.Revision, ParagraphVariantDraft, nil
	case ParagraphVariantPublished:
		if chapter.PublishedRevision == nil {
			return 0, "", ErrNotFound
		}
		return *chapter.PublishedRevision, ParagraphVariantPublished, nil
	default:
		return 0, "", ErrValidation
	}
}

func chapterContent(record ChapterRecord, paragraphs []Paragraph, variant ParagraphVariant, revision int64) ChapterContent {
	chapter := record.Chapter
	sort.Slice(paragraphs, func(i, j int) bool {
		if paragraphs[i].Order == paragraphs[j].Order {
			return paragraphs[i].ID < paragraphs[j].ID
		}
		return paragraphs[i].Order < paragraphs[j].Order
	})
	wordCount := 0
	for _, paragraph := range paragraphs {
		if paragraph.WordCount > 0 {
			wordCount += paragraph.WordCount
		}
	}
	return ChapterContent{
		Chapter:         chapter,
		Paragraphs:      paragraphs,
		WordCount:       wordCount,
		ContentVariant:  variant,
		ContentRevision: revision,
		Navigation:      record.Navigation,
	}
}
