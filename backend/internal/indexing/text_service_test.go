package indexing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

const (
	textIndexServiceProjectID  = "10000000-0000-4000-8000-000000000001"
	textIndexServiceBookID     = "10000000-0000-4000-8000-000000000002"
	textIndexServiceChapterAID = "10000000-0000-4000-8000-000000000003"
	textIndexServiceChapterBID = "10000000-0000-4000-8000-000000000004"
	textIndexServiceJobID      = "10000000-0000-4000-8000-000000000006"
)

func TestTextIndexServiceBoundsChapterParallelism(t *testing.T) {
	store := newTextIndexServiceStoreForTest(8)
	store.replaceStarted = make(chan string, 8)
	release := make(chan struct{})
	store.replaceRelease = release
	service := NewService(store, &startIDsForTest{}, textIndexServiceClock{now: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)})

	done := make(chan error, 1)
	go func() {
		done <- service.RunForClaim(context.Background(), textIndexServiceClaim(), textIndexServiceSubject())
	}()
	waitForTextIndexServiceStarts(t, store.replaceStarted, textIndexChapterParallelism)
	select {
	case chapterID := <-store.replaceStarted:
		t.Fatalf("chapter %s started above parallelism limit", chapterID)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := waitForTextIndexServiceResult(t, done); err != nil {
		t.Fatal(err)
	}
	replacements, maxActive, completeCalls, activeAtComplete := store.stats()
	if replacements != 8 || maxActive != textIndexChapterParallelism {
		t.Fatalf("replacements = %d, max active = %d", replacements, maxActive)
	}
	if completeCalls != 1 || activeAtComplete != 0 {
		t.Fatalf("complete calls = %d, active replacements at complete = %d", completeCalls, activeAtComplete)
	}
}

func TestTextIndexServiceSkipsCurrentChapter(t *testing.T) {
	store := newTextIndexServiceStoreForTest(2)
	snapshot := store.snapshots[textIndexServiceChapterAID]
	snapshot.Unchanged = true
	store.snapshots[textIndexServiceChapterAID] = snapshot
	service := NewService(store, &startIDsForTest{}, textIndexServiceClock{now: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)})

	if err := service.RunForClaim(context.Background(), textIndexServiceClaim(), textIndexServiceSubject()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.replacements) != 1 || store.replacements[0].Snapshot.ChapterID != textIndexServiceChapterBID {
		t.Fatalf("replacements = %#v", store.replacements)
	}
	if store.completeCalls != 1 {
		t.Fatalf("complete calls = %d, want 1", store.completeCalls)
	}
}

func TestTextIndexServiceSendsWholeChapterWithoutChunks(t *testing.T) {
	store := newTextIndexServiceStoreForTest(1)
	service := NewService(store, &startIDsForTest{}, textIndexServiceClock{now: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)})

	if err := service.RunForClaim(context.Background(), textIndexServiceClaim(), textIndexServiceSubject()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.replacements) != 1 || store.replacements[0].Snapshot.ChapterID != textIndexServiceChapterAID {
		t.Fatalf("replacements = %#v, want the whole-chapter snapshot", store.replacements)
	}
}

func TestTextIndexServiceStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := newTextIndexServiceStoreForTest(8)
	store.replaceStarted = make(chan string, 8)
	store.replaceRelease = make(chan struct{})
	service := NewService(store, &startIDsForTest{}, textIndexServiceClock{now: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)})

	done := make(chan error, 1)
	go func() {
		done <- service.RunForClaim(ctx, textIndexServiceClaim(), textIndexServiceSubject())
	}()
	waitForTextIndexServiceStarts(t, store.replaceStarted, textIndexChapterParallelism)
	cancel()
	if err := waitForTextIndexServiceResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunForClaim() error = %v, want context.Canceled", err)
	}
	replacements, _, completeCalls, _ := store.stats()
	if replacements != textIndexChapterParallelism || len(store.replaceStarted) != 0 || completeCalls != 0 {
		t.Fatalf("replacements = %d, queued starts = %d, complete calls = %d", replacements, len(store.replaceStarted), completeCalls)
	}
}

