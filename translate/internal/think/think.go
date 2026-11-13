// SPDX-License-Identifier: MIT

// Package think moves <think> markup that a reasoning model leaks into the
// text of its answer back onto the reasoning channel. It is a port of
// src/think.rs of codex-relay (MIT, Copyright (c) 2025 Sapient Inc.; see
// LICENSE.codex-relay).
//
// Thinking models are supposed to expose their chain of thought in a separate
// reasoning_content (or reasoning) field. Deployments served without a
// matching reasoning parser put the raw chat-template markup into the text:
//
//	<think>The user said hi. I should greet them back.</think>Hello!
//
// The client then shows the chain of thought as the answer and replays it,
// teaching the model to keep producing it. Some chat templates pre-fill the
// opening tag into the prompt, so an answer may hold only the closing tag;
// only an explicit opening tag is recognized, because a later bare closing tag
// would otherwise be read differently depending on the chunks of a stream.
package think

import "strings"

var (
	// openTags are the opening markers; ◁think▷ is Kimi's.
	openTags = []string{"<thinking>", "<think>", "◁think▷"}
	// closeTags match openTags.
	closeTags = []string{"</thinking>", "</think>", "◁/think▷"}
)

// Split is text divided between the reasoning and the visible answer.
type Split struct {
	Reasoning string
	Text      string
}

// Filter splits streamed text. Text that could be a tag or its start is held
// back until the next piece settles it, so a tag torn across chunks ("<thi",
// then "nk>") is still recognized. The zero value is ready to use.
type Filter struct {
	pending string
	inThink bool
}

// Push adds a piece of streamed text and returns what is safe to emit now.
func (f *Filter) Push(piece string) Split {
	var reasoning, text strings.Builder
	f.pending += piece
	for {
		if f.inThink {
			if at, tag, ok := firstTag(f.pending, closeTags); ok {
				reasoning.WriteString(f.pending[:at])
				f.pending = f.pending[at+len(tag):]
				f.inThink = false
				continue
			}
			keep := len(f.pending) - longestTagPrefixSuffix(f.pending, closeTags)
			reasoning.WriteString(f.pending[:keep])
			f.pending = f.pending[keep:]
			return Split{Reasoning: reasoning.String(), Text: text.String()}
		}
		if at, tag, ok := firstTag(f.pending, openTags); ok {
			text.WriteString(f.pending[:at])
			f.pending = f.pending[at+len(tag):]
			f.inThink = true
			continue
		}
		keep := len(f.pending) - longestTagPrefixSuffix(f.pending, openTags)
		text.WriteString(f.pending[:keep])
		f.pending = f.pending[keep:]
		return Split{Reasoning: reasoning.String(), Text: text.String()}
	}
}

// Finish returns what is still held back at the end of the stream; an
// unterminated think block is reasoning, not text.
func (f *Filter) Finish() Split {
	rest := f.pending
	f.pending = ""
	if f.inThink {
		return Split{Reasoning: rest}
	}
	return Split{Text: rest}
}

// Heal splits a whole answer the way a stream of it would be split. found is
// false when the answer has no opening tag and stays as it is.
func Heal(answer string) (split Split, found bool) {
	if _, _, ok := firstTag(answer, openTags); !ok {
		return Split{Text: answer}, false
	}
	var filter Filter
	split = filter.Push(answer)
	tail := filter.Finish()
	return Split{Reasoning: split.Reasoning + tail.Reasoning, Text: split.Text + tail.Text}, true
}

// firstTag is the byte offset and the tag that starts earliest in text,
// preferring the longest tag at the same offset.
func firstTag(text string, tags []string) (int, string, bool) {
	at, found := -1, ""
	for _, tag := range tags {
		index := strings.Index(text, tag)
		if index >= 0 && (at < 0 || index < at || index == at && len(tag) > len(found)) {
			at, found = index, tag
		}
	}
	return at, found, at >= 0
}

// longestTagPrefixSuffix is the length in bytes of the longest end of text
// that is a proper beginning of a tag, cut at a character boundary.
func longestTagPrefixSuffix(text string, tags []string) int {
	best := 0
	for _, tag := range tags {
		for i := range tag {
			if i > best && strings.HasSuffix(text, tag[:i]) {
				best = i
			}
		}
	}
	return best
}
