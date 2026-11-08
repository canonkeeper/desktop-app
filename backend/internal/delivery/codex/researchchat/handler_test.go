package researchchat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/researchchat"
)

type readServiceStub struct {
	err      error
	search   *service.SearchInput
	read     *service.ReadInput
	chapters *service.ChaptersInput
	scan     *service.ScanInput
	entity   *service.EntityInput
	timeline *service.TimelineInput
	threads  *service.ThreadsInput
	attached *service.AttachmentInput
}

func (stub readServiceStub) Attachment(_ context.Context, _ service.ToolContext, input service.AttachmentInput) (string, error) {
	if stub.attached != nil {
		*stub.attached = input
	}
	return "Приложение 1 «Глава 634»\n¶1 текст\n", stub.err
}

func (stub readServiceStub) Search(_ context.Context, _ service.ToolContext, input service.SearchInput) (string, error) {
	if stub.search != nil {
		*stub.search = input
	}
	return "### «вьюжница» (any): 1 абзацев в 1 главах\n", stub.err
}

func (stub readServiceStub) Read(_ context.Context, _ service.ToolContext, input service.ReadInput) (string, error) {
	if stub.read != nil {
		*stub.read = input
	}
	return "¶1 текст\n", stub.err
}

func (stub readServiceStub) Chapters(_ context.Context, _ service.ToolContext, input service.ChaptersInput) (string, error) {
	if stub.chapters != nil {
		*stub.chapters = input
	}
	return "Глав: 1\n", stub.err
}

func (stub readServiceStub) Glossary(context.Context, service.ToolContext, service.GlossaryInput) (string, error) {
	return "Найдено записей: 1\n", stub.err
}

func (stub readServiceStub) Changes(context.Context, service.ToolContext, service.ChangesInput) (string, error) {
	return "Изменений: 1\n", stub.err
}

func (stub readServiceStub) Entity(_ context.Context, _ service.ToolContext, input service.EntityInput) (string, error) {
	if stub.entity != nil {
		*stub.entity = input
	}
	return "Мирон (персонаж): фактов 1\n", stub.err
}

func (stub readServiceStub) Timeline(_ context.Context, _ service.ToolContext, input service.TimelineInput) (string, error) {
	if stub.timeline != nil {
		*stub.timeline = input
	}
	return "Шкала времени\n", stub.err
}

func (stub readServiceStub) Threads(_ context.Context, _ service.ToolContext, input service.ThreadsInput) (string, error) {
	if stub.threads != nil {
		*stub.threads = input
	}
	return "Нитей: 1 (важных 1); слой собран по гл. 2, глав новее: 0.\n", stub.err
}

func (stub readServiceStub) Scan(_ context.Context, _ service.ToolContext, input service.ScanInput) (string, error) {
	if stub.scan != nil {
		*stub.scan = input
	}
	return "Просмотрены главы 1–3.\n", stub.err
}

func call(tool, arguments string) agent.ToolCall {
	return agent.ToolCall{
		Caller: agent.ToolCaller{ThreadID: "root", RootThreadID: "root"}, TurnID: "turn",
		Namespace: "saga", Tool: tool, Arguments: json.RawMessage(arguments),
	}
}

func failure(t *testing.T, result agent.ToolResult) toolFailure {
	t.Helper()
	var decoded toolFailure
	if result.Success || json.Unmarshal(result.Payload, &decoded) != nil {
		t.Fatalf("result = success %v, %s; want a typed failure", result.Success, result.Payload)
	}
	return decoded
}

