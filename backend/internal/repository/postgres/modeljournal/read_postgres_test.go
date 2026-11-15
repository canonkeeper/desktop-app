// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	domain "canonkeeper/backend/internal/domain/modeljournal"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

const (
	readingJob = "95000000-0000-4000-8000-000000000011"
	otherSaga  = "95000000-0000-4000-8000-000000000012"
	otherJob   = "95000000-0000-4000-8000-000000000013"
	checkOne   = "95000000-0000-4000-8000-000000000014"
)

// seedJob is an ended Job of a saga: a live one needs the sources of its artifact, which a journal does not read.
func seedJob(t *testing.T, db *storedb.DB, jobID, projectID string) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO jobs (id, project_id, kind, status, progress_json, result_json, error_json, governance_payload_state,
		  subject_json, creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, 'import', 'expired', '{}', NULL, NULL, 'source_deleted', NULL, NULL, false, 'user_demanded', 0, 3,
		  clock_timestamp(), clock_timestamp(), clock_timestamp())`, jobID, projectID); err != nil {
		t.Fatal(err)
	}
}

func seedOtherSaga(t *testing.T, db *storedb.DB) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($1, $2, 'Переправа', '', now(), now())`, otherSaga, journalAuthor); err != nil {
		t.Fatal(err)
	}
}

func send(t *testing.T, journal *Journal, sending domain.Sending) string {
	t.Helper()
	if sending.ID == "" {
		sending.ID = domain.NewID()
	}
	if sending.Profile == "" {
		sending.Profile, sending.Host = "own", "provider.example.test"
	}
	if sending.Bytes == 0 {
		sending.Bytes = 1000
	}
	if err := journal.Send(context.Background(), sending); err != nil {
		t.Fatal(err)
	}
	return sending.ID
}

func utc(text string) time.Time {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(err)
	}
	return at
}

