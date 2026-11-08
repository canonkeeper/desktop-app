package researchchat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/authormemory"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The memory of the author in the chat (specs/2026-11-01-writer-assistant-index.md, UC4): saga.remember
// keeps what the author said or approved, saga.memory reads it, each record signed with its kind.

const (
	// maxMemoryRecords and maxMemoryOutputRunes bound one answer of saga.memory; the rest is narrowed by
	// query or kind.
	maxMemoryRecords     = 500
	maxMemoryOutputRunes = 150000
)

// AuthorMemoryRepository is the port of the memory tools, every call under the authority of the chat turn.
type AuthorMemoryRepository interface {
	ReadAuthority(context.Context, ToolContext) (researchdomain.ReadAuthority, error)
	// RememberForTurn keeps a record written by the author of the turn, an editor or above;
	// researchdomain.ErrNotEditor for anyone else.
	RememberForTurn(context.Context, ToolContext, researchdomain.ReadAuthority, authormemory.Draft) error
	AuthorMemoryForTurn(context.Context, ToolContext, researchdomain.ReadAuthority, authormemory.Filter) (authormemory.Page, error)
}

// ChapterVersions reads the chapter order and keys of a text version, without the text.
type ChapterVersions interface {
	ChapterVersion(ctx context.Context, projectID, view string) (*sagamemory.Version, error)
}

// AuthorMemoryTools are saga.remember and saga.memory.
type AuthorMemoryTools struct {
	repository AuthorMemoryRepository
	chapters   ChapterVersions
}

func NewAuthorMemoryTools(repository AuthorMemoryRepository, chapters ChapterVersions) *AuthorMemoryTools {
	if repository == nil || chapters == nil {
		panic("research chat: author memory repository and chapter versions are required")
	}
	return &AuthorMemoryTools{repository: repository, chapters: chapters}
}

// RememberInput is a call of saga.remember.
type RememberInput struct {
	Kind    string
	Text    string
	Chapter string
	Name    string
}

// MemoryInput is a call of saga.memory.
type MemoryInput struct {
	Query string
	Kind  string
}

// Remember keeps a record of the project.
func (tools *AuthorMemoryTools) Remember(ctx context.Context, call ToolContext, input RememberInput) (string, error) {
	kind, ok := authormemory.ParseKind(input.Kind)
	if !ok {
		return "", argumentError("$.kind", "допустимы plan, correction, style_rule, rejected")
	}
	text, ok := authormemory.CleanText(input.Text)
	if !ok {
		return "", argumentError("$.text", "нужен непустой текст до %d символов", authormemory.MaxTextRunes)
	}
	name, ok := authormemory.CleanName(input.Name)
	if !ok {
		return "", argumentError("$.name", "имя до %d символов", authormemory.MaxNameRunes)
	}
	if err := ValidToolContext(ctx, call); err != nil {
		return "", err
	}
	authority, err := tools.repository.ReadAuthority(ctx, call)
	if err != nil {
		return "", err
	}
	// The Job of the turn that remembered it, so its line in the feed can undo it.
	draft := authormemory.Draft{Kind: kind, Text: text, Name: name, JobID: authority.JobID}
	signed := authormemory.Record{Kind: kind, Name: name}
	var keys map[string]string
	if key := strings.TrimSpace(input.Chapter); key != "" {
		version, err := tools.chapters.ChapterVersion(ctx, authority.ProjectID, authority.SourceView)
		if err != nil {
			return "", err
		}
		chapter, found := version.Find(key)
		if !found || authority.Scope == researchdomain.ScopeBook && chapter.BookID != authority.BookID {
			return "", argumentError("$.chapter", "глава %q не найдена; используйте сквозной номер главы или ключ из результатов search/chapters", key)
		}
		draft.ChapterID, signed.ChapterID = chapter.ID, chapter.ID
		keys = map[string]string{chapter.ID: chapter.Key}
	}
	if err := tools.repository.RememberForTurn(ctx, call, authority, draft); err != nil {
		if errors.Is(err, researchdomain.ErrNotEditor) {
			return "", &ToolOutcome{
				Type:    "memory_forbidden",
				Message: "Автор этого хода может только читать память: записывать в неё может редактор проекта. Скажите ему об этом.",
				Next:    []string{"tell_author"},
			}
		}
		return "", err
	}
	return fmt.Sprintf("Запись сохранена в памяти автора: %s. Автор видит её в ленте, где может отменить, и правит на карте саги, «Что помнит помощник».",
		signed.Signature(keys)), nil
}

// Memory reads the records of the project, newest first, each signed with its kind (R4.3).
func (tools *AuthorMemoryTools) Memory(ctx context.Context, call ToolContext, input MemoryInput) (string, error) {
	filter := authormemory.Filter{Limit: maxMemoryRecords}
	if value := strings.TrimSpace(input.Kind); value != "" {
		kind, ok := authormemory.ParseKind(value)
		if !ok {
			return "", argumentError("$.kind", "допустимы plan, correction, style_rule, rejected")
		}
		filter.Kinds = []authormemory.Kind{kind}
	}
	query, ok := authormemory.CleanQuery(input.Query)
	if !ok {
		return "", argumentError("$.query", "до %d символов", authormemory.MaxQueryRunes)
	}
	filter.Query = query
	if err := ValidToolContext(ctx, call); err != nil {
		return "", err
	}
	authority, err := tools.repository.ReadAuthority(ctx, call)
	if err != nil {
		return "", err
	}
	page, err := tools.repository.AuthorMemoryForTurn(ctx, call, authority, filter)
	if err != nil {
		return "", err
	}
	if len(page.Records) == 0 {
		if query == "" && len(filter.Kinds) == 0 {
			return "Записей: 0. В памяти автора пока ничего нет.", nil
		}
		return "Записей: 0. Ничего не найдено; без query и kind покажется вся память.", nil
	}
	keys, err := tools.chapterKeys(ctx, authority, page.Records)
	if err != nil {
		return "", err
	}
	return renderMemory(page, keys), nil
}

// chapterKeys names the chapters of the records in the text version of the turn.
func (tools *AuthorMemoryTools) chapterKeys(ctx context.Context, authority researchdomain.ReadAuthority, records []authormemory.Record) (map[string]string, error) {
	wanted := false
	for _, record := range records {
		wanted = wanted || record.ChapterID != ""
	}
	if !wanted {
		return nil, nil
	}
	version, err := tools.chapters.ChapterVersion(ctx, authority.ProjectID, authority.SourceView)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]string, len(version.Chapters))
	for index := range version.Chapters {
		keys[version.Chapters[index].ID] = version.Chapters[index].Key
	}
	return keys, nil
}

func renderMemory(page authormemory.Page, keys map[string]string) string {
	var out strings.Builder
	shown := 0
	var lines strings.Builder
	for _, record := range page.Records {
		line := fmt.Sprintf("[%s] %s (%s, %s)\n", record.Signature(keys), record.Text,
			record.AuthorName, record.CreatedAt.Format("02.01.2006"))
		if utf8.RuneCountInString(lines.String())+utf8.RuneCountInString(line) > maxMemoryOutputRunes {
			break
		}
		lines.WriteString(line)
		shown++
	}
	fmt.Fprintf(&out, "Записей: %d", page.Total)
	if int64(shown) < page.Total {
		fmt.Fprintf(&out, ", показаны %d самых новых; остальные — через query или kind", shown)
	}
	out.WriteString(". Это слова автора, а не текст книги: план ещё не случился в книге; поправка важнее карточек, но не текста, при расхождении назовите оба.\n\n")
	out.WriteString(lines.String())
	return out.String()
}
