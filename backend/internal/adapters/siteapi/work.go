// SPDX-License-Identifier: AGPL-3.0-only

package siteapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/appwork"
)

// The terms of the work of the helper of the account (specs/2026-11-13-desk-account-ai.md AI2.1, AI2.7, AI3.1): an operation
// of a permit has its own short term; a step waits for the whole call of the model — the site answers its headers only
// after it, a card takes minutes — and a stream of a turn has no term of its own: the request of Codex ends it.
const (
	permitTime = 10 * time.Second
	stepTime   = 30 * time.Minute
	// maxStep bounds the answer of a step: the text of the model and its usage.
	maxStep = 16 << 20
)

// permitJSON is AppPermit of the contract; a field this app does not know is skipped.
type permitJSON struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Outcome  string `json:"outcome"`
	Returned bool   `json:"returned"`
}

func (permit permitJSON) domain() appwork.Permit {
	return appwork.Permit{
		ID: permit.ID, Kind: appwork.Kind(permit.Kind), State: appwork.State(permit.State), Outcome: appwork.Outcome(permit.Outcome),
		Returned: permit.Returned,
	}
}

// StepAnswer is AppStepAnswer of the contract: the text of the model whole and its usage.
type StepAnswer struct {
	Text  string    `json:"text"`
	Model string    `json:"model"`
	Usage StepUsage `json:"usage"`
}

// StepUsage is the usage of a step as the site counted it.
type StepUsage struct {
	Calls                 int64 `json:"calls"`
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
}

// WhenDropped tells the owner of the link that a refusal of an operation of the work dropped the pass held
// (specs/2026-11-13-desk-account-ai.md AI2.1): the link is broken at once.
func (client *Client) WhenDropped(dropped func()) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.dropped = dropped
}

// LinkMark is the mark of the link of the pass held now (specs/2026-11-13-desk-account-ai.md AI2.3): a SHA-256 of the salt
// and the pass, never the pass; "" — no pass. The site knows the refs of one device only, so a work started under another
// link has no permit the site would find by its ref, and asking it would open a new one.
func (client *Client) LinkMark(salt []byte) string {
	pass, _ := client.held()
	if pass == "" {
		return ""
	}
	hash := sha256.New()
	hash.Write(salt)
	hash.Write([]byte("\x00link\x00"))
	hash.Write([]byte(pass))
	return hex.EncodeToString(hash.Sum(nil))
}

// OpenPermit is openModelPermit: an unknown ref opens a permit and spends its unit, a known one answers the same permit as it
// is now — open, closed or expired, with its outcome — and spends nothing.
func (client *Client) OpenPermit(ctx context.Context, kind appwork.Kind, ref string) (appwork.Permit, error) {
	body, _ := json.Marshal(struct {
		Kind string `json:"kind"`
		Ref  string `json:"ref"`
	}{string(kind), ref})
	var permit permitJSON
	err := client.work(ctx, http.MethodPost, "/api/v1/app/permits", body, &permit)
	return permit.domain(), err
}

// ChargeUnit is chargeModelPermit: one more unit of the turn for a correction; the same ref answers the same.
func (client *Client) ChargeUnit(ctx context.Context, permitID, ref string) error {
	body, _ := json.Marshal(struct {
		Ref string `json:"ref"`
	}{ref})
	return client.work(ctx, http.MethodPost, "/api/v1/app/permits/"+url.PathEscape(permitID)+"/units", body, nil)
}

// ReturnUnit is returnModelPermitUnit: the unit of a correction the turn did not take comes back once; false — over the
// ceiling of the returns of the day.
func (client *Client) ReturnUnit(ctx context.Context, permitID, ref string) (bool, error) {
	var answer struct {
		Returned bool `json:"returned"`
	}
	err := client.work(ctx, http.MethodDelete, "/api/v1/app/permits/"+url.PathEscape(permitID)+"/units/"+url.PathEscape(ref), nil, &answer)
	return answer.Returned, err
}

// ClosePermit is closeModelPermit: the work ended so; the site keeps the first outcome and decides the return.
func (client *Client) ClosePermit(ctx context.Context, permitID string, outcome appwork.Outcome) (appwork.Permit, error) {
	body, _ := json.Marshal(struct {
		Outcome string `json:"outcome"`
	}{string(outcome)})
	var permit permitJSON
	err := client.work(ctx, http.MethodPost, "/api/v1/app/permits/"+url.PathEscape(permitID)+"/close", body, &permit)
	return permit.domain(), err
}

