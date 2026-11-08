package canonstore

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type LoadArtifactSourceTransferInput struct {
	OwnerID    string
	ProjectID  string
	ArtifactID string
	Kind       governance.ArtifactKind
	SourceView governance.SourceViewArm
}

type ArtifactSourceTransfer struct {
	Refs           []governance.ArtifactSourceRef
	OwnerManifest  governance.ArtifactManifest
	ContentAllowed bool
}

type preparedArtifactSourceTransfer struct {
	ownerParams sqlc.CreateArtifactSourceOwnerParams
	refs        []sqlc.CreateArtifactSourceRefsParams
}

type artifactSourceTransferBookLock struct {
	id         pgtype.UUID
	generation int64
}

type artifactSourceTransferChapterLock struct {
	id         pgtype.UUID
	bookID     pgtype.UUID
	generation int64
}

type artifactSourceTransferChatLock struct {
	id    pgtype.UUID
	epoch int64
}

func (db *Store) LoadArtifactSourceTransferInTx(
	ctx context.Context,
	tx pgx.Tx,
	input LoadArtifactSourceTransferInput,
) (ArtifactSourceTransfer, error) {
	if tx == nil || input.SourceView.Validate() != nil || !knownArtifactSourceKind(input.Kind) {
		return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
	}
	ownerID, projectID, artifactID, err := artifactSourceTransferIdentityValues(
		input.OwnerID, input.ProjectID, input.ArtifactID,
	)
	if err != nil {
		return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
	}
	queries := sqlc.New(tx)
	owner, err := queries.ReadArtifactSourceTransferOwner(ctx, sqlc.ReadArtifactSourceTransferOwnerParams{
		OwnerID: ownerID, ProjectID: projectID, ArtifactKind: string(input.Kind), ArtifactID: artifactID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ArtifactSourceTransfer{}, nil
	}
	if err != nil {
		return ArtifactSourceTransfer{}, err
	}
	if !owner.ProjectMatches || !owner.KindMatches || !owner.ArtifactMatches ||
		owner.SourceView != artifactSourceTransferViewValue(input.SourceView) {
		return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
	}
	if owner.ReadState != "active" || owner.FencedAt.Valid {
		return ArtifactSourceTransfer{}, nil
	}
	if (owner.ManifestVersion != 1 && owner.ManifestVersion != 2) ||
		len(owner.ManifestHash) != 32 || owner.RefCount < 1 || owner.SourceIdentityState == "" {
		return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
	}
	rows, err := queries.ListArtifactSourceTransferRefs(ctx, sqlc.ListArtifactSourceTransferRefsParams{
		OwnerID: ownerID, ProjectID: projectID,
	})
	if err != nil {
		return ArtifactSourceTransfer{}, err
	}
	if len(rows) != int(owner.RefCount) {
		return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
	}
	refs := make([]governance.ArtifactSourceRef, len(rows))
	for index, row := range rows {
		if !row.ID.Valid || row.OwnerID != ownerID || row.ProjectID != projectID {
			return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
		}
		refs[index], err = artifactSourceTransferStoredRef(row)
		if err != nil {
			return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
		}
	}
	ownerManifest, matches := artifactSourceTransferManifest(input, owner, refs)
	if !matches {
		return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
	}
	transfer, err := db.NormalizeArtifactSourceTransferInTx(ctx, tx, input.ProjectID, input.SourceView, refs)
	if err != nil || !transfer.ContentAllowed {
		return transfer, err
	}
	_, err = queries.LockArtifactSourceTransferOwner(ctx, sqlc.LockArtifactSourceTransferOwnerParams{
		OwnerID: owner.ID, ProjectID: owner.ProjectID, ArtifactKind: owner.ArtifactKind,
		ArtifactID: owner.ArtifactID, SourceView: owner.SourceView,
		ManifestVersion: owner.ManifestVersion, ManifestHash: owner.ManifestHash,
		RefCount: owner.RefCount, SourceIdentityState: owner.SourceIdentityState,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
	}
	if err != nil {
		return ArtifactSourceTransfer{}, err
	}
	transfer.OwnerManifest = ownerManifest
	return transfer, nil
}

func (db *Store) NormalizeArtifactSourceTransferInTx(
	ctx context.Context,
	tx pgx.Tx,
	projectID string,
	sourceView governance.SourceViewArm,
	refs []governance.ArtifactSourceRef,
) (ArtifactSourceTransfer, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if tx == nil || err != nil || sourceView.Validate() != nil || len(refs) == 0 {
		return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
	}
	type projectedRef struct {
		ref        governance.ArtifactSourceRef
		fields     governance.ArtifactSourceRefFields
		projection []byte
	}
	unique := make(map[string]projectedRef, len(refs))
	for _, ref := range refs {
		fields, fieldsErr := ref.Fields()
		if fieldsErr != nil || fields.ProjectID != projectID || fields.SourceView != sourceView {
			return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
		}
		projection, projectionErr := governance.ArtifactSourceRefProjection(ref)
		if projectionErr != nil {
			return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
		}
		unique[string(projection)] = projectedRef{ref: ref, fields: fields, projection: projection}
	}
	ordered := make([]projectedRef, 0, len(unique))
	for _, item := range unique {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(left, right int) bool {
		return bytes.Compare(ordered[left].projection, ordered[right].projection) < 0
	})
	originalRefs := make([]governance.ArtifactSourceRef, len(ordered))
	for index := range ordered {
		originalRefs[index] = ordered[index].ref
	}
	prepared, err := prepareArtifactSourceTransferRefs(projectUUID, originalRefs)
	if err != nil {
		return ArtifactSourceTransfer{}, err
	}
	allowed, err := artifactSourceTransferLifecyclesCurrent(ctx, tx, prepared)
	if err != nil || !allowed {
		return ArtifactSourceTransfer{ContentAllowed: false}, err
	}

	normalized := make(map[string]projectedRef, len(ordered))
	for _, item := range ordered {
		ref := item.ref
		switch item.fields.RefKind {
		case governance.ArtifactSourceRefChapter:
			view, present := item.fields.SourceView.View()
			if !present {
				return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
			}
			resolved, resolveErr := db.ResolveCanonicalChapterSourceInTx(
				ctx, tx, item.fields.ProjectID, view, item.fields.BookID, item.fields.ChapterID,
			)
			switch {
			case resolveErr == nil:
				ref, err = artifactSourceTransferNormalizeResolvedChapterRef(
					resolved.ExactRef, item.fields, item.projection,
				)
				if err != nil {
					return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
				}
			case errors.Is(resolveErr, ErrCanonicalSourceNotCurrent):
				ref, err = artifactSourceTransferChapterScope(item.fields)
				if err != nil {
					return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
				}
			case errors.Is(resolveErr, ErrCanonicalSourceValidation):
				return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
			default:
				return ArtifactSourceTransfer{}, resolveErr
			}
		}
		projection, projectionErr := governance.ArtifactSourceRefProjection(ref)
		if projectionErr != nil {
			return ArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesConflict
		}
		normalized[string(projection)] = projectedRef{ref: ref, projection: projection}
	}
	ordered = ordered[:0]
	for _, item := range normalized {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(left, right int) bool {
		return bytes.Compare(ordered[left].projection, ordered[right].projection) < 0
	})
	result := make([]governance.ArtifactSourceRef, len(ordered))
	for index := range ordered {
		result[index] = ordered[index].ref
	}
	if err := db.artifactSources.PrelockArtifactSourceRefs(ctx, tx, projectID, result); err != nil {
		return ArtifactSourceTransfer{}, err
	}
	return ArtifactSourceTransfer{Refs: result, ContentAllowed: true}, nil
}

func artifactSourceTransferManifest(
	input LoadArtifactSourceTransferInput,
	owner sqlc.ReadArtifactSourceTransferOwnerRow,
	refs []governance.ArtifactSourceRef,
) (governance.ArtifactManifest, bool) {
	manifestInput := governance.ArtifactManifestInput{
		Kind: input.Kind, ProjectID: input.ProjectID, ArtifactID: input.ArtifactID,
		OwnerSourceView: input.SourceView, Refs: refs,
	}
	var manifest governance.ArtifactManifest
	var err error
	switch owner.ManifestVersion {
	case 1:
		manifest, err = governance.NewArtifactManifest(manifestInput)
	case 2:
		manifest, err = governance.NewArtifactManifestV2(manifestInput)
	default:
		return governance.ArtifactManifest{}, false
	}
	if err != nil || manifest.RefCount() != uint32(owner.RefCount) ||
		owner.SourceIdentityState != string(manifest.SourceIdentityState()) {
		return governance.ArtifactManifest{}, false
	}
	hash := manifest.Hash()
	if bytes.Equal(hash[:], owner.ManifestHash) {
		return manifest, true
	}
	return governance.ArtifactManifest{}, false
}

func artifactSourceTransferStoredRef(row sqlc.ArtifactSourceRef) (governance.ArtifactSourceRef, error) {
	projectID := storedb.UUIDString(row.ProjectID)
	if projectID == "" || row.ProjectGeneration < 1 ||
		row.BookID.Valid != row.BookGeneration.Valid ||
		row.ChapterID.Valid != row.ChapterGeneration.Valid ||
		(row.ChapterID.Valid && !row.BookID.Valid) {
		return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
	}
	vector := governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: governance.LifecycleGeneration(row.ProjectGeneration),
	}
	if row.BookID.Valid {
		vector.BookID = storedb.UUIDString(row.BookID)
		if vector.BookID == "" || row.BookGeneration.Int64 < 1 {
			return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
		}
		vector.BookGeneration = governance.LifecycleGeneration(row.BookGeneration.Int64)
	}
	if row.ChapterID.Valid {
		vector.ChapterID = storedb.UUIDString(row.ChapterID)
		if vector.ChapterID == "" || row.ChapterGeneration.Int64 < 1 {
			return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
		}
		vector.ChapterGeneration = governance.LifecycleGeneration(row.ChapterGeneration.Int64)
	}
	view, err := artifactSourceTransferView(row.SourceView)
	if err != nil || row.ChatSessionID.Valid != row.ChatLifecycleEpoch.Valid {
		return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
	}
	chat := governance.AbsentChatArm()
	if row.ChatSessionID.Valid {
		chatID := storedb.UUIDString(row.ChatSessionID)
		if chatID == "" || row.ChatLifecycleEpoch.Int64 < 1 {
			return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
		}
		chat, err = governance.PresentChatArm(
			projectID, chatID, governance.ChatLifecycleEpoch(row.ChatLifecycleEpoch.Int64),
		)
		if err != nil {
			return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
		}
	}

	switch governance.ArtifactSourceRefKind(row.RefKind) {
	case governance.ArtifactSourceRefChapter:
		if row.ScopeKind.Valid || !row.SourceView.Valid || !row.BookID.Valid || !row.ChapterID.Valid ||
			!row.SourceRevision.Valid || row.SourceRevision.Int64 < 1 ||
			len(row.LocatorSourceGeneration) != 32 || len(row.ContentHash) != 32 ||
			!row.TextIdentityState.Valid || row.TextIdentityState.String != string(governance.SourceIdentityTextDerivedCurrent) ||
			row.SourceIndependent {
			return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
		}
		var generation, contentHash governance.Digest
		copy(generation[:], row.LocatorSourceGeneration)
		copy(contentHash[:], row.ContentHash)
		return governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
			Source: vector, SourceRevision: uint64(row.SourceRevision.Int64), SourceView: view,
			SourceGenerationDigest: generation, ContentHash: contentHash, Chat: chat,
		})
	case governance.ArtifactSourceRefScope:
		if !row.ScopeKind.Valid || row.TextIdentityState.Valid || row.SourceRevision.Valid ||
			len(row.LocatorSourceGeneration) != 0 || len(row.ContentHash) != 0 {
			return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
		}
		switch row.ScopeKind.String {
		case "project":
			if row.BookID.Valid || row.ChapterID.Valid {
				return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
			}
			if row.SourceIndependent {
				return governance.NewSourceIndependentProjectRef(vector, chat)
			}
			return governance.NewViewBoundProjectRef(vector, view, chat)
		case "book":
			if !row.BookID.Valid || row.ChapterID.Valid || row.SourceIndependent {
				return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
			}
			return governance.NewBookArtifactRef(vector, view, chat)
		case "chapter":
			if !row.BookID.Valid || !row.ChapterID.Valid || row.SourceIndependent {
				return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
			}
			return governance.NewChapterArtifactRef(vector, view, chat)
		}
	}
	return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
}

