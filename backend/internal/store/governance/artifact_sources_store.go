package governancestore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrArtifactSourcesConflict   = errors.New("store: artifact sources conflict")
	ErrArtifactSourcesValidation = errors.New("store: invalid artifact sources")
)

type SealArtifactSourcesInput struct {
	OwnerID    string
	ProjectID  string
	ArtifactID string
	Kind       governance.ArtifactKind
	SourceView governance.SourceViewArm
	Refs       []governance.ArtifactSourceRef
	RefIDs     []string
	Manifest   governance.ArtifactManifest
}

type ReplaceArtifactSourcesInput struct {
	SealArtifactSourcesInput
	ExpectedManifest governance.ArtifactManifest
}

// ExpectedArtifactSourcesManifest — снимок манифеста, с которым сравнивается
// текущий владелец перед заменой ссылок в уже открытой транзакции.
type ExpectedArtifactSourcesManifest struct {
	Version  int32
	Hash     []byte
	RefCount int32
}

type DeleteArtifactWithSourcesInput struct {
	OwnerID          string
	ProjectID        string
	ArtifactID       string
	Kind             governance.ArtifactKind
	ExpectedManifest governance.ArtifactManifest
}

type ArtifactSourceCreateMutation func(context.Context, pgx.Tx, pgtype.UUID) error
type ArtifactSourceMutation func(context.Context, pgx.Tx, string) error
type ArtifactSourceDeleteMutation func(context.Context, pgx.Tx) error

type Store struct {
	*storedb.DB
	failpoint func(string) error
}

type Option func(*Store)

func New(db *storedb.DB, options ...Option) *Store {
	store := &Store{DB: db}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return store
}

func NewArtifactSourceOwnerID(ctx context.Context, tx pgx.Tx) (string, error) {
	if tx == nil {
		return "", ErrArtifactSourcesValidation
	}
	var ownerID string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&ownerID); err != nil {
		return "", err
	}
	return ownerID, nil
}

func WithFailpoint(failpoint func(string) error) Option {
	return func(store *Store) {
		store.failpoint = failpoint
	}
}

func (s *Store) CreateArtifactWithSources(
	ctx context.Context,
	tx pgx.Tx,
	input SealArtifactSourcesInput,
	mutate ArtifactSourceCreateMutation,
) error {
	if tx == nil || mutate == nil {
		return ErrArtifactSourcesValidation
	}
	prepared, err := prepareArtifactSources(input)
	if err != nil {
		return err
	}
	if err := lockCurrentArtifactSourceRefs(ctx, tx, prepared); err != nil {
		return err
	}
	if err := s.hit("before_owner"); err != nil {
		return err
	}
	queries := sqlc.New(tx)
	if err := queries.CreateArtifactSourceOwner(ctx, prepared.ownerParams); err != nil {
		return mapArtifactSourcesWriteError(err)
	}
	if err := s.hit("after_owner"); err != nil {
		return err
	}
	if err := s.createRefs(ctx, tx, queries, prepared); err != nil {
		return err
	}
	if err := s.hit("before_artifact"); err != nil {
		return err
	}
	if err := mutate(ctx, tx, prepared.ownerParams.ID); err != nil {
		return err
	}
	if err := s.hit("after_artifact"); err != nil {
		return err
	}
	return s.seal(ctx, queries, prepared)
}

// PrelockArtifactSourceRefs блокирует текущие lifecycle-строки для набора
// ссылок, когда вызывающая операция уже открыла общую транзакцию.
func (s *Store) PrelockArtifactSourceRefs(
	ctx context.Context,
	tx pgx.Tx,
	projectID string,
	refs []governance.ArtifactSourceRef,
) error {
	projectUUID, err := storedb.ParseUUID(projectID)
	if tx == nil || err != nil || len(refs) == 0 {
		return ErrArtifactSourcesValidation
	}
	prepared := preparedArtifactSources{
		ownerParams: sqlc.CreateArtifactSourceOwnerParams{ProjectID: projectUUID},
		refs:        make([]sqlc.CreateArtifactSourceRefsParams, 0, len(refs)),
	}
	for _, ref := range refs {
		fields, fieldsErr := ref.Fields()
		if fieldsErr != nil || fields.ProjectID != projectID {
			return ErrArtifactSourcesValidation
		}
		params, fieldsErr := ArtifactSourceRefParams(pgtype.UUID{}, projectUUID, fields)
		if fieldsErr != nil {
			return fieldsErr
		}
		prepared.refs = append(prepared.refs, params)
	}
	return lockCurrentArtifactSourceRefs(ctx, tx, prepared)
}

