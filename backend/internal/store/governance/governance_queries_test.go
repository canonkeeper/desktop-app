package governancestore

import (
	"strings"
	"testing"
)

func TestGovernanceQueriesKeepOrdinaryRowsProjectScoped(t *testing.T) {
	sql := normalizeSQL(readStoreSQLFile(t, "db", "queries", "artifact_sources.sql"))
	for _, query := range []string{
		"SealArtifactSourceOwner",
	} {
		body := namedQueryBodyForTest(t, sql, query)
		if !strings.Contains(body, "sqlc.arg(project_id)") {
			t.Fatalf("ordinary governance query %s has no project_id argument", query)
		}
	}
}

func TestGovernanceGeneratedParamsCarryProjectID(t *testing.T) {
	generated := readStoreSQLFile(t, "internal", "store", "sqlc", "artifact_sources.sql.go")
	for _, params := range []string{
		"SealArtifactSourceOwnerParams",
	} {
		start := strings.Index(generated, "type "+params+" struct {")
		if start == -1 {
			t.Fatalf("generated code is missing %s", params)
		}
		body := generated[start:]
		end := strings.Index(body, "}\n")
		if end == -1 || !strings.Contains(body[:end], "ProjectID") {
			t.Fatalf("generated %s does not carry ProjectID", params)
		}
	}
}

func TestDeletionOutboxCompletionKeepsPostgreSQLLast(t *testing.T) {
	sql := normalizeSQL(readStoreSQLFile(t, "db", "queries", "deletion_outbox.sql"))
	body := namedQueryBodyForTest(t, sql, "CompleteDeletionTargetTask")
	for _, guard := range []string{
		"task.target <> 'postgresql'",
		"sibling.state NOT IN ('succeeded', 'no_data')",
		"NOT EXISTS",
	} {
		if !strings.Contains(body, guard) {
			t.Fatalf("CompleteDeletionTargetTask is missing PostgreSQL-last guard %q", guard)
		}
	}
}

func namedQueryBodyForTest(t *testing.T, normalizedSQL, name string) string {
	t.Helper()
	marker := "-- name: " + name + " "
	start := strings.Index(normalizedSQL, marker)
	if start == -1 {
		t.Fatalf("missing named query %s", name)
	}
	body := normalizedSQL[start+len(marker):]
	if next := strings.Index(body, " -- name: "); next >= 0 {
		body = body[:next]
	}
	return body
}
