// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// journalOfManyRows is a journal of rows reading jobs of one chapter each, two sendings a row; it counts its reads.
type journalOfManyRows struct {
	rows                              int
	rowReads, sendingReads, oneByOne  int
	placeReads, clockReads, keysAsked int
}

func (journal *journalOfManyRows) Rows(_ context.Context, query modeljournal.RowsQuery) ([]modeljournal.Row, *modeljournal.Cursor, error) {
	journal.rowReads++
	from := 0
	if query.After != nil {
		fmt.Sscan(query.After.Key, &from)
	}
	var page []modeljournal.Row
	for index := from; index < journal.rows && len(page) < query.Limit; index++ {
		page = append(page, modeljournal.Row{
			Key: fmt.Sprint(index + 1), Kind: modeljournal.KindReading, Start: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC).Add(-time.Duration(index) * time.Minute),
			Recipients: []string{"provider.example.test"}, Bytes: 2000, Sendings: 2,
			Named: disclosure.Named{Chapters: []disclosure.Chapter{{ID: "c1", Precision: disclosure.Whole}}},
		})
	}
	if from+len(page) >= journal.rows {
		return page, nil, nil
	}
	return page, &modeljournal.Cursor{Start: page[len(page)-1].Start, Key: page[len(page)-1].Key}, nil
}

func (journal *journalOfManyRows) Sendings(context.Context, string, string, *modeljournal.SendingCursor, int) (modeljournal.Kind, []modeljournal.SendingLine, *modeljournal.SendingCursor, error) {
	journal.oneByOne++
	return modeljournal.KindReading, nil, nil, nil
}

func (journal *journalOfManyRows) SendingsOf(_ context.Context, keys []string, _ string) (map[string][]modeljournal.SendingLine, error) {
	journal.sendingReads++
	journal.keysAsked += len(keys)
	all := map[string][]modeljournal.SendingLine{}
	for _, key := range keys {
		for minute := range 2 {
			all[key] = append(all[key], modeljournal.SendingLine{
				At: time.Date(2026, 11, 13, 9, minute, 0, 0, time.UTC), Recipient: "provider.example.test", Bytes: 1000,
				Named: disclosure.Named{Chapters: []disclosure.Chapter{{ID: "c1", Precision: disclosure.Whole}}},
			})
		}
	}
	return all, nil
}

func (journal *journalOfManyRows) Places(context.Context, disclosure.Named) (modeljournal.Places, error) {
	journal.placeReads++
	return modeljournal.Places{Chapters: map[string]modeljournal.ChapterPlace{"c1": {State: modeljournal.PlaceLive, BookID: "b1", Book: 1, Number: "1"}}}, nil
}

func (journal *journalOfManyRows) Sagas(context.Context) (int, error) { return 1, nil }
func (journal *journalOfManyRows) Clear(context.Context) error        { return nil }

func (journal *journalOfManyRows) Local(_ context.Context, _ string, moments []time.Time) (map[int64]time.Time, error) {
	journal.clockReads++
	clock := map[int64]time.Time{}
	for _, moment := range moments {
		clock[moment.UnixMicro()] = moment
	}
	return clock, nil
}

type noClearing struct{}

func (noClearing) LogClearedAt() *time.Time       { return nil }
func (noClearing) KeepLogCleared(time.Time) error { return nil }

// JR2.5 (the third check, C9): the file reads the rows page by page and the sendings of a page in one read by their keys —
// never one read a row; the places and the clock of a page are one read each; a choice longer than the file stops the
// reading at the page that filled it and ends with the line of the cut.
func TestTheFileReadsTheJournalInPortions(t *testing.T) {
	journal := &journalOfManyRows{rows: 120}
	log := SendLog{Store: journal, Clears: noClearing{}, Now: func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }, Pages: 50}
	name, text, err := log.File(context.Background(), "", "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	if name != "canonkeeper-journal-2026-11-13.txt" || strings.Count(text, " · Чтение книги · ") != 120 {
		t.Fatalf("the file %s has %d rows", name, strings.Count(text, " · Чтение книги · "))
	}
	if journal.rowReads != 3 || journal.sendingReads != 3 || journal.keysAsked != 120 || journal.oneByOne != 0 || journal.placeReads != 3 || journal.clockReads != 4 {
		t.Fatalf("reads: rows %d, sendings %d (keys %d, one by one %d), places %d, clock %d",
			journal.rowReads, journal.sendingReads, journal.keysAsked, journal.oneByOne, journal.placeReads, journal.clockReads)
	}

	cut := &journalOfManyRows{rows: 120}
	log = SendLog{Store: cut, Clears: noClearing{}, Now: log.Now, Pages: 50, FileLines: 100}
	if _, text, err = log.File(context.Background(), "", "Europe/Moscow"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 100 || lines[99] != modeljournal.FileCut || cut.rowReads != 1 || cut.sendingReads != 1 {
		t.Fatalf("a choice longer than the file: %d lines, the last %q, rows read %d times", len(lines), lines[len(lines)-1], cut.rowReads)
	}
}
