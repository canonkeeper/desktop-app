package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/notes"
	service "canonkeeper/backend/internal/services/notes"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

func testID(n int) string           { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func testText(value string) *string { return &value }
func seedAccounts(t *testing.T, db *storedb.DB) []service.Write {
	t.Helper()
	actors := make([]service.Write, 2)
	for index := range actors {
		actors[index] = service.Write{UserID: testID(index + 1), Proof: auth.SessionWriteProof{
			SessionHash: strings.Repeat(fmt.Sprintf("%x", index+1), 64), CSRFSecretHash: strings.Repeat(fmt.Sprintf("%x", index+3), 64)}}
		_, err := db.Pool.Exec(context.Background(), `INSERT INTO users(id,email,display_name,password_hash,email_verified_at) VALUES($1,$2,'Notes','fixture-hash',clock_timestamp())`, actors[index].UserID, fmt.Sprintf("notes%d@example.test", index))
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Pool.Exec(context.Background(), `INSERT INTO sessions(id,user_id,session_hash,csrf_secret_hash,expires_at) VALUES($1,$2,$3,$4,clock_timestamp()+interval '1 hour')`, testID(index+11), actors[index].UserID, actors[index].Proof.SessionHash, actors[index].Proof.CSRFSecretHash)
		if err != nil {
			t.Fatal(err)
		}
	}
	return actors
}
func TestPersonalNotesPrivacyReplayRevisionAndSessionFence(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_personal_notes")
	actors := seedAccounts(t, db)
	repo := New(db)
	ctx := context.Background()
	create := service.Create{Repository: repo}
	input := service.CreateInput{Body: "Личная копия"}
	first, err := create.Execute(ctx, actors[0], input, "request-1", "POST", "/personal-notes")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := create.Execute(ctx, actors[0], input, "request-1", "POST", "/personal-notes")
	if err != nil || replay.ID != first.ID {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	if _, err = create.Execute(ctx, actors[0], service.CreateInput{Body: "different"}, "request-1", "POST", "/personal-notes"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed replay = %v", err)
	}
	own, err := create.Execute(ctx, actors[1], input, "request-1", "POST", "/personal-notes")
	if err != nil || own.ID == first.ID {
		t.Fatalf("account-scoped replay = %#v, %v", own, err)
	}
	if _, err = repo.Get(ctx, actors[1].UserID, first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign read = %v", err)
	}
	if _, err = repo.Update(ctx, actors[1], first.ID, service.UpdateInput{Body: testText("stolen"), ExpectedRevision: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign update = %v", err)
	}
	// An empty thought is a thought beside a quote only (specs/2026-11-11-ux7-notes.md NT1.2): another account's note is
	// still not found, an own note without a quote is refused and kept.
	if _, err = (service.Update{Repository: repo}).Execute(ctx, actors[1], first.ID, service.UpdateInput{Body: testText(""), ExpectedRevision: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign empty thought = %v", err)
	}
	if _, err = (service.Update{Repository: repo}).Execute(ctx, actors[0], first.ID, service.UpdateInput{Body: testText(" "), ExpectedRevision: 1}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("own note emptied = %v", err)
	}
	if kept, err := repo.Get(ctx, actors[0].UserID, first.ID); err != nil || kept.Body != "Личная копия" || kept.Revision != 1 {
		t.Fatalf("refused empty thought changed the note = %#v, %v", kept, err)
	}
	if err = repo.Delete(ctx, actors[1], first.ID, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign delete = %v", err)
	}
	if _, err = repo.ReadSource(ctx, actors[1].UserID, first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign source = %v", err)
	}
	updated, err := repo.Update(ctx, actors[0], first.ID, service.UpdateInput{Body: testText("Изменено"), ExpectedRevision: 1})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	if _, err = repo.Update(ctx, actors[0], first.ID, service.UpdateInput{Body: testText("stale"), ExpectedRevision: 1}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale update = %v", err)
	}
	if err = repo.Delete(ctx, actors[0], first.ID, 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale delete = %v", err)
	}
	page, err := (service.List{Repository: repo}).Execute(ctx, actors[0].UserID, service.ListInput{})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != first.ID {
		t.Fatalf("owner list = %#v, %v", page, err)
	}
	if err = repo.Delete(ctx, actors[0], first.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Get(ctx, actors[0].UserID, first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted read = %v", err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE user_id=$1`, actors[1].UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = create.Execute(ctx, actors[1], input, "request-2", "POST", "/personal-notes"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("revoked write = %v", err)
	}
	if _, err = (service.Update{Repository: repo}).Execute(ctx, actors[1], own.ID, service.UpdateInput{Body: testText(""), ExpectedRevision: 1}); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("revoked empty thought = %v", err)
	}
}
