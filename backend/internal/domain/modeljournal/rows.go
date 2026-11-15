// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
)

// ErrZone is a zone of time the journal does not know, or a key of a row of another form.
var ErrZone = errors.New("model journal: the zone of time or the row is not known")

// Cursor is where a page of rows ends: the start of its last row and its key (specs/2026-11-14-desk-journal.md JR2.2).
type Cursor struct {
	Start time.Time
	Key   string
}

// RowsQuery asks a page of rows: of a kind or of all, by the days of a zone (an IANA name), older than After.
type RowsQuery struct {
	Kind  Kind
	Zone  string
	After *Cursor
	Limit int
}

// SendingCursor is where a page of the sendings of a row ends: the time and the number of its last sending.
type SendingCursor struct {
	At time.Time
	ID string
}

// Row is a work the author knows for one day of his computer (specs/2026-11-14-desk-journal.md JR2.1, JR2.2): a question of
// the chat with its scan, the reading of one Job, the parsing of the materials, one check of the connection. Key names the
// work and its day; Start and Last are its first and its last sending of the day; Named is what all its sendings gave;
// Protocol says every one of them was «Определить». Recipients are the words of whom they went to. Own are the numbers of
// the requests of «Свой ИИ» of its sendings.
type Row struct {
	Key         string
	Kind        Kind
	Start, Last time.Time
	ProjectID   string
	SagaTitle   string
	SagaDeleted bool
	// ChatTitle names the chat of a question; ChatGone — the chat was deleted.
	ChatTitle  string
	ChatGone   bool
	Named      disclosure.Named
	Protocol   bool
	Recipients []string
	Bytes      int64
	Sendings   int
	Running    bool
	FirstTime  bool
	Own        *OwnNumbers
	// Paged: an answer of a program the row holds went by pages (specs/2026-11-15-desk-mcp.md MC4.4).
	Paged bool
}

// OwnNumbers are the requests of «Свой ИИ» of a row, their tokens — estimated when one had no usage — and their cost in each
// currency.
type OwnNumbers struct {
	Requests  int
	Tokens    int64
	Estimated bool
	Costs     []Cost
}

// Sending is one sending of a row as its details show it (JR2.4): its time, to whom, its bytes, what it gave new, the first
// time to its helper, the words of its outcome — none for an answer — and whether it repeats the one before.
type SendingLine struct {
	ID        string
	At        time.Time
	Recipient string
	Bytes     int64
	Named     disclosure.Named
	FirstTime bool
	Outcome   string
	Repeat    bool
	// Page and Pages are the page of the answer of a reading of a program and how many it had (MC4.4); none for a sending.
	Page  int
	Pages int
}

// OutcomeWords are the words of the outcome of a sending (Data «подробности: исход») by the row of its request: an answer
// says nothing; a request written before the journal of the sendings, or none, is «ответ не записан».
func OutcomeWords(outcome Outcome, failure string, recorded bool) string {
	switch {
	case !recorded:
		return "ответ не записан"
	case failure == FailureUnsent:
		return "не отправлено"
	case outcome == OutcomeCompleted:
		return ""
	case outcome == OutcomeCanceled:
		return "остановлено"
	case outcome == OutcomeIncomplete, failure == FailureTorn:
		return "ответ оборвался"
	case failure == FailureConnection:
		return "ответа нет: связь прервалась"
	default:
		return "помощник отказал"
	}
}

// KindWords are the words of a kind (Data «виды»).
func KindWords(kind Kind) string {
	switch kind {
	case KindQuestion:
		return "Вопрос"
	case KindReading:
		return "Чтение книги"
	case KindMaterials:
		return "Ваши материалы"
	case KindProgram:
		return "Свой Claude или Codex"
	default:
		return "Проверка связи"
	}
}

// SizeWords is a size as the part «Данные» writes it: «48 Б», «6 КБ», «1,9 МБ», «1,2 ГБ».
func SizeWords(bytes int64) string {
	switch {
	case bytes < 1024:
		return strconv.FormatInt(bytes, 10) + " Б"
	case bytes < 1024*1024/10:
		return strconv.FormatInt(int64(math.Round(float64(bytes)/1024)), 10) + " КБ"
	case bytes < 1024*1024*1024:
		return oneDecimal(float64(bytes)/1024/1024) + " МБ"
	default:
		return oneDecimal(float64(bytes)/1024/1024/1024) + " ГБ"
	}
}

func oneDecimal(value float64) string {
	return strings.Replace(strconv.FormatFloat(value, 'f', 1, 64), ".", ",", 1)
}

var months = []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}

// MomentWords are a moment in the zone of the author: «13 ноября 2026, 14:05».
func MomentWords(at time.Time) string {
	return fmt.Sprintf("%d %s %d, %02d:%02d", at.Day(), months[at.Month()-1], at.Year(), at.Hour(), at.Minute())
}
