package indexing

import (
	"time"

	"canonkeeper/backend/internal/domain/canon"
	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
)

type statusScope struct {
	bookID     string
	sourceView governance.SourceView
}

type statusSnapshot struct {
	role  projects.Role
	views map[governance.SourceView]statusViewSnapshot
	jobs  []jobs.Job
	wave  *statusWave
	// waiting is what the reading waits for as the build says it (specs/2026-11-12-desk-window.md WN3.1).
	waiting canon.PreparationWaiting
}

// statusWave is the wave of the preparation of the saga as the reader of the status sees it
// (specs/2026-11-09-first-session.md, FS4, FS5): when it started and closed, the time of the database, whether the
// reader is whom the wave is told to, and of an open wave — whether a new book is still to be imported and how many
// cards were written over indexingdomain.PaceSpan.
type statusWave struct {
	startedAt   time.Time
	readyAt     *time.Time
	now         time.Time
	recipient   bool
	importing   bool
	recentCards int
}

type statusViewSnapshot struct {
	revision uint64
	books    []statusBookSnapshot
}

// statusBookSnapshot counts the live chapters of one book in a text version, those whose
// current revision is indexed and those that also have the card of that text.
type statusBookSnapshot struct {
	id       string
	chapters int
	indexed  int
	carded   int
}

func (book statusBookSnapshot) ready() bool {
	return book.indexed == book.chapters && book.carded == book.chapters
}

func buildStatusResponse(snapshot statusSnapshot, scope statusScope) (canon.IndexingStatusResponse, error) {
	response := canon.IndexingStatusResponse{Published: buildViewStatus(snapshot, governance.SourceViewPublished)}
	if projects.CanMutateProject(snapshot.role) {
		draft := buildViewStatus(snapshot, governance.SourceViewDraft)
		response.Draft = &draft
	}
	response.Steps = []canon.IndexingStep{}
	applyStatusWave(&response, snapshot)
	if scope.bookID == "" {
		return response, nil
	}
	for _, book := range snapshot.views[scope.sourceView].books {
		if book.id == scope.bookID {
			response.Steps = buildStatusSteps(snapshot, book, scope.sourceView)
			return response, nil
		}
	}
	return canon.IndexingStatusResponse{}, indexingdomain.ErrNotFound
}

// applyStatusWave adds what the status says of the reading of the text the site reads. A closed wave is told for
// indexingdomain.ReadyShownFor while that text is ready, with the books and chapters read now (FS4.1). While the text is
// in work, or a new book of an open wave is still to be imported — between the volumes of an import queue every chapter
// there is has its card, and the wave goes on — the status counts the reading (specs/2026-11-10-ux3-saga-state.md
// SS1.1). An open wave adds the estimate of the chapters without cards, by the pace of the latest cards and only when no
// new book is still to be imported, whose chapters nobody has counted, and the promise of the letter to its recipient
// once the wave is long enough to be worth one (FS5.1, FS5.2, FS5.6). Without a pace or a wave a few chapters still
// have their usual time said (indexingdomain.RemainingSeconds).
func applyStatusWave(response *canon.IndexingStatusResponse, snapshot statusSnapshot) {
	wave, context := snapshot.wave, response.Published.Context
	if context == nil {
		return
	}
	reading := readingOf(snapshot.views[governance.SourceViewPublished].books)
	if wave != nil && wave.readyAt != nil && context.State == canon.IndexingStateReady && reading.ChaptersTotal > 0 &&
		!wave.now.After(wave.readyAt.Add(indexingdomain.ReadyShownFor)) {
		response.SagaReady = &canon.IndexingSagaReady{At: wave.readyAt.UTC(), Books: reading.BooksTotal, Chapters: reading.ChaptersTotal}
	}
	open := wave != nil && wave.readyAt == nil
	if snapshot.waiting != "" {
		// Nobody reads the books while the reading waits (specs/2026-11-12-desk-window.md WN3.1): the text read so far and
		// a new book still to be imported are counted as they are, with what the reading waits for and no time or letter.
		if reading.ChaptersRead < reading.ChaptersTotal || open && wave.importing {
			reading.Waiting = snapshot.waiting
			response.Preparation = &reading
		}
		return
	}
	if context.State != canon.IndexingStateProcessing && !(open && wave.importing) {
		return
	}
	recent, span := 0, time.Duration(0)
	if open {
		reading.LetterPromised = wave.recipient && wave.now.Sub(wave.startedAt) > indexingdomain.ReadyLetterAfter
		recent, span = wave.recentCards, indexingdomain.PaceSpan(wave.startedAt, wave.now)
	}
	// A text read again without an open wave — a chapter added or edited — has no pace, only the usual time of a few
	// chapters; a new book still to be imported has chapters nobody has counted.
	if !(open && wave.importing) {
		if seconds, ok := indexingdomain.RemainingSeconds(recent, span, reading.ChaptersTotal-reading.ChaptersRead); ok {
			reading.RemainingSeconds = &seconds
		}
	}
	response.Preparation = &reading
}

