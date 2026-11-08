package researchchat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicAssignmentQuestionUsesSharedContractWithoutContext(t *testing.T) {
	for _, scope := range []Scope{ScopeProject, ScopeBook} {
		t.Run(string(scope), func(t *testing.T) {
			assignment := Assignment{
				Question:   "Вопрос с \"кавычками\"\nи текстом {\"question\":\"не wrapper\"}",
				ProjectRef: "project", SourceView: "draft", Scope: scope,
				SelectedContext: []ContextSelection{{BookTitle: "Private context", ChapterPosition: 2, ChapterTitle: "Do not display"}},
			}
			if scope == ScopeBook {
				assignment.BookRef = "book"
			}
			input, err := json.Marshal(assignment)
			if err != nil {
				t.Fatal(err)
			}
			if question, ok := PublicAssignmentQuestion(string(input)); !ok || question.Text != assignment.Question || question.Images != nil {
				t.Fatalf("public question = %+v, valid = %t", question, ok)
			}
		})
	}
}

func TestPublicAssignmentQuestionRejectsUnknownOrMalformedNativeInputs(t *testing.T) {
	for _, input := range []string{
		"Continue the assigned operation.",
		`null`,
		`{"question":"Only part of the wrapper"}`,
		`{"question":" ","projectRef":"project","sourceView":"draft","scope":"project"}`,
		`{"question":"Q","projectRef":"other","sourceView":"draft","scope":"project"}`,
		`{"question":"Q","projectRef":"project","sourceView":"other","scope":"project"}`,
		`{"question":"Q","projectRef":"project","sourceView":"draft","scope":"other"}`,
		`{"question":"Q","projectRef":"project","sourceView":"draft","scope":"book"}`,
		`{"question":"Q","projectRef":"project","bookRef":"book","sourceView":"draft","scope":"project"}`,
		`{"question":"Q","projectRef":"project","sourceView":"draft","scope":"project","internalPrompt":"private"}`,
		`{"question":"Q","projectRef":"project","sourceView":"draft","scope":"project","selectedContext":[{"internalPrompt":"private"}]}`,
		`{"question":"Q","projectRef":"project","sourceView":"draft","scope":"project"} {"question":"extra"}`,
		`{"question":"Q","projectRef":"project","sourceView":"draft","scope":"project","attachedImages":[1,1]}`,
		`{"question":"Q","projectRef":"project","sourceView":"draft","scope":"project","attachedImages":[0]}`,
		`{"question":"Q","projectRef":"project","sourceView":"draft","scope":"project","attachedImages":[1,2,3,4,5,6,7,8,9]}`,
	} {
		if question, ok := PublicAssignmentQuestion(input); ok || question.Text != "" {
			t.Fatalf("unexpected public question = %+v, valid = %t", question, ok)
		}
	}
}

// specs/2026-10-29-saga-illustrations.md R15.9: the images attached to a question reach the model in
// the turn's input and the timeline shows them at the question; a question without them reads as
// before.
func TestPublicAssignmentQuestionCarriesTheAttachedImages(t *testing.T) {
	input, err := json.Marshal(Assignment{
		Question: "Нарисуй Мирона с этим лицом", ProjectRef: "project", SourceView: "published", Scope: ScopeProject,
		AttachedImages: []int{81, 82},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(input), `"attachedImages":[81,82]`) {
		t.Fatalf("the input of the turn = %s", input)
	}
	question, ok := PublicAssignmentQuestion(string(input))
	if !ok || question.Text != "Нарисуй Мирона с этим лицом" || len(question.Images) != 2 || question.Images[0] != 81 || question.Images[1] != 82 {
		t.Fatalf("public question = %+v, valid = %t", question, ok)
	}
	plain, err := json.Marshal(Assignment{Question: "Q", ProjectRef: "project", SourceView: "draft", Scope: ScopeProject})
	if err != nil || strings.Contains(string(plain), "attachedImages") {
		t.Fatalf("a question without images = %s, %v", plain, err)
	}
}
