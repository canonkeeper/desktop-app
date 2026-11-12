package sagadraft

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/sagadraft"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

type draftProject struct {
	db       *storedb.DB
	store    *Repository
	library  *librarystore.Store
	project  string
	owner    service.Write
	editor   service.Write
	viewer   service.Write
	outsider service.Write
}

func seedDraftProject(t *testing.T, database string) draftProject {
	t.Helper()
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, database)
	accounts := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	register := func(email string) (string, auth.SessionWriteProof) {
		user, err := accounts.CreateUser(ctx, auth.NewUser{Email: email, DisplayName: email})
		if err != nil {
			t.Fatal(err)
		}
		session, csrfToken, err := accounts.CreateSession(ctx, auth.NewSession{UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		_, proof, err := accounts.AuthenticateWrite(ctx, session.CookieValue, csrfToken)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `UPDATE users SET email_verified_at = clock_timestamp() WHERE id = $1`, user.ID); err != nil {
			t.Fatal(err)
		}
		return user.ID, proof
	}
	ownerID, ownerProof := register("owner@example.test")
	editorID, editorProof := register("editor@example.test")
	viewerID, viewerProof := register("viewer@example.test")
	outsiderID, outsiderProof := register("outsider@example.test")
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
	write := func(user string, proof auth.SessionWriteProof) service.Write {
		return service.Write{Actor: service.Actor{UserID: user, ProjectID: project.ID}, Proof: proof}
	}
	libraries := librarystore.New(db)
	return draftProject{
		db: db, store: New(db, libraries, nil), library: libraries, project: project.ID,
		owner: write(ownerID, ownerProof), editor: write(editorID, editorProof),
		viewer: write(viewerID, viewerProof), outsider: write(outsiderID, outsiderProof),
	}
}

func replayOf(key, request string) service.Replay {
	return service.Replay{ScopeHash: "scope", OperationID: "chapter_draft.test", KeyHash: key, RequestHash: request, At: time.Now().UTC()}
}