// readingOf counts the reading of the books of the published text by the counters the wave closes on (SS1.1, SS1.4): a
// chapter is read once its current text has its card, a book once every chapter is, the search finds a chapter once its
// current text is indexed. A book without chapters is not read and not counted.
func readingOf(books []statusBookSnapshot) canon.IndexingPreparation {
	reading := canon.IndexingPreparation{Books: []canon.IndexingPreparationBook{}}
	for _, book := range books {
		if book.chapters == 0 {
			continue
		}
		reading.Books = append(reading.Books, canon.IndexingPreparationBook{BookID: book.id, ChaptersRead: book.carded, ChaptersTotal: book.chapters})
		reading.BooksTotal++
		reading.ChaptersTotal += book.chapters
		reading.ChaptersRead += book.carded
		reading.Askable = reading.Askable || book.indexed > 0
		if book.ready() {
			reading.BooksRead++
		} else if reading.CurrentBookID == nil {
			reading.CurrentBookID = statusString(book.id)
		}
	}
	return reading
}

// buildViewStatus is ready when every book of the version has indexed text and cards,
// processing while its text index or cards Job is active (the current book is the first one
// still missing something), and partial otherwise, when the author may restart indexing.
func buildViewStatus(snapshot statusSnapshot, view governance.SourceView) canon.IndexingViewEnvelope {
	viewSnapshot, ok := snapshot.views[view]
	if !ok {
		return canon.IndexingViewEnvelope{Exists: false}
	}
	context := &canon.IndexingViewContext{State: canon.IndexingStateReady}
	active := jobActive(latestTextJob(snapshot.jobs, view, viewSnapshot.revision)) || jobActive(latestCardsJob(snapshot.jobs, view))
	for index, book := range viewSnapshot.books {
		if book.ready() {
			continue
		}
		if active {
			context.State = canon.IndexingStateProcessing
			context.CurrentBook = &canon.IndexingCurrentBook{BookID: book.id, Position: index + 1, Total: len(viewSnapshot.books)}
		} else {
			context.State = canon.IndexingStatePartial
			if projects.CanMutateProject(snapshot.role) {
				context.RetryableFailedBook = &canon.IndexingRetryableFailedBook{BookID: book.id}
			}
		}
		break
	}
	return canon.IndexingViewEnvelope{Exists: true, Context: context}
}

func buildStatusSteps(snapshot statusSnapshot, book statusBookSnapshot, view governance.SourceView) []canon.IndexingStep {
	viewSnapshot := snapshot.views[view]
	importStep := stepFromJob(canon.IndexingStepImport, latestImportJob(snapshot.jobs, book.id), snapshot.role, book.id, "")
	textStep := stepFromJob(canon.IndexingStepTextIndex, latestTextJob(snapshot.jobs, view, viewSnapshot.revision), snapshot.role, "", view)
	textStep.Counters = chapterCounters(book.indexed, book.chapters)
	switch {
	case book.indexed == book.chapters:
		textStep.Status, textStep.Progress = canon.IndexingStepSucceeded, statusFloat(1)
	case textStep.Status == canon.IndexingStepSucceeded:
		textStep.Status, textStep.Progress = canon.IndexingStepPending, nil
	}
	cardsStep := stepFromJob(canon.IndexingStepCards, latestCardsJob(snapshot.jobs, view), snapshot.role, "", view)
	cardsStep.Counters = chapterCounters(book.carded, book.chapters)
	if book.chapters > 0 {
		cardsStep.Progress = statusFloat(float64(book.carded) / float64(book.chapters))
	}
	switch {
	case book.ready():
		cardsStep.Status, cardsStep.Progress = canon.IndexingStepSucceeded, statusFloat(1)
	case cardsStep.Status == canon.IndexingStepSucceeded:
		// The finished Job covered an older text: the cards of this book follow the next one.
		cardsStep.Status = canon.IndexingStepPending
	}
	readyStep := canon.IndexingStep{
		Kind: canon.IndexingStepReady, Status: canon.IndexingStepPending,
		BookID: statusString(book.id), SourceView: statusView(view),
	}
	if book.ready() && importStep.Status != canon.IndexingStepFailed && importStep.Status != canon.IndexingStepCanceled {
		readyStep.Status, readyStep.Progress = canon.IndexingStepSucceeded, statusFloat(1)
	}
	return []canon.IndexingStep{importStep, textStep, cardsStep, readyStep}
}

