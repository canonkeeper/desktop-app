package researchchat

import (
	"context"
	"errors"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

func (repository *Repository) readAuthorityInTx(
	ctx context.Context,
	tx pgx.Tx,
	call service.ToolContext,
) (researchdomain.ReadAuthority, error) {
	row, err := resolveNativeAuthorityInTx(ctx, tx, call.Caller.RootThreadID, call.TurnID)
	if err != nil {
		return researchdomain.ReadAuthority{}, err
	}
	projectID, jobID, chatID := storedb.UUIDString(row.ProjectID), storedb.UUIDString(row.JobID), storedb.UUIDString(row.ChatSessionID)
	rootThreadID, turnID := row.RootThreadID.String, row.TurnID.String
	decoded, err := jobs.DecodeSubject(jobs.KindResearchChatTurn, row.SubjectJson)
	subject, ok := decoded.(jobs.ResearchChatTurnSubject)
	if err != nil || !ok || row.Kind != string(jobs.KindResearchChatTurn) || row.Origin != string(jobs.ModelOriginUser) ||
		!row.RootThreadID.Valid || !row.TurnID.Valid || rootThreadID != call.Caller.RootThreadID || turnID != call.TurnID ||
		subject.ProjectID != projectID || subject.ChatID != chatID {
		return researchdomain.ReadAuthority{}, researchdomain.ErrForbidden
	}
	bookID := ""
	if subject.BookID != nil {
		bookID = *subject.BookID
	}
	authority := researchdomain.ReadAuthority{
		ProjectID: projectID, JobID: jobID, ChatID: chatID,
		RootThreadID: call.Caller.RootThreadID, ProviderTurnID: call.TurnID,
		SourceView: string(subject.SourceView), Scope: researchdomain.Scope(subject.Scope), BookID: bookID,
		ChatTurnID: subject.TurnID, ActorUserID: storedb.UUIDString(row.ActorUserID),
	}
	place, err := sqlc.New(tx).ChapterDraftPlaceOfChat(ctx, sqlc.ChapterDraftPlaceOfChatParams{ProjectID: row.ProjectID, ChatID: row.ChatSessionID})
	switch {
	case err == nil:
		authority.Moment = true
		if place.BookID.Valid {
			authority.MomentBookID = storedb.UUIDString(place.BookID)
		}
		if place.AfterChapterID.Valid {
			authority.MomentAfterChapterID = storedb.UUIDString(place.AfterChapterID)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return researchdomain.ReadAuthority{}, err
	}
	err = repository.validateLateEffectInTx(ctx, tx, lateEffectKey{
		ProjectID: projectID, JobID: jobID, ChatID: chatID, TurnID: subject.TurnID,
		ActorUserID: storedb.UUIDString(row.ActorUserID),
	}, func(ctx context.Context, tx pgx.Tx, _ jobs.ModelClaim) error {
		current, err := resolveNativeAuthorityInTx(ctx, tx, call.Caller.RootThreadID, call.TurnID)
		if err != nil {
			return err
		}
		if !sameNativeAuthority(row, current) {
			return researchdomain.ErrConflict
		}
		return nil
	})
	return authority, err
}

func (repository *Repository) confirmReadAuthorityInTx(
	ctx context.Context,
	tx pgx.Tx,
	call service.ToolContext,
	expected researchdomain.ReadAuthority,
) error {
	current, err := repository.readAuthorityInTx(ctx, tx, call)
	if err != nil {
		return err
	}
	if current != expected {
		return researchdomain.ErrConflict
	}
	return nil
}
