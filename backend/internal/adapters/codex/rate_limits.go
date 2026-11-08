package codex

import (
	"context"
	"time"

	"canonkeeper/backend/internal/domain/quota"

	"github.com/openai/codex/sdk/go/protocol"
)

// rateLimitResubscribeDelay is the pause before reading the app-server again after its stream
// ended or while it is not running.
const rateLimitResubscribeDelay = 30 * time.Second

// WatchRateLimits reads account/rateLimits/updated of the running app-server and hands every
// reading to record until ctx ends (specs/2026-10-28-writer-quotas-and-fair-queue.md R9.1). The
// notification is global, so one subscription of the process serves; after the app-server
// restarts the watch subscribes again. It never starts the app-server itself.
func (client *Client) WatchRateLimits(ctx context.Context, record func(context.Context, quota.LimitSample)) {
	for {
		if client.RuntimeReady() {
			if runtime, err := client.runtime(ctx); err == nil {
				runtime.readRateLimits(ctx, record)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(rateLimitResubscribeDelay):
		}
	}
}

func (client *Client) readRateLimits(ctx context.Context, record func(context.Context, quota.LimitSample)) {
	if client == nil || client.sdk == nil {
		return
	}
	stream, err := client.sdk.Notifications(ctx)
	if err != nil {
		return
	}
	defer stream.Close()
	for {
		notification, ok := stream.Next(ctx)
		if !ok {
			return
		}
		if update, ok := notification.Payload.(protocol.AccountRateLimitsUpdatedNotification); ok {
			if sample, ok := limitSample(update.RateLimits, time.Now()); ok {
				record(ctx, sample)
			}
		}
	}
}

// limitSample takes the weekly (secondary) window, or the primary one when only it is sent.
func limitSample(limits protocol.RateLimitSnapshot, observedAt time.Time) (quota.LimitSample, bool) {
	window, ok := limits.Secondary.Value()
	if !ok {
		if window, ok = limits.Primary.Value(); !ok {
			return quota.LimitSample{}, false
		}
	}
	sample := quota.LimitSample{ObservedAt: observedAt.UTC().Truncate(time.Microsecond), UsedPercent: float64(window.UsedPercent)}
	if id, ok := limits.LimitID.Value(); ok {
		sample.LimitID = id
	}
	if minutes, ok := window.WindowDurationMins.Value(); ok {
		sample.WindowMinutes = minutes
	}
	if resets, ok := window.ResetsAt.Value(); ok && resets > 0 {
		at := time.Unix(resets, 0).UTC()
		sample.ResetsAt = &at
	}
	return sample, true
}
