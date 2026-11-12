package projectstore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/projects"
	canonstore "canonkeeper/backend/internal/store/canon"
	"canonkeeper/backend/internal/store/db"
	librarystore "canonkeeper/backend/internal/store/library"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Store struct {
	*storedb.DB
	canon                    *canonstore.Store
	library                  *librarystore.Store
	quota                    quotastore.Fence
	projectDeletionFailpoint func(string) error
}

type Option func(*Store)

// WithQuota gives the store the quota fence of the build (specs/2026-11-08-subscription-tiers.md TR2.4); without it the
// fence has no plan, as the open core and its tests run.
func WithQuota(fence quotastore.Fence) Option {
	return func(store *Store) {
		store.quota = fence
	}
}

const listProjectsForActorCursorSQL = `
SELECT
  p.id,
  p.name,
  p.description,
  project_counts.book_count,
  project_counts.chapter_count,
  project_counts.word_count,
  p.created_at,
  p.updated_at,
  m.id,
  m.project_id,
  m.user_id,
  m.role,
  m.removed_at,
  m.created_at,
  m.updated_at,
  p.demo
FROM projects p
JOIN project_memberships m ON m.project_id = p.id
LEFT JOIN LATERAL (
  SELECT
    COUNT(DISTINCT b.id)::integer AS book_count,
    COUNT(DISTINCT c.id)::integer AS chapter_count,
    COALESCE(SUM(cp.word_count), 0)::integer AS word_count
  FROM books b
  LEFT JOIN chapters c ON c.book_id = b.id AND c.deleted_at IS NULL
  LEFT JOIN chapter_paragraphs cp
    ON cp.chapter_id = c.id
    AND cp.revision = c.revision
    AND cp.variant = 'draft'
  WHERE b.project_id = p.id
    AND b.deleted_at IS NULL
) project_counts ON true
WHERE p.deleted_at IS NULL
  AND m.user_id = $1
  AND m.removed_at IS NULL
  AND (NOT $2::boolean OR (p.created_at, p.id) < ($3::timestamptz, $4::uuid))
ORDER BY p.created_at DESC, p.id DESC
LIMIT $5`

type projectActorCollectionRow struct {
	ID                  pgtype.UUID
	Name                string
	Description         string
	BookCount           int32
	ChapterCount        int32
	WordCount           int32
	CreatedAt           pgtype.Timestamptz
	UpdatedAt           pgtype.Timestamptz
	MembershipID        pgtype.UUID
	MembershipProjectID pgtype.UUID
	MembershipUserID    pgtype.UUID
	MembershipRole      string
	MembershipRemovedAt pgtype.Timestamptz
	MembershipCreatedAt pgtype.Timestamptz
	MembershipUpdatedAt pgtype.Timestamptz
	Demo                bool
}

func New(db *storedb.DB, options ...Option) *Store {
	store := &Store{
		DB:      db,
		canon:   canonstore.New(db),
		library: librarystore.New(db),
	}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return store
}

func (s *Store) CreateProjectWithOwnerMembership(ctx context.Context, input projects.CreateProjectStoreInput) (projects.ProjectRecord, error) {
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return projects.ProjectRecord{}, err
	}
	ownerUserID, err := storedb.ParseUUID(input.OwnerUserID)
	if err != nil {
		return projects.ProjectRecord{}, err
	}
	membershipID, err := storedb.ParseUUID(input.OwnerMembershipID)
	if err != nil {
		return projects.ProjectRecord{}, err
	}
	createdAt := storedb.Timestamptz(input.CreatedAt)

	var createdProject sqlc.CreateProjectRow
	var createdMembership sqlc.ProjectMembership
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		// A blocked or deleted account creates no project; the lock orders this with blocking
		// and deletion of the account (specs/2026-10-28-account-data-rights.md, R3.10).
		var enabled bool
		if err := tx.QueryRow(ctx, `
			SELECT true FROM users WHERE id = $1 AND disabled_at IS NULL FOR SHARE
		`, ownerUserID).Scan(&enabled); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrForbidden
		} else if err != nil {
			return err
		}
		// The demo saga is not a saga of the limit of its owner (specs/2026-11-09-demo-saga.md DS3.2); the index of
		// the table lets him have one live.
		if !input.Demo {
			if err := s.quota.CheckOwnerProjectsInTx(ctx, tx, ownerUserID.String(), 1); err != nil {
				return err
			}
		}
		queries := sqlc.New(tx)
		createdProject, err = queries.CreateProject(ctx, sqlc.CreateProjectParams{
			ID: projectID, OwnerUserID: ownerUserID, Name: input.Title,
			Description: input.Description, Demo: input.Demo, CreatedAt: createdAt,
		})
		if isSecondLiveDemoSaga(err) {
			return projects.ErrConflict
		}
		if err != nil {
			return err
		}

		createdMembership, err = queries.CreateProjectMembership(ctx, sqlc.CreateProjectMembershipParams{
			ID:        membershipID,
			ProjectID: projectID,
			UserID:    ownerUserID,
			Role:      string(projects.RoleOwner),
			CreatedAt: createdAt,
		})
		if err != nil {
			return err
		}
		if _, err = s.library.CreateDraftProjectSourceView(ctx, tx, projectID, 1); err != nil {
			return err
		}
		return s.runProjectDeletionFailpoint("create_after_catalog_mutation")
	})
	if err != nil {
		return projects.ProjectRecord{}, err
	}
	return projectRecord(createdProject, createdMembership), nil
}

