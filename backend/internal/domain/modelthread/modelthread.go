// SPDX-License-Identifier: AGPL-3.0-only

// Package modelthread is the thread of a direct call of a model (specs/2026-11-17-model-cache.md MC1.1): the owner of a call
// names what its calls share — the Job and the step of a build, the chunk of a scan, the call of the voice — by parts in
// the context of the call, and the client of the models of the site makes of them the thread codex-lb keeps on one account,
// so the provider gives the shared beginning of the calls from its cache. The parts never go into a journal or an error.
package modelthread

import "context"

type contextKey struct{}

// With is ctx of the calls of a thread of the parts. A thread with an empty part, or with none, is no thread: a call
// outside a Job keeps none (MC1.3), and ctx is as it was.
func With(ctx context.Context, parts ...string) context.Context {
	if len(parts) == 0 {
		return ctx
	}
	for _, part := range parts {
		if part == "" {
			return ctx
		}
	}
	return context.WithValue(ctx, contextKey{}, append([]string(nil), parts...))
}

// Of is the parts of the thread of ctx, in their order; false without a thread.
func Of(ctx context.Context) ([]string, bool) {
	parts, ok := ctx.Value(contextKey{}).([]string)
	if !ok {
		return nil, false
	}
	return append([]string(nil), parts...), true
}
