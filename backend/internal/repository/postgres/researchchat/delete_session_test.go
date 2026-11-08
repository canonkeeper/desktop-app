package researchchat

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/testutil"
)

func TestMarkDeletedAtomicallyEnqueuesGovernedChatDeletion(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_chat_delete")
	seedRenameSession(t, db)
	repository := New(db)
	ctx := context.Background()
	for name, userID := range map[string]string{"viewer": renameViewerID, "outsider": renameOutsiderID} {
		_, err := repository.MarkDeleted(ctx, researchdomain.Actor{UserID: userID}, renameChatID, agent.SessionResearch)
		want := researchdomain.ErrNotFound
		if !errors.Is(err, want) {
			t.Fatalf("%s deletion: %v, want %v", name, err, want)
		}
	}
	target, err := repository.MarkDeleted(ctx, researchdomain.Actor{UserID: renameOwnerID}, renameChatID, agent.SessionResearch)
	if err != nil {
		t.Fatalf("governed chat deletion: %v", err)
	}
	if target.ChatID != renameChatID || target.ProjectID != renameProjectID {
		t.Fatalf("wrong deletion target: %+v", target)
	}
	var targets, registrySteps int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM deletion_target_tasks task
		JOIN deletion_tombstones t ON t.id=task.tombstone_id AND t.project_id=task.project_id
		JOIN chat_sessions chat ON chat.id=t.chat_session_id AND chat.project_id=t.project_id
		WHERE chat.id=$1 AND chat.deleted_at IS NOT NULL AND chat.lifecycle_epoch=2
		  AND t.origin='governed_online' AND t.entity_kind='chat' AND t.purge_mode='chat_epoch'
		  AND t.entity_id=chat.id AND t.chat_lifecycle_epoch=1
		  AND t.fenced_at=chat.deleted_at AND t.purge_deadline=chat.purge_after
		  AND task.state='pending'
	`, renameChatID).Scan(&targets); err != nil || targets != 3 {
		t.Fatalf("durable deletion targets = %d, error = %v", targets, err)
	}
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM deletion_target_step_progress step
		JOIN deletion_tombstones t ON t.id=step.tombstone_id AND t.project_id=step.project_id
		WHERE t.chat_session_id=$1 AND step.state='pending'
	`, renameChatID).Scan(&registrySteps); err != nil || registrySteps == 0 {
		t.Fatalf("PostgreSQL registry steps = %d, error = %v", registrySteps, err)
	}
	if _, err := repository.MarkDeleted(ctx, researchdomain.Actor{UserID: renameOwnerID}, renameChatID, agent.SessionResearch); err == nil {
		t.Fatal("already deleted chat allowed a second epoch transition")
	}
	var tombstones int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_tombstones WHERE chat_session_id=$1`, renameChatID).Scan(&tombstones); err != nil || tombstones != 1 {
		t.Fatalf("tombstones after repeated deletion = %d, error = %v", tombstones, err)
	}
}
