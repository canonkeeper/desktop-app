// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"regexp"
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	domain "canonkeeper/backend/internal/domain/modeljournal"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

const (
	journalAuthor  = "95000000-0000-4000-8000-000000000001"
	journalSaga    = "95000000-0000-4000-8000-000000000002"
	journalJob     = "95000000-0000-4000-8000-000000000003"
	journalChapter = "95000000-0000-4000-8000-000000000004"
	journalCheck   = "95000000-0000-4000-8000-000000000005"
)

// seedJournalSaga is a saga with one Job of the made-up «Северный предел».
func seedJournalSaga(t *testing.T, db *storedb.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1::uuid, $1::text || '@example.test', 'A', 'hash', now())`, journalAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($1, $2, 'Северный предел', '', now(), now())`, journalSaga, journalAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO jobs (id, project_id, kind, status, progress_json, result_json, error_json, governance_payload_state,
		  subject_json, creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, 'import', 'expired', '{}', NULL, NULL, 'source_deleted', NULL, NULL, false, 'user_demanded', 0, 3,
		  clock_timestamp(), clock_timestamp(), clock_timestamp())`, journalJob, journalSaga); err != nil {
		t.Fatal(err)
	}
}

// purposesOfCheck are the purposes the check of a table names.
func purposesOfCheck(t *testing.T, db *storedb.DB, constraint string) []string {
	t.Helper()
	var definition string
	if err := db.Pool.QueryRow(context.Background(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1`, constraint).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	var purposes []string
	for _, match := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(definition, -1) {
		purposes = append(purposes, match[1])
	}
	slices.Sort(purposes)
	return purposes
}

// specs/2026-11-14-desk-journal.md JR1.8: the check of the records is the list of the purposes of a sending, the check of the
// rows of the requests the list of the purposes of a request — the one list of the domain each.
func TestTheChecksOfTheJournalAreTheListsOfTheDomain(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_purposes")
	sends, requests := domain.SendPurposes(), domain.Purposes()
	slices.Sort(sends)
	slices.Sort(requests)
	if got := purposesOfCheck(t, db, "model_disclosures_purpose_check"); !slices.Equal(got, sends) {
		t.Fatalf("the check of the records = %v, the domain %v", got, sends)
	}
	if got := purposesOfCheck(t, db, "model_requests_purpose_check"); !slices.Equal(got, requests) {
		t.Fatalf("the check of the requests = %v, the domain %v", got, requests)
	}
}

// JR1.1, JR1.4, JR1.5: a record lies with the saga of its Job and takes what the tools of its thread named; a record that
// did not lie leaves it for the next one; the first sending of a thread to a helper it never went to while it went to
// another carries «впервые», the next one does not, and a probe has its check and no saga.
func TestARecordTakesWhatItsThreadNamed(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_records")
	seedJournalSaga(t, db)
	journal := New(db)
	ctx := context.Background()
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var read disclosure.Named
	read.Chapter(journalChapter, disclosure.Paragraphs, 3, 9)
	if _, err := journal.Name(ctx, "thread-1", journalSaga, read); err != nil {
		t.Fatal(err)
	}
	refused := domain.Sending{ID: domain.NewID(), At: at, JobID: journalJob, ThreadID: "thread-1", Purpose: "nonsense", Profile: "own", Host: "provider.example.test", Bytes: 10}
	if err := journal.Send(ctx, refused); err == nil {
		t.Fatal("a record of no purpose lay")
	}
	sending := func(host string, named disclosure.Named) domain.Sending {
		return domain.Sending{ID: domain.NewID(), At: at, JobID: journalJob, ThreadID: "thread-1", Purpose: "chat", Profile: "own", Host: host, Bytes: 4096, Named: named}
	}
	first := sending("provider.example.test", disclosure.Named{Question: true})
	for _, record := range []domain.Sending{first, sending("provider.example.test", disclosure.Named{}), sending("other.example.test", disclosure.Named{}), sending("other.example.test", disclosure.Named{})} {
		if err := journal.Send(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	probe := domain.Sending{ID: domain.NewID(), At: at, CheckID: journalCheck, Purpose: domain.PurposeProbe, Profile: "own", Host: "provider.example.test", Bytes: 2}
	if err := journal.Send(ctx, probe); err != nil {
		t.Fatal(err)
	}
	if err := journal.Send(ctx, domain.Sending{ID: domain.NewID(), At: at, Purpose: domain.PurposeProbe, Profile: "own", Host: "h", Bytes: 2}); err == nil {
		t.Fatal("a probe without its check lay")
	}
	rows, err := db.Pool.Query(ctx, `SELECT coalesce(project_id::text, ''), named::text, first_time FROM model_disclosures ORDER BY provider_host DESC, check_id IS NOT NULL, id = $1 DESC, first_time DESC`, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		saga, named string
		first       bool
	}
	var got []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.saga, &item.named, &item.first); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("records = %+v", got)
	}
	if got[0].saga != journalSaga || got[0].named != `{"chapters": [{"p": "paragraphs", "r": [{"f": 3, "t": 9}], "id": "`+journalChapter+`"}], "question": true}` || got[0].first {
		t.Fatalf("the first record = %+v", got[0])
	}
	if got[1].named != `{}` || got[1].first {
		t.Fatalf("the second record = %+v", got[1])
	}
	if got[2].saga != "" || got[2].named != `{}` || got[2].first {
		t.Fatalf("the probe = %+v", got[2])
	}
	if !got[3].first || got[4].first {
		t.Fatalf("the records to the second helper = %+v, %+v", got[3], got[4])
	}
	var pending int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM model_disclosures_pending`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending %d, %v", pending, err)
	}
	// A correction Codex refused is taken back while no sending took it.
	id, err := journal.Name(ctx, "thread-1", journalSaga, disclosure.Named{Correction: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Withdraw(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM model_disclosures_pending`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending after the withdrawal %d, %v", pending, err)
	}
}
