// SPDX-License-Identifier: AGPL-3.0-only

package door

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/services/programreads"
	researchservice "canonkeeper/backend/internal/services/researchchat"
)

const sagaID = "11111111-2222-4333-8444-555555555555"

// shelf is the readings of the author with one saga and a journal that keeps what it records. Like the journal of the app —
// its transaction begins on the context of the call —, it refuses a record whose call is cancelled; tried hears every try.
type shelf struct {
	mu        sync.Mutex
	sagas     []programreads.Saga
	recorded  []programreads.Read
	recordErr error
	tried     chan struct{}
}

func (reads *shelf) Sagas(context.Context) ([]programreads.Saga, error) { return reads.sagas, nil }

func (reads *shelf) Find(_ context.Context, argument string) (programreads.Saga, error) {
	if len(reads.sagas) == 0 {
		return programreads.Saga{}, programreads.ErrNoSagas
	}
	if argument == "" || argument == reads.sagas[0].Key {
		return reads.sagas[0], nil
	}
	return programreads.Saga{}, programreads.ErrNoSuch
}

func (reads *shelf) Record(ctx context.Context, read programreads.Read) error {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	if reads.tried != nil {
		reads.tried <- struct{}{}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if reads.recordErr != nil {
		return reads.recordErr
	}
	reads.recorded = append(reads.recorded, read)
	return nil
}

// handlerOf answers every call with the text, naming a chapter of the saga; it counts the calls it got.
func handlerOf(text string, calls *[]agent.ToolCall) Handlers {
	return func(projectID string, naming researchservice.Naming) Handler {
		return func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			*calls = append(*calls, call)
			if projectID != sagaID {
				return agent.ToolResult{}, errors.New("another saga")
			}
			var named disclosure.Named
			named.Chapter("aaaaaaaa-0000-4000-8000-000000000001", disclosure.Paragraphs, 1, 9)
			_, _ = naming.Name(ctx, call.Caller.RootThreadID, projectID, named)
			return agent.ToolResult{Success: true, Payload: json.RawMessage(text)}, nil
		}
	}
}

func request(tool, client string, arguments string) programtools.Request {
	return programtools.Request{
		Fingerprint: programtools.Fingerprint(), Version: "1.4.0", Kind: programtools.KindInstall, Client: client, Tool: tool,
		Arguments: json.RawMessage(arguments),
	}
}

func oneSaga() *shelf {
	return &shelf{sagas: []programreads.Saga{{ID: sagaID, Key: "11111111", Title: "Северный предел", Books: 2, Chapters: 40}}}
}

// MC2.2: the door answers only the names of its set — scan, remember, attachment and a made-up name are refused before the
// handler, with the names it has.
func TestTheDoorAnswersOnlyItsSet(t *testing.T) {
	var calls []agent.ToolCall
	reads := oneSaga()
	door := New(reads, handlerOf("текст", &calls), "1.4.0")
	for _, name := range []string{"scan", "remember", "attachment", "image", "draft.write", "drawing.edit", "saga.read", "made_up", ""} {
		answer := door.Answer(context.Background(), request(name, "claude-code", `{"chapter":"1"}`))
		if !answer.Error || answer.Text != programtools.WordsNoTool() || !strings.Contains(answer.Text, "search, read") {
			t.Fatalf("%s: %+v", name, answer)
		}
	}
	if len(calls) != 0 || len(reads.recorded) != 0 {
		t.Fatalf("calls %d, records %d", len(calls), len(reads.recorded))
	}
	answer := door.Answer(context.Background(), request("read", "claude-code", `{"chapter":"1","saga":"11111111"}`))
	if answer.Error || answer.Text != "текст" || len(calls) != 1 || string(calls[0].Arguments) != `{"chapter":"1"}` ||
		calls[0].Namespace != "saga" || calls[0].Tool != "read" {
		t.Fatalf("read: %+v, %+v", answer, calls)
	}
	if got := reads.recorded[0]; got.ProjectID != sagaID || got.Page != 1 || got.Pages != 1 || got.Bytes != int64(len("текст")) ||
		got.Client != "claude-code" || len(got.Named.Chapters) != 1 {
		t.Fatalf("the record: %+v", got)
	}
}

