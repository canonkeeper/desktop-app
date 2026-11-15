package librarystore

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/bookexport"
	domain "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	chapterrepo "canonkeeper/backend/internal/repository/postgres/library"
	chapterservice "canonkeeper/backend/internal/services/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
	"canonkeeper/backend/internal/testutil"
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

// specs/2026-11-15-reader-chapter-number.md RN1.2: a search of chapters by a number is one query, whatever the chapters of
// the saga whose titles hold its digits and whatever the page. A book of 300 chapters «Глава N. Перевал» and a book of
// three chapters without a number in the title, both imported by the handler of the import Job: «1» finds the first
// chapter of each book (the number its title writes, the place of the other) a page at a time, «глава 7» the seventh
// chapter, «Брод» the chapter of that title — each page one query.
func TestASearchOfChaptersByANumberIsOneQuery(t *testing.T) {
	counter := &roundTripCounter{}
	fixture := newImportWriteFixture(t, testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_store", counter))
	long := make([]scenarioChapter, 300)
	for index := range long {
		long[index] = scenarioChapter{title: fmt.Sprintf("Глава %d. Перевал", index+1), paragraphs: []string{"Ветер гнал снег по склону."}}
	}
	fixture.importFile(library.ImportModeNewBook, "", "long.fb2", scenarioFile(t, bookexport.FormatFB2, long))
	short := []scenarioChapter{
		{title: "Устье", paragraphs: []string{"Река уходила в море."}},
		{title: "Брод", paragraphs: []string{"Вода стояла по колено."}},
		{title: "Мельница", paragraphs: []string{"Колесо скрипело всю ночь."}},
	}
	fixture.importFile(library.ImportModeNewBook, "", "short.fb2", scenarioFile(t, bookexport.FormatFB2, short))
	search := chapterservice.NewSearchChapters(chapterrepo.New(fixture.db))
	execute := func(request domain.ChapterSearchRequest) (domain.ChapterSearchPage, int) {
		t.Helper()
		counter.mu.Lock()
		counter.on, counter.roundTrips = true, 0
		counter.mu.Unlock()
		page, err := search.Execute(fixture.ctx, domain.Actor{UserID: fixture.userID}, request)
		counter.mu.Lock()
		counter.on = false
		queries := counter.roundTrips
		counter.mu.Unlock()
		if err != nil {
			t.Fatalf("search %q: %v", request.Query, err)
		}
		return page, queries
	}
	found := func(page domain.ChapterSearchPage) []string {
		named := []string{}
		for _, row := range page.Data {
			named = append(named, row.ChapterNumber+" "+row.ChapterDisplayTitle)
		}
		return named
	}
	for _, query := range []struct {
		query string
		want  []string
	}{
		{"1", []string{"1 Перевал", "1 Устье"}},
		{"глава 7", []string{"7 Перевал"}},
		{"300", []string{"300 Перевал"}},
		{"Брод", []string{"2 Брод"}},
	} {
		var named []string
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > len(query.want) {
				t.Fatalf("search %q pages on past %v", query.query, named)
			}
			page, queries := execute(domain.ChapterSearchRequest{Query: query.query, SourceView: "published", Limit: 1, Cursor: cursor})
			if queries != 1 {
				t.Errorf("search %q, page %d: %d queries; want one", query.query, pages+1, queries)
			}
			named = append(named, found(page)...)
			if !page.Meta.HasMore {
				break
			}
			cursor = page.Meta.NextCursor
		}
		slices.Sort(named)
		if !slices.Equal(named, query.want) {
			t.Errorf("search %q found %v; want %v", query.query, named, query.want)
		}
		page, queries := execute(domain.ChapterSearchRequest{Query: query.query, SourceView: "published"})
		if found := found(page); queries != 1 || page.Meta.HasMore || len(found) != len(query.want) {
			t.Errorf("search %q in one page: %v, more %v, %d queries; want %v in one query", query.query, found, page.Meta.HasMore, queries, query.want)
		}
	}
}
