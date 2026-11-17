package sagamemory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"strconv"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/modelthread"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// threadedCardsModel answers as namedCardsModel and keeps the thread of each call by its instructions.
type threadedCardsModel struct {
	namedCardsModel
	mu      sync.Mutex
	threads map[string][][]string
}

func (model *threadedCardsModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	parts, _ := modelthread.Of(ctx)
	model.mu.Lock()
	if model.threads == nil {
		model.threads = map[string][][]string{}
	}
	model.threads[instructions] = append(model.threads[instructions], parts)
	model.mu.Unlock()
	return model.namedCardsModel.Complete(ctx, instructions, input)
}

func (model *threadedCardsModel) of(instructions string) [][]string {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([][]string(nil), model.threads[instructions]...)
}

// bucketOf is the bucket of the key of a chapter by the words of the spec: the first 8 bytes of its SHA-256 as a number,
// modulo 48.
func bucketOf(key string) string {
	sum := sha256.Sum256([]byte(key))
	return strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])%48, 10)
}

// specs/2026-11-17-model-cache.md MC1.3: the card and the pass of a chapter go under the threads of their step in the bucket
// of the key of the chapter within the Job — one bucket, two steps —, and the register under the thread of the names of the
// Job. A card written outside a Job (the operator's build) goes with none.
func TestTheCardsAndTheNamesGoUnderTheThreadsOfTheirJob(t *testing.T) {
	writer := NewCardWriter(&threadedCardsModel{}, domain.CurrentCardInstructions)
	for _, key := range []string{"12", "b2c7"} {
		model := &threadedCardsModel{}
		writer.model = model
		chapter := &domain.Chapter{ID: "chapter-" + key, Key: key, Title: "Глава", Paragraphs: []domain.Paragraph{{Text: "Застава стояла у перевала."}}}
		if _, err := writer.Write(jobs.WithJobID(context.Background(), "job-cards-north"), chapter); err != nil {
			t.Fatal(err)
		}
		card, pass := model.of(domain.CurrentCardInstructions.Text), model.of(domain.CurrentCardInstructions.Pass)
		if want := []string{"cards", "job-cards-north", "card", bucketOf(key)}; len(card) != 1 || !slices.Equal(card[0], want) {
			t.Fatalf("the card of %q went under %q, want %q", key, card, want)
		}
		if want := []string{"cards", "job-cards-north", "card_pass", bucketOf(key)}; len(pass) != 1 || !slices.Equal(pass[0], want) {
			t.Fatalf("the pass of %q went under %q, want %q", key, pass, want)
		}
	}
	operator := &threadedCardsModel{}
	writer.model = operator
	if _, err := writer.Write(context.Background(), &domain.Chapter{ID: "c", Key: "1", Paragraphs: []domain.Paragraph{{Text: "Текст."}}}); err != nil {
		t.Fatal(err)
	}
	if card := operator.of(domain.CurrentCardInstructions.Text); len(card) != 1 || card[0] != nil {
		t.Fatalf("a card outside a Job went under %q", card)
	}

	model := &threadedCardsModel{}
	_, build, _ := namesFixture(&namesModel{answer: namesAnswer})
	build.model, build.writer = model, NewCardWriter(model, domain.CurrentCardInstructions)
	if _, err := build.RunForClaim(jobs.WithJobID(context.Background(), cardsJobID), cardsClaim, cardsSubject, false); err != nil {
		t.Fatal(err)
	}
	if names := model.of(domain.NameInstructions); len(names) != 1 || !slices.Equal(names[0], []string{"names", cardsJobID}) {
		t.Fatalf("the register went under %q", names)
	}
}
