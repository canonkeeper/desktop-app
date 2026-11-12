package indexing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/canon"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
)

// unreadSnapshot is a saga whose two books are imported and indexed while nobody reads them: no cards Job, no card, the
// wave open since the import.
func unreadSnapshot(waiting canon.PreparationWaiting) statusSnapshot {
	return statusSnapshot{
		role: projects.RoleOwner,
		views: map[governance.SourceView]statusViewSnapshot{
			governance.SourceViewPublished: {revision: 2, books: []statusBookSnapshot{
				{id: "book-1", chapters: 12, indexed: 12},
				{id: "book-2", chapters: 8, indexed: 8},
			}},
		},
		jobs:    []jobs.Job{statusTextJob("text", jobs.StatusSucceeded, governance.SourceViewPublished, 2, statusTestTime)},
		wave:    &statusWave{startedAt: statusTestTime, now: statusTestTime.Add(time.Hour), recipient: true},
		waiting: waiting,
	}
}

// specs/2026-11-12-desk-window.md WN3.1: while the helper of the build is not connected the status counts the reading of
// the saga — what is read, what is to read — and says what it waits for: the helper; no time and no letter are promised,
// for nobody reads. A build with its helper — the site — answers as before: the text nobody reads is partial, and no
// field `waiting` is in its answer.
func TestTheStatusSaysThePreparationWaitsForTheHelper(t *testing.T) {
	waiting := statusOf(t, unreadSnapshot(canon.PreparationWaitingHelper))
	preparation := waiting.Preparation
	if preparation == nil || preparation.Waiting != canon.PreparationWaitingHelper || preparation.ChaptersRead != 0 ||
		preparation.ChaptersTotal != 20 || preparation.BooksTotal != 2 || preparation.RemainingSeconds != nil || preparation.LetterPromised ||
		!preparation.Askable || preparation.CurrentBookID == nil || *preparation.CurrentBookID != "book-1" {
		t.Fatalf("the preparation waiting for the helper = %+v", preparation)
	}
	if body, _ := json.Marshal(waiting); !strings.Contains(string(body), `"waiting":"helper"`) {
		t.Fatalf("the answer = %s", body)
	}

	// A new book still parsed is waited for the same way, before any chapter is counted.
	parsing := unreadSnapshot(canon.PreparationWaitingHelper)
	parsing.views = map[governance.SourceView]statusViewSnapshot{governance.SourceViewPublished: {revision: 1}}
	parsing.wave.importing = true
	if parsed := statusOf(t, parsing).Preparation; parsed == nil || parsed.Waiting != canon.PreparationWaitingHelper || parsed.CurrentBookID != nil {
		t.Fatalf("a new book still parsed = %+v", parsed)
	}

	// A saga read through before has nothing to wait for.
	read := unreadSnapshot(canon.PreparationWaitingHelper)
	read.views[governance.SourceViewPublished].books[0].carded = 12
	read.views[governance.SourceViewPublished].books[1].carded = 8
	if ready := statusOf(t, read); ready.Preparation != nil {
		t.Fatalf("a saga read through = %+v", ready.Preparation)
	}

	site := statusOf(t, unreadSnapshot(""))
	if site.Preparation != nil || site.Published.Context == nil || site.Published.Context.State != canon.IndexingStatePartial {
		t.Fatalf("the site's status of a text nobody reads = %+v, %+v", site.Preparation, site.Published.Context)
	}
	running := waveSnapshot(&statusWave{startedAt: statusTestTime, now: statusTestTime.Add(4 * time.Minute), recentCards: 5})
	if body, _ := json.Marshal(statusOf(t, running)); strings.Contains(string(body), "waiting") {
		t.Fatalf("the site's answer names waiting: %s", body)
	}
}
