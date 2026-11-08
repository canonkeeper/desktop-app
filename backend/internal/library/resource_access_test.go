package library

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/projectaccess"
)

// Резолвер доступа может вернуть чужую или неполную запись; живые операции над
// книгой и главой обязаны скрыть её как 404 и ничего не менять.
func TestLiveResourceOperationsConcealInconsistentResolverAccess(t *testing.T) {
	distortions := []struct {
		name    string
		distort func(projectaccess.ResolvedResourceAccess) projectaccess.ResolvedResourceAccess
	}{
		{name: "foreign identity", distort: func(access projectaccess.ResolvedResourceAccess) projectaccess.ResolvedResourceAccess {
			switch identity := access.Identity.(type) {
			case projectaccess.BookIdentity:
				identity.BookID = "book_foreign"
				access.Identity = identity
			case projectaccess.ChapterIdentity:
				identity.ChapterID = "chapter_foreign"
				access.Identity = identity
			}
			return access
		}},
		{name: "missing owning project", distort: func(access projectaccess.ResolvedResourceAccess) projectaccess.ResolvedResourceAccess {
			access.ProjectID = ""
			return access
		}},
		{name: "wrong visibility", distort: func(access projectaccess.ResolvedResourceAccess) projectaccess.ResolvedResourceAccess {
			if access.Visibility == projectaccess.VisibilityActive {
				access.Visibility = projectaccess.VisibilityDeletedSource
			} else {
				access.Visibility = projectaccess.VisibilityActive
			}
			return access
		}},
		{name: "wrong identity variant", distort: func(access projectaccess.ResolvedResourceAccess) projectaccess.ResolvedResourceAccess {
			switch identity := access.Identity.(type) {
			case projectaccess.BookIdentity:
				access.Identity = projectaccess.ChapterIdentity{BookID: identity.BookID, ChapterID: identity.BookID}
			case projectaccess.ChapterIdentity:
				access.Identity = projectaccess.BookIdentity{BookID: identity.BookID}
			}
			return access
		}},
		{name: "missing identity", distort: func(access projectaccess.ResolvedResourceAccess) projectaccess.ResolvedResourceAccess {
			access.Identity = nil
			return access
		}},
	}
	operations := []struct {
		name      string
		arrange   func(*memoryStoreForTest) string
		run       func(*Service, string) error
		unchanged func(*memoryStoreForTest) bool
	}{
		{
			name:    "delete book",
			arrange: func(store *memoryStoreForTest) string { store.SeedBook("project_1", "book_1"); return "book_1" },
			run: func(service *Service, id string) error {
				return service.DeleteBook(context.Background(), ActorEditor("project_1"), id)
			},
			unchanged: func(store *memoryStoreForTest) bool { return store.lastFenceBookInput == nil },
		},
		{
			name: "restore book",
			arrange: func(store *memoryStoreForTest) string {
				store.SeedBook("project_1", "book_1")
				store.deletedBooks["book_1"] = struct{}{}
				return "book_1"
			},
			run: func(service *Service, id string) error {
				_, err := service.RestoreBook(context.Background(), ActorEditor("project_1"), id)
				return err
			},
			unchanged: func(store *memoryStoreForTest) bool {
				return store.lastRestoreBookInput == nil && store.resolveDeletedBookCalls == 0
			},
		},
		{
			name:    "delete chapter",
			arrange: func(store *memoryStoreForTest) string { return store.SeedChapter("project_1", "book_1", nil) },
			run: func(service *Service, id string) error {
				return service.DeleteChapter(context.Background(), ActorEditor("project_1"), id)
			},
			unchanged: func(store *memoryStoreForTest) bool { return store.lastFenceChapterInput == nil },
		},
		{
			name: "restore chapter",
			arrange: func(store *memoryStoreForTest) string {
				chapterID := store.SeedChapter("project_1", "book_1", nil)
				store.deletedChapters[chapterID] = struct{}{}
				return chapterID
			},
			run: func(service *Service, id string) error {
				_, err := service.RestoreChapter(context.Background(), ActorEditor("project_1"), id)
				return err
			},
			unchanged: func(store *memoryStoreForTest) bool {
				return store.lastRestoreChapterInput == nil && store.resolveDeletedChapterCalls == 0
			},
		},
	}
	for _, operation := range operations {
		for _, distortion := range distortions {
			t.Run(operation.name+"/"+distortion.name, func(t *testing.T) {
				store := NewMemoryStoreForTest()
				id := operation.arrange(store)
				store.accessOverride = distortion.distort
				service := NewService(store, StaticIDsForTest(), StaticClockForTest())
				if err := operation.run(service, id); !errors.Is(err, ErrNotFound) {
					t.Fatalf("error = %v, want ErrNotFound", err)
				}
				if !operation.unchanged(store) {
					t.Fatal("inconsistent access reached the store mutation")
				}
			})
		}
	}
}
