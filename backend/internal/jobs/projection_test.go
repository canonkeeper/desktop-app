package jobs

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/httpapi"
)

func TestJobProjectionMapsEveryInternalStatus(t *testing.T) {
	cases := map[Status]PublicStatus{
		StatusQueued:    PublicStatusQueued,
		StatusRunning:   PublicStatusRunning,
		StatusSucceeded: PublicStatusSucceeded,
		StatusFailed:    PublicStatusFailed,
		StatusExpired:   PublicStatusFailed,
		StatusCanceling: PublicStatusCanceling,
		StatusCanceled:  PublicStatusCanceled,
	}
	for internal, want := range cases {
		got, err := ToPublicStatus(internal)
		if err != nil || got != want {
			t.Fatalf("ToPublicStatus(%q) = %q, %v; want %q", internal, got, err, want)
		}
	}
	if _, err := ToPublicStatus(Status("future")); err == nil {
		t.Fatal("ToPublicStatus accepted an unknown state")
	}
}

func TestJobProjectionKeepsCancelingDistinctFromCanceled(t *testing.T) {
	canceling, err := ToPublicStatus(StatusCanceling)
	if err != nil || canceling != PublicStatus("canceling") {
		t.Fatalf("canceling projection = %q, %v; want canceling", canceling, err)
	}
	canceled, err := ToPublicStatus(StatusCanceled)
	if err != nil || canceled != PublicStatusCanceled || canceled == canceling {
		t.Fatalf("canceled projection = %q, %v; want distinct canceled", canceled, err)
	}
}

func TestJobContractExposesOneTimelineLink(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	working := ResearchChatPhaseWorking
	response, err := jobResponseFromJob(Job{
		ID: testTurnID, ProjectID: testProjectID, Kind: KindResearchChatTurn,
		Status: StatusRunning, Phase: &working,
		Subject:   validResearchChatSubject(),
		CreatedAt: now, UpdatedAt: now,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	link := httpapi.AgentTimelineLink(testProjectID, testTurnID)
	if response.Links.Timeline == nil || link == nil || *response.Links.Timeline != *link {
		t.Fatalf("timeline link = %#v", response.Links.Timeline)
	}
}

func TestJobProjectionCarriesOnlyActiveResearchChatPhase(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	waiting := ResearchChatPhaseWaiting
	preparing := ResearchChatPhasePreparingContext
	working := ResearchChatPhaseWorking
	for _, test := range []struct {
		name   string
		status Status
		phase  *ResearchChatPhase
		want   *ResearchChatPhase
	}{
		{name: "queued", status: StatusQueued, phase: &waiting, want: &waiting},
		{name: "running_preparing", status: StatusRunning, phase: &preparing, want: &preparing},
		{name: "running_working", status: StatusRunning, phase: &working, want: &working},
		{name: "terminal", status: StatusSucceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := jobResponseFromJob(Job{
				ID: testTurnID, ProjectID: testProjectID, Kind: KindResearchChatTurn,
				Status: test.status, Phase: test.phase, Subject: validResearchChatSubject(),
				CreatedAt: now, UpdatedAt: now,
			}, "")
			if err != nil || !reflect.DeepEqual(response.Phase, test.want) || response.Progress != nil {
				t.Fatalf("response = %#v, err = %v", response, err)
			}
		})
	}
}

func TestJobProjectionRejectsMissingOrStaleResearchChatPhase(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	waiting := ResearchChatPhaseWaiting
	for _, test := range []struct {
		name   string
		status Status
		phase  *ResearchChatPhase
	}{
		{name: "queued_without_phase", status: StatusQueued},
		{name: "running_without_phase", status: StatusRunning},
		{name: "running_with_waiting_phase", status: StatusRunning, phase: &waiting},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := jobResponseFromJob(Job{
				ID: testTurnID, ProjectID: testProjectID, Kind: KindResearchChatTurn,
				Status: test.status, Phase: test.phase, Subject: validResearchChatSubject(),
				CreatedAt: now, UpdatedAt: now,
			}, "")
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("error = %v, want conflict", err)
			}
		})
	}
}

