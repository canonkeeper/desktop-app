package sagalayer

import (
	"fmt"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/sagamemory"
)

func refsVersion() *sagamemory.Version {
	return sagamemory.NewVersion("p", "draft", "fp", []sagamemory.Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, Order: 1, Title: "Глава 312. Начало"},
		{ID: "c2", BookID: "b1", BookOrder: 1, Order: 2, Title: "Пролог"},
	})
}

// R2.5: a reference of the model is a chapter key with a paragraph; what is stored survives a shift of keys.
func TestParseRefsResolvesKeysToChapters(t *testing.T) {
	version := refsVersion()
	for text, want := range map[string][]Ref{
		"312 ¶5":           {{Chapter: "c1", P: 5}},
		"гл.312 ¶ 5":       {{Chapter: "c1", P: 5}},
		"b1c2":             {{Chapter: "c2"}},
		"B1C2 ¶7, 312 ¶1":  {{Chapter: "c2", P: 7}, {Chapter: "c1", P: 1}},
		"¶5":               nil,
		"глава неизвестна": nil,
		"999 ¶1":           nil,
	} {
		got := ParseRefs(text, version)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("ParseRefs(%q) = %v, want %v", text, got, want)
		}
	}
	if got := RenderRefs([]Ref{{Chapter: "c2", P: 7}, {Chapter: "c1"}, {Chapter: "gone", P: 1}}, version); got != "b1c2 ¶7, 312" {
		t.Fatalf("RenderRefs() = %q", got)
	}
}

// pipe-final.py ct(): the exact digest of a card fed to the map call, with the real chapter key.
func TestChapterDigestIsWhatWasMeasured(t *testing.T) {
	chapter := sagamemory.Chapter{Key: "b1c2", Title: "Пролог"}
	card := sagamemory.Card{
		Summary: "Пересказ.",
		Events: []sagamemory.Event{
			{Event: "мимо", P: 1}, {Event: "главное", P: 2, Key: true},
		},
		Hooks: []sagamemory.Hook{{What: "странный ключ", P: 3}},
		Knows: []sagamemory.Knowledge{
			{Who: "Мирон", Kind: "скрывает", What: "имя"}, {Who: "Любава", Kind: "узнал", What: "дорогу"},
		},
		Facts: []sagamemory.Fact{
			{Subject: "Мирон", Attribute: "Цель", Value: "дойти"}, {Subject: "Мирон", Attribute: "цель", Value: "старое", Past: true},
			{Subject: "Любава", Attribute: "возраст", Value: "20"},
		},
		Changes: []sagamemory.Change{{Subject: "Любава", Change: "ранена"}},
	}
	want := "### b1c2 «Пролог»\nПЕРЕСКАЗ: Пересказ.\nГЛАВНЫЕ СОБЫТИЯ: ¶2 главное\nОСТАЛОСЬ ОТКРЫТЫМ: ¶3 странный ключ\n" +
		"КТО ЧТО СКРЫВАЕТ ИЛИ НЕ ЗНАЕТ: Мирон скрывает: имя\nНАМЕРЕНИЯ, ОБЯЗАТЕЛЬСТВА: Мирон: Цель = дойти\nИЗМЕНЕНИЯ: Любава: ранена"
	if got := ChapterDigest(&chapter, &card); got != want {
		t.Fatalf("ChapterDigest() =\n%s\nwant\n%s", got, want)
	}
	var events []sagamemory.Event
	for i := range 20 {
		events = append(events, sagamemory.Event{Event: fmt.Sprintf("e%d", i), P: i})
	}
	noKey := ChapterDigest(&chapter, &sagamemory.Card{Events: events})
	if !strings.Contains(noKey, "¶14 e14\n") || strings.Contains(noKey, "e15") {
		t.Fatalf("a card without main events shows its first 15: %s", noKey)
	}
}

