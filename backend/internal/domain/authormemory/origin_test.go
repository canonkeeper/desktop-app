package authormemory

import "testing"

// specs/2026-11-10-ux8-memory-records.md MR1.5: a record says where it came from by the Job it keeps; a chat is named only
// while it lives and the reader sees it in the list of chats. specs/2026-11-13-desk-sagafile.md SF4.1: a record a saga file
// brought keeps where it came from when its Job is not found, without a chat, a draft or a drawing to open.
func TestTheOriginOfARecordIsWhatIsLeftOfItsConversation(t *testing.T) {
	cases := map[string]struct {
		job          string
		conversation *Conversation
		carried      OriginKind
		want         Origin
	}{
		"written on the page":     {"", nil, "", Origin{Kind: OriginManual}},
		"a chat that lives":       {"job", &Conversation{SessionKind: "research", ChatID: "chat", ChatTitle: "Что Мара знает", Alive: true, Visible: true}, "", Origin{Kind: OriginChat, ChatID: "chat", Title: "Что Мара знает"}},
		"a deleted chat":          {"job", &Conversation{SessionKind: "research", ChatID: "chat", ChatTitle: "Что Мара знает", Visible: true}, "", Origin{Kind: OriginChat}},
		"a chat hidden to him":    {"job", &Conversation{SessionKind: "research", ChatID: "chat", ChatTitle: "Черновик", Alive: true}, "", Origin{Kind: OriginChat}},
		"a purged conversation":   {"job", nil, "", Origin{Kind: OriginChat}},
		"the helper of a draft":   {"job", &Conversation{SessionKind: "draft", ChatID: "chat", ChatTitle: "Глава", DraftTitle: "Белый порт", Alive: true, Visible: true}, "", Origin{Kind: OriginDraft, Title: "Белый порт"}},
		"a deleted draft":         {"job", &Conversation{SessionKind: "draft", ChatID: "chat", Visible: true}, "", Origin{Kind: OriginDraft}},
		"the helper of a picture": {"job", &Conversation{SessionKind: "drawing", ChatID: "chat", ChatTitle: "Рисунок", Alive: true, Visible: true}, "", Origin{Kind: OriginDrawing}},
		"a Job that lives wins":   {"job", &Conversation{SessionKind: "drawing", ChatID: "chat", Alive: true, Visible: true}, OriginDraft, Origin{Kind: OriginDrawing}},
		"carried from a chat":     {"", nil, OriginChat, Origin{Kind: OriginChat}},
		"carried from a draft":    {"", nil, OriginDraft, Origin{Kind: OriginDraft}},
		"carried from a drawing":  {"", nil, OriginDrawing, Origin{Kind: OriginDrawing}},
		"carried, its Job gone":   {"job", nil, OriginDrawing, Origin{Kind: OriginDrawing}},
	}
	for name, item := range cases {
		if got := OriginOf(item.job, item.conversation, item.carried); got != item.want {
			t.Errorf("%s: %+v, want %+v", name, got, item.want)
		}
	}
}

// SF4.2: a saga file says the origin of a record by its kind; the kind read back is the one written, and a record written on
// the page carries none.
func TestTheCarriedOriginIsAKindOfAConversation(t *testing.T) {
	for _, kind := range []OriginKind{OriginChat, OriginDraft, OriginDrawing} {
		if got, ok := ParseCarried(string(kind)); !ok || got != kind {
			t.Errorf("ParseCarried(%q) = %q, %v", kind, got, ok)
		}
	}
	if got, ok := ParseCarried(string(OriginManual)); !ok || got != "" {
		t.Errorf("a record of the page carries %q, %v; want nothing", got, ok)
	}
	if _, ok := ParseCarried("letter"); ok {
		t.Error("an unknown origin is taken")
	}
}
