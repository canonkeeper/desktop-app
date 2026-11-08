package researchchat

import (
	"context"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/researchchat"
	canonstore "canonkeeper/backend/internal/store/canon"
	governancestore "canonkeeper/backend/internal/store/governance"

	"github.com/jackc/pgx/v5"
)

// Successful reads are dependencies of both the response and its native thread.
// Keep them in the same governed manifests used by timeline and deletion.
func (repository *Repository) retainReadSourcesInTx(
	ctx context.Context, tx pgx.Tx, call service.ToolContext,
	authority researchdomain.ReadAuthority, sources ...governance.ArtifactSourceRef,
) error {
	if len(sources) == 0 {
		return repository.confirmReadAuthorityInTx(ctx, tx, call, authority)
	}
	var jobOwnerID, turnOwnerID, turnID string
	var epoch governance.ChatLifecycleEpoch
	if err := tx.QueryRow(ctx, `
		SELECT job.artifact_source_owner_id::text, turn_row.artifact_source_owner_id::text,
		       turn_row.id::text, chat.lifecycle_epoch
		FROM jobs AS job
		JOIN chat_turns AS turn_row ON turn_row.project_id=job.project_id AND turn_row.job_id=job.id
		JOIN chat_sessions AS chat ON chat.project_id=turn_row.project_id AND chat.id=turn_row.chat_id
		WHERE job.project_id=$1::uuid AND job.id=$2::uuid AND chat.id=$3::uuid
		  AND turn_row.provider_turn_id=$4 AND chat.thread_id=$5 AND chat.deleted_at IS NULL
		FOR UPDATE OF job, turn_row, chat
	`, authority.ProjectID, authority.JobID, authority.ChatID, call.TurnID, call.Caller.RootThreadID).
		Scan(&jobOwnerID, &turnOwnerID, &turnID, &epoch); err != nil {
		return err
	}
	view, err := governance.PresentSourceView(governance.SourceView(authority.SourceView))
	if err != nil {
		return err
	}
	chat, err := governance.PresentChatArm(authority.ProjectID, authority.ChatID, epoch)
	if err != nil {
		return err
	}
	readRefs := append([]governance.ArtifactSourceRef(nil), sources...)
	if err := bindTurnRefsToChat(readRefs, chat); err != nil {
		return err
	}
	for _, owner := range []manifestOwner{
		{jobOwnerID, authority.JobID, governance.ArtifactJob, 2},
		{turnOwnerID, turnID, governance.ArtifactChatTurn, 2},
	} {
		if err := repository.addOwnerRefsInTx(ctx, tx, owner, authority.ProjectID, view, readRefs, func(ctx context.Context, tx pgx.Tx) error {
			return repository.confirmReadAuthorityInTx(ctx, tx, call, authority)
		}); err != nil {
			return err
		}
	}
	return nil
}

// manifestOwner is the source owner of one artifact and the lowest manifest version it may hold.
type manifestOwner struct {
	id, artifactID string
	kind           governance.ArtifactKind
	minVersion     uint32
}

// addOwnerRefsInTx adds chat-bound references to the manifest of an owner; confirm runs in the
// replacement of a manifest that changes. A manifest that holds a source no longer alive cannot be
// loaded: that is ErrConflict.
func (repository *Repository) addOwnerRefsInTx(
	ctx context.Context, tx pgx.Tx, owner manifestOwner, projectID string, view governance.SourceViewArm,
	added []governance.ArtifactSourceRef, confirm func(context.Context, pgx.Tx) error,
) error {
	current, err := repository.canon.LoadArtifactSourceTransferInTx(ctx, tx, canonstore.LoadArtifactSourceTransferInput{
		OwnerID: owner.id, ProjectID: projectID, ArtifactID: owner.artifactID, Kind: owner.kind, SourceView: view,
	})
	if err != nil {
		return err
	}
	if !current.ContentAllowed || current.OwnerManifest.Version() < owner.minVersion {
		return researchdomain.ErrConflict
	}
	refs, err := uniqueRefs(append(current.Refs, added...))
	if err != nil {
		return err
	}
	if len(refs) == len(current.Refs) {
		return nil
	}
	manifest, err := governance.NewArtifactManifestV2(governance.ArtifactManifestInput{
		Kind: owner.kind, ProjectID: projectID, ArtifactID: owner.artifactID, OwnerSourceView: view, Refs: refs,
	})
	if err != nil {
		return err
	}
	return repository.sources.ReplaceArtifactSources(ctx, tx, governancestore.ReplaceArtifactSourcesInput{
		SealArtifactSourcesInput: governancestore.SealArtifactSourcesInput{
			OwnerID: owner.id, ProjectID: projectID, ArtifactID: owner.artifactID,
			Kind: owner.kind, SourceView: view, Refs: refs, Manifest: manifest,
		},
		ExpectedManifest: current.OwnerManifest,
	}, func(ctx context.Context, tx pgx.Tx, _ string) error {
		return confirm(ctx, tx)
	})
}
