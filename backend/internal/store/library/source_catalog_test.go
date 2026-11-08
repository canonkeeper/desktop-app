package librarystore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSourceCatalogDistinguishesAbsentFromCurrentEmptyAndKeepsViewsIndependent(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	projectID, bookID, emptyBookID, chapterID := seedSourceCatalogSourcesForTest(t, db, 4100)

	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		lockDraftView := sqlc.LockProjectSourceViewParams{
			ProjectID: projectID, SourceView: string(governance.SourceViewDraft), ProjectLifecycleGeneration: 1,
		}
		if _, err := sqlc.New(tx).LockProjectSourceView(ctx, lockDraftView); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("absent marker error = %v, want pgx.ErrNoRows", err)
		}
		draft, err := createDraftProjectSourceView(ctx, tx, projectID, 1)
		if err != nil {
			return err
		}
		if draft.CatalogRevision != 1 {
			t.Fatalf("created draft revision = %d", draft.CatalogRevision)
		}
		locked, err := sqlc.New(tx).LockProjectSourceView(ctx, lockDraftView)
		if err != nil {
			return err
		}
		if locked.CatalogRevision != 1 {
			t.Fatalf("current-empty draft revision = %d", locked.CatalogRevision)
		}

		for _, member := range []library.BookSourceMember{
			{ProjectID: projectID.String(), ProjectLifecycleGeneration: 1, SourceView: governance.SourceViewDraft, BookID: bookID.String(), BookLifecycleGeneration: 1, Title: "Draft book"},
			{ProjectID: projectID.String(), ProjectLifecycleGeneration: 1, SourceView: governance.SourceViewDraft, BookID: emptyBookID.String(), BookLifecycleGeneration: 1, Title: "Empty draft book"},
		} {
			if err := upsertBookSourceMember(ctx, tx, member); err != nil {
				return err
			}
		}
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
				SourceView: governance.SourceViewDraft, BookID: bookID.String(), BookLifecycleGeneration: 1, Title: "Draft book",
			},
			ChapterID: chapterID.String(), ChapterLifecycleGeneration: 1,
			SourceRevision: 1, Title: "Draft chapter", Order: 0,
		}); err != nil {
			return err
		}
		draft, err = bumpProjectSourceView(ctx, tx, projectID, 1, governance.SourceViewDraft)
		if err != nil {
			return err
		}
		if draft.CatalogRevision != 2 {
			t.Fatalf("single draft bump after multiple members = %d", draft.CatalogRevision)
		}

		published, err := createOrBumpPublishedSourceView(ctx, tx, projectID, 1)
		if err != nil {
			return err
		}
		if published.CatalogRevision != 1 {
			t.Fatalf("created published revision = %d", published.CatalogRevision)
		}
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
			SourceView: governance.SourceViewPublished, BookID: bookID.String(), BookLifecycleGeneration: 1, Title: "Published book",
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := currentSourceCatalogBookCountForTest(t, db, projectID, governance.SourceViewDraft); got != 2 {
		t.Fatalf("current draft books = %d, want 2 including zero-chapter book", got)
	}
	if got := currentSourceCatalogBookCountForTest(t, db, projectID, governance.SourceViewPublished); got != 0 {
		t.Fatalf("current published books without current chapter = %d, want 0", got)
	}

	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
				SourceView: governance.SourceViewPublished, BookID: bookID.String(), BookLifecycleGeneration: 1, Title: "Published book",
			},
			ChapterID: chapterID.String(), ChapterLifecycleGeneration: 1,
			SourceRevision: 1, Title: "Published chapter", Order: 7,
		}); err != nil {
			return err
		}
		_, err := bumpProjectSourceView(ctx, tx, projectID, 1, governance.SourceViewPublished)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var draftBookTitle, publishedBookTitle, draftChapterTitle, publishedChapterTitle string
	var draftRevision, publishedRevision int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT draft_book.title, published_book.title,
		       draft_chapter.title, published_chapter.title,
		       draft_chapter.source_revision, published_chapter.source_revision
		FROM book_source_members AS draft_book
		JOIN book_source_members AS published_book
		  ON published_book.project_id = draft_book.project_id
		 AND published_book.book_id = draft_book.book_id
		 AND published_book.source_view = 'published'
		JOIN chapter_source_members AS draft_chapter
		  ON draft_chapter.project_id = draft_book.project_id
		 AND draft_chapter.book_id = draft_book.book_id
		 AND draft_chapter.source_view = 'draft'
		JOIN chapter_source_members AS published_chapter
		  ON published_chapter.project_id = draft_book.project_id
		 AND published_chapter.chapter_id = draft_chapter.chapter_id
		 AND published_chapter.source_view = 'published'
		WHERE draft_book.project_id = $1 AND draft_book.source_view = 'draft'
		  AND draft_book.book_id = $2
	`, projectID, bookID).Scan(
		&draftBookTitle, &publishedBookTitle, &draftChapterTitle, &publishedChapterTitle,
		&draftRevision, &publishedRevision,
	); err != nil {
		t.Fatal(err)
	}
	if draftBookTitle != "Draft book" || publishedBookTitle != "Published book" ||
		draftChapterTitle != "Draft chapter" || publishedChapterTitle != "Published chapter" ||
		draftRevision != 1 || publishedRevision != 1 {
		t.Fatalf("independent projections = %q/%q %q/%q %d/%d", draftBookTitle, publishedBookTitle, draftChapterTitle, publishedChapterTitle, draftRevision, publishedRevision)
	}

	var beforeReorder int64
	if err := db.Pool.QueryRow(ctx, `SELECT catalog_revision FROM project_source_views WHERE project_id=$1 AND source_view='draft'`, projectID).Scan(&beforeReorder); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE books SET "order" = "order" + 10 WHERE project_id=$1`, projectID); err != nil {
		t.Fatal(err)
	}
	var afterReorder int64
	if err := db.Pool.QueryRow(ctx, `SELECT catalog_revision FROM project_source_views WHERE project_id=$1 AND source_view='draft'`, projectID).Scan(&afterReorder); err != nil {
		t.Fatal(err)
	}
	if afterReorder != beforeReorder {
		t.Fatalf("project-relative book reorder changed catalog %d -> %d", beforeReorder, afterReorder)
	}
}