func artifactSourceTransferView(value pgtype.Text) (governance.SourceViewArm, error) {
	if !value.Valid {
		return governance.AbsentSourceView(), nil
	}
	return governance.PresentSourceView(governance.SourceView(value.String))
}

func prepareArtifactSourceTransferRefs(
	projectID pgtype.UUID,
	refs []governance.ArtifactSourceRef,
) (preparedArtifactSourceTransfer, error) {
	if !projectID.Valid || len(refs) == 0 {
		return preparedArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
	}
	prepared := preparedArtifactSourceTransfer{
		ownerParams: sqlc.CreateArtifactSourceOwnerParams{ProjectID: projectID},
		refs:        make([]sqlc.CreateArtifactSourceRefsParams, 0, len(refs)),
	}
	for _, ref := range refs {
		fields, err := ref.Fields()
		if err != nil || fields.ProjectID != projectID.String() {
			return preparedArtifactSourceTransfer{}, governancestore.ErrArtifactSourcesValidation
		}
		params, err := governancestore.ArtifactSourceRefParams(pgtype.UUID{}, projectID, fields)
		if err != nil {
			return preparedArtifactSourceTransfer{}, err
		}
		prepared.refs = append(prepared.refs, params)
	}
	return prepared, nil
}

func artifactSourceTransferLifecyclesCurrent(
	ctx context.Context,
	tx pgx.Tx,
	prepared preparedArtifactSourceTransfer,
) (bool, error) {
	if tx == nil || len(prepared.refs) == 0 {
		return false, governancestore.ErrArtifactSourcesValidation
	}
	projectGeneration := prepared.refs[0].ProjectGeneration
	books := make(map[string]artifactSourceTransferBookLock)
	chapters := make(map[string]artifactSourceTransferChapterLock)
	chats := make(map[string]artifactSourceTransferChatLock)
	for _, ref := range prepared.refs {
		if ref.ProjectGeneration != projectGeneration {
			return false, governancestore.ErrArtifactSourcesConflict
		}
		if ref.BookID.Valid {
			book := artifactSourceTransferBookLock{id: ref.BookID, generation: ref.BookGeneration.Int64}
			key := ref.BookID.String()
			if current, exists := books[key]; exists && current != book {
				return false, governancestore.ErrArtifactSourcesConflict
			}
			books[key] = book
		}
		if ref.ChapterID.Valid {
			chapter := artifactSourceTransferChapterLock{
				id: ref.ChapterID, bookID: ref.BookID, generation: ref.ChapterGeneration.Int64,
			}
			key := ref.ChapterID.String()
			if current, exists := chapters[key]; exists && current != chapter {
				return false, governancestore.ErrArtifactSourcesConflict
			}
			chapters[key] = chapter
		}
		if ref.ChatSessionID.Valid {
			chat := artifactSourceTransferChatLock{id: ref.ChatSessionID, epoch: ref.ChatLifecycleEpoch.Int64}
			key := ref.ChatSessionID.String()
			if current, exists := chats[key]; exists && current != chat {
				return false, governancestore.ErrArtifactSourcesConflict
			}
			chats[key] = chat
		}
	}

	current, live, err := artifactSourceTransferLifecycle(ctx, tx, `
		SELECT lifecycle_generation, deleted_at IS NULL
		FROM projects
		WHERE id = $1
	`, prepared.ownerParams.ProjectID)
	if err != nil || !live {
		return false, err
	}
	if current != projectGeneration {
		return false, governancestore.ErrArtifactSourcesConflict
	}
	for _, key := range sortedArtifactSourceTransferKeys(books) {
		book := books[key]
		current, live, err = artifactSourceTransferLifecycle(ctx, tx, `
			SELECT lifecycle_generation, deleted_at IS NULL
			FROM books
			WHERE id = $1 AND project_id = $2
		`, book.id, prepared.ownerParams.ProjectID)
		if err != nil || !live {
			return false, err
		}
		if current != book.generation {
			return false, governancestore.ErrArtifactSourcesConflict
		}
	}
	for _, key := range sortedArtifactSourceTransferKeys(chapters) {
		chapter := chapters[key]
		current, live, err = artifactSourceTransferLifecycle(ctx, tx, `
			SELECT lifecycle_generation, deleted_at IS NULL
			FROM chapters
			WHERE id = $1 AND book_id = $2 AND project_id = $3
		`, chapter.id, chapter.bookID, prepared.ownerParams.ProjectID)
		if err != nil || !live {
			return false, err
		}
		if current != chapter.generation {
			return false, governancestore.ErrArtifactSourcesConflict
		}
	}
	for _, key := range sortedArtifactSourceTransferKeys(chats) {
		chat := chats[key]
		current, live, err = artifactSourceTransferLifecycle(ctx, tx, `
			SELECT lifecycle_epoch, deleted_at IS NULL
			FROM chat_sessions
			WHERE id = $1 AND project_id = $2
		`, chat.id, prepared.ownerParams.ProjectID)
		if err != nil || !live {
			return false, err
		}
		if current != chat.epoch {
			return false, governancestore.ErrArtifactSourcesConflict
		}
	}
	return true, nil
}

