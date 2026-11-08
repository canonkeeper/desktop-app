package researchchat

import (
	"context"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/authormaterials"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
	authormaterialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"
)

func keepMaterial(t *testing.T, db *storedb.DB, actor string, draft authormaterials.Draft) {
	t.Helper()
	project, err := storedb.ParseUUID(submitTurnProjectID)
	if err != nil {
		t.Fatal(err)
	}
	user, err := storedb.ParseUUID(actor)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlc.New(db.Pool).UpsertAuthorMaterial(context.Background(), sqlc.UpsertAuthorMaterialParams{
		ProjectID: project, Name: draft.Name, NameKey: sagamemory.GlossaryKey(draft.Name), Kind: string(draft.Kind), Description: draft.Description,
		Properties: []byte(`[{"key":"Ранг","value":"Уникальный"}]`), Section: draft.Section, SourceName: "навыки.xlsx", SourceKind: "xlsx", Actor: user,
	}); err != nil {
		t.Fatal(err)
	}
}

// specs/2026-11-04-saga-map.md R9.7: a record is signed as a plan of the author until the book knows its name, and it is
// no chapter: reading it retains nothing for the turn.
func TestToolsSignTheMaterialsAndRetainNothingForThemPostgres(t *testing.T) {
	turn := startSagaToolsTurn(t, "ck_rch_author_materials", researchdomain.ScopeProject)
	db, ctx, call := turn.db, context.Background(), turn.call
	var author string
	if err := db.Pool.QueryRow(ctx, `SELECT owner_user_id::text FROM projects WHERE id = $1::uuid`, submitTurnProjectID).Scan(&author); err != nil {
		t.Fatal(err)
	}
	keepMaterial(t, db, author, authormaterials.Draft{Name: "омут снов", Kind: authormaterials.KindSkill, Description: "Сны становятся явью и пугают стражу."})
	tools := turn.tools.WithMaterials(authormaterialsrepo.New(db, jobstore.New(db)))
	retained := func() int {
		t.Helper()
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM artifact_source_refs WHERE project_id = $1::uuid AND chapter_id IS NOT NULL`, submitTurnProjectID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := retained()

	entity, err := tools.Entity(ctx, call, service.EntityInput{Name: "ОМУТ снов"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"омут снов (навык; план автора, в книге ещё нет)", "Сны становятся явью", "Ранг: Уникальный", "Источник: навыки.xlsx"} {
		if !strings.Contains(entity, want) {
			t.Fatalf("entity = %q, no %q", entity, want)
		}
	}
	glossary, err := tools.Glossary(ctx, call, service.GlossaryInput{Query: "омут"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(glossary, "омут снов (навык; план автора, в книге ещё нет) — Сны становятся явью") {
		t.Fatalf("glossary = %q", glossary)
	}
	summary, err := tools.Glossary(ctx, call, service.GlossaryInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "Материалов автора: 1") {
		t.Fatalf("glossary without a query = %q", summary)
	}
	found, err := tools.Search(ctx, call, service.SearchInput{Queries: []string{"стражу"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found, "омут снов (навык; план автора") {
		t.Fatalf("search = %q", found)
	}
	if got := retained(); got != before {
		t.Fatalf("reading a record retained chapters: %d, were %d", got, before)
	}
}
