package authormaterials

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	domain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
)

// ErrInvalidJob reports a Job the run cannot be given.
var ErrInvalidJob = errors.New("author materials: invalid job")

// callAttempts is how many times one section is asked: an answer that fails or does not read is asked again.
const callAttempts = 3

// answerFailed is the code of a Job whose model kept answering what does not read.
const answerFailed = "author_materials.unreadable_answer"

const mapInstructions = `Ты размечаешь раздел файла, в котором автор книги собрал свои материалы: навыки, места, персонажей, предметы, фракции.
Верни только JSON без пояснений.

Если раздел — таблица (строки с номерами столбцов): {"kind":"<тип>","header":<первая строка — заголовки столбцов: true или false>,"name":<номер столбца с названием записи>,"description":<номер столбца с описанием или null>}.

Если раздел — абзацы: {"kind":"<тип>","items":[{"name":"<имя записи дословно из текста>","kind":"<тип>","from":<номер первого абзаца о ней>,"to":<номер последнего абзаца о ней>}]}.
Запись — отдельная сущность с именем, о которой абзацы что-то сообщают. Обычное повествование без такой сущности — не запись.

<тип> — одно из: skill (навык, умение, заклинание), place (место), character (персонаж), item (предмет), faction (фракция, организация), other (другое). Тип таблицы подскажет название раздела и столбцов.
Не выдумывай имён и не добавляй записей, которых нет в тексте.`

// Model is the direct model client of the Job: the cards model behind the model_api gate, below the chat.
type Model interface {
	Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error)
}

// UsageRecorder adds the spend of the Job to the day's aggregates; it never fails the Job.
type UsageRecorder interface {
	RecordModelUsage(ctx context.Context, record quota.UsageRecord)
}

// RunStore is the port of the Job, under its claim: what the file holds, and its end, written with the candidates in
// one transaction. A Job that is stopped or fails leaves nothing of the text.
type RunStore interface {
	ImportParsed(ctx context.Context, claim jobs.WorkerClaim, importID string) (domain.Parsed, error)
	FinishImport(ctx context.Context, claim jobs.WorkerClaim, outcome ImportOutcome) error
}

// ImportOutcome is the terminal state of the Job.
type ImportOutcome struct {
	Status     jobs.Status
	Candidates []domain.Draft
	Error      *jobs.Error
}

// RunImport runs the Job: one or more calls per section, then the candidates.
type RunImport struct {
	Store     RunStore
	Model     Model
	ModelName string
	Usage     UsageRecorder
}

func (run RunImport) RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.AuthorMaterialsImportSubject) (jobs.Status, error) {
	if claim.Validate() != nil || claim.ProjectID != subject.ProjectID {
		return "", ErrInvalidJob
	}
	parsed, err := run.Store.ImportParsed(ctx, claim, subject.ImportID)
	if err != nil {
		return "", err
	}
	mappings := make([]domain.Mapping, len(parsed.Sections))
	for index, section := range parsed.Sections {
		mapping, ok, err := run.mapSection(ctx, claim, section)
		if err != nil {
			return "", err
		}
		if !ok {
			failure := jobs.Error{Code: answerFailed, Message: "the model did not map a section"}
			return jobs.StatusFailed, run.Store.FinishImport(ctx, claim, ImportOutcome{Status: jobs.StatusFailed, Error: &failure})
		}
		mappings[index] = mapping
	}
	candidates := domain.Candidates(parsed, mappings)
	return jobs.StatusSucceeded, run.Store.FinishImport(ctx, claim, ImportOutcome{Status: jobs.StatusSucceeded, Candidates: candidates})
}

// CancelForClaim ends a stopped Job: its import goes with the text.
func (run RunImport) CancelForClaim(ctx context.Context, claim jobs.WorkerClaim) error {
	return run.Store.FinishImport(ctx, claim, ImportOutcome{Status: jobs.StatusCanceled})
}

// FailForClaim ends a Job the worker gave up on.
func (run RunImport) FailForClaim(ctx context.Context, claim jobs.WorkerClaim, failure jobs.Error) (jobs.Status, error) {
	return jobs.StatusFailed, run.Store.FinishImport(ctx, claim, ImportOutcome{Status: jobs.StatusFailed, Error: &failure})
}

func (run RunImport) mapSection(ctx context.Context, claim jobs.WorkerClaim, section domain.Section) (domain.Mapping, bool, error) {
	var mapping domain.Mapping
	for chunkIndex, chunk := range section.Chunks() {
		answer, ok, err := run.ask(ctx, claim, chunk)
		if err != nil || !ok {
			return domain.Mapping{}, false, err
		}
		part, ok := answer.mapping(section)
		if !ok {
			return domain.Mapping{}, false, nil
		}
		if chunkIndex == 0 {
			mapping = part
			continue
		}
		mapping.Items = append(mapping.Items, part.Items...)
	}
	return mapping, true, nil
}

func (run RunImport) ask(ctx context.Context, claim jobs.WorkerClaim, input string) (mapAnswer, bool, error) {
	var lastErr error
	for range callAttempts {
		text, usage, err := run.Model.Complete(ctx, mapInstructions, input)
		if ctx.Err() != nil {
			return mapAnswer{}, false, ctx.Err()
		}
		if run.Usage != nil {
			run.Usage.RecordModelUsage(context.WithoutCancel(ctx), quota.UsageRecord{
				Source: quota.UsageAuthorMaterials, ProjectID: claim.ProjectID, JobID: claim.JobID, Model: run.ModelName, Usage: usage,
			})
		}
		if err != nil {
			lastErr = err
			continue
		}
		var answer mapAnswer
		if json.Unmarshal([]byte(jsonOf(text)), &answer) == nil {
			return answer, true, nil
		}
	}
	return mapAnswer{}, false, lastErr
}

// jsonOf is the object of an answer, without the fence a model may put around it.
func jsonOf(text string) string {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return ""
	}
	return text[start : end+1]
}

// mapAnswer is what the model answers about a section.
type mapAnswer struct {
	Kind        string `json:"kind"`
	Header      bool   `json:"header"`
	Name        *int   `json:"name"`
	Description *int   `json:"description"`
	Items       []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
		From int    `json:"from"`
		To   int    `json:"to"`
	} `json:"items"`
}

func (answer mapAnswer) mapping(section domain.Section) (domain.Mapping, bool) {
	kind, ok := domain.ParseKind(answer.Kind)
	if !ok {
		kind = domain.KindOther
	}
	mapping := domain.Mapping{Kind: kind, Header: answer.Header, Description: -1}
	if len(section.Rows) > 0 {
		if answer.Name == nil || *answer.Name < 0 {
			return domain.Mapping{}, false
		}
		mapping.Name = *answer.Name
		if answer.Description != nil {
			mapping.Description = *answer.Description
		}
		return mapping, true
	}
	for _, item := range answer.Items {
		itemKind, ok := domain.ParseKind(item.Kind)
		if !ok {
			itemKind = kind
		}
		mapping.Items = append(mapping.Items, domain.ProseItem{Name: item.Name, Kind: itemKind, From: item.From, To: item.To})
	}
	return mapping, true
}
