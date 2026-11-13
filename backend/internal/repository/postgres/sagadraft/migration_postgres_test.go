package sagadraft

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/domain/textface"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// todaysProjects gives a schema older than 0124 the sign of the demo saga (specs/2026-11-09-demo-saga.md DS3.1), and
// one older than 0144 the limit of the returns on the word of an app (specs/2026-11-13-desk-permits.md PM2.4): today's
// project, library and quota code, which makes the rows of these tests, reads and writes them. None of the migrations
// applied here touches them, so they stay to the end of a test.
func todaysProjects(t *testing.T, db *storedb.DB) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		ALTER TABLE projects ADD COLUMN demo boolean NOT NULL DEFAULT false;
		ALTER TABLE writer_quota_overrides ADD COLUMN app_returns_per_day integer;
		ALTER TABLE platform_quota_defaults ADD COLUMN app_returns_per_day integer`); err != nil {
		t.Fatal(err)
	}
}

// specs/2026-11-01-writer-assistant-index.md, R7.3: migration 0087 removes the chapter_draft Jobs the stand has
// from before the section, with their source owners and references, and nothing else.
func TestMigration0087RemovesTheJobsOfChats(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_migration_0087", 86)
	todaysProjects(t, db)
	// Today's project code reads the limit columns of 0087, 0089, 0091 and 0108: they exist only while the rows are made.
	if _, err := db.Pool.Exec(ctx, `
		ALTER TABLE writer_quota_overrides ADD COLUMN chapter_drafts_per_day integer, ADD COLUMN drawing_turns_per_day integer, ADD COLUMN draft_turns_per_day integer,
		  ADD COLUMN draft_rewrites_per_day integer;
		ALTER TABLE platform_quota_defaults ADD COLUMN chapter_drafts_per_day integer, ADD COLUMN drawing_turns_per_day integer, ADD COLUMN draft_turns_per_day integer,
		  ADD COLUMN draft_rewrites_per_day integer`); err != nil {
		t.Fatal(err)
	}
	registered, err := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateUser(ctx, auth.NewUser{Email: "owner@example.test", DisplayName: "Автор"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, registered.ID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	const draftJob = "00000000-0000-4000-8000-00000000b001"
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := jobstore.New(db).CreateChapterDraftJobInTx(ctx, tx, jobstore.ChapterDraftJobInput{
			JobID: draftJob, CreatorUserID: registered.ID, CreatedAt: time.Now().UTC(),
			Subject: jobs.ChapterDraftSubject{
				Type: jobs.KindChapterDraft, ProjectID: project.ID, DraftID: draftJob, Mode: jobs.ChapterDraftWrite, Erased: true,
			},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Another artifact of the project stays with its owner.
	const otherJob = "00000000-0000-4000-8000-00000000b002"
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := jobstore.New(db).CreateIllustrationJobInTx(ctx, tx, jobstore.IllustrationJobInput{
			JobID: otherJob, CreatorUserID: registered.ID, CreatedAt: time.Now().UTC(),
			Subject: jobs.IllustrationSubject{Type: jobs.KindIllustration, ProjectID: project.ID, ImageID: otherJob},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		ALTER TABLE writer_quota_overrides DROP COLUMN chapter_drafts_per_day, DROP COLUMN drawing_turns_per_day, DROP COLUMN draft_turns_per_day,
		  DROP COLUMN draft_rewrites_per_day;
		ALTER TABLE platform_quota_defaults DROP COLUMN chapter_drafts_per_day, DROP COLUMN drawing_turns_per_day, DROP COLUMN draft_turns_per_day,
		  DROP COLUMN draft_rewrites_per_day`); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 86); err != nil {
		t.Fatal(err)
	}
	count := func(query, id string) int {
		t.Helper()
		var found int
		if err := db.Pool.QueryRow(ctx, query, id).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	const jobsOf = `SELECT count(*) FROM jobs WHERE id = $1::uuid`
	const ownersOf = `SELECT count(*) FROM artifact_source_owners WHERE artifact_id = $1::uuid`
	if count(jobsOf, draftJob) != 0 || count(ownersOf, draftJob) != 0 {
		t.Fatal("the Job of a chat stayed")
	}
	if count(jobsOf, otherJob) != 1 || count(ownersOf, otherJob) != 1 {
		t.Fatal("the migration took another Job")
	}
}

