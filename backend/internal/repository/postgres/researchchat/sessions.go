package researchchat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/researchchat"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type sessionRecord struct {
	ID              string
	ProjectID       string
	SourceView      governance.SourceView
	Scope           researchdomain.Scope
	BookID          string
	Title           string
	SavedAssignment string
	ThreadID        string
	LifecycleEpoch  governance.ChatLifecycleEpoch
	AgentGeneration int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LatestTurnID    string
	LatestTurnJobID string
	// LatestTurnStatus is the Job status of the latest turn, read with the chat.
	LatestTurnStatus string
	// LatestTurnAt is when the latest turn was asked; zero for a chat without one.
	LatestTurnAt time.Time
	// Kind is research for a chat, drawing for the session of a helper of the drawing workshop.
	Kind string
}

type writableSession struct {
	sessionRecord
	ActorRole                  string
	ProjectLifecycleGeneration int64
	SourceViewArm              governance.SourceViewArm
	ChatArm                    governance.ChatArm
}

type createSessionReplayEnvelope struct {
	ChatID string `json:"chatId"`
}

func (repository *Repository) CreateSession(ctx context.Context, command service.CreateSessionCommand) (researchdomain.Session, error) {
	ids, bookID, err := sessionInputIDs(command.ID, command.ProjectID, command.ActorUserID, command.BookID)
	view := governance.SourceView(command.SourceView)
	if err != nil || !validSessionShape(view, command.Scope, command.BookID) || strings.TrimSpace(command.Title) == "" ||
		len([]rune(command.Title)) > 180 || command.CreatedAt.IsZero() || !command.SessionWriteProof.Valid() ||
		!validSavedAssignment(command.SavedAssignment) || !validSessionIdempotency(command) {
		return researchdomain.Session{}, researchdomain.ErrValidation
	}
	var created sessionRecord
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockSessionWriteProof(ctx, tx, ids[2], command.SessionWriteProof); err != nil {
			return err
		}
		queries := sqlc.New(tx)
		record, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: command.ScopeHash, OperationID: command.OperationID,
			IdempotencyKeyHash: command.IdempotencyKeyHash, RequestHash: command.RequestHash,
			StatusCode: int32(command.StatusCode), ResponseJson: []byte(`{}`), CreatedAt: storedb.Timestamptz(command.CreatedAt),
		})
		newRecord := err == nil
		if errors.Is(err, pgx.ErrNoRows) {
			record, err = queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: command.ScopeHash, OperationID: command.OperationID,
				IdempotencyKeyHash: command.IdempotencyKeyHash,
			})
		}
		if err != nil {
			return mapWriteError(err)
		}
		if !newRecord {
			if record.RequestHash != command.RequestHash || record.StatusCode != int32(command.StatusCode) {
				return researchdomain.ErrConflict
			}
			var envelope createSessionReplayEnvelope
			if json.Unmarshal(record.ResponseJson, &envelope) != nil || envelope.ChatID == "" {
				return researchdomain.ErrConflict
			}
			locked, err := repository.lockWritableSessionInTx(ctx, tx, command.ProjectID, envelope.ChatID, command.ActorUserID)
			if err != nil {
				return err
			}
			if locked.SourceView != view || locked.Scope != command.Scope || locked.BookID != command.BookID {
				return researchdomain.ErrConflict
			}
			created = locked.sessionRecord
			return nil
		}
		row, err := queries.CreateChatSession(ctx, sqlc.CreateChatSessionParams{
			ID: ids[0], ProjectID: ids[1], ActorUserID: ids[2], SourceView: string(view),
			Scope: string(command.Scope), BookID: bookID, Title: command.Title,
			SavedAssignment: pgtype.Text{String: command.SavedAssignment, Valid: command.SavedAssignment != ""},
			AgentGeneration: researchdomain.CurrentAgentGeneration, Kind: agent.SessionResearch,
			CreatedAt: storedb.Timestamptz(command.CreatedAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return researchdomain.ErrNotFound
		}
		if err != nil {
			return mapWriteError(err)
		}
		response, err := json.Marshal(createSessionReplayEnvelope{ChatID: storedb.UUIDString(row.ID)})
		if err != nil {
			return err
		}
		if _, err := queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{
			StatusCode: int32(command.StatusCode), ResponseJson: response, ID: record.ID,
		}); err != nil {
			return mapWriteError(err)
		}
		created = sessionFromSQL(row)
		return nil
	})
	return sessionValue(created), mapError(err)
}

