package sagamemory

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func factsOf(prefix string, from, to int) []Fact {
	var facts []Fact
	for number := from; number <= to; number++ {
		facts = append(facts, Fact{Subject: "Застава", Attribute: "примета", Value: fmt.Sprintf("%s %d", prefix, number), P: number})
	}
	return facts
}

func values(facts []Fact) []string {
	var said []string
	for _, fact := range facts {
		said = append(said, fact.Value)
	}
	return said
}

// spreadNames are share of the n items named prefix 1…n spread over them all: the middle of each of share equal stretches
// (LC1.4).
func spreadNames(prefix string, n, share int) []string {
	var said []string
	for stretch := range share {
		said = append(said, fmt.Sprintf("%s %d", prefix, (2*stretch+1)*n/(2*share)+1))
	}
	return said
}

func names(prefix string, from, to int) []string {
	var said []string
	for number := from; number <= to; number++ {
		said = append(said, fmt.Sprintf("%s %d", prefix, number))
	}
	return said
}

// specs/2026-11-17-long-chapters.md LC1.4: the card of a chapter read in parts is merged from the cards of its parts, rule by
// rule and within the bounds of a card.
func TestTheCardsOfThePartsMergeIntoTheCardOfTheChapter(t *testing.T) {
	t.Run("the retellings one per line", func(t *testing.T) {
		card := MergeCards([]Card{{Summary: "Вера пришла на заставу."}, {Summary: "Мирон ушёл к перевалу."}})
		if card.Summary != "Вера пришла на заставу.\nМирон ушёл к перевалу." {
			t.Fatalf("summary = %q", card.Summary)
		}
	})
	t.Run("the retellings longer than a summary, each cut to its share at the end of a sentence", func(t *testing.T) {
		sentence := "Вера ждала у ворот заставы. "
		long := strings.TrimSpace(strings.Repeat(sentence, 300))
		spaced := strings.Repeat("слово ", 2_000) + "конец"
		card := MergeCards([]Card{{Summary: long}, {Summary: long}, {Summary: spaced}})
		lines := strings.Split(card.Summary, "\n")
		share := (12_000 - 2) / 3
		if len(lines) != 3 || utf8.RuneCountInString(card.Summary) > 12_000 {
			t.Fatalf("summary of %d lines, %d runes", len(lines), utf8.RuneCountInString(card.Summary))
		}
		for index, line := range lines[:2] {
			if !strings.HasSuffix(line, "заставы.") || utf8.RuneCountInString(line) > share || utf8.RuneCountInString(line)+utf8.RuneCountInString(sentence) <= share {
				t.Fatalf("retelling %d ends %q, %d runes of a share of %d", index+1, line[len(line)-30:], utf8.RuneCountInString(line), share)
			}
		}
		if last := lines[2]; !strings.HasSuffix(last, "слово") || utf8.RuneCountInString(last) > share || utf8.RuneCountInString(last)+6 <= share {
			t.Fatalf("the retelling without sentences ends %q, %d runes", last[len(last)-20:], utf8.RuneCountInString(last))
		}
		longer := strings.TrimSpace(strings.Repeat(sentence, 420))
		medium := strings.TrimSpace(strings.Repeat("Мирон ушёл. ", 84))
		short := MergeCards([]Card{{Summary: longer}, {Summary: medium}})
		if lines := strings.Split(short.Summary, "\n"); len(lines) != 2 || lines[1] != medium || utf8.RuneCountInString(lines[0]) > (12_000-1)/2 {
			t.Fatalf("a short retelling beside a long one = %d runes, %d", utf8.RuneCountInString(lines[0]), utf8.RuneCountInString(lines[1]))
		}
	})
	t.Run("the time of the chapter is of the first part, without how long it lasts", func(t *testing.T) {
		first := &ChapterTime{Opens: "На рассвете", After: &Span{N: 1, Unit: "день"}, Lasts: &Span{N: 3, Unit: "час"}, Date: "весна", Note: "воспоминание"}
		card := MergeCards([]Card{{Summary: "a", When: first}, {Summary: "b", When: &ChapterTime{Opens: "К вечеру", Date: "лето"}}})
		if want := (&ChapterTime{Opens: "На рассвете", After: &Span{N: 1, Unit: "день"}, Date: "весна", Note: "воспоминание"}); !reflect.DeepEqual(card.When, want) || first.Lasts == nil {
			t.Fatalf("when = %+v", card.When)
		}
		if card := MergeCards([]Card{{Summary: "a"}, {Summary: "b", When: &ChapterTime{Opens: "К вечеру"}}}); card.When != nil {
			t.Fatalf("when of a first part without one = %+v", card.When)
		}
		if card := MergeCards([]Card{{Summary: "a", When: &ChapterTime{Lasts: &Span{N: 2, Unit: "день"}}}, {Summary: "b"}}); card.When != nil {
			t.Fatalf("when that only lasted = %+v", card.When)
		}
	})
	t.Run("the lists one after another without repeats by the keys of Add", func(t *testing.T) {
		card := MergeCards([]Card{
			{Summary: "a", Time: []string{"утро", "Утро"}, Events: []Event{{Event: "Вера пришла", P: 1, Key: true}},
				Facts: []Fact{{Subject: "Мирон", Attribute: "возраст", Value: "24 года", P: 2}}, Changes: []Change{{Subject: "Мирон", Change: "ранен"}},
				Knows: []Knowledge{{Who: "Вера", What: "тайна"}}, Hooks: []Hook{{What: "письмо"}}},
			{Summary: "b", Time: []string{"утро", "вечер"}, Events: []Event{{Event: "вера пришла", P: 300}, {Event: "Мирон ушёл", P: 301}},
				Facts:   []Fact{{Subject: "мирон", Attribute: "возраст", Value: "24 года", P: 302}, {Subject: "Мирон", Attribute: "рост", Value: "высокий"}},
				Changes: []Change{{Subject: "Мирон", Change: "Ранен"}, {Subject: "Вера", Change: "ушла"}}, Knows: []Knowledge{{Who: "вера", What: "Тайна"}},
				Hooks: []Hook{{What: "Письмо"}, {What: "шрам"}}},
		})
		want := Card{
			Summary: "a\nb", Time: []string{"утро", "вечер"},
			Events:  []Event{{Event: "Вера пришла", P: 1, Key: true}, {Event: "Мирон ушёл", P: 301}},
			Facts:   []Fact{{Subject: "Мирон", Attribute: "возраст", Value: "24 года", P: 2}, {Subject: "Мирон", Attribute: "рост", Value: "высокий"}},
			Changes: []Change{{Subject: "Мирон", Change: "ранен"}, {Subject: "Вера", Change: "ушла"}},
			Knows:   []Knowledge{{Who: "Вера", What: "тайна"}}, Hooks: []Hook{{What: "письмо"}, {What: "шрам"}},
		}
		if !reflect.DeepEqual(card, want) {
			t.Fatalf("merged = %+v\nwant %+v", card, want)
		}
	})
	t.Run("a list longer than its bound: shares by turns, each spread over its part, the repeats out first", func(t *testing.T) {
		even := MergeCards([]Card{{Summary: "a", Facts: factsOf("первая", 1, 300)}, {Summary: "b", Facts: factsOf("вторая", 1, 300)}})
		if want := append(spreadNames("первая", 300, 200), spreadNames("вторая", 300, 200)...); !reflect.DeepEqual(values(even.Facts), want) {
			t.Fatalf("400 of 600 facts = %d: %v…", len(even.Facts), values(even.Facts)[:3])
		}
		uneven := MergeCards([]Card{{Summary: "a", Facts: factsOf("первая", 1, 50)}, {Summary: "b", Facts: factsOf("вторая", 1, 500)}})
		if want := append(names("первая", 1, 50), spreadNames("вторая", 500, 350)...); !reflect.DeepEqual(values(uneven.Facts), want) {
			t.Fatalf("400 of 50 and 500 facts = %d", len(uneven.Facts))
		}
		second := append(factsOf("первая", 1, 100), factsOf("вторая", 1, 200)...)
		repeats := MergeCards([]Card{{Summary: "a", Facts: factsOf("первая", 1, 300)}, {Summary: "b", Facts: second}})
		if want := append(spreadNames("первая", 300, 200), names("вторая", 1, 200)...); !reflect.DeepEqual(values(repeats.Facts), want) {
			t.Fatalf("the repeats were taken by turns: %d facts", len(repeats.Facts))
		}
		var times [2][]string
		times[0], times[1] = names("утро", 1, 150), names("вечер", 1, 150)
		timed := MergeCards([]Card{{Summary: "a", Time: times[0]}, {Summary: "b", Time: times[1]}})
		if want := append(spreadNames("утро", 150, 100), spreadNames("вечер", 150, 100)...); !reflect.DeepEqual(timed.Time, want) {
			t.Fatalf("time markers = %d, want 200", len(timed.Time))
		}
	})
	t.Run("a part over its share keeps its whole story, not its beginning", func(t *testing.T) {
		// The chapter of production of 17.11 (839 thousand characters, three parts): the first share of each part's events
		// left the middle and the end of each part without one.
		var parts []Card
		for part := range 3 {
			var events []Event
			for number := 1; number <= 300; number++ {
				events = append(events, Event{Event: fmt.Sprintf("часть %d, событие %d", part+1, number), P: part*1000 + number})
			}
			parts = append(parts, Card{Summary: "a", Events: events})
		}
		card := MergeCards(parts)
		last := map[int]int{}
		for _, event := range card.Events {
			if part := event.P / 1000; event.P%1000 > last[part] {
				last[part] = event.P % 1000
			}
		}
		for part := range 3 {
			if len(card.Events) != 400 || last[part] < 290 {
				t.Fatalf("%d events, part %d read to its event %d of 300", len(card.Events), part+1, last[part])
			}
		}
	})
	t.Run("the main events are taken first", func(t *testing.T) {
		var first, second []Event
		for number := 1; number <= 300; number++ {
			first = append(first, Event{Event: fmt.Sprintf("первая %d", number), Key: number > 290})
			second = append(second, Event{Event: fmt.Sprintf("вторая %d", number)})
		}
		card := MergeCards([]Card{{Summary: "a", Events: first}, {Summary: "b", Events: second}})
		var said []string
		keys := 0
		for _, event := range card.Events {
			said = append(said, event.Event)
			if event.Key {
				keys++
			}
		}
		// The ten main ones first, then 195 of the other 290 of the first part and 195 of the 300 of the second, each spread.
		var others []string
		for stretch := range 195 {
			others = append(others, fmt.Sprintf("первая %d", (2*stretch+1)*290/(2*195)+1))
		}
		want := append(append(others, names("первая", 291, 300)...), spreadNames("вторая", 300, 195)...)
		if !reflect.DeepEqual(said, want) || keys != 10 {
			t.Fatalf("events = %d, main %d; begins %v", len(said), keys, said[:3])
		}
	})
	t.Run("a character once by its name, of the part that named it first", func(t *testing.T) {
		card := MergeCards([]Card{
			{Summary: "a", Characters: []Mention{{Name: "Мирон", Aliases: []string{"капитан", "мирон"}, Note: "капитан заставы", P: 40}}},
			{Summary: "b", Characters: []Mention{
				{Name: "Вера", Type: "человек", P: 301},
				{Name: "мирон", Aliases: []string{"Капитан", "Мирон Сергеевич"}, Type: "человек", Note: "Капитан заставы", P: 12},
				{Name: "МИРОН", Note: "ранен у перевала", P: 0},
			}},
		})
		want := []Mention{
			{Name: "Мирон", Aliases: []string{"капитан", "Мирон Сергеевич"}, Type: "человек", Note: "капитан заставы; ранен у перевала", P: 12},
			{Name: "Вера", Type: "человек", P: 301},
		}
		if !reflect.DeepEqual(card.Characters, want) {
			t.Fatalf("characters = %+v\nwant %+v", card.Characters, want)
		}
		var aliases []string
		for number := range 40 {
			aliases = append(aliases, fmt.Sprintf("прозвище %d", number))
		}
		long := strings.Repeat("я", 995)
		bounded := MergeCards([]Card{
			{Summary: "a", Entities: []Mention{{Name: "Застава", Aliases: aliases[:20], Note: long}}},
			{Summary: "b", Entities: []Mention{{Name: "застава", Aliases: aliases[10:], Note: "крепость"}, {Name: "Перевал", Note: "ещё"}}},
		})
		if entity := bounded.Entities[0]; len(entity.Aliases) != 30 || entity.Aliases[29] != "прозвище 29" || entity.Note != long {
			t.Fatalf("an entity of two parts holds %d aliases, a note of %d runes", len(entity.Aliases), utf8.RuneCountInString(entity.Note))
		}
		var many [2][]Mention
		for number := 1; number <= 300; number++ {
			many[0] = append(many[0], Mention{Name: fmt.Sprintf("первый %d", number)})
			many[1] = append(many[1], Mention{Name: fmt.Sprintf("второй %d", number)})
		}
		crowd := MergeCards([]Card{{Summary: "a", Characters: many[0]}, {Summary: "b", Characters: many[1]}})
		if len(crowd.Characters) != 400 || crowd.Characters[199].Name != "первый 300" || crowd.Characters[200].Name != "второй 1" {
			t.Fatalf("600 characters = %d", len(crowd.Characters))
		}
		crowd.Facts = MergeCards([]Card{{Summary: "a", Facts: factsOf("первая", 1, 300)}, {Summary: "b", Facts: factsOf("вторая", 1, 300)}}).Facts
		crowd.Entities = bounded.Entities
		output, err := json.Marshal(crowd)
		if err != nil {
			t.Fatal(err)
		}
		if parsed, err := ParseCard(string(output), 1, 1<<30); err != nil || !reflect.DeepEqual(parsed, crowd) {
			t.Fatalf("the merged card is not within the bounds of a card: %v", err)
		}
	})
}
