package researchchat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/authormemory"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	authormemoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	service "canonkeeper/backend/internal/services/researchchat"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	canonstore "canonkeeper/backend/internal/store/canon"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// drawingCall is a tool call of the root thread of the turn the read authority fixtures seed.
var drawingCall = service.ToolContext{
	Caller: agent.ToolCaller{ThreadID: "root-rch-1", RootThreadID: "root-rch-1"},
	TurnID: "provider-turn-rch-1",
}

func memoryTools(db *storedb.DB, repository *Repository) *service.AuthorMemoryTools {
	return service.NewAuthorMemoryTools(repository, sagamemoryservice.NewSaga(sagamemoryrepo.New(db), sagamemoryrepo.New(db)))
}

// specs/2026-11-01-writer-assistant-index.md, R4.1, R4.2, R4.6: what the author said in one chat is known in
// a new chat; deleting its chapter keeps the record and says the chapter is gone.
func TestWhatIsRememberedIsKnownInANewChatAndOutlivesItsChapterPostgres(t *testing.T) {
	turn := startSagaToolsTurn(t, "ck_rch_author_memory", researchdomain.ScopeProject)
	db, repository, ctx := turn.db, turn.repository, context.Background()
	tools := memoryTools(db, repository)
	version, err := sagamemoryservice.NewSaga(sagamemoryrepo.New(db), sagamemoryrepo.New(db)).ChapterVersion(ctx, submitTurnProjectID, string(governance.SourceViewDraft))
	if err != nil {
		t.Fatal(err)
	}
	var key string
	for index := range version.Chapters {
		if version.Chapters[index].ID == turn.chapterID {
			key = version.Chapters[index].Key
		}
	}
	if key == "" {
		t.Fatalf("no key of the chapter among %d", len(version.Chapters))
	}
	saved, err := tools.Remember(ctx, turn.call, service.RememberInput{Kind: "plan", Text: "Кира узнает правду", Chapter: key, Name: "Кира"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved, "план автора, в книге этого ещё нет") {
		t.Fatalf("remember = %s", saved)
	}
	var author string
	if err := db.Pool.QueryRow(ctx, `SELECT created_by::text FROM author_memory WHERE project_id=$1::uuid`, submitTurnProjectID).Scan(&author); err != nil || author != submitTurnActorID {
		t.Fatalf("the record is of %q, %v", author, err)
	}
	// specs/2026-11-02-writer-workshop.md, Decision log 03.11: the chat finds what its turn remembered, to undo it.
	ofTurn := func(jobID string) int {
		t.Helper()
		var page authormemory.Page
		if err := db.WithTx(ctx, func(tx pgx.Tx) error {
			var err error
			page, err = authormemoryrepo.ListInTx(ctx, tx, submitTurnProjectID, authormemory.Filter{JobID: jobID, Limit: 10})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return len(page.Records)
	}
	authority, err := repository.ReadAuthority(ctx, turn.call)
	if err != nil {
		t.Fatal(err)
	}
	if ofTurn(authority.JobID) != 1 || ofTurn("ab000000-0000-4000-8000-0000000000ff") != 0 {
		t.Fatal("the record does not keep the turn that wrote it")
	}

	another := startTurnInChat(t, db, turn.proof, turn.now, turn.chapterID, "ab000000-0000-4000-8000-0000000000a1", researchdomain.ScopeProject, "Что дальше?")
	read, err := memoryTools(db, repository).Memory(ctx, another.call, service.MemoryInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read, "Кира узнает правду") || !strings.Contains(read, "глава "+key) || !strings.Contains(read, "план автора") {
		t.Fatalf("a new chat reads: %s", read)
	}

	deleteSourceForTest(t, db, governance.DeletionSelectionChapter, governance.SourceGenerationVector{
		ProjectID: submitTurnProjectID, ProjectGeneration: 1, BookID: submitTurnBookID, BookGeneration: 1,
		ChapterID: turn.chapterID, ChapterGeneration: 1,
	}, turn.chapterID)
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM author_memory WHERE project_id=$1::uuid`, submitTurnProjectID).Scan(&left); err != nil || left != 1 {
		t.Fatalf("records after the chapter went: %d, %v", left, err)
	}
	read, err = memoryTools(db, repository).Memory(ctx, another.call, service.MemoryInput{Kind: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read, "Кира узнает правду") || !strings.Contains(read, "глава удалена") {
		t.Fatalf("after the chapter went: %s", read)
	}
}

// R4.5: a member who has become a reader writes nothing: his turn has lost its authority over the saga.
func TestADowngradedAuthorWritesNothingPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_author_memory_reader")
	seedReadAuthority(t, db, time.Now().UTC().Truncate(time.Microsecond))
	ctx := context.Background()
	repository := New(db)
	tools := memoryTools(db, repository)
	if _, err := tools.Remember(ctx, drawingCall, service.RememberInput{Kind: "correction", Text: "Ей двадцать лет"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role='viewer' WHERE id=$1::uuid`, authorityMembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Remember(ctx, drawingCall, service.RememberInput{Kind: "plan", Text: "нельзя"}); !errors.Is(err, researchdomain.ErrForbidden) {
		t.Fatalf("a reader writes: %v", err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM author_memory`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("records = %d, %v", count, err)
	}
}

// deleteSourceForTest fences a deleted book or chapter and hides it, as the deletion does in its fence.
func deleteSourceForTest(t *testing.T, db *storedb.DB, kind governance.DeletionSelectionKind, vector governance.SourceGenerationVector, chapterIDs ...string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		store := canonstore.New(db)
		if err := store.RevokeExactSourceArtifactOwnersAtFence(ctx, tx, kind, vector, now); err != nil {
			return err
		}
		if err := store.ScrubCanonicalDerivedSourceInTx(ctx, tx, kind, vector, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		hide := func(table string, id string) error {
			_, err := tx.Exec(ctx, `UPDATE `+table+`
				SET deleted_at=$2, governance_retention_started_at=$2, purge_after=$3, purge_deadline=$4
				WHERE id=$1::uuid`, id, now, now.Add(7*24*time.Hour), now.Add(8*24*time.Hour))
			return err
		}
		if kind == governance.DeletionSelectionBook {
			if err := hide("books", vector.BookID); err != nil {
				return err
			}
		}
		for _, chapterID := range chapterIDs {
			if err := hide("chapters", chapterID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM chapter_source_members WHERE chapter_id=$1::uuid`, chapterID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
