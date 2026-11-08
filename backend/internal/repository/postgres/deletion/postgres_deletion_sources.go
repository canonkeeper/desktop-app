package deletion

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/governance"
	deletionservice "canonkeeper/backend/internal/services/deletion"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func deletePostgresSourceRows(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
	cursor pgtype.UUID,
	table string,
) (postgresMutationResult, error) {
	if selection.mode != governance.PurgeModeSourceHardPurge {
		return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
	}
	var query string
	switch table {
	case "chapter_paragraphs":
		query = `
			WITH candidate AS (
			  SELECT paragraph.id AS cursor_id, paragraph.chapter_id
			  FROM chapter_paragraphs paragraph
			  JOIN chapters chapter ON chapter.id = paragraph.chapter_id
			  WHERE chapter.project_id = $1::uuid
			    AND (
			      $2::text = 'project'
			      OR ($2::text = 'book' AND chapter.book_id = $4::uuid)
			      OR ($2::text = 'chapter' AND chapter.id = $6::uuid)
			    )
			    AND paragraph.id > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
			  ORDER BY paragraph.id LIMIT $11 FOR UPDATE OF paragraph
			), deleted AS (
			  DELETE FROM chapter_paragraphs paragraph USING candidate
			  WHERE paragraph.id = candidate.cursor_id AND paragraph.chapter_id = candidate.chapter_id
			  RETURNING paragraph.id AS cursor_id
			)
			SELECT COALESCE((SELECT cursor_id::text FROM deleted ORDER BY cursor_id DESC LIMIT 1), ''),
			       (SELECT count(*) FROM deleted)`
	case "chapters":
		query = `
			WITH candidate AS (
			  SELECT chapter.id AS cursor_id
			  FROM chapters chapter
			  WHERE chapter.project_id = $1::uuid
			    AND (
			      $2::text = 'project'
			      OR ($2::text = 'book' AND chapter.book_id = $4::uuid)
			      OR ($2::text = 'chapter'
			          AND chapter.id = $6::uuid
			          AND chapter.lifecycle_generation = $7::bigint
			          AND chapter.deleted_at IS NOT NULL
			          AND chapter.hard_purge_started_at = $12::timestamptz
			          AND chapter.purge_after <= clock_timestamp())
			    )
			    AND chapter.id > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
			  ORDER BY chapter.id LIMIT $11 FOR UPDATE OF chapter
			), deleted AS (
			  DELETE FROM chapters chapter USING candidate
			  WHERE chapter.id = candidate.cursor_id AND chapter.project_id = $1::uuid
			  RETURNING chapter.id AS cursor_id
			)
			SELECT COALESCE((SELECT cursor_id::text FROM deleted ORDER BY cursor_id DESC LIMIT 1), ''),
			       (SELECT count(*) FROM deleted)`
	case "books":
		query = `
			WITH candidate AS (
			  SELECT book.id AS cursor_id
			  FROM books book
			  WHERE book.project_id = $1::uuid
			    AND (
			      $2::text = 'project'
			      OR ($2::text = 'book'
			          AND book.id = $4::uuid
			          AND book.lifecycle_generation = $5::bigint
			          AND book.deleted_at IS NOT NULL
			          AND book.hard_purge_started_at = $12::timestamptz
			          AND book.purge_after <= clock_timestamp())
			    )
			    AND book.id > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
			  ORDER BY book.id LIMIT $11 FOR UPDATE OF book
			), deleted AS (
			  DELETE FROM books book USING candidate
			  WHERE book.id = candidate.cursor_id AND book.project_id = $1::uuid
			  RETURNING book.id AS cursor_id
			)
			SELECT COALESCE((SELECT cursor_id::text FROM deleted ORDER BY cursor_id DESC LIMIT 1), ''),
			       (SELECT count(*) FROM deleted)`
	case "project_invitations":
		if selection.kind != governance.DeletionSelectionProject {
			return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
		}
		query = `
			WITH candidate AS (
			  SELECT invitation.id AS cursor_id
			  FROM project_invitations invitation
			  WHERE invitation.project_id = $1::uuid
			    AND invitation.id > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
			  ORDER BY invitation.id LIMIT $11 FOR UPDATE OF invitation
			), deleted AS (
			  DELETE FROM project_invitations invitation USING candidate
			  WHERE invitation.id = candidate.cursor_id AND invitation.project_id = $1::uuid
			  RETURNING invitation.id AS cursor_id
			)
			SELECT COALESCE((SELECT cursor_id::text FROM deleted ORDER BY cursor_id DESC LIMIT 1), ''),
			       (SELECT count(*) FROM deleted)`
	case "project_memberships":
		if selection.kind != governance.DeletionSelectionProject {
			return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
		}
		query = `
			WITH candidate AS (
			  SELECT membership.user_id AS cursor_id
			  FROM project_memberships membership
			  WHERE membership.project_id = $1::uuid
			    AND membership.user_id > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
			  ORDER BY membership.user_id LIMIT $11 FOR UPDATE OF membership
			), deleted AS (
			  DELETE FROM project_memberships membership USING candidate
			  WHERE membership.user_id = candidate.cursor_id AND membership.project_id = $1::uuid
			  RETURNING membership.user_id AS cursor_id
			)
			SELECT COALESCE((SELECT cursor_id::text FROM deleted ORDER BY cursor_id DESC LIMIT 1), ''),
			       (SELECT count(*) FROM deleted)`
	default:
		return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
	}
	const candidateMarker = "WITH candidate AS ("
	const typedCandidate = `WITH typed_input AS (
		SELECT $2::text AS selected_kind,
		       $3::bigint AS project_generation,
		       $4::uuid AS book_id,
		       $5::bigint AS book_generation,
		       $6::uuid AS chapter_id,
		       $7::bigint AS chapter_generation,
		       $8::uuid AS chat_id,
		       $9::bigint AS chat_epoch
	), candidate AS (`
	if !strings.Contains(query, candidateMarker) {
		return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
	}
	query = strings.Replace(query, candidateMarker, typedCandidate, 1)
	args := selection.mutationArgs(cursor, postgresDeletionBatchSize)
	if table == "chapters" || table == "books" {
		args = append(args, selection.fencedAt)
	}
	var result postgresMutationResult
	if err := tx.QueryRow(ctx, query, args...).Scan(&result.cursor, &result.count); err != nil {
		return postgresMutationResult{}, err
	}
	return result, nil
}

