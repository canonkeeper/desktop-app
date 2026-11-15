// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"

	"canonkeeper/backend/internal/adapters/llmgateway"
	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	readingdomain "canonkeeper/backend/internal/domain/readingconsent"
	sagadomain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	readingrepo "canonkeeper/backend/internal/repository/postgres/readingconsent"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	readingservice "canonkeeper/backend/internal/services/readingconsent"
	sagalayerservice "canonkeeper/backend/internal/services/sagalayer"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
)

// readingSettings are «Свой ИИ» as the reading of the books reads it (specs/2026-11-13-desk-consent.md): the settings, the
// calibration of the last check that measured the model of the reading, and the rule «помощник подключён».
type readingSettings struct {
	ownAI
	// site and account are the site of the account and the link with it (specs/2026-11-13-desk-account-ai.md AI3.4, AI3.7):
	// the host its consent names and the limits of its tier for the reading.
	site    string
	account *accountLink
}

func (own readingSettings) SiteHost() string { return accountdomain.HostOf(own.site) }

// AccountLimits are the characters and the work of the reading of 30 days the app holds as the site told them last: the poll
// of the status neither asks the site nor waits for it — the owner of the link reads the account aside, at most once a minute
// (GetAccount.Held), while a status of a reading of the account is asked —, so a silent site does not hold the status.
func (own readingSettings) AccountLimits(context.Context) (readingservice.AccountLimits, bool) {
	if own.account == nil || !own.account.parts.Linked() {
		return readingservice.AccountLimits{}, false
	}
	told := own.account.parts.Read.Held().Account
	if told == nil || !told.Quota {
		return readingservice.AccountLimits{}, false
	}
	return readingservice.AccountLimits{
		Characters: readingdomain.Limit{Used: told.Reading.Used, Limit: told.Reading.Limit},
		Work:       readingdomain.Limit{Used: told.Work.Used, Limit: told.Work.Limit},
	}, true
}

func (own readingSettings) Calibration() (float64, bool) {
	report, ok := own.settings.Report()
	if !ok {
		return 0, false
	}
	return report.ReadingCalibration(own.settings.AI())
}

var _ readingservice.Settings = readingSettings{}

// sagaToRead is what the published text of a saga has to read, by the rules of the core: the chapters without a card of
// their current text (sagamemory.MissingCards), the names that wait for the model (sagamemory.NamesOf) and the layer
// that is due (sagalayer.Pending); measured, the characters of each chapter by its revision. What did not read or come
// together is left out only while its mark acts — the one rule of the policy of the reading
// (specs/2026-11-15-desk-reading-rest.md RR1.4).
type sagaToRead struct {
	saga    *sagamemoryrepo.Repository
	reading *readingrepo.Repository
	policy  readingservice.Policy
}

func (reading sagaToRead) ToRead(ctx context.Context, projectID string, measured bool) (readingservice.ToRead, error) {
	view := string(governance.SourceViewPublished)
	selection, missing, err := sagamemoryservice.MissingCards(ctx, reading.saga, reading.saga, projectID, view)
	if err != nil {
		return readingservice.ToRead{}, err
	}
	revisions := make([]readingrepo.ChapterRevision, 0, len(selection.Chapters))
	books := map[string]bool{}
	for _, chapter := range selection.Chapters {
		revisions = append(revisions, readingrepo.ChapterRevision{ID: chapter.ID, Revision: chapter.Revision})
		books[chapter.BookID] = true
	}
	characters := map[string]int{}
	if measured {
		if characters, err = reading.reading.Characters(ctx, projectID, revisions); err != nil {
			return readingservice.ToRead{}, err
		}
	}
	marks, err := reading.policy.Marked(ctx, projectID, sagadomain.CurrentCardInstructions.Version)
	if err != nil {
		return readingservice.ToRead{}, err
	}
	marked := make(map[sagamemoryservice.CardKey]readingservice.Unread, len(marks))
	for _, mark := range marks {
		marked[sagamemoryservice.CardKey{ChapterID: mark.ID, ContentHash: mark.Hash}] = mark
	}
	toRead := readingservice.ToRead{Books: len(books)}
	unread := map[string]bool{}
	for _, chapter := range missing.Chapters {
		unread[chapter.ID] = true
		if mark, ok := marked[sagamemoryservice.CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}]; ok {
			toRead.Marked = append(toRead.Marked, mark)
			continue
		}
		toRead.Unread = append(toRead.Unread, readingservice.Chapter{ID: chapter.ID, Hash: chapter.ContentHash, Characters: characters[chapter.ID]})
	}
	for _, chapter := range selection.Chapters {
		if !unread[chapter.ID] {
			toRead.Carded++
			toRead.CardedCharacters += int64(characters[chapter.ID])
		}
	}
	if len(toRead.Unread) == 0 && len(selection.Chapters) > len(toRead.Marked) {
		if toRead.NamesWait, toRead.NamesUnread, err = sagamemoryservice.NamesOf(ctx, reading.saga, reading.policy, projectID, selection); err != nil {
			return readingservice.ToRead{}, err
		}
		if toRead.ThreadsWait, toRead.ThreadsUnread, err = sagalayerservice.Pending(ctx, reading.saga, reading.policy, projectID, view); err != nil {
			return readingservice.ToRead{}, err
		}
	}
	return toRead, nil
}