func TestJobResponseUsesClosedSubjectAndResult(t *testing.T) {
	progress := 1.0
	job := Job{
		ID:            "10000000-0000-4000-8000-000000000007",
		ProjectID:     testProjectID,
		Kind:          KindImport,
		Status:        StatusSucceeded,
		Progress:      &progress,
		Subject:       ImportSubject{Type: KindImport, ID: testProjectID, ProjectID: testProjectID},
		Result:        ImportJobResult{Type: KindImport, BookID: testBookID, ChapterCount: 2, ParagraphCount: 3, ImportMode: "new_book", SourceFileName: "book.docx"},
		CreatorUserID: "10000000-0000-4000-8000-000000000008",
		CreatedAt:     time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 7, 20, 10, 1, 0, 0, time.UTC),
	}
	response, err := jobResponseFromJob(job, "")
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != PublicStatusSucceeded {
		t.Fatalf("status = %q", response.Status)
	}
	if !reflect.DeepEqual(response.Subject, job.Subject) || !reflect.DeepEqual(response.Result, job.Result) {
		t.Fatalf("response = %#v", response)
	}
}

// specs/2026-10-27-update-book-from-file.md R6.3: the places «было → стало» of a preview quote the file of its
// creator; another member reads the preview without them, and a preview belongs to update_preview only.
func TestJobResponseShowsPreviewPlacesToItsCreatorOnly(t *testing.T) {
	count := func(value int) *int { return &value }
	preview := ImportJobResult{
		Type: KindImport, BookID: testBookID, ChapterCount: 2, ParagraphCount: 3, ImportMode: "update_preview", SourceFileName: "book.docx",
		UnchangedChapters: count(0), ChangedChapters: count(1), AddedChapters: count(0), RemovedChapters: count(1),
		PunctuationChapters: count(0), RenamedChapters: count(1),
		Preview: &ImportPreview{NewCards: 1, Chapters: []ImportPreviewChapter{
			{Order: 1, Title: "Глава 1. Мост", PreviousTitle: "Глава 1. Переправа", Change: ImportPreviewWords,
				Places: []ImportPreviewPlace{{Before: "…из пятнадцати человек…", After: "…из тринадцати человек…"}}},
			{Order: 2, Title: "Глава 2", Change: ImportPreviewRemoved},
		}},
	}
	creator := "10000000-0000-4000-8000-000000000008"
	job := Job{
		ID: "10000000-0000-4000-8000-000000000007", ProjectID: testProjectID, Kind: KindImport, Status: StatusSucceeded,
		Subject: ImportSubject{Type: KindImport, ID: testProjectID, ProjectID: testProjectID, BookID: testBookID, Preview: true},
		Result:  preview, CreatorUserID: creator,
		CreatedAt: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 11, 5, 10, 1, 0, 0, time.UTC),
	}
	own, err := jobResponseForViewer(job, "", creator)
	if err != nil || !reflect.DeepEqual(own.Result, preview) {
		t.Fatalf("creator reads %#v, %v; want the whole preview", own.Result, err)
	}
	other, err := jobResponseForViewer(job, "", "10000000-0000-4000-8000-000000000009")
	if err != nil {
		t.Fatal(err)
	}
	read, ok := other.Result.(ImportJobResult)
	if !ok || read.Preview == nil || len(read.Preview.Chapters) != 2 || read.Preview.Chapters[0].Places != nil ||
		read.Preview.Chapters[0].PreviousTitle != "Глава 1. Переправа" || preview.Preview.Chapters[0].Places == nil {
		t.Fatalf("another member reads %#v, want the chapters without their places", other.Result)
	}
	if anonymous, err := jobResponseFromJob(job, ""); err != nil || anonymous.Result.(ImportJobResult).Preview.Chapters[0].Places != nil {
		t.Fatalf("a projection without a viewer = %#v, %v; want no places", anonymous.Result, err)
	}
	misplaced := preview
	misplaced.ImportMode = "update_book"
	if validateResult(KindImport, misplaced) == nil {
		t.Fatal("an update_book result with a preview is valid")
	}
	missing := preview
	missing.Preview = nil
	if validateResult(KindImport, missing) == nil {
		t.Fatal("an update_preview result without its preview is valid")
	}
}

