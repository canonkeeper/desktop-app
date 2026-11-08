package jobstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
	"canonkeeper/backend/internal/testutil"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The release of the writing agent (specs/2026-11-06-writing-agent.md, WA13, slice S8) reads every Job row the earlier
// releases left, by the path of listProjectJobs and getJob: one row the decoder does not know makes the whole list of a
// project a conflict (JobFromSQLJob turns any decode error into jobs.ErrConflict, the API into 409).

type releaseShapes struct {
	ctx       context.Context
	db        *storedb.DB
	store     *Store
	actorID   string
	projectID string
	now       time.Time
	next      int
	router    http.Handler
}

func newReleaseShapes(t *testing.T, prefix string, through int64) *releaseShapes {
	t.Helper()
	db := testutil.OpenIsolatedPostgresTestDB(t, prefix, through)
	fixture := &releaseShapes{
		ctx: context.Background(), db: db, store: New(db),
		actorID:   "00000000-0000-4000-8000-00000000f101",
		projectID: "00000000-0000-4000-8000-00000000f102",
		now:       time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
	}
	insertProjectStoreUserForTest(t, db, fixture.actorID, "release-shapes@example.test")
	insertProjectStoreProjectForTest(t, db, fixture.projectID, fixture.actorID, "Сага", fixture.now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-00000000f103", fixture.projectID, fixture.actorID, projects.RoleOwner, fixture.now, false)
	router := chi.NewRouter()
	jobs.NewHandlers(jobs.NewService(fixture.store, fixedJobsClock{fixture.now}, everyTargetReadable{}), shapesViewer{fixture.actorID}, nil).Mount(router)
	fixture.router = router
	return fixture
}

// row makes a readable Job of the project, owned by the project as the store owns it, and gives it the stored payload
// of an earlier release. An import keeps its progress contract; any other Job keeps the progress its own creation
// writes ('{}', a number only for the indexing Jobs), so a row is not read back before it has its payload.
func (fixture *releaseShapes) row(t *testing.T, kind jobs.Kind, status jobs.Status, subject jobs.Subject, stored string) string {
	t.Helper()
	fixture.next++
	id := fmt.Sprintf("00000000-0000-4000-8000-0000000f%04d", fixture.next)
	at := fixture.now.Add(time.Duration(fixture.next) * time.Minute)
	queue := map[jobs.Kind]jobs.QueueClass{
		jobs.KindChapterCards: jobs.QueueClassAutomatic, jobs.KindSagaLayer: jobs.QueueClassAutomatic,
		jobs.KindChapterDraft: jobs.QueueClassInteractive, jobs.KindIllustration: jobs.QueueClassInteractive,
		jobs.KindResearchChatTurn: jobs.QueueClassInteractive,
	}[kind]
	if queue == "" {
		queue = jobs.QueueClassUserDemanded
	}
	input := jobs.CreateJobStoreInput{
		ID: id, ProjectID: fixture.projectID, Kind: kind, Status: status, Subject: subject, CreatorUserID: fixture.actorID,
		QueueClass: queue, MaxAttempts: 3, NextAttemptAt: at, CreatedAt: at, UpdatedAt: at,
	}
	if kind == jobs.KindImport {
		if _, err := fixture.store.createJobForTest(fixture.ctx, input); err != nil {
			t.Fatalf("create %s %s: %v", status, kind, err)
		}
	} else {
		fixture.create(t, input)
		if kind != jobs.KindCanonTextIndex && kind != jobs.KindChapterCards && kind != jobs.KindSagaLayer && !strings.Contains(stored, "progress_json") {
			stored = strings.TrimPrefix(stored+`, progress_json = '{}'`, ", ")
		}
	}
	if stored != "" {
		if _, err := fixture.db.Pool.Exec(fixture.ctx, `UPDATE jobs SET `+stored+` WHERE id = $1`, id); err != nil {
			t.Fatalf("give %s the stored shape: %v", kind, err)
		}
	}
	return id
}

// create inserts the Job by the query of the store under an owner of the project's current generation.
func (fixture *releaseShapes) create(t *testing.T, input jobs.CreateJobStoreInput) {
	t.Helper()
	project, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		generation, err := currentJobsProjectGeneration(fixture.ctx, tx, project)
		if err != nil {
			return err
		}
		return fixture.store.createCurrentProjectJobArtifactForTest(fixture.ctx, tx, input.ProjectID, governance.LifecycleGeneration(generation), input.ID,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				id, err := storedb.ParseUUID(input.ID)
				if err != nil {
					return err
				}
				creator, err := storedb.ParseUUID(input.CreatorUserID)
				if err != nil {
					return err
				}
				subject, err := jobs.EncodeSubject(input.Kind, input.Subject)
				if err != nil {
					return err
				}
				_, err = sqlc.New(tx).CreateJob(ctx, sqlc.CreateJobParams{
					ID: id, Kind: string(input.Kind), Status: string(input.Status), SubjectJson: subject, CreatorUserID: creator,
					QueueClass: string(input.QueueClass), MaxAttempts: int32(input.MaxAttempts), NextAttemptAt: storedb.Timestamptz(input.NextAttemptAt),
					ArtifactSourceOwnerID: ownerID, CreatedAt: storedb.Timestamptz(input.CreatedAt), UpdatedAt: storedb.Timestamptz(input.UpdatedAt),
					ProjectID: project,
				})
				return err
			})
	}); err != nil {
		t.Fatalf("create %s %s: %v", input.Status, input.Kind, err)
	}
}

