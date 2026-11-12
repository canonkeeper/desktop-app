package jobs

import (
	"reflect"
	"strings"
	"testing"
)

// specs/2026-11-12-ux10-scene-stream.md ST1.2, ST1.3: the scene in writing is its own key of the result — a row that
// holds one still decodes; only a scene, a call, a step of the three and a text of its bounds are kept; the row of a
// finished Job keeps none of it.
func TestTheSceneInWritingIsItsOwnKeyAndSealedDropsIt(t *testing.T) {
	result := ChapterDraftResult{Type: KindChapterDraft, Mode: ChapterDraftScenes, Phase: ChapterDraftPhaseScenes, Total: 3, Done: 1,
		SceneRuns: []ChapterDraftRun{{Scene: 1, Words: 4, Attempts: 1, Text: "Сцена."}},
		Writing:   &ChapterDraftWriting{Scene: 2, Attempt: 1, Step: ChapterDraftWritingStep, Text: "Кира шла"}}
	raw, err := EncodeResult(KindChapterDraft, result)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := DecodeResult(KindChapterDraft, raw); err != nil || !reflect.DeepEqual(back, result) {
		t.Fatalf("decoded %+v, %v", back, err)
	}
	if sealed := result.Sealed(); sealed.Writing != nil || strings.Contains(string(mustEncode(t, sealed)), "Кира шла") {
		t.Fatalf("sealed %+v", sealed)
	}
	if public := result.Public(); public.Writing != nil {
		t.Fatalf("public %+v", public)
	}
	own, err := EncodeChapterDraftWriting(ChapterDraftWriting{Scene: 2, Attempt: 2, Step: ChapterDraftAgainStep, Reason: ChapterDraftAgainLost})
	if err != nil || string(own) != `{"scene":2,"attempt":2,"step":"again","reason":"lost"}` {
		t.Fatalf("EncodeChapterDraftWriting() = %s, %v", own, err)
	}
	for name, wrong := range map[string]ChapterDraftWriting{
		"no scene":            {Attempt: 1, Step: ChapterDraftWritingStep},
		"no call":             {Scene: 1, Step: ChapterDraftWritingStep},
		"another step":        {Scene: 1, Attempt: 1, Step: "done"},
		"a reason of writing": {Scene: 1, Attempt: 1, Step: ChapterDraftWritingStep, Reason: ChapterDraftAgainLost},
		"another reason":      {Scene: 1, Attempt: 2, Step: ChapterDraftAgainStep, Reason: "model"},
		"too long":            {Scene: 1, Attempt: 1, Step: ChapterDraftWritingStep, Text: strings.Repeat("а", MaxChapterDraftWritingBytes)},
		"not text":            {Scene: 1, Attempt: 1, Step: ChapterDraftCheckingStep, Text: "\xff"},
	} {
		if _, err := EncodeChapterDraftWriting(wrong); err == nil {
			t.Fatalf("%s was accepted", name)
		}
		bad := result
		bad.Writing = &wrong
		if _, err := EncodeResult(KindChapterDraft, bad); err == nil {
			t.Fatalf("a result with %s was accepted", name)
		}
	}
}

func mustEncode(t *testing.T, result ChapterDraftResult) []byte {
	t.Helper()
	raw, err := EncodeResult(KindChapterDraft, result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
