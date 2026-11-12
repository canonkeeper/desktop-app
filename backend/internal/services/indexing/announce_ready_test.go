package indexing

import (
	"context"
	"errors"
	"testing"
	"time"

	indexingdomain "canonkeeper/backend/internal/domain/indexing"
)

const readyProjectID = "95000000-0000-4000-8000-000000000001"

type readyWavesFake struct {
	wave   indexingdomain.ReadyWave
	closed bool
	err    error
	asked  []string
}

func (fake *readyWavesFake) CloseWave(_ context.Context, projectID string) (indexingdomain.ReadyWave, bool, error) {
	fake.asked = append(fake.asked, projectID)
	return fake.wave, fake.closed, fake.err
}

type readyLettersFake struct {
	sent     []indexingdomain.ReadyWave
	err      error
	promised bool
	asked    []string
}

func (fake *readyLettersFake) Send(_ context.Context, wave indexingdomain.ReadyWave) error {
	fake.sent = append(fake.sent, wave)
	return fake.err
}

func (fake *readyLettersFake) Promised(_ context.Context, userID string) (bool, error) {
	fake.asked = append(fake.asked, userID)
	return fake.promised, fake.err
}

func readyWaveOf(preparation time.Duration) indexingdomain.ReadyWave {
	started := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	return indexingdomain.ReadyWave{
		ProjectID: readyProjectID, ProjectName: "Сага", RecipientUserID: "95000000-0000-4000-8000-000000000002",
		StartedAt: started, ReadyAt: started.Add(preparation), Books: 2, Chapters: 40,
	}
}

// specs/2026-11-09-first-session.md, FS2.1: the letter is of a wave this call closed after a preparation longer than
// two minutes; a short one, a wave that stays open and a build without letters write nothing.
func TestTheLetterIsOfAWaveClosedAfterALongPreparation(t *testing.T) {
	cases := []struct {
		name        string
		closed      bool
		preparation time.Duration
		letters     bool
		sent        int
	}{
		{"a long preparation", true, indexingdomain.ReadyLetterAfter + time.Second, true, 1},
		{"exactly two minutes", true, indexingdomain.ReadyLetterAfter, true, 0},
		{"a short preparation", true, 30 * time.Second, true, 0},
		{"a wave that stays open", false, time.Hour, true, 0},
		{"nobody is written to", true, time.Hour, false, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			waves := &readyWavesFake{wave: readyWaveOf(test.preparation), closed: test.closed}
			letters := &readyLettersFake{}
			var port ReadyLetters
			if test.letters {
				port = letters
			}
			NewAnnounceReady(waves, port, func(_ string, stage string, err error) {
				t.Errorf("failure %s: %v", stage, err)
			}).Execute(context.Background(), readyProjectID)
			if len(waves.asked) != 1 || waves.asked[0] != readyProjectID {
				t.Fatalf("asked %v", waves.asked)
			}
			if len(letters.sent) != test.sent {
				t.Fatalf("letters = %+v, want %d", letters.sent, test.sent)
			}
			if test.sent == 1 && letters.sent[0] != waves.wave {
				t.Fatalf("letter of %+v, want %+v", letters.sent[0], waves.wave)
			}
		})
	}
}

// FS1.3, FS2.5: whatever fails is logged with its stage and fails nothing; a stopped Job reports nothing, and a letter
// that failed is not sent again — the wave is closed already.
func TestAFailedAnnouncementIsLoggedOnce(t *testing.T) {
	var failures []string
	log := func(projectID, stage string, err error) {
		failures = append(failures, projectID+" "+stage+" "+err.Error())
	}

	broken := &readyWavesFake{err: errors.New("database is away")}
	letters := &readyLettersFake{}
	NewAnnounceReady(broken, letters, log).Execute(context.Background(), readyProjectID)
	if len(failures) != 1 || failures[0] != readyProjectID+" close database is away" || len(letters.sent) != 0 {
		t.Fatalf("failures = %v, letters %+v", failures, letters.sent)
	}

	stopped, stop := context.WithCancel(context.Background())
	stop()
	failures = nil
	NewAnnounceReady(broken, letters, log).Execute(stopped, readyProjectID)
	if len(failures) != 0 {
		t.Fatalf("a stopped Job reported %v", failures)
	}

	waves := &readyWavesFake{wave: readyWaveOf(time.Hour), closed: true}
	letters = &readyLettersFake{err: errors.New("queue is full")}
	NewAnnounceReady(waves, letters, log).Execute(context.Background(), readyProjectID)
	if len(failures) != 1 || failures[0] != readyProjectID+" letter queue is full" || len(letters.sent) != 1 {
		t.Fatalf("failures = %v, letters %+v", failures, letters.sent)
	}
	// A nil log is allowed.
	NewAnnounceReady(broken, nil, nil).Execute(context.Background(), readyProjectID)
}