func artifactSourceTransferLifecycle(
	ctx context.Context,
	tx pgx.Tx,
	query string,
	args ...any,
) (int64, bool, error) {
	var generation int64
	var live bool
	err := tx.QueryRow(ctx, query, args...).Scan(&generation, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return generation, live, err
}

func artifactSourceTransferResolvedExactChapterRef(
	resolved governance.ArtifactSourceRef,
	original governance.ArtifactSourceRefFields,
) (governance.ArtifactSourceRef, error) {
	fields, err := resolved.Fields()
	if err != nil || fields.RefKind != governance.ArtifactSourceRefChapter {
		return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
	}
	chat, err := artifactSourceTransferChat(original)
	if err != nil {
		return governance.ArtifactSourceRef{}, err
	}
	return governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
		Source: governance.SourceGenerationVector{
			ProjectID: fields.ProjectID, ProjectGeneration: fields.ProjectGeneration,
			BookID: fields.BookID, BookGeneration: fields.BookGeneration,
			ChapterID: fields.ChapterID, ChapterGeneration: fields.ChapterGeneration,
		},
		SourceRevision: fields.SourceRevision, SourceView: fields.SourceView,
		SourceGenerationDigest: governance.Digest(fields.LocatorSourceGenerationDigest),
		ContentHash:            governance.Digest(fields.ContentHash), Chat: chat,
	})
}

