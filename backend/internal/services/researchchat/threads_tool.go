package researchchat

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// maxThreadsOutputRunes bounds one answer of the tool: the registry of a long saga fits, the threads of
// all its books do not and are narrowed by query, who or kind.
const maxThreadsOutputRunes = 150000

// ThreadLayer is the port the tool reads the finished thread layer of a project through, nil without
// one. Without withBooks the threads and arcs of the books may be left out.
type ThreadLayer interface {
	SagaThreads(ctx context.Context, projectID string, withBooks bool) (*sagalayer.Layer, error)
}

// ThreadsInput selects from the layer (specs/2026-11-01-writer-assistant-index.md, R2.7).
type ThreadsInput struct {
	Query string
	Who   string
	Kind  string
	Level string
	Part  string
}

// Threads shows the thread layer of the saga: the registry of open threads, with level=all also the
// threads of the books it does not hold, with part=arcs the arcs by book. A book-scoped chat sees what
// the call of its book extracted: the registry is about the whole saga. It names up to which chapter the
// layer was built and how many are newer, and retains the chapters it shows.
func (tools *ReadTools) Threads(ctx context.Context, call ToolContext, input ThreadsInput) (string, error) {
	for path, value := range map[string]string{"$.query": input.Query, "$.who": input.Who, "$.kind": input.Kind} {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxQueryRunes {
			return "", argumentError(path, "до %d символов", maxQueryRunes)
		}
	}
	if input.Level != "" && input.Level != "all" {
		return "", argumentError("$.level", "допустимо all")
	}
	if input.Part != "" && input.Part != "arcs" {
		return "", argumentError("$.part", "допустимо arcs")
	}
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		scoped := saga.authority.Scope == researchdomain.ScopeBook
		layer, err := tools.layer.SagaThreads(ctx, saga.authority.ProjectID, scoped || input.Level == "all" || input.Part == "arcs")
		if err != nil {
			return "", err
		}
		if layer == nil {
			return "Нитей и арок пока нет: слой саги ещё не собран. Ищите через chapters и search, полный просмотр — scan.", nil
		}
		view := threadsView{saga: saga, chapters: map[string]*sagamemory.Chapter{}}
		for i := range saga.version().Chapters {
			view.chapters[saga.version().Chapters[i].ID] = &saga.version().Chapters[i]
		}
		matcher, who, err := newThreadMatcher(saga, input)
		if err != nil {
			return "", err
		}
		if input.Part == "arcs" {
			return view.arcs(layer, matcher, who), nil
		}
		return view.threads(layer, input, matcher, who), nil
	})
}

// newThreadMatcher resolves who through the glossary, so any name of the hero finds him. The words of
// what is searched for are compared by their beginnings: the records are in any inflection.
func newThreadMatcher(saga *sagaScope, input ThreadsInput) (*threadMatcher, string, error) {
	matcher := &threadMatcher{kind: sagamemory.Normalize(strings.TrimSpace(input.Kind)), query: searchWords(input.Query)}
	who := strings.TrimSpace(input.Who)
	if who == "" {
		return matcher, "", nil
	}
	cards, err := saga.cards(0, -1, sagamemory.CardParts{Mentions: true})
	if err != nil {
		return nil, "", err
	}
	glossary := cards.Glossary
	if saga.authority.Scope == researchdomain.ScopeBook {
		glossary = sagamemory.BuildGlossary(saga.scopedCards(cards), cards.Names)
	}
	entry, ok := glossary.Lookup(who)
	if !ok {
		matcher.names = [][]wordPart{searchWords(who)}
		return matcher, fmt.Sprintf("«%s» нет в словаре, искал это написание; имена проверьте через glossary.", who), nil
	}
	saga.useEntry(entry)
	for _, key := range entry.Keys() {
		matcher.names = append(matcher.names, searchWords(key))
	}
	named := entry.Name
	if len(entry.Aliases) > 0 {
		named += ", также: " + strings.Join(entry.Aliases, ", ")
	}
	return matcher, "Нити героя: " + named + ".", nil
}

