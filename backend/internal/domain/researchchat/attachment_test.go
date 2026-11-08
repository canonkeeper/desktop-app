package researchchat

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// specs/2026-11-07-chat-attach-chapter.md CA1.1, CA1.2: the attachment is made of the one reader of a chapter file — its
// paragraphs with words without the title, its words — and named by the title of the chapter, else by the file.
func TestTheAttachmentIsTheChapterTheFileHolds(t *testing.T) {
	markdown, err := chapterfile.Parse("перевал.md", []byte("# Глава 7. Перевал\n\nКайл шёл *быстро*.\n\nОльм отстал."))
	if err != nil {
		t.Fatal(err)
	}
	attachment := NewAttachment(markdown, "перевал.md")
	if attachment.Title != "Глава 7. Перевал" || attachment.FileName != "перевал.md" || attachment.Format != "md" ||
		!reflect.DeepEqual(attachment.Paragraphs, []string{"Кайл шёл быстро.", "Ольм отстал."}) || attachment.Words != 5 {
		t.Fatalf("attachment of a markdown file = %+v", attachment)
	}

	plain, err := chapterfile.Parse("Новая глава.txt", []byte("Первый абзац.\nВторой абзац."))
	if err != nil {
		t.Fatal(err)
	}
	if attachment := NewAttachment(plain, "Новая глава.txt"); attachment.Title != "Новая глава" || attachment.Format != "txt" || len(attachment.Paragraphs) != 2 {
		t.Fatalf("a file without a heading is named by the file: %+v", attachment)
	}

	pasted, err := chapterfile.ParseText("Просто текст.\nЕщё текст.")
	if err != nil {
		t.Fatal(err)
	}
	if attachment := NewAttachment(pasted, ""); attachment.Title != "" || attachment.FileName != "" || attachment.Format != "text" {
		t.Fatalf("a pasted text without a chapter line has no title: %+v", attachment)
	}

	titled, err := chapterfile.ParseText("Глава 7. Перевал\nКайл шёл.")
	if err != nil {
		t.Fatal(err)
	}
	if attachment := NewAttachment(titled, ""); attachment.Title != "Глава 7. Перевал" || !reflect.DeepEqual(attachment.Paragraphs, []string{"Кайл шёл."}) {
		t.Fatalf("a pasted chapter is named by its chapter line: %+v", attachment)
	}

	long := strings.Repeat("я", MaxAttachmentTitleRunes+40) + ".txt"
	if attachment := NewAttachment(plain, long); len([]rune(attachment.Title)) != MaxAttachmentTitleRunes {
		t.Fatalf("the title is cut to %d signs: %d", MaxAttachmentTitleRunes, len([]rune(attachment.Title)))
	}
}

// CA1.4, CA1.5: the input of the turn carries the header of the attachment, signed, never its text; the timeline reads the
// number, the title and the words of it at the question, and a correction carries none.
func TestTheQuestionCarriesTheHeaderOfTheAttachmentSigned(t *testing.T) {
	attachment := Attachment{Number: 2, Title: "Глава 634", Paragraphs: []string{"Тайный абзац главы."}, Words: 3, Format: "md"}
	assignment := Assignment{
		Question: "Ложится ли глава на канон?", ProjectRef: "project", SourceView: "draft", Scope: ScopeProject,
		Attachment: attachment.Header(),
	}
	input, err := json.Marshal(assignment)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(input), AttachmentNote) || strings.Contains(string(input), "Тайный абзац") ||
		!strings.Contains(string(input), `"paragraphs":1`) || !strings.Contains(string(input), `"number":2`) {
		t.Fatalf("the input of the turn = %s", input)
	}
	question, ok := PublicAssignmentQuestion(string(input))
	if !ok || question.Attachment == nil || *question.Attachment != (PublicAttachment{Number: 2, Title: "Глава 634", Words: 3}) {
		t.Fatalf("public question = %+v, valid = %t", question, ok)
	}

	plain, _ := json.Marshal(Assignment{Question: "Q", ProjectRef: "project", SourceView: "draft", Scope: ScopeProject})
	if question, ok := PublicAssignmentQuestion(string(plain)); !ok || question.Attachment != nil || strings.Contains(string(plain), "attachment") {
		t.Fatalf("a question without an attachment = %s, %+v", plain, question)
	}

	steer := assignment
	steer.Correction = true
	corrected, _ := json.Marshal(steer)
	if _, ok := PublicAssignmentQuestion(string(corrected)); ok {
		t.Fatalf("a correction with an attachment is not a public question: %s", corrected)
	}
	broken := assignment
	broken.Attachment = &AssignmentAttachment{Number: 0, Words: 1, Paragraphs: 1, Note: AttachmentNote}
	zero, _ := json.Marshal(broken)
	if _, ok := PublicAssignmentQuestion(string(zero)); ok {
		t.Fatalf("an attachment without its number is not public: %s", zero)
	}
	// A turn written under another wording of the signature keeps its question in the feed.
	older := assignment
	older.Attachment = &AssignmentAttachment{Number: 1, Words: 1, Paragraphs: 1, Note: "текст автора"}
	reworded, _ := json.Marshal(older)
	if question, ok := PublicAssignmentQuestion(string(reworded)); !ok || question.Attachment == nil {
		t.Fatalf("an older signature hides the question: %s", reworded)
	}
}