func artifactSourceTransferNormalizeResolvedChapterRef(
	resolved governance.ArtifactSourceRef,
	original governance.ArtifactSourceRefFields,
	originalProjection []byte,
) (governance.ArtifactSourceRef, error) {
	current, err := artifactSourceTransferResolvedExactChapterRef(resolved, original)
	if err != nil {
		return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
	}
	projection, err := governance.ArtifactSourceRefProjection(current)
	if err != nil {
		return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
	}
	if bytes.Equal(originalProjection, projection) {
		return current, nil
	}
	scope, err := artifactSourceTransferChapterScope(original)
	if err != nil {
		return governance.ArtifactSourceRef{}, governancestore.ErrArtifactSourcesConflict
	}
	return scope, nil
}

func artifactSourceTransferChapterScope(
	fields governance.ArtifactSourceRefFields,
) (governance.ArtifactSourceRef, error) {
	chat, err := artifactSourceTransferChat(fields)
	if err != nil {
		return governance.ArtifactSourceRef{}, err
	}
	return governance.NewChapterArtifactRef(governance.SourceGenerationVector{
		ProjectID: fields.ProjectID, ProjectGeneration: fields.ProjectGeneration,
		BookID: fields.BookID, BookGeneration: fields.BookGeneration,
		ChapterID: fields.ChapterID, ChapterGeneration: fields.ChapterGeneration,
	}, fields.SourceView, chat)
}

