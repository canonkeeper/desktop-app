package sagamemory

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagalayer"
	researchservice "canonkeeper/backend/internal/services/researchchat"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
)

// TestSagaSearchLabCosts replays the recorded calls of every chat tool through the product tools
// over the search rows: the latency and the bytes allocated per call, the live backend heap after
// a series of calls on the sagas (nothing may stay), and the size of the search rows.
func TestSagaSearchLabCosts(t *testing.T) {
	dir := labDir(t)
	data := loadLabData(t, dir)
	db, projects := labDatabase(t, data)
	labSizes(t, db, projects)
	data.paragraphs, data.cards = nil, nil
	repository := New(db)
	saga := service.NewSaga(repository, repository)
	labTools(t, data, saga, projects)
}

func labSizes(t *testing.T, db *storedb.DB, projects []string) {
	ctx := context.Background()
	for _, table := range []string{"chapter_search_texts", "chapter_search_words", "chapter_paragraphs", "chapter_cards"} {
		var heap, total int64
		if err := db.Pool.QueryRow(ctx, `SELECT pg_table_size($1::regclass), pg_total_relation_size($1::regclass)`, table).Scan(&heap, &total); err != nil {
			t.Fatal(err)
		}
		t.Logf("size %-22s table+toast %7.1f MB, with indexes %7.1f MB", table, float64(heap)/1e6, float64(total)/1e6)
	}
	for number, project := range projects {
		var words, texts, paragraphs int64
		if err := db.Pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM chapter_search_words WHERE project_id = $1),
			       (SELECT count(*) FROM chapter_search_texts WHERE project_id = $1),
			       (SELECT coalesce(sum(cardinality(paragraph_tokens)), 0) FROM chapter_search_texts WHERE project_id = $1)`,
			project).Scan(&words, &texts, &paragraphs); err != nil {
			t.Fatal(err)
		}
		t.Logf("saga %d: %d chapter texts, %d paragraphs, %d word rows", number+1, texts, paragraphs, words)
	}
}

type labAuthority struct{ authority researchdomain.ReadAuthority }

func (lab *labAuthority) ReadAuthority(context.Context, researchservice.ToolContext) (researchdomain.ReadAuthority, error) {
	return lab.authority, nil
}

func (*labAuthority) RetainChapters(context.Context, researchservice.ToolContext, researchdomain.ReadAuthority, []researchservice.ChapterRevision) error {
	return nil
}

// labScanModel answers every scan chunk at once with nothing found: the lab measures the reads.
type labScanModel struct{}

func (labScanModel) Complete(context.Context, string, string) (string, quota.ModelUsage, error) {
	return `{"found":[]}`, quota.ModelUsage{}, nil
}

var labCall = researchservice.ToolContext{Caller: agent.ToolCaller{ThreadID: "lab", RootThreadID: "lab"}, TurnID: "lab"}

// labRun replays one recorded tool call through the product tools; ok is false for a call the
// tool rejects as invalid arguments.
func labRun(ctx context.Context, tools *researchservice.ReadTools, call labToolCall) (bool, error) {
	raw, err := json.Marshal(call.Args)
	if err != nil {
		return false, err
	}
	decode := func(target any) bool { return json.Unmarshal(raw, target) == nil }
	switch call.Tool {
	case "search":
		var input struct {
			Queries []string `json:"queries"`
			Match   string   `json:"match"`
			Order   string   `json:"order"`
			From    string   `json:"from"`
			To      string   `json:"to"`
			Books   []int    `json:"books"`
			Limit   int      `json:"limit"`
			Offset  int      `json:"offset"`
		}
		if !decode(&input) {
			return false, nil
		}
		_, err = tools.Search(ctx, labCall, researchservice.SearchInput(input))
	case "read":
		var input struct {
			Chapter string `json:"chapter"`
			From    int    `json:"from"`
			To      int    `json:"to"`
			Around  int    `json:"around"`
			Radius  *int   `json:"radius"`
		}
		if !decode(&input) {
			return false, nil
		}
		_, err = tools.Read(ctx, labCall, researchservice.ReadInput(input))
	case "chapters":
		var input struct {
			From   string `json:"from"`
			To     string `json:"to"`
			Books  []int  `json:"books"`
			Title  string `json:"title"`
			Detail string `json:"detail"`
		}
		if !decode(&input) {
			return false, nil
		}
		_, err = tools.Chapters(ctx, labCall, researchservice.ChaptersInput{From: input.From, To: input.To, Books: input.Books, Title: input.Title, Detail: researchservice.ChaptersDetail(input.Detail)})
	case "glossary":
		var input struct {
			Query string   `json:"query"`
			Types []string `json:"types"`
			Limit int      `json:"limit"`
		}
		if !decode(&input) {
			return false, nil
		}
		_, err = tools.Glossary(ctx, labCall, researchservice.GlossaryInput(input))
	case "changes":
		var input struct {
			Subject string `json:"subject"`
			From    string `json:"from"`
			To      string `json:"to"`
		}
		if !decode(&input) {
			return false, nil
		}
		_, err = tools.Changes(ctx, labCall, researchservice.ChangesInput(input))
	case "scan":
		var input struct {
			Task  string `json:"task"`
			From  string `json:"from"`
			To    string `json:"to"`
			Books []int  `json:"books"`
		}
		if !decode(&input) {
			return false, nil
		}
		_, err = tools.Scan(ctx, labCall, researchservice.ScanInput(input))
	default:
		return false, nil
	}
	if _, invalid := researchservice.IsArgumentError(err); invalid {
		return false, nil
	}
	return err == nil, err
}

func labTools(t *testing.T, data labData, saga *service.Saga, projects []string) {
	ctx := context.Background()
	live := func() float64 {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return float64(stats.HeapAlloc) / 1e6
	}
	allocated := func() uint64 {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.TotalAlloc
	}
	baseline := live()
	millis, megabytes := map[string][]float64{}, map[string][]float64{}
	var heaps []string
	for number, project := range projects {
		tools := researchservice.NewReadTools(&labAuthority{authority: researchdomain.ReadAuthority{
			ProjectID: project, SourceView: "draft", Scope: researchdomain.ScopeProject,
		}}, saga, labScanModel{}, nil, researchservice.ScanSettings{Parallel: 8, Budget: 50 * time.Second}, labNoLayer{})
		for _, call := range data.tools {
			if call.Project != project {
				continue
			}
			before := allocated()
			started := time.Now()
			ok, err := labRun(ctx, tools, call)
			if err != nil {
				t.Fatalf("%s: %v", call.Tool, err)
			}
			if !ok {
				continue
			}
			key := call.Tool
			if number == 0 {
				key = "1/" + key
			}
			millis[key] = append(millis[key], since(started))
			megabytes[key] = append(megabytes[key], float64(allocated()-before)/1e6)
		}
		heaps = append(heaps, fmt.Sprintf("%.1f", live()-baseline))
	}
	for _, tool := range []string{"search", "read", "chapters", "glossary", "changes", "scan"} {
		for _, key := range []string{"1/" + tool, tool} {
			if len(millis[key]) == 0 {
				continue
			}
			scope := "largest saga"
			if !strings.HasPrefix(key, "1/") {
				scope = "other sagas"
			}
			t.Logf("tool %-8s %-12s calls %4d: ms p50 %7.2f p95 %7.2f max %7.2f; allocated MB p50 %5.1f p95 %5.1f max %5.1f", tool, scope, len(millis[key]),
				percentile(millis[key], 50), percentile(millis[key], 95), percentile(millis[key], 100),
				percentile(megabytes[key], 50), percentile(megabytes[key], 95), percentile(megabytes[key], 100))
		}
	}
	t.Logf("live heap above the start after the calls of each saga, MB: [%s]", strings.Join(heaps, " "))
}

// labNoLayer is the thread layer of the lab: its calls do not ask about it.
type labNoLayer struct{}

func (labNoLayer) SagaThreads(context.Context, string, bool) (*sagalayer.Layer, error) {
	return nil, nil
}