// work sends one operation of a permit in its own term and reads its answer into answer.
func (client *Client) work(ctx context.Context, method, path string, body []byte, answer any) error {
	ctx, cancel := context.WithTimeout(ctx, permitTime)
	defer cancel()
	headers := map[string]string{"Accept": "application/json"}
	if body != nil {
		headers["Content-Type"] = "application/json"
	}
	response, err := client.send(ctx, client.http, method, path, headers, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
	if err != nil {
		return domain.ErrUnreachable
	}
	if answer != nil && len(raw) > 0 && json.Unmarshal(raw, answer) != nil {
		return domain.ErrUnreachable
	}
	return nil
}

// Responses is streamAppResponses: the body of a request of Codex, compressed by gzip, under the permit of its turn; the
// stream of the provider comes as it is, and the caller closes it.
func (client *Client) Responses(ctx context.Context, permitID string, body []byte) (io.ReadCloser, error) {
	compressed, err := gzipped(body)
	if err != nil {
		return nil, err
	}
	response, err := client.send(ctx, client.long, http.MethodPost, "/api/v1/app/ai/responses", map[string]string{
		"X-CanonKeeper-Permit": permitID, "Content-Type": "application/json", "Content-Encoding": "gzip", "Accept": "text/event-stream",
	}, compressed)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// Step is runAppStep: the words of the core and the input of one call, compressed by gzip, under the permit of the kind of
// the step; the answer comes whole after the whole call of the model, in the term of a step.
func (client *Client) Step(ctx context.Context, step, permitID string, body []byte) (StepAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, stepTime)
	defer cancel()
	compressed, err := gzipped(body)
	if err != nil {
		return StepAnswer{}, err
	}
	response, err := client.send(ctx, client.long, http.MethodPost, "/api/v1/app/ai/steps/"+url.PathEscape(step), map[string]string{
		"X-CanonKeeper-Permit": permitID, "Content-Type": "application/json", "Content-Encoding": "gzip", "Accept": "application/json",
	}, compressed)
	if err != nil {
		return StepAnswer{}, err
	}
	defer response.Body.Close()
	var answer StepAnswer
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxStep))
	if err != nil || json.Unmarshal(raw, &answer) != nil {
		return StepAnswer{}, domain.ErrUnreachable
	}
	return answer, nil
}

// send sends one request of the work with the pass held and reads the site's verdict (AI3.3): an answer of 2xx is the
// response; the site's 429 is read by its code — a refusal of the tier, ai.busy and saga_transfer.* are the answer of the
// operation, while
// rate_limit.exceeded or a 429 without a code keeps the client from the site until its Retry-After —; a refusal of the
// models of the site (ai.*) is its answer too, any other 5xx and a broken connection are no connection, waited for by their
// Retry-After when they name one; app.pass_invalid drops the pass held and tells the link. No address goes into an error.
func (client *Client) send(ctx context.Context, through *http.Client, method, path string, headers map[string]string, body []byte) (*http.Response, error) {
	pass, generation := client.held()
	if pass == "" {
		return nil, errors.Join(domain.ErrNotHeld, domain.ErrNotSent)
	}
	client.mu.Lock()
	waiting := client.now().Before(client.notBefore)
	client.mu.Unlock()
	if waiting {
		return nil, errors.Join(domain.ErrUnreachable, domain.ErrNotSent)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.site+path, reader)
	if err != nil {
		return nil, errors.New("siteapi: the request could not be made")
	}
	request.Header.Set("User-Agent", "CanonKeeper")
	request.Header.Set("Authorization", "Bearer "+pass)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := through.Do(request)
	if err != nil {
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled) {
			return nil, context.Canceled
		}
		return nil, domain.ErrUnreachable
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &problem)
	retry := response.Header.Get("Retry-After")
	refusal := &appwork.Refusal{Status: response.StatusCode, Code: problem.Code}
	if retry != "" {
		refusal.RetryAfter = waitOf(retry, client.now())
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized && problem.Code == appwork.CodePassInvalid:
		if client.drop(generation) {
			client.mu.Lock()
			dropped := client.dropped
			client.mu.Unlock()
			if dropped != nil {
				dropped()
			}
			return nil, domain.ErrPassInvalid
		}
		return nil, domain.ErrPassReplaced
	case response.StatusCode == http.StatusTooManyRequests && (appwork.Quota(problem.Code) || problem.Code == appwork.CodeBusy ||
		strings.HasPrefix(problem.Code, "saga_transfer.")):
		// A refusal of the tier, ai.busy and the busy saves of a transfer of a saga (specs/2026-11-16-desk-site-saga.md TR4.2)
		// answer their operation and keep the client from nothing.
		return nil, refusal
	case response.StatusCode == http.StatusTooManyRequests:
		client.wait(waitOf(retry, client.now()))
		return nil, domain.ErrUnreachable
	case response.StatusCode >= 500 && strings.HasPrefix(problem.Code, "ai."):
		return nil, refusal
	case response.StatusCode >= 500:
		if retry != "" {
			client.wait(refusal.RetryAfter)
		}
		return nil, domain.ErrUnreachable
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return nil, domain.ErrUnreachable
	default:
		return nil, refusal
	}
}

// wait keeps the client from the site for the wait.
func (client *Client) wait(wait time.Duration) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if until := client.now().Add(wait); until.After(client.notBefore) {
		client.notBefore = until
	}
}

func gzipped(body []byte) ([]byte, error) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(body); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}