// payload is a stored subject and result of an earlier release, with the project of the fixture.
func (fixture *releaseShapes) payload(subject, result string) string {
	set := `subject_json = '` + strings.ReplaceAll(subject, "P", fixture.projectID) + `'`
	if result == "" {
		return set + `, result_json = NULL`
	}
	return set + `, result_json = '` + result + `'`
}

// rows is what the jobs table keeps, to show a migration left it byte for byte.
func (fixture *releaseShapes) rows(t *testing.T) string {
	t.Helper()
	var all string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT string_agg(concat_ws('|', id, kind, status, subject_json::text, result_json::text, progress_json::text, updated_at::text), E'\n' ORDER BY id)
		FROM jobs
	`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	return all
}

func (fixture *releaseShapes) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	recorder := httptest.NewRecorder()
	fixture.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Code, recorder.Body.Bytes()
}

type shapesPage struct {
	Data []struct {
		ID      string          `json:"id"`
		Kind    string          `json:"kind"`
		Subject json.RawMessage `json:"subject"`
	} `json:"data"`
	Meta struct {
		HasMore    bool    `json:"hasMore"`
		NextCursor *string `json:"nextCursor"`
	} `json:"meta"`
}

// list reads every page of the project's Jobs by the API, limit at a time, and gives the ids in list order.
func (fixture *releaseShapes) list(t *testing.T, query string, limit int) []string {
	t.Helper()
	var ids []string
	cursor := ""
	for range 200 {
		path := fmt.Sprintf("/projects/%s/jobs?limit=%d%s%s", fixture.projectID, limit, query, cursor)
		status, body := fixture.get(t, path)
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, status, body)
		}
		var page shapesPage
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		for _, job := range page.Data {
			ids = append(ids, job.ID)
		}
		if !page.Meta.HasMore {
			return ids
		}
		cursor = "&cursor=" + *page.Meta.NextCursor
	}
	t.Fatal("the list does not end")
	return nil
}

type fixedJobsClock struct{ now time.Time }

func (clock fixedJobsClock) Now() time.Time { return clock.now }

// everyTargetReadable leaves the read check of a target out: the read of the rows is what these tests see.
type everyTargetReadable struct{}

func (everyTargetReadable) CanReadJobTarget(context.Context, jobs.Actor, jobs.Subject) (bool, error) {
	return true, nil
}

type shapesViewer struct{ id string }

func (viewer shapesViewer) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: viewer.id}, nil
}

func (shapesViewer) ValidateCSRF(context.Context, string, string) error { return nil }

// Every shape of a Job row production keeps (the keys of its subject and result, read before the release of the
// writing agent) is read by listProjectJobs — whole, by kind, active only, page by page through the cursor — and by
// getJob once 0109 and 0110 have run; a row whose source is gone stays out of the list and is not found. 0110 leaves
// production as 0109 left it, byte for byte: its earlier writing is all sealed by 0109.
func TestEveryProductionJobShapeReadsAfterTheWritingRelease(t *testing.T) {
	fixture := newReleaseShapes(t, "ck_release_shapes", 108)
	const (
		other = "00000000-0000-4000-8000-00000000f201"
		book  = "00000000-0000-4000-8000-00000000f202"
		after = "00000000-0000-4000-8000-00000000f203"
		chat  = "00000000-0000-4000-8000-00000000f204"
		turn  = "00000000-0000-4000-8000-00000000f205"
		queue = "00000000-0000-4000-8000-00000000f206"
		item  = "00000000-0000-4000-8000-00000000f207"
		image = "00000000-0000-4000-8000-00000000f208"
		layer = "00000000-0000-4000-8000-00000000f209"
		draft = "00000000-0000-4000-8000-00000000f20a"
	)
	p := fixture.projectID
	draftSubject := jobs.ChapterDraftSubject{Type: jobs.KindChapterDraft, ProjectID: p, DraftID: draft, Mode: jobs.ChapterDraftWrite, Erased: true}
	importSubject := jobs.ImportSubject{Type: jobs.KindImport, ID: p, ProjectID: p}
	chatSubject := jobs.ResearchChatTurnSubject{Type: jobs.KindResearchChatTurn, ProjectID: p, ChatID: chat, TurnID: turn, SourceView: governance.SourceViewDraft, Scope: "project"}
	imageSubject := jobs.IllustrationSubject{Type: jobs.KindIllustration, ProjectID: p, ImageID: image}
	var readable, gone []string
	keep := func(id string) { readable = append(readable, id) }

	// Each line of the snapshot of production: kind | ended | mode | keys of the subject | keys of the result.
	keep(fixture.row(t, jobs.KindAuthorMaterialsImport, jobs.StatusSucceeded,
		jobs.AuthorMaterialsImportSubject{Type: jobs.KindAuthorMaterialsImport, ProjectID: p, ImportID: other},
		fixture.payload(`{"type":"author_materials_import","projectId":"P","importId":"`+other+`"}`, `{"type":"author_materials_import","candidates":4}`)))
	keep(fixture.row(t, jobs.KindCanonTextIndex, jobs.StatusSucceeded,
		jobs.CanonTextIndexSubject{Type: jobs.KindCanonTextIndex, ProjectID: p, SourceView: governance.SourceViewDraft, CatalogRevision: 3},
		fixture.payload(`{"type":"canon_text_index","projectId":"P","sourceView":"draft","catalogRevision":3}`,
			`{"type":"canon_text_index","sourceView":"draft","textCoverage":{"currentBooks":2,"totalBooks":2,"nonCurrentBooks":0,"totalNonCurrentBooks":0,"truncated":false}}`)))
	keep(fixture.row(t, jobs.KindChapterCards, jobs.StatusSucceeded,
		jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: p, SourceView: governance.SourceViewDraft},
		fixture.payload(`{"type":"chapter_cards","projectId":"P","sourceView":"draft"}`, `{"type":"chapter_cards","sourceView":"draft","chapters":40,"built":40}`)))
	// The earlier writing as production keeps it: a finished run sealed by the release before (Sealed), and a run of a
	// deleted draft with the fixed word in place of the text. 0109 seals them all.
	writes := []string{
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusSucceeded, draftSubject, fixture.payload(
			`{"type":"chapter_draft","projectId":"P","draftId":"`+draft+`","mode":"write","sketch":"Набросок автора.","bookId":"`+book+`","afterChapterId":"`+after+`"}`,
			`{"type":"chapter_draft","mode":"write","total":3,"chapters":[{"chapterId":"`+after+`","revision":2}]}`)),
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusSucceeded, draftSubject, fixture.payload(
			`{"type":"chapter_draft","projectId":"P","draftId":"`+draft+`","mode":"write","sketch":"erased"}`,
			`{"type":"chapter_draft","mode":"write","total":2,"chapters":[{"chapterId":"`+after+`","revision":1}]}`)),
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusFailed, draftSubject, fixture.payload(
			`{"type":"chapter_draft","projectId":"P","draftId":"`+draft+`","mode":"write","sketch":"Набросок автора."}`,
			`{"type":"chapter_draft","mode":"write","total":1}`)),
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusSucceeded, draftSubject, fixture.payload(
			`{"type":"chapter_draft","projectId":"P","draftId":"`+draft+`","mode":"write","sketch":"Набросок автора.","title":"Глава 7"}`,
			`{"type":"chapter_draft","mode":"write","total":2,"chapters":[{"chapterId":"`+after+`","revision":3}]}`)),
	}
	readable = append(readable, writes...)
	keep(fixture.row(t, jobs.KindIllustration, jobs.StatusSucceeded, imageSubject, fixture.payload(
		`{"type":"illustration","projectId":"P","imageId":"`+image+`","layerId":"`+layer+`"}`, `{"type":"illustration","imageNumber":12}`)))
	keep(fixture.row(t, jobs.KindIllustration, jobs.StatusSucceeded, imageSubject, fixture.payload(
		`{"type":"illustration","projectId":"P","imageId":"`+image+`"}`, `{"type":"illustration","imageNumber":11}`)))
	keep(fixture.row(t, jobs.KindIllustration, jobs.StatusFailed, imageSubject, fixture.payload(
		`{"type":"illustration","projectId":"P","imageId":"`+image+`","layerId":"`+layer+`"}`, ``)))
	keep(fixture.row(t, jobs.KindIllustration, jobs.StatusCanceled, imageSubject, fixture.payload(
		`{"type":"illustration","projectId":"P","imageId":"`+image+`"}`, ``)))
	waiting := fixture.row(t, jobs.KindIllustration, jobs.StatusQueued, imageSubject, fixture.payload(
		`{"type":"illustration","projectId":"P","imageId":"`+image+`","layerId":"`+layer+`"}`, ``))
	keep(waiting)
	keep(fixture.row(t, jobs.KindImport, jobs.StatusSucceeded, importSubject, fixture.payload(
		`{"type":"import","id":"P","projectId":"P","expectedBookOrder":2,"importQueueId":"`+queue+`","importQueueItemId":"`+item+`"}`,
		`{"type":"import","bookId":"`+book+`","chapterCount":30,"paragraphCount":900,"importMode":"new_book","sourceFileName":"tom2.docx"}`)))
	keep(fixture.row(t, jobs.KindImport, jobs.StatusFailed, importSubject, fixture.payload(
		`{"type":"import","id":"P","projectId":"P","expectedBookOrder":3,"importQueueId":"`+queue+`","importQueueItemId":"`+item+`"}`, ``)))
	gone = append(gone, fixture.row(t, jobs.KindImport, jobs.StatusSucceeded, importSubject, purgedJobRow))
	keep(fixture.row(t, jobs.KindImport, jobs.StatusSucceeded, importSubject, fixture.payload(
		`{"type":"import","id":"P","projectId":"P","bookId":"`+book+`"}`,
		`{"type":"import","bookId":"`+book+`","chapterCount":31,"paragraphCount":930,"importMode":"update_book","sourceFileName":"tom2.docx",`+
			`"unchangedChapters":29,"changedChapters":1,"addedChapters":1,"removedChapters":0}`)))
	keep(fixture.row(t, jobs.KindResearchChatTurn, jobs.StatusSucceeded, chatSubject, fixture.payload(
		`{"type":"research_chat_turn","projectId":"P","chatId":"`+chat+`","turnId":"`+turn+`","sourceView":"draft","scope":"project"}`,
		`{"type":"research_chat_turn","chatId":"`+chat+`","turnId":"`+turn+`","response":"Ответ по саге."}`)))
	keep(fixture.row(t, jobs.KindResearchChatTurn, jobs.StatusFailed, chatSubject, fixture.payload(
		`{"type":"research_chat_turn","projectId":"P","chatId":"`+chat+`","turnId":"`+turn+`","sourceView":"draft","scope":"project"}`, ``)))
	gone = append(gone, fixture.row(t, jobs.KindResearchChatTurn, jobs.StatusSucceeded, chatSubject, purgedJobRow))
	keep(fixture.row(t, jobs.KindResearchChatTurn, jobs.StatusSucceeded, chatSubject, fixture.payload(
		`{"type":"research_chat_turn","projectId":"P","chatId":"`+chat+`","turnId":"`+turn+`","sourceView":"draft","scope":"book","bookId":"`+book+`"}`,
		`{"type":"research_chat_turn","chatId":"`+chat+`","turnId":"`+turn+`","response":"Ответ по книге."}`)))
	keep(fixture.row(t, jobs.KindResearchChatTurn, jobs.StatusCanceled, chatSubject, fixture.payload(
		`{"type":"research_chat_turn","projectId":"P","chatId":"`+chat+`","turnId":"`+turn+`","sourceView":"draft","scope":"book","bookId":"`+book+`"}`, ``)))
	keep(fixture.row(t, jobs.KindSagaLayer, jobs.StatusSucceeded,
		jobs.SagaLayerSubject{Type: jobs.KindSagaLayer, ProjectID: p, SourceView: governance.SourceViewDraft},
		fixture.payload(`{"type":"saga_layer","projectId":"P","sourceView":"draft"}`,
			`{"type":"saga_layer","sourceView":"draft","chapters":40,"threads":12,"booksBuilt":2,"unverified":1}`)))

	if err := testutil.ApplyNextMigration(t, fixture.db, 108); err != nil {
		t.Fatal(err)
	}
	sealed := fixture.rows(t)
	if err := testutil.ApplyNextMigration(t, fixture.db, 109); err != nil {
		t.Fatal(err)
	}
	if got := fixture.rows(t); got != sealed {
		t.Fatalf("0110 changed a row of production:\nbefore\n%s\nafter\n%s", sealed, got)
	}

	newest := slices.Clone(readable)
	slices.Reverse(newest)
	if got := fixture.list(t, "", 100); !slices.Equal(got, newest) {
		t.Fatalf("the list = %v, want %v", got, newest)
	}
	if got := fixture.list(t, "", 1); !slices.Equal(got, newest) {
		t.Fatalf("the list page by page = %v, want %v", got, newest)
	}
	for _, kind := range []jobs.Kind{jobs.KindImport, jobs.KindCanonTextIndex, jobs.KindChapterCards, jobs.KindSagaLayer, jobs.KindChapterDraft,
		jobs.KindAuthorMaterialsImport, jobs.KindResearchChatTurn, jobs.KindIllustration} {
		if got := fixture.list(t, "&kind="+string(kind), 2); len(got) == 0 {
			t.Fatalf("no %s Job in the list by kind", kind)
		}
	}
	if got := fixture.list(t, "&active=true", 100); !slices.Equal(got, []string{waiting}) {
		t.Fatalf("the active list = %v", got)
	}
	for _, id := range readable {
		if status, body := fixture.get(t, "/jobs/"+id); status != http.StatusOK {
			t.Fatalf("GET /jobs/%s = %d %s", id, status, body)
		}
	}
	for _, id := range gone {
		if status, body := fixture.get(t, "/jobs/"+id); status != http.StatusNotFound {
			t.Fatalf("GET /jobs/%s of a deleted source = %d %s", id, status, body)
		}
	}
	for _, id := range writes {
		_, body := fixture.get(t, "/jobs/"+id)
		if !strings.Contains(string(body), `"subject":{"type":"chapter_draft","projectId":"`+p+`","mode":"write"}`) {
			t.Fatalf("the run of the earlier writing reads %s", body)
		}
	}
}

// purgedJobRow is a row whose source is gone: the payload is purged, only its place in the list stays.
const purgedJobRow = `governance_payload_state = 'source_deleted', artifact_source_owner_id = NULL, status = 'expired', subject_json = NULL,
	creator_user_id = NULL, can_cancel = false, progress_json = '{}', result_json = NULL, error_json = NULL`