func allRows(t *testing.T, journal *Journal, kind domain.Kind, zone string, limit int) []domain.Row {
	t.Helper()
	var rows []domain.Row
	var after *domain.Cursor
	for {
		page, next, err := journal.Rows(context.Background(), domain.RowsQuery{Kind: kind, Zone: zone, After: after, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, page...)
		if next == nil {
			return rows
		}
		after = next
	}
}

// specs/2026-11-14-desk-journal.md JR2.1, JR2.2: a row is a work within a day of the zone of the author — a reading across
// midnight is two rows in New York and one in Moscow, a day of the change of the time has its 25 hours —, the kinds choose
// their rows, the pages go by the cursor without a row twice or missed, a check is one row, the helpers of a row are all
// named, «Свой ИИ» counts its requests and its costs in each currency, and a Job of 600 chapters a day is one row whose
// words stay short.
func TestTheRowsOfTheJournalAreWorksByDays(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_rows")
	seedJournalSaga(t, db)
	seedOtherSaga(t, db)
	seedJob(t, db, readingJob, journalSaga)
	seedJob(t, db, otherJob, otherSaga)
	journal := New(db)
	ctx := context.Background()
	// A reading across the midnight of New York: 23:30 and 01:30 of the change to the winter time; 23:30 of the 25-hour day.
	for _, at := range []string{"2026-11-01T03:30:00Z", "2026-11-01T05:30:00Z", "2026-11-02T04:30:00Z"} {
		var named disclosure.Named
		named.Chapter(journalChapter, disclosure.Whole, 0, 0)
		send(t, journal, domain.Sending{At: utc(at), JobID: readingJob, Purpose: "card", Named: named})
	}
	// The same Job reads at another helper the same day: both are named.
	send(t, journal, domain.Sending{At: utc("2026-11-01T05:40:00Z"), JobID: readingJob, Purpose: "card_pass", Profile: "account", Host: "canonkeeper.example.test"})
	// A question of the other saga with two requests of «Свой ИИ» in two currencies.
	first := send(t, journal, domain.Sending{At: utc("2026-11-03T10:00:00Z"), JobID: otherJob, Purpose: "chat", ThreadID: "thread-x", Named: disclosure.Named{Question: true}})
	second := send(t, journal, domain.Sending{At: utc("2026-11-03T10:01:00Z"), JobID: otherJob, Purpose: "scan", Named: disclosure.Named{Chapters: []disclosure.Chapter{{ID: journalChapter, Precision: disclosure.Whole}}}})
	for index, id := range []string{first, second} {
		currency := []string{"RUB", "USD"}[index]
		if err := journal.Record(ctx, domain.Request{ID: id, At: utc("2026-11-03T10:00:00Z"), Purpose: "chat", JobID: otherJob, Profile: "own", ProviderHost: "provider.example.test",
			Model: "north", Tokens: &domain.Tokens{Input: 100, Output: 20}, Outcome: domain.OutcomeCompleted, Cost: &domain.Cost{Micros: 5000, Currency: currency}}); err != nil {
			t.Fatal(err)
		}
	}
	// One check of the connection: five probes and «Определить».
	for minute := range 6 {
		send(t, journal, domain.Sending{At: utc(fmt.Sprintf("2026-11-03T12:0%d:00Z", minute)), CheckID: checkOne, Purpose: domain.PurposeProbe, Named: disclosure.Named{Protocol: minute == 0}})
	}

	york := allRows(t, journal, "", "America/New_York", 50)
	if len(york) != 4 {
		t.Fatalf("New York: %d rows %+v", len(york), york)
	}
	if york[0].Kind != domain.KindCheck || york[0].Sendings != 6 || york[0].Protocol || york[1].Kind != domain.KindQuestion || york[1].SagaTitle != "Переправа" || !york[1].ChatGone {
		t.Fatalf("the newest rows = %+v", york[:2])
	}
	if own := york[1].Own; own == nil || own.Requests != 2 || own.Tokens != 240 || len(own.Costs) != 2 || own.Costs[0].Micros != 5000 {
		t.Fatalf("the numbers of «Свой ИИ» = %+v", york[1].Own)
	}
	nov1, oct31 := york[2], york[3]
	if !strings.HasSuffix(nov1.Key, "|2026-11-01") || nov1.Sendings != 3 || !strings.HasSuffix(oct31.Key, "|2026-10-31") || oct31.Sendings != 1 {
		t.Fatalf("the reading in New York = %+v / %+v", nov1, oct31)
	}
	if nov1.Running || nov1.SagaTitle != "Северный предел" || !slices.Equal(nov1.Recipients, []string{"сервер CanonKeeper", "provider.example.test"}) {
		t.Fatalf("the row of the reading = %+v", nov1)
	}
	moscow := allRows(t, journal, "", "Europe/Moscow", 50)
	if len(moscow) != 4 || moscow[2].Sendings != 1 || moscow[3].Sendings != 3 {
		t.Fatalf("Moscow: %+v", moscow)
	}
	if reading := allRows(t, journal, domain.KindReading, "Europe/Moscow", 50); len(reading) != 2 || reading[0].Kind != domain.KindReading {
		t.Fatalf("the reading = %+v", reading)
	}
	if checks := allRows(t, journal, domain.KindCheck, "Europe/Moscow", 50); len(checks) != 1 {
		t.Fatalf("the checks = %+v", checks)
	}
	// The pages of one row go without a row twice or missed.
	var keys []string
	for _, row := range allRows(t, journal, "", "America/New_York", 1) {
		keys = append(keys, row.Key)
	}
	var wanted []string
	for _, row := range york {
		wanted = append(wanted, row.Key)
	}
	if !slices.Equal(keys, wanted) {
		t.Fatalf("by pages %v, at once %v", keys, wanted)
	}
	if _, _, err := journal.Rows(ctx, domain.RowsQuery{Zone: strings.Repeat("Z", 65), Limit: 50}); err == nil {
		t.Fatal("a zone of another form was read")
	}
	if sagas, err := journal.Sagas(ctx); err != nil || sagas != 2 {
		t.Fatalf("sagas %d, %v", sagas, err)
	}

	// 600 chapters a day of one Job are one row with words of its books.
	big := "95000000-0000-4000-8000-000000000015"
	seedJob(t, db, big, journalSaga)
	for index := range 600 {
		send(t, journal, domain.Sending{At: utc("2026-11-04T08:00:00Z").Add(time.Duration(index) * time.Second), JobID: big, Purpose: "card",
			Named: disclosure.Named{Chapters: []disclosure.Chapter{{ID: fmt.Sprintf("95000000-0000-4000-8000-%012d", 100000+index), Precision: disclosure.Whole}}}})
	}
	rows := allRows(t, journal, domain.KindReading, "Europe/Moscow", 50)
	if len(rows) != 3 || rows[0].Sendings != 600 || len(rows[0].Named.Chapters) != 600 {
		t.Fatalf("the big Job = %d rows, %d sendings", len(rows), rows[0].Sendings)
	}
	if words := domain.WhatWent(rows[0].Kind, rows[0].Named, domain.Places{}, domain.Paging{}); words != "600 глав, которых уже нет в книге" {
		t.Fatalf("the words of the big Job = %q", words)
	}
}

// JR2.2: the rows are read by the indexes — a journal of 200 000 records is never read whole for a page.
func TestTheRowsAreReadByTheIndexes(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_plan")
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO model_disclosures (id, at, project_id, job_id, thread_id, check_id, purpose, profile_name, provider_host, request_bytes, named, first_time)
		SELECT gen_random_uuid(), timestamptz '2026-01-01 00:00:00+00' + make_interval(secs => serial * 120), NULL, NULL, NULL, gen_random_uuid(),
		  'probe', 'own', 'provider.example.test', 100, '{}'::jsonb, false
		FROM generate_series(1, 200000) AS serial`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `ANALYZE model_disclosures`); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"a day": `EXPLAIN SELECT coalesce(job_id, check_id, id)::text, min(at), count(*) FROM model_disclosures
			WHERE at >= timestamptz '2026-03-01 00:00:00+00' AND at < timestamptz '2026-03-02 00:00:00+00' AND (cardinality('{}'::text[]) = 0 OR purpose = ANY('{}'::text[])) GROUP BY 1`,
		"the latest before":  `EXPLAIN SELECT max(at) FROM model_disclosures WHERE at < timestamptz '2026-03-01 00:00:00+00'`,
		"the latest of kind": `EXPLAIN SELECT max(at) FROM model_disclosures WHERE purpose = 'probe' AND at < timestamptz '2026-03-01 00:00:00+00'`,
	} {
		rows, err := db.Pool.Query(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(line + "\n")
		}
		rows.Close()
		if strings.Contains(plan.String(), "Seq Scan") {
			t.Fatalf("%s reads the whole journal:\n%s", name, plan.String())
		}
	}
}

// JR2.4: the sendings of a row go in their order by pages, each with the words of its outcome by the row of its request — an
// answer none, a refusal of the helper, no connection, a broken answer, a stop, no row — and a repeat of the one before.
func TestTheSendingsOfARowSayTheirOutcomes(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_sendings")
	seedJournalSaga(t, db)
	journal := New(db)
	ctx := context.Background()
	outcomes := []struct {
		outcome  domain.Outcome
		failure  string
		words    string
		recorded bool
	}{
		{domain.OutcomeCompleted, "", "", true},
		{domain.OutcomeFailed, domain.FailureRefused, "помощник отказал", true},
		{domain.OutcomeFailed, domain.FailureConnection, "ответа нет: связь прервалась", true},
		{domain.OutcomeFailed, domain.FailureTorn, "ответ оборвался", true},
		{domain.OutcomeCanceled, "", "остановлено", true},
		{domain.OutcomeFailed, domain.FailureUnsent, "не отправлено", true},
		{"", "", "ответ не записан", false},
	}
	for index, row := range outcomes {
		at := utc("2026-11-05T09:00:00Z").Add(time.Duration(index) * time.Minute)
		id := send(t, journal, domain.Sending{At: at, JobID: journalJob, Purpose: "card", Bytes: 500})
		if row.recorded {
			if err := journal.Record(ctx, domain.Request{ID: id, At: at, Purpose: "card", JobID: journalJob, Profile: "own", ProviderHost: "provider.example.test",
				Model: "north", Outcome: row.outcome, Failure: row.failure}); err != nil {
				t.Fatal(err)
			}
		}
	}
	key := journalJob + "|2026-11-05"
	var lines []domain.SendingLine
	var after *domain.SendingCursor
	for {
		_, page, next, err := journal.Sendings(ctx, key, "Europe/Moscow", after, 4)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, page...)
		if next == nil {
			break
		}
		after = next
	}
	if len(lines) != len(outcomes) {
		t.Fatalf("sendings = %d", len(lines))
	}
	for index, line := range lines {
		if line.Outcome != outcomes[index].words || line.Recipient != "provider.example.test" || line.Bytes != 500 {
			t.Errorf("sending %d = %+v, want %q", index, line, outcomes[index].words)
		}
		if line.Repeat != (index > 0) {
			t.Errorf("sending %d repeat %v", index, line.Repeat)
		}
	}
	if _, _, _, err := journal.Sendings(ctx, "not-a-row", "Europe/Moscow", nil, 4); err == nil {
		t.Fatal("a row of another form was read")
	}
	// JR2.5: the file reads the sendings of a page of rows in one read by their keys — the same sendings, row by row.
	checkAt := utc("2026-11-05T10:00:00Z")
	send(t, journal, domain.Sending{At: checkAt, CheckID: checkOne, Purpose: "probe", Bytes: 200})
	send(t, journal, domain.Sending{At: checkAt.Add(time.Second), CheckID: checkOne, Purpose: "probe", Bytes: 200})
	check := checkOne + "|2026-11-05"
	all, err := journal.SendingsOf(ctx, []string{key, check}, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(all[key], lines, func(a, b domain.SendingLine) bool { return fmt.Sprint(a) == fmt.Sprint(b) }) || len(all[check]) != 2 ||
		all[check][0].Repeat || !all[check][1].Repeat {
		t.Fatalf("the sendings of two rows in one read = %+v", all)
	}
	if _, err := journal.SendingsOf(ctx, []string{key, "not-a-row"}, "Europe/Moscow"); err == nil {
		t.Fatal("a row of another form was read with the rest")
	}
}

// JR2.1: a zone of the computer PostgreSQL does not know does not break the journal — its days go by UTC.
func TestAZoneTheDatabaseDoesNotKnowReadsTheDaysByUTC(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_unknown_zone")
	seedJournalSaga(t, db)
	journal := New(db)
	ctx := context.Background()
	send(t, journal, domain.Sending{At: utc("2026-11-05T23:30:00Z"), JobID: journalJob, Purpose: "card"})
	send(t, journal, domain.Sending{At: utc("2026-11-06T00:30:00Z"), JobID: journalJob, Purpose: "card"})
	unknown := allRows(t, journal, "", "Etc/Unknown", 50)
	if len(unknown) != 2 || unknown[0].Key != journalJob+"|2026-11-06" || unknown[1].Key != journalJob+"|2026-11-05" {
		t.Fatalf("the rows of a zone nobody knows = %+v", unknown)
	}
	if _, lines, _, err := journal.Sendings(ctx, unknown[0].Key, "Etc/Unknown", nil, 10); err != nil || len(lines) != 1 {
		t.Fatalf("the sendings of a zone nobody knows = %d, %v", len(lines), err)
	}
	if _, err := journal.Local(ctx, "Etc/Unknown", []time.Time{utc("2026-11-05T23:30:00Z")}); err != nil {
		t.Fatalf("the clock of a zone nobody knows: %v", err)
	}
	if _, _, err := journal.Rows(ctx, domain.RowsQuery{Zone: "", Limit: 50}); err == nil {
		t.Fatal("a journal without a zone was read")
	}
}

// JR2.6: the clearing takes the records and the rows of the requests there are; what a thread named and has not sent, the
// helpers of the threads and the requests of the sample stay — the next record takes the naming.
func TestTheClearingKeepsWhatIsNotTheJournal(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_clear")
	seedJournalSaga(t, db)
	journal := New(db)
	ctx := context.Background()
	id := send(t, journal, domain.Sending{At: time.Now(), JobID: journalJob, ThreadID: "thread-1", Purpose: "chat"})
	if err := journal.Record(ctx, domain.Request{ID: id, At: time.Now(), Purpose: "chat", JobID: journalJob, Profile: "own", ProviderHost: "provider.example.test", Model: "north", Outcome: domain.OutcomeCompleted}); err != nil {
		t.Fatal(err)
	}
	var read disclosure.Named
	read.Chapter(journalChapter, disclosure.Paragraphs, 1, 4)
	if _, err := journal.Name(ctx, "thread-1", journalSaga, read); err != nil {
		t.Fatal(err)
	}
	if err := journal.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	counts := func() (records, requests, pending, recipients int) {
		if err := db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM model_disclosures), (SELECT count(*) FROM model_requests),
			(SELECT count(*) FROM model_disclosures_pending), (SELECT count(*) FROM model_thread_recipients)`).Scan(&records, &requests, &pending, &recipients); err != nil {
			t.Fatal(err)
		}
		return
	}
	if records, requests, pending, recipients := counts(); records != 0 || requests != 0 || pending != 1 || recipients != 1 {
		t.Fatalf("after the clearing: records %d, requests %d, pending %d, recipients %d", records, requests, pending, recipients)
	}
	send(t, journal, domain.Sending{At: time.Now(), JobID: journalJob, ThreadID: "thread-1", Purpose: "chat"})
	rows := allRows(t, journal, "", "Europe/Moscow", 50)
	if len(rows) != 1 || len(rows[0].Named.Chapters) != 1 || rows[0].Named.Chapters[0].Precision != disclosure.Paragraphs {
		t.Fatalf("the next record after the clearing = %+v", rows)
	}
}
