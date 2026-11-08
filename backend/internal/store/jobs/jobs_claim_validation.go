package jobstore

import (
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/store/sqlc"
)

func modelClaimLockStatusMatches(locked sqlc.Job, expected jobs.Status) bool {
	switch expected {
	case jobs.StatusRunning:
		return locked.Status == string(expected) && !locked.CancelRequestedAt.Valid
	case jobs.StatusCanceling:
		return locked.Status == string(expected) && locked.CancelRequestedAt.Valid
	default:
		return false
	}
}

func validateModelClaimLockPreview(preview jobs.ModelClaimLockPreview) error {
	if preview.Claim.Validate() != nil || preview.Binding.Validate() != nil || preview.Subject == nil ||
		preview.Binding.JobID != preview.Claim.JobID || preview.Binding.ProjectID != preview.Claim.ProjectID ||
		preview.Subject.Kind() != preview.Binding.Kind || preview.Subject.Project() != preview.Binding.ProjectID {
		return jobs.ErrValidation
	}
	return nil
}