func TestSourceCatalogAllowsDeferredPublishedMarker(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	projectID, bookID, _, chapterID := seedSourceCatalogSourcesForTest(t, db, 4090)

	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
			SourceView: governance.SourceViewPublished, BookID: bookID.String(),
			BookLifecycleGeneration: 1, Title: "Published book",
		}); err != nil {
			return err
		}
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
				SourceView: governance.SourceViewPublished, BookID: bookID.String(),
				BookLifecycleGeneration: 1, Title: "Published book",
			},
			ChapterID: chapterID.String(), ChapterLifecycleGeneration: 1,
			SourceRevision: 2, Title: "Published chapter", Order: 0,
		}); err != nil {
			return err
		}
		revision, err := createOrBumpPublishedSourceView(ctx, tx, projectID, 1)
		if err != nil {
			return err
		}
		if revision.CatalogRevision != 1 {
			t.Fatalf("published revision = %d, want 1", revision.CatalogRevision)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("commit members before deferred marker: %v", err)
	}
	if got := currentSourceCatalogBookCountForTest(t, db, projectID, governance.SourceViewPublished); got != 1 {
		t.Fatalf("published book count = %d, want 1", got)
	}
	if got := currentSourceCatalogChapterCountForTest(t, db, projectID, governance.SourceViewPublished); got != 1 {
		t.Fatalf("published chapter count = %d, want 1", got)
	}
}

