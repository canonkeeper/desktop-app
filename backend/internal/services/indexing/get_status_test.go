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