// specs/2026-11-02-writer-workshop.md, W8.3: what should happen becomes the idea of its draft; the plan a model
// made from it moves into the draft between the marks of what the author did not write, with his edits; a sketch
// and a plan stay as they are. The Jobs of a plan from what should happen go with their source owners, and the
// rest lose the counts of the plan and the rounds of the judge.
func TestMigration0090MovesWhatShouldHappenIntoTheIdea(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_migration_0090", 89)
	todaysProjects(t, db)
	// Today's project code reads the limit columns of 0091 and 0108: they exist only while the project is made.
	if _, err := db.Pool.Exec(ctx, `
		ALTER TABLE writer_quota_overrides ADD COLUMN draft_turns_per_day integer, ADD COLUMN draft_rewrites_per_day integer;
		ALTER TABLE platform_quota_defaults ADD COLUMN draft_turns_per_day integer, ADD COLUMN draft_rewrites_per_day integer`); err != nil {
		t.Fatal(err)
	}
	registered, err := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateUser(ctx, auth.NewUser{Email: "owner@example.test", DisplayName: "Автор"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, registered.ID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		ALTER TABLE writer_quota_overrides DROP COLUMN draft_turns_per_day, DROP COLUMN draft_rewrites_per_day;
		ALTER TABLE platform_quota_defaults DROP COLUMN draft_turns_per_day, DROP COLUMN draft_rewrites_per_day`); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("с", 8001)
	drafts := map[string][4]string{ // kind, body, plan, plan_edited
		"00000000-0000-4000-8000-00000000c001": {"skeleton", "встреча у кузницы", "", "false"},
		"00000000-0000-4000-8000-00000000c002": {"skeleton", "встреча", "¶1 моя правка\n¶2", "true"},
		"00000000-0000-4000-8000-00000000c003": {"sketch", "набросок", "", "false"},
		"00000000-0000-4000-8000-00000000c004": {"plan", "¶1\n¶2\n¶3", "", "false"},
		"00000000-0000-4000-8000-00000000c005": {"skeleton", long, "¶1", "false"},
	}
	for id, draft := range drafts {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO chapter_drafts (id, project_id, title, kind, body, plan, plan_edited, created_by, updated_by)
			VALUES ($1, $2, 'Глава', $3, $4, $5, $6::boolean, $7, $7)
		`, id, project.ID, draft[0], draft[1], draft[2], draft[3], registered.ID); err != nil {
			t.Fatal(err)
		}
	}
	jobOf := func(id string) {
		t.Helper()
		if err := db.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := jobstore.New(db).CreateChapterDraftJobInTx(ctx, tx, jobstore.ChapterDraftJobInput{
				JobID: id, CreatorUserID: registered.ID, CreatedAt: time.Now().UTC(),
				Subject: jobs.ChapterDraftSubject{Type: jobs.KindChapterDraft, ProjectID: project.ID, DraftID: id, Mode: jobs.ChapterDraftWrite, Erased: true},
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	const planJob, sketchJob = "00000000-0000-4000-8000-00000000d001", "00000000-0000-4000-8000-00000000d002"
	jobOf(planJob)
	jobOf(sketchJob)
	// The release before this one wrote these shapes: a plan from what should happen and the counts of its plan.
	for id, shape := range map[string]string{
		planJob: `subject_json = subject_json - 'erased' || '{"mode":"plan","skeleton":"встреча"}',
		  result_json = '{"type":"chapter_draft","mode":"plan","plan":"¶1","examples":6,"threads":2}'`,
		sketchJob: `subject_json = subject_json - 'erased' || '{"sketch":"набросок"}',
		  result_json = '{"type":"chapter_draft","mode":"write","total":2,"examples":0,"threads":0,"review":{"round":2,"words":10,"fixes":{"0":"а"}}}'`,
	} {
		if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET `+shape+` WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := testutil.ApplyNextMigration(t, db, 89); err != nil {
		t.Fatal(err)
	}
	read := func(id string) (idea, kind, body string, revision int64) {
		t.Helper()
		if err := db.Pool.QueryRow(ctx, `SELECT idea, kind, body, revision FROM chapter_drafts WHERE id = $1`, id).Scan(&idea, &kind, &body, &revision); err != nil {
			t.Fatal(err)
		}
		return
	}
	for id, want := range map[string][4]string{
		"00000000-0000-4000-8000-00000000c001": {"встреча у кузницы", "sketch", "", "2"},
		"00000000-0000-4000-8000-00000000c002": {"встреча", "plan", "⟦¶1 моя правка\n¶2⟧", "2"},
		"00000000-0000-4000-8000-00000000c003": {"", "sketch", "набросок", "1"},
		"00000000-0000-4000-8000-00000000c004": {"", "plan", "¶1\n¶2\n¶3", "1"},
		"00000000-0000-4000-8000-00000000c005": {"", "plan", long + "\n\n⟦¶1⟧", "2"},
	} {
		idea, kind, body, revision := read(id)
		if idea != want[0] || kind != want[1] || body != want[2] || strconv.FormatInt(revision, 10) != want[3] {
			t.Fatalf("%s: idea %q, kind %s, body %.40q, revision %d", id, idea, kind, body, revision)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE chapter_drafts SET kind = 'skeleton' WHERE id = '00000000-0000-4000-8000-00000000c003'`); err == nil {
		t.Fatal("a draft took the kind of what should happen")
	}
	var plans, owners int
	if err := db.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM jobs WHERE id = $1), (SELECT count(*) FROM artifact_source_owners WHERE artifact_id = $1)
	`, planJob).Scan(&plans, &owners); err != nil || plans != 0 || owners != 0 {
		t.Fatalf("the Job of a plan stayed: %d jobs, %d owners, %v", plans, owners, err)
	}
	var result []byte
	if err := db.Pool.QueryRow(ctx, `SELECT result_json FROM jobs WHERE id = $1`, sketchJob).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.DecodeResult(jobs.KindChapterDraft, result); err != nil {
		t.Fatalf("the Job of a chapter does not read: %s, %v", result, err)
	}
}

// specs/2026-11-06-writing-agent.md, WA10.4, WA10.5, WA13 (S8): the chapter becomes the one working text and the author's
// text before it a version «switch», counted by the rule of «Ваших слов»; what the earlier writing kept for its next run
// is emptied; the notes on the chapter stand in the working text; every run of the earlier writing is sealed so that the
// decoder of this release reads it, and a run of the writing agent is left as it is.
func TestMigration0109MakesTheChapterTheOneWorkingText(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_migration_0109", 108)
	todaysProjects(t, db)
	registered, err := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateUser(ctx, auth.NewUser{Email: "owner@example.test", DisplayName: "Автор"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, registered.ID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../domain/sagadraft/testdata/words.json")
	if err != nil {
		t.Fatal(err)
	}
	var table []struct {
		Text  string `json:"text"`
		Valid bool   `json:"valid"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	draft := func(id, body, chapter string) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO chapter_drafts (id, project_id, title, kind, body, chapter_text, pending_block, last_run, last_run_words, created_by, updated_by)
			VALUES ($1, $2, 'Глава', 'sketch', $3, $4, CASE WHEN $4 <> '' THEN 'блок' ELSE '' END, CASE WHEN $4 <> '' THEN 'continued' ELSE '' END,
			        CASE WHEN $4 <> '' THEN 40 ELSE 0 END, $5, $5)
		`, id, project.ID, body, chapter, registered.ID); err != nil {
			t.Fatal(err)
		}
	}
	const written, blank, unwritten = "00000000-0000-4000-8000-00000000e001", "00000000-0000-4000-8000-00000000e002", "00000000-0000-4000-8000-00000000e003"
	draft(written, "Мой набросок. ⟦Блок помощника.⟧", "Глава, написанная письмом.\n\n⟦Её конец.⟧")
	draft(blank, "", "Глава правки из книги.")
	draft(unwritten, "Только набросок автора.", "")
	counted := map[string]string{}
	for index, test := range table {
		// 0109 ran before the author's italic and bold came into the working text
		// (specs/2026-11-08-italic-bold-in-text.md TF1.4): a text of that time has no mark of a face.
		if test.Valid && test.Text != "" && !textface.HasFaces(test.Text) {
			id := fmt.Sprintf("00000000-0000-4000-8000-0000000f%04d", index)
			counted[id] = test.Text
			draft(id, test.Text, "глава")
		}
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chapter_draft_notes (project_id, draft_id, number, kind, target, origin, quote, body, status, job_id)
		VALUES ($1, $2, 1, 'canon', 'chapter', 'helper', 'Её конец.', 'расхождение', 'open', NULL)
	`, project.ID, written); err != nil {
		t.Fatal(err)
	}
	job := func(id string, mode string) {
		t.Helper()
		subject := jobs.ChapterDraftSubject{Type: jobs.KindChapterDraft, ProjectID: project.ID, DraftID: written, Mode: mode, Erased: true}
		if mode != jobs.ChapterDraftWrite {
			subject = jobs.ChapterDraftSubject{Type: jobs.KindChapterDraft, ProjectID: project.ID, DraftID: written, Mode: mode, Revision: 3, PlanNumber: 1}
		}
		if err := db.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := jobstore.New(db).CreateChapterDraftJobInTx(ctx, tx, jobstore.ChapterDraftJobInput{
				JobID: id, CreatorUserID: registered.ID, CreatedAt: time.Now().UTC(), Subject: subject,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	const failedRun, erasedRun, agentRun = "00000000-0000-4000-8000-00000000e101", "00000000-0000-4000-8000-00000000e102", "00000000-0000-4000-8000-00000000e103"
	job(failedRun, jobs.ChapterDraftWrite)
	job(erasedRun, jobs.ChapterDraftWrite)
	job(agentRun, jobs.ChapterDraftScenes)
	// The earlier release wrote these shapes: a failed run keeps the author's text and what it wrote, a run of a deleted
	// draft keeps the fixed word in place of the text.
	for id, shape := range map[string]string{
		failedRun: `status = 'failed', completed_at = clock_timestamp(), subject_json = subject_json - 'erased' || '{"sketch":"Мой набросок.","title":"Глава","rework":true}',
		  result_json = '{"type":"chapter_draft","mode":"write","phase":"pieces","sketch":"Мой набросок.","pieces":["кусок"],"total":2,"judged":true,
		    "notes":[{"kind":"meaning","quote":"кусок","body":"кто","status":"applied"}],"chapters":[{"chapterId":"00000000-0000-4000-8000-00000000e201","revision":1}]}'`,
		erasedRun: `status = 'succeeded', completed_at = clock_timestamp(), subject_json = subject_json - 'erased' || '{"sketch":"erased","rework":false}',
		  result_json = '{"type":"chapter_draft","mode":"write","total":2}'`,
		agentRun: `status = 'succeeded', completed_at = clock_timestamp(), result_json = '{"type":"chapter_draft","mode":"scenes","total":1,"done":1,"writtenVersion":2}'`,
	} {
		if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET `+shape+` WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	var agentSubject []byte
	if err := db.Pool.QueryRow(ctx, `SELECT subject_json FROM jobs WHERE id = $1`, agentRun).Scan(&agentSubject); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 108); err != nil {
		t.Fatal(err)
	}

	type row struct {
		body, chapter, pending, lastRun string
		revision                        int64
		versions                        int
	}
	read := func(id string) row {
		t.Helper()
		var got row
		if err := db.Pool.QueryRow(ctx, `
			SELECT body, chapter_text, pending_block, last_run, revision, last_version_number FROM chapter_drafts WHERE id = $1
		`, id).Scan(&got.body, &got.chapter, &got.pending, &got.lastRun, &got.revision, &got.versions); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read(written); got != (row{body: "Глава, написанная письмом.\n\n⟦Её конец.⟧", revision: 2, versions: 1}) {
		t.Fatalf("a written chapter = %+v", got)
	}
	if got := read(blank); got != (row{body: "Глава правки из книги.", revision: 2}) {
		t.Fatalf("a chapter without the author's text = %+v", got)
	}
	if got := read(unwritten); got != (row{body: "Только набросок автора.", revision: 1}) {
		t.Fatalf("a draft without a chapter = %+v", got)
	}
	version := func(id string) (reason, text string, words, own int) {
		t.Helper()
		if err := db.Pool.QueryRow(ctx, `
			SELECT reason, text, words, author_words FROM chapter_draft_versions WHERE draft_id = $1 AND number = 1
		`, id).Scan(&reason, &text, &words, &own); err != nil {
			t.Fatal(err)
		}
		return
	}
	if reason, text, words, own := version(written); reason != "switch" || text != "Мой набросок. ⟦Блок помощника.⟧" || words != 4 || own != 2 {
		t.Fatalf("the version «switch» = %s %q, %d words, %d of the author", reason, text, words, own)
	}
	for id, text := range counted {
		_, _, words, own := version(id)
		if words != len(domain.WordsOf(domain.WithoutMarks(text))) || own != domain.AuthorWords(text) {
			t.Fatalf("%q: %d words, %d of the author; the rule of «Ваших слов» says %d, %d", text, words, own,
				len(domain.WordsOf(domain.WithoutMarks(text))), domain.AuthorWords(text))
		}
	}
	var target string
	if err := db.Pool.QueryRow(ctx, `SELECT target FROM chapter_draft_notes WHERE draft_id = $1`, written).Scan(&target); err != nil || target != "draft" {
		t.Fatalf("the note on the chapter stands on %q, %v", target, err)
	}
	stored := func(id string) (jobs.ChapterDraftSubject, jobs.ChapterDraftResult, []byte) {
		t.Helper()
		var subject, result []byte
		if err := db.Pool.QueryRow(ctx, `SELECT subject_json, result_json FROM jobs WHERE id = $1`, id).Scan(&subject, &result); err != nil {
			t.Fatal(err)
		}
		decodedSubject, err := jobs.DecodeSubject(jobs.KindChapterDraft, subject)
		if err != nil {
			t.Fatalf("%s: the subject does not read: %s, %v", id, subject, err)
		}
		decodedResult, err := jobs.DecodeResult(jobs.KindChapterDraft, result)
		if err != nil {
			t.Fatalf("%s: the result does not read: %s, %v", id, result, err)
		}
		return decodedSubject.(jobs.ChapterDraftSubject), decodedResult.(jobs.ChapterDraftResult), subject
	}
	for _, id := range []string{failedRun, erasedRun} {
		subject, result, rawSubject := stored(id)
		if !subject.Erased || subject.Mode != jobs.ChapterDraftWrite || subject.DraftID != written || result.Mode != jobs.ChapterDraftWrite || result.Phase != "" ||
			strings.Contains(string(rawSubject), "набросок") {
			t.Fatalf("%s: the run of the earlier writing = %+v, %+v", id, subject, result)
		}
	}
	if _, result, _ := stored(failedRun); result.Total != 2 || len(result.Chapters) != 1 {
		t.Fatalf("the failed run lost what it read: %+v", result)
	}
	subject, result, rawSubject := stored(agentRun)
	if subject.Erased || subject.PlanNumber != 1 || result.WrittenVersion != 2 || string(rawSubject) != string(agentSubject) {
		t.Fatalf("the run of the writing agent changed: %s, %+v", rawSubject, result)
	}
}

// S8: a run of the earlier writing that has not ended stops the release at its migration: nothing reads its work any more.
func TestMigration0109WaitsForTheEarlierWritingToEnd(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_migration_0109_active", 108)
	todaysProjects(t, db)
	registered, err := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateUser(ctx, auth.NewUser{Email: "owner@example.test", DisplayName: "Автор"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, registered.ID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	const queued = "00000000-0000-4000-8000-00000000e301"
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := jobstore.New(db).CreateChapterDraftJobInTx(ctx, tx, jobstore.ChapterDraftJobInput{
			JobID: queued, CreatorUserID: registered.ID, CreatedAt: time.Now().UTC(),
			Subject: jobs.ChapterDraftSubject{Type: jobs.KindChapterDraft, ProjectID: project.ID, DraftID: queued, Mode: jobs.ChapterDraftWrite, Erased: true},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// The migration as the runner applies it, in the schema of the fixture.
	migration := strings.ReplaceAll(string(testutil.MigrationSQL(t, 109)), "public.", testutil.IsolatedPostgresSchema(t, db)+".")
	if _, err := db.Pool.Exec(ctx, migration); err == nil || !strings.Contains(err.Error(), "earlier writing") {
		t.Fatalf("the migration ran under a queued run of the earlier writing: %v", err)
	}
	var body string
	if err := db.Pool.QueryRow(ctx, `SELECT subject_json->>'mode' FROM jobs WHERE id = $1`, queued).Scan(&body); err != nil || body != jobs.ChapterDraftWrite {
		t.Fatalf("the refused migration changed the run: %q, %v", body, err)
	}
}

// specs/2026-11-08-italic-bold-in-text.md TF3.1, TF1.5: migration 0116 escapes the stars and backslashes of a paragraph
// of the book whose markdown is its plain text, so «* * *» reads as three stars and not as italic, and leaves plain_text
// and a paragraph with its own markdown as they are; the signs ⟨ ⟩ ⟪ ⟫ that came into a working text as text before
// become ‹ › « » in it, in its versions and in the quotes of its notes, and the draft moves its revision.
func TestMigration0116EscapesTheBookAndTheSignsOfFaces(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_migration_0116", 115)
	todaysProjects(t, db)
	registered, err := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateUser(ctx, auth.NewUser{Email: "owner@example.test", DisplayName: "Автор"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, registered.ID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	libraries := librarystore.New(db)
	const book = "00000000-0000-4000-8000-00000000f601"
	if _, err := libraries.CreateBook(ctx, library.CreateBookStoreInput{ID: book, ProjectID: project.ID, Title: "Том", Order: 1, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	legacy := []string{"* * *", `Сумма 5*3 и путь a\b.`, "Обычный абзац."}
	var chapter string
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		chapter, err = libraries.AddChapterInTx(ctx, tx, library.AddChapterInput{
			ProjectID: project.ID, BookID: book, Title: "Глава", Paragraphs: append(append([]string{}, legacy...), "*Своя* разметка."), At: time.Now().UTC(),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Every writer of the book before 0116 put the plain text into markdown.
	for order, text := range legacy {
		if _, err := db.Pool.Exec(ctx, `UPDATE chapter_paragraphs SET markdown = $2, plain_text = $2 WHERE chapter_id = $1 AND "order" = $3`,
			chapter, text, order+1); err != nil {
			t.Fatal(err)
		}
	}
	const signed, plain = "00000000-0000-4000-8000-00000000f611", "00000000-0000-4000-8000-00000000f612"
	for id, body := range map[string]string{signed: "Стрелка ⟨вправо⟩ и ⟪кавычки⟫, ⟦модель⟧ одна ⟩.", plain: "Без знаков ⟦модели⟧."} {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO chapter_drafts (id, project_id, title, kind, body, created_by, updated_by) VALUES ($1, $2, 'Глава', 'sketch', $3, $4, $4)
		`, id, project.ID, body, registered.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chapter_draft_versions (project_id, draft_id, number, reason, text, words, author_words) VALUES ($1, $2, 1, 'before_apply', '⟪Было⟫ так.', 2, 2);
	`, project.ID, signed); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chapter_draft_notes (project_id, draft_id, number, kind, target, origin, quote, body, replacement, status)
		VALUES ($1, $2, 1, 'canon', 'draft', 'helper', 'Стрелка ⟨вправо⟩', 'знак', 'Стрелка ⟨влево⟩', 'open')
	`, project.ID, signed); err != nil {
		t.Fatal(err)
	}
	revisions := func() map[string]int64 {
		rows, err := db.Pool.Query(ctx, `SELECT id::text, revision FROM chapter_drafts WHERE project_id = $1`, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]int64{}
		for rows.Next() {
			var id string
			var revision int64
			if err := rows.Scan(&id, &revision); err != nil {
				t.Fatal(err)
			}
			found[id] = revision
		}
		return found
	}
	before := revisions()

	if err := testutil.ApplyNextMigration(t, db, 115); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Pool.Query(ctx, `SELECT variant || '|' || markdown || '|' || plain_text FROM chapter_paragraphs WHERE chapter_id = $1 ORDER BY variant, "order"`, chapter)
	if err != nil {
		t.Fatal(err)
	}
	paragraphs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`draft|\* \* \*|* * *`, `draft|Сумма 5\*3 и путь a\\b.|Сумма 5*3 и путь a\b.`, "draft|Обычный абзац.|Обычный абзац.", "draft|*Своя* разметка.|Своя разметка.",
		`published|\* \* \*|* * *`, `published|Сумма 5\*3 и путь a\\b.|Сумма 5*3 и путь a\b.`, "published|Обычный абзац.|Обычный абзац.", "published|*Своя* разметка.|Своя разметка.",
	}
	if strings.Join(paragraphs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the paragraphs of the book:\n%q\nwant\n%q", paragraphs, want)
	}
	for _, paragraph := range want[:3] {
		parts := strings.Split(paragraph, "|")
		if textface.MarkdownPlain(parts[1]) != parts[2] {
			t.Fatalf("%q does not read as its plain text", parts[1])
		}
	}
	var body, version, quote, replacement, kept string
	if err := db.Pool.QueryRow(ctx, `SELECT body FROM chapter_drafts WHERE id = $1`, signed).Scan(&body); err != nil || body != "Стрелка ‹вправо› и «кавычки», ⟦модель⟧ одна ›." {
		t.Fatalf("the working text = %q, %v", body, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT text FROM chapter_draft_versions WHERE draft_id = $1`, signed).Scan(&version); err != nil || version != "«Было» так." {
		t.Fatalf("the version = %q, %v", version, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT quote, replacement FROM chapter_draft_notes WHERE draft_id = $1`, signed).Scan(&quote, &replacement); err != nil ||
		quote != "Стрелка ‹вправо›" || replacement != "Стрелка ‹влево›" {
		t.Fatalf("the note = %q → %q, %v", quote, replacement, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT body FROM chapter_drafts WHERE id = $1`, plain).Scan(&kept); err != nil || kept != "Без знаков ⟦модели⟧." {
		t.Fatalf("a text without the signs = %q, %v", kept, err)
	}
	after := revisions()
	if after[signed] != before[signed]+1 || after[plain] != before[plain] {
		t.Fatalf("revisions %v, then %v", before, after)
	}
}
