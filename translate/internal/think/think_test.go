// SPDX-License-Identifier: MIT

package think

import "testing"

// run drives a filter over a sequence of pieces and joins the channels.
func run(pieces ...string) Split {
	var filter Filter
	var all Split
	for _, piece := range pieces {
		split := filter.Push(piece)
		all.Reasoning += split.Reasoning
		all.Text += split.Text
	}
	tail := filter.Finish()
	all.Reasoning += tail.Reasoning
	all.Text += tail.Text
	return all
}

func expect(t *testing.T, got Split, reasoning, text string) {
	t.Helper()
	if got.Reasoning != reasoning || got.Text != text {
		t.Fatalf("got reasoning %q text %q, want %q and %q", got.Reasoning, got.Text, reasoning, text)
	}
}

func TestSplitsACompleteThinkBlock(t *testing.T) {
	expect(t, run("<think>musing</think>\n\nHello!"), "musing", "\n\nHello!")
}

func TestRecognizesTagsSplitAcrossPieces(t *testing.T) {
	expect(t, run("<thi", "nk>mus", "ing</th", "ink>Hi"), "musing", "Hi")
}

func TestABareCloseTagInAStreamIsVisibleTextWhateverTheChunks(t *testing.T) {
	expect(t, run("musing", "</think>", "Hi"), "", "musing</think>Hi")
	if run("musing", "</think>", "Hi") != run("musing</think>Hi") {
		t.Fatal("a bare close tag depends on the chunks")
	}
}

func TestPassesPlainTextThroughUntouched(t *testing.T) {
	expect(t, run("Hello", " world"), "", "Hello world")
}

func TestPreservesPlainTextLeadingWhitespace(t *testing.T) {
	expect(t, run("  indented"), "", "  indented")
	expect(t, run("\n", "  indented"), "", "\n  indented")
}

func TestExplicitTagsDoNotDependOnTheChunks(t *testing.T) {
	want := run("<think>musing</think>Hi")
	if run("<thi", "nk>musing</th", "ink>Hi") != want || run("<think>musing</think>", "Hi") != want {
		t.Fatal("explicit tags depend on the chunks")
	}
}

func TestPreservesWhitespaceAfterAThinkBlock(t *testing.T) {
	expect(t, run("<think>x</think>  indented"), "x", "  indented")
	expect(t, run("<think>x</think>\n    code"), "x", "\n    code")
}

func TestPreservesWhitespaceAroundAThinkBlockInTheMiddle(t *testing.T) {
	expect(t, run("prefix ", "<think>x</think>", " suffix"), "x", "prefix  suffix")
}

// Once real text has been emitted a bare </think> is just text; only a
// matched <think>…</think> pair splits.
func TestACloseTagAfterVisibleTextIsNotReasoning(t *testing.T) {
	expect(t, run("see the </think> tag"), "", "see the </think> tag")
}

func TestAnUnterminatedThinkBlockStaysReasoning(t *testing.T) {
	expect(t, run("<think>cut off mid-thought"), "cut off mid-thought", "")
}

func TestKimiMarkersAreRecognized(t *testing.T) {
	expect(t, run("◁think▷musing◁/think▷Hi"), "musing", "Hi")
	expect(t, run("◁thi", "nk▷mus", "ing◁/thi", "nk▷Hi"), "musing", "Hi")
}

func TestThinkingTagsAreRecognized(t *testing.T) {
	expect(t, run("<thinking>musing</thinking>Hi"), "musing", "Hi")
}

func TestHealsAWholeAnswer(t *testing.T) {
	split, found := Heal("<think>musing</think>Hello!")
	if !found {
		t.Fatal("markup not found")
	}
	expect(t, split, "musing", "Hello!")
}

func TestABareCloseTagInAWholeAnswerStaysVisibleAsInAStream(t *testing.T) {
	split, found := Heal("musing</think>Hi")
	if found {
		t.Fatal("a bare close tag was taken for markup")
	}
	expect(t, split, "", "musing</think>Hi")
}

func TestAnEmptyThinkBlockIsRemovedFromAWholeAnswer(t *testing.T) {
	split, found := Heal("<think></think>")
	if !found {
		t.Fatal("markup not found")
	}
	expect(t, split, "", "")
}
