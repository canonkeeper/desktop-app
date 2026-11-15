// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// SendLogStore reads and clears the journal of what went out (repository/postgres/modeljournal).
type SendLogStore interface {
	Rows(ctx context.Context, query modeljournal.RowsQuery) ([]modeljournal.Row, *modeljournal.Cursor, error)
	Sendings(ctx context.Context, key, zone string, after *modeljournal.SendingCursor, limit int) (modeljournal.Kind, []modeljournal.SendingLine, *modeljournal.SendingCursor, error)
	// SendingsOf are all the sendings of the rows of the keys, by the key, in one read.
	SendingsOf(ctx context.Context, keys []string, zone string) (map[string][]modeljournal.SendingLine, error)
	Places(ctx context.Context, named disclosure.Named) (modeljournal.Places, error)
	Sagas(ctx context.Context) (int, error)
	Clear(ctx context.Context) error
	// Local are moments as the clock of a zone shows them, by their microseconds since the epoch.
	Local(ctx context.Context, zone string, moments []time.Time) (map[int64]time.Time, error)
}

// LogClearing keeps when the author cleared the journal last, in the settings of the app.
type LogClearing interface {
	LogClearedAt() *time.Time
	KeepLogCleared(at time.Time) error
}

// RunningCheck names the check of the connection that runs now, "" when none does.
type RunningCheck interface {
	Running() string
}

// The sizes of the pages of the journal (specs/2026-11-14-desk-journal.md JR2.2, JR2.4).
const (
	logRowsPage     = 50
	logSendingsPage = 100
)

// ErrLogCursor is a cursor of a page of the journal this server did not give.
var ErrLogCursor = errors.New("desktop: the cursor of the journal is not one it gave")

// SendLog is the journal of what went out as «Настройки → Журнал» reads it (JR2): its rows by days with the words of what
// went, the sendings of a row, the file of a choice and the clearing.
type SendLog struct {
	Store   SendLogStore
	Clears  LogClearing
	Checks  RunningCheck
	Now     func() time.Time
	Pages   int
	Details int
	// FileLines is how many lines the file holds; modeljournal.FileLines but in the tests.
	FileLines int
}

// LogRow is a row with the words of what went.
type LogRow struct {
	modeljournal.Row
	WhatWent string
}

// LogPage is a page of rows, the cursor of the next page, when the journal was cleared last and how many sagas it names.
type LogPage struct {
	Rows      []LogRow
	Next      string
	ClearedAt *time.Time
	Sagas     int
}

// LogSending is a sending with the words of what it gave new.
type LogSending struct {
	modeljournal.SendingLine
	WhatWent string
}

// LogSendings are a page of the sendings of a row.
type LogSendings struct {
	Sendings []LogSending
	Next     string
}

// Rows is listSendLog (JR2.2).
func (log SendLog) Rows(ctx context.Context, kind modeljournal.Kind, zone, cursor string) (LogPage, error) {
	after, err := rowCursorOf(cursor)
	if err != nil {
		return LogPage{}, err
	}
	rows, next, err := log.Store.Rows(ctx, modeljournal.RowsQuery{Kind: kind, Zone: zone, After: after, Limit: log.pages()})
	if err != nil {
		return LogPage{}, err
	}
	words, err := log.words(ctx, rows)
	if err != nil {
		return LogPage{}, err
	}
	sagas, err := log.Store.Sagas(ctx)
	if err != nil {
		return LogPage{}, err
	}
	page := LogPage{Rows: words, ClearedAt: log.Clears.LogClearedAt(), Sagas: sagas}
	if next != nil {
		page.Next = encodeCursor(next.Start, next.Key)
	}
	return page, nil
}

// words are the rows with the words of what went, by the places of now.
func (log SendLog) words(ctx context.Context, rows []modeljournal.Row) ([]LogRow, error) {
	log.markRunning(rows)
	namings := make([]disclosure.Named, len(rows))
	for index, row := range rows {
		namings[index] = row.Named
	}
	places, err := log.Store.Places(ctx, disclosure.Merge(namings...))
	if err != nil {
		return nil, err
	}
	words := make([]LogRow, len(rows))
	for index, row := range rows {
		named := row.Named
		named.Protocol = row.Protocol
		words[index] = LogRow{Row: row, WhatWent: modeljournal.WhatWent(row.Kind, named, places, modeljournal.Paging{Paged: row.Paged})}
	}
	return words, nil
}

// markRunning marks the row of the check that runs now as a row that goes on.
func (log SendLog) markRunning(rows []modeljournal.Row) {
	running := ""
	if log.Checks != nil {
		running = log.Checks.Running()
	}
	for index := range rows {
		if rows[index].Kind == modeljournal.KindCheck {
			work, _, _ := strings.Cut(rows[index].Key, "|")
			rows[index].Running = running != "" && work == running
		}
	}
}

