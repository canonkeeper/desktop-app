package jobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"
)

func EncodeSubject(kind Kind, subject Subject) ([]byte, error) {
	if err := validateSubject(kind, subject); err != nil {
		return nil, err
	}
	return json.Marshal(subject)
}

func DecodeSubject(kind Kind, raw []byte) (Subject, error) {
	var d discriminator
	if err := decodeDiscriminator(raw, &d); err != nil {
		return nil, err
	}
	var subject Subject
	switch Kind(d.Type) {
	case KindImport:
		subject = ImportSubject{}
	case KindCanonTextIndex:
		subject = CanonTextIndexSubject{}
	case KindChapterCards:
		subject = ChapterCardsSubject{}
	case KindSagaLayer:
		subject = SagaLayerSubject{}
	case KindChapterDraft:
		subject = ChapterDraftSubject{}
	case KindAuthorMaterialsImport:
		subject = AuthorMaterialsImportSubject{}
	case KindResearchChatTurn:
		subject = ResearchChatTurnSubject{}
	case KindIllustration:
		subject = IllustrationSubject{}
	default:
		return nil, ErrValidation
	}
	target := subjectPointer(subject)
	if err := decodeStrict(raw, target); err != nil {
		return nil, err
	}
	subject = subjectValue(target)
	if err := validateSubject(kind, subject); err != nil {
		return nil, err
	}
	return subject, nil
}

func EncodeResult(kind Kind, result Result) ([]byte, error) {
	if err := validateResult(kind, result); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func DecodeResult(kind Kind, raw []byte) (Result, error) {
	var d discriminator
	if err := decodeDiscriminator(raw, &d); err != nil {
		return nil, err
	}
	var result Result
	switch Kind(d.Type) {
	case KindImport:
		result = ImportJobResult{}
	case KindCanonTextIndex:
		result = CanonTextIndexResult{}
	case KindChapterCards:
		result = ChapterCardsResult{}
	case KindSagaLayer:
		result = SagaLayerResult{}
	case KindChapterDraft:
		result = ChapterDraftResult{}
	case KindAuthorMaterialsImport:
		result = AuthorMaterialsImportResult{}
	case KindResearchChatTurn:
		result = ResearchChatTurnResult{}
	case KindIllustration:
		result = IllustrationResult{}
	default:
		return nil, ErrValidation
	}
	target := resultPointer(result)
	if err := decodeStrict(raw, target); err != nil {
		return nil, err
	}
	result = resultValue(target)
	if err := validateResult(kind, result); err != nil {
		return nil, err
	}
	return result, nil
}

func EncodeError(jobError Error) ([]byte, error) {
	if err := validateError(jobError); err != nil {
		return nil, err
	}
	return json.Marshal(jobError)
}

func DecodeError(raw []byte) (Error, error) {
	var jobError Error
	if err := decodeStrict(raw, &jobError); err != nil {
		return Error{}, err
	}
	if err := validateError(jobError); err != nil {
		return Error{}, err
	}
	return jobError, nil
}

type discriminator struct {
	Type string `json:"type"`
}

func decodeDiscriminator(raw []byte, target *discriminator) error {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil || target.Type == "" {
		return ErrValidation
	}
	return rejectTrailingJSON(decoder)
}

func decodeStrict(raw []byte, target any) error {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrValidation
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return ErrValidation
	}
	return nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	}
	return fmt.Errorf("%w: invalid request JSON", ErrValidation)
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	return rejectTrailingJSON(decoder)
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrValidation
			}
			if _, exists := seen[key]; exists {
				return ErrValidation
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return ErrValidation
	}
	_, err = decoder.Token()
	return err
}

func validateSubject(kind Kind, subject Subject) error {
	if subject == nil || subject.Kind() != kind || !uuidShaped(subject.Project()) {
		return ErrValidation
	}
	switch s := subject.(type) {
	case ImportSubject:
		queueLineageEmpty := s.ImportQueueID == "" && s.ImportQueueItemID == ""
		queueLineageValid := uuidShaped(s.ImportQueueID) && uuidShaped(s.ImportQueueItemID)
		updatesBook := s.BookID != ""
		if s.Type != KindImport || s.ID != s.ProjectID || s.ExpectedBookOrder < 0 || !queueLineageEmpty && !queueLineageValid ||
			updatesBook && (!uuidShaped(s.BookID) || s.ExpectedBookOrder > 0 || !queueLineageEmpty) || s.Preview && !updatesBook {
			return ErrValidation
		}
	case CanonTextIndexSubject:
		if s.Type != KindCanonTextIndex || !validSourceView(s.SourceView) || s.CatalogRevision == 0 {
			return ErrValidation
		}
	case ChapterCardsSubject:
		if s.Type != KindChapterCards || !validSourceView(s.SourceView) {
			return ErrValidation
		}
	case SagaLayerSubject:
		if s.Type != KindSagaLayer || !validSourceView(s.SourceView) {
			return ErrValidation
		}
	case ChapterDraftSubject:
		if !validChapterDraftSubject(s) {
			return ErrValidation
		}
	case AuthorMaterialsImportSubject:
		if s.Type != KindAuthorMaterialsImport || !uuidShaped(s.ImportID) {
			return ErrValidation
		}
	case ResearchChatTurnSubject:
		if s.Type != KindResearchChatTurn || !uuidShaped(s.ChatID) || !uuidShaped(s.TurnID) || !validSourceView(s.SourceView) {
			return ErrValidation
		}
		if s.Scope == "project" && s.BookID != nil || s.Scope == "book" && (s.BookID == nil || !uuidShaped(*s.BookID)) || s.Scope != "project" && s.Scope != "book" {
			return ErrValidation
		}
	case IllustrationSubject:
		if s.Type != KindIllustration || !uuidShaped(s.ImageID) || s.LayerID != "" && !uuidShaped(s.LayerID) {
			return ErrValidation
		}
	default:
		return ErrValidation
	}
	return nil
}

