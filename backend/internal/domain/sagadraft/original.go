package sagadraft

import (
	"canonkeeper/backend/internal/domain/chapterfile"
	"canonkeeper/backend/internal/domain/textface"
)

// FileNow is what the file with corrections writes against the original of a draft (specs/2026-11-07-writing-proofread.md
// PR6.1; specs/2026-11-08-italic-bold-in-text.md TF7.2): the title of the draft and the paragraphs of its working text
// with their italic and bold, without the marks of what the model wrote.
func FileNow(title, text string) (string, []chapterfile.Paragraph) {
	return title, textface.Lines(text)
}

// FileRevisions is the number of revisions of Word that file has now (specs/2026-11-11-ux5-proofread-mode.md PM1.4): the
// writer of the file counted, so «Скачать Word с исправлениями (K)» names what Word shows.
func FileRevisions(original chapterfile.Document, title, text string) int {
	title, paragraphs := FileNow(title, text)
	return chapterfile.Revisions(original, title, paragraphs)
}
