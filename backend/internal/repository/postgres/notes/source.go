package notes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf16"

	domain "canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/notes"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (repository *Repository) captureSource(ctx context.Context, tx pgx.Tx, owner pgtype.UUID, source domain.Source) (savedSource, error) {
	saved := savedSource{Source: source}
	if err := source.Validate(); err != nil {
		return saved, err
	}
	if source.Kind == "chat" {
		binding, err := readableChat(ctx, tx, owner, source)
		saved.Chat = &binding
		return saved, err
	}
	locator := source.Locator
	current, err := readableChapter(ctx, tx, owner, source)
	if err != nil {
		return saved, err
	}
	if current.SourceRevision != uint64(locator.Revision) || !quoteMatches(source, current) {
		return saved, domain.ErrConflict
	}
	saved.Chapter = &governance.CanonicalChapterSourceLocatorV1{
		ProjectID: locator.ProjectID, BookID: locator.BookID, ChapterID: locator.ChapterID, SourceRef: locator.ChapterID,
		SourceView: governance.SourceViewPublished, SourceRevision: current.SourceRevision, SourceGeneration: current.SourceGeneration, ContentHash: current.ContentHash,
	}
	return saved, nil
}

func readableChat(ctx context.Context, tx pgx.Tx, owner pgtype.UUID, source domain.Source) (service.ChatBinding, error) {
	var binding service.ChatBinding
	err := tx.QueryRow(ctx, `
  SELECT chat.project_id::text,chat.thread_id,job.id::text,turn_row.id::text,chat.lifecycle_epoch,chat.source_view
  FROM chat_sessions AS chat
  JOIN projects AS project ON project.id=chat.project_id AND project.deleted_at IS NULL
  JOIN project_memberships AS member ON member.project_id=project.id AND member.user_id=$1 AND member.removed_at IS NULL
  JOIN project_source_views AS view ON view.project_id=project.id AND view.source_view=chat.source_view AND view.project_lifecycle_generation=project.lifecycle_generation
  JOIN chat_turns AS turn_row ON turn_row.chat_id=chat.id AND turn_row.project_id=chat.project_id AND turn_row.source_view=chat.source_view
  JOIN jobs AS job ON job.id=turn_row.job_id AND job.project_id=turn_row.project_id AND job.kind='research_chat_turn'
  JOIN artifact_source_owners AS job_owner ON job_owner.id=job.artifact_source_owner_id AND job_owner.project_id=job.project_id
  JOIN artifact_source_owners AS turn_owner ON turn_owner.id=turn_row.artifact_source_owner_id AND turn_owner.project_id=turn_row.project_id
  WHERE chat.id=$2::uuid AND chat.deleted_at IS NULL AND chat.scope='project' AND chat.book_id IS NULL
   AND chat.thread_id IS NOT NULL AND turn_row.provider_turn_id=$3
   AND (chat.source_view='published' OR member.role IN ('owner','admin','editor'))
   AND job.governance_payload_state='active'
   AND job_owner.read_state='active' AND job_owner.fenced_at IS NULL
   AND turn_owner.read_state='active' AND turn_owner.fenced_at IS NULL
  FOR SHARE OF chat,project,member,view,turn_row,job,job_owner,turn_owner
 `, owner, source.ChatID, source.ProviderTurnID).Scan(&binding.ProjectID, &binding.RootThreadID, &binding.JobID, &binding.TurnID, &binding.Epoch, &binding.SourceView)
	return binding, err
}

func (repository *Repository) ReadSource(ctx context.Context, userID, noteID string) (service.SourceContext, error) {
	unavailable := service.SourceContext{Resolution: domain.Resolution{State: "unavailable"}}
	owner, err := storedb.ParseUUID(userID)
	if err != nil {
		return unavailable, domain.ErrNotFound
	}
	id, err := storedb.ParseUUID(noteID)
	if err != nil {
		return unavailable, domain.ErrNotFound
	}
	result := unavailable
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		row, err := sqlc.New(tx).GetPersonalNote(ctx, sqlc.GetPersonalNoteParams{OwnerUserID: owner, ID: id})
		if err != nil {
			return err
		}
		if len(row.SourceJson) == 0 {
			return nil
		}
		var saved savedSource
		if err := json.Unmarshal(row.SourceJson, &saved); err != nil {
			return err
		}
		if saved.Source.Validate() != nil {
			return domain.ErrUnavailable
		}
		if saved.Source.Kind == "chat" {
			current, err := readableChat(ctx, tx, owner, saved.Source)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if saved.Chat == nil || current != *saved.Chat {
				return nil
			}
			result = service.SourceContext{Chat: &current, Resolution: domain.Resolution{State: "current", Source: &domain.ResolvedSource{Source: saved.Source, ProjectID: current.ProjectID, SourceView: current.SourceView}}}
			return nil
		}
		current, err := readableChapter(ctx, tx, owner, saved.Source)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if saved.Chapter == nil {
			return domain.ErrUnavailable
		}
		if current.SourceGeneration != saved.Chapter.SourceGeneration {
			return nil
		}
		locator := saved.Source.Locator
		state := "current"
		if current.SourceRevision != saved.Chapter.SourceRevision || current.SourceGeneration != saved.Chapter.SourceGeneration ||
			current.ContentHash != saved.Chapter.ContentHash || !quoteMatches(saved.Source, current) {
			state = "changed"
			locator.Revision = int64(current.SourceRevision)
			locator.ParagraphID, locator.Range = nil, nil
		}
		result.Resolution = domain.Resolution{State: state, Source: &domain.ResolvedSource{Source: saved.Source}, Place: current.Place}
		return nil
	})
	return result, mapError(err)
}

func quoteMatches(source domain.Source, current chapterSnapshot) bool {
	locator := source.Locator
	if locator.ParagraphID == nil {
		return locator.Range == nil && strings.Contains(current.Content, source.Quote)
	}
	for _, paragraph := range current.Paragraphs {
		if paragraph.ParagraphID != *locator.ParagraphID {
			continue
		}
		if locator.Range == nil {
			return strings.Contains(paragraph.Content, source.Quote)
		}
		selected := locator.Range
		units := utf16.Encode([]rune(paragraph.Content))
		if selected.StartOffset < 0 || selected.EndOffset > len(units) || selected.EndOffset <= selected.StartOffset {
			return false
		}
		quote := string(utf16.Decode(units[selected.StartOffset:selected.EndOffset]))
		return quote == source.Quote && (selected.Quote == nil || *selected.Quote == quote)
	}
	return false
}
