package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) hitImportFinalize(point string) error {
	if s == nil || s.importFinalizeFailpoint == nil {
		return nil
	}
	return s.importFinalizeFailpoint(point)
}

func lockActiveImportProject(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) error {
	return lockImportProject(ctx, tx, projectID, true)
}

func lockImportProject(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, requireActive bool) error {
	query := `SELECT id FROM projects WHERE id = $1 FOR SHARE`
	if requireActive {
		query = `SELECT id FROM projects WHERE id = $1 AND deleted_at IS NULL FOR SHARE`
	}
	var lockedProjectID pgtype.UUID
	if err := tx.QueryRow(ctx, query, projectID).Scan(&lockedProjectID); errors.Is(err, pgx.ErrNoRows) {
		return library.ErrNotFound
	} else {
		return err
	}
}

func lockImportProjectActor(ctx context.Context, tx pgx.Tx, projectID, actorUserID pgtype.UUID) error {
	if err := lockActiveImportProject(ctx, tx, projectID); err != nil {
		return err
	}
	var role string
	if err := tx.QueryRow(ctx, `
		SELECT role
		FROM project_memberships
		WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL
		FOR SHARE
	`, projectID, actorUserID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return library.ErrNotFound
	} else if err != nil {
		return err
	}
	if !projects.CanMutateProject(projects.Role(role)) {
		return library.ErrForbidden
	}
	return nil
}

func importJobFromExecutionRow(row sqlc.GetImportJobForExecutionRow) (library.ImportJob, error) {
	job, err := importJobBase(row.ID, row.RequestProjectID, row.Kind, row.Status, row.SubjectJson, row.ExpiresAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return library.ImportJob{}, err
	}
	job.ActorUserID = row.ActorUserID.String()
	job.CanCancel = row.CanCancel
	job.Metadata = library.ImportFileMetadata{
		StorageKey:         row.StorageKey,
		SourceFileName:     row.SourceFileName,
		Extension:          row.Extension,
		MIMEType:           row.MimeType,
		SizeBytes:          row.SizeBytes,
		FileSHA256:         row.FileSha256,
		IdempotencyKey:     row.IdempotencyKey,
		ImportMode:         library.ImportMode(row.ImportMode),
		InitialPublication: library.InitialPublication(row.InitialPublication),
		Title:              textPtr(row.Title),
	}
	if row.TargetBookID.Valid {
		job.Metadata.TargetBookID = row.TargetBookID.String()
	}
	job.RequestFingerprint = row.RequestFingerprint
	return job, nil
}

func importJobBase(id, projectID pgtype.UUID, kind, status string, subjectJSON []byte, expiresAt, createdAt, updatedAt pgtype.Timestamptz) (library.ImportJob, error) {
	projectIDValue := projectID.String()
	subject, err := jobs.DecodeSubject(jobs.Kind(kind), subjectJSON)
	if err != nil {
		return library.ImportJob{}, library.ErrConflict
	}
	importSubject, ok := subject.(jobs.ImportSubject)
	if !ok || importSubject.ProjectID != projectIDValue {
		return library.ImportJob{}, library.ErrConflict
	}
	return library.ImportJob{
		ID:        id.String(),
		ProjectID: projectIDValue,
		Kind:      kind,
		Status:    status,
		Subject: library.ImportJobSubject{
			Type:              string(importSubject.Type),
			ID:                importSubject.ID,
			ProjectID:         importSubject.ProjectID,
			ExpectedBookOrder: importSubject.ExpectedBookOrder,
			BookID:            importSubject.BookID,
		},
		ExpiresAt: storedb.OptionalTime(expiresAt),
		CreatedAt: createdAt.Time,
		UpdatedAt: updatedAt.Time,
	}, nil
}
