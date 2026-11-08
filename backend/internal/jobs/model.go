package jobs

import "strings"

type ModelOrigin string

const ModelOriginUser ModelOrigin = "user"

type ModelBinding struct {
	JobID                   string      `json:"-"`
	ProjectID               string      `json:"-"`
	Kind                    Kind        `json:"-"`
	Origin                  ModelOrigin `json:"-"`
	ActorUserID             string      `json:"-"`
	InitiatingAuthSessionID string      `json:"-"`
	InputContractVersion    uint32      `json:"-"`
}

type ModelClaim struct {
	Claim                   WorkerClaim `json:"-"`
	Kind                    Kind        `json:"-"`
	Subject                 Subject     `json:"-"`
	Origin                  ModelOrigin `json:"-"`
	ActorUserID             string      `json:"-"`
	InitiatingAuthSessionID string      `json:"-"`
	InputContractVersion    uint32      `json:"-"`
}

type ModelPolicy struct {
	QueueClass  QueueClass
	MaxAttempts int
}

func (input CreateModelJobInput) Validate() error {
	if strings.TrimSpace(input.JobID) == "" ||
		strings.TrimSpace(input.ProjectID) == "" ||
		input.Subject == nil ||
		input.InputContractVersion == 0 ||
		input.CreatedAt.IsZero() {
		return ErrValidation
	}
	if validateSubject(input.Subject.Kind(), input.Subject) != nil || input.Subject.Project() != input.ProjectID {
		return ErrValidation
	}
	idempotency := input.Idempotency
	if strings.TrimSpace(idempotency.RecordID) == "" ||
		strings.TrimSpace(idempotency.ScopeHash) == "" ||
		strings.TrimSpace(idempotency.OperationID) == "" ||
		strings.TrimSpace(idempotency.IdempotencyKeyHash) == "" ||
		strings.TrimSpace(idempotency.RequestHash) == "" ||
		idempotency.StatusCode <= 0 {
		return ErrValidation
	}
	if input.Origin != ModelOriginUser || strings.TrimSpace(input.ActorUserID) == "" || !input.SessionProof.Valid() {
		return ErrValidation
	}
	return nil
}

func ModelPolicyFor(input CreateModelJobInput) (ModelPolicy, error) {
	if err := input.Validate(); err != nil {
		return ModelPolicy{}, err
	}
	if input.Subject.Kind() != KindResearchChatTurn {
		return ModelPolicy{}, ErrValidation
	}
	return ModelPolicy{QueueClass: QueueClassInteractive, MaxAttempts: MaxJobAttempts}, nil
}

func (binding ModelBinding) Validate() error {
	if strings.TrimSpace(binding.JobID) == "" || strings.TrimSpace(binding.ProjectID) == "" || binding.InputContractVersion == 0 {
		return ErrValidation
	}
	if binding.Kind != KindResearchChatTurn || binding.Origin != ModelOriginUser ||
		strings.TrimSpace(binding.ActorUserID) == "" || strings.TrimSpace(binding.InitiatingAuthSessionID) == "" {
		return ErrValidation
	}
	return nil
}

func (claim ModelClaim) Validate() error {
	if claim.Claim.Validate() != nil || claim.Subject == nil ||
		validateSubject(claim.Kind, claim.Subject) != nil ||
		claim.Subject.Project() != claim.Claim.ProjectID {
		return ErrValidation
	}
	binding := ModelBinding{
		JobID: claim.Claim.JobID, ProjectID: claim.Claim.ProjectID, Kind: claim.Kind,
		Origin: claim.Origin, ActorUserID: claim.ActorUserID,
		InitiatingAuthSessionID: claim.InitiatingAuthSessionID,
		InputContractVersion:    claim.InputContractVersion,
	}
	return binding.Validate()
}
