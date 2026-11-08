package sagamemory

import (
	"context"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSagaSearchLabMatchesTheMemoryIndex replays every recorded saga.search call of the lab
// sagas through the product reader of the search rows (service.Saga) and through the frozen
// in-memory index, and requires the same hits in the same order with the same Matched and
// bit-equal Score, the same chapter distribution and the same printed snippets (R2.1, R6.1).
//
// The sagas come from the export into a fixture database, or — with
// CANONKEEPER_SAGA_SEARCH_LAB_DSN — from that database itself, read in read-only transactions
// (TEST after its search rows are loaded; UC4).
func TestSagaSearchLabMatchesTheMemoryIndex(t *testing.T) {
	dir := labDir(t)
	data := loadLabData(t, dir)
	db, projects := labDatabase(t, data)
	ctx := context.Background()
	repository := New(db)
	saga := service.NewSaga(repository, repository)
	type tally struct {
		calls, skipped, queries, withHits, hits, shown int
		resultMismatch, snippetMismatch                int
		millis                                         []float64
		byMode                                         map[string][]float64
	}
	byProject := map[string]*tally{}
	for _, project := range projects {
		byProject[project] = &tally{byMode: map[string][]float64{}}
	}
	calls := append([]labSearchCall(nil), data.search...)
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].Project < calls[j].Project })
	oracleProject := ""
	var oracle *oracleIndex
	for callIndex, call := range calls {
		count := byProject[call.Project]
		if count == nil {
			continue
		}
		if call.Project != oracleProject {
			oracle, oracleProject = labOracle(t, db, call.Project), call.Project
		}
		count.calls++
		queries, ok := labQueries(oracle.Version, call)
		if !ok || call.Limit < 0 || call.Limit > 150 || call.Offset < 0 {
			count.skipped++
			continue
		}
		shown := labShown(call)
		started := time.Now()
		outline, err := saga.Outline(ctx, call.Project, "draft")
		if err != nil {
			t.Fatal(err)
		}
		index, results, err := saga.Search(ctx, outline, queries, shown)
		if err != nil {
			t.Fatal(err)
		}
		snippets := make([]map[int]string, len(queries))
		for i, query := range queries {
			snippets[i] = map[int]string{}
			for _, hit := range shown(query, results[i]) {
				snippets[i][hit.Para] = index.Snippet(hit, 320)
			}
		}
		took := since(started)
		count.millis = append(count.millis, took)
		count.byMode[string(queries[0].Mode)] = append(count.byMode[string(queries[0].Mode)], took)
		for i, query := range queries {
			count.queries++
			want := oracle.Run(oracleQuery{Text: query.Text, Mode: query.Mode, From: query.From, To: query.To, BookIDs: query.BookIDs, Story: query.Story})
			if len(want.Hits) > 0 {
				count.withHits++
			}
			count.hits += len(want.Hits)
			if !sameAsOracle(want, results[i]) {
				count.resultMismatch++
				t.Errorf("call %d query %d (%s): %d hits in memory, %d stored", callIndex, i, query.Mode, len(want.Hits), len(results[i].Hits))
				continue
			}
			wantSnippets := map[int]string{}
			for _, hit := range shown(query, results[i]) {
				wantSnippets[hit.Para] = oracle.Snippet(oracleHitOf(want, hit.Para), 320)
			}
			count.shown += len(wantSnippets)
			if !reflect.DeepEqual(wantSnippets, snippets[i]) {
				count.snippetMismatch++
				t.Errorf("call %d query %d: snippets differ", callIndex, i)
			}
		}
	}
	for number, project := range projects {
		count := byProject[project]
		t.Logf("saga %d: calls %d (skipped %d), queries %d (with hits %d), hits %d, shown snippets %d; result mismatches %d, snippet mismatches %d; search call ms p50 %.2f p95 %.2f max %.2f",
			number+1, count.calls, count.skipped, count.queries, count.withHits, count.hits, count.shown, count.resultMismatch, count.snippetMismatch,
			percentile(count.millis, 50), percentile(count.millis, 95), percentile(count.millis, 100))
		for _, mode := range []string{"any", "all", "phrase", "substring"} {
			values := count.byMode[mode]
			if len(values) > 0 {
				t.Logf("  %-9s calls %4d: p50 %.2f p95 %.2f max %.2f ms", mode, len(values), percentile(values, 50), percentile(values, 95), percentile(values, 100))
			}
		}
	}
}

