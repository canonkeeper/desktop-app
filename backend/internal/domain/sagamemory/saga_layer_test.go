package sagamemory

import (
	"math"
	"reflect"
	"testing"
)

// specs/2026-10-29-chapter-cards-quality.md R4.1: the profile of an entry gathers the facts of
// all its names; an attribute with one value shows the latest value not from the past and the
// history of changes, the others their distinct values with the latest statement first.
func TestBuildProfileGathersFactsUnderAllNames(t *testing.T) {
	cards := map[int]*Card{
		0: {Characters: []Mention{{Name: "Мирон", Aliases: []string{"Ворон"}}}, Facts: []Fact{
			{Subject: "Мирон", Attribute: "возраст", Value: "24 года", P: 12},
			{Subject: "Мирон", Attribute: "возраст", Value: "7 лет", P: 30, Past: true},
			{Subject: "Мирон", Attribute: "навык", Value: "Взлом, ученик", P: 40},
			{Subject: "Любава", Attribute: "возраст", Value: "14 лет", P: 2},
		}},
		1: {Characters: []Mention{{Name: "Ворон"}}, Facts: []Fact{
			{Subject: "Ворон", Attribute: "уровень", Value: "9", P: 3},
			{Subject: "ВОРОН", Attribute: "уровень", Value: "9", P: 8},
			{Subject: "Ворон", Attribute: "навык", Value: "Взлом, подмастерье", P: 9},
			{Subject: "Ворон", Attribute: "навык", Value: "Стелс", P: 9},
		}},
		2: {Characters: []Mention{{Name: "Мирон"}}, Facts: []Fact{
			{Subject: "Мирон", Attribute: "уровень", Value: "14", P: 1},
			{Subject: "Мирон", Attribute: "возраст", Value: "25 лет", P: 5},
		}},
	}
	glossary := BuildGlossary(cards, nil)
	entry, ok := glossary.Lookup("ворон")
	if !ok {
		t.Fatal("the hero has no entry")
	}
	var facts []ChapterFact
	for chapter := 0; chapter < 3; chapter++ {
		for _, fact := range cards[chapter].Facts {
			facts = append(facts, ChapterFact{Chapter: chapter, Fact: fact})
		}
	}
	profile := BuildProfile(entry, facts)
	if profile.Facts != 9 || profile.Chapters != 3 {
		t.Fatalf("profile counts = %d facts in %d chapters, want 9 in 3", profile.Facts, profile.Chapters)
	}
	var names []string
	for _, attribute := range profile.Attributes {
		names = append(names, attribute.Name)
	}
	if !reflect.DeepEqual(names, []string{"возраст", "уровень", "навык"}) {
		t.Fatalf("attributes = %v, want single values in their order, then the others", names)
	}
	age := profile.Attributes[0]
	if !age.Single || age.Current == nil || age.Current.Value != "25 лет" || age.Current.Chapter != 2 {
		t.Fatalf("age = %+v, want 25 лет of chapter 3", age)
	}
	if got := historyValues(age.History); !reflect.DeepEqual(got, []string{"24 года", "7 лет", "25 лет"}) {
		t.Fatalf("age history = %v", got)
	}
	level := profile.Attributes[1]
	if got := historyValues(level.History); !reflect.DeepEqual(got, []string{"9", "14"}) || level.History[0].Last != 1 {
		t.Fatalf("level history = %+v, want a repeated value kept once", level.History)
	}
	skills := profile.Attributes[2]
	if skills.Single || skills.Current != nil {
		t.Fatalf("skills = %+v, want a list of values", skills)
	}
	if got := historyValues(skills.History); !reflect.DeepEqual(got, []string{"Стелс", "Взлом, подмастерье"}) || skills.History[1].Chapter != 0 {
		t.Fatalf("skills = %+v, want the latest statement of each skill, newest first, with its first chapter", skills.History)
	}
}