func chapterCounters(done, total int) *canon.IndexingStepCounters {
	return &canon.IndexingStepCounters{IndexedChapters: &done, TotalChapters: &total}
}

func stepFromJob(
	kind canon.IndexingStepKind,
	job *jobs.Job,
	role projects.Role,
	bookID string,
	view governance.SourceView,
) canon.IndexingStep {
	step := canon.IndexingStep{
		Kind: kind, Status: canon.IndexingStepPending, BookID: statusString(bookID), SourceView: statusView(view),
	}
	if job == nil {
		return step
	}
	step.Status, step.Progress = jobStepStatus(job.Status), job.Progress
	step.JobID, step.JobCreatedAt = statusString(job.ID), &job.CreatedAt
	step.JobStartedAt, step.JobCompletedAt = job.StartedAt, job.CompletedAt
	step.StartedAt, step.CompletedAt = job.StartedAt, job.CompletedAt
	step.CanCancel = job.CanCancel
	step.CanRetry = projects.CanMutateProject(role) && job.Status == jobs.StatusFailed
	if job.Status == jobs.StatusSucceeded {
		step.Progress = statusFloat(1)
	}
	if kind == canon.IndexingStepImport && job.ImportProgress != nil {
		progress := job.ImportProgress
		step.ImportPhase = &progress.Phase
		step.Counters = &canon.IndexingStepCounters{
			BytesTransferred: &progress.BytesTransferred, BytesTotal: &progress.BytesTotal,
			ParsedChapters: progress.ParsedChapters, SavedChapters: progress.SavedChapters,
			TotalChapters: progress.TotalChapters,
		}
	}
	return step
}

func latestImportJob(all []jobs.Job, bookID string) *jobs.Job {
	return latestJob(all, func(job jobs.Job) bool {
		if job.Kind != jobs.KindImport {
			return false
		}
		result, ok := job.Result.(jobs.ImportJobResult)
		return ok && result.BookID == bookID
	})
}

func latestTextJob(all []jobs.Job, view governance.SourceView, revision uint64) *jobs.Job {
	return latestJob(all, func(job jobs.Job) bool {
		subject, ok := job.Subject.(jobs.CanonTextIndexSubject)
		return job.Kind == jobs.KindCanonTextIndex && ok && subject.SourceView == view && subject.CatalogRevision == revision
	})
}

// latestCardsJob prefers an active cards Job of the version over a newer finished one.
func latestCardsJob(all []jobs.Job, view governance.SourceView) *jobs.Job {
	matches := func(job jobs.Job) bool {
		subject, ok := job.Subject.(jobs.ChapterCardsSubject)
		return job.Kind == jobs.KindChapterCards && ok && subject.SourceView == view
	}
	if active := latestJob(all, func(job jobs.Job) bool { return matches(job) && jobActive(&job) }); active != nil {
		return active
	}
	return latestJob(all, matches)
}

func latestJob(all []jobs.Job, matches func(jobs.Job) bool) *jobs.Job {
	var latest *jobs.Job
	for index := range all {
		candidate := &all[index]
		if matches(*candidate) && (latest == nil || candidate.CreatedAt.After(latest.CreatedAt) ||
			candidate.CreatedAt.Equal(latest.CreatedAt) && candidate.ID > latest.ID) {
			latest = candidate
		}
	}
	return latest
}

func jobActive(job *jobs.Job) bool {
	return job != nil && (job.Status == jobs.StatusQueued || job.Status == jobs.StatusRunning || job.Status == jobs.StatusCanceling)
}

func jobStepStatus(status jobs.Status) canon.IndexingStepStatus {
	switch status {
	case jobs.StatusQueued:
		return canon.IndexingStepQueued
	case jobs.StatusRunning:
		return canon.IndexingStepRunning
	case jobs.StatusCanceling:
		return canon.IndexingStepCanceling
	case jobs.StatusSucceeded:
		return canon.IndexingStepSucceeded
	case jobs.StatusFailed, jobs.StatusExpired:
		return canon.IndexingStepFailed
	case jobs.StatusCanceled:
		return canon.IndexingStepCanceled
	default:
		return canon.IndexingStepPartial
	}
}

func statusString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func statusView(value governance.SourceView) *governance.SourceView {
	if value == "" {
		return nil
	}
	return &value
}

func statusFloat(value float64) *float64 { return &value }
