package quotastore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/domain/quota"

	"github.com/jackc/pgx/v5"
)

// ErrProjectNotFound means the project is not live; the caller maps it to its own not-found.
var ErrProjectNotFound = errors.New("quota: project not found")

// CheckNewBooksInTx is the preflight of UC3 (R3.2): adding books would not take the project or
// all projects of its owner over the book limit, and, when text comes with them, the owner's text
// is not already at its limit. The exact text is checked after the write (CheckOwnerSourcesInTx).
//
// The demo saga takes no book (specs/2026-11-09-demo-saga.md DS3.3): it is refused before any limit.
func (fence Fence) CheckNewBooksInTx(ctx context.Context, tx pgx.Tx, projectID string, books int64, withText bool) error {
	owner, demo, err := lockProjectOwnerQuotaInTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if demo {
		return quota.ErrDemoSaga
	}
	limits, project, account, err := fence.ownerTotals(ctx, tx, owner, projectID)
	if err != nil {
		return err
	}
	switch {
	case project.Books+books > limits.ProjectBooks:
		return &quota.Exceeded{Key: quota.KeyProjectBooks, Limit: limits.ProjectBooks}
	case account.Books+books > limits.AccountBooks:
		return &quota.Exceeded{Key: quota.KeyAccountBooks, Limit: limits.AccountBooks}
	case withText && project.Characters >= limits.ProjectCharacters:
		return &quota.Exceeded{Key: quota.KeyProjectCharacters, Limit: limits.ProjectCharacters}
	case withText && account.Characters >= limits.AccountCharacters:
		return &quota.Exceeded{Key: quota.KeyAccountCharacters, Limit: limits.AccountCharacters}
	}
	return nil
}

// CheckOwnerSourcesInTx checks the project and every live project of its owner after text was
// written or restored in the caller's transaction (R3.3, R3.4): project limits first, then the
// account ones; books, then chapters, then characters. Only the measures the write grows are
// checked (specs/2026-11-08-subscription-tiers.md TR2.5): an edit of a chapter grows characters,
// a new or restored chapter chapters and characters, a new or restored book every measure. A
// refusal rolls the write back.
//
// It is the check of a write of text, and the demo saga takes none but its seed (specs/2026-11-09-demo-saga.md
// DS3.3): there the write is refused with quota.ErrDemoSaga. Text whose way in was admitted before is checked by
// CheckAdmittedSourcesInTx.
func (fence Fence) CheckOwnerSourcesInTx(ctx context.Context, tx pgx.Tx, projectID string, grows quota.Measures) error {
	return fence.checkSourcesInTx(ctx, tx, projectID, grows, quota.ErrDemoSaga)
}

// CheckAdmittedSourcesInTx checks the same totals for text whose way into the saga was admitted before: the book of
// an import whose staging passed (R3.3), a book or a chapter restored from the trash (R3.4). In the demo saga such
// text is its seed and what was deleted from it — not the author's and out of the totals of its owner — so there it
// checks nothing (DS3.3, DS3.4).
func (fence Fence) CheckAdmittedSourcesInTx(ctx context.Context, tx pgx.Tx, projectID string, grows quota.Measures) error {
	return fence.checkSourcesInTx(ctx, tx, projectID, grows, nil)
}