func (s *Store) ReplaceArtifactSources(
	ctx context.Context,
	tx pgx.Tx,
	input ReplaceArtifactSourcesInput,
	mutate ArtifactSourceMutation,
) error {
	if tx == nil || mutate == nil || !validArtifactManifestValue(input.ExpectedManifest) {
		return ErrArtifactSourcesValidation
	}
	expectedHash := input.ExpectedManifest.Hash()
	return s.ReplaceArtifactSourcesInTx(ctx, tx, input.SealArtifactSourcesInput, ExpectedArtifactSourcesManifest{
		Version: int32(input.ExpectedManifest.Version()), Hash: expectedHash[:],
		RefCount: int32(input.ExpectedManifest.RefCount()),
	}, mutate)
}

func (s *Store) ReplaceArtifactSourcesInTx(
	ctx context.Context,
	tx pgx.Tx,
	input SealArtifactSourcesInput,
	expected ExpectedArtifactSourcesManifest,
	mutate ArtifactSourceMutation,
) error {
	if tx == nil || mutate == nil || (expected.Version != 1 && expected.Version != 2) ||
		len(expected.Hash) != 32 || expected.RefCount < 1 {
		return ErrArtifactSourcesValidation
	}
	prepared, err := prepareArtifactSources(input)
	if err != nil {
		return err
	}
	if err := lockCurrentArtifactSourceRefs(ctx, tx, prepared); err != nil {
		return err
	}
	if err := s.hit("before_owner"); err != nil {
		return err
	}
	queries := sqlc.New(tx)
	_, err = queries.ReplaceArtifactSourceOwnerManifest(ctx, sqlc.ReplaceArtifactSourceOwnerManifestParams{
		SourceView:              prepared.ownerParams.SourceView,
		NewManifestVersion:      prepared.ownerParams.ManifestVersion,
		NewManifestHash:         prepared.ownerParams.ManifestHash,
		NewRefCount:             prepared.ownerParams.RefCount,
		SourceIdentityState:     prepared.ownerParams.SourceIdentityState,
		OwnerID:                 prepared.ownerParams.ID,
		ProjectID:               prepared.ownerParams.ProjectID,
		ArtifactKind:            prepared.ownerParams.ArtifactKind,
		ArtifactID:              prepared.ownerParams.ArtifactID,
		ExpectedManifestVersion: expected.Version,
		ExpectedManifestHash:    expected.Hash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArtifactSourcesConflict
	}
	if err != nil {
		return mapArtifactSourcesWriteError(err)
	}
	if err := s.hit("after_owner"); err != nil {
		return err
	}
	deleted, err := queries.DeleteArtifactSourceRefs(ctx, sqlc.DeleteArtifactSourceRefsParams{
		OwnerID: prepared.ownerParams.ID, ProjectID: prepared.ownerParams.ProjectID,
	})
	if err != nil {
		return err
	}
	if deleted != int64(expected.RefCount) {
		return ErrArtifactSourcesConflict
	}
	if err := s.createRefs(ctx, tx, queries, prepared); err != nil {
		return err
	}
	if err := s.hit("before_artifact"); err != nil {
		return err
	}
	if err := mutate(ctx, tx, input.OwnerID); err != nil {
		return err
	}
	if err := s.hit("after_artifact"); err != nil {
		return err
	}
	return s.seal(ctx, queries, prepared)
}

func (s *Store) DeleteArtifactWithSources(
	ctx context.Context,
	tx pgx.Tx,
	input DeleteArtifactWithSourcesInput,
	mutate ArtifactSourceDeleteMutation,
) error {
	if tx == nil || mutate == nil || !validArtifactManifestValue(input.ExpectedManifest) {
		return ErrArtifactSourcesValidation
	}
	ownerID, projectID, artifactID, err := artifactSourceIdentityValues(
		input.OwnerID, input.ProjectID, input.ArtifactID,
	)
	if err != nil || !knownArtifactKind(input.Kind) {
		return ErrArtifactSourcesValidation
	}
	if err := s.hit("before_owner"); err != nil {
		return err
	}
	queries := sqlc.New(tx)
	expectedHash := input.ExpectedManifest.Hash()
	_, err = queries.LockArtifactSourceOwnerForDelete(ctx, sqlc.LockArtifactSourceOwnerForDeleteParams{
		OwnerID: ownerID, ProjectID: projectID, ArtifactKind: string(input.Kind), ArtifactID: artifactID,
		ExpectedManifestVersion: int32(input.ExpectedManifest.Version()),
		ExpectedManifestHash:    expectedHash[:],
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArtifactSourcesConflict
	}
	if err != nil {
		return err
	}
	if err := s.hit("after_owner"); err != nil {
		return err
	}
	if err := s.hit("before_artifact"); err != nil {
		return err
	}
	if err := mutate(ctx, tx); err != nil {
		return err
	}
	if err := s.hit("after_artifact"); err != nil {
		return err
	}
	deletedRefs, err := queries.DeleteArtifactSourceRefs(ctx, sqlc.DeleteArtifactSourceRefsParams{
		OwnerID: ownerID, ProjectID: projectID,
	})
	if err != nil {
		return err
	}
	if deletedRefs != int64(input.ExpectedManifest.RefCount()) {
		return ErrArtifactSourcesConflict
	}
	if err := s.hit("after_ref_0"); err != nil {
		return err
	}
	if err := s.hit("before_seal"); err != nil {
		return err
	}
	deletedOwner, err := queries.DeleteArtifactSourceOwner(ctx, sqlc.DeleteArtifactSourceOwnerParams{
		OwnerID: ownerID, ProjectID: projectID, ArtifactKind: string(input.Kind), ArtifactID: artifactID,
		ExpectedManifestVersion: int32(input.ExpectedManifest.Version()),
		ExpectedManifestHash:    expectedHash[:],
	})
	if err != nil {
		return err
	}
	if deletedOwner != 1 {
		return ErrArtifactSourcesConflict
	}
	return s.hit("after_seal")
}

type preparedArtifactSources struct {
	ownerParams sqlc.CreateArtifactSourceOwnerParams
	refs        []sqlc.CreateArtifactSourceRefsParams
}

type artifactSourceBookLock struct {
	id         pgtype.UUID
	generation int64
}

type artifactSourceChapterLock struct {
	id         pgtype.UUID
	bookID     pgtype.UUID
	generation int64
}

type artifactSourceChatLock struct {
	id    pgtype.UUID
	epoch int64
}

func lockCurrentArtifactSourceRefs(ctx context.Context, tx pgx.Tx, prepared preparedArtifactSources) error {
	if tx == nil || len(prepared.refs) == 0 {
		return ErrArtifactSourcesValidation
	}
	projectGeneration := prepared.refs[0].ProjectGeneration
	books := make(map[string]artifactSourceBookLock)
	chapters := make(map[string]artifactSourceChapterLock)
	chats := make(map[string]artifactSourceChatLock)
	for _, ref := range prepared.refs {
		if ref.ProjectGeneration != projectGeneration {
			return ErrArtifactSourcesConflict
		}
		if ref.BookID.Valid {
			book := artifactSourceBookLock{id: ref.BookID, generation: ref.BookGeneration.Int64}
			key := ref.BookID.String()
			if current, exists := books[key]; exists && current != book {
				return ErrArtifactSourcesConflict
			}
			books[key] = book
		}
		if ref.ChapterID.Valid {
			chapter := artifactSourceChapterLock{
				id: ref.ChapterID, bookID: ref.BookID, generation: ref.ChapterGeneration.Int64,
			}
			key := ref.ChapterID.String()
			if current, exists := chapters[key]; exists && current != chapter {
				return ErrArtifactSourcesConflict
			}
			chapters[key] = chapter
		}
		if ref.ChatSessionID.Valid {
			chat := artifactSourceChatLock{id: ref.ChatSessionID, epoch: ref.ChatLifecycleEpoch.Int64}
			key := ref.ChatSessionID.String()
			if current, exists := chats[key]; exists && current != chat {
				return ErrArtifactSourcesConflict
			}
			chats[key] = chat
		}
	}

	batch := &pgx.Batch{}
	batch.Queue(`
		SELECT 1
		FROM projects
		WHERE id = $1 AND lifecycle_generation = $2 AND deleted_at IS NULL
		FOR SHARE
	`, prepared.ownerParams.ProjectID, projectGeneration)
	for _, key := range sortedArtifactSourceLockKeys(books) {
		book := books[key]
		batch.Queue(`
			SELECT 1
			FROM books
			WHERE id = $1 AND project_id = $2
			  AND lifecycle_generation = $3 AND deleted_at IS NULL
			FOR SHARE
		`, book.id, prepared.ownerParams.ProjectID, book.generation)
	}
	for _, key := range sortedArtifactSourceLockKeys(chapters) {
		chapter := chapters[key]
		batch.Queue(`
			SELECT 1
			FROM chapters
			WHERE id = $1 AND book_id = $2 AND project_id = $3
			  AND lifecycle_generation = $4 AND deleted_at IS NULL
			FOR SHARE
		`, chapter.id, chapter.bookID, prepared.ownerParams.ProjectID, chapter.generation)
	}
	for _, key := range sortedArtifactSourceLockKeys(chats) {
		chat := chats[key]
		batch.Queue(`
			SELECT 1
			FROM chat_sessions
			WHERE id = $1 AND project_id = $2 AND lifecycle_epoch = $3
			  AND deleted_at IS NULL
			FOR SHARE
		`, chat.id, prepared.ownerParams.ProjectID, chat.epoch)
	}
	results := tx.SendBatch(ctx, batch)
	for index := 0; index < batch.Len(); index++ {
		var marker int
		if err := results.QueryRow().Scan(&marker); err != nil {
			_ = results.Close()
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrArtifactSourcesConflict
			}
			return err
		}
	}
	return results.Close()
}

func sortedArtifactSourceLockKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func prepareArtifactSources(input SealArtifactSourcesInput) (preparedArtifactSources, error) {
	ownerID, projectID, artifactID, err := artifactSourceIdentityValues(
		input.OwnerID, input.ProjectID, input.ArtifactID,
	)
	if err != nil || len(input.Refs) > math.MaxInt32 ||
		(len(input.RefIDs) != 0 && len(input.RefIDs) != len(input.Refs)) {
		return preparedArtifactSources{}, ErrArtifactSourcesValidation
	}
	manifestInput := governance.ArtifactManifestInput{
		Kind: input.Kind, ProjectID: input.ProjectID, ArtifactID: input.ArtifactID,
		OwnerSourceView: input.SourceView, Refs: input.Refs,
	}
	var wantManifest governance.ArtifactManifest
	switch input.Manifest.Version() {
	case 1:
		wantManifest, err = governance.NewArtifactManifest(manifestInput)
	case 2:
		wantManifest, err = governance.NewArtifactManifestV2(manifestInput)
	default:
		return preparedArtifactSources{}, ErrArtifactSourcesValidation
	}
	if err != nil || !sameArtifactManifest(input.Manifest, wantManifest) {
		return preparedArtifactSources{}, ErrArtifactSourcesValidation
	}
	manifestHash := input.Manifest.Hash()
	prepared := preparedArtifactSources{
		ownerParams: sqlc.CreateArtifactSourceOwnerParams{
			ID: ownerID, ProjectID: projectID, ArtifactKind: string(input.Kind), ArtifactID: artifactID,
			SourceView:      artifactSourceViewValue(input.SourceView),
			ManifestVersion: int32(input.Manifest.Version()),
			ManifestHash:    manifestHash[:], RefCount: int32(input.Manifest.RefCount()),
			SourceIdentityState: string(input.Manifest.SourceIdentityState()),
		},
		refs: make([]sqlc.CreateArtifactSourceRefsParams, 0, len(input.Refs)),
	}
	for index, ref := range input.Refs {
		fields, err := ref.Fields()
		if err != nil || fields.ProjectID != input.ProjectID {
			return preparedArtifactSources{}, ErrArtifactSourcesValidation
		}
		params, err := ArtifactSourceRefParams(ownerID, projectID, fields)
		if err != nil {
			return preparedArtifactSources{}, err
		}
		if len(input.RefIDs) != 0 {
			params.ID, err = storedb.ParseUUID(input.RefIDs[index])
			if err != nil {
				return preparedArtifactSources{}, ErrArtifactSourcesValidation
			}
		}
		prepared.refs = append(prepared.refs, params)
	}
	return prepared, nil
}

func ArtifactSourceRefParams(
	ownerID pgtype.UUID,
	projectID pgtype.UUID,
	fields governance.ArtifactSourceRefFields,
) (sqlc.CreateArtifactSourceRefsParams, error) {
	projectGeneration, ok := lifecycleInt64(fields.ProjectGeneration)
	if !ok {
		return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
	}
	params := sqlc.CreateArtifactSourceRefsParams{
		OwnerID: ownerID, ProjectID: projectID, RefKind: string(fields.RefKind),
		ScopeKind:  pgtype.Text{String: fields.ScopeKind, Valid: fields.ScopeKind != ""},
		SourceView: artifactSourceViewValue(fields.SourceView), ProjectGeneration: projectGeneration,
		SourceIndependent: fields.SourceIndependent,
	}
	var err error
	if params.BookID, err = optionalArtifactUUID(fields.BookID); err != nil {
		return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
	}
	if params.BookGeneration, err = optionalLifecycleInt8(fields.BookID != "", fields.BookGeneration); err != nil {
		return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
	}
	if params.ChapterID, err = optionalArtifactUUID(fields.ChapterID); err != nil {
		return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
	}
	if params.ChapterGeneration, err = optionalLifecycleInt8(fields.ChapterID != "", fields.ChapterGeneration); err != nil {
		return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
	}
	if fields.RefKind == governance.ArtifactSourceRefChapter {
		if fields.SourceRevision == 0 || fields.SourceRevision > math.MaxInt64 ||
			fields.TextIdentityState != governance.SourceIdentityTextDerivedCurrent ||
			fields.LocatorSourceGenerationDigest == ([32]byte{}) || fields.ContentHash == ([32]byte{}) {
			return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
		}
		params.SourceRevision = pgtype.Int8{Int64: int64(fields.SourceRevision), Valid: true}
		generation := fields.LocatorSourceGenerationDigest
		params.LocatorSourceGeneration = generation[:]
		params.TextIdentityState = pgtype.Text{String: string(fields.TextIdentityState), Valid: true}
		contentHash := fields.ContentHash
		params.ContentHash = contentHash[:]
	} else if fields.RefKind != governance.ArtifactSourceRefScope ||
		fields.SourceRevision != 0 || fields.TextIdentityState != "" ||
		fields.LocatorSourceGenerationDigest != ([32]byte{}) || fields.ContentHash != ([32]byte{}) {
		return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
	}
	if fields.ChatID != "" {
		if params.ChatSessionID, err = optionalArtifactUUID(fields.ChatID); err != nil {
			return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
		}
		epoch, ok := lifecycleInt64(governance.LifecycleGeneration(fields.ChatLifecycleEpoch))
		if !ok {
			return sqlc.CreateArtifactSourceRefsParams{}, ErrArtifactSourcesValidation
		}
		params.ChatLifecycleEpoch = pgtype.Int8{Int64: epoch, Valid: true}
	}
	return params, nil
}

func (s *Store) createRefs(
	ctx context.Context,
	_ pgx.Tx,
	queries *sqlc.Queries,
	prepared preparedArtifactSources,
) error {
	var firstErr error
	queries.CreateArtifactSourceRefs(ctx, prepared.refs).Exec(func(index int, err error) {
		if firstErr != nil {
			return
		}
		if err != nil {
			firstErr = mapArtifactSourcesWriteError(err)
			return
		}
		firstErr = s.hit(fmt.Sprintf("after_ref_%d", index))
	})
	return firstErr
}

func (s *Store) seal(
	ctx context.Context,
	queries *sqlc.Queries,
	prepared preparedArtifactSources,
) error {
	if err := s.hit("before_seal"); err != nil {
		return err
	}
	_, err := queries.SealArtifactSourceOwner(ctx, sqlc.SealArtifactSourceOwnerParams{
		OwnerID: prepared.ownerParams.ID, ProjectID: prepared.ownerParams.ProjectID,
		ArtifactKind: prepared.ownerParams.ArtifactKind, ArtifactID: prepared.ownerParams.ArtifactID,
		SourceView: prepared.ownerParams.SourceView, ManifestVersion: prepared.ownerParams.ManifestVersion,
		ManifestHash: prepared.ownerParams.ManifestHash, RefCount: prepared.ownerParams.RefCount,
		SourceIdentityState: prepared.ownerParams.SourceIdentityState,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArtifactSourcesConflict
	}
	if err != nil {
		return err
	}
	return s.hit("after_seal")
}

func (s *Store) hit(point string) error {
	if s == nil || s.failpoint == nil {
		return nil
	}
	return s.failpoint(point)
}

func artifactSourceIdentityValues(ownerID, projectID, artifactID string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	ownerUUID, ownerErr := storedb.ParseUUID(ownerID)
	projectUUID, projectErr := storedb.ParseUUID(projectID)
	artifactUUID, artifactErr := storedb.ParseUUID(artifactID)
	if ownerErr != nil || projectErr != nil || artifactErr != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, ErrArtifactSourcesValidation
	}
	return ownerUUID, projectUUID, artifactUUID, nil
}

func artifactSourceViewValue(view governance.SourceViewArm) pgtype.Text {
	value, present := view.View()
	return pgtype.Text{String: string(value), Valid: present}
}

func optionalArtifactUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return storedb.ParseUUID(value)
}

