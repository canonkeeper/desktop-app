package jobs

import "errors"

var (
	ErrNotFound   = errors.New("jobs: not found")
	ErrForbidden  = errors.New("jobs: forbidden")
	ErrValidation = errors.New("jobs: validation failed")
	ErrConflict   = errors.New("jobs: conflict")
)

type WorkerClaim struct {
	ProjectID        string
	JobID            string
	ClaimIncarnation int64
}

func (claim WorkerClaim) Validate() error {
	if claim.ProjectID == "" || claim.JobID == "" || claim.ClaimIncarnation < 1 {
		return ErrValidation
	}
	return nil
}