func TestJobResponseRejectsSubjectResultMismatch(t *testing.T) {
	job := Job{
		ID:        "10000000-0000-4000-8000-000000000007",
		ProjectID: testProjectID,
		Kind:      KindImport,
		Status:    StatusSucceeded,
		Subject:   ImportSubject{Type: KindImport, ID: testProjectID, ProjectID: testProjectID},
		Result:    ChapterCardsResult{Type: KindChapterCards, SourceView: governance.SourceViewDraft, Chapters: 1, Built: 1},
		CreatedAt: time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 20, 10, 1, 0, 0, time.UTC),
	}
	if _, err := jobResponseFromJob(job, ""); err == nil {
		t.Fatal("jobResponseFromJob accepted a mismatched result")
	}
}

func TestJobResponseHidesStoredResultUntilTerminal(t *testing.T) {
	progress := 0.25
	for _, status := range []Status{StatusQueued, StatusRunning, StatusCanceling} {
		t.Run(string(status), func(t *testing.T) {
			job := Job{
				ID:        "10000000-0000-4000-8000-000000000007",
				ProjectID: testProjectID,
				Kind:      KindChapterCards,
				Status:    status,
				Progress:  &progress,
				Subject:   ChapterCardsSubject{Type: KindChapterCards, ProjectID: testProjectID, SourceView: governance.SourceViewDraft},
				Result:    ChapterCardsResult{Type: KindChapterCards, SourceView: governance.SourceViewDraft, Chapters: 2, Built: 1},
				CanCancel: true,
				CreatedAt: time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 7, 20, 10, 1, 0, 0, time.UTC),
			}

			response, err := PublicJobResponseFromJob(job, "")
			if err != nil {
				t.Fatal(err)
			}
			if response.Result != nil {
				t.Fatalf("nonterminal result = %#v, want nil", response.Result)
			}
			wantCanCancel := status != StatusCanceling
			if response.CanCancel != wantCanCancel || (response.Links.Cancel != nil) != wantCanCancel {
				t.Fatalf("cancel projection = %v/%v, want enabled %v", response.CanCancel, response.Links.Cancel, wantCanCancel)
			}
		})
	}
}

func TestJobResponsePreservesValidatedDottedErrorCode(t *testing.T) {
	jobError := Error{Code: "worker.handler_failed", Message: "The background job failed."}
	job := Job{
		ID:        "10000000-0000-4000-8000-000000000007",
		ProjectID: testProjectID,
		Kind:      KindChapterCards,
		Status:    StatusFailed,
		Subject:   ChapterCardsSubject{Type: KindChapterCards, ProjectID: testProjectID, SourceView: governance.SourceViewDraft},
		Error:     &jobError,
		CanCancel: true,
		CreatedAt: time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 20, 10, 1, 0, 0, time.UTC),
	}

	const requestID = "00000000-0000-4000-8000-000000000903"
	response, err := PublicJobResponseFromJob(job, requestID)
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.Error.(httpapi.Problem)
	if !ok || problem.Code != jobError.Code || problem.RequestID != requestID {
		t.Fatalf("projected error = %#v, want exact code %q and requestId %q", response.Error, jobError.Code, requestID)
	}
	if response.CanCancel || response.Links.Cancel != nil {
		t.Fatalf("terminal cancel projection = %v/%v, want disabled", response.CanCancel, response.Links.Cancel)
	}
}

func TestJobResponseFailsClosedForErrorWithoutRequestID(t *testing.T) {
	jobError := Error{Code: "worker.handler_failed", Message: "The background job failed."}
	job := Job{
		ID:        "10000000-0000-4000-8000-000000000007",
		ProjectID: testProjectID,
		Kind:      KindChapterCards,
		Status:    StatusFailed,
		Subject:   ChapterCardsSubject{Type: KindChapterCards, ProjectID: testProjectID, SourceView: governance.SourceViewDraft},
		Error:     &jobError,
		CreatedAt: time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 20, 10, 1, 0, 0, time.UTC),
	}

	if _, err := PublicJobResponseFromJob(job, ""); err == nil {
		t.Fatal("PublicJobResponseFromJob accepted a public error without a request ID")
	}
}