// specs/2026-11-02-image-workshop.md IW7.1 and specs/2026-11-07-desktop-app.md DA9.2: every chat turn reads the saga;
// a tool of the build (the site's saga.image) stands before the reading of the attached chapter, and the chat has no
// tool that draws, chooses a variant, checks a drawing or sets a reference.
func TestBundleOffersOnlySagaMemoryTools(t *testing.T) {
	bundle := Bundle(readServiceStub{}, memoryStub{})
	var names []string
	for _, spec := range bundle.Tools {
		names = append(names, spec.Namespace+"."+spec.Name)
		var schema map[string]any
		if json.Unmarshal(spec.InputSchema, &schema) != nil || schema["additionalProperties"] != false {
			t.Fatalf("%s schema is not a closed object: %s", spec.Name, spec.InputSchema)
		}
		if spec.Name == "image" {
			for _, word := range []string{`"choose"`, `"check"`, `"facts"`, "draw"} {
				if strings.Contains(spec.Description+string(spec.InputSchema), word) {
					t.Errorf("saga.image names %s", word)
				}
			}
		}
	}
	if !reflect.DeepEqual(names, []string{"saga.search", "saga.read", "saga.chapters", "saga.glossary", "saga.entity", "saga.timeline", "saga.threads", "saga.changes", "saga.scan", "saga.remember", "saga.memory", "saga.attachment"}) {
		t.Fatalf("tools = %v", names)
	}
	extra := Tool{Spec: agent.ToolSpec{Namespace: "saga", Name: "extra", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
		Run: func(context.Context, service.ToolContext, json.RawMessage) (service.ToolOutput, error) {
			return service.ToolOutput{Text: "ok\n"}, nil
		}}
	tools := Bundle(readServiceStub{}, memoryStub{}, extra).Tools
	if last := tools[len(tools)-2:]; last[0].Name != "extra" || last[1].Name != "attachment" {
		t.Fatalf("a tool of the build stands at %v", last)
	}
}

// The chat skill reaches the model as developer instructions read from the repository profile:
// Codex expands a $skill mention only in user input, so a mention in instructions loses the text.
func TestBundleSkillIsTheRepositoryChatSkill(t *testing.T) {
	skill := Bundle(readServiceStub{}, memoryStub{}).SkillName
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "codex-homes", "research-chat", "skills", skill, "SKILL.md"))
	if skill == "" || err != nil || !strings.Contains(string(body), "name: "+skill) {
		t.Fatalf("skill %q: %v", skill, err)
	}
}

func TestCardToolsDecodeTheirArguments(t *testing.T) {
	var chapters service.ChaptersInput
	var scan service.ScanInput
	var entity service.EntityInput
	var timeline service.TimelineInput
	var threads service.ThreadsInput
	handler := NewHandler(readServiceStub{chapters: &chapters, scan: &scan, entity: &entity, timeline: &timeline, threads: &threads}, memoryStub{})
	for _, test := range []agent.ToolCall{
		call("chapters", `{"from":"10","to":"20","detail":"card"}`),
		call("glossary", `{"query":"вьюжница","types":["раса"],"limit":10}`),
		call("entity", `{"name":"Мирон","at":"300","attributes":["возраст","уровень"]}`),
		call("timeline", `{"from":"1","to":"132","query":"день рождения"}`),
		call("threads", `{"query":"долг","who":"Мирон","kind":"обещание","level":"all","part":"arcs"}`),
		call("changes", `{"subject":"Мирон","to":"20"}`),
		call("scan", `{"task":"все расы","books":[1,2]}`),
	} {
		if result, err := handler.Handle(context.Background(), test); err != nil || !result.Success {
			t.Fatalf("%s = %v, %s, %v", test.Tool, result.Success, result.Payload, err)
		}
	}
	if threads != (service.ThreadsInput{Query: "долг", Who: "Мирон", Kind: "обещание", Level: "all", Part: "arcs"}) {
		t.Fatalf("decoded threads %#v", threads)
	}
	threadsResult, _ := handler.Handle(context.Background(), call("threads", `{"name":"Мирон"}`))
	if decoded := failure(t, threadsResult); decoded.Type != "invalid_arguments" {
		t.Fatalf("threads with an unknown field = %#v", decoded)
	}
	if !reflect.DeepEqual(chapters, service.ChaptersInput{From: "10", To: "20", Detail: service.ChaptersCard}) ||
		scan.Task != "все расы" || !reflect.DeepEqual(scan.Books, []int{1, 2}) {
		t.Fatalf("decoded chapters %#v, scan %#v", chapters, scan)
	}
	if !reflect.DeepEqual(entity, service.EntityInput{Name: "Мирон", At: "300", Attributes: []string{"возраст", "уровень"}}) ||
		timeline != (service.TimelineInput{From: "1", To: "132", Query: "день рождения"}) {
		t.Fatalf("decoded entity %#v, timeline %#v", entity, timeline)
	}
	entityResult, _ := handler.Handle(context.Background(), call("entity", `{"who":"Мирон"}`))
	if decoded := failure(t, entityResult); decoded.Type != "invalid_arguments" {
		t.Fatalf("entity with an unknown argument = %#v, want a correctable error", decoded)
	}
	result, _ := handler.Handle(context.Background(), call("chapters", `{"summaries":true}`))
	if decoded := failure(t, result); decoded.Type != "invalid_arguments" {
		t.Fatalf("old chapters argument = %#v, want a correctable error", decoded)
	}
}

