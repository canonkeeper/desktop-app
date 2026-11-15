// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
)

// Data «подробности: числа «Своего ИИ»»: the file says them as the details of the page do — one table for both.
func TestTheNumbersOfTheOwnAIAreTheTable(t *testing.T) {
	raw, err := os.ReadFile("testdata/own-words.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Cases []struct {
			Own struct {
				Requests  int   `json:"requests"`
				Tokens    int64 `json:"tokens"`
				Estimated bool  `json:"estimated"`
				Costs     []struct {
					Currency string `json:"currency"`
					Micros   int64  `json:"micros"`
				} `json:"costs"`
			} `json:"own"`
			Want string `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil || len(table.Cases) < 10 {
		t.Fatalf("the table: %v, %d cases", err, len(table.Cases))
	}
	for _, example := range table.Cases {
		own := OwnNumbers{Requests: example.Own.Requests, Tokens: example.Own.Tokens, Estimated: example.Own.Estimated}
		for _, cost := range example.Own.Costs {
			own.Costs = append(own.Costs, Cost{Currency: cost.Currency, Micros: cost.Micros})
		}
		if got := OwnWords(own); got != example.Want {
			t.Errorf("%+v:\n got %q\nwant %q", example.Own, got, example.Want)
		}
	}
}

func fileOfTheTest(kind Kind, limit int, rows ...Row) string {
	file := NewFile(kind, nil, limit)
	places := Places{Chapters: map[string]ChapterPlace{"c12": {State: PlaceLive, BookID: "b2", Book: 2, Number: "12", Title: "Белый порт"}}}
	sendings := []SendingLine{
		{At: time.Date(2026, 11, 13, 9, 40, 0, 0, time.UTC), Recipient: "provider.example.test", Bytes: 2100, Named: disclosure.Named{Question: true}},
		{At: time.Date(2026, 11, 13, 9, 41, 0, 0, time.UTC), Recipient: "сервер CanonKeeper", Bytes: 4100, Outcome: "ответ оборвался", Repeat: true,
			Named: disclosure.Named{Chapters: []disclosure.Chapter{{ID: "c12", Precision: disclosure.Paragraphs, Paragraphs: []disclosure.Range{{From: 3, To: 9}}}}}},
	}
	for _, row := range rows {
		if !file.Add(row, sendings, places, func(at time.Time) time.Time { return at.Add(3 * time.Hour) }) {
			break
		}
	}
	return file.Text()
}

func questionOfTheTest(recipients ...string) Row {
	return Row{
		Key: "q|2026-11-13", Kind: KindQuestion, Start: time.Date(2026, 11, 13, 9, 40, 0, 0, time.UTC), ChatTitle: "Кто стоит на заставе?",
		Named: disclosure.Named{Question: true}, Recipients: recipients, Bytes: 6200, Sendings: 2,
		Own: &OwnNumbers{Requests: 2, Tokens: 48000, Costs: []Cost{{Currency: "RUB", Micros: 3_000_000}}},
	}
}

// JR2.5, Data «файл журнала»: the first line names the choice; a choice without rows says so; a row carries the numbers of
// «Свой ИИ» of the details; a sending names its helper only when the row went to several; a choice longer than the file
// ends with FileCut and no more.
func TestTheFileNamesItsChoiceAndItsNumbers(t *testing.T) {
	all := fileOfTheTest("", FileLines, questionOfTheTest("provider.example.test"))
	if first, _, _ := strings.Cut(all, "\n"); first != "Журнал отправок CanonKeeper · Все виды" {
		t.Fatalf("the first line = %q", first)
	}
	for _, want := range []string{
		"13 ноября 2026, 12:40 · Вопрос · ваш вопрос · provider.example.test · 6 КБ\n",
		"  Чат: «Кто стоит на заставе?»\n",
		"  2 отправки · всего 6 КБ\n",
		"  2 запроса · 48 тыс. токенов · ≈\u00a03\u00a0₽\n",
		"  13 ноября 2026, 12:40 · ваш вопрос · 2 КБ\n",
		"  13 ноября 2026, 12:41 · Книга 2, гл. 12 „Белый порт“, абзацы 3–9 · 4 КБ · ответ оборвался · повтор\n",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the file has no line %q:\n%s", want, all)
		}
	}
	two := fileOfTheTest(KindQuestion, FileLines, questionOfTheTest("provider.example.test", "сервер CanonKeeper"))
	if first, _, _ := strings.Cut(two, "\n"); first != "Журнал отправок CanonKeeper · Вопрос" {
		t.Fatalf("the first line of a kind = %q", first)
	}
	if !strings.Contains(two, "  13 ноября 2026, 12:41 · Книга 2, гл. 12 „Белый порт“, абзацы 3–9 · сервер CanonKeeper · 4 КБ · ответ оборвался · повтор\n") {
		t.Fatalf("a row of two helpers names the helper of each sending:\n%s", two)
	}
	if empty := fileOfTheTest("", FileLines); !strings.HasSuffix(empty, "\nЕщё ничего не уходило\n") {
		t.Fatalf("an empty journal:\n%s", empty)
	}
	if empty := fileOfTheTest(KindMaterials, FileLines); !strings.HasSuffix(empty, "\nВ этом виде ничего не уходило\n") || strings.Contains(empty, "Ещё ничего") {
		t.Fatalf("an empty choice:\n%s", empty)
	}
	rows := make([]Row, 100)
	for index := range rows {
		rows[index] = questionOfTheTest("provider.example.test")
	}
	cut := fileOfTheTest("", 40, rows...)
	lines := strings.Split(strings.TrimSuffix(cut, "\n"), "\n")
	if len(lines) != 40 || lines[39] != FileCut || strings.Count(cut, FileCut) != 1 {
		t.Fatalf("a choice longer than the file: %d lines, the last %q", len(lines), lines[len(lines)-1])
	}
}

// specs/2026-11-15-desk-mcp.md MC4.4, Data «журнал»: a row of the readings of a program goes in the order of every row of the
// file — the time, «Свой Claude или Codex», what it read, the program, the size —, its saga below and its calls counted; a
// sending names its page of the answer and no outcome. The second line of the file names both kinds of the journal.
func TestTheReadingsOfAProgramGoInTheFileAsEveryRow(t *testing.T) {
	file := NewFile("", nil, FileLines)
	places := Places{Chapters: map[string]ChapterPlace{"c3": {State: PlaceLive, BookID: "b1", Book: 1, Number: "3", Title: "Переправа"}}}
	paged := disclosure.Named{Chapters: []disclosure.Chapter{{ID: "c3", Precision: disclosure.Pages}}}
	row := Row{
		Kind: KindProgram, Start: time.Date(2026, 11, 13, 11, 5, 0, 0, time.UTC), Named: paged, Paged: true,
		Recipients: []string{"Claude Code"}, Bytes: 6246, Sendings: 6, SagaTitle: "Северный предел",
	}
	file.Add(row, []SendingLine{{At: time.Date(2026, 11, 13, 11, 6, 0, 0, time.UTC), Recipient: "Claude Code", Bytes: 1000, Named: paged, Page: 2, Pages: 6}},
		places, func(at time.Time) time.Time { return at.Add(3 * time.Hour) })
	text := file.Text()
	for _, want := range []string{
		"Что из ваших книг и чатов приложение отправляло помощнику и отдавало вашим программам на этом компьютере: когда, кому и что.\n",
		"13 ноября 2026, 14:05 · Свой Claude или Codex · Книга 1, гл. 3 „Переправа“, страницами ответа · Claude Code · 6 КБ\n",
		"  Сага: «Северный предел»\n",
		"  6 вызовов · всего 6 КБ\n",
		"  13 ноября 2026, 14:06 · Книга 1, гл. 3 „Переправа“ · страница 2 из 6 ответа · 1000 Б\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the file has no %q:\n%s", want, text)
		}
	}
}