// pipe-final.py evidence(): the two first and the last chapter of the record, each up to 30 000
// runes, and up to 40 later chapters that share enough words.
func TestEvidenceIsWhatWasMeasured(t *testing.T) {
	var corpus []Source
	for i := range 60 {
		summary := "ничего особенного"
		if i > 2 {
			summary = "серебряный медальон потерян хозяином"
		}
		corpus = append(corpus, Source{
			Title: fmt.Sprintf("Глава %d", i), Chapter: fmt.Sprintf("c%d", i),
			Card: &sagamemory.Card{Summary: summary, Events: []sagamemory.Event{{Event: "ключевое", Key: true}, {Event: "прочее"}}, Hooks: []sagamemory.Hook{{What: "открыто"}}},
		})
	}
	index := NewEvidenceIndex(corpus)
	thread := Thread{
		Text: "Серебряный медальон потерян", First: []Ref{{Chapter: "c0", P: 1}},
		Last: []Ref{{Chapter: "c1", P: 2}, {Chapter: "c2", P: 3}},
	}
	wanted := index.Chapters(thread)
	if fmt.Sprint(wanted) != "[0 1 2]" {
		t.Fatalf("Chapters() = %v", wanted)
	}
	texts := map[int]string{
		0: "=== Глава 0 ===\n" + strings.Repeat("я", 30000),
		1: "=== Глава 1 ===\nтекст", 2: "=== Глава 2 ===\nтекст",
	}
	got := index.Evidence(thread, "c0 ¶1", "c1 ¶2, c2 ¶3", texts)
	head := "ЗАПИСЬ: Серебряный медальон потерян (возникла c0 ¶1, последний раз c1 ¶2, c2 ¶3)\n\nТЕКСТ ГЛАВ:\n=== Глава 0 ===\n"
	if !strings.HasPrefix(got, head) || !strings.Contains(got, "\n\n=== Глава 1 ===\nтекст\n\n=== Глава 2 ===\nтекст\n\nПОЗДНЕЙШИЕ ГЛАВЫ:\n") {
		t.Fatalf("evidence starts with %q", got[:min(len(got), 200)])
	}
	if strings.Count(got, "--- Глава") != 40 || !strings.Contains(got, "--- Глава 59\nсеребряный медальон потерян хозяином\nСОБЫТИЯ: ключевое\nИЗМЕНЕНИЯ: \nОТКРЫТО: открыто") {
		t.Fatalf("later chapters: %d in %q", strings.Count(got, "--- Глава"), got[len(got)-200:])
	}
	if strings.Contains(got, "--- Глава 2\n") || strings.Contains(got, "--- Глава 19\n") || !strings.Contains(got, "--- Глава 20\n") {
		t.Fatal("later chapters must come after the last one of the record, the 40 best with the latest on a tie, in reading order")
	}
	if got := ChapterEvidenceText("Глава", []string{"раз", "два"}); got != "=== Глава ===\nраз\nдва" {
		t.Fatalf("ChapterEvidenceText() = %q", got)
	}
	if got := ChapterEvidenceText("Г", []string{strings.Repeat("щ", 30005)}); len([]rune(got)) != len([]rune("=== Г ===\n"))+30000 {
		t.Fatalf("a chapter is cut at 30 000 runes, got %d", len([]rune(got)))
	}
	if got := index.Chapters(Thread{Text: "нить без ссылок"}); len(got) != 0 {
		t.Fatalf("a record without references reads no chapter text: %v", got)
	}
	repeated := Thread{Text: "т", First: []Ref{{Chapter: "c5"}, {Chapter: "c5"}}, Last: []Ref{{Chapter: "c7"}, {Chapter: "c8"}, {Chapter: "c9"}}}
	if got := index.Chapters(repeated); fmt.Sprint(got) != "[5 7 9]" {
		t.Fatalf("Chapters() = %v, want the two first and the last", got)
	}
}

func TestParseBookReadsAnswersLeniently(t *testing.T) {
	version := refsVersion()
	output := "Вот:\n```json\n" + `{"arcs":[{"title":"Осада","from":"312","to":"b1c2","gist":"Суть","outcome":"Итог"},{"title":""}],
	"threads":[{"thread":"Кто убил","kind":"тайна","first":"312 ¶2","last":"b1c2 ¶3","status":"open","continues_older":"нет"},
	{"thread":"Клятва","first":12,"last":null,"status":"closed","closed_how":"исполнена"},{"thread":"  "}]}` + "\n```"
	book, err := ParseBook(output, version)
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Arcs) != 1 || book.Arcs[0].Title != "Осада" || book.Arcs[0].From.Chapter != "c1" || book.Arcs[0].To.Chapter != "c2" {
		t.Fatalf("arcs = %+v", book.Arcs)
	}
	if len(book.Threads) != 2 || book.Threads[0].Closed || book.Threads[0].First[0] != (Ref{Chapter: "c1", P: 2}) ||
		!book.Threads[1].Closed || book.Threads[1].ClosedHow != "исполнена" {
		t.Fatalf("threads = %+v", book.Threads)
	}
	if _, err := ParseBook(`{"arcs":[]}`, version); err == nil {
		t.Fatal("an answer without threads is asked again")
	}
}