func (fixture draftProject) create(t *testing.T, kind domain.Kind, text string) domain.Draft {
	t.Helper()
	draft, err := fixture.store.CreateChapterDraft(context.Background(), service.CreateCommand{
		Write: fixture.editor, Replay: replayOf("create-"+text+string(kind), "create-"+text), Title: "Глава", Kind: kind, Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func (fixture draftProject) get(t *testing.T, id string) domain.Draft {
	t.Helper()
	draft, err := fixture.store.GetChapterDraft(context.Background(), fixture.viewer.Actor, id)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func (fixture draftProject) book(t *testing.T, id, title string) {
	t.Helper()
	if _, err := fixture.library.CreateBook(context.Background(), library.CreateBookStoreInput{
		ID: id, ProjectID: fixture.project, Title: title, Order: 1, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// chapterIn adds a chapter straight through the library.
func (fixture draftProject) chapterIn(t *testing.T, book, after, title string) string {
	t.Helper()
	var id string
	if err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		id, err = fixture.library.AddChapterInTx(context.Background(), tx, library.AddChapterInput{
			ProjectID: fixture.project, BookID: book, AfterChapterID: after, Title: title, Paragraphs: []string{"абзац"}, At: time.Now().UTC(),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// specs/2026-11-01-writer-assistant-index.md, R7.1: an editor writes, a reader only reads, someone outside the
// project does not learn it exists.
func TestChapterDraftRightsPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_rights")
	ctx := context.Background()
	store := fixture.store
	draft := fixture.create(t, domain.KindSketch, "набросок")
	if draft.Revision != 1 || draft.Stage() != domain.StageText || draft.Text != "набросок" {
		t.Fatalf("created = %+v", draft)
	}
	if _, err := store.CreateChapterDraft(ctx, service.CreateCommand{Write: fixture.viewer, Replay: replayOf("r", "r"), Kind: domain.KindSketch}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader creates: %v", err)
	}
	title := "нельзя"
	if _, err := store.UpdateChapterDraft(ctx, fixture.viewer, draft.ID, 1, domain.Change{Title: &title}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader edits: %v", err)
	}
	if err := store.DeleteChapterDraft(ctx, fixture.viewer, draft.ID, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader removes: %v", err)
	}
	if _, err := store.AddChapterDraftToBook(ctx, fixture.viewer, draft.ID, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader adds: %v", err)
	}
	page, err := store.ListChapterDrafts(ctx, fixture.viewer.Actor, nil, 10, "")
	if err != nil || len(page.Drafts) != 1 || page.Total != 1 || page.Drafts[0].ID != draft.ID || page.Drafts[0].Stage != domain.StageText {
		t.Fatalf("a reader lists: %+v, %v", page, err)
	}
	if read := fixture.get(t, draft.ID); read.Text != "набросок" {
		t.Fatalf("a reader reads: %+v", read)
	}
	if _, err := store.ListChapterDrafts(ctx, fixture.outsider.Actor, nil, 10, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an outsider lists: %v", err)
	}
	if _, err := store.GetChapterDraft(ctx, fixture.outsider.Actor, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an outsider reads: %v", err)
	}
	if err := store.DeleteChapterDraft(ctx, fixture.outsider, draft.ID, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an outsider removes: %v", err)
	}
	if _, err := store.GetChapterDraft(ctx, fixture.viewer.Actor, "not-a-uuid"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a bad id: %v", err)
	}
}

// R7.1: a repeat of a creation answers the first and makes nothing; another request under the key is refused.
func TestChapterDraftCreationIsReplayedPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_replay")
	ctx := context.Background()
	command := service.CreateCommand{Write: fixture.editor, Replay: replayOf("key", "request"), Title: "Глава", Kind: domain.KindSketch, Text: "набросок"}
	first, err := fixture.store.CreateChapterDraft(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	again, err := fixture.store.CreateChapterDraft(ctx, command)
	if err != nil || again.ID != first.ID {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	command.RequestHash = "other"
	if _, err := fixture.store.CreateChapterDraft(ctx, command); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("another request under the key: %v", err)
	}
	page, err := fixture.store.ListChapterDrafts(ctx, fixture.editor.Actor, nil, 10, "")
	if err != nil || page.Total != 1 {
		t.Fatalf("drafts = %+v, %v", page, err)
	}
}

// R7.2, W2.1: an edit goes at the revision the page saw; the author's idea is kept as he wrote it; a place
// must stand in the saga.
func TestChapterDraftEditsGoAtTheirRevisionPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_revision")
	ctx := context.Background()
	draft := fixture.create(t, domain.KindSketch, "встреча")
	text := "встреча и уход"
	updated, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Text: &text})
	if err != nil || updated.Revision != 2 || updated.Text != text {
		t.Fatalf("edit = %+v, %v", updated, err)
	}
	if _, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, 1, domain.Change{Text: &text}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale edit: %v", err)
	}
	idea := "  Кира узнаёт правду\n"
	updated, err = fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, 2, domain.Change{Idea: &idea})
	if err != nil || updated.Idea != idea || updated.Stage() != domain.StageText {
		t.Fatalf("idea edit = %+v, %v", updated, err)
	}
	missing := domain.Place{BookID: "00000000-0000-4000-8000-00000000abcd"}
	if _, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, 3, domain.Change{Place: &missing}); !errors.Is(err, domain.ErrNoDestination) {
		t.Fatalf("a place outside the saga: %v", err)
	}
	book := "00000000-0000-4000-8000-00000000a001"
	fixture.book(t, book, "Том")
	placed, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, 3, domain.Change{Place: &domain.Place{BookID: book}})
	if err != nil || placed.Place.BookID != book || placed.BookTitle != "Том" {
		t.Fatalf("a place = %+v, %v", placed, err)
	}
	cleared, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, 4, domain.Change{Place: &domain.Place{}})
	if err != nil || cleared.Place.BookID != "" {
		t.Fatalf("a cleared place = %+v, %v", cleared, err)
	}
	if err := fixture.store.DeleteChapterDraft(ctx, fixture.editor, draft.ID, 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale removal: %v", err)
	}
	if err := fixture.store.DeleteChapterDraft(ctx, fixture.editor, draft.ID, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.GetChapterDraft(ctx, fixture.viewer.Actor, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a removed draft: %v", err)
	}
}

func usedToday(t *testing.T, db *storedb.DB, user, column string) int {
	t.Helper()
	var used int
	err := db.Pool.QueryRow(context.Background(), `SELECT coalesce(sum(`+column+`), 0) FROM writer_usage_days WHERE user_id=$1`, user).Scan(&used)
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func bookChapters(t *testing.T, db *storedb.DB, book string) []string {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `
		SELECT title FROM chapters WHERE book_id = $1::uuid AND deleted_at IS NULL ORDER BY "order", id
	`, book)
	if err != nil {
		t.Fatal(err)
	}
	titles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return titles
}

// R7.5: the chapter goes exactly after the chapter named, in both text versions with its text-index intents;
// the chapters around it keep their text and revision, no import is spent, and a repeat adds nothing.
func TestAddToBookMakesOneChapterInItsPlacePostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_add")
	ctx := context.Background()
	book := "00000000-0000-4000-8000-00000000a004"
	fixture.book(t, book, "Том")
	first := fixture.chapterIn(t, book, "", "Первая")
	second := fixture.chapterIn(t, book, first, "Вторая")
	third := fixture.chapterIn(t, book, second, "Третья")
	importsBefore := usedToday(t, fixture.db, fixture.editor.UserID, "imports")

	draft := fixture.create(t, domain.KindSketch, "")
	if _, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, draft.Revision); !errors.Is(err, domain.ErrNoDestination) {
		t.Fatalf("an add without a place: %v", err)
	}
	placed, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Place: &domain.Place{BookID: book, AfterChapterID: second}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, placed.Revision); !errors.Is(err, domain.ErrNoText) {
		t.Fatalf("an add without a text: %v", err)
	}
	body := "Первый абзац главы.\n\n― Реплика.\nТретий абзац."
	written, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, placed.Revision, domain.Change{Text: &body})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, placed.Revision); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("an add at a stale revision: %v", err)
	}
	var thirdRevision int64
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT revision FROM chapters WHERE id=$1::uuid`, third).Scan(&thirdRevision); err != nil {
		t.Fatal(err)
	}
	added, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, written.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if added.AddedChapterID == "" || added.AddedChapterTitle != "Глава" || added.Stage() != domain.StageAdded {
		t.Fatalf("added = %+v", added)
	}
	if titles := bookChapters(t, fixture.db, book); strings.Join(titles, ",") != "Первая,Вторая,Глава,Третья" {
		t.Fatalf("chapters = %v", titles)
	}
	repeat, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, written.Revision)
	if err != nil || repeat.AddedChapterID != added.AddedChapterID {
		t.Fatalf("a repeat = %+v, %v", repeat, err)
	}
	if titles := bookChapters(t, fixture.db, book); len(titles) != 4 {
		t.Fatalf("a repeat added a chapter: %v", titles)
	}
	var revision, members, paragraphs, intents int
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT revision FROM chapters WHERE id=$1::uuid`, third).Scan(&revision); err != nil || int64(revision) != thirdRevision {
		t.Fatalf("the chapter after it moved its revision: %d, %v", revision, err)
	}
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT count(*) FROM chapter_source_members WHERE chapter_id=$1::uuid`, added.AddedChapterID).Scan(&members); err != nil || members != 2 {
		t.Fatalf("members of both text versions = %d, %v", members, err)
	}
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT count(*) FROM chapter_paragraphs WHERE chapter_id=$1::uuid AND variant='published'`, added.AddedChapterID).Scan(&paragraphs); err != nil || paragraphs != 3 {
		t.Fatalf("paragraphs = %d, %v", paragraphs, err)
	}
	if err := fixture.db.Pool.QueryRow(ctx, `
		SELECT count(DISTINCT subject_json->>'sourceView') FROM jobs
		WHERE project_id=$1::uuid AND kind='canon_text_index' AND status='queued'
	`, fixture.project).Scan(&intents); err != nil || intents != 2 {
		t.Fatalf("text-index intents of the versions = %d, %v", intents, err)
	}
	if used := usedToday(t, fixture.db, fixture.editor.UserID, "imports"); used != importsBefore {
		t.Fatalf("an add spent an import: %d", used)
	}
	// Once in the book a draft is closed: the chapter is changed there.
	text := "поздно"
	if _, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, added.Revision, domain.Change{Text: &text}); !errors.Is(err, domain.ErrAdded) {
		t.Fatalf("an edit after the add: %v", err)
	}
	// At the start of the book without a chapter named; a chapter of the book it no longer stands in is refused.
	start := fixture.create(t, domain.KindSketch, "начало")
	placed, err = fixture.store.UpdateChapterDraft(ctx, fixture.editor, start.ID, start.Revision, domain.Change{Place: &domain.Place{BookID: book}, Text: &body})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, start.ID, placed.Revision); err != nil {
		t.Fatal(err)
	}
	if titles := bookChapters(t, fixture.db, book); titles[0] != "Глава" || len(titles) != 5 {
		t.Fatalf("chapters = %v", titles)
	}
}