func TestSuccessfulReadReturnsPlainTextForTheModel(t *testing.T) {
	var got service.SearchInput
	result, err := NewHandler(readServiceStub{search: &got}, memoryStub{}).Handle(context.Background(),
		call("search", `{"queries":["вьюжница","жрица"],"match":"all","order":"story","from":"10","books":[2],"limit":5,"offset":5}`))
	if err != nil || !result.Success || string(result.Payload) != "### «вьюжница» (any): 1 абзацев в 1 главах\n" {
		t.Fatalf("search result = %v, %q, %v", result.Success, result.Payload, err)
	}
	want := service.SearchInput{Queries: []string{"вьюжница", "жрица"}, Match: "all", Order: "story", From: "10", Books: []int{2}, Limit: 5, Offset: 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("search input = %#v", got)
	}
}

func TestInvalidArgumentsStayCorrectable(t *testing.T) {
	handler := NewHandler(readServiceStub{}, memoryStub{})
	for _, test := range []agent.ToolCall{
		call("read", `{"chapter":"1","page":2}`),
		call("read", `{"chapter":1}`),
		call("search", `{"queries":["a"]}{}`),
		call("summary", `{}`),
		{Caller: agent.ToolCaller{ThreadID: "root", RootThreadID: "root"}, TurnID: "turn", Namespace: "source", Tool: "read", Arguments: json.RawMessage(`{}`)},
	} {
		result, err := handler.Handle(context.Background(), test)
		if err != nil {
			t.Fatal(err)
		}
		if decoded := failure(t, result); decoded.Status != "error" || decoded.Type != "invalid_arguments" {
			t.Fatalf("%s.%s(%s) = %#v", test.Namespace, test.Tool, test.Arguments, decoded)
		}
	}
	result, _ := NewHandler(readServiceStub{err: &service.ArgumentError{Path: "$.chapter", Message: "глава \"999\" не найдена"}}, memoryStub{}).
		Handle(context.Background(), call("read", `{"chapter":"999"}`))
	if decoded := failure(t, result); decoded.Path != "$.chapter" || decoded.Message != "глава \"999\" не найдена" {
		t.Fatalf("argument error = %#v", decoded)
	}
}

func TestLostAuthorityIsTerminalAndChangedTextIsRetryable(t *testing.T) {
	for _, test := range []struct {
		err          error
		status, kind string
	}{
		{researchdomain.ErrForbidden, "terminal", "authority_lost"},
		{researchdomain.ErrConflict, "terminal", "authority_lost"},
		{researchdomain.ErrStaleSource, "error", "source_changed"},
		{sagamemory.ErrTextChanged, "error", "source_changed"},
		{sagamemory.ErrSearchNotReady, "terminal", "saga_not_ready"},
		{errors.New("db down"), "error", "read_failed"},
	} {
		result, err := NewHandler(readServiceStub{err: test.err}, memoryStub{}).Handle(context.Background(), call("chapters", `{}`))
		if err != nil {
			t.Fatal(err)
		}
		if decoded := failure(t, result); decoded.Status != test.status || decoded.Type != test.kind {
			t.Fatalf("%v -> %#v", test.err, decoded)
		}
	}
}

