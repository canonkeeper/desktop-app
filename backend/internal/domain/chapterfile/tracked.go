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
// paragraphs now, and «Отклонить все» the original. The paragraphs now carry their italic and bold
// (specs/2026-11-08-italic-bold-in-text.md TF7.2): an equal word in the face it had stays as it stood, an equal word
// whose face changed is a revision of its formatting that keeps the properties of the original, and inserted words are
// in their face now. The title of a draft has no face of its own and keeps that of the original, and so do paragraphs
// now without one italic or bold word: a working text from before its faces were kept, or from a text without them. The
// empty paragraphs of the original keep their places.
// An original with the look of its Word file is written in it (PR6.7): its styles, theme and section, the properties of
// its paragraphs and runs as they stood, a changed face switched in them.
func Tracked(writer io.Writer, original Document, title string, paragraphs []Paragraph, author string, at time.Time) error {
	body := &trackedBody{author: author, date: at.UTC().Truncate(time.Second).Format(time.RFC3339), layout: original.Layout}
	body.write(original, title, paragraphs)
	return writeTrackedPackage(writer, body.document(), title, author, at, original.Layout)
}

type paragraphMark int8

const (
	markNormal paragraphMark = iota
	markInserted
	markDeleted
)

// outParagraph is a paragraph of the file as it will be written.
type outParagraph struct {
	heading  bool
	content  strings.Builder
	revision paragraphMark
	// whole says the content is one revision of the mark's kind: a paragraph inserted or deleted whole.
	whole paragraphMark
	// props and mark are the properties of the paragraph of the original it stands for, or of the one before it (PR6.7).
	props, mark string
	// kinds are what each run written in content is, in order: the revisions of the file are counted by them (Revisions).
	kinds []revisionKind
}

type trackedBody struct {
	author, date string
	revision     int
	paragraphs   []*outParagraph
	layout       *Layout
	// last is the paragraph of the original written last: an inserted paragraph takes its look.
	last *Paragraph
	// current is the paragraph written now: what is written goes into its content.
	current *outParagraph
}

// add writes a paragraph for one of the original, or, without one, an inserted paragraph in the look of the paragraph of
// the original before it.
func (body *trackedBody) add(heading bool, source *Paragraph) *outParagraph {
	paragraph := &outParagraph{heading: heading}
	if source != nil && !heading {
		body.last = source
	}
	if look := source; look != nil || body.last != nil {
		if look == nil {
			look = body.last
		}
		paragraph.props, paragraph.mark = look.Props, look.Mark
	}
	body.paragraphs = append(body.paragraphs, paragraph)
	body.current = paragraph
	return paragraph
}

// wrote notes the kind of a run just written into the paragraph written now.
func (body *trackedBody) wrote(kind revisionKind) {
	if body.current != nil {
		body.current.kinds = append(body.current.kinds, kind)
	}
}

// lastFace is the face of the end of the paragraph of the original written last: an inserted paragraph goes on in it.
func (body *trackedBody) lastFace() Run {
	if body.last == nil {
		return Run{}
	}
	return faceAt(*body.last, len(body.last.Text()))
}