// specs/2026-11-04-writing-plain-path.md P2.3: the book takes the working text, written or not, without the marks.
func TestTheBookTakesTheWorkingTextPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_add_working")
	ctx := context.Background()
	book := "00000000-0000-4000-8000-00000000a009"
	fixture.book(t, book, "Том")
	draft := fixture.create(t, domain.KindSketch, "Первый абзац автора.\n\n⟦Абзац помощника.⟧")
	placed, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Place: &domain.Place{BookID: book}})
	if err != nil {
		t.Fatal(err)
	}
	added, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, placed.Revision)
	if err != nil || added.AddedChapterID == "" {
		t.Fatalf("added = %+v, %v", added, err)
	}
	var paragraphs []string
	rows, err := fixture.db.Pool.Query(ctx, `SELECT plain_text FROM chapter_paragraphs WHERE chapter_id=$1::uuid AND variant='published' ORDER BY "order"`, added.AddedChapterID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		paragraphs = append(paragraphs, text)
	}
	if len(paragraphs) != 2 || paragraphs[0] != "Первый абзац автора." || paragraphs[1] != "Абзац помощника." {
		t.Fatalf("the chapter of the book = %q", paragraphs)
	}
}

// R7.5: the limits of the text hold for a chapter added by hand; over them nothing stays.
func TestAddToBookStopsAtTheLimitsOfTheTextPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_add_quota")
	ctx := context.Background()
	book := "00000000-0000-4000-8000-00000000a005"
	fixture.book(t, book, "Том")
	body := "Абзац."
	draft := fixture.create(t, domain.KindSketch, "набросок")
	placed, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Place: &domain.Place{BookID: book}, Text: &body})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `
		INSERT INTO writer_quota_overrides (user_id, project_chapters, updated_at) VALUES ($1, 0, clock_timestamp())
	`, fixture.owner.UserID); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, placed.Revision)
	if exceeded, ok := quota.AsExceeded(err); !ok || exceeded.Key != quota.KeyProjectChapters {
		t.Fatalf("over the limit: %v", err)
	}
	if titles := bookChapters(t, fixture.db, book); len(titles) != 0 {
		t.Fatalf("a refused add left %v", titles)
	}
	if read := fixture.get(t, draft.ID); read.AddedChapterID != "" {
		t.Fatalf("a refused add marked the draft: %+v", read)
	}
}

