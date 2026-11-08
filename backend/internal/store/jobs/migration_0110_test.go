package jobstore

import (
	"net/http"
	"strings"
	"testing"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/testutil"
)

// The stand ran the writing agent before S8: its start put the title of the draft into the subject of every run, a key
// this release does not know, and each such row made listProjectJobs of its project 409. 0110 seals those runs by the
// rule of a deleted draft — who and where, the kind of work, how far it came and what it read — and leaves a run of this
// release as it is, every key of its subject and result included.
func TestMigration0110SealsTheRunsOfTheEarlierShape(t *testing.T) {
	fixture := newReleaseShapes(t, "ck_migration_0110", 109)
	const (
		draft = "00000000-0000-4000-8000-00000000f301"
		book  = "00000000-0000-4000-8000-00000000f302"
		after = "00000000-0000-4000-8000-00000000f303"
		read  = "00000000-0000-4000-8000-00000000f304"
	)
	p := fixture.projectID
	subject := func(mode string) jobs.ChapterDraftSubject {
		subject := jobs.ChapterDraftSubject{Type: jobs.KindChapterDraft, ProjectID: p, DraftID: draft, Mode: mode, Revision: 4}
		switch mode {
		case jobs.ChapterDraftScenes:
			subject.PlanNumber = 1
		case jobs.ChapterDraftSelection:
			subject.Place, subject.Instruction = &jobs.ChapterDraftPlace{Quote: "Слова автора."}, "короче"
		case jobs.ChapterDraftSeams:
			subject.Seams = []int{120}
		}
		return subject
	}
	titled := `subject_json = subject_json || '{"title":"Глава автора","bookId":"` + book + `","afterChapterId":"` + after + `"}', `
	earlier := map[string]string{
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusSucceeded, subject(jobs.ChapterDraftScenes), titled+
			`result_json = '{"type":"chapter_draft","mode":"scenes","total":3,"done":3,"chapters":[{"chapterId":"`+read+`","revision":2}],"pack":"`+book+`","beforeVersion":4,"writtenVersion":5}'`): jobs.ChapterDraftScenes,
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusSucceeded, subject(jobs.ChapterDraftSelection), titled+
			`result_json = '{"type":"chapter_draft","mode":"selection","total":1,"done":1}'`): jobs.ChapterDraftSelection,
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusFailed, subject(jobs.ChapterDraftSeams), titled+
			`result_json = '{"type":"chapter_draft","mode":"seams","phase":"scenes","total":2,"done":1,"sceneRuns":[{"scene":1,"words":40,"attempts":1,"text":"Сцена помощника."}]}'`): jobs.ChapterDraftSeams,
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusCanceled, subject(jobs.ChapterDraftCheck), titled+
			`result_json = '{"type":"chapter_draft","mode":"check","phase":"check","total":1}'`): jobs.ChapterDraftCheck,
	}
	// A result key the release no longer knows, under a subject it knows.
	notes := fixture.row(t, jobs.KindChapterDraft, jobs.StatusFailed, subject(jobs.ChapterDraftCheck),
		`result_json = '{"type":"chapter_draft","mode":"check","total":1,"judged":true,"notes":[{"kind":"meaning","quote":"q","body":"b","status":"open"}]}'`)
	earlier[notes] = jobs.ChapterDraftCheck
	// Runs of this release: every key of a subject and a result, and a run of a deleted draft.
	current := subject(jobs.ChapterDraftScenes)
	current.BookID, current.AfterChapterID, current.Scenes, current.Tail = ptr(book), ptr(after), []int{1, 2}, jobs.ChapterDraftTailKeep
	current.From = &jobs.ChapterDraftPlace{From: "Начало.", To: "Конец."}
	kept := []string{
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusFailed, current,
			`result_json = '{"type":"chapter_draft","mode":"scenes","phase":"scenes","total":2,"done":1,"chapters":[{"chapterId":"`+read+`","revision":1}],`+
				`"sceneRuns":[{"scene":1,"words":40,"attempts":1,"from":"Начало.","to":"Конец.","text":"Начало. Конец."}],"pack":"`+book+`","beforeVersion":2,"writtenVersion":3}'`),
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusSucceeded, subject(jobs.ChapterDraftSelection), `result_json = '{"type":"chapter_draft","mode":"selection","total":1}'`),
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusSucceeded, subject(jobs.ChapterDraftSeams), ``),
		fixture.row(t, jobs.KindChapterDraft, jobs.StatusSucceeded,
			jobs.ChapterDraftSubject{Type: jobs.KindChapterDraft, ProjectID: p, DraftID: draft, Mode: jobs.ChapterDraftScenes, BookID: ptr(book), Erased: true},
			`result_json = '{"type":"chapter_draft","mode":"scenes","total":1}'`),
	}
	stored := func(id string) string {
		t.Helper()
		var row string
		if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT concat_ws('|', subject_json::text, result_json::text, updated_at::text) FROM jobs WHERE id = $1`, id).Scan(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	before := map[string]string{}
	for _, id := range kept {
		before[id] = stored(id)
	}

	// The fault the stand showed: one row of the earlier shape and the list of the project is a conflict.
	path := "/projects/" + p + "/jobs?limit=100&kind=chapter_draft"
	if status, body := fixture.get(t, path); status != http.StatusConflict {
		t.Fatalf("before 0110 GET %s = %d %s", path, status, body)
	}
	if _, err := fixture.store.ListProjectJobs(fixture.ctx, jobs.ListProjectJobsStoreInput{ActorUserID: fixture.actorID, ProjectID: p, Limit: 100}); err != jobs.ErrConflict {
		t.Fatalf("before 0110 the store reads the earlier shape: %v", err)
	}

	if err := testutil.ApplyNextMigration(t, fixture.db, 109); err != nil {
		t.Fatal(err)
	}
	if got := fixture.list(t, "&kind=chapter_draft", 100); len(got) != len(earlier)+len(kept) {
		t.Fatalf("the list after 0110 = %v", got)
	}
	for id, mode := range earlier {
		status, body := fixture.get(t, "/jobs/"+id)
		if status != http.StatusOK || !strings.Contains(string(body), `"subject":{"type":"chapter_draft","projectId":"`+p+`","mode":"`+mode+`"}`) {
			t.Fatalf("GET /jobs/%s = %d %s", id, status, body)
		}
		var subject, result []byte
		if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT subject_json, result_json FROM jobs WHERE id = $1`, id).Scan(&subject, &result); err != nil {
			t.Fatal(err)
		}
		decoded, err := jobs.DecodeSubject(jobs.KindChapterDraft, subject)
		if err != nil || !decoded.(jobs.ChapterDraftSubject).Erased || decoded.(jobs.ChapterDraftSubject).Mode != mode {
			t.Fatalf("%s: the sealed subject %s, %v", id, subject, err)
		}
		for _, text := range []string{"Глава автора", "Слова автора", "короче", "Сцена помощника", `"phase"`, `"notes"`} {
			if strings.Contains(string(subject)+string(result), text) {
				t.Fatalf("%s: the sealed run keeps %s: %s %s", id, text, subject, result)
			}
		}
	}
	var sealed []byte
	scenes := ""
	for id, mode := range earlier {
		if mode == jobs.ChapterDraftScenes {
			scenes = id
		}
	}
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT subject_json::text || result_json::text FROM jobs WHERE id = $1`, scenes).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{`"bookId": "` + book, `"afterChapterId": "` + after, `"done": 3`, `"chapterId": "` + read, `"pack": "` + book, `"writtenVersion": 5`} {
		if !strings.Contains(string(sealed), kept) {
			t.Fatalf("the sealed run of the scenes lost %s: %s", kept, sealed)
		}
	}
	for _, id := range kept {
		if got := stored(id); got != before[id] {
			t.Fatalf("0110 changed a run of this release:\n%s\n%s", before[id], got)
		}
	}
}

// A run of the earlier shape that has not ended stops the release at 0110, as 0109 stops at a run of the earlier
// writing: it is not sealed under its worker.
func TestMigration0110WaitsForARunOfTheEarlierShapeToEnd(t *testing.T) {
	fixture := newReleaseShapes(t, "ck_migration_0110_active", 109)
	queued := fixture.row(t, jobs.KindChapterDraft, jobs.StatusQueued,
		jobs.ChapterDraftSubject{Type: jobs.KindChapterDraft, ProjectID: fixture.projectID, DraftID: fixture.projectID, Mode: jobs.ChapterDraftCheck, Revision: 2},
		`subject_json = subject_json || '{"title":"Глава автора"}'`)
	// The migration as the runner applies it, in the schema of the fixture.
	migration := strings.ReplaceAll(string(testutil.MigrationSQL(t, 110)), "public.", testutil.IsolatedPostgresSchema(t, fixture.db)+".")
	if _, err := fixture.db.Pool.Exec(fixture.ctx, migration); err == nil || !strings.Contains(err.Error(), "earlier shape") {
		t.Fatalf("the migration ran under a queued run of the earlier shape: %v", err)
	}
	var title string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT subject_json->>'title' FROM jobs WHERE id = $1`, queued).Scan(&title); err != nil || title != "Глава автора" {
		t.Fatalf("the refused migration changed the run: %q, %v", title, err)
	}
}

func ptr(value string) *string { return &value }
