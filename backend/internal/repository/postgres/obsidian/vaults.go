// SPDX-License-Identifier: AGPL-3.0-only

// Package obsidian keeps the link of a saga with the folder of its vault of Obsidian (obsidian_vaults,
// specs/2026-11-13-desk-obsidian.md OB1.4, OB1.5): one row a saga, removed with the saga.
package obsidian

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/obsidian"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ service.Vaults = (*Vaults)(nil)

// Vaults is the store of the links.
type Vaults struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Vaults {
	if db == nil {
		panic("obsidian vaults: the database is required")
	}
	return &Vaults{db: db}
}

// Vault reads a live saga and its link for a member with the access.
func (vaults *Vaults) Vault(ctx context.Context, actorUserID, projectID string, access service.Access) (service.Linked, error) {
	var linked service.Linked
	err := vaults.db.WithTx(ctx, func(tx pgx.Tx) error {
		project, err := member(ctx, tx, actorUserID, projectID, access)
		if err != nil {
			return err
		}
		var vaultPath, exportDir pgtype.Text
		var exportedAt pgtype.Timestamptz
		err = tx.QueryRow(ctx, `
			SELECT p.name, v.vault_path, v.export_dir, v.last_export_at
			FROM projects AS p
			LEFT JOIN obsidian_vaults AS v ON v.project_id = p.id
			WHERE p.id = $1
		`, project).Scan(&linked.Title, &vaultPath, &exportDir, &exportedAt)
		linked.Path, linked.ExportDir = vaultPath.String, exportDir.String
		if exportedAt.Valid {
			at := exportedAt.Time.UTC()
			linked.ExportedAt = &at
		}
		return err
	})
	return linked, err
}

// Link keeps the path of the vault of a saga: the row of the saga, new or with a new path. The folder of the export stays
// what it was (OB1.4): a new path of the vault keeps every link of a book, whose folders are paths from its root; a new path
// forgets the time of the last export, which went into the vault of the path before
// (specs/2026-11-15-desk-obsidian-export.md OE1.2).
func (vaults *Vaults) Link(ctx context.Context, projectID, vaultPath string) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	_, err = vaults.db.Pool.Exec(ctx, `
		INSERT INTO obsidian_vaults (project_id, vault_path) VALUES ($1, $2)
		ON CONFLICT (project_id) DO UPDATE SET vault_path = EXCLUDED.vault_path,
			last_export_at = CASE WHEN obsidian_vaults.vault_path = EXCLUDED.vault_path THEN obsidian_vaults.last_export_at END
	`, project, vaultPath)
	return err
}

// Unlink removes the link of a saga for a member who changes its books (OB1.5). Nothing of the saga and nothing of the
// vault goes with it.
func (vaults *Vaults) Unlink(ctx context.Context, actorUserID, projectID string) error {
	return vaults.db.WithTx(ctx, func(tx pgx.Tx) error {
		project, err := member(ctx, tx, actorUserID, projectID, service.AccessWrite)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM obsidian_vaults WHERE project_id = $1`, project)
		return err
	})
}

// member checks the actor is a member of the live saga with the access: one who is not sees no saga.
func member(ctx context.Context, tx pgx.Tx, actorUserID, projectID string, access service.Access) (pgtype.UUID, error) {
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return pgtype.UUID{}, service.ErrNotFound
	}
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return pgtype.UUID{}, service.ErrNotFound
	}
	role, err := sqlc.New(tx).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pgtype.UUID{}, service.ErrNotFound
	case err != nil:
		return pgtype.UUID{}, err
	case !projects.CanReadProject(projects.Role(role)):
		return pgtype.UUID{}, service.ErrNotFound
	case access == service.AccessWrite && !projects.CanMutateProject(projects.Role(role)):
		return pgtype.UUID{}, service.ErrForbidden
	}
	return project, nil
}