// write lays out the paragraphs: the title, then the paragraphs of the original aligned with the paragraphs now (PR6.2).
func (body *trackedBody) write(original Document, title string, paragraphs []Paragraph) {
	if len(original.Paragraphs) > 0 {
		// A paragraph inserted before every paragraph of the original takes the look of the first.
		body.last = &original.Paragraphs[0]
	}
	if original.Title != nil {
		heading := body.add(true, original.Title)
		// The title now has the words of the draft and the faces of the original.
		if now := strings.TrimSpace(title); now == "" || now == original.Title.Text() {
			body.equal(&heading.content, *original.Title, 0, len(original.Title.Text()), nil, 0)
		} else {
			body.changed(&heading.content, *original.Title, Paragraph{Runs: []Run{{Text: now}}}, false)
		}
	}
	lines := make([]string, len(paragraphs))
	faced := false
	for i, paragraph := range paragraphs {
		lines[i] = paragraph.Text()
		for _, run := range paragraph.Runs {
			faced = faced || run.Italic || run.Bold
		}
	}
	facesOf := func(index int) *Paragraph {
		if !faced {
			return nil
		}
		return &paragraphs[index]
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
				body.add(false, &original.Paragraphs[next])
			}
		}
		next = max(next, index+1)
	}
	edits := difference(texts, lines)
	for k := 0; k < len(edits); {
		if edits[k].kind == editEqual {
			upTo(places[edits[k].a])
			paragraph := body.add(false, &original.Paragraphs[places[edits[k].a]])
			source := original.Paragraphs[places[edits[k].a]]
			body.equal(&paragraph.content, source, 0, len(source.Text()), facesOf(edits[k].b), 0)
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
			body.changed(&body.add(false, &original.Paragraphs[places[deleted[p]]]).content, original.Paragraphs[places[deleted[p]]], paragraphs[inserted[p]], faced)
		}
		for _, index := range deleted[pairs:] {
			upTo(places[index])
			paragraph := body.add(false, &original.Paragraphs[places[index]])
			paragraph.revision, paragraph.whole = markDeleted, markDeleted
			source := original.Paragraphs[places[index]]
			body.deleted(&paragraph.content, source, 0, len(source.Text()))
		}
		for _, index := range inserted[pairs:] {
			look := body.lastFace()
			paragraph := body.add(false, nil)
			paragraph.revision, paragraph.whole = markInserted, markInserted
			body.inserted(&paragraph.content, paragraphs[index].Runs, look, faced)
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
		body.paragraphs[last].revision = markNormal
		if first > 0 {
			body.paragraphs[first-1].revision = kind
		}
	}
}

