// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/modeljournal"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-13-desk-gateway.md GW1.7, migration 0147: the table takes every purpose of the one list — the chat, the
// probe and each step of the core — and no other; a request without usage keeps its tokens unknown, a Job nobody knows
// leaves the request without a saga, and a probe never has one.
func TestTheJournalTakesThePurposesOfTheOneList(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_model_journal")
	ctx := context.Background()
	journal := New(db)
	at := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for _, purpose := range domain.Purposes() {
		request := domain.Request{
			At: at, Purpose: purpose, JobID: "5d0c7a1e-0000-4000-8000-000000000001", Profile: "own", ProviderHost: "api.example.test",
			Model: "north-cards", Tokens: &domain.Tokens{Input: 10, CachedInput: 4, Output: 3}, RequestBytes: 1200,
			Outcome: domain.OutcomeCompleted, Duration: 1500 * time.Millisecond,
		}
		if purpose == domain.PurposeProbe {
			request.JobID, request.Tokens = "", nil
		}
		if err := journal.Record(ctx, request); err != nil {
			t.Fatalf("the purpose %s: %v", purpose, err)
		}
	}
	if err := journal.Record(ctx, domain.Request{At: at, Purpose: "chat_turn", Profile: "own", ProviderHost: "h", Model: "m", Outcome: domain.OutcomeFailed}); err == nil {
		t.Fatal("a purpose of no list was written")
	}
	var rows, sagas, jobs, cached, bytes, probes, unknown int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*), count(project_id), count(job_id), coalesce(sum(cached_input_tokens), 0), coalesce(sum(request_bytes), 0),
		  count(*) FILTER (WHERE purpose = 'probe'), count(*) FILTER (WHERE input_tokens IS NULL AND output_tokens IS NULL)
		FROM model_requests
	`).Scan(&rows, &sagas, &jobs, &cached, &bytes, &probes, &unknown); err != nil {
		t.Fatal(err)
	}
	if rows != len(domain.Purposes()) || sagas != 0 || jobs != 0 || probes != 1 || unknown != 1 ||
		cached != 4*(rows-1) || bytes != 1200*rows {
		t.Fatalf("rows %d, sagas %d, jobs %d, cached %d, bytes %d, probes %d, unknown tokens %d", rows, sagas, jobs, cached, bytes, probes, unknown)
	}
}

// specs/2026-11-13-desk-account-ai.md AI1.7, AI4.2, migration 0151: a row of the profile account keeps the code of the refusal
// of the site — a code, never words —, and a row without one keeps none.
func TestARowOfTheAccountKeepsTheCodeOfTheRefusalOfTheSite(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_model_journal_refusal")
	ctx := context.Background()
	journal := New(db)
	at := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	row := domain.Request{At: at, Purpose: "chat", Profile: "account", ProviderHost: "site.example.test", Model: "site-chat", Outcome: domain.OutcomeFailed}
	for _, code := range []string{"ai.daily_fence", "quota.account_characters", ""} {
		row.RefusalCode = code
		if err := journal.Record(ctx, row); err != nil {
			t.Fatalf("%q: %v", code, err)
		}
	}
	row.RefusalCode = "the site refused: words"
	if err := journal.Record(ctx, row); err == nil {
		t.Fatal("words were kept as a code")
	}
	var codes, none int
	if err := db.Pool.QueryRow(ctx, `SELECT count(refusal_code), count(*) FILTER (WHERE refusal_code IS NULL) FROM model_requests`).Scan(&codes, &none); err != nil {
		t.Fatal(err)
	}
	if codes != 2 || none != 1 {
		t.Fatalf("codes %d, none %d", codes, none)
	}
}