func validateResult(kind Kind, result Result) error {
	if result == nil || result.Kind() != kind {
		return ErrValidation
	}
	switch r := result.(type) {
	case ImportJobResult:
		if r.Type != KindImport || !uuidShaped(r.BookID) || r.ChapterCount < 0 || r.ParagraphCount < 0 || !r.validCounters() || !boundedText(r.SourceFileName, 255) {
			return ErrValidation
		}
	case CanonTextIndexResult:
		if r.Type != KindCanonTextIndex || !validSourceView(r.SourceView) || !r.TextCoverage.valid() {
			return ErrValidation
		}
	case ChapterCardsResult:
		if r.Type != KindChapterCards || !validSourceView(r.SourceView) || r.Chapters < 0 || r.Built < 0 || r.Built > r.Chapters {
			return ErrValidation
		}
	case SagaLayerResult:
		if r.Type != KindSagaLayer || !validSourceView(r.SourceView) || r.Chapters < 0 || r.Threads < 0 || r.BooksBuilt < 0 || r.Unverified < 0 {
			return ErrValidation
		}
	case AuthorMaterialsImportResult:
		if r.Type != KindAuthorMaterialsImport || r.Candidates < 0 {
			return ErrValidation
		}
	case ChapterDraftResult:
		if r.Type != KindChapterDraft || !validChapterDraftMode(r.Mode) || r.Total < 0 || r.Done < 0 || !validChapterDraftFindings(r) {
			return ErrValidation
		}
	case ResearchChatTurnResult:
		if r.Type != KindResearchChatTurn || !uuidShaped(r.ChatID) || !uuidShaped(r.TurnID) || strings.TrimSpace(r.Response) == "" {
			return ErrValidation
		}
	case IllustrationResult:
		if r.Type != KindIllustration || r.ImageNumber < 1 {
			return ErrValidation
		}
	default:
		return ErrValidation
	}
	return nil
}

func (coverage TextCoverage) valid() bool {
	return coverage.CurrentBooks >= 0 && coverage.TotalBooks >= 0 && coverage.NonCurrentBooks >= 0 && coverage.TotalNonCurrentBooks >= 0
}

func validateError(jobError Error) error {
	if !validErrorCode(jobError.Code) || !boundedText(jobError.Message, 240) {
		return ErrValidation
	}
	for _, r := range jobError.Message {
		if r < ' ' || r == 0x7f {
			return ErrValidation
		}
	}
	return nil
}

func validErrorCode(code string) bool {
	if code == "" {
		return false
	}
	for _, segment := range strings.Split(code, ".") {
		if segment == "" || segment[0] < 'a' || segment[0] > 'z' {
			return false
		}
		for i := 1; i < len(segment); i++ {
			c := segment[i]
			if c < 'a' || c > 'z' {
				if c < '0' || c > '9' {
					if c != '_' {
						return false
					}
				}
			}
		}
	}
	return len(code) <= 120
}

func validSourceView(view governance.SourceView) bool {
	return view == governance.SourceViewDraft || view == governance.SourceViewPublished
}

func boundedText(value string, maxBytes int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && len(value) <= maxBytes
}

func uuidShaped(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				if c < 'A' || c > 'F' {
					return false
				}
			}
		}
	}
	return true
}

func subjectPointer(subject Subject) any {
	switch subject.(type) {
	case ImportSubject:
		return &ImportSubject{}
	case CanonTextIndexSubject:
		return &CanonTextIndexSubject{}
	case ChapterCardsSubject:
		return &ChapterCardsSubject{}
	case SagaLayerSubject:
		return &SagaLayerSubject{}
	case ChapterDraftSubject:
		return &ChapterDraftSubject{}
	case AuthorMaterialsImportSubject:
		return &AuthorMaterialsImportSubject{}
	case ResearchChatTurnSubject:
		return &ResearchChatTurnSubject{}
	case IllustrationSubject:
		return &IllustrationSubject{}
	default:
		return nil
	}
}