// changed writes a paragraph changed word by word (PR6.3): equal tokens as they stood, a run of differences as one
// deletion of what stood and one insertion of what stands now. With faces, the paragraph now gives the faces of its
// equal and inserted words (TF7.2); without, they are those of the original around them.
func (body *trackedBody) changed(out *strings.Builder, source Paragraph, current Paragraph, faces bool) {
	before, now := source.Text(), current.Text()
	beforeTokens, nowTokens := tokens(before), tokens(now)
	a := make([]string, len(beforeTokens))
	for i, token := range beforeTokens {
		a[i] = before[token.start:token.end]
	}
	b := make([]string, len(nowTokens))
	for i, token := range nowTokens {
		b[i] = now[token.start:token.end]
	}
	var facesNow *Paragraph
	if faces {
		facesNow = &current
	}
	edits := difference(a, b)
	for k := 0; k < len(edits); {
		if edits[k].kind == editEqual {
			start, nowStart := beforeTokens[edits[k].a].start, nowTokens[edits[k].b].start
			for k < len(edits) && edits[k].kind == editEqual {
				k++
			}
			body.equal(out, source, start, beforeTokens[edits[k-1].a].end, facesNow, nowStart)
			continue
		}
		deleteFrom, deleteTo, at := -1, -1, -1
		insertFrom, insertTo := -1, -1
		for ; k < len(edits) && edits[k].kind != editEqual; k++ {
			if edits[k].kind == editDelete {
				if deleteFrom < 0 {
					deleteFrom = beforeTokens[edits[k].a].start
				}
				deleteTo = beforeTokens[edits[k].a].end
			} else {
				if insertFrom < 0 {
					insertFrom = nowTokens[edits[k].b].start
				}
				insertTo = nowTokens[edits[k].b].end
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
		if insertFrom >= 0 {
			body.inserted(out, pieces(current, insertFrom, insertTo), faceAt(source, at), faces)
		}
	}
}

// faceAt is the face of the original before an offset: inserted words read as the text around them.
func faceAt(source Paragraph, offset int) Run {
	target, position := max(offset-1, 0), 0
	for _, run := range source.Runs {
		if target < position+len(run.Text) {
			return Run{Italic: run.Italic, Bold: run.Bold, Props: run.Props}
		}
		position += len(run.Text)
	}
	if last := len(source.Runs) - 1; last >= 0 {
		return Run{Italic: source.Runs[last].Italic, Bold: source.Runs[last].Bold, Props: source.Runs[last].Props}
	}
	return Run{}
}

// equal writes the runs of the original between two byte offsets as they stand. With the paragraph now, whose words
// from nowStart are the same, a piece in another face now is written in it with the properties of the original kept in
// a revision of its formatting (TF7.2).
func (body *trackedBody) equal(out *strings.Builder, source Paragraph, start, end int, now *Paragraph, nowStart int) {
	eachPiece(source, start, end, func(run Run, at int) {
		if now == nil {
			body.run(out, run)
			return
		}
		eachPiece(*now, nowStart+at-start, nowStart+at-start+len(run.Text), func(current Run, _ int) {
			piece := run
			piece.Text = current.Text
			if current.Italic == run.Italic && current.Bold == run.Bold {
				body.run(out, piece)
				return
			}
			out.WriteString(`<w:r><w:rPr>` + body.facedProperties(run, current) + `<w:rPrChange ` + body.attributes() + `><w:rPr>` +
				body.properties(run) + `</w:rPr></w:rPrChange></w:rPr><w:t xml:space="preserve">`)
			escape(out, piece.Text)
			out.WriteString(`</w:t></w:r>`)
			body.wrote(revisionFormatted)
		})
	})
}

// run writes a run of the original as it stands.
func (body *trackedBody) run(out *strings.Builder, run Run) {
	out.WriteString(`<w:r>` + body.runProperties(run) + `<w:t xml:space="preserve">`)
	escape(out, run.Text)
	out.WriteString(`</w:t></w:r>`)
	body.wrote(revisionNone)
}

// deleted writes the runs of the original between two byte offsets as one deletion.
func (body *trackedBody) deleted(out *strings.Builder, source Paragraph, start, end int) {
	if start >= end {
		return
	}
	out.WriteString(`<w:del ` + body.attributes() + `>`)
	eachPiece(source, start, end, func(run Run, _ int) {
		out.WriteString(`<w:r>` + body.runProperties(run) + `<w:delText xml:space="preserve">`)
		escape(out, run.Text)
		out.WriteString(`</w:delText></w:r>`)
	})
	out.WriteString(`</w:del>`)
	body.wrote(revisionDeleted)
}

// inserted writes new words as one insertion in the look of the original around them: in their faces now, or, without
// faces of their own, in the face of the original too.
func (body *trackedBody) inserted(out *strings.Builder, words []Run, look Run, faces bool) {
	out.WriteString(`<w:ins ` + body.attributes() + `>`)
	for _, word := range words {
		properties := body.runProperties(look)
		if faces {
			properties = ""
			if inner := body.facedProperties(look, word); inner != "" {
				properties = `<w:rPr>` + inner + `</w:rPr>`
			}
		}
		out.WriteString(`<w:r>` + properties + `<w:t xml:space="preserve">`)
		escape(out, word.Text)
		out.WriteString(`</w:t></w:r>`)
	}
	out.WriteString(`</w:ins>`)
	if len(words) > 0 {
		body.wrote(revisionInserted)
	}
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

// eachPiece calls write for the pieces of the runs of a paragraph between two byte offsets, with the offset each starts at.
func eachPiece(source Paragraph, start, end int, write func(Run, int)) {
	position := 0
	for _, run := range source.Runs {
		from, to := max(start, position), min(end, position+len(run.Text))
		if from < to {
			write(Run{Text: run.Text[from-position : to-position], Italic: run.Italic, Bold: run.Bold, Props: run.Props}, from)
		}
		position += len(run.Text)
	}
}

// pieces are the runs of a paragraph between two byte offsets.
func pieces(paragraph Paragraph, start, end int) []Run {
	var out []Run
	eachPiece(paragraph, start, end, func(run Run, _ int) { out = append(out, run) })
	return out
}

// runProperties are the properties element of a run of the original as it stood, or nothing.
func (body *trackedBody) runProperties(run Run) string {
	if inner := body.properties(run); inner != "" {
		return `<w:rPr>` + inner + `</w:rPr>`
	}
	return ""
}

// properties are the inside of the properties of a run of the original: its look as it stood (PR6.7), otherwise its
// italic and bold.
func (body *trackedBody) properties(run Run) string {
	if body.layout != nil {
		return run.Props
	}
	return faceProperties(run.Italic, run.Bold)
}

// facedProperties are the inside of the properties of a run in the look of a run of the original and the face of
// another (TF7.2): with the look of the file, its properties with a changed italic or bold switched in their place.
func (body *trackedBody) facedProperties(look, face Run) string {
	if body.layout == nil {
		return faceProperties(face.Italic, face.Bold)
	}
	properties := look.Props
	if face.Bold != look.Bold {
		properties = toggled(properties, "b", face.Bold)
	}
	if face.Italic != look.Italic {
		properties = toggled(properties, "i", face.Italic)
	}
	return properties
}

// faceProperties are the properties of italic and bold of a file without a look of its own.
func faceProperties(italic, bold bool) string {
	properties := ""
	if bold {
		properties += `<w:b/><w:bCs/>`
	}
	if italic {
		properties += `<w:i/><w:iCs/>`
	}
	return properties
}

// toggleOrder is the place of the first properties of a run in w:rPr, whose order Word holds to: a toggle goes before
// every property after it, an unknown or extended one included.
var toggleOrder = map[string]int{"rStyle": 0, "rFonts": 1, "b": 2, "bCs": 3, "i": 4, "iCs": 5}

// toggled is the inside of the properties of a run with its toggle name — "i" or "b" — set on or off explicitly, in its
// place by the order of w:rPr: the toggle that stood goes, so the face does not hang on its style any more.
func toggled(properties, name string, on bool) string {
	value := `<w:` + name + `/>`
	if !on {
		value = `<w:` + name + ` w:val="0"/>`
	}
	rank := func(element xml.Name) int {
		if order, known := toggleOrder[element.Local]; known && element.Space == "w" {
			return order
		}
		return len(toggleOrder)
	}
	decoder := xml.NewDecoder(strings.NewReader(properties))
	var out strings.Builder
	placed, depth := false, 0
	var from int64
	for {
		before := decoder.InputOffset()
		token, err := decoder.RawToken()
		if err != nil {
			break
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				from = before
				if !placed && rank(element.Name) > toggleOrder[name] {
					out.WriteString(value)
					placed = true
				}
			}
		case xml.EndElement:
			depth--
			if depth > 0 {
				continue
			}
			if element.Name.Space == "w" && element.Name.Local == name {
				if !placed {
					out.WriteString(value)
					placed = true
				}
				continue
			}
			out.WriteString(properties[from:decoder.InputOffset()])
		}
	}
	if !placed {
		out.WriteString(value)
	}
	return out.String()
}

// escape writes words as XML text; a sign XML has no place for becomes the replacement sign.
func escape(out *strings.Builder, text string) {
	_ = xml.EscapeText(out, []byte(text))
}

// document is word/document.xml.
func (body *trackedBody) document() string {
	var out strings.Builder
	namespaces, section := ` xmlns:w="`+wordNamespace+`"`, trackedSection
	if body.layout != nil {
		// The namespaces of the original declare those of its properties (PR6.7); its own declares w.
		namespaces = body.layout.Namespaces
		if body.layout.Section != "" {
			section = body.layout.Section
		}
	}
	out.WriteString(xmlDeclaration + `<w:document` + namespaces + `><w:body>`)
	for _, paragraph := range body.paragraphs {
		out.WriteString(`<w:p>`)
		properties, mark := "", ""
		switch {
		case body.layout != nil:
			properties, mark = paragraph.props, paragraph.mark
		case paragraph.heading:
			properties = `<w:pStyle w:val="Heading1"/>`
		}
		switch paragraph.revision {
		case markInserted:
			mark = `<w:ins ` + body.attributes() + `/>` + mark
		case markDeleted:
			mark = `<w:del ` + body.attributes() + `/>` + mark
		}
		// The properties of the mark close the paragraph's: a list, a section and revisions are not kept (PR1.7).
		if mark != "" {
			properties += `<w:rPr>` + mark + `</w:rPr>`
		}
		if properties != "" {
			out.WriteString(`<w:pPr>` + properties + `</w:pPr>`)
		}
		out.WriteString(paragraph.content.String())
		out.WriteString(`</w:p>`)
	}
	out.WriteString(section + `</w:body></w:document>`)
	return out.String()
}
