package authormaterials

import (
	"context"
	"time"

	domain "canonkeeper/backend/internal/domain/authormaterials"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// ArchiveMaterial is a material a saga file brought: the record with its source and its dates.
type ArchiveMaterial struct {
	Draft      domain.Draft
	Key        string
	SourceName string
	SourceKind string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CreateArchiveInTx keeps the materials of a saga file, written by the author who opened it, on the transaction of the
// open (specs/2026-11-13-desk-sagafile.md SF3.7): one name is one record, as the file checked.
func CreateArchiveInTx(ctx context.Context, tx pgx.Tx, projectID, actorUserID string, materials []ArchiveMaterial) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return domain.ErrNotFound
	}
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return domain.ErrNotFound
	}
	for _, material := range materials {
		properties, err := propertiesJSON(material.Draft.Properties)
		if err != nil {
			return domain.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO author_materials (
			  project_id, name, name_key, kind, description, properties, section, source_name, source_kind,
			  created_by, updated_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $10, $11, $12)
		`, project, material.Draft.Name, material.Key, string(material.Draft.Kind), material.Draft.Description, properties,
			material.Draft.Section, material.SourceName, material.SourceKind, actor,
			storedb.Timestamptz(material.CreatedAt), storedb.Timestamptz(material.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}
