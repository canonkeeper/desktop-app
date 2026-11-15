// SPDX-License-Identifier: AGPL-3.0-only

package siteapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/appwork"
)

const permitOfTheTest = "4b1c0f2e-6c8f-4f6e-9a65-2f1d3c4b5a69"

func permitAnswer(state, outcome string, returned bool) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": permitOfTheTest, "kind": "chat_turn", "state": state, "outcome": outcome, "day": "2026-11-13",
			"validUntil": "2026-11-13T12:05:00Z", "returned": returned, "newer": 1,
		})
	}
}

func gunzipped(t *testing.T, body string) string {
	t.Helper()
	reader, err := gzip.NewReader(strings.NewReader(body))
	if err != nil {
		t.Fatalf("the body is not gzip: %v", err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(plain)
}

// specs/2026-11-13-desk-account-ai.md AI2.1, AI2.7, AI2.8: the operations of the permits go with the pass held to the
// paths of the contract, without a cookie, an Origin or a query, and read the permit with fields this app does not know.
func TestThePermitsGoToTheSiteWithThePass(t *testing.T) {
	site := newStub(t, permitAnswer("open", "", false))
	client := trusted(site, time.Now)
	client.Hold(passOfTheTest)
	permit, err := client.OpenPermit(context.Background(), appwork.KindTurn, "ref-north")
	if err != nil || permit.ID != permitOfTheTest || !permit.Live() || permit.Kind != appwork.KindTurn {
		t.Fatalf("OpenPermit = %+v, %v", permit, err)
	}
	site.set(permitAnswer("closed", "failed", true))
	closed, err := client.ClosePermit(context.Background(), permitOfTheTest, appwork.OutcomeFailed)
	if err != nil || closed.Live() || !closed.Returned || closed.Outcome != appwork.OutcomeFailed {
		t.Fatalf("ClosePermit = %+v, %v", closed, err)
	}
	site.set(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"day":"2026-11-13","returned":true}`)
	})
	if err := client.ChargeUnit(context.Background(), permitOfTheTest, "steer-north"); err != nil {
		t.Fatalf("ChargeUnit = %v", err)
	}
	if returned, err := client.ReturnUnit(context.Background(), permitOfTheTest, "steer-north"); err != nil || !returned {
		t.Fatalf("ReturnUnit = %v, %v", returned, err)
	}
	requests := site.requests()
	want := []string{
		"POST /api/v1/app/permits", "POST /api/v1/app/permits/" + permitOfTheTest + "/close",
		"POST /api/v1/app/permits/" + permitOfTheTest + "/units", "DELETE /api/v1/app/permits/" + permitOfTheTest + "/units/steer-north",
	}
	for index, request := range requests {
		if request.Method+" "+request.URL.Path != want[index] || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "Bearer "+passOfTheTest ||
			request.Header.Get("Origin") != "" || request.Header.Get("Cookie") != "" {
			t.Fatalf("request %d = %s %s %v", index, request.Method, request.URL, request.Header)
		}
	}
	if site.bodies[0] != `{"kind":"chat_turn","ref":"ref-north"}` || site.bodies[1] != `{"outcome":"failed"}` || site.bodies[2] != `{"ref":"steer-north"}` {
		t.Fatalf("the bodies = %q", site.bodies)
	}
}

// AI3.3 (the correction of the rule of the link): the site's 429 is read by its code — a refusal of the tier and ai.busy
// are the answer of their operation and the other requests go at once; only rate_limit.exceeded, 5xx and a broken
// connection keep the client from the site until their Retry-After. A refusal of the pass drops it and tells the link.
func TestA429IsReadByItsCode(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	site := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "40000")
		problem(http.StatusTooManyRequests, "quota.chat_turns_daily")(w, r)
	})
	client := trusted(site, func() time.Time { return now })
	client.Hold(passOfTheTest)
	_, err := client.OpenPermit(context.Background(), appwork.KindTurn, "ref-north")
	refusal, ok := appwork.AsRefusal(err)
	if !ok || refusal.Status != http.StatusTooManyRequests || refusal.Code != "quota.chat_turns_daily" || refusal.RetryAfter != 40000*time.Second {
		t.Fatalf("a refusal of the tier = %v", err)
	}
	site.set(account)
	if _, err := client.Account(context.Background()); err != nil {
		t.Fatalf("the account after a refusal of the tier = %v", err)
	}
	site.set(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		problem(http.StatusTooManyRequests, "ai.busy")(w, r)
	})
	if _, err := client.Step(context.Background(), "card", permitOfTheTest, []byte(`{"instruction":"w","input":"i"}`)); err == nil {
		t.Fatal("ai.busy is no refusal")
	} else if refusal, ok := appwork.AsRefusal(err); !ok || refusal.Code != "ai.busy" || refusal.RetryAfter != time.Second {
		t.Fatalf("ai.busy = %v", err)
	}
	site.set(account)
	if _, err := client.Account(context.Background()); err != nil {
		t.Fatalf("the account after ai.busy = %v", err)
	}
	site.set(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		problem(http.StatusTooManyRequests, "rate_limit.exceeded")(w, r)
	})
	if _, err := client.OpenPermit(context.Background(), appwork.KindTurn, "ref-north"); !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("rate_limit.exceeded = %v", err)
	}
	asked := len(site.requests())
	site.set(permitAnswer("open", "", false))
	if _, err := client.OpenPermit(context.Background(), appwork.KindTurn, "ref-north"); !errors.Is(err, domain.ErrUnreachable) || len(site.requests()) != asked {
		t.Fatalf("before the Retry-After of rate_limit.exceeded = %v, %d requests", err, len(site.requests())-asked)
	}
	now = now.Add(31 * time.Second)
	if _, err := client.OpenPermit(context.Background(), appwork.KindTurn, "ref-north"); err != nil {
		t.Fatalf("after the Retry-After = %v", err)
	}
	dropped := 0
	client.WhenDropped(func() { dropped++ })
	site.set(problem(http.StatusUnauthorized, "app.pass_invalid"))
	if _, err := client.ClosePermit(context.Background(), permitOfTheTest, appwork.OutcomeAnswered); !errors.Is(err, domain.ErrPassInvalid) || client.Held() || dropped != 1 {
		t.Fatalf("a refused pass = %v, held %v, the link told %d", err, client.Held(), dropped)
	}
	if _, err := client.OpenPermit(context.Background(), appwork.KindTurn, "ref-north"); !errors.Is(err, domain.ErrNotHeld) {
		t.Fatalf("without a pass = %v", err)
	}
}

// AI2.4, AI3.1: the stream of a turn and a step send only the pass, the permit, the kind and the compression of the body and
// what they accept, the body compressed by gzip; their answers come in the term of their operation, not of the short
// operations — the headers of a step come after the whole call of the model.
func TestTheStreamAndTheStepWaitInTheTermOfTheirOperation(t *testing.T) {
	site := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		if strings.HasSuffix(r.URL.Path, "/responses") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"{\"ok\":true}","model":"gpt-north-cards","usage":{"calls":1,"inputTokens":300,"cachedInputTokens":10,"outputTokens":20,"reasoningOutputTokens":5},"later":1}`)
	})
	client := trusted(site, time.Now)
	client.headersTime(50 * time.Millisecond)
	client.Hold(passOfTheTest)
	if _, err := client.Account(context.Background()); !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("a short operation past its term of the headers = %v", err)
	}
	stream, err := client.Responses(context.Background(), permitOfTheTest, []byte(`{"input":"вопрос","model":"canonkeeper-chat"}`))
	if err != nil {
		t.Fatalf("Responses = %v", err)
	}
	events, _ := io.ReadAll(stream)
	_ = stream.Close()
	if !bytes.Contains(events, []byte("response.completed")) {
		t.Fatalf("the stream = %q", events)
	}
	answer, err := client.Step(context.Background(), "card", permitOfTheTest, []byte(`{"instruction":"w","input":"i"}`))
	if err != nil || answer.Text != `{"ok":true}` || answer.Model != "gpt-north-cards" || answer.Usage.InputTokens != 300 ||
		answer.Usage.CachedInputTokens != 10 || answer.Usage.OutputTokens != 20 || answer.Usage.ReasoningOutputTokens != 5 {
		t.Fatalf("Step = %+v, %v", answer, err)
	}
	requests := site.requests()
	allowed := map[string]bool{
		"Authorization": true, "X-Canonkeeper-Permit": true, "Content-Type": true, "Content-Encoding": true, "Accept": true,
		"User-Agent": true, "Content-Length": true, "Accept-Encoding": true,
	}
	for _, request := range requests[1:] {
		for name := range request.Header {
			if !allowed[name] {
				t.Fatalf("%s carries the header %s", request.URL.Path, name)
			}
		}
		if request.Header.Get("X-CanonKeeper-Permit") != permitOfTheTest || request.Header.Get("Content-Encoding") != "gzip" ||
			request.URL.RawQuery != "" || request.Header.Get("Origin") != "" {
			t.Fatalf("%s = %v", request.URL.Path, request.Header)
		}
	}
	if requests[1].URL.Path != "/api/v1/app/ai/responses" || requests[1].Header.Get("Accept") != "text/event-stream" ||
		gunzipped(t, site.bodies[1]) != `{"input":"вопрос","model":"canonkeeper-chat"}` {
		t.Fatalf("the stream asked %s %v", requests[1].URL.Path, requests[1].Header)
	}
	if requests[2].URL.Path != "/api/v1/app/ai/steps/card" || gunzipped(t, site.bodies[2]) != `{"instruction":"w","input":"i"}` {
		t.Fatalf("the step asked %s", requests[2].URL.Path)
	}
}

// AI2.3: the mark of a link is the same for the same pass and salt, another for a new pass, none without a pass, and never
// holds the pass.
func TestTheMarkOfALinkNamesItsPassWithoutHoldingIt(t *testing.T) {
	client, err := New("https://site.example.test")
	if err != nil {
		t.Fatal(err)
	}
	salt := []byte("salt-of-the-test")
	if client.LinkMark(salt) != "" {
		t.Fatal("a client without a pass has a mark")
	}
	first := "ckd_" + strings.Repeat("a", 43)
	client.Hold(first)
	mark := client.LinkMark(salt)
	if len(mark) != 64 || strings.Contains(mark, strings.Repeat("a", 43)) || client.LinkMark(salt) != mark {
		t.Fatalf("the mark of a link = %q", mark)
	}
	if client.LinkMark([]byte("another salt")) == mark {
		t.Fatal("the mark does not hang on the salt")
	}
	client.Hold("ckd_" + strings.Repeat("b", 43))
	if client.LinkMark(salt) == mark {
		t.Fatal("a new link has the mark of the old one")
	}
}
