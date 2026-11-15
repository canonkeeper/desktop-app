// SPDX-License-Identifier: AGPL-3.0-only

package deletion

import (
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	journalrepo "canonkeeper/backend/internal/repository/postgres/modeljournal"
	readingrepo "canonkeeper/backend/internal/repository/postgres/readingconsent"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	readingservice "canonkeeper/backend/internal/services/readingconsent"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-07-desktop-app.md, DA15.1; specs/2026-11-13-desk-consent.md CN3.1: the project purge removes the consent of
// the reading of the saga with the project row.
func TestProjectHardPurgeRemovesTheDesktopRowsOfTheSaga(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_project_purge_desktop")
	ctx := t.Context()
	const (
		userID    = "10000000-0000-4000-8000-000000000031"
		projectID = "10000000-0000-4000-8000-000000000032"
		tombstone = "10000000-0000-4000-8000-000000000034"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, 'desktop-purge@example.test', 'Author', 'hash', clock_timestamp())
	`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($2, $1, 'Desktop Project', '', clock_timestamp(), clock_timestamp())
	`, userID, projectID); err != nil {
		t.Fatal(err)
	}
	consents := readingrepo.New(db)
	if _, err := consents.ChangeConsent(ctx, projectID, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, domain.SourceOwn, "api.deepseek.com", domain.UnitTokens)
	}, false); err != nil {
		t.Fatal(err)
	}
	if consent, err := consents.Consent(ctx, projectID); err != nil || consent == nil || consent.State != domain.StateAllowed {
		t.Fatalf("the consent before the purge = %+v, %v", consent, err)
	}
	// specs/2026-11-15-desk-reading-rest.md RR1.6: the names that did not come together go with the saga.
	if err := consents.MarkRest(ctx, projectID, readingservice.RestMark{
		Kind: readingservice.RestNames, View: "published", Names: "set-of-names", Reason: "unreadable", Helper: strings.Repeat("a", 64),
	}, now); err != nil {
		t.Fatal(err)
	}
	if marks, err := consents.RestMarks(ctx, projectID); err != nil || len(marks) != 1 {
		t.Fatalf("the marks before the purge = %+v, %v", marks, err)
	}

	outcome := purgeProjectForTest(t, db, projectID, tombstone, now)
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM card_build_permissions WHERE project_id=$1`, projectID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	marks, err := consents.RestMarks(ctx, projectID)
	if outcome != deletionservice.PostgresStepSucceeded || left != 0 || err != nil || len(marks) != 0 {
		t.Fatalf("the project purge = outcome:%s consents left:%d marks left:%d %v", outcome, left, len(marks), err)
	}
}

// specs/2026-11-14-desk-journal.md JR2.7: the purge of a saga takes the records of its sendings, what its threads named and have
// not sent, the helpers of its threads and the requests of its samples; a check of the connection, which has no saga, stays.
func TestProjectHardPurgeRemovesTheJournalOfTheSaga(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_project_purge_journal")
	ctx := t.Context()
	const (
		userID    = "10000000-0000-4000-8000-000000000041"
		projectID = "10000000-0000-4000-8000-000000000042"
		jobID     = "10000000-0000-4000-8000-000000000043"
		tombstone = "10000000-0000-4000-8000-000000000044"
		checkID   = "10000000-0000-4000-8000-000000000045"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, statement := range []struct {
		sql       string
		arguments []any
	}{
		{`INSERT INTO users (id, email, display_name, password_hash, email_verified_at) VALUES ($1, 'journal-purge@example.test', 'Author', 'hash', clock_timestamp())`, []any{userID}},
		{`INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at) VALUES ($2, $1, 'Journal Project', '', clock_timestamp(), clock_timestamp())`, []any{userID, projectID}},
		// A record of the saga and a helper of its thread as the journal writes them under a Job of the saga, whose rows the
		// purge removes first.
		{`INSERT INTO model_disclosures (id, at, project_id, job_id, thread_id, purpose, profile_name, provider_host, request_bytes, named, first_time)
		  VALUES (gen_random_uuid(), clock_timestamp(), $1, $2, 'thread-of-the-purge', 'chat', 'own', 'provider.example.test', 10, '{}', false)`, []any{projectID, jobID}},
		{`INSERT INTO model_thread_recipients (thread_id, profile_name, provider_host, project_id, at)
		  VALUES ('thread-of-the-purge', 'own', 'provider.example.test', $1, clock_timestamp())`, []any{projectID}},
	} {
		if _, err := db.Pool.Exec(ctx, statement.sql, statement.arguments...); err != nil {
			t.Fatal(err)
		}
	}
	journal := journalrepo.New(db)
	if err := journal.Send(ctx, modeljournal.Sending{ID: modeljournal.NewID(), At: now, CheckID: checkID, Purpose: modeljournal.PurposeProbe, Profile: "own", Host: "provider.example.test", Bytes: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Name(ctx, "thread-of-the-purge", projectID, disclosure.Named{Question: true}); err != nil {
		t.Fatal(err)
	}
	if err := readingrepo.New(db).SettleSample(ctx, projectID, modeljournal.Request{
		ID: modeljournal.NewID(), At: now, Purpose: "card", JobID: jobID, Profile: "own", ProviderHost: "provider.example.test", Model: "north", Outcome: modeljournal.OutcomeCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	outcome := purgeProjectForTest(t, db, projectID, tombstone, now)
	var records, checks, pending, recipients, samples int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM model_disclosures), (SELECT count(*) FROM model_disclosures WHERE check_id IS NOT NULL),
		(SELECT count(*) FROM model_disclosures_pending), (SELECT count(*) FROM model_thread_recipients), (SELECT count(*) FROM reading_sample_requests)`).Scan(
		&records, &checks, &pending, &recipients, &samples); err != nil {
		t.Fatal(err)
	}
	if outcome != deletionservice.PostgresStepSucceeded || records != 1 || checks != 1 || pending != 0 || recipients != 0 || samples != 0 {
		t.Fatalf("after the purge %s: records %d, checks %d, pending %d, recipients %d, samples %d", outcome, records, checks, pending, recipients, samples)
	}
}
