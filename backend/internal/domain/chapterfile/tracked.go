package chapterfile

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"time"
)

// Tracked writes the file of a chapter with revisions (PR6), the step «скачать .docx с правками»: the original as the
// author loaded it, and every difference of the title and the paragraphs now from it as a revision of Word — a deletion
// or an insertion by author at the time at — so that «Принять все исправления» gives the title and exactly the
// paragraphs now, and «Отклонить все» the original. Equal words keep their italic and bold, inserted words take the face
// of the original before them; the empty paragraphs of the original keep their places.
func Tracked(writer io.Writer, original Document, title string, lines []string, author string, at time.Time) error {
	body := &trackedBody{author: author, date: at.UTC().Truncate(time.Second).Format(time.RFC3339)}
	body.write(original, title, lines)
	return writeTrackedPackage(writer, body.document(), title, author, at)
}

type paragraphMark int8

const (
	markNormal paragraphMark = iota
	markInserted
	markDeleted
)

// outParagraph is a paragraph of the file as it will be written.
type outParagraph struct {
	heading bool
	content strings.Builder
	mark    paragraphMark
	// whole says the content is one revision of the mark's kind: a paragraph inserted or deleted whole.
	whole paragraphMark
}

type trackedBody struct {
	author, date string
	revision     int
	paragraphs   []*outParagraph
}

func (body *trackedBody) add(heading bool) *outParagraph {
	paragraph := &outParagraph{heading: heading}
	body.paragraphs = append(body.paragraphs, paragraph)
	return paragraph
}

// write lays out the paragraphs: the title, then the paragraphs of the original aligned with the lines now (PR6.2).
func (body *trackedBody) write(original Document, title string, lines []string) {
	if original.Title != nil {
		heading := body.add(true)
		if now := strings.TrimSpace(title); now == "" || now == original.Title.Text() {
			body.equal(&heading.content, *original.Title, 0, len(original.Title.Text()))
		} else {
			body.changed(&heading.content, *original.Title, now)
		}
	}
	var texts []string
	var places []int // the index in original.Paragraphs of each paragraph with words
	for i, paragraph := range original.Paragraphs {
		if text := paragraph.Text(); strings.TrimSpace(text) != "" {
			texts, places = append(texts, text), append(places, i)
		}
	}
	next := 0 // the next paragraph of the original to write, empty ones included
	upTo := func(index int) {
		for ; next < index; next++ {
			if strings.TrimSpace(original.Paragraphs[next].Text()) == "" {
				body.add(false)
			}
		}
		next = max(next, index+1)
	}
	edits := difference(texts, lines)
	for k := 0; k < len(edits); {
		if edits[k].kind == editEqual {
			upTo(places[edits[k].a])
			paragraph := body.add(false)
			source := original.Paragraphs[places[edits[k].a]]
			body.equal(&paragraph.content, source, 0, len(source.Text()))
			k++
			continue
		}
		var deleted, inserted []int
		for ; k < len(edits) && edits[k].kind != editEqual; k++ {
			if edits[k].kind == editDelete {
				deleted = append(deleted, edits[k].a)
			} else {
				inserted = append(inserted, edits[k].b)
			}
		}
		// A deleted and an inserted paragraph in one gap are one changed paragraph, in their order.
		pairs := min(len(deleted), len(inserted))
		for p := range pairs {
			upTo(places[deleted[p]])
			body.changed(&body.add(false).content, original.Paragraphs[places[deleted[p]]], lines[inserted[p]])
		}
		for _, index := range deleted[pairs:] {
			upTo(places[index])
			paragraph := body.add(false)
			paragraph.mark, paragraph.whole = markDeleted, markDeleted
			source := original.Paragraphs[places[index]]
			body.deleted(&paragraph.content, source, 0, len(source.Text()))
		}
		for _, index := range inserted[pairs:] {
			paragraph := body.add(false)
			paragraph.mark, paragraph.whole = markInserted, markInserted
			body.inserted(&paragraph.content, lines[index], Run{})
		}
	}
	upTo(len(original.Paragraphs))
	// The mark of the last paragraph of a document cannot be inserted or deleted: a run of paragraphs inserted or deleted
	// whole at the end takes the mark of the paragraph before it instead, so accepting or rejecting all still joins them
	// right (PR6.4).
	last := len(body.paragraphs) - 1
	if last >= 0 && body.paragraphs[last].whole != markNormal {
		kind := body.paragraphs[last].whole
		first := last
		for first > 0 && body.paragraphs[first-1].whole == kind {
			first--
		}
		body.paragraphs[last].mark = markNormal
		if first > 0 {
			body.paragraphs[first-1].mark = kind
		}
	}
}

