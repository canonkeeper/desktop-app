package authormaterials

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	service "canonkeeper/backend/internal/services/authormaterials"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	projectstore "canonkeeper/backend/internal/store/projects"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

type materialsProject struct {
	db       *storedb.DB
	store    *Repository
	editor   service.Write
	viewer   service.Write
	outsider service.Write
}

func seedProject(t *testing.T, database string) materialsProject {
	t.Helper()
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, database)
	accounts := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	register := func(email string) (string, auth.SessionWriteProof) {
		user, err := accounts.CreateUser(ctx, auth.NewUser{Email: email, DisplayName: email})
		if err != nil {
			t.Fatal(err)
		}
		// An import is model work, for a verified address only (specs/2026-11-09-quota-accounting.md Q5.3).
		if _, err := db.Pool.Exec(ctx, `UPDATE users SET email_verified_at = clock_timestamp() WHERE id = $1`, user.ID); err != nil {
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
		return user.ID, proof
	}
	editorID, editorProof := register("editor@example.test")
	viewerID, viewerProof := register("viewer@example.test")
	outsiderID, outsiderProof := register("outsider@example.test")
	project, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, editorID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO project_memberships (id, project_id, user_id, role, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'viewer', clock_timestamp(), clock_timestamp())
	`, project.ID, viewerID); err != nil {
		t.Fatal(err)
	}
	write := func(user string, proof auth.SessionWriteProof) service.Write {
		return service.Write{Actor: service.Actor{UserID: user, ProjectID: project.ID}, Proof: proof}
	}
	return materialsProject{
		db: db, store: New(db, jobstore.New(db)),
		editor: write(editorID, editorProof), viewer: write(viewerID, viewerProof), outsider: write(outsiderID, outsiderProof),
	}
}

func (fixture materialsProject) start(t *testing.T, key string, parsed domain.Parsed) domain.Import {
	t.Helper()
	body, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	started, err := fixture.store.StartAuthorMaterialsImport(context.Background(), startCommand(fixture.editor, key, string(body)))
	if err != nil {
		t.Fatal(err)
	}
	return started
}

func startCommand(write service.Write, key, body string) service.StartCommand {
	return service.StartCommand{
		Write:      write,
		Replay:     service.Replay{ScopeHash: "scope-" + write.UserID, OperationID: "author_materials.import.v1", KeyHash: key, RequestHash: body},
		SourceName: "навыки.xlsx", SourceKind: domain.SourceXLSX, Parsed: []byte(body), At: time.Now().UTC(),
	}
}

// claimed runs the Job up to its claim, as the worker does.
func (fixture materialsProject) claimed(t *testing.T, started domain.Import) jobs.WorkerClaim {
	t.Helper()
	ctx := context.Background()
	var incarnation int64
	if err := fixture.db.Pool.QueryRow(ctx, `
		UPDATE jobs SET status = 'running', claim_incarnation = claim_incarnation + 1, claimed_at = clock_timestamp(), started_at = clock_timestamp()
		WHERE id = $1 RETURNING claim_incarnation
	`, started.JobID).Scan(&incarnation); err != nil {
		t.Fatal(err)
	}
	return jobs.WorkerClaim{ProjectID: fixture.editor.ProjectID, JobID: started.JobID, ClaimIncarnation: incarnation}
}

func (fixture materialsProject) finished(t *testing.T, started domain.Import, candidates []domain.Draft) {
	t.Helper()
	if err := fixture.store.FinishImport(context.Background(), fixture.claimed(t, started), service.ImportOutcome{Status: jobs.StatusSucceeded, Candidates: candidates}); err != nil {
		t.Fatal(err)
	}
}

func (fixture materialsProject) confirm(t *testing.T, started domain.Import, key string, choices ...domain.Choice) int {
	t.Helper()
	saved, err := fixture.store.ConfirmAuthorMaterialsImport(context.Background(), service.ConfirmCommand{
		Write:    fixture.editor,
		Replay:   service.Replay{ScopeHash: "scope", OperationID: "author_materials.confirm.v1", KeyHash: key, RequestHash: key},
		ImportID: started.ID, Choices: choices,
	})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

var fileHolds = domain.Parsed{Sections: []domain.Section{{Title: "Навыки", Rows: [][]domain.Cell{{{Text: "Секрет автора", Note: "текст файла"}}}}}}

func skill(name, description string) domain.Draft {
	return domain.Draft{Name: name, Kind: domain.KindSkill, Description: description, Properties: []domain.Property{{Key: "Ранг", Value: "1"}}, Section: "Навыки"}
}

// specs/2026-11-04-saga-map.md R9.2, R9.4: a file goes through its Job into candidates; the answer writes one record per
// name, updates a name the project has, and takes the text of the file with it.
func TestImportFromFileToRecordsPostgres(t *testing.T) {
	fixture := seedProject(t, "ck_author_materials_import")
	ctx := context.Background()
	actor := fixture.editor.Actor

	started := fixture.start(t, "key-1", fileHolds)
	if started.JobStatus != "queued" || started.Candidates != nil || started.SourceName != "навыки.xlsx" {
		t.Fatalf("started = %+v", started)
	}
	var subject []byte
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT subject_json FROM jobs WHERE id = $1`, started.JobID).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if string(subject) == "" || json.Valid(subject) == false {
		t.Fatalf("subject = %s", subject)
	}
	replay, err := fixture.store.StartAuthorMaterialsImport(ctx, func() service.StartCommand {
		body, _ := json.Marshal(fileHolds)
		return startCommand(fixture.editor, "key-1", string(body))
	}())
	if err != nil || replay.ID != started.ID {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	if _, err := fixture.store.StartAuthorMaterialsImport(ctx, startCommand(fixture.editor, "key-2", `{"sections":[]}`)); !errors.Is(err, domain.ErrImportBusy) {
		t.Fatalf("a second import while the first runs: %v", err)
	}
	if _, err := fixture.store.ConfirmAuthorMaterialsImport(ctx, service.ConfirmCommand{
		Write: fixture.editor, ImportID: started.ID,
		Replay: service.Replay{ScopeHash: "scope", OperationID: "author_materials.confirm.v1", KeyHash: "early", RequestHash: "early"},
	}); !errors.Is(err, domain.ErrImportBusy) {
		t.Fatalf("an answer before the Job ends: %v", err)
	}

	parsed, err := fixture.store.ImportParsed(ctx, fixture.claimedPeek(started), started.ID)
	if err != nil || len(parsed.Sections) != 1 || parsed.Sections[0].Rows[0][0].Note != "текст файла" {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
	fixture.finished(t, started, []domain.Draft{skill("Ужас", "пугает"), skill("Ёж", "колется"), skill("лишний", "")})

	var left []byte
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT parsed FROM author_material_imports WHERE id = $1`, started.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if string(left) != "{}" {
		t.Fatalf("the file stayed after its Job: %s", left)
	}
	found, keys, err := fixture.store.AuthorMaterialsImport(ctx, fixture.viewer.Actor)
	if err != nil || found == nil || len(found.Candidates) != 3 || found.JobStatus != "succeeded" || len(keys) != 0 {
		t.Fatalf("a member reads it: %+v, %v, %v", found, keys, err)
	}
	if _, _, err := fixture.store.AuthorMaterialsImport(ctx, service.Actor{UserID: fixture.outsider.UserID, ProjectID: actor.ProjectID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an outsider: %v", err)
	}

	edited := "Еж"
	if saved := fixture.confirm(t, started, "answer-1", domain.Choice{Index: 0}, domain.Choice{Index: 1, Name: &edited}); saved != 2 {
		t.Fatalf("saved = %d", saved)
	}
	again, err := fixture.store.ConfirmAuthorMaterialsImport(ctx, service.ConfirmCommand{
		Write: fixture.editor, ImportID: started.ID,
		Replay: service.Replay{ScopeHash: "scope", OperationID: "author_materials.confirm.v1", KeyHash: "answer-1", RequestHash: "answer-1"},
	})
	if err != nil || again != 2 {
		t.Fatalf("a replay answers the same: %d, %v", again, err)
	}
	gone, _, err := fixture.store.AuthorMaterialsImport(ctx, actor)
	if err != nil || gone != nil {
		t.Fatalf("the import stayed: %+v, %v", gone, err)
	}
	var kept int
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT count(*) FROM author_material_imports`).Scan(&kept); err != nil || kept != 0 {
		t.Fatalf("rows of imports = %d, %v", kept, err)
	}

	// The same file again: the names update, nothing doubles (Ё = Е, the case does not matter).
	second := fixture.start(t, "key-3", fileHolds)
	fixture.finished(t, second, []domain.Draft{skill("ужас", "пугает сильнее"), skill("Ёж", "колется")})
	withKeys, keysNow, err := fixture.store.AuthorMaterialsImport(ctx, actor)
	if err != nil || withKeys == nil || !keysNow["ужас"] || !keysNow["еж"] {
		t.Fatalf("keys = %v, %v", keysNow, err)
	}
	if saved := fixture.confirm(t, second, "answer-2", domain.Choice{Index: 0}, domain.Choice{Index: 1}); saved != 2 {
		t.Fatalf("saved = %d", saved)
	}
	records, err := fixture.store.AuthorMaterials(ctx, actor.ProjectID)
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %+v, %v", records, err)
	}
	if records[1].Name != "ужас" || records[1].Description != "пугает сильнее" || records[1].Revision != 2 || records[1].Properties[0].Key != "Ранг" {
		t.Fatalf("the name updated its record: %+v", records[1])
	}
}

