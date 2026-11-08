package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueryBoundaryCheckerAcceptsClosedFixture(t *testing.T) {
	sql := readGovernanceQueryFixture(t, "good.sql")
	if findings := CheckQueryBoundarySQL("good.sql", sql); len(findings) != 0 {
		t.Fatalf("good fixture findings = %+v", findings)
	}
}

func TestQueryBoundaryCheckerRejectsUnsafeFixture(t *testing.T) {
	sql := readGovernanceQueryFixture(t, "bad.sql")
	findings := CheckQueryBoundarySQL("bad.sql", sql)
	wantCodes := map[QueryBoundaryCode]bool{
		QueryBoundaryMissingProject:      false,
		QueryBoundaryUnregisteredActor:   false,
		QueryBoundaryActorReturnsContent: false,
		QueryBoundaryInlineWorkerClaim:   false,
	}
	for _, finding := range findings {
		if _, ok := wantCodes[finding.Code]; ok {
			wantCodes[finding.Code] = true
		}
	}
	for code, found := range wantCodes {
		if !found {
			t.Fatalf("bad fixture did not produce %s; findings=%+v", code, findings)
		}
	}
}

func TestQueryBoundaryCheckerRejectsActorProjectionAndPaginationDrift(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{
			name: "resolver wildcard projection",
			sql: `-- name: ResolveBookAccessForActor :one
SELECT *
FROM books b
JOIN project_memberships m ON m.project_id = b.project_id
WHERE b.id = sqlc.arg(book_id)
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL;`,
		},
		{
			name: "collection expanded projection",
			sql: `-- name: ListProjectsForActor :many
SELECT p.id, p.name, p.description, p.revision
FROM projects p
JOIN project_memberships m ON m.project_id = p.id
WHERE m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg(limit_count);`,
		},
		{
			name: "collection without bounded pagination",
			sql: `-- name: ListProjectsForActor :many
SELECT p.id, p.name, p.description
FROM projects p
JOIN project_memberships m ON m.project_id = p.id
WHERE m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL;`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := CheckQueryBoundarySQL("drift.sql", test.sql)
			if !hasQueryBoundaryCode(findings, QueryBoundaryActorContractDrift) {
				t.Fatalf("actor query drift was accepted: findings=%+v", findings)
			}
		})
	}
}

func TestQueryBoundaryCheckerRejectsActorCardinalityDrift(t *testing.T) {
	file := filepath.Join("..", "..", "db", "queries", "projects.sql")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(
		string(raw),
		"-- name: ListProjectsForActor :many",
		"-- name: ListProjectsForActor :one",
		1,
	)
	if drifted == string(raw) {
		t.Fatal("live ListProjectsForActor marker was not found")
	}
	findings := CheckQueryBoundarySQL(file, drifted)
	if !hasQueryBoundaryCode(findings, QueryBoundaryActorContractDrift) {
		t.Fatalf("actor query cardinality drift was accepted: findings=%+v", findings)
	}
}

func TestQueryBoundaryCheckerAcceptsEveryLiveRegisteredActorQuery(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "db", "queries", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	registry := make(map[string]bool, len(ActorQueryRegistry()))
	for _, registration := range ActorQueryRegistry() {
		registry[string(registration.Name)] = true
	}
	seen := make(map[ActorQuery]string, len(actorQuerySQLContractDigests))
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, query := range splitNamedQueries(string(raw)) {
			if !registry[query.name] {
				continue
			}
			name := ActorQuery(query.name)
			if previous := seen[name]; previous != "" {
				t.Fatalf("registered actor query %s appears in both %s and %s", name, previous, file)
			}
			seen[name] = file
			source := "-- name: " + query.name + " " + query.command + "\n" + query.body
			if findings := CheckQueryBoundarySQL(file, source); len(findings) != 0 {
				t.Fatalf("live registered actor query %s findings = %+v", name, findings)
			}
		}
	}
	for name := range actorQuerySQLContractDigests {
		if seen[name] == "" {
			t.Fatalf("frozen actor query %s is not present in live query sources", name)
		}
	}
}

func hasQueryBoundaryCode(findings []QueryBoundaryFinding, want QueryBoundaryCode) bool {
	for _, finding := range findings {
		if finding.Code == want {
			return true
		}
	}
	return false
}

func readGovernanceQueryFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "query_boundaries", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
