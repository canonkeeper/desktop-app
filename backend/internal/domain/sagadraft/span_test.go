package sagadraft

import "testing"

// specs/2026-11-08-italic-bold-in-text.md TF2.2: a quote of a model is found through the marks of faces — in the middle
// of a phrase too — and its place stands in the text with the marks outside it; the scene a note holds has no mark of
// a face.
func TestAQuoteIsFoundThroughTheFaces(t *testing.T) {
	text := "Он ⟨шёл⟩ домой.\n\nНочь ⟪была⟫ тихой."
	found := QuotesIn(text, "Он шёл домой.")
	if len(found) != 1 || text[found[0][0]:found[0][1]] != "Он ⟨шёл⟩ домой." {
		t.Fatalf("QuotesIn = %v", found)
	}
	if found := QuotesIn(text, "шёл"); len(found) != 1 || text[found[0][0]:found[0][1]] != "шёл" {
		t.Fatalf("a quote inside a face = %v", found)
	}
	place, start, end, err := Scene{From: "Он шёл", To: "была тихой."}.SpanIn(text)
	if err != nil || place != "Он шёл домой.\n\nНочь была тихой." || text[start:end] != "Он ⟨шёл⟩ домой.\n\nНочь ⟪была⟫ тихой." {
		t.Fatalf("SpanIn = %q %q %v", place, text[start:end], err)
	}
	if !Contains("⟦Он ⟨шёл⟩⟧", "Он шёл") || !Contains("Он ⟨шёл⟩", "Он ⟨ шёл") || Contains("Он ⟨шёл⟩", "Он ушёл") {
		t.Fatal("Contains does not read through the faces")
	}
}
