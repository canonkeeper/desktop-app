// SPDX-License-Identifier: AGPL-3.0-only

package siteapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
)

const challengeOfTheTest = "challenge-of-the-test-00000000000000000000"

// specs/2026-11-16-desk-site-saga.md TR2.1, TR4.2: the announcement and the download go with the pass held to the paths of
// the contract, without a cookie, an Origin or a query; only 200 with application/zip is a saga, named for the page only;
// the refusals of the transfer are its own — saga_transfer.busy keeps the client from nothing else —, and a Retry-After the
// client waits out is told as a wait without a request.
func TestTheTransferAsksTheSiteWithThePass(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	site := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"request":"request-of-the-test","expiresAt":"2026-11-16T09:10:00Z","later":1}`)
	})
	client := trusted(site, func() time.Time { return now })
	if _, err := client.RequestSagaTransfer(context.Background(), challengeOfTheTest); !errors.Is(err, domain.ErrNotHeld) {
		t.Fatalf("without a pass = %v", err)
	}
	client.Hold(passOfTheTest)
	request, err := client.RequestSagaTransfer(context.Background(), challengeOfTheTest)
	if err != nil || request != "request-of-the-test" {
		t.Fatalf("RequestSagaTransfer = %q, %v", request, err)
	}

	site.set(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename*=utf-8''%D0%A1%D0%B5%D0%B2%D0%B5%D1%80.cksaga`)
		_, _ = io.WriteString(w, "PK-the-saga")
	})
	stream, name, err := client.TakeSagaTransfer(context.Background(), "code-of-the-test", "verifier-of-the-test")
	if err != nil || name != "Север.cksaga" {
		t.Fatalf("TakeSagaTransfer = %q, %v", name, err)
	}
	file, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(file) != "PK-the-saga" {
		t.Fatalf("the file = %q", file)
	}
	requests := site.requests()
	for index, path := range []string{"/api/v1/app/saga-transfers/requests", "/api/v1/app/saga-transfers"} {
		request := requests[index]
		if request.Method != http.MethodPost || request.URL.Path != path || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "Bearer "+passOfTheTest ||
			request.Header.Get("Origin") != "" || request.Header.Get("Cookie") != "" {
			t.Fatalf("request %d = %s %s %v", index, request.Method, request.URL, request.Header)
		}
	}
	if site.bodies[0] != `{"challenge":"`+challengeOfTheTest+`"}` || site.bodies[1] != `{"code":"code-of-the-test","verifier":"verifier-of-the-test"}` {
		t.Fatalf("the bodies = %q", site.bodies)
	}

	site.set(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>")
	})
	if _, _, err := client.TakeSagaTransfer(context.Background(), "code", "verifier"); !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("an answer that is not a saga file = %v", err)
	}
	for _, refusal := range []struct {
		status int
		code   string
		want   error
	}{
		{http.StatusBadRequest, "app.transfer_invalid", domain.ErrTransferInvalid},
		{http.StatusNotFound, "saga_transfer.gone", domain.ErrTransferGone},
		{http.StatusBadGateway, "server.error", domain.ErrUnreachable},
	} {
		site.set(problem(refusal.status, refusal.code))
		if _, _, err := client.TakeSagaTransfer(context.Background(), "code", "verifier"); !errors.Is(err, refusal.want) {
			t.Fatalf("%s = %v", refusal.code, err)
		}
	}
	site.set(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		problem(http.StatusTooManyRequests, "saga_transfer.busy")(w, r)
	})
	var busy domain.SiteBusy
	if _, _, err := client.TakeSagaTransfer(context.Background(), "code", "verifier"); !errors.As(err, &busy) || !busy.Saves || busy.Wait != time.Minute {
		t.Fatalf("saga_transfer.busy = %v", err)
	}
	site.set(account)
	if _, err := client.Account(context.Background()); err != nil {
		t.Fatalf("the account after the busy saves of a transfer = %v", err)
	}

	site.set(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		problem(http.StatusTooManyRequests, "rate_limit.exceeded")(w, r)
	})
	if _, _, err := client.TakeSagaTransfer(context.Background(), "code", "verifier"); !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("rate_limit.exceeded = %v", err)
	}
	asked := len(site.requests())
	if _, _, err := client.TakeSagaTransfer(context.Background(), "code", "verifier"); !errors.As(err, &busy) || busy.Saves || busy.Wait != 30*time.Second ||
		len(site.requests()) != asked {
		t.Fatalf("before the Retry-After = %v, %d requests", err, len(site.requests())-asked)
	}

	now = now.Add(time.Minute)
	site.set(problem(http.StatusUnauthorized, "app.pass_invalid"))
	if _, _, err := client.TakeSagaTransfer(context.Background(), "code", "verifier"); !errors.Is(err, domain.ErrPassInvalid) || client.Held() {
		t.Fatalf("a refused pass = %v, held %v", err, client.Held())
	}
}
