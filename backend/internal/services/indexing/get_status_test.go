package indexing

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/canon"
)

type statusPortFake struct {
	status canon.IndexingStatusResponse
}

func (fake statusPortFake) GetStatus(context.Context, GetStatusInput) (canon.IndexingStatusResponse, error) {
	status := fake.status
	if status.Preparation != nil {
		// The use case changes what it returns, not what the store keeps.
		copied := *status.Preparation
		status.Preparation = &copied
	}
	return status, nil
}

const statusReaderID = "95000000-0000-4000-8000-000000000003"

// specs/2026-11-09-first-session.md, FS5.6: the status promises the letter «Сага готова» only when the store names the
// reader as its recipient and the letters of the build would write to him; a build without letters, an author who is
// not written to and an account that cannot be read are no promise, and the status itself is answered all the same.
func TestTheStatusPromisesOnlyALetterTheBuildWouldWrite(t *testing.T) {
	seconds := 240
	candidate := canon.IndexingStatusResponse{Preparation: &canon.IndexingPreparation{RemainingSeconds: &seconds, LetterPromised: true}}
	input := GetStatusInput{ActorUserID: statusReaderID, ProjectID: readyProjectID}
	cases := []struct {
		name     string
		letters  *readyLettersFake
		promised bool
	}{
		{"an author who is written to", &readyLettersFake{promised: true}, true},
		{"an author who is not", &readyLettersFake{}, false},
		{"an account that cannot be read", &readyLettersFake{promised: true, err: errors.New("database is away")}, false},
		{"a build without letters", nil, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(statusPortFake{status: candidate})
			if test.letters != nil {
				service.WithLetters(test.letters)
			}
			status, err := service.GetStatus(context.Background(), input)
			if err != nil || status.Preparation == nil || status.Preparation.LetterPromised != test.promised ||
				status.Preparation.RemainingSeconds == nil || *status.Preparation.RemainingSeconds != 240 {
				t.Fatalf("GetStatus() = %+v, %v; want promised %v", status.Preparation, err, test.promised)
			}
			if test.letters != nil && (len(test.letters.asked) != 1 || test.letters.asked[0] != statusReaderID) {
				t.Fatalf("asked of %v, want the reader", test.letters.asked)
			}
		})
	}

	// A status the store does not promise in asks nobody.
	letters := &readyLettersFake{promised: true}
	for _, status := range []canon.IndexingStatusResponse{{}, {Preparation: &canon.IndexingPreparation{}}} {
		answered, err := NewService(statusPortFake{status: status}).WithLetters(letters).GetStatus(context.Background(), input)
		if err != nil || len(letters.asked) != 0 || (answered.Preparation != nil && answered.Preparation.LetterPromised) {
			t.Fatalf("GetStatus() = %+v, %v, asked %v", answered.Preparation, err, letters.asked)
		}
	}
}

// statusInputFake keeps what the use case asked the store.
type statusInputFake struct {
	asked *[]GetStatusInput
}

func (fake statusInputFake) GetStatus(_ context.Context, input GetStatusInput) (canon.IndexingStatusResponse, error) {
	*fake.asked = append(*fake.asked, input)
	return canon.IndexingStatusResponse{}, nil
}

type helperFake bool

func (connected helperFake) Connected(context.Context) bool { return bool(connected) }