// labDatabase is the database of the lab: the read-only database named by
// CANONKEEPER_SAGA_SEARCH_LAB_DSN, or a fixture with the exported sagas and their search rows.
// It returns the projects to compare, the largest first.
func labDatabase(t *testing.T, data labData) (*storedb.DB, []string) {
	t.Helper()
	ctx := context.Background()
	if dsn := os.Getenv("CANONKEEPER_SAGA_SEARCH_LAB_DSN"); dsn != "" {
		config, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatal("lab DSN does not parse")
		}
		config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal("lab database is unavailable")
		}
		t.Cleanup(pool.Close)
		db := &storedb.DB{Pool: pool}
		seen := map[string]bool{}
		var projects []string
		for _, call := range data.search {
			if seen[call.Project] {
				continue
			}
			seen[call.Project] = true
			var live bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND deleted_at IS NULL)`, call.Project).Scan(&live); err != nil {
				t.Fatal(err)
			}
			if live {
				projects = append(projects, call.Project)
			}
		}
		sort.Strings(projects)
		return db, projects
	}
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_search_lab")
	projects := data.labProjects()
	for owner, project := range projects {
		seedLabProject(t, db, data, project, owner+1)
		took, words := buildLabSearchRows(t, db, project)
		t.Logf("saga %d: %d word rows built in %v", owner+1, words, took.Round(time.Millisecond))
	}
	if _, err := db.Pool.Exec(ctx, "VACUUM ANALYZE"); err != nil {
		t.Fatal(err)
	}
	return db, projects
}

// labOracle builds the frozen in-memory index of the current draft version of a project from its
// paragraphs and russian_stem lexemes, as the backend did before the search rows.
func labOracle(t *testing.T, db *storedb.DB, project string) *oracleIndex {
	t.Helper()
	ctx := context.Background()
	repository := New(db)
	selection, err := repository.Selection(ctx, project, "draft")
	if err != nil {
		t.Fatal(err)
	}
	chapters, err := repository.Chapters(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	version := domain.NewVersion(project, "draft", selection.Fingerprint, chapters)
	seen := map[string]bool{}
	var words []string
	for _, chapter := range version.Chapters {
		for _, paragraph := range chapter.Paragraphs {
			for _, token := range domain.Tokenize(paragraph.Norm) {
				if !seen[token.Word] {
					seen[token.Word] = true
					words = append(words, token.Word)
				}
			}
		}
	}
	stems := map[string]string{}
	rows, err := db.Pool.Query(ctx, `SELECT word, coalesce(array_to_string(ts_lexize('russian_stem', word), ' '), word) FROM unnest($1::text[]) AS word`, words)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var word, stem string
		if err := rows.Scan(&word, &stem); err != nil {
			t.Fatal(err)
		}
		stems[word] = stem
	}
	rows.Close()
	return buildOracleIndex(version, stems)
}

func sameAsOracle(want oracleResult, got domain.Result) bool {
	if !reflect.DeepEqual(want.Terms, got.Terms) || len(want.Hits) != len(got.Hits) || len(want.Chapters) != len(got.Chapters) {
		return false
	}
	for i, item := range want.Chapters {
		if item.Chapter != got.Chapters[i].Chapter || item.Count != got.Chapters[i].Count {
			return false
		}
	}
	for i, hit := range want.Hits {
		other := got.Hits[i]
		if hit.Para != other.Para || hit.Matched != other.Matched || math.Float64bits(hit.Score) != math.Float64bits(other.Score) {
			return false
		}
	}
	return true
}

func oracleHitOf(result oracleResult, para int) oracleHit {
	for _, hit := range result.Hits {
		if hit.Para == para {
			return hit
		}
	}
	return oracleHit{Para: para}
}

// labQueries turns a recorded search call into the queries the chat tool runs; ok is false when
// the tool would reject the call (a chapter key or volume that is not in the saga).
func labQueries(version *domain.Version, call labSearchCall) ([]domain.Query, bool) {
	mode := domain.Mode(call.Match)
	switch mode {
	case "":
		mode = domain.ModeAny
	case domain.ModeAny, domain.ModeAll, domain.ModePhrase, domain.ModeSubstring:
	default:
		return nil, false
	}
	from, to := 0, -1
	if strings.TrimSpace(call.From) != "" {
		chapter, ok := version.Find(call.From)
		if !ok {
			return nil, false
		}
		from = chapter.Index
	}
	if strings.TrimSpace(call.To) != "" {
		chapter, ok := version.Find(call.To)
		if !ok || from > chapter.Index {
			return nil, false
		}
		to = chapter.Index
	}
	var books []string
	for _, number := range call.Books {
		found := ""
		for _, chapter := range version.Chapters {
			if chapter.BookOrder == number {
				found = chapter.BookID
				break
			}
		}
		if found == "" {
			return nil, false
		}
		books = append(books, found)
	}
	var queries []domain.Query
	for _, text := range call.Queries {
		if strings.TrimSpace(text) == "" || len([]rune(text)) > 300 {
			return nil, false
		}
		queries = append(queries, domain.Query{Text: text, Mode: mode, From: from, To: to, BookIDs: books, Story: call.Order == "story"})
	}
	return queries, len(queries) > 0 && len(queries) <= 8
}

// labShown are the hits whose snippets a search call prints: the requested page and, in
// relevance order, the earliest and the latest hit (researchchat shownHits).
func labShown(call labSearchCall) func(domain.Query, domain.Result) []domain.Hit {
	limit := call.Limit
	if limit == 0 {
		limit = 30
	}
	return func(query domain.Query, result domain.Result) []domain.Hit {
		hits := result.Hits
		if len(hits) == 0 || call.Offset >= len(hits) {
			return nil
		}
		shown := append([]domain.Hit(nil), hits[call.Offset:min(len(hits), call.Offset+limit)]...)
		if !query.Story && len(hits) > 1 {
			earliest, latest := hits[0], hits[0]
			for _, hit := range hits[1:] {
				if hit.Para < earliest.Para {
					earliest = hit
				}
				if hit.Para > latest.Para {
					latest = hit
				}
			}
			shown = append(shown, earliest, latest)
		}
		return shown
	}
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	at := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return sorted[max(0, min(at, len(sorted)-1))]
}

func since(started time.Time) float64 { return float64(time.Since(started).Microseconds()) / 1000 }