// specs/2026-11-07-desktop-app.md DA9.2: a tool of the build gets the raw arguments of its call, its answer may
// carry an image, an argument error and a typed outcome are answered as the chat's own; a tool outside the set is
// refused.
func TestBuildToolRunsWithTheChatsAnswers(t *testing.T) {
	var got json.RawMessage
	var answer error
	tool := Tool{Spec: agent.ToolSpec{Namespace: "saga", Name: "extra"}, Run: func(_ context.Context, _ service.ToolContext, arguments json.RawMessage) (service.ToolOutput, error) {
		got = arguments
		return service.ToolOutput{Text: "Рисунок 3: Кай у костра.\n", Image: []byte("jpeg")}, answer
	}}
	handler := NewHandler(readServiceStub{}, memoryStub{}, tool)
	result, err := handler.Handle(context.Background(), call("extra", `{"image":3}`))
	if err != nil || !result.Success || string(result.Payload) != "Рисунок 3: Кай у костра.\n" || result.ImageURL != "data:image/jpeg;base64,anBlZw==" ||
		string(got) != `{"image":3}` {
		t.Fatalf("extra = %+v, %v, arguments %s", result, err, got)
	}
	answer = &service.ArgumentError{Path: "$", Message: "аргументы не соответствуют схеме extra"}
	if failed := failure(t, mustHandle(t, handler, call("extra", `{}`))); failed.Type != "invalid_arguments" || failed.Path != "$" {
		t.Fatalf("an argument error = %+v", failed)
	}
	answer = &service.ToolOutcome{Type: "variant_not_chosen", Message: "Рисунок 3 — вариант, который автор не выбрал.", Next: []string{"continue"}}
	if failed := failure(t, mustHandle(t, handler, call("extra", `{}`))); failed.Type != "variant_not_chosen" ||
		!reflect.DeepEqual(failed.NextValidActions, []string{"continue"}) {
		t.Fatalf("a typed outcome = %+v", failed)
	}
	for _, name := range []string{"image", "draw", "reference"} {
		if failed := failure(t, mustHandle(t, NewHandler(readServiceStub{}, memoryStub{}), call(name, `{"image":3}`))); failed.Type != "invalid_arguments" {
			t.Fatalf("saga.%s without the build's tool = %+v", name, failed)
		}
	}
}

func mustHandle(t *testing.T, handler *Handler, toolCall agent.ToolCall) agent.ToolResult {
	t.Helper()
	result, err := handler.Handle(context.Background(), toolCall)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type memoryStub struct {
	remember *service.RememberInput
	memory   *service.MemoryInput
}

func (stub memoryStub) Remember(_ context.Context, _ service.ToolContext, input service.RememberInput) (string, error) {
	if stub.remember != nil {
		*stub.remember = input
	}
	return "Запись сохранена", nil
}

func (stub memoryStub) Memory(_ context.Context, _ service.ToolContext, input service.MemoryInput) (string, error) {
	if stub.memory != nil {
		*stub.memory = input
	}
	return "Записей: 0.", nil
}

// specs/2026-11-01-writer-assistant-index.md, UC4: the memory tools decode their closed arguments.
func TestMemoryToolsDecodeTheirArguments(t *testing.T) {
	var remember service.RememberInput
	var memory service.MemoryInput
	handler := NewHandler(readServiceStub{}, memoryStub{remember: &remember, memory: &memory})
	result, err := handler.Handle(context.Background(), call("remember", `{"kind":"plan","text":"Кира узнает","chapter":"12","name":"Кира"}`))
	if err != nil || !result.Success || remember != (service.RememberInput{Kind: "plan", Text: "Кира узнает", Chapter: "12", Name: "Кира"}) {
		t.Fatalf("remember = %+v, %+v, %v", remember, result, err)
	}
	result, err = handler.Handle(context.Background(), call("memory", `{"query":"Кира","kind":"plan"}`))
	if err != nil || !result.Success || memory != (service.MemoryInput{Query: "Кира", Kind: "plan"}) {
		t.Fatalf("memory = %+v, %+v, %v", memory, result, err)
	}
	for _, name := range []string{"remember", "memory"} {
		result, err = handler.Handle(context.Background(), call(name, `{"unknown":1}`))
		if err != nil || result.Success {
			t.Fatalf("%s took an unknown argument: %+v, %v", name, result, err)
		}
	}
}

// specs/2026-11-07-chat-attach-chapter.md CA3.2: the chapter attached to a question is read by the chat only — the helpers
// that read the saga with the chat's tools are not offered it — and its arguments are those of its schema.
func TestAttachmentToolIsTheChatsAndDecodesItsArguments(t *testing.T) {
	for _, spec := range SagaToolSpecs("search", "read", "chapters", "entity", "memory", "attachment") {
		if spec.Name == "attachment" {
			t.Fatal("the helpers are offered the attachment of the chat")
		}
	}
	var got service.AttachmentInput
	handler := NewHandler(readServiceStub{attached: &got}, memoryStub{})
	result, err := handler.Handle(context.Background(), call("attachment", `{"attachment":2,"from":5,"to":9}`))
	if err != nil || !result.Success || got != (service.AttachmentInput{Attachment: 2, From: 5, To: 9}) {
		t.Fatalf("attachment = %+v, %v, %+v", result, err, got)
	}
	if bad := failure(t, mustHandle(t, handler, call("attachment", `{"chapter":"481"}`))); bad.Type != "invalid_arguments" {
		t.Fatalf("arguments outside the schema = %+v", bad)
	}
}