func TestJobStartResponseKeepsSupportCodeOfCreatingRequest(t *testing.T) {
	jobError := Error{Code: "worker.handler_failed", Message: "The background job failed."}
	job := Job{
		ID:               "10000000-0000-4000-8000-000000000007",
		ProjectID:        testProjectID,
		Kind:             KindChapterCards,
		Status:           StatusFailed,
		Subject:          ChapterCardsSubject{Type: KindChapterCards, ProjectID: testProjectID, SourceView: governance.SourceViewDraft},
		Error:            &jobError,
		CreatorRequestID: "00000000-0000-4000-8000-000000000905",
		CreatedAt:        time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 7, 20, 10, 1, 0, 0, time.UTC),
	}

	response, err := PublicJobResponseFromJob(job, "00000000-0000-4000-8000-000000000906")
	if err != nil {
		t.Fatal(err)
	}
	if problem, ok := response.Error.(httpapi.Problem); !ok || problem.RequestID != job.CreatorRequestID {
		t.Fatalf("projected error = %#v, want support code of the creating request %q", response.Error, job.CreatorRequestID)
	}
}

// R10.3: only a waiting chat turn carries its place in the fair queue.
func TestJobProjectionCarriesQueuePositionOnlyForWaitingTurn(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	waiting := ResearchChatPhaseWaiting
	preparing := ResearchChatPhasePreparingContext
	place := int64(2)
	queued, err := jobResponseFromJob(Job{
		ID: testTurnID, ProjectID: testProjectID, Kind: KindResearchChatTurn, Status: StatusQueued,
		Phase: &waiting, Subject: validResearchChatSubject(), QueuePosition: &place, CreatedAt: now, UpdatedAt: now,
	}, "")
	if err != nil || queued.QueuePosition == nil || *queued.QueuePosition != 2 {
		t.Fatalf("queued response = %#v, err = %v", queued, err)
	}
	running, err := jobResponseFromJob(Job{
		ID: testTurnID, ProjectID: testProjectID, Kind: KindResearchChatTurn, Status: StatusRunning,
		Phase: &preparing, Subject: validResearchChatSubject(), QueuePosition: &place, CreatedAt: now, UpdatedAt: now,
	}, "")
	if err != nil || running.QueuePosition != nil {
		t.Fatalf("running response = %#v, err = %v", running, err)
	}
}

// specs/2026-11-01-writer-assistant-index.md, UC6: the job API names the kind of work of a chapter_draft
// Job, never the author's text, what it wrote or the chat it belongs to.
func TestChapterDraftJobResponseKeepsTheTextOut(t *testing.T) {
	const secret = "тайный-текст"
	job := Job{
		ID: testChapterID, ProjectID: testProjectID, Kind: KindChapterDraft, Status: StatusSucceeded,
		Subject: ChapterDraftSubject{
			Type: KindChapterDraft, ProjectID: testProjectID, DraftID: testChapterID, Mode: ChapterDraftSelection, Revision: 3,
			Place: &ChapterDraftPlace{Quote: secret}, Instruction: secret,
		},
		Result: ChapterDraftResult{
			Type: KindChapterDraft, Mode: ChapterDraftSelection, Phase: ChapterDraftPhaseNotes, Chapters: []ChapterDraftSource{{ChapterID: testChapterID, Revision: 1}},
			SceneRuns: []ChapterDraftRun{{Scene: 1, Text: secret}},
		},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	response, err := PublicJobResponseFromJob(job, "request")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, secret) || strings.Contains(text, testChatID) || strings.Contains(text, testTurnID) || strings.Contains(text, "chapterId") {
		t.Fatalf("the job API shows what the Job holds: %s", text)
	}
	if !strings.Contains(text, `"subject":{"type":"chapter_draft","projectId":"`+testProjectID+`","mode":"selection"}`) ||
		!strings.Contains(text, `"result":{"type":"chapter_draft","mode":"selection"}`) {
		t.Fatalf("job response = %s", text)
	}
}

// The feed of the voice lists the Jobs of a project without a filter, and one finished drawing answered 500 for the whole
// list: the contract has its result, the projection did not.
func TestAFinishedDrawingProjectsIntoTheJobList(t *testing.T) {
	const projectID, jobID = "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	job := Job{
		ID: jobID, ProjectID: projectID, Kind: KindIllustration, Status: StatusSucceeded,
		Subject: IllustrationSubject{Type: KindIllustration, ProjectID: projectID, ImageID: "00000000-0000-4000-8000-000000000003"},
		Result:  IllustrationResult{Type: KindIllustration, ImageNumber: 7},
	}
	response, err := jobResponseFromJob(job, jobID)
	if err != nil {
		t.Fatalf("jobResponseFromJob: %v", err)
	}
	if response.Result != (IllustrationResult{Type: KindIllustration, ImageNumber: 7}) {
		t.Fatalf("result = %#v", response.Result)
	}
}
