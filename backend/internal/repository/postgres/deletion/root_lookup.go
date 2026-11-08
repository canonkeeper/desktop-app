package deletion

import (
	"context"
	"math"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// ListDeletionRoots finds the native chat threads a deletion removes: the chats whose turns
// read the deleted source and the chats a chat epoch or hard purge deletes.
func (store *Store) ListDeletionRoots(ctx context.Context, tombstone deletiondomain.Tombstone) ([]deletiondomain.RootCandidate, error) {
	if store == nil || store.Pool == nil || ctx == nil || tombstone.Validate() != nil {
		return nil, deletiondomain.ErrInvalidTargetRequest
	}
	params, err := deletionRootParams(tombstone)
	if err != nil {
		return nil, err
	}
	rows, err := store.Pool.Query(ctx, deletionRootSelectorsSQL,
		params.projectID, params.selectedKind, params.projectGeneration,
		params.bookID, params.bookGeneration, params.chapterID, params.chapterGeneration,
		params.chatSessionID, params.chatLifecycleEpoch, params.purgeMode,
	)
	if err != nil {
		return nil, err
	}
	candidates, err := scanRootCandidates(rows, params.projectID.String())
	if err != nil {
		return nil, err
	}
	return mergeRootCandidates(params.projectID.String(), candidates)
}

func (store *Store) ListPermissionLossRoots(ctx context.Context, scope deletiondomain.PermissionLossScope) ([]deletiondomain.RootCandidate, error) {
	if store == nil || store.Pool == nil || ctx == nil || scope.Validate() != nil {
		return nil, deletiondomain.ErrInvalidTargetRequest
	}
	rows, err := store.Pool.Query(ctx, permissionLossRootSelectorsSQL, mustUUID(scope.ProjectID), mustUUID(scope.ActorUserID))
	if err != nil {
		return nil, err
	}
	candidates, err := scanRootCandidates(rows, scope.ProjectID)
	if err != nil {
		return nil, err
	}
	return mergeRootCandidates(scope.ProjectID, candidates)
}

func (store *Store) ReleaseDeletedRoot(ctx context.Context, candidate deletiondomain.RootCandidate, deletedRootID string) error {
	if store == nil || store.Pool == nil || ctx == nil || candidate.Validate() != nil {
		return deletiondomain.ErrInvalidTargetRequest
	}
	expectedRootID, err := releasedRootBindingID(candidate, deletedRootID)
	if err != nil {
		return err
	}
	if expectedRootID == "" {
		return nil
	}
	projectID, err := storedb.ParseUUID(candidate.ProjectID)
	if err != nil {
		return deletiondomain.ErrInvalidTargetRequest
	}
	chatID, err := storedb.ParseUUID(candidate.Selector.CorrelationID)
	if err != nil {
		return deletiondomain.ErrInvalidTargetRequest
	}
	var state string
	if err := store.Pool.QueryRow(
		ctx, releaseDeletedChatRootSQL, projectID, chatID, expectedRootID,
	).Scan(&state); err != nil {
		return err
	}
	switch state {
	case "absent", "cleared", "unbound":
		return nil
	default:
		return deletiondomain.ErrRootBindingConflict
	}
}

func releasedRootBindingID(candidate deletiondomain.RootCandidate, deletedRootID string) (string, error) {
	if candidate.Selector.ThreadID == "" {
		return deletedRootID, nil
	}
	if deletedRootID != "" && deletedRootID != candidate.Selector.ThreadID {
		return "", deletiondomain.ErrRootBindingConflict
	}
	return candidate.Selector.ThreadID, nil
}

type deletionRootSelection struct {
	projectID          pgtype.UUID
	selectedKind       string
	projectGeneration  int64
	bookID             pgtype.UUID
	bookGeneration     pgtype.Int8
	chapterID          pgtype.UUID
	chapterGeneration  pgtype.Int8
	chatSessionID      pgtype.UUID
	chatLifecycleEpoch pgtype.Int8
	purgeMode          string
}

func deletionRootParams(tombstone deletiondomain.Tombstone) (deletionRootSelection, error) {
	fields, err := tombstone.Identity.SelectedVector.Fields()
	if err != nil || uint64(fields.Source.ProjectGeneration) > math.MaxInt64 ||
		uint64(fields.Source.BookGeneration) > math.MaxInt64 ||
		uint64(fields.Source.ChapterGeneration) > math.MaxInt64 ||
		uint64(fields.ChatLifecycleEpoch) > math.MaxInt64 {
		return deletionRootSelection{}, deletiondomain.ErrInvalidTargetRequest
	}
	return deletionRootSelection{
		projectID: mustUUID(fields.ProjectID), selectedKind: string(fields.Kind),
		projectGeneration:  int64(fields.Source.ProjectGeneration),
		bookID:             nullablePostgresUUID(fields.Source.BookID),
		bookGeneration:     nullablePostgresInt8(int64(fields.Source.BookGeneration)),
		chapterID:          nullablePostgresUUID(fields.Source.ChapterID),
		chapterGeneration:  nullablePostgresInt8(int64(fields.Source.ChapterGeneration)),
		chatSessionID:      nullablePostgresUUID(fields.ChatID),
		chatLifecycleEpoch: pgtype.Int8{Int64: int64(fields.ChatLifecycleEpoch), Valid: fields.Kind == governance.DeletionSelectionChat},
		purgeMode:          string(tombstone.Identity.Mode),
	}, nil
}

type rootRows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

func scanRootCandidates(rows rootRows, projectID string) ([]deletiondomain.RootCandidate, error) {
	defer rows.Close()
	var result []deletiondomain.RootCandidate
	for rows.Next() {
		var correlation, kind string
		var thread pgtype.Text
		if err := rows.Scan(&correlation, &thread, &kind); err != nil {
			return nil, err
		}
		// The native tree lives in the app-server of the session's kind (specs/2026-11-02-image-workshop.md IW6.4).
		flow, known := agentdomain.SessionFlow(kind)
		if !known {
			return nil, deletiondomain.ErrInvalidTargetRequest
		}
		candidate := deletiondomain.RootCandidate{
			ProjectID: projectID,
			Selector: deletiondomain.RootSelector{
				Flow: flow, CorrelationID: correlation, ThreadID: thread.String,
			},
		}
		if candidate.Validate() != nil {
			return nil, deletiondomain.ErrInvalidTargetRequest
		}
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func mergeRootCandidates(projectID string, candidates []deletiondomain.RootCandidate) ([]deletiondomain.RootCandidate, error) {
	result := make([]deletiondomain.RootCandidate, 0, len(candidates))
	seenThreads := make(map[string]struct{}, len(candidates))
	seenSelectors := make(map[deletiondomain.RootSelector]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.Validate() != nil || candidate.ProjectID != projectID {
			return nil, deletiondomain.ErrInvalidTargetRequest
		}
		if _, exists := seenSelectors[candidate.Selector]; exists {
			continue
		}
		if _, exists := seenThreads[candidate.Selector.ThreadID]; candidate.Selector.ThreadID != "" && exists {
			continue
		}
		result = append(result, candidate)
		seenSelectors[candidate.Selector] = struct{}{}
		if candidate.Selector.ThreadID != "" {
			seenThreads[candidate.Selector.ThreadID] = struct{}{}
		}
	}
	return result, nil
}

// deletionRootSelectorsSQL names the Codex roots a deletion removes: the chats of a deleted
// project or chat and the book-scoped chats of a deleted book; a chat that only read a deleted
// book or chapter keeps its root (specs/2026-10-16-book-deletion-keeps-chats.md R1.2).
const deletionRootSelectorsSQL = `
WITH selected_jobs AS (
	SELECT job.id, job.project_id
	FROM jobs AS job
	JOIN artifact_source_owners AS owner
	  ON owner.id = job.artifact_source_owner_id
	 AND owner.project_id = job.project_id
	 AND owner.artifact_kind = 'job'
	 AND owner.artifact_id = job.id
	WHERE job.project_id = $1
	  AND job.kind = 'research_chat_turn'
	  AND EXISTS (
		SELECT 1
		FROM artifact_source_refs AS ref
		WHERE ref.owner_id = owner.id
		  AND ref.project_id = owner.project_id
		  AND (
			($2::text = 'project' AND ref.project_generation = $3)
			OR ($2::text = 'book' AND ref.project_generation = $3
				AND ref.book_id = $4 AND ref.book_generation = $5
				AND ref.chat_session_id IS NULL)
			OR ($2::text = 'chapter' AND ref.project_generation = $3
				AND ref.book_id = $4 AND ref.book_generation = $5
				AND ref.chapter_id = $6 AND ref.chapter_generation = $7
				AND ref.chat_session_id IS NULL)
			OR ($2::text = 'chat' AND ref.chat_session_id = $8
				AND ref.chat_lifecycle_epoch = $9)
		  )
	  )
), source_chats AS (
	SELECT chat.id::text AS correlation_id, chat.thread_id, chat.kind
	FROM selected_jobs AS job
	JOIN chat_turns AS turn
	  ON turn.job_id = job.id
	 AND turn.project_id = job.project_id
	JOIN chat_sessions AS chat
	  ON chat.id = turn.chat_id
	 AND chat.project_id = turn.project_id
), selected_chats AS (
	SELECT chat.id::text AS correlation_id, chat.thread_id, chat.kind
	FROM chat_sessions AS chat
	WHERE chat.project_id = $1
	  AND (
		($10::text = 'chat_epoch' AND $2::text = 'chat'
			AND chat.id = $8 AND chat.deleted_at IS NOT NULL
			AND chat.lifecycle_epoch = $9::bigint + 1)
		OR ($10::text = 'source_hard_purge' AND $2::text = 'project')
		OR ($10::text = 'source_hard_purge' AND $2::text = 'book' AND chat.book_id = $4)
	  )
)
SELECT correlation_id, thread_id, kind FROM source_chats
UNION
SELECT correlation_id, thread_id, kind FROM selected_chats
ORDER BY 1, 2 NULLS FIRST
`

const releaseDeletedChatRootSQL = `
WITH target AS MATERIALIZED (
	SELECT chat.thread_id
	FROM chat_sessions AS chat
	WHERE chat.project_id = $1 AND chat.id = $2
	FOR UPDATE
), cleared AS (
	UPDATE chat_sessions AS chat
	SET thread_id = NULL, updated_at = clock_timestamp()
	FROM target
	WHERE chat.project_id = $1
	  AND chat.id = $2
	  AND chat.thread_id = $3
	RETURNING true
)
SELECT CASE
	WHEN NOT EXISTS (SELECT 1 FROM target) THEN 'absent'
	WHEN EXISTS (SELECT 1 FROM cleared) THEN 'cleared'
	WHEN (SELECT thread_id FROM target) IS NULL THEN 'unbound'
	ELSE 'conflict'
END
`

const permissionLossRootSelectorsSQL = `
SELECT DISTINCT chat.id::text AS correlation_id, chat.thread_id, chat.kind
FROM jobs AS job
JOIN chat_turns AS turn ON turn.job_id = job.id AND turn.project_id = job.project_id
JOIN chat_sessions AS chat ON chat.id = turn.chat_id AND chat.project_id = turn.project_id
WHERE job.project_id = $1 AND job.creator_user_id = $2 AND job.kind = 'research_chat_turn'
  AND NOT EXISTS (
	SELECT 1 FROM project_memberships AS membership
	WHERE membership.project_id = job.project_id
	  AND membership.user_id = $2
	  AND membership.removed_at IS NULL
	  AND membership.role IN ('owner', 'admin', 'editor')
  )
ORDER BY 1, 2 NULLS FIRST
`
