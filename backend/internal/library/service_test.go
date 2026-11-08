package library

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projectaccess"
	"canonkeeper/backend/internal/projects"
)

func TestDeleteBookUsesExactActorResolverAndAtomicFence(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.SeedBook("project_1", "book_1")
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	actor := ActorEditor("project_1")

	if err := service.DeleteBook(context.Background(), actor, "book_1"); err != nil {
		t.Fatalf("DeleteBook() error = %v", err)
	}
	if store.getBookCalls != 0 || store.resolveBookCalls != 1 {
		t.Fatalf("DeleteBook lookups = GetBook %d resolver %d", store.getBookCalls, store.resolveBookCalls)
	}
	want := FenceBookDeletionInput{
		ActorUserID: actor.UserID,
		ProjectID:   "project_1",
		BookID:      "book_1",
		TombstoneID: "tombstone_generated_1",
		DeletedAt:   staticTestTime(),
	}
	if store.lastFenceBookInput == nil || *store.lastFenceBookInput != want {
		t.Fatalf("FenceBookDeletion input = %#v, want %#v", store.lastFenceBookInput, want)
	}
}

func TestDeleteBookConcealsMissingMembershipAndForbidsViewer(t *testing.T) {
	tests := []struct {
		name    string
		actor   Actor
		arrange func(*memoryStoreForTest)
		wantErr error
	}{
		{
			name:  "viewer",
			actor: Actor{UserID: "viewer"},
			arrange: func(store *memoryStoreForTest) {
				store.projectRoles[roleKey("viewer", "project_1")] = projects.RoleViewer
			},
			wantErr: ErrForbidden,
		},
		{
			name:    "non member",
			actor:   Actor{UserID: "stranger"},
			arrange: func(*memoryStoreForTest) {},
			wantErr: ErrNotFound,
		},
		{
			name:  "member of another project",
			actor: Actor{UserID: "cross"},
			arrange: func(store *memoryStoreForTest) {
				store.projectRoles[roleKey("cross", "project_2")] = projects.RoleOwner
			},
			wantErr: ErrNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			store.SeedBook("project_1", "book_1")
			tt.arrange(store)
			service := NewService(store, StaticIDsForTest(), StaticClockForTest())
			err := service.DeleteBook(context.Background(), tt.actor, "book_1")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("DeleteBook() error = %v, want %v", err, tt.wantErr)
			}
			if store.lastFenceBookInput != nil {
				t.Fatalf("FenceBookDeletion called for rejected actor: %#v", store.lastFenceBookInput)
			}
		})
	}
}

func TestListBooksDeletedStateUsesActorScopedMutationBoundary(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.SeedBook("project_1", "book_active")
	store.SeedBook("project_1", "book_deleted")
	deletedAt := staticTestTime()
	store.deletedBooks["book_deleted"] = struct{}{}
	store.deletedBookAt["book_deleted"] = deletedAt
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	actor := ActorEditor("project_1")

	page, err := service.ListBooks(context.Background(), actor, "project_1", Page{State: SourceListDeleted})
	if err != nil {
		t.Fatalf("ListBooks(deleted) error = %v", err)
	}
	books := page.Data
	if len(books) != 1 || books[0].ID != "book_deleted" || books[0].DeletedAt == nil ||
		!books[0].DeletedAt.Equal(deletedAt) || books[0].RestoreUntil == nil ||
		!books[0].RestoreUntil.Equal(deletedAt.Add(7*24*time.Hour)) {
		t.Fatalf("ListBooks(deleted) = %#v", books)
	}
	if store.lastListBooksInput.ActorUserID != actor.UserID ||
		store.lastListBooksInput.ProjectID != "project_1" ||
		store.lastListBooksInput.State != SourceListDeleted {
		t.Fatalf("ListBooks store input = %#v", store.lastListBooksInput)
	}
	if _, err := service.ListBooks(context.Background(), actor, "project_1", Page{State: "archived"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListBooks(unknown state) error = %v, want ErrValidation", err)
	}
}

func TestRestoreBookUsesRegisteredResolverBeforeDeletedLifecycle(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.SeedBook("project_1", "book_1")
	store.deletedBooks["book_1"] = struct{}{}
	store.deletedBookAt["book_1"] = staticTestTime()
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	actor := ActorEditor("project_1")

	restored, err := service.RestoreBook(context.Background(), actor, "book_1")
	if err != nil {
		t.Fatalf("RestoreBook() error = %v", err)
	}
	if restored.ID != "book_1" || restored.DeletedAt != nil || restored.RestoreUntil != nil {
		t.Fatalf("RestoreBook() = %#v", restored)
	}
	want := RestoreBookStoreInput{
		ActorUserID: actor.UserID,
		ProjectID:   "project_1",
		BookID:      "book_1",
	}
	if store.lastRestoreBookInput == nil || *store.lastRestoreBookInput != want {
		t.Fatalf("RestoreBook input = %#v, want %#v", store.lastRestoreBookInput, want)
	}
	if store.resolveBookCalls != 1 || store.resolveDeletedBookCalls != 1 {
		t.Fatalf("RestoreBook resolvers = registered %d lifecycle %d, want 1/1", store.resolveBookCalls, store.resolveDeletedBookCalls)
	}
}

func TestRestoreBookChecksRoleBeforeDeletedLifecycle(t *testing.T) {
	tests := []struct {
		name      string
		role      projects.Role
		lifecycle projectaccess.DeletedSourceLifecycle
		wantErr   error
	}{
		{name: "inactive membership concealed", lifecycle: projectaccess.DeletedSourceExpired, wantErr: ErrNotFound},
		{name: "viewer forbidden before expiry", role: projects.RoleViewer, lifecycle: projectaccess.DeletedSourceExpired, wantErr: ErrForbidden},
		{name: "editor sees expired conflict", role: projects.RoleEditor, lifecycle: projectaccess.DeletedSourceExpired, wantErr: ErrConflict},
		{name: "editor sees terminal conflict", role: projects.RoleEditor, lifecycle: projectaccess.DeletedSourceTerminal, wantErr: ErrConflict},
		{name: "editor sees parent deleted conflict", role: projects.RoleEditor, lifecycle: projectaccess.DeletedSourceParentDeleted, wantErr: ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			store.SeedBook("project_1", "book_1")
			store.deletedBooks["book_1"] = struct{}{}
			store.deletedBookLifecycle["book_1"] = tt.lifecycle
			actor := Actor{UserID: "lifecycle_actor"}
			if tt.role != "" {
				store.projectRoles[roleKey(actor.UserID, "project_1")] = tt.role
			}
			service := NewService(store, StaticIDsForTest(), StaticClockForTest())

			_, err := service.RestoreBook(t.Context(), actor, "book_1")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RestoreBook() error = %v, want %v", err, tt.wantErr)
			}
			if store.lastRestoreBookInput != nil {
				t.Fatalf("RestoreBook() mutated store after denial: %#v", store.lastRestoreBookInput)
			}
		})
	}
}