func (s *Store) ListProjectsForActor(ctx context.Context, input projects.ListProjectsStoreInput) ([]projects.ProjectRecord, error) {
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return nil, err
	}
	if input.State == projects.ProjectListDeleted {
		return s.listDeletedProjectsForActor(ctx, input, actorUserID)
	}
	if input.State != "" && input.State != projects.ProjectListActive {
		return nil, projects.ErrValidation
	}
	cursorCreatedAt, cursorID, err := actorCollectionCursorValues(input.HasCursor, input.CursorCreatedAt, input.CursorID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, listProjectsForActorCursorSQL,
		actorUserID,
		input.HasCursor,
		cursorCreatedAt,
		cursorID,
		actorCollectionLimit(input.Limit, 50),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]projects.ProjectRecord, 0)
	for rows.Next() {
		var row projectActorCollectionRow
		if err := rows.Scan(
			&row.ID,
			&row.Name,
			&row.Description,
			&row.BookCount,
			&row.ChapterCount,
			&row.WordCount,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.MembershipID,
			&row.MembershipProjectID,
			&row.MembershipUserID,
			&row.MembershipRole,
			&row.MembershipRemovedAt,
			&row.MembershipCreatedAt,
			&row.MembershipUpdatedAt,
			&row.Demo,
		); err != nil {
			return nil, err
		}
		records = append(records, projectRecordFromActorCollection(row))
	}
	return records, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, actorUserID, projectID string) (projects.ProjectRecord, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return projects.ProjectRecord{}, projects.ErrNotFound
	}
	actorUserUUID, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return projects.ProjectRecord{}, err
	}

	row, err := sqlc.New(s.Pool).GetProjectForActor(ctx, sqlc.GetProjectForActorParams{
		ProjectID:   projectUUID,
		ActorUserID: actorUserUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.ProjectRecord{}, projects.ErrNotFound
	}
	if err != nil {
		return projects.ProjectRecord{}, err
	}
	return projectRecordFromGetProjectForActor(row), nil
}

// DemoProject reads the live demo saga of the owner as he sees it.
func (s *Store) DemoProject(ctx context.Context, ownerUserID string) (projects.ProjectRecord, error) {
	ownerUserUUID, err := storedb.ParseUUID(ownerUserID)
	if err != nil {
		return projects.ProjectRecord{}, err
	}
	var projectID pgtype.UUID
	if err := s.Pool.QueryRow(ctx, `
		SELECT id FROM projects WHERE owner_user_id = $1 AND demo AND deleted_at IS NULL
	`, ownerUserUUID).Scan(&projectID); errors.Is(err, pgx.ErrNoRows) {
		return projects.ProjectRecord{}, projects.ErrNotFound
	} else if err != nil {
		return projects.ProjectRecord{}, err
	}
	return s.GetProject(ctx, ownerUserID, projectID.String())
}

// isSecondLiveDemoSaga tells the refusal of the index that lets an owner have one live demo saga.
func isSecondLiveDemoSaga(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "projects_one_live_demo_per_owner"
}

