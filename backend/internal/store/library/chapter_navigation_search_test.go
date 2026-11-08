package librarystore

import (
	"context"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	chapterrepo "canonkeeper/backend/internal/repository/postgres/library"
	chapterservice "canonkeeper/backend/internal/services/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
)

func TestChapterNavigationSearchFinds458BeforeIndexingAcrossReadableSagas(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	ctx := context.Background()
	actor := indexingStoreUUIDForTest(19701)
	viewer := indexingStoreUUIDForTest(19801)
	for _, base := range []int{19700, 19800} {
		project, book, _, chapter := seedSourceCatalogSourcesForTest(t, db, base)
		err := db.WithTx(ctx, func(tx pgx.Tx) error {
			if _, err := createDraftProjectSourceView(ctx, tx, project, 1); err != nil {
				return err
			}
			member := library.BookSourceMember{ProjectID: project.String(), ProjectLifecycleGeneration: 1, SourceView: governance.SourceViewDraft, BookID: book.String(), BookLifecycleGeneration: 1, Title: "Том 9"}
			if err := upsertBookSourceMember(ctx, tx, member); err != nil {
				return err
			}
			if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{BookSourceMember: member, ChapterID: chapter.String(), ChapterLifecycleGeneration: 1, SourceRevision: 1, Title: "Глава 458. Создание навыка", Order: 20}); err != nil {
				return err
			}
			_, err := sqlc.New(tx).CreateProjectMembership(ctx, sqlc.CreateProjectMembershipParams{
				ID: uuidValueForSourceCatalogTest(t, indexingStoreUUIDForTest(base+7)), ProjectID: project, UserID: uuidValueForSourceCatalogTest(t, actor), Role: "editor", CreatedAt: storedb.Timestamptz(time.Now()),
			})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := sqlc.New(db.Pool).CreateProjectMembership(ctx, sqlc.CreateProjectMembershipParams{
		ID: uuidValueForSourceCatalogTest(t, indexingStoreUUIDForTest(19907)), ProjectID: uuidValueForSourceCatalogTest(t, indexingStoreUUIDForTest(19702)), UserID: uuidValueForSourceCatalogTest(t, viewer), Role: "viewer", CreatedAt: storedb.Timestamptz(time.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	search := chapterservice.NewSearchChapters(chapterrepo.New(db))
	for _, query := range []string{"458", "глава 458", "ГЛАВА 00458", "создание навыка"} {
		page, err := search.Execute(ctx, domain.Actor{UserID: actor}, domain.ChapterSearchRequest{Query: query, SourceView: "draft"})
		if err != nil || len(page.Data) != 2 {
			t.Fatalf("query=%q count=%d err=%v", query, len(page.Data), err)
		}
		for _, entry := range page.Data {
			if entry.ChapterNumber != "458" || entry.Locator.Revision != 2 {
				t.Fatalf("wrong ordinal or manuscript revision: %#v", entry)
			}
		}
	}
	for _, query := range []string{"21", "1458", "%", "_"} {
		page, err := search.Execute(ctx, domain.Actor{UserID: actor}, domain.ChapterSearchRequest{Query: query, SourceView: "draft"})
		if err != nil || len(page.Data) != 0 {
			t.Fatalf("unmatched query=%q page=%#v err=%v", query, page, err)
		}
	}
	for _, input := range []struct{ actor, view string }{{actor, "published"}, {viewer, "draft"}, {indexingStoreUUIDForTest(19999), "draft"}} {
		page, err := search.Execute(ctx, domain.Actor{UserID: input.actor}, domain.ChapterSearchRequest{Query: "458", SourceView: input.view})
		if err != nil || len(page.Data) != 0 {
			t.Fatalf("ACL/view input=%#v page=%#v err=%v", input, page, err)
		}
	}
	request := domain.ChapterSearchRequest{Query: "458", SourceView: "draft", Limit: 1}
	first, err := search.Execute(ctx, domain.Actor{UserID: actor}, request)
	if err != nil || len(first.Data) != 1 || !first.Meta.HasMore || first.Meta.NextCursor == "" {
		t.Fatalf("first page=%#v err=%v", first, err)
	}
	request.Cursor = first.Meta.NextCursor
	second, err := search.Execute(ctx, domain.Actor{UserID: actor}, request)
	if err != nil || len(second.Data) != 1 || second.Meta.HasMore || second.Data[0].Locator.ChapterID == first.Data[0].Locator.ChapterID {
		t.Fatalf("second page=%#v err=%v", second, err)
	}

	project := uuidValueForSourceCatalogTest(t, indexingStoreUUIDForTest(19702))
	chapter := uuidValueForSourceCatalogTest(t, indexingStoreUUIDForTest(19705))
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := createOrBumpPublishedSourceView(ctx, tx, project, 1); err != nil {
			return err
		}
		member := library.BookSourceMember{ProjectID: project.String(), ProjectLifecycleGeneration: 1, SourceView: governance.SourceViewPublished, BookID: indexingStoreUUIDForTest(19703), BookLifecycleGeneration: 1, Title: "Опубликованный том"}
		if err := upsertBookSourceMember(ctx, tx, member); err != nil {
			return err
		}
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{BookSourceMember: member, ChapterID: chapter.String(), ChapterLifecycleGeneration: 1, SourceRevision: 1, Title: "Глава 458. Опубликованное название", Order: 20}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE chapters SET published_revision=1 WHERE id=$1`, chapter)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	publishedRequest := domain.ChapterSearchRequest{Query: "458", SourceView: "published"}
	published, err := search.Execute(ctx, domain.Actor{UserID: viewer}, publishedRequest)
	if err != nil || len(published.Data) != 1 || published.Data[0].Locator.Revision != 1 || published.Data[0].ChapterTitle != "Глава 458. Опубликованное название" || published.Data[0].BookTitle != "Опубликованный том" {
		t.Fatalf("published viewer page=%#v err=%v", published, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at=now() WHERE project_id=$1 AND user_id=$2`, project, viewer); err != nil {
		t.Fatal(err)
	}
	revoked, err := search.Execute(ctx, domain.Actor{UserID: viewer}, publishedRequest)
	if err != nil || len(revoked.Data) != 0 {
		t.Fatalf("revoked viewer page=%#v err=%v", revoked, err)
	}
	if _, err := New(db).FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
		ActorUserID: actor, ProjectID: project.String(), BookID: indexingStoreUUIDForTest(19703), ChapterID: chapter.String(),
		TombstoneID: indexingStoreUUIDForTest(19908), DeletedAt: time.Now().UTC().Truncate(time.Microsecond),
	}); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := search.Execute(ctx, domain.Actor{UserID: actor}, domain.ChapterSearchRequest{Query: "458", SourceView: "draft"})
	if err != nil || len(afterDelete.Data) != 1 || afterDelete.Data[0].Locator.ChapterID == chapter.String() {
		t.Fatalf("deleted chapter page=%#v err=%v", afterDelete, err)
	}
}
