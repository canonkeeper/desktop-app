package app

import (
	"io"
	"log"
	"testing"

	"canonkeeper/backend/internal/config"
)

// specs/2026-11-17-model-cache.md MC4.1, MC4.2: the client of the models of the site waits out a refusal of the pool of
// codex-lb for its own calls, journals the waits and knows the key of the reading; the client of the app — which goes to its
// own gateway (CarryJob) — waits out nothing.
func TestTheClientOfTheSiteWaitsOutThePoolAndTheClientOfTheAppDoesNot(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	site := processModel(config.ModelAPI{BaseURL: "https://models.example.test", KeyEnv: "CODEX_LB_API_KEY",
		ReadingKeyEnv: "CODEX_LB_CANONKEEPER_READING_KEY", Model: "gpt-north"}, logger)
	if !site.WaitsPool || site.Logger != logger || site.ReadingKeyEnv != "CODEX_LB_CANONKEEPER_READING_KEY" || site.CarryJob {
		t.Fatalf("the client of the site = %+v", site)
	}
	desktop := processModel(config.ModelAPI{BaseURL: "http://127.0.0.1:4100/v1", Key: "key-test-gateway-0001", CarryJob: true,
		Stops: true, Model: "canonkeeper-chat"}, logger)
	if desktop.WaitsPool || !desktop.CarryJob || !desktop.Stops || desktop.Key != "key-test-gateway-0001" {
		t.Fatalf("the client of the app = %+v", desktop)
	}
}
