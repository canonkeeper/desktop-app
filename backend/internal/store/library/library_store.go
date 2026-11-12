package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projectaccess"
	"canonkeeper/backend/internal/projects"
	canonstore "canonkeeper/backend/internal/store/canon"
	"canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	jobstore "canonkeeper/backend/internal/store/jobs"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

type Store struct {
	*storedb.DB
	canon                   *canonstore.Store
	jobs                    *jobstore.Store
	quota                   quotastore.Fence
	deletionFailpoint       func(string) error
	importStagingFailpoint  func(string) error
	importFinalizeFailpoint func(string) error
}

type Option func(*Store)

// WithQuota gives the store the quota fence of the build (specs/2026-11-08-subscription-tiers.md TR2.4); without it the
// fence has no plan, as the open core and its tests run.
func WithQuota(fence quotastore.Fence) Option {
	return func(store *Store) {
		store.quota = fence
	}
}

func withDeletionFailpoint(failpoint func(string) error) Option {
	return func(store *Store) {
		store.deletionFailpoint = failpoint
	}
}

func withImportStagingFailpoint(failpoint func(string) error) Option {
	return func(store *Store) {
		store.importStagingFailpoint = failpoint
	}
}

func withImportFinalizeFailpoint(failpoint func(string) error) Option {
	return func(store *Store) {
		store.importFinalizeFailpoint = failpoint
	}
}

func New(db *storedb.DB, opts ...Option) *Store {
	store := &Store{
		DB:    db,
		canon: canonstore.New(db),
		jobs:  jobstore.New(db),
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

func (s *Store) artifactSourcesStore(failpoint func(string) error) *governancestore.Store {
	if failpoint == nil {
		return governancestore.New(s.DB)
	}
	return governancestore.New(s.DB, governancestore.WithFailpoint(failpoint))
}

func (s *Store) ActiveProjectRole(ctx context.Context, actorUserID, projectID string) (projects.Role, error) {
	actorUserUUID, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return "", library.ErrNotFound
	}
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", library.ErrNotFound
	}
	role, err := sqlc.New(s.Pool).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{
		ActorUserID: actorUserUUID,
		ProjectID:   projectUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", library.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return projects.Role(role), nil
}

func (s *Store) ResolveBookAccessForActor(ctx context.Context, actorUserID, bookID string, visibility projectaccess.Visibility) (projectaccess.ResolvedResourceAccess, error) {
	actorUserUUID, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return projectaccess.ResolvedResourceAccess{}, library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return projectaccess.ResolvedResourceAccess{}, library.ErrNotFound
	}
	if visibility != projectaccess.VisibilityActive && visibility != projectaccess.VisibilityDeletedSource {
		return projectaccess.ResolvedResourceAccess{}, library.ErrValidation
	}
	row, err := sqlc.New(s.Pool).ResolveBookAccessForActor(ctx, sqlc.ResolveBookAccessForActorParams{
		ActorUserID:        actorUserUUID,
		BookID:             bookUUID,
		ExpectedVisibility: string(visibility),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return projectaccess.ResolvedResourceAccess{}, library.ErrNotFound
	}
	if err != nil {
		return projectaccess.ResolvedResourceAccess{}, err
	}
	return projectaccess.ResolvedResourceAccess{
		ProjectID: row.ProjectID.String(),
		Identity: projectaccess.BookIdentity{
			BookID: row.BookID.String(),
		},
		ProjectRole: projects.Role(row.Role),
		Visibility:  projectaccess.Visibility(row.Visibility),
	}, nil
}

func (s *Store) LoadDeletedBookLifecycle(ctx context.Context, projectID, bookID string) (projectaccess.DeletedSourceLifecycle, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return "", library.ErrNotFound
	}
	lifecycle, err := sqlc.New(s.Pool).LoadDeletedBookLifecycle(ctx, sqlc.LoadDeletedBookLifecycleParams{
		ProjectID: projectUUID,
		BookID:    bookUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", library.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return projectaccess.DeletedSourceLifecycle(lifecycle), nil
}

func (s *Store) ResolveChapterAccessForActor(ctx context.Context, actorUserID, chapterID string, visibility projectaccess.Visibility) (projectaccess.ResolvedResourceAccess, error) {
	actorUserUUID, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return projectaccess.ResolvedResourceAccess{}, library.ErrNotFound
	}
	chapterUUID, err := storedb.ParseUUID(chapterID)
	if err != nil {
		return projectaccess.ResolvedResourceAccess{}, library.ErrNotFound
	}
	if visibility != projectaccess.VisibilityActive && visibility != projectaccess.VisibilityDeletedSource {
		return projectaccess.ResolvedResourceAccess{}, library.ErrValidation
	}
	row, err := sqlc.New(s.Pool).ResolveChapterAccessForActor(ctx, sqlc.ResolveChapterAccessForActorParams{
		ActorUserID:        actorUserUUID,
		ChapterID:          chapterUUID,
		ExpectedVisibility: string(visibility),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return projectaccess.ResolvedResourceAccess{}, library.ErrNotFound
	}
	if err != nil {
		return projectaccess.ResolvedResourceAccess{}, err
	}
	return projectaccess.ResolvedResourceAccess{
		ProjectID: row.ProjectID.String(),
		Identity: projectaccess.ChapterIdentity{
			BookID: row.BookID.String(), ChapterID: row.ChapterID.String(),
		},
		ProjectRole: projects.Role(row.Role),
		Visibility:  projectaccess.Visibility(row.Visibility),
	}, nil
}

func (s *Store) LoadDeletedChapterLifecycle(ctx context.Context, projectID, bookID, chapterID string) (projectaccess.DeletedSourceLifecycle, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return "", library.ErrNotFound
	}
	chapterUUID, err := storedb.ParseUUID(chapterID)
	if err != nil {
		return "", library.ErrNotFound
	}
	lifecycle, err := sqlc.New(s.Pool).LoadDeletedChapterLifecycle(ctx, sqlc.LoadDeletedChapterLifecycleParams{
		ProjectID: projectUUID,
		BookID:    bookUUID,
		ChapterID: chapterUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", library.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return projectaccess.DeletedSourceLifecycle(lifecycle), nil
}