// MC2.5: a long answer goes by pages within the bound of the program in bytes, its line first and last; the pages without
// their lines put together are the answer of the handler to the sign; one page goes without the lines; a page past the last,
// a changed answer and a value of page not of its form are refused; nothing is kept between the calls.
func TestALongAnswerGoesByPages(t *testing.T) {
	var lines []string
	for index := range 2400 {
		lines = append(lines, fmt.Sprintf("¶%d Он сказал: \"идём\" — и пошёл к заставе %d.", index+1, index))
	}
	lines = append(lines, strings.Repeat("я", 30_000))
	text := strings.Join(lines, "\n") + "\n"
	if len([]rune(text)) < 120_000 {
		t.Fatalf("the chapter is %d characters", len([]rune(text)))
	}
	for _, client := range []string{"claude-code", "claude-ai", "codex-mcp-client", "my-writer", ""} {
		var calls []agent.ToolCall
		reads := oneSaga()
		door := New(reads, handlerOf(text, &calls), "1.4.0")
		bound := PageBound(client)
		first := door.Answer(context.Background(), request("read", client, `{"chapter":"1"}`))
		if first.Error {
			t.Fatalf("%s: %+v", client, first)
		}
		var joined strings.Builder
		page := 1
		answer := first
		for {
			if len(answer.Text) > bound {
				t.Fatalf("%s: page %d is %d bytes over %d", client, page, len(answer.Text), bound)
			}
			head, rest, _ := strings.Cut(answer.Text, "\n")
			body, tail := rest[:strings.LastIndexByte(rest, '\n')], rest[strings.LastIndexByte(rest, '\n')+1:]
			if head != tail || !strings.HasPrefix(head, fmt.Sprintf("Страница %d из ", page)) {
				t.Fatalf("%s: the lines of page %d: %q %q", client, page, head, tail)
			}
			joined.WriteString(body)
			_, next, more := strings.Cut(head, `"page": "`)
			if !more {
				break
			}
			value := strings.TrimSuffix(next, `"`)
			page++
			answer = door.Answer(context.Background(), request("read", client, `{"chapter":"1","page":"`+value+`"}`))
			if answer.Error {
				t.Fatalf("%s: page %d: %+v", client, page, answer)
			}
		}
		if joined.String() != text {
			t.Fatalf("%s: the pages put together are not the answer", client)
		}
		if got := reads.recorded; len(got) != page || got[1].Page != 2 || got[1].Pages != page {
			t.Fatalf("%s: records %+v", client, len(got))
		}
		if answer := door.Answer(context.Background(), request("read", client, fmt.Sprintf(`{"chapter":"1","page":"%d:%s"}`, page+1, markOf(text)))); !answer.Error ||
			answer.Text != programtools.WordsNoPage(page+1, page) {
			t.Fatalf("%s: a page past the last: %+v", client, answer)
		}
	}
	var calls []agent.ToolCall
	door := New(oneSaga(), handlerOf("коротко", &calls), "1.4.0")
	if answer := door.Answer(context.Background(), request("read", "claude-code", `{"chapter":"1"}`)); answer.Text != "коротко" {
		t.Fatalf("one page: %+v", answer)
	}
	if answer := door.Answer(context.Background(), request("read", "claude-code", `{"chapter":"1","page":"2:`+markOf(text)+`"}`)); answer.Text != programtools.WordsChanged {
		t.Fatalf("a changed answer: %+v", answer)
	}
	for _, value := range []string{`"2"`, `"2:abc"`, `"0:` + markOf("коротко") + `"`, `2`} {
		answer := door.Answer(context.Background(), request("read", "claude-code", `{"chapter":"1","page":`+value+`}`))
		if !answer.Error || strings.Contains(answer.Text, "коротко") {
			t.Fatalf("page %s: %+v", value, answer)
		}
	}
}

// MC2.5: a line longer than the bound is cut on the borders of the signs, and the pages give it back to the sign.
func TestALineLongerThanThePageIsCutBySigns(t *testing.T) {
	text := strings.Repeat("ёж", 20_000)
	var joined strings.Builder
	for number := 1; ; number++ {
		page, _, err := pageOf(text, smallPage, pageAsked{number: number})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.text) > smallPage || !strings.HasPrefix(page.text, "Страница") {
			t.Fatalf("page %d: %d bytes", number, len(page.text))
		}
		_, rest, _ := strings.Cut(page.text, "\n")
		joined.WriteString(rest[:strings.LastIndexByte(rest, '\n')])
		if number == page.pages {
			break
		}
	}
	if joined.String() != text {
		t.Fatal("the pages of a long line are not the line")
	}
}

// MC2.7: the refusal of the handler goes as it is, the words of two kinds put in the words of a program; no word of a turn of
// the chat.
func TestTheRefusalsOfTheHandlerSpeakToAProgram(t *testing.T) {
	refusal := func(kind, message string) Handlers {
		return func(string, researchservice.Naming) Handler {
			return func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
				payload, _ := json.Marshal(failure{Status: "terminal", Type: kind, Path: "$", Message: message, NextValidActions: []string{"stop"}})
				return agent.ToolResult{Success: false, Payload: payload}, nil
			}
		}
	}
	for kind, want := range map[string]string{
		"authority_lost":    programtools.WordsNoSuch,
		"saga_not_ready":    programtools.WordsNotReady,
		"source_changed":    "Текст саги изменился во время чтения. Повторите тот же вызов.",
		"read_failed":       "Чтение не удалось. Повторите вызов.",
		"invalid_arguments": "аргументы не прошли проверку",
	} {
		message := map[string]string{"authority_lost": "Ход чата больше не может читать сагу. Остановитесь.", "saga_not_ready": "Текст саги ещё подготавливается… и остановитесь."}[kind]
		if message == "" {
			message = want
		}
		reads := oneSaga()
		answer := New(reads, refusal(kind, message), "1.4.0").Answer(context.Background(), request("search", "claude-code", `{"queries":["застава"]}`))
		var got failure
		if err := json.Unmarshal([]byte(answer.Text), &got); err != nil || !answer.Error || got.Message != want || got.Type != kind ||
			strings.Contains(strings.ToLower(answer.Text), "ход чата") || strings.Contains(strings.ToLower(answer.Text), "остановитесь") {
			t.Fatalf("%s: %+v", kind, answer)
		}
		if len(reads.recorded) != 0 {
			t.Fatalf("%s: a refusal recorded", kind)
		}
	}
}