func TestImportEndsWithoutTheTextPostgres(t *testing.T) {
	fixture := seedProject(t, "ck_author_materials_end")
	ctx := context.Background()
	actor := fixture.editor.Actor

	failed := fixture.start(t, "key-1", fileHolds)
	failure := jobs.Error{Code: "author_materials.unreadable_answer", Message: "no"}
	if err := fixture.store.FinishImport(ctx, fixture.claimed(t, failed), service.ImportOutcome{Status: jobs.StatusFailed, Error: &failure}); err != nil {
		t.Fatal(err)
	}
	found, _, err := fixture.store.AuthorMaterialsImport(ctx, actor)
	if err != nil || found == nil || found.JobStatus != "failed" || found.Candidates != nil {
		t.Fatalf("failed = %+v, %v", found, err)
	}
	var left []byte
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT parsed FROM author_material_imports`).Scan(&left); err != nil || string(left) != "{}" {
		t.Fatalf("the file stayed: %s, %v", left, err)
	}

	// A new file replaces what waits; then the author stops it and nothing stays.
	stopped := fixture.start(t, "key-2", fileHolds)
	claim := fixture.claimed(t, stopped)
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE jobs SET status = 'canceling' WHERE id = $1`, stopped.JobID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.FinishImport(ctx, claim, service.ImportOutcome{Status: jobs.StatusCanceled}); err != nil {
		t.Fatal(err)
	}
	if gone, _, err := fixture.store.AuthorMaterialsImport(ctx, actor); err != nil || gone != nil {
		t.Fatalf("a stopped import stayed: %+v, %v", gone, err)
	}

	// The author discards what waits; a running Job is not discarded.
	waiting := fixture.start(t, "key-3", fileHolds)
	if err := fixture.store.DiscardAuthorMaterialsImport(ctx, fixture.editor, waiting.ID); !errors.Is(err, domain.ErrImportBusy) {
		t.Fatalf("discarding a running import: %v", err)
	}
	fixture.finished(t, waiting, []domain.Draft{skill("Ужас", "")})
	if err := fixture.store.DiscardAuthorMaterialsImport(ctx, fixture.viewer, waiting.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a viewer discards: %v", err)
	}
	if err := fixture.store.DiscardAuthorMaterialsImport(ctx, fixture.editor, waiting.ID); err != nil {
		t.Fatal(err)
	}
	records, err := fixture.store.AuthorMaterials(ctx, actor.ProjectID)
	if err != nil || len(records) != 0 {
		t.Fatalf("a discard wrote records: %+v, %v", records, err)
	}
}

