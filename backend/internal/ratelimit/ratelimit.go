package ratelimit

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync"
	"time"
)

const AnonymousPrincipal = "anonymous"

var ErrInvalidConfig = errors.New("invalid rate limiter config")

type Key struct {
	PrincipalID string
	ProjectID   string
	Operation   string
	BucketID    string
}

func AuthenticatedKey(principalID, projectID, operation string) Key {
	return Key{PrincipalID: principalID, ProjectID: projectID, Operation: operation}
}

func AnonymousKey(operation, ip string) Key {
	bucket := "ip:" + strings.TrimSpace(ip)
	return Key{PrincipalID: AnonymousPrincipal, Operation: operation, BucketID: bucket}
}

func (key Key) String() string {
	return strings.Join([]string{
		"principal=" + key.PrincipalID,
		"project=" + key.ProjectID,
		"operation=" + key.Operation,
		"bucket=" + key.BucketID,
	}, "|")
}

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	RetryAt    time.Time
}

type Limiter interface {
	Allow(ctx context.Context, key Key) (Decision, error)
}

// Rule is a budget of Limit requests per Window for one key.
type Rule struct {
	Limit  int
	Window time.Duration
}

// MemoryLimiterConfig gives every key the default Limit per Window unless its
// operation has its own rule in Operations.
type MemoryLimiterConfig struct {
	Limit      int
	Window     time.Duration
	Operations map[string]Rule
	Now        func() time.Time
}

type MemoryLimiter struct {
	rule       Rule
	operations map[string]Rule
	now        func() time.Time
	mu         sync.Mutex
	buckets    map[string]memoryBucket
}

type memoryBucket struct {
	count   int
	resetAt time.Time
}

func NewMemoryLimiter(config MemoryLimiterConfig) *MemoryLimiter {
	if config.Now == nil {
		config.Now = time.Now
	}
	return &MemoryLimiter{
		rule:       Rule{Limit: config.Limit, Window: config.Window},
		operations: maps.Clone(config.Operations),
		now:        config.Now,
		buckets:    make(map[string]memoryBucket),
	}
}

func (limiter *MemoryLimiter) Allow(ctx context.Context, key Key) (Decision, error) {
	rule, ok := limiter.operations[key.Operation]
	if !ok {
		rule = limiter.rule
	}
	if rule.Limit <= 0 || rule.Window <= 0 {
		return Decision{}, ErrInvalidConfig
	}
	select {
	case <-ctx.Done():
		return Decision{}, ctx.Err()
	default:
	}

	now := limiter.now()
	mapKey := key.String()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	limiter.deleteExpiredBuckets(now)
	bucket := limiter.buckets[mapKey]
	if bucket.resetAt.IsZero() || !now.Before(bucket.resetAt) {
		bucket = memoryBucket{
			resetAt: now.Add(rule.Window),
		}
	}

	if bucket.count >= rule.Limit {
		retryAfter := bucket.resetAt.Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		limiter.buckets[mapKey] = bucket
		return Decision{
			Allowed:    false,
			RetryAfter: retryAfter,
			RetryAt:    bucket.resetAt,
		}, nil
	}

	bucket.count++
	limiter.buckets[mapKey] = bucket
	return Decision{Allowed: true}, nil
}

func (limiter *MemoryLimiter) deleteExpiredBuckets(now time.Time) {
	for key, bucket := range limiter.buckets {
		if !bucket.resetAt.IsZero() && !now.Before(bucket.resetAt) {
			delete(limiter.buckets, key)
		}
	}
}