// MC2.3, MC4.2: sagas lists the sagas with their keys and is recorded with their count; a refusal of a saga names none; a
// failed record gives no text; a saga gone before the record is no such saga.
func TestSagasAndTheRefusalsOfASaga(t *testing.T) {
	var calls []agent.ToolCall
	reads := &shelf{sagas: []programreads.Saga{{ID: sagaID, Key: "11111111", Title: "Северный предел", Books: 2, Chapters: 40}, {ID: "22222222-0000-4000-8000-000000000000", Key: "22222222", Title: "Южный предел"}}}
	door := New(reads, handlerOf("текст главы", &calls), "1.4.0")
	answer := door.Answer(context.Background(), request("sagas", "codex-mcp-client", `{}`))
	if answer.Error || !strings.Contains(answer.Text, `«Северный предел» — saga: "11111111"; книг: 2, глав: 40`) || reads.recorded[0].Sagas != 2 || reads.recorded[0].ProjectID != "" {
		t.Fatalf("sagas: %+v %+v", answer, reads.recorded)
	}
	if answer := door.Answer(context.Background(), request("sagas", "codex-mcp-client", `{"saga":"x"}`)); !answer.Error {
		t.Fatalf("sagas with a saga: %+v", answer)
	}
	if answer := door.Answer(context.Background(), request("read", "codex-mcp-client", `{"chapter":"1","saga":"Восточный"}`)); answer.Text != programtools.WordsNoSuch || !answer.Error {
		t.Fatalf("no such saga: %+v", answer)
	}
	if answer := New(&shelf{}, handlerOf("x", &calls), "1.4.0").Answer(context.Background(), request("sagas", "", `{}`)); answer.Text != programtools.WordsNoSagas {
		t.Fatalf("no sagas: %+v", answer)
	}
	reads.recordErr = errors.New("the journal is down")
	if answer := door.Answer(context.Background(), request("read", "codex-mcp-client", `{"chapter":"1"}`)); answer.Text != programtools.WordsNotRecord || strings.Contains(answer.Text, "текст главы") {
		t.Fatalf("a failed record: %+v", answer)
	}
	reads.recordErr = programreads.ErrNoSuch
	if answer := door.Answer(context.Background(), request("read", "codex-mcp-client", `{"chapter":"1"}`)); answer.Text != programtools.WordsNoSuch {
		t.Fatalf("a saga gone before the record: %+v", answer)
	}
	for _, words := range []string{programtools.WordsNoSuch, programtools.WordsSeveral, programtools.WordsAmbiguous, programtools.WordsNoSagas, programtools.WordsNotReady} {
		if strings.Contains(words, "Северный") || len([]rune(words)) >= 2000 {
			t.Fatalf("the words %q", words)
		}
	}
}

// MC1.5, MC3.1: the door keeps the programs that turned to it — on their first word and on every call — with whether their
// bridge has its set; a bridge of another set gets the words by the versions and no reading.
func TestTheDoorKnowsWhoTurnedToIt(t *testing.T) {
	var calls []agent.ToolCall
	door := New(oneSaga(), handlerOf("текст", &calls), "1.4.0")
	door.Answer(context.Background(), programtools.Request{Fingerprint: programtools.Fingerprint(), Kind: programtools.KindInstall, Client: "claude-code", Title: "Claude Code", Hello: true})
	other := request("read", "claude-ai", `{"chapter":"1"}`)
	other.Fingerprint, other.Kind, other.Version = "0000", programtools.KindExtension, "1.3.0"
	if answer := door.Answer(context.Background(), other); answer.Text != programtools.WordsReinstall || len(calls) != 0 {
		t.Fatalf("another set: %+v", answer)
	}
	programs := door.Programs()
	if len(programs) != 2 || programs[0].Name != "Claude Desktop" || programs[0].Matches || !programs[0].Extension ||
		programs[1].Name != "Claude Code" || !programs[1].Matches {
		t.Fatalf("programs: %+v", programs)
	}
	if programtools.Mismatch("1.4.0", "1.5.0", programtools.KindInstall) != programtools.WordsRestart ||
		programtools.Mismatch("1.4.0", "1.3.9", programtools.KindInstall) != programtools.WordsNewSession ||
		programtools.Mismatch("1.4.0", "dev", programtools.KindExtension) != programtools.WordsReinstall {
		t.Fatal("the words of the versions")
	}
}
