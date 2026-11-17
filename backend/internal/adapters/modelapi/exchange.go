package modelapi

import (
	"context"

	"canonkeeper/backend/internal/domain/quota"
)

// exchange is one counted HTTP attempt of a call: each request of it holds a place of the gate for itself alone
// (specs/2026-10-28-writer-quotas-and-fair-queue.md R11.1), and what its answers spent adds up. A refusal of the thread of
// the call goes again at once with the next generation of the thread, once a call (specs/2026-11-17-model-cache.md MC1.2);
// a refusal of the pool of codex-lb is waited out by the client of the site and goes again while the call lives, and is a
// PoolRefusal at once for a client without the wait (MC4.2, MC3.3). Neither counts as an attempt.
func exchange[T any](ctx context.Context, client *Client, class Class, thread *callThread,
	send func() (T, quota.ModelUsage, attempt, error),
) (T, quota.ModelUsage, attempt, error) {
	var usage quota.ModelUsage
	for {
		var none T
		if err := client.gate.acquire(ctx, class); err != nil {
			return none, usage, attempt{}, err
		}
		result, spent, outcome, err := send()
		client.gate.release()
		usage = usage.Add(spent)
		switch {
		case err == nil:
		case outcome.threadRefused && thread.refusedOnce():
			continue
		case outcome.pool && !client.config.WaitsPool:
			return none, usage, attempt{}, &PoolRefusal{RetryAfter: outcome.wait}
		case outcome.pool:
			if err := client.waitPool(ctx, class, outcome.wait); err != nil {
				return none, usage, attempt{}, err
			}
			continue
		}
		return result, usage, outcome, err
	}
}
