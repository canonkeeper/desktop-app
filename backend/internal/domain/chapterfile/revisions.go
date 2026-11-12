package chapterfile

// revisionKind is what a run of the file with revisions is: a run of the original as it stood, or one revision of Word.
type revisionKind int8

const (
	revisionNone revisionKind = iota
	revisionInserted
	revisionDeleted
	revisionFormatted
)

// Revisions is the number of corrected places the file Tracked writes for the same chapter has
// (specs/2026-11-11-ux5-proofread-mode.md PM1.4): the page names it on «Скачать Word с исправлениями (K)» and in the
// window before the download, and the author who accepted four corrections reads four. It is the same writer, counted
// as the author sees the file: a stretch of revisions — inserted, deleted, of formatting, in any mix — with no run of
// the original and no paragraph mark as it stood between is one place, and an inserted or deleted paragraph mark
// belongs to the stretch it touches. A word replaced is one place, though Word lists its deletion and its insertion
// apart (the walk of TEST 11.11: four corrections accepted read «(6)»).
func Revisions(original Document, title string, paragraphs []Paragraph) int {
	body := &trackedBody{layout: original.Layout}
	body.write(original, title, paragraphs)
	count, previous := 0, revisionNone
	step := func(kind revisionKind) {
		if kind != revisionNone && previous == revisionNone {
			count++
		}
		previous = kind
	}
	for _, paragraph := range body.paragraphs {
		for _, kind := range paragraph.kinds {
			step(kind)
		}
		switch paragraph.revision {
		case markInserted:
			step(revisionInserted)
		case markDeleted:
			step(revisionDeleted)
		default:
			step(revisionNone)
		}
	}
	return count
}
