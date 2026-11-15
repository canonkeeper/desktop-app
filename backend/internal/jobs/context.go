package jobs

import "context"

// jobIDKey is the key of the Job a piece of work runs under.
type jobIDKey struct{}

// WithJobID is ctx of the work of a Job (specs/2026-11-13-desk-gateway.md GW3.2): who knows the Job — the worker of a
// claim, the tool of a turn — puts it, and the client of the model reads it to name the Job of a call to the local gateway
// of the app. The ports of the consumers of the model do not change.
func WithJobID(ctx context.Context, jobID string) context.Context {
	if jobID == "" {
		return ctx
	}
	return context.WithValue(ctx, jobIDKey{}, jobID)
}

// JobIDOf is the Job of the work, empty outside one.
func JobIDOf(ctx context.Context) string {
	jobID, _ := ctx.Value(jobIDKey{}).(string)
	return jobID
}
