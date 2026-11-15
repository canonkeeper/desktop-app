// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"canonkeeper/backend/internal/adapters/llmgateway"
	"canonkeeper/backend/internal/adapters/siteapi"
	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/aisettings"
	appworkdomain "canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	appworkrepo "canonkeeper/backend/internal/repository/postgres/appwork"
	appworkservice "canonkeeper/backend/internal/services/appwork"
	quotaservice "canonkeeper/backend/internal/services/quota"

	"github.com/jackc/pgx/v5"
)

// settingsWork is the section work of settings.json: the salt the refs of the permits of the app are derived with
// (specs/2026-11-13-desk-account-ai.md AI2.2) — the same after a restart, so a Job taken again asks its own permit.
const settingsWork = "work"

type workFile struct {
	Salt string `json:"salt"`
}

// WorkSalt is the salt of the refs of this data directory, made at its first need.
func (current *settings) WorkSalt() ([]byte, error) {
	current.mu.Lock()
	defer current.mu.Unlock()
	var section workFile
	if json.Unmarshal(current.values[settingsWork], &section) == nil {
		if salt, err := hex.DecodeString(section.Salt); err == nil && len(salt) == 32 {
			return salt, nil
		}
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(workFile{Salt: hex.EncodeToString(salt)})
	if err != nil {
		return nil, err
	}
	if err := current.write(settingsWork, encoded); err != nil {
		return nil, err
	}
	return salt, nil
}

// charges is the port Charges of the fence of the app (AI1.4, AI2.1, AI2.8, AI3.8): a turn and an upload of materials
// keep the source of the settings in the transaction of their start — it serves them to their end —, and the work of the
// account is opened at its site as the last step; a correction of a turn of the account is charged and returned there.
type charges struct {
	settings *settings
	link     linkHolder
	work     *appworkservice.Work
}

func (counted charges) OpenInTx(ctx context.Context, tx pgx.Tx, work quota.Work) error {
	var source appworkdomain.Source
	link := ""
	switch counted.settings.AI().Source {
	case aisettings.SourceOwn:
		source = appworkdomain.SourceOwn
	case aisettings.SourceAccount:
		if counted.link == nil || !counted.link.Linked() {
			return quota.ErrCounterUnlinked
		}
		if link = counted.work.Mark(); link == "" {
			return quota.ErrCounterUnlinked
		}
		source = appworkdomain.SourceAccount
	default:
		return nil
	}
	if err := appworkrepo.RecordInTx(ctx, tx, work.JobID, appworkdomain.Kind(work.Kind), source, link, work.At); err != nil {
		return err
	}
	if source != appworkdomain.SourceAccount {
		return nil
	}
	return counted.work.Open(ctx, work)
}

func (counted charges) SteerInTx(ctx context.Context, _ pgx.Tx, steer quota.Steer) error {
	return counted.work.Steer(ctx, steer)
}

func (counted charges) UnsteerInTx(ctx context.Context, _ pgx.Tx, steer quota.Steer) error {
	return counted.work.Unsteer(ctx, steer)
}

// questions answers getJob of a turn whether the unit of its question came back (AI2.9).
type questions struct{ work *appworkservice.Work }

func (asked questions) Returned(ctx context.Context, job jobs.Job) string {
	return string(asked.work.Returned(ctx, job.ID))
}

// accountQuota is getAccountQuota of the app (AI2.11): the counters of the computer, and — while the source is the account
// and the site told them — the questions of the day, «разом» and the uploads of materials of the day as the site counts
// them, one number for the site and every app of the account; the volume of the books stays the computer's. When the site
// told the places of this app, «разом» says who holds its places (specs/2026-11-15-desk-turns-here.md TH2.1): the turns this
// app still answers, counted here, the places the site still counts for its questions over here, and the site with the other
// apps.
type accountQuota struct {
	local    quotaservice.GetAccountQuota
	settings *settings
	link     accountLink
	work     *appworkservice.Work
}

func (counts accountQuota) Execute(ctx context.Context, userID string) (quota.Account, error) {
	local, err := counts.local.Execute(ctx, userID)
	if err != nil || counts.settings.AI().Source != aisettings.SourceAccount || !counts.link.parts.Linked() {
		return local, err
	}
	state := counts.link.parts.Read.Execute(ctx, false)
	told := state.Account
	if told == nil || !told.Quota {
		return local, nil
	}
	local.ChatTurns, local.Limits.ChatTurnsPerDay = told.Questions.Used, told.Questions.Limit
	local.ActiveTurns, local.Limits.ActiveTurns = told.ActiveTurns.Used, told.ActiveTurns.Limit
	if told.ThisApp != nil {
		answering, err := counts.work.Answering(ctx)
		if err != nil {
			return quota.Account{}, err
		}
		places := accountdomain.PlacesOf(told.ActiveTurns.Used, *told.ThisApp, answering)
		local.Places = &places
	}
	local.Imports, local.Limits.ImportsPerDay = told.Imports.Used, told.Imports.Limit
	if told.ResetsAt != nil {
		local.ResetsAt = told.ResetsAt.UTC()
	}
	if day, err := time.Parse(time.DateOnly, told.Day); err == nil {
		local.Day = day
	}
	return local, nil
}

// errNoSalt is a data directory whose salt of the refs could not be kept.
var errNoSalt = errors.New("desktop: the salt of the refs of the permits could not be kept")

// gatewayAccount is the helper «Аккаунт CanonKeeper» as the gateway asks it (specs/2026-11-13-desk-account-ai.md AI1.4,
// AI2.3–AI2.6, AI3.1–AI3.3): the link with the account, the count of the work of the run and the one client of the app to
// the site.
type gatewayAccount struct {
	link   linkHolder
	host   string
	work   *appworkservice.Work
	client *siteapi.Client
}

func (account gatewayAccount) Linked() bool { return account.link.Linked() }

func (account gatewayAccount) Host() string { return account.host }

func (account gatewayAccount) WorkOf(ctx context.Context, jobID string) (llmgateway.JobWork, bool) {
	ticket, ok := account.work.WorkOf(ctx, jobID)
	return llmgateway.JobWork{Source: ticket.Source, Kind: ticket.Kind, PermitID: ticket.PermitID, ChatID: ticket.ChatID}, ok
}

func (account gatewayAccount) Materials(ctx context.Context, jobID string) (string, error) {
	return account.work.Materials(ctx, jobID)
}

func (account gatewayAccount) Reading(ctx context.Context, jobID string, renew bool) (string, error) {
	return account.work.Reading(ctx, jobID, renew)
}

func (account gatewayAccount) Refused(jobID, purpose string, answer appworkdomain.Answer) {
	account.work.Refused(jobID, purpose, answer)
}

func (account gatewayAccount) Accepted(jobID string) { account.work.Accepted(jobID) }

func (account gatewayAccount) Dropped(jobID string) { account.work.Dropped(jobID) }

func (account gatewayAccount) RefusedNow(purpose, chatID string) string {
	return account.work.RefusedNow(purpose, chatID)
}

func (account gatewayAccount) Responses(ctx context.Context, permitID string, body []byte) (io.ReadCloser, error) {
	return account.client.Responses(ctx, permitID, body)
}

func (account gatewayAccount) Step(ctx context.Context, step, permitID string, body []byte) (llmgateway.StepAnswer, error) {
	answer, err := account.client.Step(ctx, step, permitID, body)
	if err != nil {
		return llmgateway.StepAnswer{}, err
	}
	return llmgateway.StepAnswer{Text: answer.Text, Model: answer.Model, Usage: llmgateway.StepUsage{
		InputTokens: answer.Usage.InputTokens, CachedInputTokens: answer.Usage.CachedInputTokens,
		OutputTokens: answer.Usage.OutputTokens, ReasoningOutputTokens: answer.Usage.ReasoningOutputTokens,
	}}, nil
}

// workEnded closes the permit of a Job of the reading or of an upload of materials once the builder committed its end
// (specs/2026-11-13-desk-account-ai.md AI3.2, AI3.8): done, canceled or failed; a Job that asked no model holds none.
type workEnded struct{ work *appworkservice.Work }

func (ended workEnded) Ended(ctx context.Context, jobID string, outcome jobs.Status) {
	closed := appworkdomain.OutcomeFailed
	switch outcome {
	case jobs.StatusSucceeded:
		closed = appworkdomain.OutcomeDone
	case jobs.StatusCanceled:
		closed = appworkdomain.OutcomeCanceled
	}
	ended.work.Done(ctx, jobID, closed)
}