// threadMatcher says whether a record is about what was asked.
type threadMatcher struct {
	kind  string
	query []wordPart
	names [][]wordPart // the words of each name of the hero
}

func (matcher *threadMatcher) matches(text, kind string) bool {
	if matcher.kind != "" && sagamemory.Normalize(strings.TrimSpace(kind)) != matcher.kind {
		return false
	}
	words := textWords(text)
	if !containsWords(words, matcher.query) {
		return false
	}
	if len(matcher.names) == 0 {
		return true
	}
	for _, name := range matcher.names {
		if len(name) > 0 && containsWords(words, name) {
			return true
		}
	}
	return false
}

func textWords(text string) []string {
	tokens := sagamemory.Tokenize(sagamemory.Normalize(text))
	words := make([]string, len(tokens))
	for i, token := range tokens {
		words[i] = token.Word
	}
	return words
}

// wordPart is a word of what is searched for: the beginning of a word in a record, or the whole word
// when it is too short to have an ending to cut: «Ян» does not find «янтарь».
type wordPart struct {
	text  string
	whole bool
}

// searchWords cuts the possible ending of each word, so «дороге» finds «дорогу».
func searchWords(text string) []wordPart {
	words := textWords(text)
	parts := make([]wordPart, len(words))
	for i, word := range words {
		runes := []rune(word)
		switch {
		case len(runes) > 4:
			parts[i] = wordPart{text: string(runes[:len(runes)-2])}
		case len(runes) == 4:
			parts[i] = wordPart{text: string(runes[:3])}
		default:
			parts[i] = wordPart{text: word, whole: true}
		}
	}
	return parts
}

func containsWords(words []string, wanted []wordPart) bool {
next:
	for _, part := range wanted {
		for _, word := range words {
			if word == part.text || !part.whole && strings.HasPrefix(word, part.text) {
				continue next
			}
		}
		return false
	}
	return true
}

// threadsView writes the answer in the chapter keys of the current text version.
type threadsView struct {
	saga     *sagaScope
	chapters map[string]*sagamemory.Chapter
}

// place names one reference and retains its chapter; a chapter not in the scope of the call stays out.
func (view *threadsView) place(ref sagalayer.Ref) string {
	chapter, ok := view.chapters[ref.Chapter]
	if !ok || !view.saga.inScope(chapter) {
		return ""
	}
	view.saga.use(chapter)
	paragraph := ref.P
	if paragraph > chapter.ParagraphCount {
		paragraph = 0
	}
	return chapter.Ref() + paragraphMark(paragraph)
}

func (view *threadsView) places(refs []sagalayer.Ref) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if place := view.place(ref); place != "" {
			parts = append(parts, place)
		}
	}
	return strings.Join(parts, ", ")
}

// freshness says up to which chapter the layer was built and how many chapters are newer or edited.
func (view *threadsView) freshness(layer *sagalayer.Layer) string {
	built := make(map[string]string, len(layer.Chapters))
	for _, stamp := range layer.Chapters {
		built[stamp.Chapter] = stamp.Hash
	}
	var last *sagamemory.Chapter
	newer := 0
	for i := range view.saga.version().Chapters {
		chapter := &view.saga.version().Chapters[i]
		if !view.saga.inScope(chapter) {
			continue
		}
		hash, ok := built[chapter.ID]
		if ok && hash == chapter.ContentHash {
			last = chapter
			continue
		}
		newer++
	}
	if last == nil {
		return fmt.Sprintf("в слое нет глав этого чата, глав новее: %d", newer)
	}
	return fmt.Sprintf("слой собран по %s, глав новее: %d", last.Ref(), newer)
}

type threadLine struct {
	marks  []string
	thread sagalayer.Thread
}

