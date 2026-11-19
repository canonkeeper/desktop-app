package researchchat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modelthread"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

const (
	maxScanTaskRunes    = 500
	maxScanPlaces       = 30
	defaultScanParallel = 8
	// maxScanParallel bounds the chunks one scan reads at once, whatever the places of the process: a scan does not take
	// the whole minute of the pool of codex-lb the turns of the chat share (specs/2026-11-17-model-cache.md MC2.2).
	maxScanParallel       = 32
	defaultScanChunkRunes = 30000
	// The app-server fails a dynamic tool call after 60 seconds, so one scan call stops
	// starting chunks it cannot finish within the budget and reports where to continue.
	defaultScanBudget = 50 * time.Second
)

type ScanInput struct {
	Task  string
	From  string
	To    string
	Books []int
}

type scanState int

const (
	scanPending scanState = iota
	scanDone
	scanFailed
	// scanSkipped is a chapter longer than skippedChunks chunks: its chunk does not go to the model
	// (specs/2026-11-17-long-chapters.md LC3.1).
	scanSkipped
)

// skippedChunks is how many chunks of text a chapter the scan does not read holds: longer, it is read by search or by read
// with paragraphs (LC3.1).
const skippedChunks = 4

type scanChunk struct {
	chapters []*sagamemory.Chapter
	state    scanState
}

type scanPlace struct {
	sagamemory.ScanFinding
	chapter *sagamemory.Chapter
}

// Scan has the model read the chapters of the range in chunks and list every place that
// matches the task, grouped by found item in story order. One call scans what fits into
// its time budget and names the chapters left for the next call.
func (tools *ReadTools) Scan(ctx context.Context, call ToolContext, input ScanInput) (string, error) {
	task := strings.TrimSpace(input.Task)
	if utf8.RuneCountInString(task) < 3 || !utf8.ValidString(task) || utf8.RuneCountInString(task) > maxScanTaskRunes {
		return "", argumentError("$.task", "опишите, что искать, от 3 до %d символов", maxScanTaskRunes)
	}
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		// The chunks name their chapters themselves to the gateway; the answer to Codex gives excerpts of the chapters with
		// finds (specs/2026-11-14-desk-journal.md JR1.3).
		saga.shownAs = ""
		from, to, err := saga.chapterRange(input.From, input.To)
		if err != nil {
			return "", err
		}
		books, err := saga.books(input.Books)
		if err != nil {
			return "", err
		}
		allowed := map[string]bool{}
		for _, book := range books {
			allowed[book] = true
		}
		chunks := tools.scanChunks(saga, from, to, allowed)
		if len(chunks) == 0 {
			return "Подходящих глав нет.", nil
		}
		places := tools.runScan(ctx, task, chunks, saga)
		return renderScan(saga, chunks, places), nil
	})
}

// scanChunks groups the chapters of the range into model inputs of bounded size; a chapter longer than skippedChunks chunks
// closes the chunk before it and stands as a chunk of its own the scan skips (LC3.1).
func (tools *ReadTools) scanChunks(saga *sagaScope, from, to int, books map[string]bool) []*scanChunk {
	var chunks []*scanChunk
	var current []*sagamemory.Chapter
	size := 0
	for i := range saga.version().Chapters {
		chapter := &saga.version().Chapters[i]
		if !saga.inRange(chapter, from, to, books) {
			continue
		}
		length := chapter.Runes
		if len(current) > 0 && size+length > tools.scan.ChunkRunes {
			chunks = append(chunks, &scanChunk{chapters: current})
			current, size = nil, 0
		}
		if length > skippedChunks*tools.scan.ChunkRunes {
			chunks = append(chunks, &scanChunk{chapters: []*sagamemory.Chapter{chapter}, state: scanSkipped})
			continue
		}
		current = append(current, chapter)
		size += length
	}
	if len(current) > 0 {
		chunks = append(chunks, &scanChunk{chapters: current})
	}
	return chunks
}

