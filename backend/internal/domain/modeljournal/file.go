// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"fmt"
	"strings"
	"time"
)

// FileLines is how many lines the file of the journal holds at most (JR2.5): a choice that holds more ends with the line
// FileCut, the author narrows the choice.
const FileLines = 50_000

// FileCut is the last line of a file of a choice the file did not hold whole (Data «файл журнала»).
const FileCut = "Показаны первые 50 000 строк — сузьте вид"

// File is the journal of a choice as text (JR2.5), written portion by portion: its first line names the choice, every row
// goes with its sendings in the words of the details of the page — what went, to whom when the row went to several, how
// much, the outcome — and the numbers of «Свой ИИ»; local is a moment as the clock of the author shows it.
type File struct {
	out   strings.Builder
	limit int
	lines int
	rows  int
	kind  Kind
	cut   bool
}

// NewFile starts the file of a choice: the kind, "" for all kinds; clearedAt — the moment of the last clearing on the
// clock of the author, nil when the journal was never cleared; limit — FileLines but in the tests.
func NewFile(kind Kind, clearedAt *time.Time, limit int) *File {
	file := &File{limit: limit, kind: kind}
	choice := "Все виды"
	if kind != "" {
		choice = KindWords(kind)
	}
	file.line("Журнал отправок CanonKeeper · " + choice)
	file.line("Что из ваших книг и чатов приложение отправляло помощнику и отдавало вашим программам на этом компьютере: когда, кому и что.")
	file.line("Места названы так, как они сейчас стоят в книгах.")
	if clearedAt != nil {
		file.line("Журнал очищен " + MomentWords(*clearedAt) + " — раньше записей нет")
	}
	return file
}

// Add writes a row with its sendings; false — the file is full and holds no more rows.
func (file *File) Add(row Row, sendings []SendingLine, places Places, local func(time.Time) time.Time) bool {
	if file.cut {
		return false
	}
	file.rows++
	head := MomentWords(local(row.Start)) + " · " + KindWords(row.Kind) + " · " + WhatWent(row.Kind, row.Named, places, Paging{Paged: row.Paged}) +
		" · " + strings.Join(row.Recipients, ", ") + " · " + SizeWords(row.Bytes)
	if row.Running {
		head += " · идёт"
	}
	file.line("")
	file.line(head)
	if row.SagaTitle != "" {
		file.line("  Сага: «" + row.SagaTitle + "»")
	}
	if row.Kind == KindQuestion {
		chat := "удалённый чат"
		if !row.ChatGone && row.ChatTitle != "" {
			chat = "«" + row.ChatTitle + "»"
		}
		file.line("  Чат: " + chat)
	}
	if row.FirstTime {
		file.line("  весь прежний разговор этого чата — впервые этому получателю")
	}
	file.line("  " + CountWords(row.Kind, row.Sendings) + " · всего " + SizeWords(row.Bytes))
	if row.Own != nil {
		file.line("  " + OwnWords(*row.Own))
	}
	for _, line := range sendings {
		parts := []string{MomentWords(local(line.At)), WhatWent(row.Kind, line.Named, places, Paging{Page: line.Page, Pages: line.Pages})}
		if line.FirstTime {
			parts = append(parts, "весь прежний разговор этого чата — впервые этому получателю")
		}
		if len(row.Recipients) > 1 {
			parts = append(parts, line.Recipient)
		}
		parts = append(parts, SizeWords(line.Bytes))
		if line.Outcome != "" {
			parts = append(parts, line.Outcome)
		}
		if line.Repeat {
			parts = append(parts, "повтор")
		}
		file.line("  " + strings.Join(parts, " · "))
	}
	return !file.cut
}

// CountWords are the sendings of a row counted: «6 отправок», the readings of a program — «6 вызовов» (Data «журнал»).
func CountWords(kind Kind, count int) string {
	if kind == KindProgram {
		return fmt.Sprintf("%d %s", count, plural(count, "вызов", "вызова", "вызовов"))
	}
	return fmt.Sprintf("%d %s", count, plural(count, "отправка", "отправки", "отправок"))
}

// Text is the file: «Ещё ничего не уходило» or «В этом виде ничего не уходило» without a row, FileCut last when the choice
// held more than the file.
func (file *File) Text() string {
	text := file.out.String()
	switch {
	case file.cut:
		text += FileCut + "\n"
	case file.rows == 0 && file.kind == "":
		text += "\nЕщё ничего не уходило\n"
	case file.rows == 0:
		text += "\nВ этом виде ничего не уходило\n"
	}
	return text
}

// line writes a line while the file has room for it and the last line; the first that does not fit cuts the file.
func (file *File) line(text string) {
	if file.cut {
		return
	}
	if file.lines >= file.limit-1 {
		file.cut = true
		return
	}
	file.out.WriteString(text + "\n")
	file.lines++
}
