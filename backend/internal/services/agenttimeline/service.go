package agenttimeline

import (
	"context"
	"errors"
	"strings"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	domain "canonkeeper/backend/internal/domain/agenttimeline"
)

var ErrInvalid = errors.New("invalid agent timeline request")

type ThreadBinding struct {
	Flow         agentdomain.Flow
	RootThreadID string
	TurnID       string
}

type ResolveRoot func(context.Context, string, string) (ThreadBinding, error)

type NativeThread interface {
	ThreadItems(context.Context, agentdomain.Flow, string) ([]domain.RawItem, error)
	ThreadUpdates(context.Context, agentdomain.Flow, string) (<-chan domain.Update, error)
}

type Service struct {
	resolveRoot ResolveRoot
	native      NativeThread
}

func New(resolveRoot ResolveRoot, native NativeThread) *Service {
	return &Service{resolveRoot: resolveRoot, native: native}
}

func (service *Service) Snapshot(ctx context.Context, projectID, jobID string) (domain.Snapshot, error) {
	binding, err := service.threadBinding(ctx, projectID, jobID)
	if err != nil {
		return domain.Snapshot{}, err
	}
	if binding.RootThreadID == "" {
		return domain.Project(jobID, nil)
	}
	items, err := service.native.ThreadItems(ctx, binding.Flow, binding.RootThreadID)
	if err != nil {
		return domain.Snapshot{}, err
	}
	return domain.Project(jobID, items)
}

func (service *Service) Updates(ctx context.Context, projectID, jobID string) (<-chan domain.Update, error) {
	binding, err := service.threadBinding(ctx, projectID, jobID)
	if err != nil {
		return nil, err
	}
	if binding.RootThreadID == "" {
		return nil, domain.ErrThreadPending
	}
	return service.native.ThreadUpdates(ctx, binding.Flow, binding.RootThreadID)
}

func (service *Service) threadBinding(ctx context.Context, projectID, jobID string) (ThreadBinding, error) {
	if service == nil || service.resolveRoot == nil || service.native == nil || ctx == nil ||
		strings.TrimSpace(projectID) == "" || strings.TrimSpace(jobID) == "" {
		return ThreadBinding{}, ErrInvalid
	}
	binding, err := service.resolveRoot(ctx, projectID, jobID)
	if err != nil {
		return ThreadBinding{}, err
	}
	if !binding.Flow.Valid() || (binding.RootThreadID != "" && strings.TrimSpace(binding.RootThreadID) == "") ||
		(binding.TurnID != "" && strings.TrimSpace(binding.TurnID) == "") {
		return ThreadBinding{}, ErrInvalid
	}
	return binding, nil
}
