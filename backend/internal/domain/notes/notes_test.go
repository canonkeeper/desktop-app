package notes

import (
	"strings"
	"testing"
)

func TestBodyAndSourceValidation(t *testing.T) {
	for _, body := range []string{"", " \n", strings.Repeat("я", 100001), "bad\x00text", string([]byte{0xff})} {
		if ValidateText(body) == nil {
			t.Fatalf("accepted invalid body of length %d", len(body))
		}
	}
	if ValidateText(strings.Repeat("😀", 100000)) != nil {
		t.Fatal("character limit counted bytes")
	}
	source := Source{Kind: "chat", ChatID: "00000000-0000-4000-8000-000000000001", ProviderTurnID: "turn", ProviderItemID: "item", Quote: "streaming copy"}
	if source.Validate() != nil {
		t.Fatal("valid streaming copy rejected")
	}
	source.ProviderItemID = ""
	if source.Validate() == nil {
		t.Fatal("missing exact message accepted")
	}
}

func TestOptionalTitleUnicodeLimit(t *testing.T) {
	for _, title := range []string{"", "Название", strings.Repeat("😀", 200)} {
		if ValidateTitle(title) != nil {
			t.Fatal("valid title rejected")
		}
	}
	for _, title := range []string{strings.Repeat("😀", 201), "bad\x00title", string([]byte{0xff})} {
		if ValidateTitle(title) == nil {
			t.Fatal("invalid title accepted")
		}
	}
}

// The thought of a note may be empty only beside a quote: a note from a selection is saved without a thought, a note of
// its own needs one (specs/2026-11-11-ux7-notes.md NT1.2).
func TestThoughtIsOptionalBesideAQuote(t *testing.T) {
	for _, thought := range []string{"", " \n", "Сверить с судом"} {
		if ValidateThought(thought, true) != nil {
			t.Fatalf("thought %q beside a quote rejected", thought)
		}
	}
	for _, thought := range []string{"", " \n"} {
		if ValidateThought(thought, false) == nil {
			t.Fatalf("empty thought %q without a quote accepted", thought)
		}
	}
	for _, thought := range []string{strings.Repeat("я", 100001), "bad\x00text", string([]byte{0xff})} {
		if ValidateThought(thought, true) == nil {
			t.Fatalf("invalid thought of length %d accepted beside a quote", len(thought))
		}
	}
}