// Owner 03.11: the list says how many words each draft has — of its working text; a dash of a line is no word.
func TestTheListCountsTheWordsOfEachDraftPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_list_words")
	draft := fixture.create(t, domain.KindSketch, "Кайл вошёл.\n― Опоздал, ― сказал Тарн.")
	page, err := fixture.store.ListChapterDrafts(context.Background(), fixture.editor.Actor, nil, 10, "")
	if err != nil || len(page.Drafts) != 1 || page.Drafts[0].Words != 5 {
		t.Fatalf("the list = %+v, %v", page.Drafts, err)
	}
	chapter := "Кайл вошёл в кузницу и сел."
	if _, err := fixture.store.UpdateChapterDraft(context.Background(), fixture.editor, draft.ID, draft.Revision, domain.Change{Text: &chapter}); err != nil {
		t.Fatal(err)
	}
	if page, err = fixture.store.ListChapterDrafts(context.Background(), fixture.editor.Actor, nil, 10, ""); err != nil || page.Drafts[0].Words != 6 {
		t.Fatalf("the list after an edit = %+v, %v", page.Drafts, err)
	}
	// specs/2026-11-08-italic-bold-in-text.md TF1.4: a mark of the author's italic and bold is no word of its own.
	edited, err := fixture.store.ListChapterDrafts(context.Background(), fixture.editor.Actor, nil, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	faced := "Кайл ⟨ вошёл⟩ в ⟪кузницу⟫ ― и сел."
	if _, err := fixture.store.UpdateChapterDraft(context.Background(), fixture.editor, draft.ID, edited.Drafts[0].Revision, domain.Change{Text: &faced}); err != nil {
		t.Fatal(err)
	}
	if page, err = fixture.store.ListChapterDrafts(context.Background(), fixture.editor.Actor, nil, 10, ""); err != nil || page.Drafts[0].Words != 6 {
		t.Fatalf("the list of a text with faces = %+v, %v", page.Drafts, err)
	}
}

