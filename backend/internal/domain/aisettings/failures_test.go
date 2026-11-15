// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import "testing"

// specs/2026-11-15-desk-reading-no-answer.md NA1.1: once the provider began its answer, a stream torn by the connection and a
// refusal said mid-answer are no answer — a failure of the provider; the funds, the key, the model and a wait keep their kind.
func TestAFailureMidAnswerIsNoAnswer(t *testing.T) {
	for failure, want := range map[Failure]Failure{
		FailureUnreachable: FailureOther, FailureRefused: FailureOther, FailureOther: FailureOther,
		FailureFunds: FailureFunds, FailureKey: FailureKey, FailureModel: FailureModel, FailureRate: FailureRate,
	} {
		if got := failure.MidAnswer(); got != want {
			t.Errorf("%s mid-answer = %s, want %s", failure, got, want)
		}
	}
}

// specs/2026-11-15-desk-reading-no-answer.md NA2.1, specs/2026-11-13-desk-consent.md CN3.4: the one table of the refusals of
// the provider. A refusal of the request — refused, which marks its chapter — is a 4xx the same request repeated gets again:
// 400 (a chapter longer than the window of the model among them), 413, 422 and the rare others; a refusal of a state that
// passes — a timeout (408), a conflict (409), a lock (423), too early (425) — is no refusal of the request but a failure the
// client repeats, no answer on its last attempt.
func TestTheTableOfTheRefusals(t *testing.T) {
	for _, row := range []struct {
		status     int
		code, text string
		connection bool
		want       Failure
	}{
		{0, "connection_error", "", true, FailureUnreachable},
		{400, "invalid_request_error", "bad request", false, FailureRefused},
		{400, "context_length_exceeded", "This model's maximum context length is 8192 tokens", false, FailureRefused},
		{413, "request_too_large", "too large", false, FailureRefused},
		{422, "", "unprocessable", false, FailureRefused},
		{406, "", "", false, FailureRefused},
		{410, "", "", false, FailureRefused},
		{421, "", "", false, FailureRefused},
		{401, "", "", false, FailureKey},
		{403, "", "", false, FailureKey},
		{402, "", "", false, FailureFunds},
		{429, "insufficient_quota", "", false, FailureFunds},
		{429, "rate_limit_exceeded", "", false, FailureRate},
		{404, "model_not_found", "The model north does not exist", false, FailureModel},
		{400, "", "invalid model: north", false, FailureModel},
		{404, "", "no such path", false, FailureOther},
		{405, "", "", false, FailureOther},
		{408, "", "request timeout", false, FailureOther},
		{409, "", "conflict", false, FailureOther},
		{423, "", "locked", false, FailureOther},
		{425, "", "too early", false, FailureOther},
		{500, "server_error", "", false, FailureOther},
		{503, "", "", false, FailureOther},
	} {
		if got := Classify(row.status, row.code, row.text, row.connection); got != row.want {
			t.Errorf("Classify(%d, %q, %q) = %s, want %s", row.status, row.code, row.text, got, row.want)
		}
	}
}
