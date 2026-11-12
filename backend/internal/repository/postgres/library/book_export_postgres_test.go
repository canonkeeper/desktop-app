package library

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/textface"
	libraryservice "canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/library"
	authstore "canonkeeper/backend/internal/store/auth"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

type noCover struct{}

func (noCover) Execute(context.Context, domainlibrary.CoverLookup) (service.CoverContent, error) {
	return service.CoverContent{}, errors.New("the book of this test has no cover")
}

// specs/2026-11-05-book-export.md E1.1, E1.5, E1.7: a stranger does not see a book, a reader sees it without the right to
// take it away, the chapters go in the order of the book, a deleted one is not in the file, and what is stored comes
// back through the import word for word.
func TestBookExportReadsThePublishedTextPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_book_export")
	ctx := context.Background()
	accounts := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	register := func(email, name string) string {
		user, err := accounts.CreateUser(ctx, auth.NewUser{Email: email, DisplayName: name})
		if err != nil {
			t.Fatal(err)
		}
		return user.ID
	}
	ownerID := register("owner@example.test", "Игорь Вельский")
	editorID := register("editor@example.test", "Редактор")
	viewerID := register("viewer@example.test", "Читатель")
	outsiderID := register("outsider@example.test", "Чужой")
	project, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, ownerID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	for member, role := range map[string]string{editorID: "editor", viewerID: "viewer"} {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO project_memberships (id, project_id, user_id, role, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, $2, $3, clock_timestamp(), clock_timestamp())
		`, project.ID, member, role); err != nil {
			t.Fatal(err)
		}
	}
	const bookID = "00000000-0000-4000-8000-000000007201"
	store := librarystore.New(db)
	if _, err := store.CreateBook(ctx, libraryservice.CreateBookStoreInput{
		ID: bookID, ProjectID: project.ID, Title: `Том «Порт» & другие`, Order: 1, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	repository := New(db)
	export := service.NewExportBook(repository, noCover{}, time.Now)
	input := func(user string) service.ExportBookInput {
		return service.ExportBookInput{Actor: domainlibrary.Actor{UserID: user}, BookID: bookID, Format: "docx"}
	}

	if _, err := export.Execute(ctx, input(outsiderID)); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("a stranger: %v", err)
	}
	if _, err := export.Execute(ctx, input(viewerID)); !errors.Is(err, domainlibrary.ErrForbidden) {
		t.Fatalf("a reader: %v", err)
	}
	if chapters, err := repository.BookExportChapters(ctx, project.ID, bookID); err != nil || len(chapters) != 0 {
		t.Fatalf("a book without text: %+v, %v", chapters, err)
	}

	after := ""
	ids := []string{}
	for _, chapter := range []struct {
		title      string
		paragraphs []string
	}{
		{"Первая", []string{`«Стой!» — *крикнул* **Кир** & Мила.`, `Два \* три.`}}, {"Удалённая", []string{"Её нет в файле."}}, {"Последняя", []string{"Конец."}},
	} {
		if err := db.WithTx(ctx, func(tx pgx.Tx) error {
			var err error
			after, err = store.AddChapterInTx(ctx, tx, libraryservice.AddChapterInput{
				ProjectID: project.ID, BookID: bookID, AfterChapterID: after, Title: chapter.title, Paragraphs: chapter.paragraphs, At: time.Now().UTC(),
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, after)
	}
	before, err := repository.BookExportChapters(ctx, project.ID, bookID)
	if err != nil || len(before) != 3 {
		t.Fatalf("chapters = %+v, %v", before, err)
	}
	if _, err := store.FenceChapterDeletion(ctx, libraryservice.FenceChapterDeletionInput{
		ActorUserID: ownerID, ProjectID: project.ID, BookID: bookID, ChapterID: ids[1],
		TombstoneID: "00000000-0000-4000-8000-000000007202", DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// A chapter deleted after the list has no text any more: the use case stops the file on it.
	if gone, err := repository.BookExportParagraphs(ctx, project.ID, bookID, before[1]); err != nil || len(gone) != 0 {
		t.Fatalf("a deleted chapter = %q, %v", gone, err)
	}

	head, err := repository.BookHead(ctx, domainlibrary.Actor{UserID: editorID}, bookID)
	if err != nil || !head.CanEdit || head.AuthorName != "Игорь Вельский" || head.CoverVersion != 0 {
		t.Fatalf("the head for an editor = %+v, %v", head, err)
	}
	file, err := export.Execute(ctx, input(editorID))
	if err != nil {
		t.Fatal(err)
	}
	var written bytes.Buffer
	if err := file.Write(&written); err != nil {
		t.Fatal(err)
	}
	back, err := libraryservice.ParseDOCX(bytes.NewReader(written.Bytes()), int64(written.Len()))
	if err != nil {
		t.Fatal(err)
	}
	// The italic and bold of the book come back with its words (specs/2026-11-08-italic-bold-in-text.md TF7.1).
	want := []string{"Первая|«Стой!» — *крикнул* **Кир** & Мила.", `Первая|Два \* три.`, "Последняя|Конец."}
	var got []string
	for _, chapter := range back.Chapters {
		for _, paragraph := range chapter.Paragraphs {
			got = append(got, chapter.Title+"|"+textface.Markdown(paragraph.Runs))
		}
	}
	if back.Title != `Том «Порт» & другие` || !reflect.DeepEqual(got, want) {
		t.Fatalf("back = %q %q", back.Title, got)
	}

	if _, err := store.FenceBookDeletion(ctx, libraryservice.FenceBookDeletionInput{
		ActorUserID: ownerID, ProjectID: project.ID, BookID: bookID,
		TombstoneID: "00000000-0000-4000-8000-000000007203", DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := export.Execute(ctx, input(ownerID)); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("a deleted book: %v", err)
	}
}

// specs/2026-11-12-ux10-book-pictures.md BP1.1, BP3.2, BP2.5: the chapters of every living book of a saga in the order
// of the shelf, each with its book and the number of all its paragraphs — as many as its text gives the file —; only
// an editor and above count the drawings of the files, a stranger does not see the saga.
func TestTheChaptersOfASagaForItsDrawingsPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_book_drawings")
	ctx := context.Background()
	accounts := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	register := func(email, name string) string {
		user, err := accounts.CreateUser(ctx, auth.NewUser{Email: email, DisplayName: name})
		if err != nil {
			t.Fatal(err)
		}
		return user.ID
	}
	ownerID := register("owner@example.test", "Автор")
	viewerID := register("viewer@example.test", "Читатель")
	outsiderID := register("outsider@example.test", "Чужой")
	project, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, ownerID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO project_memberships (id, project_id, user_id, role, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'viewer', clock_timestamp(), clock_timestamp())
	`, project.ID, viewerID); err != nil {
		t.Fatal(err)
	}
	store := librarystore.New(db)
	books := []string{"00000000-0000-4000-8000-000000007301", "00000000-0000-4000-8000-000000007302", "00000000-0000-4000-8000-000000007303"}
	// The books go one after another, their chapters numbered from one in each; the third is deleted.
	for index := range books {
		if _, err := store.CreateBook(ctx, libraryservice.CreateBookStoreInput{
			ID: books[index], ProjectID: project.ID, Title: "Том", Order: index + 1, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		after := ""
		for _, paragraphs := range [][]string{{"Один.", "Два."}, {"Три.", "Четыре.", "Пять."}} {
			if err := db.WithTx(ctx, func(tx pgx.Tx) error {
				after, err = store.AddChapterInTx(ctx, tx, libraryservice.AddChapterInput{
					ProjectID: project.ID, BookID: books[index], AfterChapterID: after, Title: "Глава", Paragraphs: paragraphs, At: time.Now().UTC(),
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := store.FenceBookDeletion(ctx, libraryservice.FenceBookDeletionInput{
		ActorUserID: ownerID, ProjectID: project.ID, BookID: books[2],
		TombstoneID: "00000000-0000-4000-8000-000000007304", DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	repository := New(db)
	chapters, err := repository.ProjectExportChapters(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, chapter := range chapters {
		paragraphs, err := repository.BookExportParagraphs(ctx, project.ID, chapter.BookID, chapter)
		if err != nil || len(paragraphs) != chapter.Paragraphs {
			t.Fatalf("%+v: the text has %d paragraphs, %v", chapter, len(paragraphs), err)
		}
		got = append(got, chapter.BookID[len(chapter.BookID)-4:]+"·"+strconv.Itoa(chapter.Paragraphs))
	}
	if want := []string{"7301·2", "7301·3", "7302·2", "7302·3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the chapters of the saga = %q, want %q", got, want)
	}
	one, err := repository.BookExportChapters(ctx, project.ID, books[0])
	if err != nil || len(one) != 2 || one[0].BookID != books[0] || one[1].Paragraphs != 3 {
		t.Fatalf("the chapters of a book = %+v, %v", one, err)
	}
	for _, check := range []struct {
		user    string
		canEdit bool
		err     error
	}{{ownerID, true, nil}, {viewerID, false, nil}, {outsiderID, false, domainlibrary.ErrNotFound}} {
		if canEdit, err := repository.ProjectCanEdit(ctx, domainlibrary.Actor{UserID: check.user}, project.ID); canEdit != check.canEdit || !errors.Is(err, check.err) {
			t.Fatalf("%s: %v, %v", check.user, canEdit, err)
		}
	}
}