func TestTextIndexServiceReturnsClosedConflictOnSourceDrift(t *testing.T) {
	store := newTextIndexServiceStoreForTest(8)
	store.replaceStarted = make(chan string, 8)
	release := make(chan struct{})
	store.replaceRelease = release
	store.replaceErrors = map[string]error{textIndexServiceChapterAID: ErrTextIndexConflict}
	service := NewService(store, &startIDsForTest{}, textIndexServiceClock{now: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)})

	done := make(chan error, 1)
	go func() {
		done <- service.RunForClaim(context.Background(), textIndexServiceClaim(), textIndexServiceSubject())
	}()
	waitForTextIndexServiceStarts(t, store.replaceStarted, textIndexChapterParallelism)
	select {
	case chapterID := <-store.replaceStarted:
		t.Fatalf("chapter %s started after source drift", chapterID)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := waitForTextIndexServiceResult(t, done); !errors.Is(err, ErrTextIndexConflict) {
		t.Fatalf("RunForClaim() error = %v, want ErrTextIndexConflict", err)
	}
	replacements, _, completeCalls, _ := store.stats()
	if replacements != textIndexChapterParallelism || len(store.replaceStarted) != 0 || completeCalls != 0 {
		t.Fatalf("replacements = %d, queued starts = %d, complete calls = %d", replacements, len(store.replaceStarted), completeCalls)
	}
}

func TestTextIndexServiceReturnsFirstChapterErrorByDescriptorOrder(t *testing.T) {
	store := newTextIndexServiceStoreForTest(4)
	store.replaceStarted = make(chan string, 4)
	releaseFirst := make(chan struct{})
	store.replaceWait = map[string]<-chan struct{}{textIndexServiceChapterAID: releaseFirst}
	store.replaceErrors = map[string]error{
		textIndexServiceChapterAID: ErrTextIndexConflict,
		textIndexServiceChapterBID: ErrTextIndexValidation,
	}
	service := NewService(store, &startIDsForTest{}, textIndexServiceClock{now: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)})
	done := make(chan error, 1)
	go func() {
		done <- service.RunForClaim(context.Background(), textIndexServiceClaim(), textIndexServiceSubject())
	}()
	waitForTextIndexServiceStarts(t, store.replaceStarted, 4)
	close(releaseFirst)
	if err := waitForTextIndexServiceResult(t, done); !errors.Is(err, ErrTextIndexConflict) {
		t.Fatalf("RunForClaim() error = %v, want first descriptor conflict", err)
	}
}

func TestTextIndexServiceRejectsInvalidSubjectBeforeStoreAccess(t *testing.T) {
	store := newTextIndexServiceStoreForTest(2)
	service := NewService(store, &startIDsForTest{}, textIndexServiceClock{now: time.Now()})
	subject := textIndexServiceSubject()
	subject.CatalogRevision = 0

	if err := service.RunForClaim(context.Background(), textIndexServiceClaim(), subject); !errors.Is(err, ErrTextIndexValidation) {
		t.Fatalf("RunForClaim() error = %v, want ErrTextIndexValidation", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.events) != 0 {
		t.Fatalf("events = %v, want none", store.events)
	}
}

func TestTextIndexServiceRedactsUnexpectedStoreFailure(t *testing.T) {
	store := newTextIndexServiceStoreForTest(2)
	store.listErr = errors.New("private database detail")
	service := NewService(store, &startIDsForTest{}, textIndexServiceClock{now: time.Now()})

	err := service.RunForClaim(context.Background(), textIndexServiceClaim(), textIndexServiceSubject())
	if !errors.Is(err, ErrTextIndexRetryable) || err.Error() != ErrTextIndexRetryable.Error() {
		t.Fatalf("RunForClaim() error = %v, want redacted retryable error", err)
	}
}

type textIndexServiceClock struct{ now time.Time }

func (c textIndexServiceClock) Now() time.Time { return c.now }

type textIndexServiceStoreForTest struct {
	// Manual-start метод Store в этих worker-тестах не вызывается.
	Store
	mu               sync.Mutex
	descriptors      []CanonTextChapterDescriptor
	snapshots        map[string]CanonTextChapterSnapshot
	events           []string
	replacements     []ReplaceCanonTextChapterInput
	listErr          error
	replaceErrors    map[string]error
	replaceWait      map[string]<-chan struct{}
	replaceStarted   chan string
	replaceRelease   <-chan struct{}
	active           int
	maxActive        int
	completeCalls    int
	activeAtComplete int
}

func newTextIndexServiceStoreForTest(chapterCount int) *textIndexServiceStoreForTest {
	digest := governance.Digest{1}
	descriptors := make([]CanonTextChapterDescriptor, chapterCount)
	snapshots := make(map[string]CanonTextChapterSnapshot, len(descriptors))
	for index := range descriptors {
		descriptor := CanonTextChapterDescriptor{
			ProjectID: textIndexServiceProjectID, ProjectLifecycleGeneration: 1,
			SourceView: governance.SourceViewDraft, CatalogRevision: 7, BookID: textIndexServiceBookID,
			ChapterID:      fmt.Sprintf("10000000-0000-4000-8000-%012d", index+3),
			SourceRevision: uint64(index + 2), SourceGeneration: digest,
		}
		descriptors[index] = descriptor
		paragraph := CanonicalParagraphSource{
			ProjectID: descriptor.ProjectID, BookID: descriptor.BookID, ChapterID: descriptor.ChapterID,
			ParagraphID: fmt.Sprintf("10000000-0000-4000-8000-%012d", index+5), SourceView: descriptor.SourceView,
			SourceRevision: descriptor.SourceRevision, SourceGeneration: descriptor.SourceGeneration,
			Order: 0, Content: "Chapter text.",
		}
		snapshots[descriptor.ChapterID] = CanonTextChapterSnapshot{Claim: textIndexServiceClaim(), CanonTextChapterDescriptor: descriptor, Paragraphs: []CanonicalParagraphSource{paragraph}, ChapterContentHash: governance.Digest{byte(index + 2)}}
	}
	return &textIndexServiceStoreForTest{descriptors: descriptors, snapshots: snapshots}
}

func (s *textIndexServiceStoreForTest) ListCanonTextChapterDescriptorsForClaim(context.Context, jobs.WorkerClaim) ([]CanonTextChapterDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "list")
	return s.descriptors, s.listErr
}