func (repository *Repository) ListSessions(ctx context.Context, query service.ListSessionsQuery) ([]researchdomain.Session, error) {
	project, projectErr := storedb.ParseUUID(query.ProjectID)
	actor, actorErr := storedb.ParseUUID(query.Actor.UserID)
	book, bookErr := optionalUUID(query.BookID)
	cursor, cursorErr := optionalUUID(query.CursorID)
	if projectErr != nil || actorErr != nil || bookErr != nil || cursorErr != nil || query.Limit < 1 || query.Limit > 100 ||
		(query.CursorID == "") != query.CursorUpdatedAt.IsZero() || !validListFilters(query) {
		return nil, researchdomain.ErrValidation
	}
	rows, err := sqlc.New(repository.db.Pool).ListReadableChatSessions(ctx, sqlc.ListReadableChatSessionsParams{
		ActorUserID: actor, ProjectID: project,
		HasScope: query.Scope != "", Scope: string(query.Scope), HasBookID: query.BookID != "", BookID: book,
		HasCursor: query.CursorID != "", CursorUpdatedAt: storedb.Timestamptz(query.CursorUpdatedAt),
		CursorID: cursor, LimitCount: int32(query.Limit),
	})
	if err != nil {
		return nil, mapError(err)
	}
	result := make([]researchdomain.Session, len(rows))
	for index, row := range rows {
		result[index] = sessionValue(sessionFromListSQL(row))
	}
	return result, nil
}

func (repository *Repository) GetSession(ctx context.Context, actor researchdomain.Actor, chatID string) (researchdomain.Session, error) {
	row, err := repository.getSession(ctx, actor.UserID, chatID)
	return sessionValue(row), mapError(err)
}

func (repository *Repository) getSession(ctx context.Context, actorUserID, chatID string) (sessionRecord, error) {
	actor, actorErr := storedb.ParseUUID(actorUserID)
	chat, chatErr := storedb.ParseUUID(chatID)
	if actorErr != nil || chatErr != nil {
		return sessionRecord{}, researchdomain.ErrValidation
	}
	row, err := sqlc.New(repository.db.Pool).GetReadableChatSession(ctx, sqlc.GetReadableChatSessionParams{
		ActorUserID: actor, ChatID: chat,
	})
	if err != nil {
		return sessionRecord{}, err
	}
	return sessionFromReadableSQL(row), nil
}

// rootThread names the root thread of a chat session and the flow of its kind.
func (repository *Repository) rootThread(ctx context.Context, projectID, chatID, actorUserID string) (string, agent.Flow, error) {
	row, err := repository.getSession(ctx, actorUserID, chatID)
	if err != nil {
		return "", "", err
	}
	flow, known := agent.SessionFlow(row.Kind)
	if row.ProjectID != projectID || !known {
		return "", "", researchdomain.ErrNotFound
	}
	return row.ThreadID, flow, nil
}

func (repository *Repository) lockWritableSessionInTx(ctx context.Context, tx pgx.Tx, projectID, chatID, actorUserID string) (writableSession, error) {
	if projectID == "" {
		return writableSession{}, researchdomain.ErrValidation
	}
	actor, actorErr := storedb.ParseUUID(actorUserID)
	chat, chatErr := storedb.ParseUUID(chatID)
	project, projectErr := storedb.ParseUUID(projectID)
	if actorErr != nil || chatErr != nil || projectErr != nil {
		return writableSession{}, researchdomain.ErrValidation
	}
	row, err := sqlc.New(tx).LockWritableChatSession(ctx, sqlc.LockWritableChatSessionParams{
		ActorUserID: actor, ChatID: chat, ProjectID: project,
	})
	if err != nil {
		return writableSession{}, err
	}
	session := sessionFromWritableSQL(row)
	view, err := governance.PresentSourceView(session.SourceView)
	if err != nil {
		return writableSession{}, researchdomain.ErrConflict
	}
	chatArm, err := governance.PresentChatArm(session.ProjectID, session.ID, session.LifecycleEpoch)
	if err != nil {
		return writableSession{}, researchdomain.ErrConflict
	}
	return writableSession{
		sessionRecord: session, ActorRole: row.ActorRole, ProjectLifecycleGeneration: row.ProjectLifecycleGeneration,
		SourceViewArm: view, ChatArm: chatArm,
	}, nil
}

func validSessionShape(view governance.SourceView, scope researchdomain.Scope, bookID string) bool {
	if view != governance.SourceViewDraft && view != governance.SourceViewPublished {
		return false
	}
	return scope == researchdomain.ScopeProject && bookID == "" || scope == researchdomain.ScopeBook && bookID != ""
}

func validSavedAssignment(value string) bool {
	return value == "" || value == strings.TrimSpace(value) && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 8000
}

func validSessionIdempotency(command service.CreateSessionCommand) bool {
	return strings.TrimSpace(command.ScopeHash) != "" && strings.TrimSpace(command.OperationID) != "" &&
		strings.TrimSpace(command.IdempotencyKeyHash) != "" && strings.TrimSpace(command.RequestHash) != "" &&
		command.StatusCode >= 200 && command.StatusCode <= 599
}