func artifactSourceTransferChat(fields governance.ArtifactSourceRefFields) (governance.ChatArm, error) {
	if fields.ChatID == "" {
		if fields.ChatLifecycleEpoch != 0 {
			return governance.ChatArm{}, governancestore.ErrArtifactSourcesConflict
		}
		return governance.AbsentChatArm(), nil
	}
	return governance.PresentChatArm(fields.ProjectID, fields.ChatID, fields.ChatLifecycleEpoch)
}

func artifactSourceTransferIdentityValues(ownerID, projectID, artifactID string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	ownerUUID, ownerErr := storedb.ParseUUID(ownerID)
	projectUUID, projectErr := storedb.ParseUUID(projectID)
	artifactUUID, artifactErr := storedb.ParseUUID(artifactID)
	if ownerErr != nil || projectErr != nil || artifactErr != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, governancestore.ErrArtifactSourcesValidation
	}
	return ownerUUID, projectUUID, artifactUUID, nil
}

func artifactSourceTransferViewValue(view governance.SourceViewArm) pgtype.Text {
	value, present := view.View()
	return pgtype.Text{String: string(value), Valid: present}
}

func knownArtifactSourceKind(kind governance.ArtifactKind) bool {
	for _, candidate := range governance.ArtifactKinds() {
		if kind == candidate {
			return true
		}
	}
	return false
}

func sortedArtifactSourceTransferKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
