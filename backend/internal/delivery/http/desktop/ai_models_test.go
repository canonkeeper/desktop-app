// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/aisettings"
	service "canonkeeper/backend/internal/services/aisettings"

	"github.com/go-chi/chi/v5"
)

type listedModels struct {
	models domain.Models
	err    error
}

func (listed *listedModels) Execute(context.Context) (domain.Models, error) {
	return listed.models, listed.err
}

// specs/2026-11-13-desk-probes.md PB1.1, PB1.2: the window reads the models of its provider — the names, that it gives
// none, or the code of the reason — with its session alone, never kept by a cache; nothing to ask is ai.not_set.
func TestTheWindowListsTheModelsOfItsProvider(t *testing.T) {
	listed := &listedModels{models: domain.Models{Outcome: domain.ModelsListed, Names: []string{"north-cards", "north-chat"}}}
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		AI:       AI{Models: listed},
		CSRF:     func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)

	for _, check := range []struct {
		models domain.Models
		want   map[string]any
	}{
		{listed.models, map[string]any{"outcome": "listed", "models": []any{"north-cards", "north-chat"}, "reason": nil}},
		{domain.Models{Outcome: domain.ModelsUnlisted}, map[string]any{"outcome": "unlisted", "models": []any{}, "reason": nil}},
		{domain.Models{Outcome: domain.ModelsFailed, Reason: domain.ReasonKeyRefused}, map[string]any{"outcome": "failed", "models": []any{}, "reason": "key_refused"}},
	} {
		listed.models = check.models
		status, answer, cache := askTheWindow(router, http.MethodGet, "/desktop/ai/models", "", "cookie-of-the-window")
		if status != http.StatusOK || cache != "no-store" || !reflect.DeepEqual(answer, check.want) {
			t.Fatalf("listAiModels of %+v = %d %v (%q)", check.models, status, answer, cache)
		}
	}
	listed.err = service.ErrNotSet
	if status, answer, _ := askTheWindow(router, http.MethodGet, "/desktop/ai/models", "", "cookie-of-the-window"); status != http.StatusConflict || answer["code"] != "ai.not_set" {
		t.Fatalf("nothing to ask = %d %v", status, answer)
	}
	for _, cookie := range []string{"", "cookie-of-another-run"} {
		if status, answer, _ := askTheWindow(router, http.MethodGet, "/desktop/ai/models", "", cookie); status != http.StatusUnauthorized || answer["code"] != "auth.session_required" {
			t.Fatalf("listAiModels without the session = %d %v", status, answer)
		}
	}
}