// runScan reads chunks in story order in parallel. A chunk starts only while the time left
// covers the average chunk duration (half the budget before the first one ends); chunks cut
// by the deadline stay pending.
func (tools *ReadTools) runScan(ctx context.Context, task string, chunks []*scanChunk, saga *sagaScope) []scanPlace {
	authority := saga.authority
	deadline := time.Now().Add(tools.scan.Budget)
	// A tool runs in the context of Codex, not of the turn: the calls of the scan name the Job of the turn themselves
	// (specs/2026-11-13-desk-gateway.md GW3.2).
	runCtx, cancel := context.WithDeadline(jobs.WithJobID(ctx, authority.JobID), deadline)
	defer cancel()
	var mu sync.Mutex
	var places []scanPlace
	var spent time.Duration
	finished := 0
	estimate := func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		if finished == 0 {
			return tools.scan.Budget / 2
		}
		return spent / time.Duration(finished)
	}
	work := make(chan *scanChunk)
	var workers sync.WaitGroup
	for range min(tools.scan.Parallel, len(chunks)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for chunk := range work {
				if time.Until(deadline) < estimate() {
					continue
				}
				began := time.Now()
				found, cost, err := tools.scanChunk(runCtx, task, saga, chunk.chapters)
				tools.recordUsage(ctx, authority, cost)
				mu.Lock()
				switch {
				case err == nil:
					chunk.state = scanDone
					places = append(places, found...)
					spent += time.Since(began)
					finished++
				case runCtx.Err() == nil:
					chunk.state = scanFailed
				}
				mu.Unlock()
			}
		}()
	}
	for _, chunk := range chunks {
		if chunk.state != scanSkipped {
			work <- chunk
		}
	}
	close(work)
	workers.Wait()
	return places
}

// recordUsage counts a chunk's spend for the author of the chat turn (R7.2), also when the
// chunk failed or the budget cut it.
func (tools *ReadTools) recordUsage(ctx context.Context, authority researchdomain.ReadAuthority, usage quota.ModelUsage) {
	if tools.usage == nil || usage.Empty() {
		return
	}
	tools.usage.RecordModelUsage(context.WithoutCancel(ctx), quota.UsageRecord{
		Source: quota.UsageScan, ProjectID: authority.ProjectID, JobID: authority.JobID, Model: tools.scan.Model, Usage: usage,
	})
}

// scanChunk reads the text of the chunk's chapters right before its model call and drops it
// after (UC3, R3.2 of specs/2026-10-28-saga-search-in-postgres.md).
func (tools *ReadTools) scanChunk(ctx context.Context, task string, saga *sagaScope, chapters []*sagamemory.Chapter) ([]scanPlace, quota.ModelUsage, error) {
	indexes := make([]int, len(chapters))
	for i, chapter := range chapters {
		indexes[i] = chapter.Index
	}
	if err := tools.saga.Chapters(ctx, saga.outline, indexes); err != nil {
		return nil, quota.ModelUsage{}, err
	}
	var text strings.Builder
	byKey := make(map[string]*sagamemory.Chapter, len(chapters))
	for _, chapter := range chapters {
		byKey[chapter.Key] = chapter
		text.WriteString(sagamemory.ChapterText(chapter))
		text.WriteString("\n")
		chapter.Paragraphs = nil
	}
	// The chapters of the chunk go whole, named by the chunk itself: the chunks run in parallel (specs/2026-11-14-desk-journal.md
	// JR1.3). Without a port that hears what a turn gives (the site) no value is made.
	if tools.naming != nil {
		named := disclosure.Named{Chapters: make([]disclosure.Chapter, 0, len(chapters))}
		for _, chapter := range chapters {
			named.Chapters = append(named.Chapters, disclosure.Chapter{ID: chapter.ID, Precision: disclosure.Whole})
		}
		ctx = disclosure.With(ctx, named)
	}
	// The text of the chunk comes first and the task after it, the empty line after its last chapter between them: the
	// same chunk in the next scan, of another task, comes from the cache of the provider under its thread
	// (specs/2026-11-17-model-cache.md MC2.1). The instructions are as they were.
	ctx = scanThread(ctx, saga.authority.ProjectID, chapters)
	output, usage, err := tools.scanModel.Complete(ctx, sagamemory.ScanInstructions, text.String()+"ЗАДАЧА: "+task)
	if err != nil {
		return nil, usage, err
	}
	findings, err := sagamemory.ParseScan(output)
	if err != nil {
		return nil, usage, err
	}
	places := make([]scanPlace, 0, len(findings))
	for _, finding := range findings {
		key := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(finding.Chapter)), "гл."))
		chapter, ok := byKey[key]
		if !ok {
			chapter = chapters[0]
		}
		if finding.P < 1 || finding.P > chapter.ParagraphCount {
			finding.P = 0
		}
		places = append(places, scanPlace{ScanFinding: finding, chapter: chapter})
	}
	return places, usage, nil
}

// scanThread is the context of the call of a chunk: the thread of the saga and the chapters of the chunk in their order
// (specs/2026-11-17-model-cache.md MC1.3) — the same chunk in the next scan comes from the cache of the provider, and the
// chunks of one scan go apart over the accounts.
func scanThread(ctx context.Context, projectID string, chapters []*sagamemory.Chapter) context.Context {
	parts := make([]string, 0, len(chapters)+2)
	parts = append(parts, "scan", projectID)
	for _, chapter := range chapters {
		parts = append(parts, chapter.ID)
	}
	return modelthread.With(ctx, parts...)
}

