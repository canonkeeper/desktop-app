package sagamemory

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// IndexedVersions lists the text versions with indexed chapters of live projects someone can
// open: an active owner or an active member. Projects of disabled accounts get no cards.
func (repository *Repository) IndexedVersions(ctx context.Context) ([]service.TextVersion, error) {
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT DISTINCT state.project_id, state.source_view
		FROM chapter_text_index_state AS state
		JOIN projects AS project ON project.id = state.project_id AND project.deleted_at IS NULL
		WHERE EXISTS (
		    SELECT 1 FROM users AS owner
		    WHERE owner.id = project.owner_user_id AND owner.disabled_at IS NULL
		  ) OR EXISTS (
		    SELECT 1
		    FROM project_memberships AS member
		    JOIN users AS person ON person.id = member.user_id AND person.disabled_at IS NULL
		    WHERE member.project_id = project.id AND member.removed_at IS NULL
		  )
		ORDER BY state.project_id, state.source_view
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []service.TextVersion
	for rows.Next() {
		var projectID pgtype.UUID
		var view string
		if err := rows.Scan(&projectID, &view); err != nil {
			return nil, err
		}
		versions = append(versions, service.TextVersion{ProjectID: storedb.UUIDString(projectID), View: view})
	}
	return versions, rows.Err()
}

// errNoTextIndex refuses a cards Job to a repository given no text index store: the rule of who queues it — not in a build
// without its helper — is the store's, and the process has one (specs/2026-11-12-desk-window.md WN3.4).
var errNoTextIndex = errors.New("saga memory: the cards Job is queued only through the text index store of the process")

// QueueCards queues the cards Job of a text version through the text index owner, which
// queues it after every text index as well, and reports whether a Job waits for the version.
func (repository *Repository) QueueCards(ctx context.Context, version service.TextVersion) (bool, error) {
	if repository.textIndex == nil {
		return false, errNoTextIndex
	}
	return repository.textIndex.QueueChapterCards(ctx, version.ProjectID, governance.SourceView(version.View))
}