// changed writes a paragraph changed word by word (PR6.3): equal tokens as they stood, a run of differences as one
// deletion of what stood and one insertion of what stands now.
func (body *trackedBody) changed(out *strings.Builder, source Paragraph, now string) {
	before := source.Text()
	beforeTokens, nowTokens := tokens(before), tokens(now)
	a := make([]string, len(beforeTokens))
	for i, token := range beforeTokens {
		a[i] = before[token.start:token.end]
	}
	b := make([]string, len(nowTokens))
	for i, token := range nowTokens {
		b[i] = now[token.start:token.end]
	}
	edits := difference(a, b)
	for k := 0; k < len(edits); {
		if edits[k].kind == editEqual {
			start := beforeTokens[edits[k].a].start
			for k < len(edits) && edits[k].kind == editEqual {
				k++
			}
			body.equal(out, source, start, beforeTokens[edits[k-1].a].end)
			continue
		}
		deleteFrom, deleteTo, at := -1, -1, -1
		var inserted strings.Builder
		for ; k < len(edits) && edits[k].kind != editEqual; k++ {
			if edits[k].kind == editDelete {
				if deleteFrom < 0 {
					deleteFrom = beforeTokens[edits[k].a].start
				}
				deleteTo = beforeTokens[edits[k].a].end
			} else {
				inserted.WriteString(b[edits[k].b])
			}
		}
		if deleteFrom >= 0 {
			body.deleted(out, source, deleteFrom, deleteTo)
			at = deleteFrom
		} else if k < len(edits) {
			at = beforeTokens[edits[k].a].start
		} else {
			at = len(before)
		}
		if inserted.Len() > 0 {
			body.inserted(out, inserted.String(), faceAt(source, at))
		}
	}
}

// faceAt is the face of the original before an offset: inserted words read as the text around them.
func faceAt(source Paragraph, offset int) Run {
	target, position := max(offset-1, 0), 0
	for _, run := range source.Runs {
		if target < position+len(run.Text) {
			return Run{Italic: run.Italic, Bold: run.Bold}
		}
		position += len(run.Text)
	}
	if last := len(source.Runs) - 1; last >= 0 {
		return Run{Italic: source.Runs[last].Italic, Bold: source.Runs[last].Bold}
	}
	return Run{}
}

// equal writes the runs of the original between two byte offsets as they stand.
func (body *trackedBody) equal(out *strings.Builder, source Paragraph, start, end int) {
	eachPiece(source, start, end, func(run Run) {
		out.WriteString(`<w:r>` + runProperties(run) + `<w:t xml:space="preserve">`)
		escape(out, run.Text)
		out.WriteString(`</w:t></w:r>`)
	})
}

// deleted writes the runs of the original between two byte offsets as one deletion.
func (body *trackedBody) deleted(out *strings.Builder, source Paragraph, start, end int) {
	if start >= end {
		return
	}
	out.WriteString(`<w:del ` + body.attributes() + `>`)
	eachPiece(source, start, end, func(run Run) {
		out.WriteString(`<w:r>` + runProperties(run) + `<w:delText xml:space="preserve">`)
		escape(out, run.Text)
		out.WriteString(`</w:delText></w:r>`)
	})
	out.WriteString(`</w:del>`)
}

// inserted writes new words as one insertion in a face.
func (body *trackedBody) inserted(out *strings.Builder, text string, face Run) {
	out.WriteString(`<w:ins ` + body.attributes() + `><w:r>` + runProperties(face) + `<w:t xml:space="preserve">`)
	escape(out, text)
	out.WriteString(`</w:t></w:r></w:ins>`)
}

// attributes are those of one revision: its number, its author and its time (PR6.5).
func (body *trackedBody) attributes() string {
	body.revision++
	var out strings.Builder
	out.WriteString(`w:id="` + strconv.Itoa(body.revision) + `" w:author="`)
	escape(&out, body.author)
	out.WriteString(`" w:date="` + body.date + `"`)
	return out.String()
}

// eachPiece calls write for the pieces of the runs of a paragraph between two byte offsets.
func eachPiece(source Paragraph, start, end int, write func(Run)) {
	position := 0
	for _, run := range source.Runs {
		from, to := max(start, position), min(end, position+len(run.Text))
		if from < to {
			write(Run{Text: run.Text[from-position : to-position], Italic: run.Italic, Bold: run.Bold})
		}
		position += len(run.Text)
	}
}

func runProperties(run Run) string {
	if !run.Italic && !run.Bold {
		return ""
	}
	properties := `<w:rPr>`
	if run.Bold {
		properties += `<w:b/><w:bCs/>`
	}
	if run.Italic {
		properties += `<w:i/><w:iCs/>`
	}
	return properties + `</w:rPr>`
}

// escape writes words as XML text; a sign XML has no place for becomes the replacement sign.
func escape(out *strings.Builder, text string) {
	_ = xml.EscapeText(out, []byte(text))
}

// document is word/document.xml.
func (body *trackedBody) document() string {
	var out strings.Builder
	out.WriteString(xmlDeclaration + `<w:document xmlns:w="` + wordNamespace + `"><w:body>`)
	for _, paragraph := range body.paragraphs {
		out.WriteString(`<w:p>`)
		properties := ""
		if paragraph.heading {
			properties += `<w:pStyle w:val="Heading1"/>`
		}
		switch paragraph.mark {
		case markInserted:
			properties += `<w:rPr><w:ins ` + body.attributes() + `/></w:rPr>`
		case markDeleted:
			properties += `<w:rPr><w:del ` + body.attributes() + `/></w:rPr>`
		}
		if properties != "" {
			out.WriteString(`<w:pPr>` + properties + `</w:pPr>`)
		}
		out.WriteString(paragraph.content.String())
		out.WriteString(`</w:p>`)
	}
	out.WriteString(trackedSection + `</w:body></w:document>`)
	return out.String()
}
