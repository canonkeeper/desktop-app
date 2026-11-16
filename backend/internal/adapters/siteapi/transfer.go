// SPDX-License-Identifier: AGPL-3.0-only

package siteapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	domain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/appwork"
)

// The transfer of a saga of the site to the app (specs/2026-11-16-desk-site-saga.md TR2.1, TR4.2): the announcement of a
// transfer is a short operation; the download is a stream with no term of its own — the site answers its headers once the
// saga is prepared, and the caller bounds the whole of it and its silences. A refusal of the transfer is its own answer —
// saga_transfer.busy keeps the client from nothing else —, the refusal of the pass drops it as every operation does.

// RequestSagaTransfer is requestAppSagaTransfer: the challenge of the verifier of a transfer, and the number of the request
// the page /transfer opens with. The refusal of the pass held is domain.ErrPassInvalid; any other failure is
// domain.ErrUnreachable or domain.ErrNotHeld.
func (client *Client) RequestSagaTransfer(ctx context.Context, challenge string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, shortTime)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"challenge": challenge})
	response, err := client.send(ctx, client.http, http.MethodPost, "/api/v1/app/saga-transfers/requests", map[string]string{
		"Accept": "application/json", "Content-Type": "application/json",
	}, body)
	if err != nil {
		return "", transferError(err)
	}
	defer response.Body.Close()
	var answer struct {
		Request string `json:"request"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &answer) != nil || answer.Request == "" {
		return "", domain.ErrUnreachable
	}
	return answer.Request, nil
}

// TakeSagaTransfer is takeAppSagaTransfer: the stream of the saga file of the code and its name as the site named it, only
// to be shown; the caller closes the stream. Only 200 with application/zip is a saga. The refusal of the code is
// domain.ErrTransferInvalid, a saga that went domain.ErrTransferGone, the busy saves of the site — and a Retry-After the
// client still waits out — domain.SiteBusy with the wait; the refusal of the pass held is domain.ErrPassInvalid; any other
// failure is domain.ErrUnreachable or domain.ErrNotHeld.
func (client *Client) TakeSagaTransfer(ctx context.Context, code, verifier string) (io.ReadCloser, string, error) {
	client.mu.Lock()
	wait := client.notBefore.Sub(client.now())
	client.mu.Unlock()
	if wait > 0 {
		return nil, "", domain.SiteBusy{Wait: wait}
	}
	body, _ := json.Marshal(map[string]string{"code": code, "verifier": verifier})
	response, err := client.send(ctx, client.long, http.MethodPost, "/api/v1/app/saga-transfers", map[string]string{
		"Accept": "application/zip", "Content-Type": "application/json",
	}, body)
	if err != nil {
		return nil, "", transferError(err)
	}
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || mediaErr != nil || media != "application/zip" {
		_ = response.Body.Close()
		return nil, "", domain.ErrUnreachable
	}
	name := ""
	if _, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
	}
	return response.Body, name, nil
}

// transferError is the error of an operation of the transfer by the refusal the site answered.
func transferError(err error) error {
	var refusal *appwork.Refusal
	if !errors.As(err, &refusal) {
		return err
	}
	switch refusal.Code {
	case "app.transfer_invalid":
		return domain.ErrTransferInvalid
	case "saga_transfer.gone":
		return domain.ErrTransferGone
	case "saga_transfer.busy":
		return domain.SiteBusy{Wait: refusal.RetryAfter, Saves: true}
	}
	return domain.ErrUnreachable
}
