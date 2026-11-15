// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"net/http"
	"time"

	"canonkeeper/backend/internal/domain/modeljournal"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/desktop"
)

// SendLog is the journal of what went out (services/desktop.SendLog; specs/2026-11-14-desk-journal.md JR2).
type SendLog interface {
	Rows(ctx context.Context, kind modeljournal.Kind, zone, cursor string) (service.LogPage, error)
	Sendings(ctx context.Context, key, zone, cursor string) (service.LogSendings, error)
	File(ctx context.Context, kind modeljournal.Kind, zone string) (string, string, error)
	Clear(ctx context.Context) (time.Time, error)
}

type logSagaJSON struct {
	Title   string `json:"title"`
	Deleted bool   `json:"deleted"`
}

type logChatJSON struct {
	Title   *string `json:"title"`
	Deleted bool    `json:"deleted"`
}

type logCostJSON struct {
	Currency string `json:"currency"`
	Micros   int64  `json:"micros"`
}

type logOwnJSON struct {
	Requests  int           `json:"requests"`
	Tokens    int64         `json:"tokens"`
	Estimated bool          `json:"estimated"`
	Costs     []logCostJSON `json:"costs"`
}

type logRowJSON struct {
	Key        string       `json:"key"`
	Kind       string       `json:"kind"`
	StartedAt  string       `json:"startedAt"`
	LastAt     string       `json:"lastAt"`
	Saga       *logSagaJSON `json:"saga"`
	Chat       *logChatJSON `json:"chat"`
	WhatWent   string       `json:"whatWent"`
	Recipients []string     `json:"recipients"`
	Bytes      int64        `json:"bytes"`
	Sendings   int          `json:"sendings"`
	Running    bool         `json:"running"`
	FirstTime  bool         `json:"firstTime"`
	Own        *logOwnJSON  `json:"own"`
}

type logPageJSON struct {
	Rows      []logRowJSON `json:"rows"`
	Next      *string      `json:"next"`
	ClearedAt *string      `json:"clearedAt"`
	Sagas     int          `json:"sagas"`
}

type logSendingJSON struct {
	At        string  `json:"at"`
	Recipient string  `json:"recipient"`
	Bytes     int64   `json:"bytes"`
	WhatWent  string  `json:"whatWent"`
	FirstTime bool    `json:"firstTime"`
	Outcome   *string `json:"outcome"`
	Repeat    bool    `json:"repeat"`
}

type logSendingsJSON struct {
	Sendings []logSendingJSON `json:"sendings"`
	Next     *string          `json:"next"`
}

type logFileJSON struct {
	FileName string `json:"fileName"`
	Text     string `json:"text"`
}

type logClearedJSON struct {
	ClearedAt string `json:"clearedAt"`
}

// logKind is the kind of the query: one of the kinds or none.
func logKind(r *http.Request) (modeljournal.Kind, bool) {
	value := modeljournal.Kind(r.URL.Query().Get("kind"))
	if value == "" {
		return "", true
	}
	for _, kind := range modeljournal.Kinds() {
		if kind == value {
			return kind, true
		}
	}
	return "", false
}

// logFailed answers a failure of the journal: a zone, a row or a cursor it does not know is 400.
func logFailed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, modeljournal.ErrZone) || errors.Is(err, service.ErrLogCursor) {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "the zone, the row or the cursor is not known", "validation.failed")
		return
	}
	unexpected(w, r, err)
}

// sendLog is listSendLog (JR2.2): the rows of the journal by the days of the zone of the author, from the newest, fifty a
// page, to the session of the window.
func (handlers Handlers) sendLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	kind, ok := logKind(r)
	if !ok {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "the kind is not known", "validation.failed")
		return
	}
	page, err := handlers.Log.Rows(r.Context(), kind, r.URL.Query().Get("zone"), r.URL.Query().Get("cursor"))
	if err != nil {
		logFailed(w, r, err)
		return
	}
	response := logPageJSON{Rows: make([]logRowJSON, 0, len(page.Rows)), Next: textOrNull(page.Next), Sagas: page.Sagas}
	if page.ClearedAt != nil {
		cleared := moment(*page.ClearedAt)
		response.ClearedAt = &cleared
	}
	for _, row := range page.Rows {
		shown := logRowJSON{
			Key: row.Key, Kind: string(row.Kind), StartedAt: moment(row.Start), LastAt: moment(row.Last), WhatWent: row.WhatWent,
			Recipients: row.Recipients, Bytes: row.Bytes, Sendings: row.Sendings, Running: row.Running, FirstTime: row.FirstTime,
		}
		if row.ProjectID != "" {
			shown.Saga = &logSagaJSON{Title: row.SagaTitle, Deleted: row.SagaDeleted}
		}
		if row.Kind == modeljournal.KindQuestion {
			shown.Chat = &logChatJSON{Deleted: row.ChatGone}
			if !row.ChatGone {
				shown.Chat.Title = textOrNull(row.ChatTitle)
			}
		}
		if own := row.Own; own != nil {
			shown.Own = &logOwnJSON{Requests: own.Requests, Tokens: own.Tokens, Estimated: own.Estimated, Costs: make([]logCostJSON, 0, len(own.Costs))}
			for _, cost := range own.Costs {
				shown.Own.Costs = append(shown.Own.Costs, logCostJSON{Currency: cost.Currency, Micros: cost.Micros})
			}
		}
		response.Rows = append(response.Rows, shown)
	}
	writeJSON(w, http.StatusOK, response)
}

// sendLogSendings is listSendLogSendings (JR2.4): the sendings of a row in their order, a hundred a page.
func (handlers Handlers) sendLogSendings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	query := r.URL.Query()
	page, err := handlers.Log.Sendings(r.Context(), query.Get("row"), query.Get("zone"), query.Get("cursor"))
	if err != nil {
		logFailed(w, r, err)
		return
	}
	response := logSendingsJSON{Sendings: make([]logSendingJSON, 0, len(page.Sendings)), Next: textOrNull(page.Next)}
	for _, sending := range page.Sendings {
		response.Sendings = append(response.Sendings, logSendingJSON{
			At: moment(sending.At), Recipient: sending.Recipient, Bytes: sending.Bytes, WhatWent: sending.WhatWent,
			FirstTime: sending.FirstTime, Outcome: textOrNull(sending.Outcome), Repeat: sending.Repeat,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// sendLogFile is getSendLogFile (JR2.5): the journal of the choice as text with the name of its file.
func (handlers Handlers) sendLogFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	kind, ok := logKind(r)
	if !ok {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "the kind is not known", "validation.failed")
		return
	}
	name, text, err := handlers.Log.File(r.Context(), kind, r.URL.Query().Get("zone"))
	if err != nil {
		logFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, logFileJSON{FileName: name, Text: text})
}

// clearSendLog is clearSendLog (JR2.6): the records and the rows of the requests there are now go; what a thread named and
// has not sent, the helpers of the threads, the count of the consents and the requests of the samples stay.
func (handlers Handlers) clearSendLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	at, err := handlers.Log.Clear(r.Context())
	if err != nil {
		unexpected(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, logClearedJSON{ClearedAt: moment(at)})
}
