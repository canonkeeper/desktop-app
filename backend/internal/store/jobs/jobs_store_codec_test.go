package jobstore

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
)

func TestJobFromSQLJobPreservesWorkerClaim(t *testing.T) {
	const (
		projectID = "11111111-1111-4111-8111-111111111111"
		jobID     = "22222222-2222-4222-8222-222222222222"
	)
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		t.Fatal(err)
	}
	jobUUID, err := storedb.ParseUUID(jobID)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := jobs.EncodeSubject(jobs.KindCanonTextIndex, jobs.CanonTextIndexSubject{
		Type: jobs.KindCanonTextIndex, ProjectID: projectID,
		SourceView: governance.SourceViewDraft, CatalogRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	job, err := JobFromSQLJob(sqlc.Job{
		ID: jobUUID, ProjectID: projectUUID, Kind: string(jobs.KindCanonTextIndex), Status: string(jobs.StatusRunning),
		ProgressJson: []byte(`{}`), SubjectJson: subject, ClaimIncarnation: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := jobs.WorkerClaim{ProjectID: projectID, JobID: jobID, ClaimIncarnation: 7}
	if job.WorkerClaim != want {
		t.Fatalf("WorkerClaim = %#v, want %#v", job.WorkerClaim, want)
	}
}

func TestParseJobErrorOnlyTreatsNilOrEmptyDriverBytesAsAbsent(t *testing.T) {
	for _, data := range [][]byte{nil, {}} {
		jobError, err := parseJobError(data)
		if err != nil || jobError != nil {
			t.Fatalf("parseJobError(%q) = (%#v, %v), want (nil, nil)", data, jobError, err)
		}
	}

	for _, data := range [][]byte{[]byte("{}"), []byte("null"), []byte(" ")} {
		if jobError, err := parseJobError(data); err == nil {
			t.Fatalf("parseJobError(%q) = (%#v, nil), want strict decode error", data, jobError)
		}
	}
}

func TestImportProgressCodecAcceptsOnlyClosedPhaseShapes(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	parsingAt := now.Add(time.Second)
	parsedAt := parsingAt.Add(2 * time.Second)
	savedAt := parsedAt.Add(3 * time.Second)
	chapters, noneSaved, allSaved := 4, 0, 4

	tests := []struct {
		name     string
		status   jobs.Status
		progress jobs.ImportProgress
	}{
		{name: "queued", status: jobs.StatusQueued, progress: jobs.ImportProgress{
			Phase: jobs.ImportPhaseQueued, BytesTransferred: 128, BytesTotal: 128,
			QueuedAt: now, TransferredAt: now,
		}},
		{name: "parsing", status: jobs.StatusRunning, progress: jobs.ImportProgress{
			Phase: jobs.ImportPhaseParsing, BytesTransferred: 128, BytesTotal: 128,
			QueuedAt: now, TransferredAt: now, ParsingStartedAt: &parsingAt,
		}},
		{name: "saving", status: jobs.StatusRunning, progress: jobs.ImportProgress{
			Phase: jobs.ImportPhaseSaving, BytesTransferred: 128, BytesTotal: 128,
			QueuedAt: now, TransferredAt: now, ParsingStartedAt: &parsingAt,
			ParsedChapters: &chapters, TotalChapters: &chapters, ParsedAt: &parsedAt,
			SavingStartedAt: &parsedAt, SavedChapters: &noneSaved,
		}},
		{name: "saved", status: jobs.StatusSucceeded, progress: jobs.ImportProgress{
			Phase: jobs.ImportPhaseSaved, BytesTransferred: 128, BytesTotal: 128,
			QueuedAt: now, TransferredAt: now, ParsingStartedAt: &parsingAt,
			ParsedChapters: &chapters, TotalChapters: &chapters, ParsedAt: &parsedAt,
			SavingStartedAt: &parsedAt, SavedChapters: &allSaved, SavedAt: &savedAt,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := MarshalImportProgress(test.progress, test.status)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := ParseImportProgress(encoded, test.status)
			if err != nil || decoded.Phase != test.progress.Phase {
				t.Fatalf("decoded = %#v, err = %v", decoded, err)
			}
		})
	}
}

func TestImportProgressCodecRejectsMalformedShapes(t *testing.T) {
	for _, raw := range []string{
		`{"phase":"queued","bytesTransferred":10,"bytesTotal":10,"queuedAt":"2026-08-10T12:00:00Z","transferredAt":"2026-08-10T12:00:00Z","unexpected":true}`,
		`{"value":0}`,
		`{"phase":"queued","bytesTransferred":9,"bytesTotal":10,"queuedAt":"2026-08-10T12:00:00Z","transferredAt":"2026-08-10T12:00:00Z"}`,
		`{"phase":"queued","bytesTransferred":10,"bytesTotal":10,"queuedAt":"2026-08-10T12:00:01Z","transferredAt":"2026-08-10T12:00:00Z"}`,
		`{"phase":"saving","bytesTransferred":10,"bytesTotal":10,"queuedAt":"2026-08-10T12:00:00Z","transferredAt":"2026-08-10T12:00:00Z"}`,
		`null`,
	} {
		if progress, err := ParseImportProgress([]byte(raw), jobs.StatusQueued); err == nil {
			t.Fatalf("ParseImportProgress(%s) = %#v, want error", raw, progress)
		}
	}
}

func TestParseJobProgressKeepsResearchChatPhaseClosed(t *testing.T) {
	progress, phase, err := parseJobProgress(
		[]byte(`{"phase":"working"}`), jobs.KindResearchChatTurn, jobs.StatusRunning,
	)
	if err != nil || progress != nil || phase == nil || *phase != jobs.ResearchChatPhaseWorking {
		t.Fatalf("parsed = progress:%v phase:%v err:%v", progress, phase, err)
	}

	progress, phase, err = parseJobProgress(
		[]byte(`{"phase":"waiting"}`), jobs.KindResearchChatTurn, jobs.StatusQueued,
	)
	if err != nil || progress != nil || phase == nil || *phase != jobs.ResearchChatPhaseWaiting {
		t.Fatalf("queued phase = progress:%v phase:%v err:%v", progress, phase, err)
	}

	for _, test := range []struct {
		raw    []byte
		status jobs.Status
	}{
		{raw: []byte(`{}`), status: jobs.StatusQueued},
		{raw: []byte(`{}`), status: jobs.StatusRunning},
		{raw: []byte(`{"phase":"waiting"}`), status: jobs.StatusRunning},
		{raw: []byte(`{"phase":"future"}`), status: jobs.StatusRunning},
		{raw: []byte(`{"value":0.5}`), status: jobs.StatusRunning},
	} {
		if _, _, err := parseJobProgress(test.raw, jobs.KindResearchChatTurn, test.status); err == nil {
			t.Fatalf("parseJobProgress(%s, %s) accepted invalid chat progress", test.raw, test.status)
		}
	}
}