func validListFilters(query service.ListSessionsQuery) bool {
	if query.Scope != "" && query.Scope != researchdomain.ScopeProject && query.Scope != researchdomain.ScopeBook {
		return false
	}
	return query.BookID == "" || query.Scope == researchdomain.ScopeBook
}

func sessionInputIDs(id, projectID, actorUserID, bookID string) ([3]pgtype.UUID, pgtype.UUID, error) {
	var result [3]pgtype.UUID
	for index, value := range []string{id, projectID, actorUserID} {
		parsed, err := storedb.ParseUUID(value)
		if err != nil {
			return result, pgtype.UUID{}, err
		}
		result[index] = parsed
	}
	book, err := optionalUUID(bookID)
	return result, book, err
}

func optionalUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return storedb.ParseUUID(value)
}

func sessionFromSQL(row sqlc.ChatSession) sessionRecord {
	return sessionRecord{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID), SourceView: governance.SourceView(row.SourceView),
		Scope: researchdomain.Scope(row.Scope), BookID: storedb.UUIDString(row.BookID), Title: row.Title, SavedAssignment: row.SavedAssignment.String,
		ThreadID: row.ThreadID.String, LifecycleEpoch: governance.ChatLifecycleEpoch(row.LifecycleEpoch),
		AgentGeneration: int(row.AgentGeneration), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		Kind: row.Kind,
	}
}

func sessionFromReadableSQL(row sqlc.GetReadableChatSessionRow) sessionRecord {
	return sessionRecord{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID), SourceView: governance.SourceView(row.SourceView),
		Scope: researchdomain.Scope(row.Scope), BookID: storedb.UUIDString(row.BookID), Title: row.Title, SavedAssignment: row.SavedAssignment.String,
		ThreadID: row.ThreadID.String, LifecycleEpoch: governance.ChatLifecycleEpoch(row.LifecycleEpoch),
		AgentGeneration: int(row.AgentGeneration), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		Kind:         row.Kind,
		LatestTurnID: storedb.UUIDString(row.LatestTurnID), LatestTurnJobID: storedb.UUIDString(row.LatestTurnJobID),
		LatestTurnStatus: row.LatestTurnStatus.String, LatestTurnAt: row.LatestTurnAt.Time,
	}
}

func sessionFromWritableSQL(row sqlc.LockWritableChatSessionRow) sessionRecord {
	return sessionRecord{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID), SourceView: governance.SourceView(row.SourceView),
		Scope: researchdomain.Scope(row.Scope), BookID: storedb.UUIDString(row.BookID), Title: row.Title, SavedAssignment: row.SavedAssignment.String,
		ThreadID: row.ThreadID.String, LifecycleEpoch: governance.ChatLifecycleEpoch(row.LifecycleEpoch),
		AgentGeneration: int(row.AgentGeneration), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		Kind: row.Kind,
	}
}

func sessionFromListSQL(row sqlc.ListReadableChatSessionsRow) sessionRecord {
	return sessionRecord{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID), SourceView: governance.SourceView(row.SourceView),
		Scope: researchdomain.Scope(row.Scope), BookID: storedb.UUIDString(row.BookID), Title: row.Title, SavedAssignment: row.SavedAssignment.String,
		ThreadID: row.ThreadID.String, LifecycleEpoch: governance.ChatLifecycleEpoch(row.LifecycleEpoch),
		AgentGeneration: int(row.AgentGeneration), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		Kind:         row.Kind,
		LatestTurnID: storedb.UUIDString(row.LatestTurnID), LatestTurnJobID: storedb.UUIDString(row.LatestTurnJobID),
		LatestTurnStatus: row.LatestTurnStatus.String, LatestTurnAt: row.LatestTurnAt.Time,
	}
}

func sessionValue(value sessionRecord) researchdomain.Session {
	var savedAssignment *researchdomain.SavedAssignment
	if value.SavedAssignment != "" {
		savedAssignment = &researchdomain.SavedAssignment{
			Body: value.SavedAssignment, AvailableActions: []researchdomain.SavedAssignmentAction{researchdomain.SavedAssignmentStart},
		}
	}
	return researchdomain.Session{
		ID: value.ID, ProjectID: value.ProjectID, BookID: value.BookID,
		SourceView: string(value.SourceView), Scope: value.Scope, Title: value.Title,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		LatestTurnID: value.LatestTurnID, LatestTurnJobID: value.LatestTurnJobID, LatestTurnStatus: value.LatestTurnStatus,
		LatestTurnAt:    latestTurnAt(value.LatestTurnAt),
		SavedAssignment: savedAssignment, LifecycleEpoch: int64(value.LifecycleEpoch),
		AgentGeneration: value.AgentGeneration, Outdated: value.AgentGeneration < researchdomain.CurrentAgentGeneration,
		Kind: value.Kind,
	}
}

// latestTurnAt is the time of the latest turn of a chat, none for a chat without a turn.
func latestTurnAt(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	at = at.UTC()
	return &at
}
