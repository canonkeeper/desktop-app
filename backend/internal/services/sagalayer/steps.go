package sagalayer

import (
	"context"
	"errors"
	"sync"

	"canonkeeper/backend/internal/domain/disclosure"
	domain "canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// Shares of the Job progress the steps end at; the checks take the most calls.
const (
	progressMap    = 0.25
	progressReduce = 0.35
	progressVerify = 0.8
	progressGap    = 0.85
	progressAdded  = 0.99
)

var digestParts = sagamemory.CardParts{Summary: true, Events: true, Facts: true, Knows: true, Hooks: true, Changes: true}

// run is one attempt of a build over the chapters that have cards.
type run struct {
	build    *BuildLayer
	ctx      context.Context
	claim    jobs.WorkerClaim
	subject  jobs.SagaLayerSubject
	outline  *sagamemory.Outline
	cards    map[int]*sagamemory.Card
	chapters []int // reading-order indexes of the chapters with cards
	stamps   []domain.Stamp
	digest   string
	index    *domain.EvidenceIndex
	stored   *domain.Stored

	mu    sync.Mutex // guards steps and the writes of a checkpoint
	steps domain.Steps

	textMu sync.Mutex
	texts  map[int]string // of the positions of chapters
	cut    map[int]bool   // the check reads only their beginning
}

func (build *BuildLayer) newRun(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject, stamps []domain.Stamp, stored *domain.Stored) (*run, error) {
	outline, err := build.text.Outline(ctx, subject.ProjectID, string(subject.SourceView))
	if err != nil {
		return nil, err
	}
	set, err := build.text.Cards(ctx, outline, 0, -1, digestParts)
	if err != nil {
		return nil, err
	}
	byChapter := make(map[string]domain.Stamp, len(stamps))
	for _, stamp := range stamps {
		byChapter[stamp.Chapter] = stamp
	}
	current := &run{build: build, ctx: ctx, claim: claim, subject: subject, outline: outline, cards: set.Cards, stored: stored, texts: map[int]string{}, cut: map[int]bool{}}
	sources := make([]domain.Source, 0, len(stamps))
	for i := range outline.Version.Chapters {
		chapter := &outline.Version.Chapters[i]
		stamp := byChapter[chapter.ID]
		if set.Cards[i] != nil && stamp.CardVersion > 0 && stamp.Hash == chapter.ContentHash {
			current.chapters = append(current.chapters, i)
			current.stamps = append(current.stamps, stamp)
			sources = append(sources, domain.Source{Chapter: chapter.ID, Title: chapter.Title, Card: set.Cards[i]})
		}
	}
	current.digest = domain.Digest(current.stamps)
	current.index = domain.NewEvidenceIndex(sources)
	current.steps = domain.Steps{Digest: current.digest}
	if stored.Steps != nil && stored.Steps.Digest == current.digest {
		current.steps = *stored.Steps
	}
	if current.steps.Verdicts == nil {
		current.steps.Verdicts = map[int]domain.Verdict{}
	}
	if current.steps.GapVerdicts == nil {
		current.steps.GapVerdicts = map[int]domain.Verdict{}
	}
	return current, nil
}

func (current *run) save(books []domain.Book, progress float64) error {
	return current.build.store.SaveCheckpoint(current.ctx, current.claim, current.stamps, Checkpoint{Books: books, Steps: current.steps, Model: current.build.modelName}, progress)
}

// ask sends one call, up to three times; parse takes an answer and says whether it can be read. With the policy of the
// reading of the app only an answer that did not read is asked again (specs/2026-11-15-desk-reading-rest.md RR1.3): a cut
// answer, a refusal and no answer would come again at the same settings, and a cut answer was paid whole — the question gives
// up at once with its reason; an answer that did not read through the three gives up as unreadable.
//
// The call names what it gives (specs/2026-11-14-desk-journal.md JR1.3) in a context of its own over the context of the run:
// the books and the checks run in parallel. A request the journal of the app did not send is not asked again (JR1.6).
func (current *run) ask(named disclosure.Named, instructions, input string, parse func(string) error) error {
	var last error
	marks, unreadable := current.build.policy != nil, false
	for range callAttempts {
		if err := current.ctx.Err(); err != nil {
			return err
		}
		output, spent, err := current.build.model.Complete(disclosure.With(current.ctx, named), instructions, input)
		current.build.recordUsage(current.ctx, current.claim.ProjectID, spent)
		if _, stopped := sagamemory.StopOf(err); stopped || errors.Is(err, disclosure.ErrJournal) {
			// A stop of the reading is not asked again (specs/2026-11-13-desk-consent.md CN3.4).
			return err
		}
		if reason := sagamemory.UnreadOf(err); marks && reason != "" {
			return &unanswered{reason: reason, err: err}
		}
		unreadable = err == nil
		if err == nil {
			err = parse(output)
		}
		if err == nil {
			return nil
		}
		last = err
	}
	if marks && unreadable {
		return &unanswered{reason: sagamemory.UnreadUnreadable, err: last}
	}
	return last
}

// unanswered is a question of a build that gave no answer for a named reason (RR1.1, RR1.3): the build is marked with it.
type unanswered struct {
	reason string
	err    error
}

func (missed *unanswered) Error() string { return missed.err.Error() }

func (missed *unanswered) Unwrap() error { return missed.err }

// steps_ goes through the steps the checkpoint does not hold yet.
func (current *run) runSteps() (*domain.Layer, *jobs.SagaLayerResult, error) {
	version := current.outline.Version
	books, built, err := current.mapBooks()
	if err != nil {
		return nil, nil, err
	}
	if !current.steps.Reduced {
		// The reduction reads the threads of the books (JR1.3).
		err := current.ask(disclosure.Named{Threads: true}, domain.ReduceInstructions, domain.ReduceInput(books, version), func(output string) (err error) {
			current.steps.Registry, err = domain.ParseRegistry(output, version)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		current.steps.Reduced = true
		if err := current.save(nil, progressReduce); err != nil {
			return nil, nil, err
		}
	}
	if err := current.verify(current.steps.Registry, current.steps.Verdicts, progressReduce, progressVerify); err != nil {
		return nil, nil, err
	}
	kept := domain.ApplyVerdicts(current.steps.Registry, current.steps.Verdicts)
	if !current.steps.GapDone {
		// «Что упущено» reads the threads and what the helper learned of the chapters of the books (JR1.3).
		gap := current.cardsOf(current.chapters)
		gap.Threads = true
		err := current.ask(gap, domain.GapInstructions, domain.GapInput(kept, books, version), func(output string) (err error) {
			current.steps.Gap, err = domain.ParseRegistry(output, version)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		current.steps.GapDone = true
		if err := current.save(nil, progressGap); err != nil {
			return nil, nil, err
		}
	}
	if err := current.verify(current.steps.Gap, current.steps.GapVerdicts, progressGap, progressAdded); err != nil {
		return nil, nil, err
	}
	threads := append(kept, domain.ApplyVerdicts(current.steps.Gap, current.steps.GapVerdicts)...)
	layer := &domain.Layer{Chapters: current.stamps, Books: books, Threads: threads}
	return layer, &jobs.SagaLayerResult{
		Type: jobs.KindSagaLayer, SourceView: current.subject.SourceView, Chapters: len(current.stamps), Threads: len(threads),
		BooksBuilt: built, Unverified: unverified(current.steps.Verdicts) + unverified(current.steps.GapVerdicts),
	}, nil
}

func unverified(verdicts map[int]domain.Verdict) int {
	failed := 0
	for _, verdict := range verdicts {
		if verdict.Failed {
			failed++
		}
	}
	return failed
}

// mapBooks takes each book from the checkpoint or the finished build when the digest of its chapters is
// the same (R2.3) and asks the model about the others, saving each answer. It returns the books in
// reading order and how many it asked about.
func (current *run) mapBooks() ([]domain.Book, int, error) {
	known := map[[2]string]domain.Book{}
	for _, book := range current.stored.Layer.Books {
		known[[2]string{book.ID, book.Digest}] = book
	}
	for _, book := range current.stored.CheckpointBooks {
		known[[2]string{book.ID, book.Digest}] = book
	}
	type plan struct {
		book     domain.Book
		chapters []int
	}
	var plans []*plan
	byBook := map[string]*plan{}
	stampsOf := map[string][]domain.Stamp{}
	for position, index := range current.chapters {
		chapter := &current.outline.Version.Chapters[index]
		if byBook[chapter.BookID] == nil {
			byBook[chapter.BookID] = &plan{book: domain.Book{ID: chapter.BookID, Order: chapter.BookOrder}}
			plans = append(plans, byBook[chapter.BookID])
		}
		byBook[chapter.BookID].chapters = append(byBook[chapter.BookID].chapters, index)
		stampsOf[chapter.BookID] = append(stampsOf[chapter.BookID], current.stamps[position])
	}
	done := make([]bool, len(plans))
	var pending []int
	for i, plan := range plans {
		plan.book.Digest = domain.Digest(stampsOf[plan.book.ID])
		if book, ok := known[[2]string{plan.book.ID, plan.book.Digest}]; ok {
			// The answer of a volume is bound to its chapters, not to its place: a volume moved on the shelf keeps it
			// under its new number (specs/2026-11-11-ux7-reorder.md RK2.3).
			book.Order = plan.book.Order
			plan.book, done[i] = book, true
			continue
		}
		pending = append(pending, i)
	}
	answered := func() []domain.Book {
		books := make([]domain.Book, 0, len(plans))
		for i, plan := range plans {
			if done[i] {
				books = append(books, plan.book)
			}
		}
		return books
	}
	err := runAll(current.ctx, current.build.parallel, pending, func(ctx context.Context, i int) error {
		plan := plans[i]
		digests := make([]string, len(plan.chapters))
		for j, index := range plan.chapters {
			digests[j] = domain.ChapterDigest(&current.outline.Version.Chapters[index], current.cards[index])
		}
		var book domain.Book
		err := current.ask(current.cardsOf(plan.chapters), domain.MapInstructions, domain.BookInput(plan.book.Order, digests), func(output string) (err error) {
			book, err = domain.ParseBook(output, current.outline.Version)
			return err
		})
		if err != nil {
			return err
		}
		book.ID, book.Order, book.Digest = plan.book.ID, plan.book.Order, plan.book.Digest
		current.mu.Lock()
		defer current.mu.Unlock()
		plan.book, done[i] = book, true
		finished := 0
		for _, ok := range done {
			if ok {
				finished++
			}
		}
		return current.save(answered(), progressMap*float64(finished)/float64(len(plans)))
	})
	if err != nil {
		return nil, 0, err
	}
	return answered(), len(pending), nil
}

// verify checks each record no verdict names yet (R2.4). A check that fails leaves its record as it is,
// and that is saved too: a stopped Job, or a text gone from under it, fails the run instead.
func (current *run) verify(threads []domain.Thread, verdicts map[int]domain.Verdict, from, to float64) error {
	var pending []int
	for i := range threads {
		if _, ok := verdicts[i]; !ok {
			pending = append(pending, i)
		}
	}
	var lastFailure error
	err := runAll(current.ctx, current.build.parallel, pending, func(ctx context.Context, i int) error {
		input, named, err := current.evidence(threads[i])
		if err != nil {
			return err
		}
		var verdict domain.Verdict
		err = current.ask(named, domain.VerifyInstructions, input, func(output string) (err error) {
			verdict, err = domain.ParseVerdict(output)
			return err
		})
		if stopped := current.ctx.Err(); stopped != nil {
			return stopped
		}
		if _, stopped := sagamemory.StopOf(err); stopped || errors.Is(err, disclosure.ErrJournal) {
			// A stop of the reading ends the build; the verdicts so far stay in the checkpoint. A check the journal did not send
			// writes no verdict: the worker repeats the Job (specs/2026-11-14-desk-journal.md JR1.6).
			return err
		}
		current.mu.Lock()
		defer current.mu.Unlock()
		if err != nil {
			verdict, lastFailure = domain.Verdict{Decision: "keep", Failed: true}, err
		}
		verdicts[i] = verdict
		return current.save(nil, from+(to-from)*float64(len(verdicts))/float64(max(len(threads), 1)))
	})
	if lastFailure != nil && current.build.failure != nil {
		current.build.failure(current.claim.ProjectID, "verify", lastFailure)
	}
	return err
}

// evidence is the input of the check of a thread and what it gives (JR1.3): the chapters of the thread whole, or their
// beginning when longer than the check reads, and what the helper learned of the later chapters.
func (current *run) evidence(thread domain.Thread) (string, disclosure.Named, error) {
	version := current.outline.Version
	texts := map[int]string{}
	var named disclosure.Named
	for _, position := range current.index.Chapters(thread) {
		text, cut, err := current.chapterText(position)
		if err != nil {
			return "", disclosure.Named{}, err
		}
		texts[position] = text
		precision := disclosure.Whole
		if cut {
			precision = disclosure.Beginning
		}
		named.Chapter(version.Chapters[current.chapters[position]].ID, precision, 0, 0)
	}
	later := make([]int, 0)
	for _, position := range current.index.Later(thread) {
		later = append(later, current.chapters[position])
	}
	named = disclosure.Merge(named, current.cardsOf(later))
	return current.index.Evidence(thread, domain.RenderRefs(thread.First, version), domain.RenderRefs(thread.Last, version), texts), named, nil
}

// cardsOf names what the helper learned of the chapters of the indexes of the version (JR1.3).
func (current *run) cardsOf(indexes []int) disclosure.Named {
	named := disclosure.Named{Chapters: make([]disclosure.Chapter, 0, len(indexes))}
	for _, index := range indexes {
		named.Chapters = append(named.Chapters, disclosure.Chapter{ID: current.outline.Version.Chapters[index].ID, Precision: disclosure.Cards})
	}
	return named
}

// chapterText loads a chapter once and keeps only what a check reads of it, and whether that is only its beginning.
func (current *run) chapterText(position int) (string, bool, error) {
	current.textMu.Lock()
	defer current.textMu.Unlock()
	if text, ok := current.texts[position]; ok {
		return text, current.cut[position], nil
	}
	index := current.chapters[position]
	if err := current.build.text.Chapters(current.ctx, current.outline, []int{index}); err != nil {
		return "", false, err
	}
	chapter := &current.outline.Version.Chapters[index]
	paragraphs := make([]string, len(chapter.Paragraphs))
	for i, paragraph := range chapter.Paragraphs {
		paragraphs[i] = paragraph.Text
	}
	current.texts[position] = domain.ChapterEvidenceText(chapter.Title, paragraphs)
	current.cut[position] = domain.EvidenceCut(paragraphs)
	chapter.Paragraphs = nil
	return current.texts[position], current.cut[position], nil
}

// runAll runs work over items with at most parallel at once; the first error stops what has not
// started and is returned when the running ones have ended.
func runAll(ctx context.Context, parallel int, items []int, work func(context.Context, int) error) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var (
		mu    sync.Mutex
		first error
		group sync.WaitGroup
	)
	queue := make(chan int)
	for range min(parallel, max(len(items), 1)) {
		group.Add(1)
		go func() {
			defer group.Done()
			for item := range queue {
				if ctx.Err() != nil {
					// The first error stopped what has not started, an item handed over at that moment too.
					continue
				}
				if err := work(ctx, item); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
					stop()
				}
			}
		}()
	}
dispatch:
	for _, item := range items {
		select {
		case queue <- item:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(queue)
	group.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}
