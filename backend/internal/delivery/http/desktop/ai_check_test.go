// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/aisettings"
	service "canonkeeper/backend/internal/services/aisettings"

	"github.com/go-chi/chi/v5"
)

// checksOfTheTest answer as a check of the process would.
type checksOfTheTest struct {
	check    service.Check
	err      error
	canceled int
}

func (checks *checksOfTheTest) Start(context.Context) (service.Check, error) {
	return checks.check, checks.err
}
func (checks *checksOfTheTest) Read() service.Check { return checks.check }
func (checks *checksOfTheTest) Cancel()             { checks.canceled++ }

type reportsOfTheTest struct{ report *domain.Report }

func (reports reportsOfTheTest) Report() (domain.Report, bool) {
	if reports.report == nil {
		return domain.Report{}, false
	}
	return *reports.report, true
}

// specs/2026-11-13-desk-probes.md PB2.1, PB2.4–PB2.6: the window starts a check and is answered at once, reads where it is
// and stops it; the probes and the verdicts are codes and models, never a text; getAiSettings carries the last report and
// whether it is stale; the refusals have their codes; without the session, the 401 of the site.
func TestTheWindowChecksItsOwnAI(t *testing.T) {
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	calibration := 312.4
	report := domain.Report{
		At: at, Checked: domain.Checked{BaseURL: "https://gateway.example.test/v1", Protocol: domain.ProtocolChat, ChatModel: "north", CardsModel: "north"},
		Calibration: &calibration,
		Results: []domain.Result{
			{Probe: domain.ProbeShort, Role: domain.RoleChat, Model: "north", Passed: true},
			{Probe: domain.ProbeTool, Role: domain.RoleChat, Model: "north", Reason: domain.ReasonNoTools},
		},
	}
	checks := &checksOfTheTest{check: service.Check{State: service.CheckRunning, StartedAt: at, Current: &domain.Step{Probe: domain.ProbeTool, Role: domain.RoleChat, Model: "north"},
		Results: report.Results[:1]}}
	own := domain.Own{BaseURL: report.Checked.BaseURL, Protocol: domain.ProtocolChat, ChatModel: "north", CardsModel: "north", Advanced: domain.Advanced{ContextWindow: 100_000, MaxOutput: 16_000}}
	settings := &settingsOfTheTest{settings: domain.Settings{Source: domain.SourceOwn, Own: &own}}
	keys := &keysOfTheTest{values: map[string]string{}}
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		AI:       AI{Read: service.Read{Settings: settings, Keys: keys, Reports: reportsOfTheTest{report: &report}}, Check: checks},
		CSRF:     func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)

	status, answer, cache := askTheWindow(router, http.MethodPost, "/desktop/ai/check", "", "cookie-of-the-window")
	want := map[string]any{
		"state": "running", "startedAt": "2026-11-13T09:30:00.000Z", "current": map[string]any{"probe": "tool", "role": "chat", "model": "north"},
		"probes": []any{map[string]any{"probe": "short", "role": "chat", "model": "north", "passed": true, "reason": nil}}, "report": nil,
	}
	if status != http.StatusAccepted || cache != "no-store" || !reflect.DeepEqual(answer, want) {
		t.Fatalf("startAiCheck = %d %v", status, answer)
	}

	checks.check = service.Check{State: service.CheckDone, StartedAt: at, Results: report.Results, Report: &service.Verdicts{
		Report: report, Chat: domain.Verdict{Fit: domain.FitNo, Reason: domain.ReasonNoTools}, Books: domain.Verdict{Fit: domain.FitNo, Reason: domain.ReasonOther},
	}}
	status, answer, cache = askTheWindow(router, http.MethodGet, "/desktop/ai/check", "", "cookie-of-the-window")
	probes := []any{
		map[string]any{"probe": "short", "role": "chat", "model": "north", "passed": true, "reason": nil},
		map[string]any{"probe": "tool", "role": "chat", "model": "north", "passed": false, "reason": "no_tools"},
	}
	wantReport := map[string]any{
		"at": "2026-11-13T09:30:00.000Z", "chatModel": "north", "cardsModel": "north", "probes": probes, "calibration": 312.4,
		"chat": map[string]any{"fit": "unfit", "reason": "no_tools"}, "books": map[string]any{"fit": "unfit", "reason": "other"}, "stale": false,
	}
	if want := (map[string]any{"state": "done", "startedAt": "2026-11-13T09:30:00.000Z", "current": nil, "probes": probes, "report": wantReport}); status != http.StatusOK ||
		cache != "no-store" || !reflect.DeepEqual(answer, want) {
		t.Fatalf("getAiCheck = %d %v", status, answer)
	}

	// getAiSettings: the kept report with its verdicts by the settings now — the probes failed, so the window does not matter.
	status, answer, _ = askTheWindow(router, http.MethodGet, "/desktop/ai", "", "cookie-of-the-window")
	if got, _ := answer["check"].(map[string]any); status != http.StatusOK || got["stale"] != false || !reflect.DeepEqual(got["chat"], map[string]any{"fit": "unfit", "reason": "no_tools"}) ||
		!reflect.DeepEqual(got["books"], map[string]any{"fit": "unfit", "reason": "other"}) {
		t.Fatalf("getAiSettings = %d %v", status, answer)
	}
	own.ChatModel = "north-2"
	if _, answer, _ = askTheWindow(router, http.MethodGet, "/desktop/ai", "", "cookie-of-the-window"); answer["check"].(map[string]any)["stale"] != true {
		t.Fatalf("getAiSettings after a new model = %v", answer["check"])
	}
	if encoded, _ := json.Marshal(answer); strings.Contains(string(encoded), "Проверка") || strings.Contains(string(encoded), "Готово") {
		t.Fatal("an answer holds a text of a probe")
	}

	for err, code := range map[error]string{service.ErrNotConnected: "helper.not_connected", service.ErrCheckRunning: "ai.check_running"} {
		checks.err = err
		if status, answer, _ = askTheWindow(router, http.MethodPost, "/desktop/ai/check", "", "cookie-of-the-window"); status != http.StatusConflict || answer["code"] != code {
			t.Fatalf("%v = %d %v", err, status, answer)
		}
	}
	checks.err = service.ErrStopped
	if status, answer, _ = askTheWindow(router, http.MethodPost, "/desktop/ai/check", "", "cookie-of-the-window"); status != http.StatusServiceUnavailable || answer["code"] != "ai.check_unavailable" {
		t.Fatalf("a check as the app stops = %d %v", status, answer)
	}
	if status, _, cache = askTheWindow(router, http.MethodDelete, "/desktop/ai/check", "", "cookie-of-the-window"); status != http.StatusNoContent || cache != "no-store" || checks.canceled != 1 {
		t.Fatalf("cancelAiCheck = %d, canceled %d", status, checks.canceled)
	}

	checks.check = service.Check{State: service.CheckIdle}
	if status, answer, _ = askTheWindow(router, http.MethodGet, "/desktop/ai/check", "", "cookie-of-the-window"); status != http.StatusOK ||
		!reflect.DeepEqual(answer, map[string]any{"state": "idle", "startedAt": nil, "current": nil, "probes": []any{}, "report": nil}) {
		t.Fatalf("getAiCheck before a check = %d %v", status, answer)
	}
	for _, request := range []struct{ method string }{{http.MethodGet}, {http.MethodPost}, {http.MethodDelete}} {
		for _, cookie := range []string{"", "cookie-of-another-run"} {
			if status, answer, _ = askTheWindow(router, request.method, "/desktop/ai/check", "", cookie); status != http.StatusUnauthorized || answer["code"] != "auth.session_required" {
				t.Fatalf("%s without the session = %d %v", request.method, status, answer)
			}
		}
	}
	if checks.canceled != 1 {
		t.Fatal("a request without the session stopped the check")
	}
}