func TestSourceCatalogRejectsWrongLifecycleAndSnapshotUsesMemberRevision(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	projectID, bookID, _, chapterID := seedSourceCatalogSourcesForTest(t, db, 4200)

	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := createDraftProjectSourceView(ctx, tx, projectID, 1); err != nil {
			return err
		}
		book := library.BookSourceMember{
			ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
			SourceView: governance.SourceViewDraft, BookID: bookID.String(), BookLifecycleGeneration: 1, Title: "Book",
		}
		if err := upsertBookSourceMember(ctx, tx, book); err != nil {
			return err
		}
		wrong := library.ChapterSourceMember{
			BookSourceMember: book, ChapterID: chapterID.String(), ChapterLifecycleGeneration: 2,
			SourceRevision: 1, Title: "Chapter", Order: 0,
		}
		if err := upsertChapterSourceMember(ctx, tx, wrong); !errors.Is(err, library.ErrNotFound) {
			t.Fatalf("wrong chapter generation error = %v, want ErrNotFound", err)
		}
		wrong.ChapterLifecycleGeneration = 1
		if err := upsertChapterSourceMember(ctx, tx, wrong); err != nil {
			return err
		}
		_, err := bumpProjectSourceView(ctx, tx, projectID, 1, governance.SourceViewDraft)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := sqlc.New(db.Pool).ListChapterSourceSnapshot(ctx, sqlc.ListChapterSourceSnapshotParams{
		ProjectID: projectID, SourceView: "draft", ChapterID: chapterID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 1 || snapshot[0].PlainText != "revision one" {
		t.Fatalf("member snapshot = %#v", snapshot)
	}

	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE chapters SET lifecycle_generation=2 WHERE id=$1`, chapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SET session_replication_role = origin`); err != nil {
		t.Fatal(err)
	}
	if got := currentSourceCatalogChapterCountForTest(t, db, projectID, governance.SourceViewDraft); got != 0 {
		t.Fatalf("wrong-lifecycle current chapters = %d, want 0", got)
	}
}

func TestSourceCatalogNormalizesPublishedOrderAndReplacesRetainedProjection(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	projectID, bookID, emptyBookID, firstChapterID := seedSourceCatalogSourcesForTest(t, db, 4300)
	secondChapterID := uuidValueForSourceCatalogTest(t, indexingStoreUUIDForTest(4307))
	secondParagraphID := indexingStoreUUIDForTest(4308)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chapters (id,project_id,book_id,title,"order",revision,published_revision)
		VALUES ($1,$2,$3,'Second chapter',1,1,1)
	`, secondChapterID, projectID, bookID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chapter_paragraphs (
		  id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count
		) VALUES
		  ($1,$2,1,'draft','paragraph',0,'second draft','second draft',2),
		  ($1,$2,1,'published','paragraph',0,'second published','second published',2)
	`, secondParagraphID, secondChapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE chapters SET published_revision=1 WHERE id=$1`, firstChapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chapter_paragraphs (
		  id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count
		)
		SELECT id,chapter_id,revision,'published',kind,"order",markdown,plain_text,word_count
		FROM chapter_paragraphs
		WHERE chapter_id=$1 AND revision=1 AND variant='draft'
	`, firstChapterID); err != nil {
		t.Fatal(err)
	}

	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := createDraftProjectSourceView(ctx, tx, projectID, 1); err != nil {
			return err
		}
		for _, sourceBookID := range []pgtype.UUID{bookID, emptyBookID} {
			if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
				SourceView: governance.SourceViewDraft, BookID: sourceBookID.String(),
				BookLifecycleGeneration: 1, Title: "Draft " + sourceBookID.String(),
			}); err != nil {
				return err
			}
		}
		for order, chapterID := range []pgtype.UUID{firstChapterID, secondChapterID} {
			if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
				BookSourceMember: library.BookSourceMember{
					ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
					SourceView: governance.SourceViewDraft, BookID: bookID.String(),
					BookLifecycleGeneration: 1, Title: "Draft book",
				},
				ChapterID: chapterID.String(), ChapterLifecycleGeneration: 1,
				SourceRevision: 1, Title: "Draft " + chapterID.String(), Order: order,
			}); err != nil {
				return err
			}
		}
		if _, err := bumpProjectSourceView(ctx, tx, projectID, 1, governance.SourceViewDraft); err != nil {
			return err
		}
		if _, err := createOrBumpPublishedSourceView(ctx, tx, projectID, 1); err != nil {
			return err
		}
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
			SourceView: governance.SourceViewPublished, BookID: bookID.String(),
			BookLifecycleGeneration: 1, Title: "Published book",
		}); err != nil {
			return err
		}
		for _, chapterID := range []pgtype.UUID{firstChapterID, secondChapterID} {
			if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
				BookSourceMember: library.BookSourceMember{
					ProjectID: projectID.String(), ProjectLifecycleGeneration: 1,
					SourceView: governance.SourceViewPublished, BookID: bookID.String(),
					BookLifecycleGeneration: 1, Title: "Published book",
				},
				ChapterID: chapterID.String(), ChapterLifecycleGeneration: 1,
				SourceRevision: 1, Title: "Published " + chapterID.String(), Order: 9,
			}); err != nil {
				return err
			}
		}
		if err := normalizePublishedChapterOrder(ctx, tx, projectID, 1, bookID, 1); err != nil {
			return err
		}
		_, err := bumpProjectSourceView(ctx, tx, projectID, 1, governance.SourceViewPublished)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSourceCatalogPublishedOrdersForTest(t, db, projectID, []int32{0, 1})

	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET lifecycle_generation=2 WHERE id=$1`, projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE books SET lifecycle_generation=2 WHERE project_id=$1`, projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE chapters SET lifecycle_generation=2 WHERE project_id=$1`, projectID); err != nil {
			return err
		}
		return New(db).RestoreProjectSourceCatalog(ctx, tx, projectID, 2)
	})
	if err != nil {
		t.Fatal(err)
	}

	var staleRows int
	if err := db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM project_source_views WHERE project_id=$1 AND project_lifecycle_generation<>2)
		+ (SELECT count(*) FROM book_source_members WHERE project_id=$1 AND (project_lifecycle_generation<>2 OR book_lifecycle_generation<>2))
		+ (SELECT count(*) FROM chapter_source_members WHERE project_id=$1 AND (project_lifecycle_generation<>2 OR book_lifecycle_generation<>2 OR chapter_lifecycle_generation<>2))
	`, projectID).Scan(&staleRows); err != nil {
		t.Fatal(err)
	}
	if staleRows != 0 {
		t.Fatalf("restored stale-generation catalog rows = %d", staleRows)
	}
	assertQueuedTextIntentsMatchCatalogForTest(t, db, projectID.String(), []governance.SourceView{
		governance.SourceViewDraft,
		governance.SourceViewPublished,
	})
	assertSourceCatalogPublishedOrdersForTest(t, db, projectID, []int32{0, 1})
}