// specs/2026-11-12-ux12-writing.md WR2.1: the list says the words of a draft as the head of its chapter does — by the rule of
// the shared table testdata/words.json («all»: runs of letters, marks and digits; «по-русски» is two words, a dash none,
// a mark of the helper parts a word, a mark of a face does not) — and so for every sign of the scripts and the punctuation
// an author writes with, sign by sign against the rule of the domain, still reading no text of a draft into the server.
func TestTheListCountsTheWordsAsTheHeadOfTheChapterPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_list_all_words")
	ctx := context.Background()
	draft := fixture.create(t, domain.KindSketch, "")
	listed := func(text string) int {
		t.Helper()
		page, err := fixture.store.ListChapterDrafts(ctx, fixture.editor.Actor, nil, 10, "")
		if err != nil || len(page.Drafts) != 1 {
			t.Fatalf("the list = %+v, %v", page.Drafts, err)
		}
		if _, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, page.Drafts[0].Revision, domain.Change{Text: &text}); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if page, err = fixture.store.ListChapterDrafts(ctx, fixture.editor.Actor, nil, 10, ""); err != nil {
			t.Fatal(err)
		}
		return page.Drafts[0].Words
	}
	raw, err := os.ReadFile("../../../domain/sagadraft/testdata/words.json")
	if err != nil {
		t.Fatal(err)
	}
	var table []struct {
		Text  string `json:"text"`
		Valid bool   `json:"valid"`
		All   int    `json:"all"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	for _, test := range table {
		if test.Valid && test.Text != "" {
			if got := listed(test.Text); got != test.All {
				t.Fatalf("the list counts %q as %d words, the head of the chapter %d", test.Text, got, test.All)
			}
		}
	}
	// Every sign of the Latin, Greek and Cyrillic letters, the marks over them, the digits, the punctuation, the symbols and
	// the number forms, alone between spaces: a word of the domain is a word of the list. The circled letters U+24B6–U+24E9
	// are symbols to the domain and letters to PostgreSQL, the one difference of these blocks (Decision log).
	var signs []rune
	for _, block := range [][2]rune{{0x21, 0x52f}, {0x1ab0, 0x1aff}, {0x1d00, 0x1fff}, {0x2000, 0x27e5}, {0x27ec, 0x2bff}, {0x2de0, 0x2dff},
		{0xa640, 0xa69f}, {0xfe00, 0xfe2f}, {0xff00, 0xffef}} {
		for sign := block[0]; sign <= block[1]; sign++ {
			if sign >= 0x24b6 && sign <= 0x24e9 || !unicode.IsGraphic(sign) && !unicode.Is(unicode.Mn, sign) || unicode.IsSpace(sign) {
				continue
			}
			signs = append(signs, sign)
		}
	}
	for from := 0; from < len(signs); from += 200 {
		part := signs[from:min(from+200, len(signs))]
		var text strings.Builder
		for _, sign := range part {
			text.WriteString(" " + string(sign))
		}
		if got, want := listed(text.String()), len(domain.WordsOf(text.String())); got != want {
			t.Fatalf("U+%04X–U+%04X: the list counts %d words, the domain %d", part[0], part[len(part)-1], got, want)
		}
	}
}
