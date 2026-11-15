// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	domain "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/aisettings"
)

// Checks is «Проверить связь» (specs/2026-11-13-desk-probes.md PB2): one check of the process at a time.
type Checks interface {
	Start(context.Context) (service.Check, error)
	Read() service.Check
	Cancel()
}

// The codes of the refusals of a check.
const (
	codeNotConnected = "helper.not_connected"
	// codeNotForSource refuses «Проверить связь» and the list of the models of the helper of the account
	// (specs/2026-11-13-desk-account-ai.md AI1.6).
	codeNotForSource     = "helper.not_for_source"
	codeCheckRunning     = "ai.check_running"
	codeCheckUnavailable = "ai.check_unavailable"
)

type verdictJSON struct {
	Fit    string  `json:"fit"`
	Reason *string `json:"reason"`
}

type probeJSON struct {
	Probe  string  `json:"probe"`
	Role   string  `json:"role"`
	Model  string  `json:"model"`
	Passed bool    `json:"passed"`
	Reason *string `json:"reason"`
}

// reportJSON is AiCheckReport of the contract: no text of a probe or of an answer is in it.
type reportJSON struct {
	At          string      `json:"at"`
	ChatModel   string      `json:"chatModel"`
	CardsModel  string      `json:"cardsModel"`
	Probes      []probeJSON `json:"probes"`
	Calibration *float64    `json:"calibration"`
	Chat        verdictJSON `json:"chat"`
	Books       verdictJSON `json:"books"`
	Stale       bool        `json:"stale"`
}

type stepJSON struct {
	Probe string `json:"probe"`
	Role  string `json:"role"`
	Model string `json:"model"`
}

// checkJSON is AiCheck of the contract.
type checkJSON struct {
	State     string      `json:"state"`
	StartedAt *string     `json:"startedAt"`
	Current   *stepJSON   `json:"current"`
	Probes    []probeJSON `json:"probes"`
	Report    *reportJSON `json:"report"`
}

// aiCheck is getAiCheck: where the check of this run is, the probe that runs, the probes that went and its report.
func (handlers Handlers) aiCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	writeCheck(w, http.StatusOK, handlers.AI.Check.Read())
}

// startAiCheck starts a check of the saved settings and answers at once (PB2.1).
func (handlers Handlers) startAiCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	check, err := handlers.AI.Check.Start(r.Context())
	switch {
	case errors.Is(err, service.ErrNotForSource):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the helper of the account is not checked", codeNotForSource)
	case errors.Is(err, service.ErrNotConnected):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the helper is not connected", codeNotConnected)
	case errors.Is(err, service.ErrCheckRunning):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "a check runs", codeCheckRunning)
	case errors.Is(err, service.ErrStopped):
		httpapi.WriteProblem(w, "", http.StatusServiceUnavailable, "Service Unavailable", "the app stops", codeCheckUnavailable)
	case err != nil:
		unexpected(w, r, err)
	default:
		writeCheck(w, http.StatusAccepted, check)
	}
}

// cancelAiCheck stops the check that runs; it keeps nothing.
func (handlers Handlers) cancelAiCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	handlers.AI.Check.Cancel()
	w.WriteHeader(http.StatusNoContent)
}

func writeCheck(w http.ResponseWriter, status int, check service.Check) {
	response := checkJSON{State: string(check.State), Probes: probesJSON(check.Results), Report: reportOf(check.Report)}
	if !check.StartedAt.IsZero() {
		started := timeJSON(check.StartedAt)
		response.StartedAt = &started
	}
	if step := check.Current; step != nil {
		response.Current = &stepJSON{Probe: string(step.Probe), Role: string(step.Role), Model: step.Model}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func probesJSON(results []domain.Result) []probeJSON {
	probes := make([]probeJSON, 0, len(results))
	for _, result := range results {
		probes = append(probes, probeJSON{
			Probe: string(result.Probe), Role: string(result.Role), Model: result.Model, Passed: result.Passed, Reason: reasonJSON(result.Reason),
		})
	}
	return probes
}

// reportOf is the report with its verdicts now, null without one.
func reportOf(verdicts *service.Verdicts) *reportJSON {
	if verdicts == nil {
		return nil
	}
	report := verdicts.Report
	return &reportJSON{
		At: timeJSON(report.At), ChatModel: report.Checked.ChatModel, CardsModel: report.Checked.CardsModel,
		Probes: probesJSON(report.Results), Calibration: report.Calibration,
		Chat:  verdictJSON{Fit: string(verdicts.Chat.Fit), Reason: reasonJSON(verdicts.Chat.Reason)},
		Books: verdictJSON{Fit: string(verdicts.Books.Fit), Reason: reasonJSON(verdicts.Books.Reason)},
		Stale: verdicts.Stale,
	}
}

func timeJSON(at time.Time) string {
	return at.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}
