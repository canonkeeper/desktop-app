package researchchat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type attachmentReaderStub struct {
	attachments map[int]researchdomain.Attachment
	asked       []string
}

func (stub *attachmentReaderStub) ChatAttachment(_ context.Context, projectID, chatID string, number int) (researchdomain.Attachment, error) {
	stub.asked = append(stub.asked, fmt.Sprintf("%s/%s/%d", projectID, chatID, number))
	if number == 0 {
		latest := 0
		for each := range stub.attachments {
			latest = max(latest, each)
		}
		number = latest
	}
	attachment, ok := stub.attachments[number]
	if !ok {
		return researchdomain.Attachment{}, researchdomain.ErrNotFound
	}
	return attachment, nil
}

func (stub *attachmentReaderStub) ChatAttachmentNumbers(context.Context, string, string) ([]int, error) {
	numbers := make([]int, 0, len(stub.attachments))
	for number := 1; number <= 999; number++ {
		if _, ok := stub.attachments[number]; ok {
			numbers = append(numbers, number)
		}
	}
	return numbers, nil
}

func attachmentTools(attachments map[int]researchdomain.Attachment) (*ReadTools, *readRepositoryStub, *attachmentReaderStub) {
	tools, repository := newTestTools(researchdomain.ScopeProject, "")
	repository.authority.ChatID = "chat-1"
	reader := &attachmentReaderStub{attachments: attachments}
	return tools.WithAttachments(reader), repository, reader
}

// specs/2026-11-07-chat-attach-chapter.md CA3.2–CA3.4: the chapter attached to the chat is read by paragraphs with its
// signature and the link of its places; it is not a chapter of the book, so nothing is retained.
func TestAttachmentReadsTheChapterOfTheAuthorByParagraphs(t *testing.T) {
	tools, repository, reader := attachmentTools(map[int]researchdomain.Attachment{
		1: {Number: 1, Title: "Глава 634", Paragraphs: []string{"Кайл шёл.", "Ольм отстал.", "Кайл обернулся."}, Words: 6},
	})
	output, err := tools.Attachment(context.Background(), rootCall, AttachmentInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Приложение 1 «Глава 634» — " + researchdomain.AttachmentNote, "3 абзаца, 6 слов",
		"[прил. ¶1](attachment:1/1)", "¶1 Кайл шёл.", "¶3 Кайл обернулся.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output does not hold %q:\n%s", want, output)
		}
	}
	if len(repository.retained) != 0 || reader.asked[0] != repository.authority.ProjectID+"/chat-1/0" {
		t.Fatalf("an attachment is not a chapter of the book: retained %v, asked %v", repository.retained, reader.asked)
	}

	output, err = tools.Attachment(context.Background(), rootCall, AttachmentInput{Attachment: 1, From: 2, To: 2})
	if err != nil || strings.Contains(output, "¶1 Кайл") || !strings.Contains(output, "¶2 Ольм отстал.") || strings.Contains(output, "¶3") {
		t.Fatalf("a range of paragraphs = %v\n%s", err, output)
	}
}

// CA3.2: a long chapter is read by parts, the part saying where to go on.
func TestAttachmentReadsALongChapterByParts(t *testing.T) {
	paragraph := strings.TrimSpace(strings.Repeat("слово ", 1000))
	paragraphs := make([]string, 10)
	for index := range paragraphs {
		paragraphs[index] = paragraph
	}
	tools, _, _ := attachmentTools(map[int]researchdomain.Attachment{2: {Number: 2, Paragraphs: paragraphs, Words: 10000}})
	output, err := tools.Attachment(context.Background(), rootCall, AttachmentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Приложение 2 «без названия»") || !strings.Contains(output, "¶6 ") || strings.Contains(output, "¶7 ") ||
		!strings.Contains(output, "Показаны ¶1–¶6 из 10; дальше — from=7") {
		t.Fatalf("the first part of a long chapter:\n%s", output[:min(len(output), 400)]+"…"+output[max(0, len(output)-200):])
	}
	output, err = tools.Attachment(context.Background(), rootCall, AttachmentInput{Attachment: 2, From: 7})
	if err != nil || !strings.Contains(output, "¶10 ") || strings.Contains(output, "дальше") {
		t.Fatalf("the rest = %v", err)
	}
}

// CA3.3: a number the chat has not, or a chat without attachments, is a mistake the model corrects.
func TestAttachmentNamesTheAttachmentsOfTheChat(t *testing.T) {
	tools, _, _ := attachmentTools(map[int]researchdomain.Attachment{1: {Number: 1, Paragraphs: []string{"а"}, Words: 1}})
	_, err := tools.Attachment(context.Background(), rootCall, AttachmentInput{Attachment: 4})
	outcome, ok := AsToolOutcome(err)
	if !ok || outcome.Type != OutcomeAttachmentNotFound || !strings.Contains(outcome.Message, "1") {
		t.Fatalf("an unknown number = %v", err)
	}
	if _, err := tools.Attachment(context.Background(), rootCall, AttachmentInput{Attachment: 1, From: 5}); err == nil {
		t.Fatal("a paragraph past the end is a mistake")
	}

	empty, _, _ := attachmentTools(map[int]researchdomain.Attachment{})
	_, err = empty.Attachment(context.Background(), rootCall, AttachmentInput{})
	if outcome, ok := AsToolOutcome(err); !ok || outcome.Type != OutcomeAttachmentNotFound || !strings.Contains(outcome.Message, "нет приложенных глав") {
		t.Fatalf("a chat without attachments = %v", err)
	}
}