func (view *threadsView) threads(layer *sagalayer.Layer, input ThreadsInput, matcher *threadMatcher, who string) string {
	var lines []threadLine
	major := 0
	scoped := view.saga.authority.Scope == researchdomain.ScopeBook
	if !scoped {
		for _, weight := range []string{sagalayer.WeightMajor, sagalayer.WeightMinor} {
			for _, thread := range layer.Threads {
				if thread.Weight == weight && matcher.matches(thread.Text, thread.Kind) {
					line := threadLine{thread: thread}
					if weight == sagalayer.WeightMajor {
						line.marks = append(line.marks, "важная")
						major++
					}
					lines = append(lines, line)
				}
			}
		}
	}
	if scoped || input.Level == "all" {
		for _, thread := range bookThreads(layer, view.saga, scoped) {
			if matcher.matches(thread.Text, thread.Kind) {
				marks := []string{fmt.Sprintf("по книге, могло закрыться; т. %d", thread.Book)}
				lines = append(lines, threadLine{marks: marks, thread: thread.Thread})
			}
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Нитей: %d (важных %d); %s.\n", len(lines), major, view.freshness(layer))
	if scoped {
		out.WriteString("Чат одной книги: нити только этой книги; реестр всей саги здесь не показывается.\n")
	}
	if who != "" {
		out.WriteString(who + "\n")
	}
	out.WriteString("Нить — навигация, а не факт: проверяйте её в тексте через read по ссылке.\n")
	written := utf8.RuneCountInString(out.String())
	for i, line := range lines {
		text := view.line(line)
		if written += utf8.RuneCountInString(text); written > maxThreadsOutputRunes {
			fmt.Fprintf(&out, "… и ещё %d; сузьте query, who или kind.\n", len(lines)-i)
			break
		}
		out.WriteString(text)
	}
	return out.String()
}

// bookThreads are the threads of the books a call shows: of a book-scoped chat all the open threads of
// its book, otherwise those the registry does not hold.
func bookThreads(layer *sagalayer.Layer, saga *sagaScope, scoped bool) []sagalayer.BookThread {
	if !scoped {
		return layer.BookOnly()
	}
	var threads []sagalayer.BookThread
	for _, book := range layer.Books {
		if book.ID != saga.authority.BookID {
			continue
		}
		for _, thread := range book.Threads {
			if !thread.Closed {
				threads = append(threads, sagalayer.BookThread{Thread: thread, Book: book.Order})
			}
		}
	}
	return threads
}

func (view *threadsView) line(line threadLine) string {
	marks := append([]string(nil), line.marks...)
	if line.thread.Kind != "" {
		marks = append(marks, line.thread.Kind)
	}
	var out strings.Builder
	out.WriteString("- ")
	if len(marks) > 0 {
		fmt.Fprintf(&out, "[%s] ", strings.Join(marks, ", "))
	}
	out.WriteString(line.thread.Text)
	var where []string
	if first := view.places(line.thread.First); first != "" {
		where = append(where, "возникла "+first)
	}
	if last := view.places(line.thread.Last); last != "" {
		where = append(where, "последний раз "+last)
	}
	if len(where) > 0 {
		out.WriteString(" — " + strings.Join(where, ", "))
	}
	out.WriteString("\n")
	return out.String()
}

func (view *threadsView) arcs(layer *sagalayer.Layer, matcher *threadMatcher, who string) string {
	var body strings.Builder
	total := 0
	for _, book := range layer.Books {
		if view.saga.authority.Scope == researchdomain.ScopeBook && book.ID != view.saga.authority.BookID {
			continue
		}
		header := false
		for _, arc := range book.Arcs {
			if !matcher.matches(arc.Title+" "+arc.Gist+" "+arc.Outcome, "") {
				continue
			}
			if !header {
				fmt.Fprintf(&body, "Том %d:\n", book.Order)
				header = true
			}
			total++
			fmt.Fprintf(&body, "- %s", arc.Title)
			from, to := view.place(arc.From), view.place(arc.To)
			if from != "" || to != "" {
				fmt.Fprintf(&body, " (%s – %s)", from, to)
			}
			if arc.Gist != "" {
				fmt.Fprintf(&body, ": %s", arc.Gist)
			}
			if arc.Outcome != "" {
				fmt.Fprintf(&body, " Итог: %s", arc.Outcome)
			}
			body.WriteString("\n")
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Арок: %d; %s.\n", total, view.freshness(layer))
	if who != "" {
		out.WriteString(who + "\n")
	}
	out.WriteString(body.String())
	return out.String()
}
