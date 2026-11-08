package quotastore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/quota"

	"github.com/jackc/pgx/v5"
)

// ErrProjectNotFound means the project is not live; the caller maps it to its own not-found.
var ErrProjectNotFound = errors.New("quota: project not found")

// ReserveImportsInTx is the upload step of UC3: a single import or «Обновить из файла» counts 1,
// a queue start counts its volumes, all for the actor (R3.1, R1.4). It runs only for a new
// idempotency key; the day row lock serialises uploads of one writer.
func ReserveImportsInTx(ctx context.Context, tx pgx.Tx, actorUserID string, count int64, at time.Time) error {
	if count < 1 {
		return nil
	}
	limits, err := EffectiveLimitsInTx(ctx, tx, actorUserID)
	if err != nil {
		return err
	}
	var imports int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO writer_usage_days AS usage (user_id, day, imports)
		VALUES ($1::uuid, $2::date, $3)
		ON CONFLICT (user_id, day) DO UPDATE SET imports = usage.imports + EXCLUDED.imports
		RETURNING usage.imports
	`, actorUserID, moscowDate(at), count).Scan(&imports); err != nil {
		return err
	}
	if imports > limits.ImportsPerDay {
		return quota.DailyExceeded(quota.KeyImportsPerDay, limits.ImportsPerDay, at)
	}
	return nil
}

// CheckNewBooksInTx is the preflight of UC3 (R3.2): adding books would not take the project or
// all projects of its owner over the book limit, and, when text comes with them, the owner's text
// is not already at its limit. The exact text is checked after the write (CheckOwnerSourcesInTx).
func CheckNewBooksInTx(ctx context.Context, tx pgx.Tx, projectID string, books int64, withText bool) error {
	owner, err := lockProjectOwnerQuotaInTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	limits, project, account, err := ownerTotals(ctx, tx, owner, projectID)
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
// account ones; books, then chapters, then characters. A refusal rolls the write back.
func CheckOwnerSourcesInTx(ctx context.Context, tx pgx.Tx, projectID string) error {
	owner, err := lockProjectOwnerQuotaInTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	limits, project, account, err := ownerTotals(ctx, tx, owner, projectID)
	if err != nil {
		return err
	}
	return sourcesExceeded(limits, project, account)
}

func sourcesExceeded(limits quota.Limits, project, account quota.Sources) error {
	checks := []struct {
		used int64
		key  quota.Key
	}{
		{project.Books, quota.KeyProjectBooks},
		{project.Chapters, quota.KeyProjectChapters},
		{project.Characters, quota.KeyProjectCharacters},
		{account.Books, quota.KeyAccountBooks},
		{account.Chapters, quota.KeyAccountChapters},
		{account.Characters, quota.KeyAccountCharacters},
	}
	for _, check := range checks {
		if limit := limits.Value(check.key); check.used > limit {
			return &quota.Exceeded{Key: check.key, Limit: limit}
		}
	}
	return nil
}

// CheckOwnerProjectsInTx is UC4: a new project (adding = 1) or a restored one (adding = 0, the
// project is already live in this transaction) keeps the owner's live projects within the limit.
func CheckOwnerProjectsInTx(ctx context.Context, tx pgx.Tx, ownerUserID string, adding int64) error {
	if err := lockOwnerQuotaInTx(ctx, tx, ownerUserID); err != nil {
		return err
	}
	limits, err := EffectiveLimitsInTx(ctx, tx, ownerUserID)
	if err != nil {
		return err
	}
	var live int64
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM projects WHERE owner_user_id = $1::uuid AND deleted_at IS NULL
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

func lockProjectOwnerQuotaInTx(ctx context.Context, tx pgx.Tx, projectID string) (string, error) {
	var owner string
	err := tx.QueryRow(ctx, `
		SELECT owner_user_id::text FROM projects WHERE id = $1::uuid AND deleted_at IS NULL
	`, projectID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrProjectNotFound
	}
	if err != nil {
		return "", err
	}
	return owner, lockOwnerQuotaInTx(ctx, tx, owner)
}

func ownerTotals(ctx context.Context, tx pgx.Tx, owner, projectID string) (quota.Limits, quota.Sources, quota.Sources, error) {
	limits, err := EffectiveLimitsInTx(ctx, tx, owner)
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
// owner keeps within the project limit and the text limits.
func CheckRestoredProjectInTx(ctx context.Context, tx pgx.Tx, projectID string) error {
	owner, err := lockProjectOwnerQuotaInTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if err := CheckOwnerProjectsInTx(ctx, tx, owner, 0); err != nil {
		return err
	}
	limits, project, account, err := ownerTotals(ctx, tx, owner, projectID)
	if err != nil {
		return err
	}
	return sourcesExceeded(limits, project, account)
}