func TestParseRegistryAndVerdict(t *testing.T) {
	version := refsVersion()
	threads, err := ParseRegistry(`{"open":[{"thread":"Нить","kind":"долг","first":"312","last":"b1c2 ¶1","weight":"MAJOR"},{"thread":"Вторая","weight":"что"}]}`, version)
	if err != nil || len(threads) != 2 || threads[0].Weight != WeightMajor || threads[1].Weight != WeightMinor {
		t.Fatalf("ParseRegistry() = %+v, %v", threads, err)
	}
	if _, err := ParseRegistry(`{"closed":[]}`, version); err == nil {
		t.Fatal("an answer without open is asked again")
	}
	verdict, err := ParseVerdict(`{"v":"fix","thread":"Точнее","why":"коротко"}`)
	if err != nil || verdict.Decision != "fix" || verdict.Text != "Точнее" {
		t.Fatalf("ParseVerdict() = %+v, %v", verdict, err)
	}
	if _, err := ParseVerdict(`нет JSON`); err == nil {
		t.Fatal("no JSON is asked again")
	}
}

// R2.4: keep leaves the record, fix replaces it, drop removes it, and no verdict leaves it as it is.
func TestApplyVerdicts(t *testing.T) {
	threads := []Thread{{Text: "a"}, {Text: "b"}, {Text: "c"}, {Text: "d"}, {Text: "e"}, {Text: "f"}}
	got := ApplyVerdicts(threads, map[int]Verdict{
		0: {Decision: "keep"}, 1: {Decision: "fix", Text: "bb"}, 2: {Decision: "drop"},
		3: {Decision: "fix"}, 4: {Failed: true, Decision: "keep"},
	})
	if len(got) != 5 || got[0].Text != "a" || got[1].Text != "bb" || got[2].Text != "d" || got[3].Text != "e" || got[4].Text != "f" {
		t.Fatalf("ApplyVerdicts() = %+v", got)
	}
}

// R2.2: not less than 5 chapters or 3 % of them since the last build, at least two; a chapter counts once.
func TestDue(t *testing.T) {
	stamps := func(total int) []Stamp {
		out := make([]Stamp, total)
		for i := range out {
			out[i] = Stamp{Chapter: fmt.Sprintf("c%d", i), Hash: "h", CardVersion: 4}
		}
		return out
	}
	if !Due(stamps(1), nil) {
		t.Fatal("no build yet: due")
	}
	if Due(nil, nil) || Due([]Stamp{{Chapter: "c0", Hash: "h"}}, nil) {
		t.Fatal("no cards: nothing to build")
	}
	current := stamps(641)
	built := append([]Stamp(nil), current...)
	if Due(current, built) {
		t.Fatal("nothing changed")
	}
	for i := range 4 {
		current[i].Hash = "new"
	}
	if Due(current, built) {
		t.Fatal("4 of 641 chapters wait")
	}
	current[4].CardVersion = 5
	if !Due(current, built) {
		t.Fatal("5 chapters, the last with a new card of its old text: due")
	}
	// specs/2026-10-27-update-book-from-file.md R5.1: one changed chapter never rebuilds the threads, also in a
	// small saga, where it is over 3 %; two are enough there.
	short := stamps(30)
	shortBuilt := append([]Stamp(nil), short...)
	short[0].Hash = "new"
	if Due(short, shortBuilt) {
		t.Fatal("1 of 30 chapters is over 3 %, but one chapter waits")
	}
	if tiny := stamps(1); Due([]Stamp{{Chapter: "c0", Hash: "new", CardVersion: 4}}, tiny) {
		t.Fatal("the one chapter of a saga waits for a second one")
	}
	short[1].Hash = "new"
	if !Due(short, shortBuilt) {
		t.Fatal("2 of 30 chapters: due")
	}
	// R3.3: a card carried over a change of signs is the card the build read.
	carried := stamps(30)
	carriedBuilt := append([]Stamp(nil), carried...)
	for i := range 6 {
		carried[i].Card, carried[i].Hash = carried[i].Hash, fmt.Sprintf("signs%d", i)
	}
	if Due(carried, carriedBuilt) || Digest(carried) != Digest(carriedBuilt) {
		t.Fatal("cards carried over changes of signs are not new cards")
	}
	long := stamps(100)
	longBuilt := append([]Stamp(nil), long...)
	long[0].Hash, long[1].Hash = "new", "new"
	if Due(long, longBuilt) {
		t.Fatal("2 of 100 chapters is under 3 % and under 5")
	}
	long[2].Hash = "new"
	if !Due(long, longBuilt) {
		t.Fatal("3 of 100 chapters is 3 %")
	}
	withoutCard := stamps(2)
	withoutCardBuilt := append([]Stamp(nil), withoutCard...)
	withoutCard[0].CardVersion = 0
	withoutCard[0].Hash = "new"
	if Due(withoutCard, withoutCardBuilt) {
		t.Fatal("a chapter without a card yet waits for it")
	}
}

