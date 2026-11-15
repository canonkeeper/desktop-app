// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"errors"
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
	readChapter = "95000000-0000-4000-8000-000000000021"
	readOther   = "95000000-0000-4000-8000-000000000022"
)

// seedMembers makes the author a member of his sagas, as the creation of a saga does.
func seedMembers(t *testing.T, db *storedb.DB, sagas ...string) {
	t.Helper()
	for _, saga := range sagas {
		if _, err := db.Pool.Exec(context.Background(), `INSERT INTO project_memberships (id, project_id, user_id, role, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, $2, 'owner', now(), now())`, saga, journalAuthor); err != nil {
			t.Fatal(err)
		}
	}
}

func readOf(t *testing.T, journal *Journal, read domain.ProgramRead) error {
	t.Helper()
	if read.ID == "" {
		read.ID = domain.NewID()
	}
	if read.ActorUserID == "" {
		read.ActorUserID = journalAuthor
	}
	if read.Page == 0 {
		read.Page, read.Pages = 1, 1
	}
	if read.Bytes == 0 {
		read.Bytes = 500
	}
	return journal.RecordRead(context.Background(), read)
}

func pagedChapter(id string) disclosure.Named {
	return disclosure.Named{Chapters: []disclosure.Chapter{{ID: id, Precision: disclosure.Pages}}}
}

