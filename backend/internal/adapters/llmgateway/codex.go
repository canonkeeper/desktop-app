// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import "fmt"

// TokenVariable is the variable of the environment of the app-server that holds the token of the gateway (GW2.1, GW2.2):
// it is set there by value and nowhere else — never in the environment of the backend or of PostgreSQL.
const TokenVariable = "CANONKEEPER_GATEWAY_TOKEN"

// codexProvider is the name of the provider of Codex of the app. It never changes: a thread keeps it, and the gateway puts
// whatever profile the author has behind it (DT4.10). Not «OpenAI» by name, so Codex compacts the history itself through
// the same route.
const codexProvider = "canonkeeper"

// maxToolOutput bounds the output of one tool as the site does (scripts/codex-lb.mjs).
const maxToolOutput = 400_000

// CodexOverrides are the settings of the app-server of the chat of the app (GW2.1, DA5.5): the alias of the model of the
// chat on the provider canonkeeper of the gateway at baseURL, its key from TokenVariable, the window of the chat, its
// threshold of compaction — 90 % of the window of «Свой ИИ», the site's for the helper of the account
// (specs/2026-11-13-desk-account-ai.md AI1.3) — and the output of a tool bounded by a quarter of the window.
func CodexOverrides(baseURL string, contextWindow, compactLimit int) []string {
	return []string{
		fmt.Sprintf("model=%q", ChatModel),
		fmt.Sprintf("model_provider=%q", codexProvider),
		fmt.Sprintf("model_context_window=%d", contextWindow),
		fmt.Sprintf("model_auto_compact_token_limit=%d", compactLimit),
		fmt.Sprintf("tool_output_token_limit=%d", min(maxToolOutput, contextWindow/4)),
		fmt.Sprintf("model_providers.%s.name=%q", codexProvider, codexProvider),
		fmt.Sprintf("model_providers.%s.base_url=%q", codexProvider, baseURL),
		fmt.Sprintf("model_providers.%s.wire_api=%q", codexProvider, "responses"),
		fmt.Sprintf("model_providers.%s.env_key=%q", codexProvider, TokenVariable),
		fmt.Sprintf("model_providers.%s.requires_openai_auth=false", codexProvider),
		fmt.Sprintf("model_providers.%s.supports_websockets=false", codexProvider),
		// A provider rate limit fails the turn once Codex runs out of stream retries: its pause doubles from 0.2 s, so 10
		// retries ride out about 3.4 minutes, as the site's do.
		fmt.Sprintf("model_providers.%s.stream_max_retries=10", codexProvider),
	}
}
