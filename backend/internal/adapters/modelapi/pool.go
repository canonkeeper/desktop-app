package modelapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// The pool of requests of codex-lb (specs/2026-11-17-model-cache.md MC4.2): a key with a pool — the key of the site and its
// forks — waits in its queue up to its bound and is refused past it, 429 with Retry-After and a body of the type requests
// and the code rate_limit_exceeded. codex-lb puts the headers of the pool on every answer of such a key, a refusal of its
// budget and the fences of its accounts included: the body alone names a refusal of the pool.

const (
	// poolWaitDefault is the wait of a refusal of the pool without Retry-After; poolWaitMax bounds any.
	poolWaitDefault = 5 * time.Second
	poolWaitMax     = time.Minute
)

// PoolRefusal is a refusal of the pool a client without the wait of the pool — the client of the steps of apps — hands its
// caller at once, with the time the pool named (MC3.3); zero when it named none.
type PoolRefusal struct {
	RetryAfter time.Duration
}

func (refusal *PoolRefusal) Error() string { return "model API: the pool of requests is full" }

// PoolRefused says whether a refusal is one of the pool of codex-lb: a 429 whose JSON names the type requests and the code
// rate_limit_exceeded. Another 429, the headers of the pool on it or not, is not.
func PoolRefused(status int, body []byte) bool {
	refusal := refusalOf(body)
	return status == http.StatusTooManyRequests && refusal.Type == "requests" && refusal.Code == "rate_limit_exceeded"
}

// poolWaitOf is the wait of a refusal of the pool: its Retry-After, or five seconds without one, a minute at most.
func poolWaitOf(asked time.Duration) time.Duration {
	if asked <= 0 {
		return poolWaitDefault
	}
	return min(asked, poolWaitMax)
}

// waitPool waits out a refusal of the pool for the call of a class — the place of the gate is free meanwhile — and journals
// the wait by the class and its seconds, nothing of the call, its thread or its key. A context that ends first ends it.
func (client *Client) waitPool(ctx context.Context, class Class, asked time.Duration) error {
	wait := client.poolWait(poolWaitOf(asked))
	if client.config.Logger != nil {
		line, err := json.Marshal(struct {
			Event   string `json:"event"`
			Class   string `json:"class"`
			Seconds int64  `json:"seconds"`
		}{"model_pool_wait", class.String(), int64(poolWaitOf(asked).Round(time.Second) / time.Second)})
		if err == nil {
			client.config.Logger.Print(string(line))
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