// CheckDemoSeedInTx stands in place of CheckNewBooksInTx for the seed of the demo saga
// (specs/2026-11-09-demo-saga.md DS3.4): under the quota lock of the owner, where every book of his is admitted, the
// saga is the demo saga and holds neither a live book nor an import still going. No limit is checked: the seed is the
// one text the demo saga takes, and it is out of the totals of the owner. Anything else is quota.ErrDemoSeed.
func (fence Fence) CheckDemoSeedInTx(ctx context.Context, tx pgx.Tx, projectID string) error {
	_, demo, err := lockProjectOwnerQuotaInTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if !demo {
		return quota.ErrDemoSeed
	}
	var seeded bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM books WHERE project_id = $1::uuid AND deleted_at IS NULL)
		    OR EXISTS (SELECT 1 FROM jobs WHERE project_id = $1::uuid AND kind = 'import'
		               AND status IN ('queued', 'running', 'canceling'))
	`, projectID).Scan(&seeded); err != nil {
		return err
	}
	if seeded {
		return quota.ErrDemoSeed
	}
	return nil
}

func (fence Fence) checkSourcesInTx(ctx context.Context, tx pgx.Tx, projectID string, grows quota.Measures, inDemo error) error {
	owner, demo, err := lockProjectOwnerQuotaInTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if demo {
		return inDemo
	}
	limits, project, account, err := fence.ownerTotals(ctx, tx, owner, projectID)
	if err != nil {
		return err
	}
	return sourcesExceeded(limits, project, account, grows)
}

func sourcesExceeded(limits quota.Limits, project, account quota.Sources, grows quota.Measures) error {
	checks := []struct {
		measure quota.Measures
		used    int64
		key     quota.Key
	}{
		{quota.MeasureBooks, project.Books, quota.KeyProjectBooks},
		{quota.MeasureChapters, project.Chapters, quota.KeyProjectChapters},
		{quota.MeasureCharacters, project.Characters, quota.KeyProjectCharacters},
		{quota.MeasureBooks, account.Books, quota.KeyAccountBooks},
		{quota.MeasureChapters, account.Chapters, quota.KeyAccountChapters},
		{quota.MeasureCharacters, account.Characters, quota.KeyAccountCharacters},
	}
	for _, check := range checks {
		if limit := limits.Value(check.key); grows.Has(check.measure) && check.used > limit {
			return &quota.Exceeded{Key: check.key, Limit: limit}
		}
	}
	return nil
}

// CheckOwnerProjectsInTx is UC4: a new project (adding = 1) or a restored one (adding = 0, the
// project is already live in this transaction) keeps the owner's live projects within the limit. The demo saga is
// not one of them (specs/2026-11-09-demo-saga.md DS3.2).
func (fence Fence) CheckOwnerProjectsInTx(ctx context.Context, tx pgx.Tx, ownerUserID string, adding int64) error {
	if err := lockOwnerQuotaInTx(ctx, tx, ownerUserID); err != nil {
		return err
	}
	limits, err := fence.EffectiveLimitsInTx(ctx, tx, ownerUserID)
	if err != nil {
		return err
	}
	var live int64
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM projects WHERE owner_user_id = $1::uuid AND deleted_at IS NULL AND NOT demo
	`, ownerUserID).Scan(&live); err != nil {
		return err
	}
	if live+adding > limits.Projects {
		return &quota.Exceeded{Key: quota.KeyProjects, Limit: limits.Projects}
	}
	return nil
}

// lockOwnerQuotaInTx serialises the source checks of one owner (R3.6). It is an advisory lock
// and not users … FOR UPDATE: the session fence already holds FOR SHARE on the user row, and two
// transactions of one writer upgrading it would deadlock. Callers take project rows first and
// take it before any writer_usage_days row: the outcome trigger locks a day row while an import
// finalize holds this lock (specs/2026-10-28-owner-tools-and-counters.md, lock order).
func lockOwnerQuotaInTx(ctx context.Context, tx pgx.Tx, ownerUserID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('writer_quota:' || $1::uuid::text, 0))`, ownerUserID)
	return err
}

// lockProjectOwnerQuotaInTx takes the quota lock of the owner of a live project and says whether the project is
// the demo saga, which is out of his limits (specs/2026-11-09-demo-saga.md DS3).
func lockProjectOwnerQuotaInTx(ctx context.Context, tx pgx.Tx, projectID string) (owner string, demo bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT owner_user_id::text, demo FROM projects WHERE id = $1::uuid AND deleted_at IS NULL
	`, projectID).Scan(&owner, &demo)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrProjectNotFound
	}
	if err != nil {
		return "", false, err
	}
	return owner, demo, lockOwnerQuotaInTx(ctx, tx, owner)
}

func (fence Fence) ownerTotals(ctx context.Context, tx pgx.Tx, owner, projectID string) (quota.Limits, quota.Sources, quota.Sources, error) {
	limits, err := fence.EffectiveLimitsInTx(ctx, tx, owner)
	if err != nil {
		return quota.Limits{}, quota.Sources{}, quota.Sources{}, err
	}
	projects, err := ownerSources(ctx, tx, owner)
	if err != nil {
		return quota.Limits{}, quota.Sources{}, quota.Sources{}, err
	}
	var project quota.Sources
	for _, candidate := range projects {
		if candidate.ProjectID == projectID {
			project = candidate.Sources
		}
	}
	return limits, project, quota.Sum(projects), nil
}

// CheckRestoredProjectInTx is R4.2: after a project was restored in the caller's transaction its
// owner keeps within the project limit and the text limits; a restored project grows every measure. A restored demo
// saga grows none (specs/2026-11-09-demo-saga.md DS5.2): its one place is kept by the index of the table.
func (fence Fence) CheckRestoredProjectInTx(ctx context.Context, tx pgx.Tx, projectID string) error {
	owner, demo, err := lockProjectOwnerQuotaInTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if demo {
		return nil
	}
	if err := fence.CheckOwnerProjectsInTx(ctx, tx, owner, 0); err != nil {
		return err
	}
	limits, project, account, err := fence.ownerTotals(ctx, tx, owner, projectID)
	if err != nil {
		return err
	}
	return sourcesExceeded(limits, project, account, quota.MeasureBooks|quota.MeasureChapters|quota.MeasureCharacters)
}