// renderScan reports the scanned, unfinished and failed chapter ranges and groups the places
// by found item so enumerations stay compact.
func renderScan(saga *sagaScope, chunks []*scanChunk, places []scanPlace) string {
	sort.SliceStable(places, func(i, j int) bool {
		if places[i].chapter.Index != places[j].chapter.Index {
			return places[i].chapter.Index < places[j].chapter.Index
		}
		return places[i].P < places[j].P
	})
	type group struct {
		name   string
		places []scanPlace
	}
	var order []*group
	groups := map[string]*group{}
	seen := map[string]bool{}
	for _, place := range places {
		name := strings.TrimSpace(place.Item)
		if name == "" {
			name = "(без названия)"
		}
		key := sagamemory.Normalize(name)
		where := fmt.Sprintf("%s|%d|%d", key, place.chapter.Index, place.P)
		if seen[where] {
			continue
		}
		seen[where] = true
		item := groups[key]
		if item == nil {
			item = &group{name: name}
			groups[key] = item
			order = append(order, item)
		}
		item.places = append(item.places, place)
		saga.use(place.chapter)
		saga.show(place.chapter, disclosure.Excerpts, 0, 0)
	}
	var out strings.Builder
	// The numbers stand first: the feed reads the first line cut to 240 signs, and the chapters read are what the author is
	// told went whole (specs/2026-11-19-desk-trust.md TW2.1).
	if done := chunkRanges(chunks, scanDone); len(done) > 0 {
		read := 0
		for _, chunk := range chunks {
			if chunk.state == scanDone {
				read += len(chunk.chapters)
			}
		}
		fmt.Fprintf(&out, "Просмотрено глав: %d. Найдено: %d (мест: %d). Главы: %s. Порядок — по первому появлению.\n", read, len(order), len(seen), strings.Join(done, ", "))
	} else {
		out.WriteString("Ни одна глава не просмотрена.\n")
	}
	if pending := chunkRanges(chunks, scanPending); len(pending) > 0 {
		next := firstChapter(chunks, scanPending)
		fmt.Fprintf(&out, "Не успел за один вызов: %s. Продолжите scan с from=%s и тем же task.\n", strings.Join(pending, ", "), next.Key)
	}
	if failed := chunkRanges(chunks, scanFailed); len(failed) > 0 {
		fmt.Fprintf(&out, "Не удалось просмотреть: %s (повторите scan для этих глав).\n", strings.Join(failed, ", "))
	}
	for _, chunk := range chunks {
		if chunk.state == scanSkipped {
			chapter := chunk.chapters[0]
			fmt.Fprintf(&out, "Глава %s слишком длинная для scan (%d знаков): ищите в ней через search или читайте read с from/to по абзацам.\n",
				chapter.Key, chapter.Runes)
		}
	}
	for _, item := range order {
		place := item.places[0]
		fmt.Fprintf(&out, "- %s — мест: %d; впервые %s%s: «%s»", item.name, len(item.places), place.chapter.Ref(), paragraphMark(place.P), strings.TrimSpace(place.Quote))
		if note := strings.TrimSpace(place.Note); note != "" {
			out.WriteString(" (" + note + ")")
		}
		if len(item.places) > 1 {
			refs := make([]string, 0, min(len(item.places)-1, maxScanPlaces+1))
			for i, other := range item.places[1:] {
				if i == maxScanPlaces {
					refs = append(refs, fmt.Sprintf("… всего %d", len(item.places)))
					break
				}
				refs = append(refs, other.chapter.Ref()+paragraphMark(other.P))
			}
			out.WriteString("; также: " + strings.Join(refs, ", "))
		}
		out.WriteString("\n")
	}
	return out.String()
}

// chunkRanges merges consecutive chunks of one state into chapter ranges.
func chunkRanges(chunks []*scanChunk, state scanState) []string {
	var ranges []string
	for i := 0; i < len(chunks); i++ {
		if chunks[i].state != state {
			continue
		}
		j := i
		for j+1 < len(chunks) && chunks[j+1].state == state {
			j++
		}
		first, last := chunks[i].chapters[0], chunks[j].chapters[len(chunks[j].chapters)-1]
		if first == last {
			ranges = append(ranges, "гл. "+first.Key)
		} else {
			ranges = append(ranges, "гл. "+first.Key+"–"+last.Key)
		}
		i = j
	}
	return ranges
}

func firstChapter(chunks []*scanChunk, state scanState) *sagamemory.Chapter {
	for _, chunk := range chunks {
		if chunk.state == state {
			return chunk.chapters[0]
		}
	}
	return nil
}
