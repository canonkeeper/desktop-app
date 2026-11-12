package quotastore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

const quotaDemoProject = "93000000-0000-4000-8000-0000000000d1"

// demoProject is the demo saga of the owner: a project with the sign of the table.
func (seed *sourceSeed) demoProject(id, owner string) {
	seed.project(id, owner)
	seed.exec(`UPDATE projects SET demo = true WHERE id = $1`, id)
}

// specs/2026-11-09-demo-saga.md DS3.2, DS3.3, DS5.2: the demo saga is not a saga of the limit of its owner and its
// text is not in his totals; no book and no text is added to it, whatever his limits are; what comes back from its
// trash is not checked, and neither is the saga itself.
func TestTheDemoSagaStaysOutOfTheLimitsOfItsOwner(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_demo_saga")
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "demo-saga@example.test")
	seed.project(quotaProjectA, quotaWriterA)
	seed.book(quotaProjectA, false, 100)
	seed.demoProject(quotaDemoProject, quotaWriterA)
	seed.book(quotaDemoProject, false, 4000, 5000)
	// The owner stands at every limit with his own saga alone: one saga, one book, one chapter, 100 characters.
	seed.exec(`INSERT INTO writer_quota_overrides (user_id, projects, project_books, account_books, project_chapters, account_chapters, project_characters, account_characters, updated_at)
		VALUES ($1, 1, 1, 1, 1, 1, 100, 100, $2)`, quotaWriterA, seed.now)
	ctx := context.Background()
	fence := Fence{}
	inTx := func(check func(tx pgx.Tx) error) error {
		return db.WithTx(ctx, check)
	}

	account, err := New(db, fence).AccountQuota(ctx, quotaWriterA, seed.now)
	if err != nil {
		t.Fatal(err)
	}
	if want := (quota.Sources{Books: 1, Chapters: 1, Characters: 100}); account.Projects != 1 || account.Sources != want ||
		len(account.OwnedProjects) != 1 || account.OwnedProjects[0].ProjectID != quotaProjectA {
		t.Fatalf("the account = %d sagas, %+v, %+v; want his own saga alone", account.Projects, account.Sources, account.OwnedProjects)
	}

	// His own saga is within the limits with the demo saga next to it, and a second saga of his own is over.
	if err := inTx(func(tx pgx.Tx) error { return fence.CheckOwnerProjectsInTx(ctx, tx, quotaWriterA, 0) }); err != nil {
		t.Fatalf("the sagas of the owner next to the demo saga: %v", err)
	}
	if exceeded, ok := quota.AsExceeded(inTx(func(tx pgx.Tx) error { return fence.CheckOwnerProjectsInTx(ctx, tx, quotaWriterA, 1) })); !ok || exceeded.Key != quota.KeyProjects {
		t.Fatalf("a second saga of his own = %+v, %v; want the limit of sagas", exceeded, ok)
	}
	if err := inTx(func(tx pgx.Tx) error {
		return fence.CheckOwnerSourcesInTx(ctx, tx, quotaProjectA, quota.MeasureChapters|quota.MeasureCharacters)
	}); err != nil {
		t.Fatalf("the text of his own saga next to the demo saga: %v", err)
	}

	// Nothing is added to the demo saga, and the refusal is its own, not a limit.
	for name, check := range map[string]func(tx pgx.Tx) error{
		"a book with text": func(tx pgx.Tx) error { return fence.CheckNewBooksInTx(ctx, tx, quotaDemoProject, 1, true) },
		"an empty book":    func(tx pgx.Tx) error { return fence.CheckNewBooksInTx(ctx, tx, quotaDemoProject, 1, false) },
		"a chapter": func(tx pgx.Tx) error {
			return fence.CheckOwnerSourcesInTx(ctx, tx, quotaDemoProject, quota.MeasureChapters|quota.MeasureCharacters)
		},
		"an edit": func(tx pgx.Tx) error {
			return fence.CheckOwnerSourcesInTx(ctx, tx, quotaDemoProject, quota.MeasureCharacters)
		},
	} {
		if err := inTx(check); !errors.Is(err, quota.ErrDemoSaga) {
			t.Fatalf("%s into the demo saga = %v; want quota.ErrDemoSaga", name, err)
		}
	}

	// What comes back from the trash of the demo saga, and the demo saga itself, pass over every limit of the owner.
	if err := inTx(func(tx pgx.Tx) error {
		return fence.CheckAdmittedSourcesInTx(ctx, tx, quotaDemoProject, quota.MeasureBooks|quota.MeasureChapters|quota.MeasureCharacters)
	}); err != nil {
		t.Fatalf("a restored book of the demo saga: %v", err)
	}
	if err := inTx(func(tx pgx.Tx) error { return fence.CheckRestoredProjectInTx(ctx, tx, quotaDemoProject) }); err != nil {
		t.Fatalf("the restored demo saga: %v", err)
	}
	// The seed goes only into a demo saga that holds no live book: not into the one with its book, not into his own.
	if err := inTx(func(tx pgx.Tx) error { return fence.CheckDemoSeedInTx(ctx, tx, quotaDemoProject) }); !errors.Is(err, quota.ErrDemoSeed) {
		t.Fatalf("a seed into the demo saga with its book = %v; want quota.ErrDemoSeed", err)
	}
	if err := inTx(func(tx pgx.Tx) error { return fence.CheckDemoSeedInTx(ctx, tx, quotaProjectA) }); !errors.Is(err, quota.ErrDemoSeed) {
		t.Fatalf("a seed into his own saga = %v; want quota.ErrDemoSeed", err)
	}
	seed.user(quotaWriterB, "empty-demo-saga@example.test")
	seed.demoProject(quotaProjectB, quotaWriterB)
	if err := inTx(func(tx pgx.Tx) error { return fence.CheckDemoSeedInTx(ctx, tx, quotaProjectB) }); err != nil {
		t.Fatalf("a seed into the demo saga without a live book: %v", err)
	}
	// A restored book of his own saga is still checked.
	seed.book(quotaProjectA, false, 100)
	if exceeded, ok := quota.AsExceeded(inTx(func(tx pgx.Tx) error {
		return fence.CheckAdmittedSourcesInTx(ctx, tx, quotaProjectA, quota.MeasureBooks|quota.MeasureChapters|quota.MeasureCharacters)
	})); !ok || exceeded.Key != quota.KeyProjectBooks {
		t.Fatalf("a restored book of his own saga = %+v, %v; want the limit of books", exceeded, ok)
	}
}
