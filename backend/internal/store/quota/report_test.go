package quotastore

import (
	"context"
	"regexp"
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-16-desk-admin-apps.md AA3.5: quota.UsageSources is every source the table of the spend takes — the CHECK of
// model_usage_days and no other —, the report has a column for each and drops none: the sources of the apps are a group of
// their own and in the total; by project they are the row «приложения» by their source, whatever their project, and the
// spend of the site under no project — of a deleted account — is the row «без саги».
func TestTheUsageReportHasAColumnForEverySourcePostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_report_sources")
	ctx := context.Background()
	var check string
	if err := db.Pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'model_usage_days_source_check'
	`).Scan(&check); err != nil {
		t.Fatal(err)
	}
	var taken []string
	for _, match := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(check, -1) {
		taken = append(taken, match[1])
	}
	var sources []string
	for _, source := range quota.UsageSources() {
		sources = append(sources, string(source))
	}
	slices.Sort(taken)
	slices.Sort(sources)
	if !slices.Equal(taken, sources) {
		t.Fatalf("the sources of the domain %v, the table takes %v", sources, taken)
	}

	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "report-a@example.test")
	seed.user(quotaWriterB, "report-b@example.test")
	seed.project(quotaProjectA, quotaWriterA)
	var calls int64
	for index, source := range quota.UsageSources() {
		project := quotaProjectA
		if source.OfApps() {
			project = noProject
		}
		spend := quota.UsageRecord{Source: source, ProjectID: project, Model: "gpt-north", Usage: quota.ModelUsage{Calls: int64(index + 1), InputTokens: 10}}
		if err := AddModelUsage(ctx, db.Pool, quotaWriterA, spend, seed.now); err != nil {
			t.Fatal(err)
		}
		calls += int64(index + 1)
	}
	deleted := quota.UsageRecord{Source: quota.UsageChat, ProjectID: quotaProjectA, Model: "gpt-north", Usage: quota.ModelUsage{Calls: 100}}
	seed.exec(`UPDATE users SET disabled_at = $2, deleted_at = $2 WHERE id = $1`, quotaWriterB, seed.now)
	if err := AddModelUsage(ctx, db.Pool, quotaWriterB, deleted, seed.now); err != nil {
		t.Fatal(err)
	}

	rows, _, err := UsageReport(ctx, db.Pool, seed.now.AddDate(0, 0, -1), false)
	if err != nil {
		t.Fatal(err)
	}
	var writer UsageRow
	for _, row := range rows {
		if row.Writer.ID == quotaWriterA {
			writer = row
		}
	}
	var apps int64
	for index, source := range quota.UsageSources() {
		if source.OfApps() {
			apps += int64(index + 1)
		}
	}
	shown := writer.Chat.Calls + writer.Scan.Calls + writer.Cards.Calls + writer.Images.Calls + writer.Layer.Calls + writer.Draft.Calls +
		writer.Drawing.Calls + writer.DraftHelper.Calls + writer.Voice.Calls + writer.Apps.Calls
	if shown != calls || writer.Apps.Calls != apps || writer.Apps.InputTokens != 60 {
		t.Fatalf("the report shows %d calls of %d, the apps %+v (want %d calls)", shown, calls, writer.Apps, apps)
	}

	rows, _, err = UsageReport(ctx, db.Pool, seed.now.AddDate(0, 0, -1), true)
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, row := range rows {
		switch {
		case row.Writer.ID == quotaWriterA && row.Part == UsageOfApps:
			if row.Apps.Calls != apps || row.Chat.Calls != 0 || row.ProjectID != "" {
				t.Fatalf("the row of the apps = %+v", row)
			}
		case row.Writer.ID == quotaWriterA && row.ProjectID == quotaProjectA:
			if row.Apps.Calls != 0 || row.Chat.Calls != 1 {
				t.Fatalf("the row of the saga = %+v", row)
			}
		case row.Part == UsageWithoutSaga:
			if row.Writer.Email != DeletedWriter || row.Chat.Calls != 100 || row.Apps.Calls != 0 {
				t.Fatalf("the row without a saga = %+v", row)
			}
		}
		parts = append(parts, string(row.Part))
	}
	if !slices.Contains(parts, string(UsageOfApps)) || !slices.Contains(parts, string(UsageWithoutSaga)) {
		t.Fatalf("the parts of the report by project = %v", parts)
	}
}

// AA3.5: every source of the spend has a column of the report.
func TestEveryUsageSourceHasAColumnOfTheReport(t *testing.T) {
	for _, source := range quota.UsageSources() {
		if usageColumn(&UsageRow{}, source) == nil {
			t.Errorf("the source %s has no column of the report", source)
		}
	}
	if usageColumn(&UsageRow{}, quota.UsageSource("unknown")) != nil {
		t.Error("an unknown source has a column")
	}
}
