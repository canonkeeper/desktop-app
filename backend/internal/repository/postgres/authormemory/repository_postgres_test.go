package authormemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/authormemory"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

type memoryProject struct {
	db       *storedb.DB
	store    *Repository
	project  string
	foreign  string
	owner    service.Write
	editor   service.Write
	viewer   service.Write
	outsider service.Write
}

func seedMemoryProject(t *testing.T, database string) memoryProject {
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
		return user.ID, proof
	}
	ownerID, ownerProof := register("owner@example.test")
	editorID, editorProof := register("editor@example.test")
	viewerID, viewerProof := register("viewer@example.test")
	outsiderID, outsiderProof := register("outsider@example.test")
	service0 := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{})
	project, err := service0.CreateProject(ctx, ownerID, projects.CreateProjectInput{Title: "Сага"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := service0.CreateProject(ctx, outsiderID, projects.CreateProjectInput{Title: "Чужая сага"})
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
	return memoryProject{
		db: db, store: New(db), project: project.ID, foreign: foreign.ID,
		owner: write(ownerID, ownerProof), editor: write(editorID, editorProof),
		viewer: write(viewerID, viewerProof), outsider: write(outsiderID, outsiderProof),
	}
}

func createCommand(write service.Write, key, request string, draft domain.Draft) service.CreateCommand {
	return service.CreateCommand{
		Write: write, Draft: draft, ScopeHash: "scope-" + write.UserID, OperationID: "author_memory.create.v1",
		KeyHash: key, RequestHash: request, At: time.Now().UTC(),
	}
}

func plan(text string) domain.Draft { return domain.Draft{Kind: domain.KindPlan, Text: text} }

// specs/2026-11-01-writer-assistant-index.md, R4.5: an editor and above write, a reader only reads, someone
// outside the project does not learn it exists.
func TestAuthorMemoryRightsPostgres(t *testing.T) {
	fixture := seedMemoryProject(t, "ck_author_memory_rights")
	ctx := context.Background()
	store := fixture.store
	first, err := store.CreateAuthorMemory(ctx, createCommand(fixture.editor, "key-1", "request-1", plan("Кира узнает правду")))
	if err != nil {
		t.Fatalf("an editor writes: %v", err)
	}
	if _, err := store.CreateAuthorMemory(ctx, createCommand(fixture.owner, "key-2", "request-2", plan("Брат вернётся"))); err != nil {
		t.Fatalf("the owner writes: %v", err)
	}
	if _, err := store.CreateAuthorMemory(ctx, createCommand(fixture.viewer, "key-3", "request-3", plan("нельзя"))); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader writes: %v", err)
	}
	if _, err := store.UpdateAuthorMemory(ctx, fixture.viewer, first.ID, 1, plan("нельзя")); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader changes: %v", err)
	}
	if err := store.DeleteAuthorMemory(ctx, fixture.viewer, first.ID, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader removes: %v", err)
	}
	page, err := store.ListAuthorMemory(ctx, fixture.viewer.Actor, domain.Filter{Limit: 10})
	if err != nil || page.Total != 2 || len(page.Records) != 2 || page.Records[0].Text != "Брат вернётся" {
		t.Fatalf("a reader reads: %+v, %v", page, err)
	}
	if page.Records[1].AuthorName != "editor@example.test" || page.Records[1].CreatedAt.IsZero() {
		t.Fatalf("who and when: %+v", page.Records[1])
	}
	// specs/2026-11-10-ux8-memory-records.md MR1.5: a record written on the page says so, and a saga of several members signs
	// its records with their author; a saga of one does not.
	if !page.Shared || page.Records[1].Origin != (domain.Origin{Kind: domain.OriginManual}) {
		t.Fatalf("the origin and the authors of a shared saga: %+v", page)
	}
	alone := service.Actor{UserID: fixture.outsider.UserID, ProjectID: fixture.foreign}
	if own, err := store.ListAuthorMemory(ctx, alone, domain.Filter{Limit: 10}); err != nil || own.Shared {
		t.Fatalf("a saga of one member: %+v, %v", own, err)
	}
	for name, attempt := range map[string]func() error{
		"read": func() error {
			_, err := store.ListAuthorMemory(ctx, fixture.outsider.Actor, domain.Filter{Limit: 10})
			return err
		},
		"create": func() error {
			_, err := store.CreateAuthorMemory(ctx, createCommand(fixture.outsider, "key-4", "request-4", plan("нельзя")))
			return err
		},
		"update": func() error {
			_, err := store.UpdateAuthorMemory(ctx, fixture.outsider, first.ID, 1, plan("нельзя"))
			return err
		},
		"delete": func() error { return store.DeleteAuthorMemory(ctx, fixture.outsider, first.ID, 1) },
		"access": func() error {
			_, err := store.CanWriteAuthorMemory(ctx, fixture.outsider.UserID, fixture.project)
			return err
		},
	} {
		if err := attempt(); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("an outsider %s: %v", name, err)
		}
	}
	for write, want := range map[service.Write]bool{fixture.editor: true, fixture.owner: true, fixture.viewer: false} {
		if got, err := store.CanWriteAuthorMemory(ctx, write.UserID, fixture.project); err != nil || got != want {
			t.Fatalf("%s can write = %v, %v", write.UserID, got, err)
		}
	}
}