// specs/2026-11-12-desk-window.md WN3.1: the read of the status asks the build whether its helper is connected; a build
// without the port — the site — asks nothing and the store counts as before; a build whose helper is not connected has the
// store say the preparation waits for it.
func TestTheStatusAsksWhetherTheHelperIsConnected(t *testing.T) {
	input := GetStatusInput{ActorUserID: statusReaderID, ProjectID: readyProjectID}
	for _, test := range []struct {
		name    string
		helper  Helper
		waiting canon.PreparationWaiting
	}{
		{"the site", nil, ""},
		{"a build with its helper", helperFake(true), ""},
		{"a build whose helper is not connected", helperFake(false), canon.PreparationWaitingHelper},
	} {
		t.Run(test.name, func(t *testing.T) {
			var asked []GetStatusInput
			service := NewService(statusInputFake{asked: &asked})
			if test.helper != nil {
				service.WithHelper(test.helper)
			}
			if _, err := service.GetStatus(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if len(asked) != 1 || asked[0].Waiting != test.waiting || asked[0].ProjectID != input.ProjectID {
				t.Fatalf("the store was asked %+v, want waiting %q", asked, test.waiting)
			}
		})
	}
}

// readingFake is the reading of the books of a saga as the app says it: what it waits for and its state by the counts of
// the store.
type readingFake struct {
	waiting canon.PreparationWaiting
	asked   *[]string
	counts  *canon.IndexingPreparation
}

func (reading readingFake) Waiting(_ context.Context, projectID string) (canon.PreparationWaiting, error) {
	*reading.asked = append(*reading.asked, projectID)
	return reading.waiting, nil
}

func (reading readingFake) State(_ context.Context, _ string, counts canon.IndexingPreparation) (*canon.IndexingReading, error) {
	*reading.counts = counts
	return &canon.IndexingReading{State: canon.ReadingPaused, ChaptersRead: counts.ChaptersRead, ChaptersTotal: counts.ChaptersTotal}, nil
}

// counted is the store of the status that counts the reading of a saga of 600 chapters, 212 of them read.
type counted struct{ asked *[]GetStatusInput }

func (fake counted) GetStatus(_ context.Context, input GetStatusInput) (canon.IndexingStatusResponse, error) {
	*fake.asked = append(*fake.asked, input)
	return canon.IndexingStatusResponse{Counts: &canon.IndexingPreparation{ChaptersRead: 212, ChaptersTotal: 600}}, nil
}

// specs/2026-11-13-desk-gateway.md GW4.3; specs/2026-11-13-desk-consent.md CN3.8: a build that asks the author — the app —
// says what the reading of a saga waits for by one port — its consent, or its pause — and the state of the reading next to
// the counts of the store; a helper not connected comes first and has no state; a build without the port — the site — asks
// nothing and answers as before.
func TestTheStatusSaysTheReadingByOnePort(t *testing.T) {
	input := GetStatusInput{ActorUserID: statusReaderID, ProjectID: readyProjectID}
	for _, test := range []struct {
		name      string
		helper    Helper
		reading   *canon.PreparationWaiting
		waiting   canon.PreparationWaiting
		askedSaga bool
		state     bool
	}{
		{"the site", nil, nil, "", false, false},
		{"no consent yet", helperFake(true), new(canon.PreparationWaitingConsent), canon.PreparationWaitingConsent, true, true},
		{"paused", helperFake(true), new(canon.PreparationWaitingPaused), canon.PreparationWaitingPaused, true, true},
		{"the consent given", helperFake(true), new(canon.PreparationWaiting("")), "", true, true},
		{"no consent and no helper", helperFake(false), new(canon.PreparationWaitingConsent), canon.PreparationWaitingHelper, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var asked []GetStatusInput
			var sagas []string
			var counts canon.IndexingPreparation
			service := NewService(counted{asked: &asked})
			if test.helper != nil {
				service.WithHelper(test.helper)
			}
			if test.reading != nil {
				service.WithReading(readingFake{waiting: *test.reading, asked: &sagas, counts: &counts})
			}
			status, err := service.GetStatus(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if len(asked) != 1 || asked[0].Waiting != test.waiting || (len(sagas) == 1) != test.askedSaga ||
				test.askedSaga && sagas[0] != readyProjectID {
				t.Fatalf("the store was asked %+v, the reading of %v; want waiting %q", asked, sagas, test.waiting)
			}
			if (status.Reading != nil) != test.state || test.state && (counts.ChaptersRead != 212 || status.Reading.ChaptersTotal != 600) {
				t.Fatalf("the state of the reading = %+v by %+v", status.Reading, counts)
			}
		})
	}
}