func TestRecordsChangeAtTheirRevisionPostgres(t *testing.T) {
	fixture := seedProject(t, "ck_author_materials_records")
	ctx := context.Background()
	started := fixture.start(t, "key-1", fileHolds)
	fixture.finished(t, started, []domain.Draft{skill("Ужас", "пугает"), skill("Страх", "тоже")})
	fixture.confirm(t, started, "answer-1", domain.Choice{Index: 0}, domain.Choice{Index: 1})

	page, err := fixture.store.ListAuthorMaterials(ctx, fixture.viewer.Actor, domain.Filter{Limit: 1})
	if err != nil || len(page.Records) != 1 || page.Total != 2 || page.NextKey != "страх" || page.Records[0].Name != "Страх" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	next, err := fixture.store.ListAuthorMaterials(ctx, fixture.viewer.Actor, domain.Filter{Limit: 5, AfterKey: page.NextKey, Query: "пуга"})
	if err != nil || len(next.Records) != 1 || next.Records[0].Name != "Ужас" || next.NextKey != "" {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if _, err := fixture.store.ListAuthorMaterials(ctx, service.Actor{UserID: fixture.outsider.UserID, ProjectID: fixture.editor.ProjectID}, domain.Filter{Limit: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an outsider lists: %v", err)
	}
	horror := next.Records[0]
	changed := horror.Draft
	changed.Description = "пугает до смерти"
	if _, err := fixture.store.UpdateAuthorMaterial(ctx, fixture.viewer, horror.ID, 1, changed, "ужас"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a viewer changes: %v", err)
	}
	updated, err := fixture.store.UpdateAuthorMaterial(ctx, fixture.editor, horror.ID, 1, changed, "ужас")
	if err != nil || updated.Revision != 2 || updated.Description != "пугает до смерти" {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	if _, err := fixture.store.UpdateAuthorMaterial(ctx, fixture.editor, horror.ID, 1, changed, "ужас"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale revision: %v", err)
	}
	changed.Name = "Страх"
	// specs/2026-11-10-ux8-memory-records.md MR2.3: a name another record has names that record, and the change keeps nothing.
	var taken domain.NameTaken
	if _, err := fixture.store.UpdateAuthorMaterial(ctx, fixture.editor, horror.ID, 2, changed, "страх"); !errors.As(err, &taken) || taken.RecordID != page.Records[0].ID {
		t.Fatalf("a name another record has: %v", err)
	}
	if kept, err := fixture.store.ListAuthorMaterials(ctx, fixture.viewer.Actor, domain.Filter{Limit: 5, Query: "пугает до"}); err != nil || len(kept.Records) != 1 || kept.Records[0].Name != "Ужас" || kept.Records[0].Revision != 2 {
		t.Fatalf("after a taken name: %+v, %v", kept, err)
	}
	stamp := func() string {
		t.Helper()
		found, err := sagamemoryrepo.New(fixture.db).CardsStamp(ctx, fixture.editor.ProjectID, nil)
		if err != nil {
			t.Fatal(err)
		}
		return found.Materials
	}
	// R9.5, R9.6: the stamp of the map follows the records, and a deleted record takes the prepared cards with it, the graph stays.
	if _, err := fixture.db.Pool.Exec(ctx, `INSERT INTO saga_map_graphs (project_id, source_view, revision, document, chapters) VALUES ($1, 'published', 'r', '\x00', '\x00')`, fixture.editor.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `INSERT INTO saga_map_nodes (project_id, source_view, node_key, name, type, chapter_count, search_text, card) VALUES ($1, 'published', 'ужас', 'Ужас', 'навык', 0, 'ужас', '\x00')`, fixture.editor.ProjectID); err != nil {
		t.Fatal(err)
	}
	before := stamp()
	if err := fixture.store.DeleteAuthorMaterial(ctx, fixture.editor, horror.ID, 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale removal: %v", err)
	}
	if after := stamp(); after != before {
		t.Fatalf("a refused removal changed the stamp: %s", after)
	}
	if err := fixture.store.DeleteAuthorMaterial(ctx, fixture.editor, horror.ID, 2); err != nil {
		t.Fatal(err)
	}
	if after := stamp(); after == before {
		t.Fatalf("the stamp did not follow the records: %s", after)
	}
	var graphs, nodes int
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM saga_map_graphs WHERE project_id = $1), (SELECT count(*) FROM saga_map_nodes WHERE project_id = $1)`, fixture.editor.ProjectID).Scan(&graphs, &nodes); err != nil {
		t.Fatal(err)
	}
	if graphs != 1 || nodes != 0 {
		t.Fatalf("after the removal: %d graphs, %d nodes", graphs, nodes)
	}
}

// claimedPeek is a claim for reading a file before the Job runs.
func (fixture materialsProject) claimedPeek(started domain.Import) jobs.WorkerClaim {
	return jobs.WorkerClaim{ProjectID: fixture.editor.ProjectID, JobID: started.JobID, ClaimIncarnation: 1}
}

// R9.2, R9.8: an import counts against the daily imports of its author; over the limit the transaction leaves no import and no Job.
func TestImportKeepsTheDailyQuotaPostgres(t *testing.T) {
	fixture := seedProject(t, "ck_author_materials_quota")
	ctx := context.Background()
	limit := int64(1)
	if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := quotastore.SetOverridesInTx(ctx, tx, fixture.editor.UserID, []quota.OverrideChange{{Key: quota.KeyImportsPerDay, Value: &limit}}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// The first import ends and waits for its answer: a new one would replace it, if the day allowed.
	fixture.finished(t, fixture.start(t, "key-1", fileHolds), []domain.Draft{skill("Ужас", "пугает")})
	body, _ := json.Marshal(fileHolds)
	var exceeded *quota.Exceeded
	if _, err := fixture.store.StartAuthorMaterialsImport(ctx, startCommand(fixture.editor, "key-2", string(body)+" ")); !errors.As(err, &exceeded) || exceeded.Key != quota.KeyImportsPerDay {
		t.Fatalf("an import over the limit: %v", err)
	}
	var imports, jobs int
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM author_material_imports WHERE project_id = $1), (SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'author_materials_import')`,
		fixture.editor.ProjectID).Scan(&imports, &jobs); err != nil {
		t.Fatal(err)
	}
	if imports != 1 || jobs != 1 {
		t.Fatalf("%d imports, %d Jobs after a refused start; want only the first", imports, jobs)
	}
}

// specs/2026-11-09-quota-accounting.md Q5.2, Q5.3: an import whose Job failed — the model or the worker — gives back the
// upload of the day; one the author stopped keeps it; the start of model work needs a verified address.
func TestAFailedImportGivesItsUploadBackPostgres(t *testing.T) {
	fixture := seedProject(t, "ck_author_materials_quota_back")
	ctx := context.Background()
	uploads := func() int64 {
		t.Helper()
		var used int64
		if err := fixture.db.Pool.QueryRow(ctx, `SELECT COALESCE(sum(imports), 0) FROM writer_usage_days WHERE user_id = $1`, fixture.editor.UserID).Scan(&used); err != nil {
			t.Fatal(err)
		}
		return used
	}
	failed := fixture.start(t, "key-failed", fileHolds)
	failure := jobs.Error{Code: "author_materials.unreadable_answer", Message: "no"}
	if err := fixture.store.FinishImport(ctx, fixture.claimed(t, failed), service.ImportOutcome{Status: jobs.StatusFailed, Error: &failure}); err != nil {
		t.Fatal(err)
	}
	if used := uploads(); used != 0 {
		t.Fatalf("uploads after a failed import = %d, want 0", used)
	}
	stopped := fixture.start(t, "key-stopped", fileHolds)
	claim := fixture.claimed(t, stopped)
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE jobs SET status = 'canceling' WHERE id = $1`, stopped.JobID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.FinishImport(ctx, claim, service.ImportOutcome{Status: jobs.StatusCanceled}); err != nil {
		t.Fatal(err)
	}
	if used := uploads(); used != 1 {
		t.Fatalf("uploads after an import the author stopped = %d, want 1", used)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE users SET email_verified_at = NULL WHERE id = $1`, fixture.editor.UserID); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(fileHolds)
	if _, err := fixture.store.StartAuthorMaterialsImport(ctx, startCommand(fixture.editor, "key-unverified", string(body))); !errors.Is(err, auth.ErrEmailNotVerified) {
		t.Fatalf("an import of an unverified address = %v", err)
	}
}

