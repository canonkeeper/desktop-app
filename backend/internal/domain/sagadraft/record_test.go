package sagadraft

import (
	"strings"
	"testing"
)

// W8.1: what should happen is no longer a kind of draft.
func TestOnlyASketchAndAPlanAreKinds(t *testing.T) {
	for value, known := range map[string]bool{"sketch": true, "plan": true, "skeleton": false, "": false} {
		if _, ok := ParseKind(value); ok != known {
			t.Fatalf("ParseKind(%q) = %v", value, ok)
		}
	}
}

func TestStageIsTheFurthestReached(t *testing.T) {
	for want, draft := range map[Stage]Draft{
		StageText:  {Idea: "замысел", Text: "черновик"},
		StageAdded: {Text: "глава", AddedChapterID: "chapter"},
	} {
		if got := draft.Stage(); got != want {
			t.Fatalf("%+v: Stage() = %s, want %s", draft, got, want)
		}
	}
}

func TestTheIdeaHashIgnoresSpaces(t *testing.T) {
	if IdeaHash("Кира  идёт\nдомой") != IdeaHash("Кира идёт домой") || IdeaHash("Кира идёт") == IdeaHash("Кира стоит") {
		t.Fatal("idea hash")
	}
	if !strings.HasPrefix(IdeaHash(""), "") {
		t.Fatal("unreachable")
	}
}
