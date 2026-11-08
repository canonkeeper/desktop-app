package deletion

import (
	"bytes"
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

func TestFilesystemDeletionStoreRestrictsBookCoversToSourceHardPurge(t *testing.T) {
	const (
		actorID    = "10000000-0000-4000-8000-000000008001"
		projectID  = "10000000-0000-4000-8000-000000008002"
		bookID     = "10000000-0000-4000-8000-000000008003"
		artifactID = "10000000-0000-4000-8000-000000008004"
	)
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_cover_filesystem")
	derivedRequest := sourceDeletionTargetRequestForTest(t, governance.DeletionSelectionBook, projectID, bookID, "")
	derivedRequest.Claim.Target = governance.TargetFilesystem
	hardVector, err := governance.BookDeletionVector(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1, BookID: bookID, BookGeneration: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	hardRequest := derivedRequest
	hardRequest.Claim.TombstoneID = "50000000-0000-4000-8000-000000000005"
	hardRequest.Tombstone.Identity.TombstoneID = hardRequest.Claim.TombstoneID
	hardRequest.Tombstone.Identity.Mode = governance.PurgeModeSourceHardPurge
	hardRequest.Tombstone.Identity.SelectedVector = hardVector
	ctx := context.Background()

	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO users (id,email,display_name,password_hash,email_verified_at)
				VALUES ($1,'cover-delete@example.test','Cover Delete','hash',$2)`, []any{actorID, derivedRequest.Tombstone.Identity.FencedAt}},
			{`INSERT INTO projects (id,owner_user_id,name,description,lifecycle_generation,created_at,updated_at)
				VALUES ($1,$2,'Cover Delete','',1,$3,$3)`, []any{projectID, actorID, derivedRequest.Tombstone.Identity.FencedAt}},
			{`INSERT INTO books (id,project_id,title,"order",display_number,status,revision,lifecycle_generation,created_at,updated_at)
				VALUES ($1,$2,'Cover Delete',1,'I','ready',1,3,$3,$3)`, []any{bookID, projectID, derivedRequest.Tombstone.Identity.FencedAt}},
		} {
			if _, err := tx.Exec(ctx, statement.query, statement.args...); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO book_cover_artifacts (
			  id,project_id,book_id,book_lifecycle_generation,version,storage_key,mime_type,
			  byte_size,width,height,content_sha256,state,created_at,activated_at,removed_at,object_deleted_at
			) VALUES ($1,$2,$3,2,1,'book-covers/hard-purge.final','image/jpeg',4,600,900,$4,'removed',$5,$5,$5,$5)
		`, artifactID, projectID, bookID, bytes.Repeat([]byte{1}, 32), derivedRequest.Tombstone.Identity.FencedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_tombstones (
			  id,project_id,origin,entity_kind,purge_mode,entity_id,
			  project_generation,book_id,book_generation,status,fenced_at,purge_deadline,predecessor_set_digest
			) VALUES
			  ($1,$2,'governed_online','book','derived_generation',$3,1,$3,2,'processing',$4,$5,NULL),
			  ($6,$2,'governed_online','book','source_hard_purge',$3,1,$3,3,'processing',$4,$5,$7)
		`, derivedRequest.Claim.TombstoneID, projectID, bookID, derivedRequest.Tombstone.Identity.FencedAt,
			derivedRequest.Tombstone.Identity.FencedAt.Add(24*time.Hour), hardRequest.Claim.TombstoneID,
			bytes.Repeat([]byte{2}, 32)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO deletion_target_tasks (
			  tombstone_id,project_id,target,state,attempts,claim_incarnation,next_attempt_at,
			  claimed_by_digest,claim_expires_at
			) VALUES
			  ($1,$3,'filesystem','processing',$4,$5,$6,$7,$8),
			  ($2,$3,'filesystem','processing',$4,$5,$6,$7,$8)
		`, derivedRequest.Claim.TombstoneID, hardRequest.Claim.TombstoneID, projectID,
			derivedRequest.Attempt, derivedRequest.Claim.ClaimIncarnation,
			derivedRequest.Tombstone.Identity.FencedAt, derivedRequest.Claim.LeaseOwnerDigest[:],
			derivedRequest.Claim.ObservedLeaseExpiry)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	store := New(db)
	derivedObjects, err := store.BeginFilesystemDeletion(ctx, derivedRequest)
	if err != nil || len(derivedObjects) != 0 {
		t.Fatalf("derived BeginFilesystemDeletion() = %#v, %v, want no cover targets", derivedObjects, err)
	}
	objects, err := store.BeginFilesystemDeletion(ctx, hardRequest)
	if err != nil || len(objects) != 1 {
		t.Fatalf("BeginFilesystemDeletion() = %#v, %v", objects, err)
	}
	object := objects[0]
	if object.ArtifactID != artifactID || object.ObjectKey != "book-covers/hard-purge.final" ||
		object.State != deletionservice.FilesystemObjectDeleted {
		t.Fatalf("cover deletion object = %#v", object)
	}
	receipt, err := deletionservice.FilesystemObjectReceiptDigest(projectID, object.ArtifactID, object.DeleteOperationID)
	if err != nil {
		t.Fatal(err)
	}
	if object.DeletionReceiptDigest != receipt {
		t.Fatalf("cover deletion receipt = %x, want %x", object.DeletionReceiptDigest, receipt)
	}
	if err := store.CommitFilesystemObjectDeletion(ctx, hardRequest, object, receipt); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyFilesystemDeletionComplete(ctx, hardRequest); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM book_cover_artifacts WHERE id = $1", artifactID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining cover artifacts = %d, %v", remaining, err)
	}
}