func TestDeleteChapterUsesExactActorResolverAndAtomicFence(t *testing.T) {
	store := NewMemoryStoreForTest()
	chapterID := store.SeedChapter("project_1", "book_1", nil)
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	actor := ActorEditor("project_1")
	if err := service.DeleteChapter(context.Background(), actor, chapterID); err != nil {
		t.Fatalf("DeleteChapter() error = %v", err)
	}
	if store.getChapterCalls != 0 || store.resolveChapterCalls != 1 {
		t.Fatalf("DeleteChapter lookups = GetChapter %d resolver %d", store.getChapterCalls, store.resolveChapterCalls)
	}
	want := FenceChapterDeletionInput{
		ActorUserID: actor.UserID,
		ProjectID:   "project_1",
		BookID:      "book_1",
		ChapterID:   chapterID,
		TombstoneID: "tombstone_generated_1",
		DeletedAt:   staticTestTime(),
	}
	if store.lastFenceChapterInput == nil || *store.lastFenceChapterInput != want {
		t.Fatalf("FenceChapterDeletion input = %#v, want %#v", store.lastFenceChapterInput, want)
	}
}

func TestListAndRestoreDeletedChapterUseExactCurrentAncestry(t *testing.T) {
	store := NewMemoryStoreForTest()
	chapterID := store.SeedChapter("project_1", "book_1", nil)
	deletedAt := staticTestTime()
	store.deletedChapters[chapterID] = struct{}{}
	store.deletedChapterAt[chapterID] = deletedAt
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	actor := ActorEditor("project_1")

	page, err := service.ListChapters(context.Background(), actor, "book_1", Page{State: SourceListDeleted})
	if err != nil {
		t.Fatalf("ListChapters(deleted) error = %v", err)
	}
	chapters := page.Data
	if len(chapters) != 1 || chapters[0].ID != chapterID || chapters[0].DeletedAt == nil ||
		!chapters[0].DeletedAt.Equal(deletedAt) || chapters[0].RestoreUntil == nil ||
		!chapters[0].RestoreUntil.Equal(deletedAt.Add(7*24*time.Hour)) {
		t.Fatalf("ListChapters(deleted) = %#v", chapters)
	}
	if store.lastListChaptersInput.ActorUserID != actor.UserID || store.lastListChaptersInput.State != SourceListDeleted {
		t.Fatalf("ListChapters store input = %#v", store.lastListChaptersInput)
	}

	restored, err := service.RestoreChapter(context.Background(), actor, chapterID)
	if err != nil {
		t.Fatalf("RestoreChapter() error = %v", err)
	}
	if restored.ID != chapterID || restored.DeletedAt != nil || restored.RestoreUntil != nil {
		t.Fatalf("RestoreChapter() = %#v", restored)
	}
	want := RestoreChapterStoreInput{
		ActorUserID: actor.UserID,
		ProjectID:   "project_1",
		BookID:      "book_1",
		ChapterID:   chapterID,
	}
	if store.lastRestoreChapterInput == nil || *store.lastRestoreChapterInput != want {
		t.Fatalf("RestoreChapter input = %#v, want %#v", store.lastRestoreChapterInput, want)
	}
	if store.resolveChapterCalls != 1 || store.resolveDeletedChapterCalls != 1 {
		t.Fatalf("RestoreChapter resolvers = registered %d lifecycle %d, want 1/1", store.resolveChapterCalls, store.resolveDeletedChapterCalls)
	}
}

func TestRestoreChapterChecksRoleBeforeDeletedLifecycle(t *testing.T) {
	tests := []struct {
		name      string
		role      projects.Role
		lifecycle projectaccess.DeletedSourceLifecycle
		wantErr   error
	}{
		{name: "inactive membership concealed", lifecycle: projectaccess.DeletedSourceTerminal, wantErr: ErrNotFound},
		{name: "viewer forbidden before parent state", role: projects.RoleViewer, lifecycle: projectaccess.DeletedSourceParentDeleted, wantErr: ErrForbidden},
		{name: "editor sees expired conflict", role: projects.RoleEditor, lifecycle: projectaccess.DeletedSourceExpired, wantErr: ErrConflict},
		{name: "editor sees terminal conflict", role: projects.RoleEditor, lifecycle: projectaccess.DeletedSourceTerminal, wantErr: ErrConflict},
		{name: "editor sees parent deleted conflict", role: projects.RoleEditor, lifecycle: projectaccess.DeletedSourceParentDeleted, wantErr: ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			chapterID := store.SeedChapter("project_1", "book_1", nil)
			store.deletedChapters[chapterID] = struct{}{}
			store.deletedChapterLifecycle[chapterID] = tt.lifecycle
			actor := Actor{UserID: "lifecycle_actor"}
			if tt.role != "" {
				store.projectRoles[roleKey(actor.UserID, "project_1")] = tt.role
			}
			service := NewService(store, StaticIDsForTest(), StaticClockForTest())

			_, err := service.RestoreChapter(t.Context(), actor, chapterID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RestoreChapter() error = %v, want %v", err, tt.wantErr)
			}
			if store.lastRestoreChapterInput != nil {
				t.Fatalf("RestoreChapter() mutated store after denial: %#v", store.lastRestoreChapterInput)
			}
		})
	}
}

