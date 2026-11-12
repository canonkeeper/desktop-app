package researchchat

import (
	"context"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/authormemory"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	authormemoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	service "canonkeeper/backend/internal/services/researchchat"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	authstore "canonkeeper/backend/internal/store/auth"
	projectstore "canonkeeper/backend/internal/store/projects"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-11-chat-corrections.md CR1, CR1.5: the correction the author made in one chat comes with the answer about the
// heroine in a new chat, under the authority of its turn; it retains no chapter, not even its own, and a record of another
// saga about a heroine of the same name does not come.
func TestACorrectionOfOneChatComesWithTheAnswersOfAnotherPostgres(t *testing.T) {
	turn := startSagaToolsTurn(t, "ck_rch_author_words", researchdomain.ScopeProject)
	db, repository, ctx := turn.db, turn.repository, context.Background()

	accounts := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	outsider, err := accounts.CreateUser(ctx, auth.NewUser{Email: "outsider-words@example.test", DisplayName: "Чужой автор"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, outsider.ID, projects.CreateProjectInput{Title: "Чужая сага"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := authormemoryrepo.CreateInTx(ctx, tx, foreign.ID, outsider.ID, authormemory.Draft{
			Kind: authormemory.KindCorrection, Name: "Кира", Text: "Кира из чужой саги старше всех.",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	version, err := sagamemoryservice.NewSaga(sagamemoryrepo.New(db), sagamemoryrepo.New(db)).ChapterVersion(ctx, submitTurnProjectID, string(governance.SourceViewDraft))
	if err != nil {
		t.Fatal(err)
	}
	var key string
	for index := range version.Chapters {
		if version.Chapters[index].ID == turn.chapterID {
			key = version.Chapters[index].Key
		}
	}
	if _, err := memoryTools(db, repository).Remember(ctx, turn.call, service.RememberInput{
		Kind: "correction", Text: "Лампу остановила Кира, а не Гриша: так я решил.", Chapter: key, Name: "Кира",
	}); err != nil {
		t.Fatal(err)
	}

	another := startTurnInChat(t, db, turn.proof, turn.now, turn.chapterID, "ab000000-0000-4000-8000-0000000000b1", researchdomain.ScopeProject, "Кто остановил лампу?")
	tools := another.tools.WithAuthorMemory(repository)
	retained := func() int {
		t.Helper()
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM artifact_source_refs WHERE project_id = $1::uuid AND chapter_id IS NOT NULL`, submitTurnProjectID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := retained()
	for name, call := range map[string]func() (string, error){
		"entity": func() (string, error) { return tools.Entity(ctx, another.call, service.EntityInput{Name: "Кира"}) },
		"search": func() (string, error) {
			return tools.Search(ctx, another.call, service.SearchInput{Queries: []string{"остановила лампу"}})
		},
	} {
		output, err := call()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"Автор сказал об этом",
			"[поправка автора: важнее карточек и пересказов; если текст книги говорит иначе — назови оба варианта; глава " + key + "; имя: Кира] Лампу остановила Кира, а не Гриша: так я решил.",
		} {
			if !strings.Contains(output, want) {
				t.Errorf("%s: no %q in %q", name, want, output)
			}
		}
		if strings.Contains(output, "чужой саги") {
			t.Errorf("%s brought a record of another saga: %q", name, output)
		}
	}
	if got := retained(); got != before {
		t.Fatalf("the block retained chapters: %d, were %d", got, before)
	}
}