// Inputs are the characters of the inputs of the card steps of the chapters (specs/2026-11-13-desk-account-ai.md AI3.4): the
// text the card writer gives the model, by the same function — what the site of the account counts.
func (reading sagaToRead) Inputs(ctx context.Context, projectID string, chapters []readingservice.Chapter) (int64, error) {
	if len(chapters) == 0 {
		return 0, nil
	}
	view := string(governance.SourceViewPublished)
	selection, err := reading.saga.Selection(ctx, projectID, view)
	if err != nil {
		return 0, err
	}
	wanted := make(map[string]bool, len(chapters))
	for _, chapter := range chapters {
		wanted[chapter.ID] = true
	}
	chosen := sagamemoryservice.Selection{ProjectID: selection.ProjectID, View: selection.View, Fingerprint: selection.Fingerprint}
	for _, chapter := range selection.Chapters {
		if wanted[chapter.ID] {
			chosen.Chapters = append(chosen.Chapters, chapter)
		}
	}
	texts, err := reading.saga.Chapters(ctx, chosen)
	if err != nil {
		return 0, err
	}
	// The keys of the chapters are those of the whole version, as the builder gives them.
	return sagamemoryservice.CardInputCharacters(selection, texts), nil
}

// gatewayReading is the check of the reading the gateway asks (specs/2026-11-13-desk-consent.md CN3.3): the port the gateway
// declares, answered by the consent of the app.
type gatewayReading struct{ admissions *readingservice.Admissions }

func (reading gatewayReading) Admit(ctx context.Context, request llmgateway.ReadingRequest) (llmgateway.Ticket, string, error) {
	ticket, refusal, err := reading.admissions.Admit(ctx, readingservice.AdmitRequest{
		JobID: request.JobID, Purpose: request.Purpose, Host: request.Host, Model: request.Model,
		RequestBytes: request.RequestBytes, Settings: request.Settings,
	})
	if ticket == nil {
		return nil, refusal, err
	}
	return ticket, "", nil
}

// readingWork queues and cancels the reading of a saga (specs/2026-11-13-desk-consent.md CN3.2): the owners of the queue of
// the core queue it, the standard cancel of the Jobs of its author stops it.
type readingWork struct {
	queue func(ctx context.Context, projectID string) error
	live  interface {
		LiveReadingJobs(ctx context.Context, projectID string) ([]string, error)
	}
	jobs interface {
		CancelJob(ctx context.Context, actor jobs.Actor, jobID string) (jobs.CancelResult, error)
	}
}

func (work readingWork) Queue(ctx context.Context, projectID string) error {
	return work.queue(ctx, projectID)
}

// Cancel asks the standard cancel of every Job of the reading of the saga; one that ended meanwhile is no error.
func (work readingWork) Cancel(ctx context.Context, actorID, projectID string) error {
	ids, err := work.live.LiveReadingJobs(ctx, projectID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, _ = work.jobs.CancelJob(ctx, jobs.Actor{UserID: actorID}, id)
	}
	return nil
}

// sampleCards are the cards the sample read, as the window of the consent shows them (specs/2026-11-13-desk-consent.md
// CN2.4): the place of the chapter in words, the retelling, the events and who is in it — of the chapters whose card is of
// the text the sample read. The place is the one of the published text (memory.ChapterVersion), as every page names it
// (specs/2026-11-14-chapter-place-number.md CP1.1).
type sampleCards struct {
	saga    *sagamemoryrepo.Repository
	memory  *sagamemoryservice.Saga
	reading *readingrepo.Repository
}

// The most events and people a card of the sample shows.
const (
	sampleEvents = 4
	samplePeople = 6
)

func (cards sampleCards) Cards(ctx context.Context, projectID string, chapters []readingservice.Chapter) ([]readingservice.SampleCard, error) {
	version, err := cards.memory.ChapterVersion(ctx, projectID, string(governance.SourceViewPublished))
	if err != nil {
		return nil, err
	}
	selected := make(map[string]*sagadomain.Chapter, len(version.Chapters))
	for i := range version.Chapters {
		selected[version.Chapters[i].ID] = &version.Chapters[i]
	}
	keys := make([]sagamemoryservice.CardKey, 0, len(chapters))
	var revisions []readingrepo.ChapterRevision
	for _, chapter := range chapters {
		keys = append(keys, sagamemoryservice.CardKey{ChapterID: chapter.ID, ContentHash: chapter.Hash})
		if current, ok := selected[chapter.ID]; ok && current.ContentHash == chapter.Hash {
			revisions = append(revisions, readingrepo.ChapterRevision{ID: chapter.ID, Revision: current.Revision})
		}
	}
	found, err := cards.saga.Cards(ctx, projectID, keys, sagadomain.CardParts{Summary: true, Events: true, Mentions: true})
	if err != nil {
		return nil, err
	}
	characters, err := cards.reading.Characters(ctx, projectID, revisions)
	if err != nil {
		return nil, err
	}
	var shown []readingservice.SampleCard
	for _, key := range keys {
		card, ok := found[key]
		if !ok {
			continue
		}
		shownCard := readingservice.SampleCard{ChapterID: key.ChapterID, Summary: card.Summary, Characters: characters[key.ChapterID]}
		if current, ok := selected[key.ChapterID]; ok {
			shownCard.Place = current.Place()
		}
		for _, event := range card.Events {
			if len(shownCard.Events) < sampleEvents && event.Event != "" {
				shownCard.Events = append(shownCard.Events, event.Event)
			}
		}
		for _, person := range card.Characters {
			if len(shownCard.People) < samplePeople && person.Name != "" {
				shownCard.People = append(shownCard.People, person.Name)
			}
		}
		shown = append(shown, shownCard)
	}
	return shown, nil
}

// cancelJob cancels a Job of the local author the standard way.
type cancelJob struct {
	jobs interface {
		CancelJob(ctx context.Context, actor jobs.Actor, jobID string) (jobs.CancelResult, error)
	}
}

func (cancel cancelJob) CancelJob(ctx context.Context, actorID, jobID string) error {
	_, err := cancel.jobs.CancelJob(ctx, jobs.Actor{UserID: actorID}, jobID)
	return err
}