func deletePostgresChatSessions(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
) (postgresMutationResult, error) {
	var command pgconn.CommandTag
	var err error
	switch {
	case selection.mode == governance.PurgeModeChatEpoch && selection.kind == governance.DeletionSelectionChat:
		command, err = tx.Exec(ctx, `
			DELETE FROM chat_sessions
			WHERE id = $1::uuid AND project_id = $2::uuid
			  AND deleted_at IS NOT NULL
			  AND lifecycle_epoch = $3::bigint + 1
		`, selection.chatID, selection.projectID, selection.chatEpoch)
	case selection.mode == governance.PurgeModeSourceHardPurge && selection.kind == governance.DeletionSelectionProject:
		command, err = tx.Exec(ctx, `DELETE FROM chat_sessions WHERE project_id = $1::uuid`, selection.projectID)
	case selection.mode == governance.PurgeModeSourceHardPurge && selection.kind == governance.DeletionSelectionBook:
		command, err = tx.Exec(ctx, `
			DELETE FROM chat_sessions
			WHERE project_id = $1::uuid AND book_id = $2::uuid
		`, selection.projectID, selection.bookID)
	default:
		return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
	}
	if err != nil {
		return postgresMutationResult{}, err
	}
	return postgresMutationResult{count: command.RowsAffected()}, nil
}

func deletePostgresProject(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
) (postgresMutationResult, error) {
	if selection.mode != governance.PurgeModeSourceHardPurge || selection.kind != governance.DeletionSelectionProject {
		return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
	}
	command, err := tx.Exec(ctx, `
		DELETE FROM projects
		WHERE id = $1::uuid
		  AND lifecycle_generation = $2::bigint
		  AND deleted_at IS NOT NULL
		  AND purge_after <= clock_timestamp()
		  AND hard_purge_started_at = $3::timestamptz
	`, selection.projectID, selection.projectGeneration, selection.fencedAt)
	if err != nil {
		return postgresMutationResult{}, err
	}
	if command.RowsAffected() != 1 {
		return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
	}
	return postgresMutationResult{count: 1}, nil
}