// specs/2026-10-27-update-book-from-file.md R6.2: the preview tells by the same rule whether the threads will be rebuilt.
func TestDueAfter(t *testing.T) {
	for _, check := range []struct {
		changed, total int
		built, want    bool
	}{
		{0, 10, true, false}, {1, 10, false, true}, {1, 10, true, false}, {1, 20, true, false},
		{2, 30, true, true}, {2, 100, true, false}, {3, 100, true, true}, {5, 645, true, true}, {4, 645, true, false},
	} {
		if got := DueAfter(check.changed, check.total, check.built); got != check.want {
			t.Fatalf("DueAfter(%d of %d, built %v) = %v", check.changed, check.total, check.built, got)
		}
	}
}

// R2.3: the digest of a book depends on its chapter texts, their cards and the instructions; the
// digest of a layer on all of them.
func TestDigests(t *testing.T) {
	base := []Stamp{{Chapter: "c1", Hash: "h1", CardVersion: 4}, {Chapter: "c2", Hash: "h2", CardVersion: 4}}
	if Digest(base) != Digest(append([]Stamp(nil), base...)) || len(Digest(base)) != 64 {
		t.Fatal("the digest is a stable sha256")
	}
	edited := append([]Stamp(nil), base...)
	edited[1].Hash = "h3"
	carded := append([]Stamp(nil), base...)
	carded[0].CardVersion = 5
	reordered := []Stamp{base[1], base[0]}
	for name, other := range map[string][]Stamp{"text": edited, "card": carded, "order": reordered} {
		if Digest(other) == Digest(base) {
			t.Fatalf("a new %s must change the digest", name)
		}
	}
}

// R2.7: a book thread the registry already holds in its own words is not listed again.
func TestBookOnlyLeavesWhatTheRegistryDoesNotHold(t *testing.T) {
	layer := Layer{
		Threads: []Thread{{Text: "Серебряный медальон Мирона потерян возле старой мельницы"}},
		Books: []Book{{Order: 2, Threads: []Thread{
			{Text: "Серебряный медальон потерян возле мельницы"},
			{Text: "Незнакомец обещал вернуться осенью"},
			{Text: "Закрытая история про ярмарку", Closed: true},
		}}},
	}
	only := layer.BookOnly()
	if len(only) != 1 || only[0].Text != "Незнакомец обещал вернуться осенью" || only[0].Book != 2 {
		t.Fatalf("BookOnly() = %+v", only)
	}
}

func TestReduceAndGapInputsAreTheMeasuredLists(t *testing.T) {
	version := refsVersion()
	books := []Book{{Order: 2, Threads: []Thread{
		{Text: "Открытая", Kind: "тайна", First: []Ref{{Chapter: "c1", P: 1}}, Last: []Ref{{Chapter: "c2", P: 2}}},
		{Text: strings.Repeat("з", 200), Closed: true, ClosedHow: strings.Repeat("к", 200)},
	}}}
	reduce := ReduceInput(books, version)
	if !strings.Contains(reduce, `"open":[{"т":2,"first":"312 ¶1","last":"b1c2 ¶2","kind":"тайна","thread":"Открытая"}]`) ||
		!strings.Contains(reduce, `"closed":[{"т":2,"thread":"`+strings.Repeat("з", 160)+`","how":"`+strings.Repeat("к", 160)+`"}]`) {
		t.Fatalf("ReduceInput() = %s", reduce)
	}
	gap := GapInput([]Thread{{Text: "В реестре"}}, books, version)
	if !strings.HasPrefix(gap, `{"registry":["В реестре"],"open":[{"т":2,`) {
		t.Fatalf("GapInput() = %s", gap)
	}
}
