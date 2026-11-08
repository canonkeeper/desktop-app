package jobstore

import (
	"math"
	"strings"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

func validateUserModelStartInput(input UserModelStartInput) error {
	switch input.Kind {
	case jobs.KindResearchChatTurn:
	default:
		return jobs.ErrValidation
	}
	idempotency := input.Idempotency
	if input.InputContractVersion == 0 || input.InputContractVersion > math.MaxInt32 ||
		input.CreatedAt.IsZero() || !input.SessionProof.Valid() ||
		strings.TrimSpace(idempotency.ScopeHash) == "" || strings.TrimSpace(idempotency.OperationID) == "" ||
		strings.TrimSpace(idempotency.IdempotencyKeyHash) == "" ||
		idempotency.StatusCode <= 0 || idempotency.StatusCode > math.MaxInt32 {
		return jobs.ErrValidation
	}
	return nil
}

func validateUserModelLockedTarget(projectID string, target UserModelLockedTarget) error {
	view, present := target.Source.SourceView.View()
	if !present || view == "" || strings.TrimSpace(target.RequestHash) == "" {
		return jobs.ErrConflict
	}
	_, err := newModelArtifactManifest(target.ManifestVersion, governance.ArtifactManifestInput{
		Kind: governance.ArtifactAgentWorkflowInput, ProjectID: projectID, ArtifactID: projectID,
		OwnerSourceView: target.Source.SourceView, Refs: target.Source.Refs,
	})
	return err
}

func userModelSourceContains(full, target jobs.JobSourceManifest) bool {
	fullView, fullPresent := full.SourceView.View()
	targetView, targetPresent := target.SourceView.View()
	if !fullPresent || !targetPresent || fullView != targetView || len(target.Refs) == 0 {
		return false
	}
	available := make(map[string]struct{}, len(full.Refs))
	for _, ref := range full.Refs {
		projection, err := governance.ArtifactSourceRefProjection(ref)
		if err != nil {
			return false
		}
		available[string(projection)] = struct{}{}
	}
	for _, ref := range target.Refs {
		projection, err := governance.ArtifactSourceRefProjection(ref)
		if err != nil {
			return false
		}
		if _, found := available[string(projection)]; !found {
			return false
		}
	}
	return true
}