// A record is reached only through its project: the owner of another project does not reach it through his.
func TestAuthorMemoryTenantPredicatePostgres(t *testing.T) {
	fixture := seedMemoryProject(t, "ck_author_memory_tenant")
	ctx := context.Background()
	record, err := fixture.store.CreateAuthorMemory(ctx, createCommand(fixture.owner, "key-1", "request-1", plan("Кира узнает правду")))
	if err != nil {
		t.Fatal(err)
	}
	theirs := fixture.outsider
	theirs.ProjectID = fixture.foreign
	if _, err := fixture.store.UpdateAuthorMemory(ctx, theirs, record.ID, 1, plan("чужое")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("through another project, a change = %v", err)
	}
	if err := fixture.store.DeleteAuthorMemory(ctx, theirs, record.ID, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("through another project, a removal = %v", err)
	}
	page, err := fixture.store.ListAuthorMemory(ctx, theirs.Actor, domain.Filter{Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("through another project, a list = %+v, %v", page, err)
	}
}

// The replay of a request returns the record it made, another request under the key is a conflict, a stale
// revision changes nothing, and a stale write proof does not pass.
func TestAuthorMemoryReplayAndRevisionsPostgres(t *testing.T) {
	fixture := seedMemoryProject(t, "ck_author_memory_replay")
	ctx := context.Background()
	store := fixture.store
	first, err := store.CreateAuthorMemory(ctx, createCommand(fixture.editor, "key-1", "request-1", plan("один")))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateAuthorMemory(ctx, createCommand(fixture.editor, "key-1", "request-1", plan("один")))
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	if _, err := store.CreateAuthorMemory(ctx, createCommand(fixture.editor, "key-1", "request-2", plan("другой"))); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("another request under the key = %v", err)
	}
	page, err := store.ListAuthorMemory(ctx, fixture.editor.Actor, domain.Filter{Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("records = %+v, %v", page, err)
	}
	changed, err := store.UpdateAuthorMemory(ctx, fixture.owner, first.ID, 1, domain.Draft{Kind: domain.KindStyleRule, Text: "короткие главы", Name: "Кира"})
	if err != nil || changed.Revision != 2 || changed.Kind != domain.KindStyleRule || changed.Text != "короткие главы" || changed.Name != "Кира" ||
		changed.AuthorName != "editor@example.test" {
		t.Fatalf("change = %+v, %v", changed, err)
	}
	if _, err := store.UpdateAuthorMemory(ctx, fixture.editor, first.ID, 1, plan("устарело")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale change = %v", err)
	}
	if err := store.DeleteAuthorMemory(ctx, fixture.editor, first.ID, 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale removal = %v", err)
	}
	stale := fixture.editor
	stale.Proof.CSRFSecretHash = "other"
	if err := store.DeleteAuthorMemory(ctx, stale, first.ID, 2); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a stale write proof = %v", err)
	}
	if err := store.DeleteAuthorMemory(ctx, fixture.editor, first.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAuthorMemory(ctx, fixture.editor, first.ID, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a second removal = %v", err)
	}
}

// The filters and the pages of the list; a plan of a chapter reads its kinds only.
func TestAuthorMemoryListAndPlanKindsPostgres(t *testing.T) {
	fixture := seedMemoryProject(t, "ck_author_memory_list")
	ctx := context.Background()
	store := fixture.store
	for index, draft := range []domain.Draft{
		{Kind: domain.KindPlan, Text: "Кира узнает правду"},
		{Kind: domain.KindCorrection, Text: "Ей двадцать лет", Name: "КИРА"},
		{Kind: domain.KindStyleRule, Text: "Короткие главы"},
		{Kind: domain.KindRejected, Text: "Брат не возвращается"},
	} {
		key := string(rune('a' + index))
		if _, err := store.CreateAuthorMemory(ctx, createCommand(fixture.editor, "key-"+key, "request-"+key, draft)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListAuthorMemory(ctx, fixture.viewer.Actor, domain.Filter{Limit: 3})
	if err != nil || !first.HasMore || len(first.Records) != 3 || first.Total != 4 || first.Records[0].Kind != domain.KindRejected {
		t.Fatalf("the first page = %+v, %v", first, err)
	}
	last := first.Records[2]
	second, err := store.ListAuthorMemory(ctx, fixture.viewer.Actor, domain.Filter{Limit: 3, Cursor: &domain.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}})
	if err != nil || second.HasMore || len(second.Records) != 1 || second.Total != 4 || second.Records[0].Kind != domain.KindPlan {
		t.Fatalf("the second page = %+v, %v", second, err)
	}
	found, err := store.ListAuthorMemory(ctx, fixture.viewer.Actor, domain.Filter{Limit: 10, Query: "кира"})
	if err != nil || found.Total != 2 {
		t.Fatalf("the search = %+v, %v", found, err)
	}
	kinds, err := store.ListAuthorMemory(ctx, fixture.viewer.Actor, domain.Filter{Limit: 10, Kinds: []domain.Kind{domain.KindCorrection}})
	if err != nil || kinds.Total != 1 || kinds.Records[0].Name != "КИРА" {
		t.Fatalf("the kind = %+v, %v", kinds, err)
	}
}

// A project being deleted is not read, whoever asks; its records stay until the purge, which removes them
// (TestProjectHardPurgeRemovesTheMemoryOfTheAuthor).
func TestAuthorMemoryOfADeletedProjectIsNotReadPostgres(t *testing.T) {
	fixture := seedMemoryProject(t, "ck_author_memory_project")
	ctx := context.Background()
	if _, err := fixture.store.CreateAuthorMemory(ctx, createCommand(fixture.owner, "key-1", "request-1", plan("Кира узнает правду"))); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE projects SET deleted_at = now(), governance_retention_started_at = now(),
			  purge_after = now() + interval '7 days', purge_deadline = now() + interval '8 days'
			WHERE id = $1
		`, fixture.project)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ListAuthorMemory(ctx, fixture.owner.Actor, domain.Filter{Limit: 10}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a deleted project is read: %v", err)
	}
	if _, err := fixture.store.CreateAuthorMemory(ctx, createCommand(fixture.owner, "key-2", "request-2", plan("после"))); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a deleted project is written: %v", err)
	}
}