func optionalLifecycleInt8(present bool, value governance.LifecycleGeneration) (pgtype.Int8, error) {
	if !present {
		return pgtype.Int8{}, nil
	}
	converted, ok := lifecycleInt64(value)
	if !ok {
		return pgtype.Int8{}, ErrArtifactSourcesValidation
	}
	return pgtype.Int8{Int64: converted, Valid: true}, nil
}

func lifecycleInt64(value governance.LifecycleGeneration) (int64, bool) {
	if value == 0 || uint64(value) > math.MaxInt64 {
		return 0, false
	}
	return int64(value), true
}

func sameArtifactManifest(left, right governance.ArtifactManifest) bool {
	return left.Version() == right.Version() && left.RefCount() == right.RefCount() &&
		left.Hash() == right.Hash() && left.SourceIdentityState() == right.SourceIdentityState()
}

func validArtifactManifestValue(manifest governance.ArtifactManifest) bool {
	hash := manifest.Hash()
	if manifest.RefCount() == 0 || manifest.RefCount() > math.MaxInt32 || hash == ([32]byte{}) {
		return false
	}
	switch manifest.Version() {
	case 1:
		return manifest.SourceIdentityState() == governance.SourceIdentityScopeOnly
	case 2:
		return manifest.SourceIdentityState() == governance.SourceIdentityTextDerivedCurrent
	default:
		return false
	}
}

func knownArtifactKind(kind governance.ArtifactKind) bool {
	for _, candidate := range governance.ArtifactKinds() {
		if kind == candidate {
			return true
		}
	}
	return false
}

func mapArtifactSourcesWriteError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("artifact sources write: %w", err)
}