// R4.1: a recollection told later does not replace what the story states of a value: the group
// keeps its latest present statement, the recollection only counts.
func TestBuildProfileKeepsThePresentOverALaterRecollection(t *testing.T) {
	entry := &GlossaryEntry{Name: "Мирон"}
	profile := BuildProfile(entry, []ChapterFact{
		{Chapter: 0, Fact: Fact{Subject: "Мирон", Attribute: "отношения", Value: "Любава: сестра", P: 1}},
		{Chapter: 3, Fact: Fact{Subject: "Мирон", Attribute: "отношения", Value: "Любава: маленькая сестра, которую он нянчил", P: 4, Past: true}},
		{Chapter: 4, Fact: Fact{Subject: "Мирон", Attribute: "отношения", Value: "Мать: исчезла", P: 2, Past: true}},
	})
	relations := profile.Attributes[0].History
	if len(relations) != 2 || relations[1].Value != "Любава: сестра" || relations[1].Past || relations[1].Count != 2 ||
		relations[0].Value != "Мать: исчезла" || !relations[0].Past {
		t.Fatalf("relations = %+v, want Любава as the story states her and Мать from a recollection", relations)
	}
}

// R5.1: a chapter whose time note declares it off the main line (an afterword, another
// storyline, a recollection, a dream) neither moves the time line nor counts as unknown.
func TestStoryDaysSkipChaptersOffTheMainLine(t *testing.T) {
	cards := map[int]*Card{
		0: {When: &ChapterTime{Lasts: &Span{N: 1, Unit: "день"}}},
		1: {When: &ChapterTime{Lasts: &Span{N: 5, Unit: "день"}, Note: "Другая сюжетная линия: Молли за несколько дней до боя"}},
		2: {When: &ChapterTime{Note: "Авторское послесловие; событий сюжетной линии нет."}},
		3: {When: &ChapterTime{After: &Span{N: 1, Unit: "день"}, Note: "Основное действие в игре; соотношение времени не указано"}},
	}
	days := StoryDays(4, cards)
	if !days[1].OffLine || !days[2].OffLine || days[3].OffLine || days[3].Start != 2 || days[3].Unknown != 1 {
		t.Fatalf("days = %+v, want chapters 2 and 3 off the line and chapter 4 a day after chapter 1 with its own length unknown", days)
	}
}

func historyValues(values []ProfileValue) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = value.Value
	}
	return out
}

// R5.1: the story day of a chapter adds the time before it and its length to the end of the
// previous one; an unknown span (the time before the first chapter is none) counts zero and is
// counted, an estimate is counted as well.
func TestStoryDaysAddSpansAndCountUnknownOnes(t *testing.T) {
	cards := map[int]*Card{
		0: {When: &ChapterTime{Lasts: &Span{N: 12, Unit: "час", Approx: true}}},
		1: {When: &ChapterTime{After: &Span{N: 1, Unit: "день"}, Lasts: &Span{N: 2, Unit: "день"}}},
		3: {When: &ChapterTime{After: &Span{N: 1, Unit: "неделя"}}},
	}
	days := StoryDays(4, cards)
	want := []StoryDay{
		{Start: 0, End: 0.5, Unknown: 0, Approx: 1},
		{Start: 1.5, End: 3.5, Unknown: 0, Approx: 1},
		{Start: 3.5, End: 3.5, Unknown: 2, Approx: 1},
		{Start: 10.5, End: 10.5, Unknown: 3, Approx: 1},
	}
	for i := range want {
		if math.Abs(days[i].Start-want[i].Start) > 1e-9 || math.Abs(days[i].End-want[i].End) > 1e-9 ||
			days[i].Unknown != want[i].Unknown || days[i].Approx != want[i].Approx {
			t.Fatalf("day %d = %+v, want %+v", i, days[i], want[i])
		}
	}
	if got := FormatDays(400); got != "≈ 1,1 года" {
		t.Fatalf("FormatDays(400) = %q", got)
	}
	if got := FormatDays(0.25); got != "≈ 6 ч" {
		t.Fatalf("FormatDays(0.25) = %q", got)
	}
}
