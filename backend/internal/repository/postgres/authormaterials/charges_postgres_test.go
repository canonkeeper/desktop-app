package authormaterials

import (
	"context"
	"encoding/json"
	"testing"

	"canonkeeper/backend/internal/domain/quota"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// countedUploads is a counter of a site as the fence of a test sees it: the uploads it opened and whether their Job, import
// and unit of the day were written in the transaction when it was asked.
type countedUploads struct {
	opened []quota.Work
	seen   []bool
	refuse error
}

func (counter *countedUploads) OpenInTx(ctx context.Context, tx pgx.Tx, work quota.Work) error {
	var rows int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM jobs WHERE id = $1::uuid) + (SELECT count(*) FROM author_material_imports WHERE job_id = $1::uuid)
	`, work.JobID).Scan(&rows); err != nil {
		return err
	}
	counter.opened = append(counter.opened, work)
	counter.seen = append(counter.seen, rows == 2)
	return counter.refuse
}
func (*countedUploads) SteerInTx(context.Context, pgx.Tx, quota.Steer) error   { return nil }
func (*countedUploads) UnsteerInTx(context.Context, pgx.Tx, quota.Steer) error { return nil }

// specs/2026-11-13-desk-account-ai.md AI3.8: the Job of an upload of materials is named before its unit of the day, and the
// counter of the site opens it as the last step of the transaction; a refusal of the site leaves no import and no Job.
func TestTheCounterOfTheSiteOpensAnUploadLastPostgres(t *testing.T) {
	fixture := seedProject(t, "ck_author_materials_charges")
	counter := &countedUploads{}
	fixture.store = fixture.store.WithQuota(quotastore.Fence{Charges: counter})
	started := fixture.start(t, "key-1", fileHolds)
	claim := fixture.claimed(t, started)
	if len(counter.opened) != 1 || counter.opened[0].Kind != quota.WorkMaterials || counter.opened[0].JobID != claim.JobID || !counter.seen[0] {
		t.Fatalf("the counter was asked %+v, seeing the upload %v", counter.opened, counter.seen)
	}
	fixture.finished(t, started, nil)
	counter.refuse = quota.DailyExceeded(quota.KeyImportsPerDay, 0, started.CreatedAt)
	body, _ := json.Marshal(fileHolds)
	if _, err := fixture.store.StartAuthorMaterialsImport(context.Background(), startCommand(fixture.editor, "key-2", string(body)+" ")); err == nil {
		t.Fatal("an upload the site refused started")
	}
	var jobs int
	if err := fixture.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'author_materials_import'`, fixture.editor.ProjectID).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("Jobs after a refused upload = %d, %v", jobs, err)
	}
}