// Sendings is listSendLogSendings (JR2.4).
func (log SendLog) Sendings(ctx context.Context, key, zone, cursor string) (LogSendings, error) {
	after, err := sendingCursorOf(cursor)
	if err != nil {
		return LogSendings{}, err
	}
	kind, lines, next, err := log.Store.Sendings(ctx, key, zone, after, log.details())
	if err != nil {
		return LogSendings{}, err
	}
	namings := make([]disclosure.Named, len(lines))
	for index, line := range lines {
		namings[index] = line.Named
	}
	places, err := log.Store.Places(ctx, disclosure.Merge(namings...))
	if err != nil {
		return LogSendings{}, err
	}
	page := LogSendings{Sendings: make([]LogSending, len(lines))}
	for index, line := range lines {
		page.Sendings[index] = LogSending{SendingLine: line, WhatWent: modeljournal.WhatWent(kind, line.Named, places, modeljournal.Paging{Page: line.Page, Pages: line.Pages})}
	}
	if next != nil {
		page.Next = encodeCursor(next.At, next.ID)
	}
	return page, nil
}

// File is getSendLogFile (JR2.5): the journal of the choice as text, every row with all its sendings in the same words, and
// the name of the file with the date of the author. The rows go page by page, the sendings of a page in one read by their
// keys, the places and the clock of a page in one read each; the reading stops when the file holds FileLines lines.
func (log SendLog) File(ctx context.Context, kind modeljournal.Kind, zone string) (name, text string, err error) {
	now, cleared := log.Now(), log.Clears.LogClearedAt()
	moments := []time.Time{now}
	if cleared != nil {
		moments = append(moments, *cleared)
	}
	// The clock of the author is the zone's, read by the database of the app: the program keeps no zones of its own.
	clock, err := log.Store.Local(ctx, zone, moments)
	if err != nil {
		return "", "", err
	}
	var clearedAt *time.Time
	if cleared != nil {
		local := clock[cleared.UnixMicro()]
		clearedAt = &local
	}
	file := modeljournal.NewFile(kind, clearedAt, log.fileLines())
	for after, more := (*modeljournal.Cursor)(nil), true; more; {
		page, next, err := log.Store.Rows(ctx, modeljournal.RowsQuery{Kind: kind, Zone: zone, After: after, Limit: log.pages()})
		if err != nil {
			return "", "", err
		}
		if more, err = log.filePage(ctx, file, page, zone); err != nil {
			return "", "", err
		}
		after, more = next, more && next != nil
	}
	return "canonkeeper-journal-" + clock[now.UnixMicro()].Format("2006-01-02") + ".txt", file.Text(), nil
}

// filePage writes a page of rows into the file; false — the file is full.
func (log SendLog) filePage(ctx context.Context, file *modeljournal.File, page []modeljournal.Row, zone string) (bool, error) {
	if len(page) == 0 {
		return true, nil
	}
	rows := page
	log.markRunning(rows)
	keys := make([]string, len(rows))
	for index := range rows {
		keys[index] = rows[index].Key
	}
	sendings, err := log.Store.SendingsOf(ctx, keys, zone)
	if err != nil {
		return false, err
	}
	namings := make([]disclosure.Named, 0, len(rows))
	var moments []time.Time
	for index := range rows {
		namings = append(namings, rows[index].Named)
		moments = append(moments, rows[index].Start)
		for _, line := range sendings[rows[index].Key] {
			namings = append(namings, line.Named)
			moments = append(moments, line.At)
		}
	}
	places, err := log.Store.Places(ctx, disclosure.Merge(namings...))
	if err != nil {
		return false, err
	}
	clock, err := log.Store.Local(ctx, zone, moments)
	if err != nil {
		return false, err
	}
	local := func(at time.Time) time.Time { return clock[at.UnixMicro()] }
	for index := range rows {
		row := rows[index]
		row.Named.Protocol = row.Protocol
		if !file.Add(row, sendings[row.Key], places, local) {
			return false, nil
		}
	}
	return true, nil
}

// Clear is clearSendLog (JR2.6): the records and the rows of the requests there are now go in one transaction, and the
// settings of the app keep when.
func (log SendLog) Clear(ctx context.Context) (time.Time, error) {
	at := log.Now()
	if err := log.Store.Clear(ctx); err != nil {
		return time.Time{}, err
	}
	if err := log.Clears.KeepLogCleared(at); err != nil {
		return time.Time{}, err
	}
	return at, nil
}

func (log SendLog) pages() int {
	if log.Pages > 0 {
		return log.Pages
	}
	return logRowsPage
}

func (log SendLog) fileLines() int {
	if log.FileLines > 0 {
		return log.FileLines
	}
	return modeljournal.FileLines
}

func (log SendLog) details() int {
	if log.Details > 0 {
		return log.Details
	}
	return logSendingsPage
}

// encodeCursor is a cursor of a page: a moment and a key, opaque to the page.
func encodeCursor(at time.Time, key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + key))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", ErrLogCursor
	}
	moment, key, ok := strings.Cut(string(raw), "|")
	at, err := time.Parse(time.RFC3339Nano, moment)
	if !ok || err != nil || key == "" {
		return time.Time{}, "", ErrLogCursor
	}
	return at, key, nil
}

func rowCursorOf(cursor string) (*modeljournal.Cursor, error) {
	if cursor == "" {
		return nil, nil
	}
	at, key, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	return &modeljournal.Cursor{Start: at, Key: key}, nil
}

func sendingCursorOf(cursor string) (*modeljournal.SendingCursor, error) {
	if cursor == "" {
		return nil, nil
	}
	at, id, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	return &modeljournal.SendingCursor{At: at, ID: id}, nil
}
