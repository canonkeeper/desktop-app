package library

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	libraryservice "canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	librarystore "canonkeeper/backend/internal/store/library"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

const (
	coverOwnerID       = "00000000-0000-4000-8000-000000007101"
	coverViewerID      = "00000000-0000-4000-8000-000000007102"
	coverOutsiderID    = "00000000-0000-4000-8000-000000007103"
	coverProjectID     = "00000000-0000-4000-8000-000000007104"
	coverBookID        = "00000000-0000-4000-8000-000000007105"
	coverArtifactOne   = "00000000-0000-4000-8000-000000007106"
	coverArtifactTwo   = "00000000-0000-4000-8000-000000007107"
	coverArtifactThree = "00000000-0000-4000-8000-000000007108"
)

func TestCoverRepositoryEnforcesVersionActorAndLifecycleState(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_book_cover")
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	seedCoverRepository(t, db, now)
	repository := New(db)
	ctx := context.Background()

	first, err := repository.ActivateCover(ctx, coverActivation(coverOwnerID, coverArtifactOne, "book-covers/one.final", now))
	if err != nil {
		t.Fatal(err)
	}
	if first.Current.Version != 1 || first.Superseded != nil {
		t.Fatalf("first activation = %#v", first)
	}
	book, err := librarystore.New(db).GetBook(ctx, coverProjectID, coverBookID)
	if err != nil || book.CoverURL == nil || *book.CoverURL != "/api/v1/books/"+coverBookID+"/cover/1" {
		t.Fatalf("book cover projection = %#v, %v", book.CoverURL, err)
	}
	read, err := repository.FindCover(ctx, domainlibrary.CoverLookup{
		Actor: domainlibrary.Actor{UserID: coverViewerID}, BookID: coverBookID, Version: 1,
	})
	if err != nil || read.ID != coverArtifactOne {
		t.Fatalf("viewer read = %#v, %v", read, err)
	}
	if _, err := repository.FindCover(ctx, domainlibrary.CoverLookup{
		Actor: domainlibrary.Actor{UserID: coverOutsiderID}, BookID: coverBookID, Version: 1,
	}); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("outsider read error = %v, want ErrNotFound", err)
	}

	second, err := repository.ActivateCover(ctx, coverActivation(coverOwnerID, coverArtifactTwo, "book-covers/two.final", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if second.Current.Version != 2 || second.Superseded == nil || second.Superseded.ID != coverArtifactOne || second.Superseded.State != domainlibrary.CoverSuperseded {
		t.Fatalf("replacement activation = %#v", second)
	}
	if _, err := repository.FindCover(ctx, domainlibrary.CoverLookup{
		Actor: domainlibrary.Actor{UserID: coverViewerID}, BookID: coverBookID, Version: 1,
	}); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("stale version read error = %v, want ErrNotFound", err)
	}
	pending, err := repository.NextCoverDeletion(ctx)
	if err != nil || pending.ID != coverArtifactOne {
		t.Fatalf("superseded deletion intent = %#v, %v", pending, err)
	}
	if err := repository.CompleteCoverDeletion(ctx, pending, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RemoveCover(ctx, domainlibrary.RemoveCoverInput{
		Actor: domainlibrary.Actor{UserID: coverViewerID}, BookID: coverBookID, At: now.Add(2 * time.Second),
	}); !errors.Is(err, domainlibrary.ErrForbidden) {
		t.Fatalf("viewer remove error = %v, want ErrForbidden", err)
	}
	removed, err := repository.RemoveCover(ctx, domainlibrary.RemoveCoverInput{
		Actor: domainlibrary.Actor{UserID: coverOwnerID}, BookID: coverBookID, At: now.Add(2 * time.Second),
	})
	if err != nil || removed.State != domainlibrary.CoverRemoved {
		t.Fatalf("owner remove = %#v, %v", removed, err)
	}
	pending, err = repository.NextCoverDeletion(ctx)
	if err != nil || pending.ID != coverArtifactTwo {
		t.Fatalf("removed deletion intent = %#v, %v", pending, err)
	}
	if err := repository.CompleteCoverDeletion(ctx, pending, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	third, err := repository.ActivateCover(ctx, coverActivation(coverOwnerID, coverArtifactThree, "book-covers/three.final", now.Add(3*time.Second)))
	if err != nil || third.Current.Version != 3 {
		t.Fatalf("post-remove activation = %#v, %v", third, err)
	}
	softDeleteCoverBook(t, db, now.Add(4*time.Second))
	var deletedCoverGeneration int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT book_lifecycle_generation FROM book_cover_artifacts WHERE id = $1
	`, coverArtifactThree).Scan(&deletedCoverGeneration); err != nil || deletedCoverGeneration != 1 {
		t.Fatalf("soft-deleted cover generation = %d, %v, want retained generation 1", deletedCoverGeneration, err)
	}
	if _, err := repository.FindCover(ctx, domainlibrary.CoverLookup{
		Actor: domainlibrary.Actor{UserID: coverViewerID}, BookID: coverBookID, Version: 3,
	}); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("soft-deleted book read error = %v, want ErrNotFound", err)
	}
	restoreCoverBook(t, db)
	if _, err := repository.FindCover(ctx, domainlibrary.CoverLookup{
		Actor: domainlibrary.Actor{UserID: coverViewerID}, BookID: coverBookID, Version: 3,
	}); err != nil {
		t.Fatalf("restored book read error = %v", err)
	}
}

func coverActivation(actorID, artifactID, key string, at time.Time) domainlibrary.ActivateCoverInput {
	return domainlibrary.ActivateCoverInput{
		Actor: domainlibrary.Actor{UserID: actorID}, ArtifactID: artifactID, BookID: coverBookID,
		StorageKey: key, MIMEType: "image/jpeg", ByteSize: 1024,
		Width: domainlibrary.MinCoverWidth, Height: domainlibrary.MinCoverHeight,
		ContentSHA256: sha256.Sum256([]byte(key)), At: at,
	}
}

func seedCoverRepository(t *testing.T, db *storedb.DB, now time.Time) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,$3,'hash',$4)", []any{coverOwnerID, "cover-owner@example.test", "Owner", now}},
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,$3,'hash',$4)", []any{coverViewerID, "cover-viewer@example.test", "Viewer", now}},
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,$3,'hash',$4)", []any{coverOutsiderID, "cover-outsider@example.test", "Outsider", now}},
		{"INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'Cover Project','',$3,$3)", []any{coverProjectID, coverOwnerID, now}},
		{"INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ('00000000-0000-4000-8000-000000007109',$1,$2,'owner',$3,$3)", []any{coverProjectID, coverOwnerID, now}},
		{"INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ('00000000-0000-4000-8000-000000007110',$1,$2,'viewer',$3,$3)", []any{coverProjectID, coverViewerID, now}},
		{"INSERT INTO books (id,project_id,title,\"order\",display_number,status,revision,created_at,updated_at) VALUES ($1,$2,'Cover Book',1,'I','ready',1,$3,$3)", []any{coverBookID, coverProjectID, now}},
	} {
		if _, err := db.Pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func softDeleteCoverBook(t *testing.T, db *storedb.DB, deletedAt time.Time) {
	t.Helper()
	_, err := librarystore.New(db).FenceBookDeletion(context.Background(), libraryservice.FenceBookDeletionInput{
		ActorUserID: coverOwnerID, ProjectID: coverProjectID, BookID: coverBookID,
		TombstoneID: "00000000-0000-4000-8000-000000007111", DeletedAt: deletedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func restoreCoverBook(t *testing.T, db *storedb.DB) {
	t.Helper()
	err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `SET LOCAL ROLE canonkeeper_product_runtime`); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), `
			UPDATE books
			SET deleted_at = NULL, governance_retention_started_at = NULL,
			    purge_after = NULL, purge_deadline = NULL
			WHERE id = $1
		`, coverBookID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