func subjectValue(subject any) Subject {
	switch s := subject.(type) {
	case *ImportSubject:
		return *s
	case *CanonTextIndexSubject:
		return *s
	case *ChapterCardsSubject:
		return *s
	case *SagaLayerSubject:
		return *s
	case *ChapterDraftSubject:
		return *s
	case *AuthorMaterialsImportSubject:
		return *s
	case *ResearchChatTurnSubject:
		return *s
	case *IllustrationSubject:
		return *s
	default:
		return nil
	}
}

func resultPointer(result Result) any {
	switch result.(type) {
	case ImportJobResult:
		return &ImportJobResult{}
	case CanonTextIndexResult:
		return &CanonTextIndexResult{}
	case ChapterCardsResult:
		return &ChapterCardsResult{}
	case SagaLayerResult:
		return &SagaLayerResult{}
	case ChapterDraftResult:
		return &ChapterDraftResult{}
	case AuthorMaterialsImportResult:
		return &AuthorMaterialsImportResult{}
	case ResearchChatTurnResult:
		return &ResearchChatTurnResult{}
	case IllustrationResult:
		return &IllustrationResult{}
	default:
		return nil
	}
}

func resultValue(result any) Result {
	switch r := result.(type) {
	case *ImportJobResult:
		return *r
	case *CanonTextIndexResult:
		return *r
	case *ChapterCardsResult:
		return *r
	case *SagaLayerResult:
		return *r
	case *ChapterDraftResult:
		return *r
	case *AuthorMaterialsImportResult:
		return *r
	case *ResearchChatTurnResult:
		return *r
	case *IllustrationResult:
		return *r
	default:
		return nil
	}
}

// validChapterDraftSubject holds a Job of the writing agent with what its mode needs, or a sealed one of any mode: who
// and where, nothing of the author's text. The earlier writing (write) is only ever sealed.
func validChapterDraftSubject(s ChapterDraftSubject) bool {
	if s.Type != KindChapterDraft || !validChapterDraftMode(s.Mode) || !uuidShaped(s.DraftID) ||
		s.BookID != nil && !uuidShaped(*s.BookID) || s.AfterChapterID != nil && !uuidShaped(*s.AfterChapterID) {
		return false
	}
	if s.Erased || s.Mode == ChapterDraftWrite {
		return s.Erased && s.Revision == 0 && s.PlanNumber == 0 && len(s.Scenes) == 0 && s.From == nil && s.Tail == "" &&
			s.Place == nil && s.Instruction == "" && len(s.Seams) == 0
	}
	if s.Revision < 1 || utf8.RuneCountInString(s.Instruction) > 2000 || !validPlace(s.From) || !validPlace(s.Place) {
		return false
	}
	switch s.Mode {
	case ChapterDraftScenes:
		return s.PlanNumber >= 1 && len(s.Scenes) <= 20 && (s.Tail == "" || s.Tail == ChapterDraftTailKeep || s.Tail == ChapterDraftTailReplace) && s.Place == nil
	case ChapterDraftSelection:
		return s.Place != nil && s.From == nil && len(s.Scenes) == 0
	case ChapterDraftSeams:
		return len(s.Seams) >= 1 && len(s.Seams) <= 10 && s.Place == nil && s.From == nil
	case ChapterDraftCheck, ChapterDraftProofread:
		return s.Place == nil && s.From == nil && len(s.Scenes) == 0 && s.PlanNumber == 0 && s.Tail == "" && s.Instruction == "" && len(s.Seams) == 0
	}
	return false
}

// validChapterDraftFindings holds the corrections a proofreading keeps while it runs (specs/2026-11-07-writing-proofread.md,
// PR3.9): only of a proofreading, within the bounds of a note.
func validChapterDraftFindings(r ChapterDraftResult) bool {
	if len(r.Findings) == 0 {
		return true
	}
	if r.Mode != ChapterDraftProofread || len(r.Findings) > 2000 {
		return false
	}
	for _, finding := range r.Findings {
		if finding.Quote == "" || finding.Replacement == "" || finding.Category == "" || utf8.RuneCountInString(finding.Quote) > 2000 ||
			utf8.RuneCountInString(finding.Replacement) > 4000 || utf8.RuneCountInString(finding.Category) > 80 ||
			utf8.RuneCountInString(finding.Problem) > 2000 || finding.Start < 0 || finding.End <= finding.Start {
			return false
		}
	}
	return true
}

func validChapterDraftMode(mode string) bool {
	switch mode {
	case ChapterDraftWrite, ChapterDraftScenes, ChapterDraftSelection, ChapterDraftSeams, ChapterDraftCheck, ChapterDraftProofread:
		return true
	}
	return false
}

// validPlace holds a place by a quote or by its first and last phrases, within the limits of a note's anchors.
func validPlace(place *ChapterDraftPlace) bool {
	if place == nil {
		return true
	}
	byQuote, byBounds := place.Quote != "", place.From != "" && place.To != ""
	return byQuote != byBounds && utf8.RuneCountInString(place.Quote) <= 400000 &&
		utf8.RuneCountInString(place.From) <= 2000 && utf8.RuneCountInString(place.To) <= 2000
}
