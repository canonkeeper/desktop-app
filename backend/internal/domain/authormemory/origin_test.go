package authormemory

import "testing"

// specs/2026-11-10-ux8-memory-records.md MR1.5: a record says where it came from by the Job it keeps; a chat is named only
// while it lives and the reader sees it in the list of chats.
func TestTheOriginOfARecordIsWhatIsLeftOfItsConversation(t *testing.T) {
	cases := map[string]struct {
		job          string
		conversation *Conversation
		want         Origin
	}{
		"written on the page":     {"", nil, Origin{Kind: OriginManual}},
		"a chat that lives":       {"job", &Conversation{SessionKind: "research", ChatID: "chat", ChatTitle: "Что Мара знает", Alive: true, Visible: true}, Origin{Kind: OriginChat, ChatID: "chat", Title: "Что Мара знает"}},
		"a deleted chat":          {"job", &Conversation{SessionKind: "research", ChatID: "chat", ChatTitle: "Что Мара знает", Visible: true}, Origin{Kind: OriginChat}},
		"a chat hidden to him":    {"job", &Conversation{SessionKind: "research", ChatID: "chat", ChatTitle: "Черновик", Alive: true}, Origin{Kind: OriginChat}},
		"a purged conversation":   {"job", nil, Origin{Kind: OriginChat}},
		"the helper of a draft":   {"job", &Conversation{SessionKind: "draft", ChatID: "chat", ChatTitle: "Глава", DraftTitle: "Белый порт", Alive: true, Visible: true}, Origin{Kind: OriginDraft, Title: "Белый порт"}},
		"a deleted draft":         {"job", &Conversation{SessionKind: "draft", ChatID: "chat", Visible: true}, Origin{Kind: OriginDraft}},
		"the helper of a picture": {"job", &Conversation{SessionKind: "drawing", ChatID: "chat", ChatTitle: "Рисунок", Alive: true, Visible: true}, Origin{Kind: OriginDrawing}},
	}
	for name, item := range cases {
		if got := OriginOf(item.job, item.conversation); got != item.want {
			t.Errorf("%s: %+v, want %+v", name, got, item.want)
		}
	}
}