func TestSourceCatalogPartialRestorePreservesRetainedSiblingProjection(t *testing.T) {
	tests := []struct {
		name string
		kind string
		base int
	}{
		{name: "book siblings", kind: "book", base: 4400},
		{name: "chapter siblings", kind: "chapter", base: 4420},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := openCurrentStoreTestDB(t)
			projectID, firstBookID, secondBookID, firstChapterID := seedSourceCatalogSourcesForTest(t, db, test.base)
			secondChapterID := uuidValueForSourceCatalogTest(t, indexingStoreUUIDForTest(test.base+7))
			secondBookChapterID := uuidValueForSourceCatalogTest(t, indexingStoreUUIDForTest(test.base+8))
			if _, err := db.Pool.Exec(ctx, `
				UPDATE books
				SET title=CASE id WHEN $2 THEN 'Current first book' ELSE 'Current sibling book' END
				WHERE project_id=$1
			`, projectID, firstBookID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `
				UPDATE chapters
				SET title='Current first chapter', "order"=2, published_revision=2
				WHERE project_id=$1 AND id=$2
			`, projectID, firstChapterID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `
				INSERT INTO chapters (id,project_id,book_id,title,"order",revision,published_revision) VALUES
				  ($2,$1,$3,'Current sibling chapter',7,7,7),
				  ($4,$1,$5,'Current sibling book chapter',8,8,8)
			`, projectID, secondChapterID, firstBookID, secondBookChapterID, secondBookID); err != nil {
				t.Fatal(err)
			}

			books := []struct {
				id               pgtype.UUID
				draft, published string
			}{
				{id: firstBookID, draft: "Draft first book", published: "Published first book"},
				{id: secondBookID, draft: "Draft sibling book", published: "Published sibling book"},
			}
			chapters := []struct {
				id, bookID        pgtype.UUID
				draft, published  string
				draftRevision     uint64
				publishedRevision uint64
				draftOrder        int
				publishedOrder    int
			}{
				{id: firstChapterID, bookID: firstBookID, draft: "Draft first chapter", published: "Published first chapter", draftRevision: 2, publishedRevision: 2, draftOrder: 2, publishedOrder: 1},
				{id: secondChapterID, bookID: firstBookID, draft: "Draft sibling chapter", published: "Published sibling chapter", draftRevision: 7, publishedRevision: 7, draftOrder: 7, publishedOrder: 4},
				{id: secondBookChapterID, bookID: secondBookID, draft: "Draft sibling book chapter", published: "Published sibling book chapter", draftRevision: 8, publishedRevision: 8, draftOrder: 8, publishedOrder: 6},
			}
			if err := db.WithTx(ctx, func(tx pgx.Tx) error {
				if _, err := createDraftProjectSourceView(ctx, tx, projectID, 1); err != nil {
					return err
				}
				if _, err := createOrBumpPublishedSourceView(ctx, tx, projectID, 1); err != nil {
					return err
				}
				for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
					for _, book := range books {
						title := book.draft
						if view == governance.SourceViewPublished {
							title = book.published
						}
						if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
							ProjectID: projectID.String(), ProjectLifecycleGeneration: 1, SourceView: view,
							BookID: book.id.String(), BookLifecycleGeneration: 1, Title: title,
						}); err != nil {
							return err
						}
					}
					for _, chapter := range chapters {
						bookTitle, title := books[0].draft, chapter.draft
						revision, order := chapter.draftRevision, chapter.draftOrder
						if chapter.bookID == secondBookID {
							bookTitle = books[1].draft
						}
						if view == governance.SourceViewPublished {
							bookTitle, title = books[0].published, chapter.published
							revision, order = chapter.publishedRevision, chapter.publishedOrder
							if chapter.bookID == secondBookID {
								bookTitle = books[1].published
							}
						}
						if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
							BookSourceMember: library.BookSourceMember{
								ProjectID: projectID.String(), ProjectLifecycleGeneration: 1, SourceView: view,
								BookID: chapter.bookID.String(), BookLifecycleGeneration: 1, Title: bookTitle,
							},
							ChapterID: chapter.id.String(), ChapterLifecycleGeneration: 1,
							SourceRevision: revision, Title: title, Order: order,
						}); err != nil {
							return err
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			firstEntityID, secondEntityID := firstChapterID, secondChapterID
			retainedBookID, retainedChapterID := firstBookID, secondChapterID
			if test.kind == "book" {
				firstEntityID, secondEntityID = firstBookID, secondBookID
				retainedBookID, retainedChapterID = secondBookID, secondBookChapterID
			}
			mutateLifecycle := func(query string, args ...any) {
				t.Helper()
				if err := db.WithTx(ctx, func(tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role=replica`); err != nil {
						return err
					}
					_, err := tx.Exec(ctx, query, args...)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			mutateLifecycle(fmt.Sprintf(`
				UPDATE %ss
				SET lifecycle_generation=2,
				    deleted_at=statement_timestamp(),
				    governance_retention_started_at=statement_timestamp(),
				    purge_after=statement_timestamp() + interval '7 days',
				    purge_deadline=statement_timestamp() + interval '8 days'
				WHERE project_id=$1 AND id=ANY($2::uuid[])
			`, test.kind), projectID, []pgtype.UUID{firstEntityID, secondEntityID})

			replace := func() {
				t.Helper()
				if err := db.WithTx(ctx, func(tx pgx.Tx) error {
					retained, err := loadRetainedSourceViewsForRestore(ctx, tx, projectID)
					if err != nil {
						return err
					}
					_, err = replaceRestoredSourceProjection(ctx, tx, projectID, 1, retained)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			mutateLifecycle(fmt.Sprintf(`
				UPDATE %ss
				SET deleted_at=NULL, governance_retention_started_at=NULL,
				    purge_after=NULL, purge_deadline=NULL
				WHERE project_id=$1 AND id=$2
			`, test.kind), projectID, firstEntityID)
			replace()

			assertProjection := func(bookGeneration, chapterGeneration int64) {
				t.Helper()
				rows, err := db.Pool.Query(ctx, `
					SELECT book.source_view, book.title, chapter.title, chapter.chapter_order,
					       chapter.source_revision, book.book_lifecycle_generation,
					       chapter.chapter_lifecycle_generation
					FROM book_source_members AS book
					JOIN chapter_source_members AS chapter
					  ON chapter.project_id=book.project_id AND chapter.source_view=book.source_view
					 AND chapter.project_lifecycle_generation=book.project_lifecycle_generation
					 AND chapter.book_id=book.book_id AND chapter.book_lifecycle_generation=book.book_lifecycle_generation
					WHERE book.project_id=$1 AND book.book_id=$2 AND chapter.chapter_id=$3
					ORDER BY book.source_view
				`, projectID, retainedBookID, retainedChapterID)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				got := make(map[string]struct {
					bookTitle, chapterTitle string
					order                   int32
					revision                int64
				})
				for rows.Next() {
					var view string
					var row struct {
						bookTitle, chapterTitle string
						order                   int32
						revision                int64
					}
					var gotBookGeneration, gotChapterGeneration int64
					if err := rows.Scan(&view, &row.bookTitle, &row.chapterTitle, &row.order, &row.revision, &gotBookGeneration, &gotChapterGeneration); err != nil {
						t.Fatal(err)
					}
					if gotBookGeneration != bookGeneration || gotChapterGeneration != chapterGeneration {
						t.Fatalf("%s generations = %d/%d, want %d/%d", view, gotBookGeneration, gotChapterGeneration, bookGeneration, chapterGeneration)
					}
					got[view] = row
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				wantDraftBook, wantPublishedBook := "Draft first book", "Published first book"
				wantDraftChapter, wantPublishedChapter := "Draft sibling chapter", "Published sibling chapter"
				wantDraftOrder, wantPublishedOrder := int32(7), int32(4)
				wantDraftRevision, wantPublishedRevision := int64(7), int64(7)
				if test.kind == "book" {
					wantDraftBook, wantPublishedBook = "Draft sibling book", "Published sibling book"
					wantDraftChapter, wantPublishedChapter = "Draft sibling book chapter", "Published sibling book chapter"
					wantDraftOrder, wantPublishedOrder = 8, 6
					wantDraftRevision, wantPublishedRevision = 8, 8
				}
				want := map[string]struct {
					bookTitle, chapterTitle string
					order                   int32
					revision                int64
				}{
					"draft":     {wantDraftBook, wantDraftChapter, wantDraftOrder, wantDraftRevision},
					"published": {wantPublishedBook, wantPublishedChapter, wantPublishedOrder, wantPublishedRevision},
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("retained projection = %#v, want %#v", got, want)
				}
			}

			assertProjection(1, 1)
			mutateLifecycle(fmt.Sprintf(`
				UPDATE %ss
				SET deleted_at=NULL, governance_retention_started_at=NULL,
				    purge_after=NULL, purge_deadline=NULL
				WHERE project_id=$1 AND id=$2
			`, test.kind), projectID, secondEntityID)
			replace()
			if test.kind == "book" {
				assertProjection(2, 1)
			} else {
				assertProjection(1, 2)
			}
		})
	}
}

func assertSourceCatalogPublishedOrdersForTest(t *testing.T, db *storedb.DB, projectID pgtype.UUID, want []int32) {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `
		SELECT chapter_order
		FROM chapter_source_members
		WHERE project_id=$1 AND source_view='published'
		ORDER BY chapter_order, chapter_id
	`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make([]int32, 0, len(want))
	for rows.Next() {
		var order int32
		if err := rows.Scan(&order); err != nil {
			t.Fatal(err)
		}
		got = append(got, order)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("published orders = %#v, want %#v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("published orders = %#v, want %#v", got, want)
		}
	}
}

func seedSourceCatalogSourcesForTest(t *testing.T, db *storedb.DB, base int) (pgtype.UUID, pgtype.UUID, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	userID := indexingStoreUUIDForTest(base + 1)
	projectID := indexingStoreUUIDForTest(base + 2)
	bookID := indexingStoreUUIDForTest(base + 3)
	emptyBookID := indexingStoreUUIDForTest(base + 4)
	chapterID := indexingStoreUUIDForTest(base + 5)
	paragraphID := indexingStoreUUIDForTest(base + 6)
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id,email,display_name,password_hash) VALUES ($1,$2,'Catalog','hash')`, userID, "catalog-"+userID+"@example.com"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO projects (id,owner_user_id,name) VALUES ($1,$2,'Catalog project')`, projectID, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO books (id,project_id,title,"order",display_number) VALUES
			  ($1,$3,'Book',0,'1'), ($2,$3,'Empty book',1,'2')
		`, bookID, emptyBookID, projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO chapters (id,project_id,book_id,title,"order",revision)
			VALUES ($1,$2,$3,'Chapter',0,2)
		`, chapterID, projectID, bookID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO chapter_paragraphs (
			  id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count
			) VALUES
			  ($1,$2,1,'draft','paragraph',0,'revision one','revision one',2),
			  ($1,$2,2,'draft','paragraph',0,'revision two','revision two',2)
		`, paragraphID, chapterID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuidValueForSourceCatalogTest(t, projectID), uuidValueForSourceCatalogTest(t, bookID), uuidValueForSourceCatalogTest(t, emptyBookID), uuidValueForSourceCatalogTest(t, chapterID)
}

func currentSourceCatalogBookCountForTest(t *testing.T, db *storedb.DB, projectID pgtype.UUID, view governance.SourceView) int {
	t.Helper()
	rows, err := sqlc.New(db.Pool).ListCurrentBookSourceMembers(context.Background(), sqlc.ListCurrentBookSourceMembersParams{
		ProjectID: projectID, SourceView: string(view),
	})
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func currentSourceCatalogChapterCountForTest(t *testing.T, db *storedb.DB, projectID pgtype.UUID, view governance.SourceView) int {
	t.Helper()
	rows, err := sqlc.New(db.Pool).ListCurrentChapterSourceMembers(context.Background(), sqlc.ListCurrentChapterSourceMembersParams{
		ProjectID: projectID, SourceView: string(view),
	})
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func uuidValueForSourceCatalogTest(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := storedb.ParseUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