func (s *Store) UpdateProject(ctx context.Context, input projects.UpdateProjectStoreInput) (projects.ProjectRecord, error) {
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return projects.ProjectRecord{}, projects.ErrNotFound
	}
	actorUserUUID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return projects.ProjectRecord{}, err
	}

	row, err := sqlc.New(s.Pool).UpdateProjectForActor(ctx, sqlc.UpdateProjectForActorParams{
		Name:        input.Title,
		Description: input.Description,
		UpdatedAt:   storedb.Timestamptz(input.UpdatedAt),
		ProjectID:   projectUUID,
		ActorUserID: actorUserUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.ProjectRecord{}, projects.ErrNotFound
	}
	if err != nil {
		return projects.ProjectRecord{}, err
	}
	return projectRecordFromUpdateProjectForActor(row), nil
}

func projectRecord(row sqlc.CreateProjectRow, membership sqlc.ProjectMembership) projects.ProjectRecord {
	return projects.ProjectRecord{
		ID:                row.ID.String(),
		Title:             row.Name,
		Description:       row.Description,
		Demo:              row.Demo,
		BookCount:         int(row.BookCount),
		ChapterCount:      int(row.ChapterCount),
		WordCount:         int(row.WordCount),
		Revision:          row.Revision,
		CreatedAt:         row.CreatedAt.Time,
		UpdatedAt:         row.UpdatedAt.Time,
		CurrentMembership: projectMembershipRecord(membership),
	}
}

func projectRecordFromActorCollection(row projectActorCollectionRow) projects.ProjectRecord {
	return projects.ProjectRecord{
		ID:           row.ID.String(),
		Title:        row.Name,
		Description:  row.Description,
		Demo:         row.Demo,
		BookCount:    int(row.BookCount),
		ChapterCount: int(row.ChapterCount),
		WordCount:    int(row.WordCount),
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
		CurrentMembership: projectMembershipRecordFromColumns(
			row.MembershipID,
			row.MembershipProjectID,
			row.MembershipUserID,
			row.MembershipRole,
			row.MembershipRemovedAt,
			row.MembershipCreatedAt,
			row.MembershipUpdatedAt,
		),
	}
}

func projectRecordFromGetProjectForActor(row sqlc.GetProjectForActorRow) projects.ProjectRecord {
	return projects.ProjectRecord{
		ID:           row.ID.String(),
		Title:        row.Name,
		Description:  row.Description,
		Demo:         row.Demo,
		BookCount:    int(row.BookCount),
		ChapterCount: int(row.ChapterCount),
		WordCount:    int(row.WordCount),
		Revision:     row.Revision,
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
		CurrentMembership: projectMembershipRecordFromColumns(
			row.MembershipID,
			row.MembershipProjectID,
			row.MembershipUserID,
			row.MembershipRole,
			row.MembershipRemovedAt,
			row.MembershipCreatedAt,
			row.MembershipUpdatedAt,
		),
	}
}

func projectRecordFromUpdateProjectForActor(row sqlc.UpdateProjectForActorRow) projects.ProjectRecord {
	return projects.ProjectRecord{
		ID:           row.ID.String(),
		Title:        row.Name,
		Description:  row.Description,
		Demo:         row.Demo,
		BookCount:    int(row.BookCount),
		ChapterCount: int(row.ChapterCount),
		WordCount:    int(row.WordCount),
		Revision:     row.Revision,
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
		CurrentMembership: projectMembershipRecordFromColumns(
			row.MembershipID,
			row.MembershipProjectID,
			row.MembershipUserID,
			row.MembershipRole,
			row.MembershipRemovedAt,
			row.MembershipCreatedAt,
			row.MembershipUpdatedAt,
		),
	}
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func actorCollectionCursorValues(hasCursor bool, createdAt time.Time, id string) (pgtype.Timestamptz, pgtype.UUID, error) {
	if !hasCursor {
		return pgtype.Timestamptz{}, pgtype.UUID{}, nil
	}
	if createdAt.IsZero() {
		return pgtype.Timestamptz{}, pgtype.UUID{}, projects.ErrValidation
	}
	cursorID, err := storedb.ParseUUID(id)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, projects.ErrValidation
	}
	return storedb.Timestamptz(createdAt), cursorID, nil
}

func actorCollectionLimit(limit, defaultLimit int) int32 {
	if limit <= 0 {
		return int32(defaultLimit)
	}
	if limit > 101 {
		return 101
	}
	return int32(limit)
}

func projectListLimit(limit int) int32 {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return int32(limit)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