func TestListChaptersSummarizesDraftsInBookOrderAndChecksMembership(t *testing.T) {
	store := NewMemoryStoreForTest()
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	firstID := store.SeedChapter("project_1", "book_1", []Paragraph{{ID: "p1", Markdown: "first draft"}})
	secondID := store.SeedChapter("project_1", "book_1", []Paragraph{{ID: "p2", Markdown: "second draft text"}})
	store.SeedPublishedRevision(secondID)
	store.projectRoles[roleKey("viewer", "project_1")] = projects.RoleViewer

	page, err := service.ListChapters(context.Background(), Actor{UserID: "viewer"}, "book_1", Page{Limit: 10})
	if err != nil {
		t.Fatalf("ListChapters() error = %v", err)
	}
	chapters := page.Data
	if len(chapters) != 2 {
		t.Fatalf("chapter count = %d, want 2: %#v", len(chapters), chapters)
	}
	if chapters[0].ID != firstID || chapters[0].Order != 1 || chapters[0].WordCount != 2 || !chapters[0].HasDraft {
		t.Fatalf("first chapter summary = %#v, want draft summary in order", chapters[0])
	}
	if chapters[1].ID != secondID || chapters[1].Order != 2 || chapters[1].WordCount != 3 || chapters[1].HasDraft {
		t.Fatalf("second chapter summary = %#v, want published summary without draft", chapters[1])
	}

	_, err = service.ListChapters(context.Background(), Actor{UserID: "stranger"}, "book_1", Page{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListChapters() non-member error = %v, want ErrNotFound", err)
	}
}

func NewMemoryStoreForTest() *memoryStoreForTest {
	return &memoryStoreForTest{
		books:                   make(map[string]Book),
		chapters:                make(map[string]Chapter),
		paragraphs:              make(map[string][]Paragraph),
		projectRoles:            make(map[string]projects.Role),
		deletedBooks:            make(map[string]struct{}),
		deletedBookAt:           make(map[string]time.Time),
		deletedBookLifecycle:    make(map[string]projectaccess.DeletedSourceLifecycle),
		deletedChapters:         make(map[string]struct{}),
		deletedChapterAt:        make(map[string]time.Time),
		deletedChapterLifecycle: make(map[string]projectaccess.DeletedSourceLifecycle),
		importFiles:             make(map[string]StoreImportFileInput),
		importJobsByKey:         make(map[string]ImportJob),
		importStartJobsByKey:    make(map[string]jobs.Job),
	}
}

type memoryStoreForTest struct {
	books                   map[string]Book
	chapters                map[string]Chapter
	paragraphs              map[string][]Paragraph
	projectRoles            map[string]projects.Role
	deletedBooks            map[string]struct{}
	deletedBookAt           map[string]time.Time
	deletedBookLifecycle    map[string]projectaccess.DeletedSourceLifecycle
	deletedChapters         map[string]struct{}
	deletedChapterAt        map[string]time.Time
	deletedChapterLifecycle map[string]projectaccess.DeletedSourceLifecycle
	importFiles             map[string]StoreImportFileInput
	importJobsByKey         map[string]ImportJob
	importStartJobsByKey    map[string]jobs.Job
	importJobs              []CreateImportJobStoreInput
	enqueuedImports         []EnqueueImportJobInput

	createImportJobErr       error
	createImportJobHook      func(CreateImportJobStoreInput)
	importJobIdempotencyHits int
	enqueueImportJobErr      error
	enqueueImportJobCalls    int

	resolveBookCalls           int
	resolveDeletedBookCalls    int
	getBookCalls               int
	lastFenceBookInput         *FenceBookDeletionInput
	lastRestoreBookInput       *RestoreBookStoreInput
	lastListBooksInput         ListBooksStoreInput
	resolveChapterCalls        int
	resolveDeletedChapterCalls int
	getChapterCalls            int
	lastFenceChapterInput      *FenceChapterDeletionInput
	lastRestoreChapterInput    *RestoreChapterStoreInput
	lastListChaptersInput      ListChaptersStoreInput

	// accessOverride искажает ответ actor-scoped резолвера, чтобы проверить,
	// что сервис сам скрывает несогласованный доступ.
	accessOverride func(projectaccess.ResolvedResourceAccess) projectaccess.ResolvedResourceAccess
}

func (s *memoryStoreForTest) SeedBook(projectID, bookID string) {
	s.projectRoles[roleKey(ActorEditor(projectID).UserID, projectID)] = projects.RoleEditor
	if _, exists := s.books[bookID]; exists {
		return
	}
	s.books[bookID] = Book{
		ID:            bookID,
		ProjectID:     projectID,
		Title:         "Book",
		Order:         s.nextBookOrder(projectID),
		DisplayNumber: "1",
		Status:        BookStatusDraft,
		Revision:      1,
		CreatedAt:     staticTestTime(),
		UpdatedAt:     staticTestTime(),
	}
}

func (s *memoryStoreForTest) ChapterCount() int {
	return len(s.chapters)
}

func (s *memoryStoreForTest) ChaptersForBook(bookID string) []Chapter {
	chapters := make([]Chapter, 0, len(s.chapters))
	for _, chapter := range s.chapters {
		if _, deleted := s.deletedChapters[chapter.ID]; deleted || chapter.BookID != bookID {
			continue
		}
		chapters = append(chapters, chapter)
	}
	sort.Slice(chapters, func(i, j int) bool {
		if chapters[i].Order == chapters[j].Order {
			return chapters[i].ID < chapters[j].ID
		}
		return chapters[i].Order < chapters[j].Order
	})
	return chapters
}

func (s *memoryStoreForTest) SeedChapter(projectID, bookID string, paragraphs []Paragraph) string {
	s.SeedBook(projectID, bookID)
	chapterID := fmt.Sprintf("chapter_%d", len(s.chapters)+1)
	chapter := Chapter{
		ID:        chapterID,
		BookID:    bookID,
		Title:     "Chapter",
		Order:     s.nextChapterOrder(bookID),
		Status:    ChapterStatusDraft,
		Revision:  1,
		CreatedAt: staticTestTime(),
		UpdatedAt: staticTestTime(),
	}
	s.chapters[chapterID] = chapter
	s.paragraphs[paragraphKey(chapterID, 1, ParagraphVariantDraft)] = s.seededParagraphs(chapterID, 1, ParagraphVariantDraft, paragraphs)
	return chapterID
}

func (s *memoryStoreForTest) seededParagraphs(chapterID string, revision int64, variant ParagraphVariant, paragraphs []Paragraph) []Paragraph {
	normalized := make([]Paragraph, 0, len(paragraphs))
	for index, paragraph := range paragraphs {
		if paragraph.Kind == "" {
			paragraph.Kind = ParagraphKindParagraph
		}
		if paragraph.Order == 0 {
			paragraph.Order = index + 1
		}
		paragraph.ChapterID = chapterID
		paragraph.Revision = revision
		paragraph.Variant = variant
		paragraph.PlainText = strings.TrimSpace(paragraph.Markdown)
		paragraph.WordCount = len(strings.Fields(paragraph.PlainText))
		paragraph.CreatedAt = staticTestTime()
		paragraph.UpdatedAt = staticTestTime()
		normalized = append(normalized, paragraph)
	}
	return normalized
}

func (s *memoryStoreForTest) ActiveProjectRole(ctx context.Context, actorUserID, projectID string) (projects.Role, error) {
	role, exists := s.projectRoles[roleKey(actorUserID, projectID)]
	if !exists {
		return "", ErrNotFound
	}
	return role, nil
}

func (s *memoryStoreForTest) ResolveBookAccessForActor(_ context.Context, actorUserID, bookID string, visibility projectaccess.Visibility) (projectaccess.ResolvedResourceAccess, error) {
	s.resolveBookCalls++
	book, exists := s.books[bookID]
	if !exists {
		return projectaccess.ResolvedResourceAccess{}, ErrNotFound
	}
	role, exists := s.projectRoles[roleKey(actorUserID, book.ProjectID)]
	if !exists {
		return projectaccess.ResolvedResourceAccess{}, ErrNotFound
	}
	_, deleted := s.deletedBooks[bookID]
	if (visibility == projectaccess.VisibilityActive && deleted) ||
		(visibility == projectaccess.VisibilityDeletedSource && !deleted) {
		return projectaccess.ResolvedResourceAccess{}, ErrNotFound
	}
	return s.overrideAccess(projectaccess.ResolvedResourceAccess{
		ProjectID:   book.ProjectID,
		Identity:    projectaccess.BookIdentity{BookID: bookID},
		ProjectRole: role,
		Visibility:  visibility,
	}), nil
}

func (s *memoryStoreForTest) overrideAccess(access projectaccess.ResolvedResourceAccess) projectaccess.ResolvedResourceAccess {
	if s.accessOverride == nil {
		return access
	}
	return s.accessOverride(access)
}

func (s *memoryStoreForTest) LoadDeletedBookLifecycle(_ context.Context, projectID, bookID string) (projectaccess.DeletedSourceLifecycle, error) {
	s.resolveDeletedBookCalls++
	book, exists := s.books[bookID]
	if !exists || book.ProjectID != projectID {
		return "", ErrNotFound
	}
	if _, deleted := s.deletedBooks[bookID]; !deleted {
		return "", ErrNotFound
	}
	lifecycle := s.deletedBookLifecycle[bookID]
	if lifecycle == "" {
		lifecycle = projectaccess.DeletedSourceRestorable
	}
	return lifecycle, nil
}

func (s *memoryStoreForTest) ResolveChapterAccessForActor(_ context.Context, actorUserID, chapterID string, visibility projectaccess.Visibility) (projectaccess.ResolvedResourceAccess, error) {
	s.resolveChapterCalls++
	chapter, exists := s.chapters[chapterID]
	if !exists {
		return projectaccess.ResolvedResourceAccess{}, ErrNotFound
	}
	book, exists := s.books[chapter.BookID]
	if !exists {
		return projectaccess.ResolvedResourceAccess{}, ErrNotFound
	}
	role, exists := s.projectRoles[roleKey(actorUserID, book.ProjectID)]
	if !exists {
		return projectaccess.ResolvedResourceAccess{}, ErrNotFound
	}
	_, bookDeleted := s.deletedBooks[book.ID]
	_, chapterDeleted := s.deletedChapters[chapterID]
	if (visibility == projectaccess.VisibilityActive && (bookDeleted || chapterDeleted)) ||
		(visibility == projectaccess.VisibilityDeletedSource && !chapterDeleted) {
		return projectaccess.ResolvedResourceAccess{}, ErrNotFound
	}
	return s.overrideAccess(projectaccess.ResolvedResourceAccess{
		ProjectID: book.ProjectID,
		Identity: projectaccess.ChapterIdentity{
			BookID: book.ID, ChapterID: chapterID,
		},
		ProjectRole: role,
		Visibility:  visibility,
	}), nil
}

func (s *memoryStoreForTest) LoadDeletedChapterLifecycle(_ context.Context, projectID, bookID, chapterID string) (projectaccess.DeletedSourceLifecycle, error) {
	s.resolveDeletedChapterCalls++
	chapter, exists := s.chapters[chapterID]
	if !exists || chapter.BookID != bookID {
		return "", ErrNotFound
	}
	if _, deleted := s.deletedChapters[chapterID]; !deleted {
		return "", ErrNotFound
	}
	book, exists := s.books[chapter.BookID]
	if !exists || book.ProjectID != projectID {
		return "", ErrNotFound
	}
	lifecycle := s.deletedChapterLifecycle[chapterID]
	if lifecycle == "" {
		lifecycle = projectaccess.DeletedSourceRestorable
	}
	return lifecycle, nil
}

func (s *memoryStoreForTest) NextBookOrder(ctx context.Context, projectID string) (int, error) {
	orders := make(map[int]struct{})
	for _, book := range s.books {
		if _, deleted := s.deletedBooks[book.ID]; deleted || book.ProjectID != projectID {
			continue
		}
		if _, duplicate := orders[book.Order]; duplicate {
			return 0, ErrImportSequenceConflict
		}
		orders[book.Order] = struct{}{}
	}
	for order := 1; ; order++ {
		if _, exists := orders[order]; !exists {
			return order, nil
		}
	}
}

func (s *memoryStoreForTest) CreateBook(ctx context.Context, input CreateBookStoreInput) (Book, error) {
	order := input.Order
	if input.AfterBookID.Present {
		var err error
		order, err = s.bookInsertionOrder(input.ProjectID, input.AfterBookID)
		if err != nil {
			return Book{}, err
		}
		s.shiftBooksForInsert(input.ProjectID, order)
	}
	book := Book{
		ID:            input.ID,
		ProjectID:     input.ProjectID,
		Title:         input.Title,
		Subtitle:      input.Subtitle,
		CoverColor:    input.CoverColor,
		Order:         order,
		DisplayNumber: input.DisplayNumber,
		Status:        BookStatusDraft,
		Revision:      1,
		CreatedAt:     input.CreatedAt,
		UpdatedAt:     input.CreatedAt,
	}
	s.books[book.ID] = book
	return s.bookWithSummary(book), nil
}

func (s *memoryStoreForTest) ListBooks(ctx context.Context, input ListBooksStoreInput) ([]Book, error) {
	s.lastListBooksInput = input
	books := make([]Book, 0, len(s.books))
	for _, book := range s.books {
		_, deleted := s.deletedBooks[book.ID]
		if book.ProjectID != input.ProjectID ||
			(input.State == SourceListDeleted && !deleted) ||
			(input.State != SourceListDeleted && deleted) {
			continue
		}
		if input.State == SourceListDeleted {
			deletedAt := s.deletedBookAt[book.ID]
			restoreUntil := deletedAt.Add(7 * 24 * time.Hour)
			book.DeletedAt = &deletedAt
			book.RestoreUntil = &restoreUntil
		}
		books = append(books, s.bookWithSummary(book))
	}
	books = keysetForTest(books, input.State, input.After, bookPosition)
	if input.Limit < len(books) {
		books = books[:input.Limit]
	}
	return books, nil
}

// keysetForTest orders rows as the PostgreSQL store does and keeps the ones after the position.
func keysetForTest[T any](rows []T, state SourceListState, after *ListPosition, position func(T) ListPosition) []T {
	less := func(left, right ListPosition) bool {
		if state == SourceListDeleted {
			if !left.DeletedAt.Equal(right.DeletedAt) {
				return left.DeletedAt.After(right.DeletedAt)
			}
			return left.ID > right.ID
		}
		if left.Order != right.Order {
			return left.Order < right.Order
		}
		return left.ID < right.ID
	}
	sort.Slice(rows, func(i, j int) bool { return less(position(rows[i]), position(rows[j])) })
	if after == nil {
		return rows
	}
	kept := rows[:0]
	for _, row := range rows {
		if less(*after, position(row)) {
			kept = append(kept, row)
		}
	}
	return kept
}

func (s *memoryStoreForTest) GetBook(ctx context.Context, projectID, bookID string) (Book, error) {
	s.getBookCalls++
	if _, deleted := s.deletedBooks[bookID]; deleted {
		return Book{}, ErrNotFound
	}
	book, exists := s.books[bookID]
	if !exists || book.ProjectID != projectID {
		return Book{}, ErrNotFound
	}
	return s.bookWithSummary(book), nil
}

func (s *memoryStoreForTest) UpdateBook(ctx context.Context, input UpdateBookStoreInput) (Book, error) {
	book, err := s.GetBook(ctx, input.ProjectID, input.BookID)
	if err != nil {
		return Book{}, err
	}
	order := book.Order
	if input.AfterBookID.Present {
		order, err = s.bookMoveTargetOrder(input.BookID, book.ProjectID, book.Order, input.AfterBookID)
		if err != nil {
			return Book{}, err
		}
		s.shiftBooksForMove(book.ProjectID, input.BookID, book.Order, order)
	}
	book.Title = input.Title
	book.Subtitle = input.Subtitle
	book.CoverColor = input.CoverColor
	book.Order = order
	book.Revision++
	book.UpdatedAt = input.UpdatedAt
	s.books[input.BookID] = book
	return s.bookWithSummary(book), nil
}

func (s *memoryStoreForTest) FenceBookDeletion(_ context.Context, input FenceBookDeletionInput) (deletion.SourceFenceResult, error) {
	book, exists := s.books[input.BookID]
	role, hasRole := s.projectRoles[roleKey(input.ActorUserID, input.ProjectID)]
	_, deleted := s.deletedBooks[input.BookID]
	if !exists || deleted || !hasRole || book.ProjectID != input.ProjectID {
		return deletion.SourceFenceResult{}, ErrNotFound
	}
	if !projects.CanMutateProject(role) {
		return deletion.SourceFenceResult{}, ErrForbidden
	}
	copyInput := input
	s.lastFenceBookInput = &copyInput
	s.deletedBooks[input.BookID] = struct{}{}
	s.deletedBookAt[input.BookID] = input.DeletedAt
	return deletion.SourceFenceResult{
		TombstoneID: input.TombstoneID,
		Deleted: governance.SourceGenerationVector{
			ProjectID: input.ProjectID, ProjectGeneration: 1,
			BookID: input.BookID, BookGeneration: 1,
		},
		Current: governance.SourceGenerationVector{
			ProjectID: input.ProjectID, ProjectGeneration: 1,
			BookID: input.BookID, BookGeneration: 2,
		},
	}, nil
}

func (s *memoryStoreForTest) RestoreBook(_ context.Context, input RestoreBookStoreInput) (Book, error) {
	book, exists := s.books[input.BookID]
	role, hasRole := s.projectRoles[roleKey(input.ActorUserID, input.ProjectID)]
	_, deleted := s.deletedBooks[input.BookID]
	if !exists || !deleted || !hasRole || book.ProjectID != input.ProjectID {
		return Book{}, ErrNotFound
	}
	if !projects.CanMutateProject(role) {
		return Book{}, ErrForbidden
	}
	copyInput := input
	s.lastRestoreBookInput = &copyInput
	delete(s.deletedBooks, input.BookID)
	delete(s.deletedBookAt, input.BookID)
	book.DeletedAt = nil
	book.RestoreUntil = nil
	return book, nil
}

func (s *memoryStoreForTest) ListChapters(ctx context.Context, input ListChaptersStoreInput) ([]Chapter, error) {
	s.lastListChaptersInput = input
	book, exists := s.books[input.BookID]
	if !exists || book.ProjectID != input.ProjectID {
		return nil, ErrNotFound
	}
	chapters := make([]Chapter, 0)
	for _, chapter := range s.chapters {
		_, deleted := s.deletedChapters[chapter.ID]
		if chapter.BookID != input.BookID ||
			(input.State == SourceListDeleted && !deleted) ||
			(input.State != SourceListDeleted && deleted) {
			continue
		}
		if input.State == SourceListDeleted {
			deletedAt := s.deletedChapterAt[chapter.ID]
			restoreUntil := deletedAt.Add(7 * 24 * time.Hour)
			chapter.DeletedAt = &deletedAt
			chapter.RestoreUntil = &restoreUntil
		}
		chapters = append(chapters, chapter)
	}
	chapters = keysetForTest(chapters, input.State, input.After, chapterPosition)
	if input.Limit < len(chapters) {
		chapters = chapters[:input.Limit]
	}
	return chapters, nil
}

func (s *memoryStoreForTest) GetChapter(ctx context.Context, projectID, bookID, chapterID string) (ChapterRecord, error) {
	s.getChapterCalls++
	if _, deleted := s.deletedChapters[chapterID]; deleted {
		return ChapterRecord{}, ErrNotFound
	}
	chapter, exists := s.chapters[chapterID]
	if !exists || chapter.BookID != bookID {
		return ChapterRecord{}, ErrNotFound
	}
	book, exists := s.books[chapter.BookID]
	if _, deleted := s.deletedBooks[chapter.BookID]; deleted || !exists || book.ProjectID != projectID {
		return ChapterRecord{}, ErrNotFound
	}
	chapters := s.ChaptersForBook(bookID)
	navigation := ChapterNavigation{Total: len(chapters)}
	for index := range chapters {
		if chapters[index].ID != chapterID {
			continue
		}
		navigation.Position = index + 1
		if index > 0 {
			previous := chapters[index-1]
			navigation.Previous = &ChapterNavigationItem{
				ID: previous.ID, Title: previous.Title, DisplayNumber: ChapterDisplayNumber(previous.Order, previous.Title),
			}
		}
		if index+1 < len(chapters) {
			next := chapters[index+1]
			navigation.Next = &ChapterNavigationItem{
				ID: next.ID, Title: next.Title, DisplayNumber: ChapterDisplayNumber(next.Order, next.Title),
			}
		}
		break
	}
	return ChapterRecord{Chapter: chapter, ProjectID: book.ProjectID, Navigation: navigation}, nil
}

func (s *memoryStoreForTest) ListChapterParagraphs(ctx context.Context, input ListChapterParagraphsStoreInput) ([]Paragraph, error) {
	paragraphs := append([]Paragraph(nil), s.paragraphs[paragraphKey(input.ChapterID, input.Revision, input.Variant)]...)
	sort.Slice(paragraphs, func(i, j int) bool {
		if paragraphs[i].Order == paragraphs[j].Order {
			return paragraphs[i].ID < paragraphs[j].ID
		}
		return paragraphs[i].Order < paragraphs[j].Order
	})
	return paragraphs, nil
}

func (s *memoryStoreForTest) bookWithSummary(book Book) Book {
	chapterCount := 0
	wordCount := 0
	for _, chapter := range s.ChaptersForBook(book.ID) {
		chapterCount++
		for _, paragraph := range s.paragraphs[paragraphKey(chapter.ID, chapter.Revision, ParagraphVariantDraft)] {
			wordCount += paragraph.WordCount
		}
	}
	book.ChapterCount = chapterCount
	book.WordCount = wordCount
	return book
}

// SeedDraftRevision stores the next draft revision of a seeded chapter.
func (s *memoryStoreForTest) SeedDraftRevision(chapterID string, paragraphs []Paragraph) {
	chapter := s.chapters[chapterID]
	chapter.Revision++
	chapter.Status = ChapterStatusDraft
	if chapter.PublishedRevision != nil {
		chapter.Status = ChapterStatusStale
	}
	s.chapters[chapterID] = chapter
	s.paragraphs[paragraphKey(chapterID, chapter.Revision, ParagraphVariantDraft)] = s.seededParagraphs(chapterID, chapter.Revision, ParagraphVariantDraft, paragraphs)
}

// SeedPublishedRevision publishes the current draft revision of a seeded chapter.
func (s *memoryStoreForTest) SeedPublishedRevision(chapterID string) {
	chapter := s.chapters[chapterID]
	revision := chapter.Revision
	publishedAt := staticTestTime()
	chapter.Status = ChapterStatusPublished
	chapter.PublishedRevision = &revision
	chapter.PublishedAt = &publishedAt
	s.chapters[chapterID] = chapter
	draft := s.paragraphs[paragraphKey(chapterID, revision, ParagraphVariantDraft)]
	s.paragraphs[paragraphKey(chapterID, revision, ParagraphVariantPublished)] = s.seededParagraphs(chapterID, revision, ParagraphVariantPublished, draft)
}

func (s *memoryStoreForTest) FenceChapterDeletion(_ context.Context, input FenceChapterDeletionInput) (deletion.SourceFenceResult, error) {
	chapter, exists := s.chapters[input.ChapterID]
	book, bookExists := s.books[input.BookID]
	role, hasRole := s.projectRoles[roleKey(input.ActorUserID, input.ProjectID)]
	_, deleted := s.deletedChapters[input.ChapterID]
	if !exists || !bookExists || deleted || !hasRole || chapter.BookID != input.BookID || book.ProjectID != input.ProjectID {
		return deletion.SourceFenceResult{}, ErrNotFound
	}
	if !projects.CanMutateProject(role) {
		return deletion.SourceFenceResult{}, ErrForbidden
	}
	copyInput := input
	s.lastFenceChapterInput = &copyInput
	s.deletedChapters[input.ChapterID] = struct{}{}
	s.deletedChapterAt[input.ChapterID] = input.DeletedAt
	return deletion.SourceFenceResult{
		TombstoneID: input.TombstoneID,
		Deleted: governance.SourceGenerationVector{
			ProjectID: input.ProjectID, ProjectGeneration: 1,
			BookID: input.BookID, BookGeneration: 1,
			ChapterID: input.ChapterID, ChapterGeneration: 1,
		},
		Current: governance.SourceGenerationVector{
			ProjectID: input.ProjectID, ProjectGeneration: 1,
			BookID: input.BookID, BookGeneration: 1,
			ChapterID: input.ChapterID, ChapterGeneration: 2,
		},
	}, nil
}

func (s *memoryStoreForTest) RestoreChapter(_ context.Context, input RestoreChapterStoreInput) (Chapter, error) {
	chapter, exists := s.chapters[input.ChapterID]
	book, bookExists := s.books[input.BookID]
	role, hasRole := s.projectRoles[roleKey(input.ActorUserID, input.ProjectID)]
	_, deleted := s.deletedChapters[input.ChapterID]
	if !exists || !bookExists || !deleted || !hasRole || chapter.BookID != input.BookID || book.ProjectID != input.ProjectID {
		return Chapter{}, ErrNotFound
	}
	if !projects.CanMutateProject(role) {
		return Chapter{}, ErrForbidden
	}
	copyInput := input
	s.lastRestoreChapterInput = &copyInput
	delete(s.deletedChapters, input.ChapterID)
	delete(s.deletedChapterAt, input.ChapterID)
	chapter.DeletedAt = nil
	chapter.RestoreUntil = nil
	return chapter, nil
}

func (s *memoryStoreForTest) CreateImportStagingForActor(_ context.Context, input CreateImportStagingForActorInput) (jobs.Job, error) {
	key := importIdempotencyKeyForTest(input.ActorUserID, input.RequestMethod, input.RequestPath, input.IdempotencyKey)
	s.importJobIdempotencyHits++
	if existing, exists := s.importJobsByKey[key]; exists {
		if existing.RequestFingerprint != input.RequestFingerprint {
			return jobs.Job{}, ErrConflict
		}
		if current, ok := s.importStartJobsByKey[key]; ok {
			return current, nil
		}
		return importStartJobForTest(existing), nil
	}
	expectedBookOrder, err := s.NextBookOrder(context.Background(), input.ProjectID)
	if err != nil {
		return jobs.Job{}, err
	}
	if input.ExpectedBookOrder > 0 && input.ExpectedBookOrder != expectedBookOrder {
		return jobs.Job{}, ErrImportExpectedOrderConflict
	}
	input.ExpectedBookOrder = expectedBookOrder
	createdAt := staticTestTime()
	uploadInput := StoreImportFileInput{
		StorageKey: input.Metadata.StorageKey, ProjectID: input.ProjectID,
		MIMEType: input.Metadata.MIMEType, SizeBytes: input.Metadata.SizeBytes,
		SHA256: input.Metadata.FileSHA256, Bytes: append([]byte(nil), input.FileBytes...),
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(ImportUploadRetention),
	}
	createInput := CreateImportJobStoreInput{
		ID: input.JobID, ProjectID: input.ProjectID, ActorUserID: input.ActorUserID,
		IdempotencyKey: input.IdempotencyKey, RequestMethod: input.RequestMethod,
		RequestPath: input.RequestPath, RequestFingerprint: input.RequestFingerprint,
		Kind: ImportJobKind, Status: ImportJobStatusQueued, Progress: 0,
		Subject:   ImportJobSubject{Type: "import", ID: input.ProjectID, ProjectID: input.ProjectID, ExpectedBookOrder: input.ExpectedBookOrder},
		CanCancel: true, CreatedAt: createdAt, UpdatedAt: createdAt, Metadata: input.Metadata,
	}
	enqueueInput := EnqueueImportJobInput{
		JobID: input.JobID, ProjectID: input.ProjectID,
		StorageKey: input.Metadata.StorageKey, EnqueuedAt: createdAt,
	}
	if s.createImportJobHook != nil {
		s.createImportJobHook(createInput)
	}
	if s.createImportJobErr != nil {
		if errors.Is(s.createImportJobErr, ErrConflict) {
			s.importJobIdempotencyHits++
			if existing, exists := s.importJobsByKey[key]; exists {
				if existing.RequestFingerprint != input.RequestFingerprint {
					return jobs.Job{}, ErrConflict
				}
				return importStartJobForTest(existing), nil
			}
		}
		return jobs.Job{}, s.createImportJobErr
	}
	s.enqueueImportJobCalls++
	if s.enqueueImportJobErr != nil {
		return jobs.Job{}, s.enqueueImportJobErr
	}
	s.importFiles[uploadInput.StorageKey] = uploadInput
	s.importJobs = append(s.importJobs, createInput)
	s.enqueuedImports = append(s.enqueuedImports, enqueueInput)
	job := ImportJob{
		ID:                 createInput.ID,
		ProjectID:          createInput.ProjectID,
		ActorUserID:        createInput.ActorUserID,
		Kind:               createInput.Kind,
		Status:             createInput.Status,
		Progress:           createInput.Progress,
		Subject:            createInput.Subject,
		CanCancel:          createInput.CanCancel,
		CreatedAt:          createInput.CreatedAt,
		UpdatedAt:          createInput.UpdatedAt,
		Metadata:           createInput.Metadata,
		RequestFingerprint: createInput.RequestFingerprint,
	}
	s.importJobsByKey[key] = job
	current := importStartJobForTest(job)
	s.importStartJobsByKey[key] = current
	return current, nil
}

func importStartJobForTest(job ImportJob) jobs.Job {
	progress := float64(job.Progress)
	return jobs.Job{
		ID:            job.ID,
		ProjectID:     job.ProjectID,
		Kind:          jobs.Kind(job.Kind),
		Status:        jobs.Status(job.Status),
		Progress:      &progress,
		Subject:       jobs.ImportSubject{Type: jobs.KindImport, ID: job.Subject.ID, ProjectID: job.Subject.ProjectID, ExpectedBookOrder: job.Subject.ExpectedBookOrder},
		CanCancel:     job.CanCancel,
		CreatorUserID: job.ActorUserID,
		ExpiresAt:     job.ExpiresAt,
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
	}
}

func (s *memoryStoreForTest) nextBookOrder(projectID string) int {
	order := 1
	for _, book := range s.books {
		if _, deleted := s.deletedBooks[book.ID]; !deleted && book.ProjectID == projectID && book.Order >= order {
			order = book.Order + 1
		}
	}
	return order
}

func (s *memoryStoreForTest) bookInsertionOrder(projectID string, placement OrderPlacement) (int, error) {
	if !placement.Present {
		return 0, ErrValidation
	}
	if placement.AfterID == nil {
		return 1, nil
	}
	anchor, err := s.GetBook(context.Background(), projectID, *placement.AfterID)
	if err != nil {
		return 0, err
	}
	if anchor.ProjectID != projectID {
		return 0, ErrNotFound
	}
	return anchor.Order + 1, nil
}

func (s *memoryStoreForTest) shiftBooksForInsert(projectID string, targetOrder int) {
	for id, book := range s.books {
		if _, deleted := s.deletedBooks[id]; !deleted && book.ProjectID == projectID && book.Order >= targetOrder {
			book.Order++
			s.books[id] = book
		}
	}
}

func (s *memoryStoreForTest) bookMoveTargetOrder(bookID, projectID string, currentOrder int, placement OrderPlacement) (int, error) {
	if !placement.Present {
		return currentOrder, nil
	}
	if placement.AfterID == nil {
		return 1, nil
	}
	if *placement.AfterID == bookID {
		return 0, ErrValidation
	}
	rawTarget, err := s.bookInsertionOrder(projectID, placement)
	if err != nil {
		return 0, err
	}
	if currentOrder < rawTarget {
		return rawTarget - 1, nil
	}
	return rawTarget, nil
}

func (s *memoryStoreForTest) shiftBooksForMove(projectID, bookID string, currentOrder, targetOrder int) {
	if targetOrder < currentOrder {
		for id, book := range s.books {
			if _, deleted := s.deletedBooks[id]; !deleted && id != bookID && book.ProjectID == projectID && book.Order >= targetOrder && book.Order < currentOrder {
				book.Order++
				s.books[id] = book
			}
		}
		return
	}
	if targetOrder > currentOrder {
		for id, book := range s.books {
			if _, deleted := s.deletedBooks[id]; !deleted && id != bookID && book.ProjectID == projectID && book.Order > currentOrder && book.Order <= targetOrder {
				book.Order--
				s.books[id] = book
			}
		}
	}
}

func (s *memoryStoreForTest) nextChapterOrder(bookID string) int {
	order := 1
	for _, chapter := range s.chapters {
		if chapter.BookID == bookID && chapter.Order >= order {
			order = chapter.Order + 1
		}
	}
	return order
}

func ActorEditor(projectID string) Actor {
	return Actor{UserID: "editor:" + projectID}
}

func roleKey(actorUserID, projectID string) string {
	return actorUserID + "|" + projectID
}

func paragraphKey(chapterID string, revision int64, variant ParagraphVariant) string {
	return fmt.Sprintf("%s|%d|%s", chapterID, revision, variant)
}

func importIdempotencyKeyForTest(actorUserID, method, path, idempotencyKey string) string {
	return actorUserID + "|" + method + "|" + path + "|" + idempotencyKey
}

func StaticIDsForTest() *staticIDsForTest {
	return &staticIDsForTest{}
}

type staticIDsForTest struct {
	book      int
	chapter   int
	paragraph int
	importJob int
	storage   int
	tombstone int
}

func (ids *staticIDsForTest) NewBookID() string {
	ids.book++
	return fmt.Sprintf("book_generated_%d", ids.book)
}

func (ids *staticIDsForTest) NewChapterID() string {
	ids.chapter++
	return fmt.Sprintf("chapter_generated_%d", ids.chapter)
}

func (ids *staticIDsForTest) NewParagraphID() string {
	ids.paragraph++
	return fmt.Sprintf("p_generated_%d", ids.paragraph)
}

func (ids *staticIDsForTest) NewImportJobID() string {
	ids.importJob++
	return fmt.Sprintf("job_generated_%d", ids.importJob)
}

func (ids *staticIDsForTest) NewImportStorageKey() string {
	ids.storage++
	return fmt.Sprintf("storage_generated_%d", ids.storage)
}

func (ids *staticIDsForTest) NewIdempotencyRecordID() string {
	ids.storage++
	return fmt.Sprintf("idem_generated_%d", ids.storage)
}

func (ids *staticIDsForTest) NewDeletionTombstoneID() string {
	ids.tombstone++
	return fmt.Sprintf("tombstone_generated_%d", ids.tombstone)
}

func StaticClockForTest() staticClockForTest {
	return staticClockForTest{}
}

type staticClockForTest struct{}

func (staticClockForTest) Now() time.Time {
	return staticTestTime()
}

func staticTestTime() time.Time {
	return time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
}
