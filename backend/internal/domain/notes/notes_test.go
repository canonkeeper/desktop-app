package notes

import (
	"strings"
	"testing"
)

func TestBodyAndSourceValidation(t *testing.T) {
	for _, body := range []string{"", " \n", strings.Repeat("я", 100001), "bad\x00text", string([]byte{0xff})} {
		if ValidateBody(body) == nil {
			t.Fatalf("accepted invalid body of length %d", len(body))
		}
	}
	if ValidateBody(strings.Repeat("😀", 100000)) != nil {
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