func createCommand(write service.Write, key string, draft domain.Draft) service.CreateCommand {
	body, _ := json.Marshal(draft)
	return service.CreateCommand{
		Write: write, Draft: draft, Key: strings.ToLower(draft.Name), At: time.Now().UTC(),
		Replay: service.Replay{ScopeHash: "scope-" + write.UserID, OperationID: "author_materials.create.v1", KeyHash: key, RequestHash: string(body)},
	}
}

// specs/2026-11-10-ux8-memory-records.md MR2.1: an editor writes a record by hand, of the source «вручную»; a reader may not,
// an outsider does not learn the saga is there; a name is one record; the same request under its key is answered again and
// another under it is a conflict; the stamp of the map follows, as after a change.
func TestARecordByHandPostgres(t *testing.T) {
	fixture := seedProject(t, "ck_author_materials_by_hand")
	ctx := context.Background()
	stamp := func() string {
		t.Helper()
		found, err := sagamemoryrepo.New(fixture.db).CardsStamp(ctx, fixture.editor.ProjectID, nil)
		if err != nil {
			t.Fatal(err)
		}
		return found.Materials
	}
	before := stamp()
	raven := domain.Draft{Name: "Ворон", Kind: domain.KindCharacter, Description: "Наёмник", Properties: []domain.Property{{Key: "Ранг", Value: "B"}}}
	created, err := fixture.store.CreateAuthorMaterial(ctx, createCommand(fixture.editor, "key-1", raven))
	if err != nil || created.SourceKind != domain.SourceManual || created.SourceName != "" || created.Name != "Ворон" || created.Revision != 1 {
		t.Fatalf("created = %+v, %v", created, err)
	}
	if after := stamp(); after == before {
		t.Fatalf("the stamp of the map did not follow a new record: %s", after)
	}
	if again, err := fixture.store.CreateAuthorMaterial(ctx, createCommand(fixture.editor, "key-1", raven)); err != nil || again.ID != created.ID {
		t.Fatalf("the same request again = %+v, %v", again, err)
	}
	fox := domain.Draft{Name: "Лиса", Kind: domain.KindCharacter}
	if _, err := fixture.store.CreateAuthorMaterial(ctx, createCommand(fixture.editor, "key-1", fox)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("another request under the key: %v", err)
	}
	var taken domain.NameTaken
	if _, err := fixture.store.CreateAuthorMaterial(ctx, createCommand(fixture.editor, "key-2", domain.Draft{Name: "Ворон", Kind: domain.KindPlace})); !errors.As(err, &taken) || taken.RecordID != created.ID {
		t.Fatalf("a taken name: %v", err)
	}
	if _, err := fixture.store.CreateAuthorMaterial(ctx, createCommand(fixture.viewer, "key-3", fox)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader writes: %v", err)
	}
	outsider := fixture.outsider
	outsider.ProjectID = fixture.editor.ProjectID
	if _, err := fixture.store.CreateAuthorMaterial(ctx, createCommand(outsider, "key-4", fox)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an outsider writes: %v", err)
	}
	stale := fixture.editor
	stale.Proof.CSRFSecretHash = "stale"
	if _, err := fixture.store.CreateAuthorMaterial(ctx, createCommand(stale, "key-5", fox)); err == nil {
		t.Fatal("a write without its proof")
	}
	page, err := fixture.store.ListAuthorMaterials(ctx, fixture.viewer.Actor, domain.Filter{Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("records = %+v, %v", page, err)
	}
}
