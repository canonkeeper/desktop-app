package researchchat

import (
	"context"
	"errors"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// VoiceReads is the read authority of a voice call (specs/2026-11-03-voice-assistant.md, Decision log 9): the agent of
// the call reads the saga with the chat's tools as the author does — a member of the project, the published text,
// the whole saga. A result is still built only from current chapter revisions, but no manifest is written: the
// conversation is kept nowhere, so no stored thread holds the chapters it read.
type VoiceReads struct {
	repository  *Repository
	actorUserID string
	projectID   string
}

var _ service.ReadRepository = (*VoiceReads)(nil)

// VoiceReads makes the read authority of one call of the author in the project.
func (repository *Repository) VoiceReads(actorUserID, projectID string) *VoiceReads {
	return &VoiceReads{repository: repository, actorUserID: actorUserID, projectID: projectID}
}

// ReadAuthority answers the author's reading of the project while he is still its member.
func (reads *VoiceReads) ReadAuthority(ctx context.Context, _ service.ToolContext) (researchdomain.ReadAuthority, error) {
	project, err := storedb.ParseUUID(reads.projectID)
	if err != nil {
		return researchdomain.ReadAuthority{}, researchdomain.ErrForbidden
	}
	actor, err := storedb.ParseUUID(reads.actorUserID)
	if err != nil {
		return researchdomain.ReadAuthority{}, researchdomain.ErrForbidden
	}
	_, err = sqlc.New(reads.repository.db.Pool).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
	if errors.Is(err, pgx.ErrNoRows) {
		return researchdomain.ReadAuthority{}, researchdomain.ErrForbidden
	}
	if err != nil {
		return researchdomain.ReadAuthority{}, err
	}
	return researchdomain.ReadAuthority{
		ProjectID: reads.projectID, SourceView: string(governance.SourceViewPublished), Scope: researchdomain.ScopeProject,
		ActorUserID: reads.actorUserID,
	}, nil
}

// RetainChapters checks that every chapter a result was built from is still at its revision; nothing is recorded.
func (reads *VoiceReads) RetainChapters(
	ctx context.Context, _ service.ToolContext, authority researchdomain.ReadAuthority, chapters []service.ChapterRevision,
) error {
	if len(chapters) == 0 {
		return nil
	}
	ids := make([]string, len(chapters))
	for index, chapter := range chapters {
		ids[index] = chapter.ChapterID
	}
	err := reads.repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		current, err := reads.repository.canon.ResolveCurrentChapterRefsInTx(
			ctx, tx, authority.ProjectID, governance.SourceView(authority.SourceView), ids,
		)
		if err != nil {
			return err
		}
		for _, chapter := range chapters {
			ref, ok := current[chapter.ChapterID]
			if !ok || chapter.Revision < 1 || ref.SourceRevision != uint64(chapter.Revision) {
				return researchdomain.ErrStaleSource
			}
		}
		return nil
	})
	return mapSourceReadError(err)
}
