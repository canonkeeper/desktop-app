// SPDX-License-Identifier: AGPL-3.0-only

package library

// QueueVolume is the file of a volume as the queue reads it when the file comes (specs/2026-11-13-desk-obsidian.md OB4.2):
// Refusal is the code the queue keeps for a volume that does not read — the code of the import Job too — and Book is
// what the import makes of a volume that reads.
type QueueVolume struct {
	Refusal string
	Book    ImportedBook
}

// ImportedBook is the book an import makes of a file: its title and its chapters with the plain words of their paragraphs.
type ImportedBook struct {
	Title    string
	Chapters []ImportedChapter
}

// ImportedChapter is a chapter of the book: the plain words of its paragraphs and, row for row, the Markdown of their
// italic and bold the book keeps (specs/2026-11-14-desk-obsidian-books.md OK0.4: the plan of an update compares both).
type ImportedChapter struct {
	Title      string
	Paragraphs []string
	Markdown   []string
}

// CheckQueueVolume reads the file of a volume of the import queue as the queue checks it and as the import Job reads it
// (preflightImportQueueItem, readImportBook): a host that builds a file itself sees the book it gives before any upload.
// A name the queue does not take is ErrValidation, a file over the size of an import ErrImportFileTooLarge — as at the
// upload. Nothing is written.
func CheckQueueVolume(fileName string, content []byte) (QueueVolume, error) {
	plan, failure, err := readImportQueueItem(fileName, "", content, sha256Hex(content))
	if err != nil {
		return QueueVolume{}, err
	}
	if failure != nil {
		return QueueVolume{Refusal: failure.Code}, nil
	}
	book := ImportedBook{Title: plan.BookTitle, Chapters: make([]ImportedChapter, 0, len(plan.Chapters))}
	for _, chapter := range plan.Chapters {
		paragraphs := make([]string, 0, len(chapter.Paragraphs))
		markdown := make([]string, 0, len(chapter.Paragraphs))
		for _, paragraph := range chapter.Paragraphs {
			if paragraph.Text != nil {
				paragraphs = append(paragraphs, *paragraph.Text)
				markdown = append(markdown, paragraph.Markdown)
			}
		}
		book.Chapters = append(book.Chapters, ImportedChapter{Title: chapter.Title, Paragraphs: paragraphs, Markdown: markdown})
	}
	return QueueVolume{Book: book}, nil
}

// MarkdownPlainLines reads a text of Markdown line by line as the import of a book in Markdown reads its lines
// (specs/2026-11-10-book-file-kinds.md K2.5) and gives each line as its plain words: a heading without its signs, a quote
// without «>», an item of a list without its mark, a link as its words, a picture and a note as nothing, the faces without
// their signs. A fence of code and the definition of a link give no line; a blank line gives "".
func MarkdownPlainLines(text string) []string {
	reader := textLines{markdown: true}
	var lines []string
	_ = eachLine(text, func(raw string) error {
		line := reader.read(raw)
		switch line.kind {
		case textSkipped:
			return nil
		case textBlank:
			lines = append(lines, "")
			return nil
		}
		_, plain := textParagraph(line, true).tidy()
		lines = append(lines, plain)
		return nil
	})
	return lines
}