// specs/2026-11-15-desk-mcp.md MC4.4: the migration of the readings goes over a journal of the version of the journal with
// the records of the gateway: every record stays, and a reading of a program lies after it.
func TestMigration0156KeepsTheRecordsOfTheGateway(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_0156", 155)
	seedJournalSaga(t, db)
	seedMembers(t, db, journalSaga)
	ctx := context.Background()
	journal := New(db)
	var named disclosure.Named
	named.Chapter(journalChapter, disclosure.Whole, 0, 0)
	send(t, journal, domain.Sending{At: utc("2026-11-13T10:00:00Z"), JobID: journalJob, Purpose: "card", Named: named})
	send(t, journal, domain.Sending{At: utc("2026-11-13T10:01:00Z"), CheckID: journalCheck, Purpose: "probe", Named: disclosure.Named{Protocol: true}})
	var before string
	if err := db.Pool.QueryRow(ctx, `SELECT string_agg(id::text || purpose || profile_name || provider_host || named::text, ',' ORDER BY at) FROM model_disclosures`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 155); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.Pool.QueryRow(ctx, `SELECT string_agg(id::text || purpose || profile_name || provider_host || named::text, ',' ORDER BY at) FROM model_disclosures`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before || before == "" {
		t.Fatalf("the records of the gateway changed:\n%s\n%s", before, after)
	}
	if err := readOf(t, journal, domain.ProgramRead{At: utc("2026-11-13T10:02:00Z"), ProjectID: journalSaga, Client: "Claude Code", Named: named}); err != nil {
		t.Fatal(err)
	}
	if rows := allRows(t, journal, "", "UTC", 10); len(rows) != 3 {
		t.Fatalf("rows after the migration: %+v", rows)
	}
}

// MC4.2, MC4.4: a reading of a program is its own row — a program × a saga × a day, sagas a program × a day without a saga —
// to its program, with no outcome; the pages of one chapter are records of their pages, the row «страницами ответа», never
// «целиком»; a chapter given whole by an answer of one page and by pages of another went whole. The cursor goes through the
// keys of the programs without a repeat or a gap; the details and the file read by the key of a program; the day is the
// author's zone's; the choice of the kind keeps them apart; the clearing takes them too. A saga that is gone or not the
// author's gets no record; the database refuses a reading with a Job, a helper or no page.
func TestTheReadingsOfAProgramAreRowsOfTheirOwn(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_programs")
	seedJournalSaga(t, db)
	seedOtherSaga(t, db)
	seedMembers(t, db, journalSaga, otherSaga)
	ctx := context.Background()
	journal := New(db)
	for page := 1; page <= 6; page++ {
		if err := readOf(t, journal, domain.ProgramRead{At: utc("2026-11-13T11:00:00Z").Add(time.Duration(page) * time.Second), ProjectID: journalSaga,
			Client: "Claude Code", Page: page, Pages: 6, Bytes: 36_000, Named: pagedChapter(readChapter)}); err != nil {
			t.Fatal(err)
		}
	}
	var whole disclosure.Named
	whole.Chapter(readOther, disclosure.Whole, 0, 0)
	for _, read := range []domain.ProgramRead{
		{At: utc("2026-11-13T11:10:00Z"), ProjectID: journalSaga, Client: "Codex", Named: pagedChapter(readOther), Page: 1, Pages: 2},
		{At: utc("2026-11-13T11:11:00Z"), ProjectID: journalSaga, Client: "Codex", Named: whole},
		{At: utc("2026-11-13T11:20:00Z"), ProjectID: otherSaga, Client: "Claude Code"},
		{At: utc("2026-11-13T11:30:00Z"), Client: "Claude Code", Named: disclosure.Named{Sagas: 2}},
		{At: utc("2026-11-13T23:30:00Z"), ProjectID: journalSaga, Client: "Codex"},
	} {
		if err := readOf(t, journal, read); err != nil {
			t.Fatal(err)
		}
	}
	rows := allRows(t, journal, domain.KindProgram, "UTC", 1)
	if len(rows) != 4 {
		t.Fatalf("the rows of the programs: %d", len(rows))
	}
	keys := map[string]bool{}
	for _, row := range rows {
		if keys[row.Key] {
			t.Fatalf("a row twice: %s", row.Key)
		}
		keys[row.Key] = true
		if row.Kind != domain.KindProgram || row.Running || row.FirstTime || row.Own != nil || len(row.Recipients) != 1 {
			t.Fatalf("a row of a program: %+v", row)
		}
	}
	byStart := func(at string) domain.Row {
		t.Helper()
		for _, row := range rows {
			if row.Start.Equal(utc(at)) {
				return row
			}
		}
		t.Fatalf("no row at %s", at)
		return domain.Row{}
	}
	paged := byStart("2026-11-13T11:00:01Z")
	if paged.Sendings != 6 || !paged.Paged || paged.Recipients[0] != "Claude Code" || paged.Named.Chapters[0].Precision != disclosure.Pages ||
		paged.ProjectID != journalSaga || paged.SagaTitle != "Северный предел" {
		t.Fatalf("six pages: %+v", paged)
	}
	if mixed := byStart("2026-11-13T11:10:00Z"); mixed.Recipients[0] != "Codex" || mixed.Named.Chapters[0].Precision != disclosure.Whole || mixed.Sendings != 3 {
		t.Fatalf("whole and by pages: %+v", mixed)
	}
	if sagas := byStart("2026-11-13T11:30:00Z"); sagas.ProjectID != "" || sagas.Named.Sagas != 2 {
		t.Fatalf("sagas: %+v", sagas)
	}
	if other := byStart("2026-11-13T11:20:00Z"); other.ProjectID != otherSaga {
		t.Fatalf("the other saga: %+v", other)
	}
	// The day is the author's: in Vladivostok the late reading of Codex is the next day, a row of its own.
	if far := allRows(t, journal, domain.KindProgram, "Asia/Vladivostok", 10); len(far) != 5 {
		t.Fatalf("the rows in another zone: %d", len(far))
	}
	if others := allRows(t, journal, domain.KindQuestion, "UTC", 10); len(others) != 0 {
		t.Fatalf("the readings among the questions: %+v", others)
	}
	kind, lines, _, err := journal.Sendings(ctx, paged.Key, "UTC", nil, 10)
	if err != nil || kind != domain.KindProgram || len(lines) != 6 {
		t.Fatalf("the details: %v %d %v", kind, len(lines), err)
	}
	for index, line := range lines {
		if line.Page != index+1 || line.Pages != 6 || line.Outcome != "" || line.Repeat || line.Recipient != "Claude Code" || line.FirstTime {
			t.Fatalf("a line of the details: %+v", line)
		}
	}
	places := domain.Places{Chapters: map[string]domain.ChapterPlace{readChapter: {State: domain.PlaceLive, Book: 1, Number: "3", Title: "Переправа"}}}
	if words := domain.WhatWent(kind, lines[1].Named, places, domain.Paging{Page: lines[1].Page, Pages: lines[1].Pages}); words != "Книга 1, гл. 3 „Переправа“ · страница 2 из 6 ответа" {
		t.Fatalf("the words of a page: %s", words)
	}
	if words := domain.WhatWent(kind, paged.Named, places, domain.Paging{Paged: paged.Paged}); words != "Книга 1, гл. 3 „Переправа“, страницами ответа" {
		t.Fatalf("the words of the row of six pages: %s", words)
	}
	files, err := journal.SendingsOf(ctx, []string{paged.Key}, "UTC")
	if err != nil || len(files[paged.Key]) != 6 {
		t.Fatalf("the sendings of the file: %v %v", files, err)
	}
	if client, at, ok, err := journal.LastRead(ctx); err != nil || !ok || client != "Codex" || !at.Equal(utc("2026-11-13T23:30:00Z")) {
		t.Fatalf("the last reading: %s %v %v %v", client, at, ok, err)
	}

	// A saga the author is no more a member of, or of another author, gets no record; a saga in «Недавно удалённые» the test of
	// the door reads through the deletion of the shelf.
	if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at = now() WHERE project_id = $1`, otherSaga); err != nil {
		t.Fatal(err)
	}
	var count int
	countOf := func() int {
		t.Helper()
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM model_disclosures WHERE purpose = 'mcp'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := countOf()
	if err := readOf(t, journal, domain.ProgramRead{At: time.Now(), ProjectID: otherSaga, Client: "Codex"}); !errors.Is(err, domain.ErrSagaGone) {
		t.Fatalf("a saga the author left: %v", err)
	}
	if err := readOf(t, journal, domain.ProgramRead{At: time.Now(), ProjectID: journalSaga, Client: "Codex", ActorUserID: "95000000-0000-4000-8000-0000000000ff"}); !errors.Is(err, domain.ErrSagaGone) {
		t.Fatalf("a saga of another author: %v", err)
	}
	if countOf() != before {
		t.Fatal("a refused reading left a record")
	}
	for name, statement := range map[string]string{
		"a Job":                    `INSERT INTO model_disclosures (id, at, project_id, job_id, purpose, client, page, pages, request_bytes, named, first_time) VALUES (gen_random_uuid(), now(), $1, $2, 'mcp', 'Codex', 1, 1, 1, '{}', false)`,
		"a helper":                 `INSERT INTO model_disclosures (id, at, project_id, purpose, client, page, pages, profile_name, provider_host, request_bytes, named, first_time) VALUES (gen_random_uuid(), now(), $1, 'mcp', 'Codex', 1, 1, 'own', 'x', 1, '{}', false) RETURNING $2::text`,
		"no page":                  `INSERT INTO model_disclosures (id, at, project_id, purpose, client, request_bytes, named, first_time) VALUES (gen_random_uuid(), now(), $1, 'mcp', 'Codex', 1, '{}', false) RETURNING $2::text`,
		"a page past the pages":    `INSERT INTO model_disclosures (id, at, project_id, purpose, client, page, pages, request_bytes, named, first_time) VALUES (gen_random_uuid(), now(), $1, 'mcp', 'Codex', 3, 2, 1, '{}', false) RETURNING $2::text`,
		"a sending with a program": `INSERT INTO model_disclosures (id, at, project_id, purpose, client, profile_name, provider_host, request_bytes, named, first_time) VALUES (gen_random_uuid(), now(), $1, 'chat', 'Codex', 'own', 'x', 1, '{}', false) RETURNING $2::text`,
	} {
		if _, err := db.Pool.Exec(ctx, statement, journalSaga, journalJob); err == nil || !strings.Contains(err.Error(), "check") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := journal.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if countOf() != 0 {
		t.Fatal("the clearing left the readings")
	}
	if _, _, ok, err := journal.LastRead(ctx); ok || err != nil {
		t.Fatalf("the last reading after the clearing: %v %v", ok, err)
	}
}

// MC3.1: the last reading is read by the index of the purposes and the time on a journal of 200 000 records of the gateway.
func TestTheLastReadingIsReadByTheIndex(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_send_journal_last_read")
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
	rows, err := db.Pool.Query(ctx, "EXPLAIN "+lastReadQuery, domain.PurposeProgram)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	rows.Close()
	if slices.ContainsFunc(plan, func(line string) bool { return strings.Contains(line, "Seq Scan") }) ||
		!slices.ContainsFunc(plan, func(line string) bool { return strings.Contains(line, "model_disclosures_purpose_at_idx") }) {
		t.Fatalf("the last reading reads the whole journal:\n%s", strings.Join(plan, "\n"))
	}
}