func (s *textIndexServiceStoreForTest) LoadCanonTextChapterSnapshotForClaim(_ context.Context, _ jobs.WorkerClaim, descriptor CanonTextChapterDescriptor) (CanonTextChapterSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "load:"+descriptor.ChapterID)
	return s.snapshots[descriptor.ChapterID], nil
}

func (s *textIndexServiceStoreForTest) ReplaceCanonTextChapterForClaim(ctx context.Context, input ReplaceCanonTextChapterInput) (CanonTextChapterReplaceDisposition, error) {
	s.mu.Lock()
	s.events = append(s.events, "replace:"+input.Snapshot.ChapterID)
	s.replacements = append(s.replacements, input)
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	started, wait, release := s.replaceStarted, s.replaceWait[input.Snapshot.ChapterID], s.replaceRelease
	err := s.replaceErrors[input.Snapshot.ChapterID]
	s.mu.Unlock()

	if started != nil {
		started <- input.Snapshot.ChapterID
	}
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil && release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}

	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return CanonTextChapterReplaced, err
}

func (s *textIndexServiceStoreForTest) CompleteCanonTextIndexForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "complete")
	s.completeCalls++
	s.activeAtComplete = s.active
	return nil
}

func (s *textIndexServiceStoreForTest) CancelCanonTextIndexForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject) error {
	return nil
}

func (s *textIndexServiceStoreForTest) FailCanonTextIndexForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject, jobs.Error) error {
	return nil
}

func (s *textIndexServiceStoreForTest) stats() (replacements, maxActive, completeCalls, activeAtComplete int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.replacements), s.maxActive, s.completeCalls, s.activeAtComplete
}

func waitForTextIndexServiceStarts(t *testing.T, started <-chan string, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %d chapter starts", count)
		}
	}
}

func waitForTextIndexServiceResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for text index service")
		return nil
	}
}

func textIndexServiceClaim() jobs.WorkerClaim {
	return jobs.WorkerClaim{ProjectID: textIndexServiceProjectID, JobID: textIndexServiceJobID, ClaimIncarnation: 1}
}

func textIndexServiceSubject() jobs.CanonTextIndexSubject {
	return jobs.CanonTextIndexSubject{Type: jobs.KindCanonTextIndex, ProjectID: textIndexServiceProjectID, SourceView: governance.SourceViewDraft, CatalogRevision: 7}
}
