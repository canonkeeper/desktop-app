package jobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func lockModelStartJobWithManifestVersion(
	ctx context.Context,
	queries *sqlc.Queries,
	input jobs.CreateModelJobInput,
	initiatingSessionID pgtype.UUID,
	expectedManifest jobs.JobSourceManifest,
	manifestVersion uint32,
	preview sqlc.Job,
	requireExpectedManifest bool,
) (jobs.Job, jobs.ModelBinding, error) {
	if !preview.ArtifactSourceOwnerID.Valid || preview.GovernancePayloadState != "active" ||
		preview.ProjectID.String() != input.ProjectID {
		return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
	}
	owner, err := queries.LockModelClaimJobOwner(ctx, sqlc.LockModelClaimJobOwnerParams{
		ProjectID: preview.ProjectID, OwnerID: preview.ArtifactSourceOwnerID, JobID: preview.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, err
	}
	if owner.ID != preview.ArtifactSourceOwnerID || !modelSourceViewMatches(owner.SourceView, expectedManifest.SourceView) ||
		(owner.ManifestVersion != 1 && owner.ManifestVersion != 2) || owner.RefCount < 1 || len(owner.ManifestHash) != sha256.Size {
		return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
	}
	if requireExpectedManifest {
		expected, err := newModelArtifactManifest(manifestVersion, governance.ArtifactManifestInput{
			Kind: governance.ArtifactJob, ProjectID: input.ProjectID, ArtifactID: preview.ID.String(),
			OwnerSourceView: expectedManifest.SourceView, Refs: expectedManifest.Refs,
		})
		if err != nil {
			return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
		}
		expectedHash := expected.Hash()
		if owner.ManifestVersion != int32(expected.Version()) || owner.RefCount != int32(expected.RefCount()) ||
			!bytes.Equal(owner.ManifestHash, expectedHash[:]) {
			return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
		}
	}
	locked, err := queries.LockJobForStatusUpdate(ctx, sqlc.LockJobForStatusUpdateParams{
		ProjectID: preview.ProjectID, JobID: preview.ID, ClaimIncarnation: preview.ClaimIncarnation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, err
	}
	if locked.ArtifactSourceOwnerID != preview.ArtifactSourceOwnerID || locked.GovernancePayloadState != "active" ||
		locked.Kind != string(input.Subject.Kind()) {
		return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
	}
	job, err := JobFromSQLJob(locked)
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, fmt.Errorf("stage=job_decode: %w", jobs.ErrConflict)
	}
	if !sameModelStartSubject(job, input.Subject) {
		return jobs.Job{}, jobs.ModelBinding{}, fmt.Errorf("stage=job_subject: %w", jobs.ErrConflict)
	}
	if terminalJobStatus(job.Status) {
		return job, jobs.ModelBinding{}, nil
	}
	if job.Status != jobs.StatusQueued && job.Status != jobs.StatusRunning && job.Status != jobs.StatusCanceling {
		return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
	}
	lockedBinding, err := queries.LockJobModelBinding(ctx, sqlc.LockJobModelBindingParams{
		ProjectID: preview.ProjectID, JobID: preview.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, err
	}
	binding := modelBindingFromSQL(lockedBinding)
	if lockedBinding.ArtifactSourceOwnerID != preview.ArtifactSourceOwnerID ||
		!modelStartBindingMatches(binding, input, initiatingSessionID) {
		return jobs.Job{}, jobs.ModelBinding{}, jobs.ErrConflict
	}
	job.WorkerClaim = jobs.WorkerClaim{ClaimIncarnation: locked.ClaimIncarnation}
	return job, binding, nil
}

func modelStartBindingMatches(
	binding jobs.ModelBinding,
	input jobs.CreateModelJobInput,
	initiatingSessionID pgtype.UUID,
) bool {
	return binding.Validate() == nil && binding.ProjectID == input.ProjectID && binding.Kind == input.Subject.Kind() &&
		binding.InputContractVersion == input.InputContractVersion && binding.Origin == input.Origin &&
		binding.ActorUserID == input.ActorUserID && initiatingSessionID.Valid &&
		binding.InitiatingAuthSessionID == initiatingSessionID.String()
}

func modelStartBinding(
	input jobs.CreateModelJobInput,
	jobID string,
	initiatingSessionID pgtype.UUID,
) (jobs.ModelBinding, error) {
	binding := jobs.ModelBinding{
		JobID: jobID, ProjectID: input.ProjectID, Kind: input.Subject.Kind(), Origin: input.Origin,
		InputContractVersion: input.InputContractVersion,
	}
	if input.Origin == jobs.ModelOriginUser {
		if !initiatingSessionID.Valid {
			return jobs.ModelBinding{}, jobs.ErrValidation
		}
		binding.ActorUserID = input.ActorUserID
		binding.InitiatingAuthSessionID = initiatingSessionID.String()
	}
	if binding.Validate() != nil {
		return jobs.ModelBinding{}, jobs.ErrValidation
	}
	return binding, nil
}

func sameModelStartSubject(job jobs.Job, expected jobs.Subject) bool {
	if job.Subject == nil || expected == nil || job.Kind != expected.Kind() {
		return false
	}
	stored, err := jobs.EncodeSubject(job.Kind, job.Subject)
	if err != nil {
		return false
	}
	want, err := jobs.EncodeSubject(expected.Kind(), expected)
	return err == nil && bytes.Equal(stored, want)
}

func sameModelSourceManifest(left, right jobs.JobSourceManifest) bool {
	leftView, leftPresent := left.SourceView.View()
	rightView, rightPresent := right.SourceView.View()
	if leftPresent != rightPresent || leftPresent && leftView != rightView || len(left.Refs) != len(right.Refs) || len(left.Refs) == 0 {
		return false
	}
	keys := make(map[string]struct{}, len(left.Refs))
	for _, ref := range left.Refs {
		projection, err := governance.ArtifactSourceRefProjection(ref)
		if err != nil {
			return false
		}
		key := string(projection)
		if _, duplicate := keys[key]; duplicate {
			return false
		}
		keys[key] = struct{}{}
	}
	seen := make(map[string]struct{}, len(right.Refs))
	for _, ref := range right.Refs {
		projection, err := governance.ArtifactSourceRefProjection(ref)
		if err != nil {
			return false
		}
		key := string(projection)
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
		if _, ok := keys[key]; !ok {
			return false
		}
	}
	return true
}

type modelPrivateInputRefRow struct {
	refKind                       string
	scopeKind                     pgtype.Text
	sourceView                    pgtype.Text
	projectGeneration             int64
	bookID                        pgtype.UUID
	bookGeneration                pgtype.Int8
	chapterID                     pgtype.UUID
	chapterGeneration             pgtype.Int8
	sourceRevision                pgtype.Int8
	locatorSourceGenerationDigest []byte
	textIdentityState             pgtype.Text
	contentHash                   []byte
	sourceIndependent             bool
	chatID                        pgtype.UUID
	chatLifecycleEpoch            pgtype.Int8
}

type modelPrivateInputOwner struct {
	artifactKind governance.ArtifactKind
	artifactID   string
	source       jobs.JobSourceManifest
	manifest     governance.ArtifactManifest
}

func readModelPrivateInputOwner(
	ctx context.Context,
	tx pgx.Tx,
	projectID, ownerID pgtype.UUID,
) (modelPrivateInputOwner, error) {
	return readModelPrivateInputOwnerWithLifecycle(ctx, tx, projectID, ownerID, false, false)
}

func readModelPrivateInputOwnerWithLifecycle(
	ctx context.Context,
	tx pgx.Tx,
	projectID, ownerID pgtype.UUID,
	lifecycleReadable bool,
	sourceDeleted bool,
) (modelPrivateInputOwner, error) {
	var artifactKind string
	var artifactID pgtype.UUID
	var sourceView pgtype.Text
	var manifestVersion pgtype.Int4
	var manifestHash []byte
	var refCount int32
	err := tx.QueryRow(ctx, `
		SELECT owner.artifact_kind, owner.artifact_id, owner.source_view,
		       owner.manifest_version, owner.manifest_hash, owner.ref_count
		FROM artifact_source_owners AS owner
		WHERE owner.project_id = $1
		  AND owner.id = $2
		  AND owner.artifact_kind <> 'job'
		  AND owner.read_state = CASE WHEN $4 THEN 'source_deleted' ELSE 'active' END
		  AND CASE WHEN $4 THEN true WHEN $3
		    THEN ck_artifact_source_owner_is_lifecycle_readable(
		      owner.id, owner.project_id, owner.artifact_kind, owner.artifact_id
		    )
		    ELSE ck_artifact_source_owner_is_readable(
		      owner.id, owner.project_id, owner.artifact_kind, owner.artifact_id
		    )
		  END
		FOR UPDATE
	`, projectID, ownerID, lifecycleReadable, sourceDeleted).Scan(
		&artifactKind, &artifactID, &sourceView, &manifestVersion, &manifestHash, &refCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return modelPrivateInputOwner{}, jobs.ErrConflict
	}
	if err != nil {
		return modelPrivateInputOwner{}, err
	}
	if !artifactID.Valid || !manifestVersion.Valid ||
		(manifestVersion.Int32 != 1 && manifestVersion.Int32 != 2) || refCount < 1 {
		return modelPrivateInputOwner{}, jobs.ErrConflict
	}

	rows, err := tx.Query(ctx, `
		SELECT ref.ref_kind, ref.scope_kind, ref.source_view, ref.project_generation,
		       ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
		       ref.source_revision, ref.locator_source_generation, ref.text_identity_state,
		       ref.content_hash, ref.source_independent,
		       ref.chat_session_id, ref.chat_lifecycle_epoch
		FROM artifact_source_refs AS ref
		WHERE ref.project_id = $1 AND ref.owner_id = $2
		ORDER BY ref.id
		FOR SHARE
	`, projectID, ownerID)
	if err != nil {
		return modelPrivateInputOwner{}, err
	}
	defer rows.Close()
	refs := make([]governance.ArtifactSourceRef, 0, refCount)
	for rows.Next() {
		var row modelPrivateInputRefRow
		if err := rows.Scan(
			&row.refKind, &row.scopeKind, &row.sourceView, &row.projectGeneration,
			&row.bookID, &row.bookGeneration, &row.chapterID, &row.chapterGeneration,
			&row.sourceRevision, &row.locatorSourceGenerationDigest, &row.textIdentityState,
			&row.contentHash, &row.sourceIndependent,
			&row.chatID, &row.chatLifecycleEpoch,
		); err != nil {
			return modelPrivateInputOwner{}, err
		}
		ref, err := modelPrivateInputRef(projectID.String(), row)
		if err != nil {
			return modelPrivateInputOwner{}, jobs.ErrConflict
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return modelPrivateInputOwner{}, err
	}
	storedView, err := modelSourceViewArm(sourceView)
	if err != nil {
		return modelPrivateInputOwner{}, jobs.ErrConflict
	}
	stored := jobs.JobSourceManifest{SourceView: storedView, Refs: refs}
	if int32(len(refs)) != refCount {
		return modelPrivateInputOwner{}, jobs.ErrConflict
	}
	manifestInput := governance.ArtifactManifestInput{
		Kind: governance.ArtifactKind(artifactKind), ProjectID: projectID.String(), ArtifactID: artifactID.String(),
		OwnerSourceView: storedView, Refs: refs,
	}
	manifest, err := governance.NewArtifactManifest(manifestInput)
	if manifestVersion.Int32 == 2 {
		manifest, err = governance.NewArtifactManifestV2(manifestInput)
	}
	if err != nil {
		return modelPrivateInputOwner{}, jobs.ErrConflict
	}
	digest := manifest.Hash()
	if manifestVersion.Int32 != int32(manifest.Version()) || refCount != int32(manifest.RefCount()) ||
		!bytes.Equal(manifestHash, digest[:]) {
		return modelPrivateInputOwner{}, jobs.ErrConflict
	}
	return modelPrivateInputOwner{
		artifactKind: governance.ArtifactKind(artifactKind), artifactID: artifactID.String(),
		source: stored, manifest: manifest,
	}, nil
}

func modelPrivateInputRef(projectID string, row modelPrivateInputRefRow) (governance.ArtifactSourceRef, error) {
	if row.projectGeneration < 1 {
		return governance.ArtifactSourceRef{}, jobs.ErrConflict
	}
	view, err := modelSourceViewArm(row.sourceView)
	if err != nil {
		return governance.ArtifactSourceRef{}, err
	}
	chat := governance.AbsentChatArm()
	if row.chatID.Valid || row.chatLifecycleEpoch.Valid {
		if !row.chatID.Valid || !row.chatLifecycleEpoch.Valid || row.chatLifecycleEpoch.Int64 < 1 {
			return governance.ArtifactSourceRef{}, jobs.ErrConflict
		}
		chat, err = governance.PresentChatArm(
			projectID,
			row.chatID.String(),
			governance.ChatLifecycleEpoch(row.chatLifecycleEpoch.Int64),
		)
		if err != nil {
			return governance.ArtifactSourceRef{}, err
		}
	}
	vector := governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: governance.LifecycleGeneration(row.projectGeneration),
	}
	if row.bookID.Valid || row.bookGeneration.Valid {
		if !row.bookID.Valid || !row.bookGeneration.Valid || row.bookGeneration.Int64 < 1 {
			return governance.ArtifactSourceRef{}, jobs.ErrConflict
		}
		vector.BookID = row.bookID.String()
		vector.BookGeneration = governance.LifecycleGeneration(row.bookGeneration.Int64)
	}
	if row.chapterID.Valid || row.chapterGeneration.Valid {
		if !row.chapterID.Valid || !row.chapterGeneration.Valid || row.chapterGeneration.Int64 < 1 {
			return governance.ArtifactSourceRef{}, jobs.ErrConflict
		}
		vector.ChapterID = row.chapterID.String()
		vector.ChapterGeneration = governance.LifecycleGeneration(row.chapterGeneration.Int64)
	}

	switch row.refKind {
	case string(governance.ArtifactSourceRefChapter):
		if row.scopeKind.Valid || row.sourceIndependent ||
			!row.sourceRevision.Valid || row.sourceRevision.Int64 < 1 ||
			len(row.locatorSourceGenerationDigest) != 32 || len(row.contentHash) != 32 ||
			!row.textIdentityState.Valid || row.textIdentityState.String != string(governance.SourceIdentityTextDerivedCurrent) {
			return governance.ArtifactSourceRef{}, jobs.ErrConflict
		}
		var generation, contentHash governance.Digest
		copy(generation[:], row.locatorSourceGenerationDigest)
		copy(contentHash[:], row.contentHash)
		return governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
			Source: vector, SourceRevision: uint64(row.sourceRevision.Int64), SourceView: view,
			SourceGenerationDigest: generation, ContentHash: contentHash, Chat: chat,
		})
	case string(governance.ArtifactSourceRefScope):
		if !row.scopeKind.Valid || row.textIdentityState.Valid || row.sourceRevision.Valid ||
			len(row.locatorSourceGenerationDigest) != 0 || len(row.contentHash) != 0 {
			return governance.ArtifactSourceRef{}, jobs.ErrConflict
		}
		switch row.scopeKind.String {
		case "project":
			if row.sourceIndependent {
				return governance.NewSourceIndependentProjectRef(vector, chat)
			}
			return governance.NewViewBoundProjectRef(vector, view, chat)
		case "book":
			if row.sourceIndependent {
				return governance.ArtifactSourceRef{}, jobs.ErrConflict
			}
			return governance.NewBookArtifactRef(vector, view, chat)
		case "chapter":
			if row.sourceIndependent {
				return governance.ArtifactSourceRef{}, jobs.ErrConflict
			}
			return governance.NewChapterArtifactRef(vector, view, chat)
		}
	}
	return governance.ArtifactSourceRef{}, jobs.ErrConflict
}

func modelSourceViewArm(value pgtype.Text) (governance.SourceViewArm, error) {
	if !value.Valid {
		return governance.AbsentSourceView(), nil
	}
	return governance.PresentSourceView(governance.SourceView(value.String))
}

func modelStartReplayAuthority(input jobs.CreateModelJobInput, actorID, initiatingSessionID pgtype.UUID) []byte {
	actorUserID := ""
	if actorID.Valid {
		actorUserID = actorID.String()
	}
	sessionID := ""
	if initiatingSessionID.Valid {
		sessionID = initiatingSessionID.String()
	}
	payload := []byte("canonkeeper:model-start-replay-authority:v1\x00" + string(input.Origin) + "\x00" +
		actorUserID + "\x00" + sessionID + "\x00")
	payload = binary.BigEndian.AppendUint32(payload, input.InputContractVersion)
	digest := sha256.Sum256(payload)
	return digest[:]
}
