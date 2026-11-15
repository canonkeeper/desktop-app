// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	layerdomain "canonkeeper/backend/internal/domain/sagalayer"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
)

// Consents read the consent of a saga, nil without one.
type Consents interface {
	Consent(ctx context.Context, projectID string) (*domain.Consent, error)
}

// Policy is the policy of the reading the app gives the open core (specs/2026-11-13-desk-consent.md CN3.1, CN3.7): the
// builders of the cards and of the layer call the model for a saga only while the helper is connected and the consent of
// the saga acts for «Свой ИИ» at the host of the settings now and is allowed; the sample of three chapters reads by its own
// Job, whose every request the gateway checks.
type Policy struct {
	Settings Settings
	Consents Consents
	Marks    UnreadMarks
}

func (policy Policy) ReadingAllowed(ctx context.Context, projectID string, sample bool) (bool, error) {
	if !policy.Settings.Connected(ctx) {
		return false, nil
	}
	if sample {
		return true, nil
	}
	to, ok := targetOf(policy.Settings)
	if !ok {
		return false, nil
	}
	consent, err := policy.Consents.Consent(ctx, projectID)
	if err != nil {
		return false, err
	}
	return consent.Acts(to.source, to.host) && consent.State == domain.StateAllowed, nil
}

// ChaptersAtOnce is «Запросов одновременно» of the author (CN3.5): the builder of the cards takes as many chapters at once, so
// a stop finds no more chapters with a paid card and no pass. The helper of the account reads with one place of its two
// (specs/2026-11-13-desk-account-ai.md AI3.3).
func (policy Policy) ChaptersAtOnce(context.Context) int {
	ai := policy.Settings.AI()
	if ai.Source == aisettings.SourceAccount {
		return 1
	}
	if own := ai.Own; own != nil {
		return own.Advanced.Parallel
	}
	return 0
}

// Unread is a chapter marked «не прочиталась» by its text, with why (CN3.6) and for which helper
// (specs/2026-11-15-desk-reading-rest.md RR1.5): a mark without one does not act.
type Unread struct {
	ID, Hash, Reason, Helper string
}

// The kinds of the marks of the names and of the threads (RR1.2, RR1.3).
const (
	RestNames   = "names"
	RestThreads = "threads"
)

// RestMark is a mark of the names or of the threads of a saga by the text version whose Job marked it (RR1.2, RR1.3): of the
// names the digest of their set, of the threads the stamps of the marked build and the version of the instructions of the
// layer; with why and for which helper.
type RestMark struct {
	Kind, View     string
	Names          string
	Stamps         []layerdomain.Stamp
	LayerVersion   int
	Reason, Helper string
}

// UnreadMarks keep the chapters of a saga marked «не прочиталась» (CN3.6) and its names and threads that did not come together
// (RR1.2, RR1.3); only a live saga is marked, and a new mark of the same kind and text version replaces the old one.
type UnreadMarks interface {
	Unread(ctx context.Context, projectID string, version int) ([]Unread, error)
	MarkUnread(ctx context.Context, projectID, chapterID, hash string, version int, reason, helper string, at time.Time) error
	RestMarks(ctx context.Context, projectID string) ([]RestMark, error)
	MarkRest(ctx context.Context, projectID string, mark RestMark, at time.Time) error
}

// helper is the helper whose marks act for the saga now — the one rule of the marks (RR1.4), read by the builders, the
// catch-up, the status and the estimate: the consent of the saga acts for the source and the host of the settings, allowed or
// paused. Without one no mark of the saga acts — "" —, and all it marked waits as unread.
func (policy Policy) helper(ctx context.Context, projectID string) (string, error) {
	to, ok := targetOf(policy.Settings)
	if !ok {
		return "", nil
	}
	consent, err := policy.Consents.Consent(ctx, projectID)
	if err != nil || !consent.Acts(to.source, to.host) {
		return "", err
	}
	return domain.HelperOf(policy.Settings.AI()), nil
}

// Marked are the chapters marked «не прочиталась» whose marks act now (RR1.4, RR1.5): of their current text — the caller
// compares — for the version of the instructions and the helper of now.
func (policy Policy) Marked(ctx context.Context, projectID string, version int) ([]Unread, error) {
	helper, err := policy.helper(ctx, projectID)
	if err != nil || helper == "" {
		return nil, err
	}
	chapters, err := policy.Marks.Unread(ctx, projectID, version)
	if err != nil {
		return nil, err
	}
	acting := chapters[:0]
	for _, chapter := range chapters {
		if chapter.Helper == helper {
			acting = append(acting, chapter)
		}
	}
	return acting, nil
}

// Unread are the chapters the reading does not take again until their text changes or the author asks to repeat them.
func (policy Policy) Unread(ctx context.Context, projectID string, version int) (map[sagamemoryservice.CardKey]bool, error) {
	chapters, err := policy.Marked(ctx, projectID, version)
	if err != nil {
		return nil, err
	}
	unread := make(map[sagamemoryservice.CardKey]bool, len(chapters))
	for _, chapter := range chapters {
		unread[sagamemoryservice.CardKey{ChapterID: chapter.ID, ContentHash: chapter.Hash}] = true
	}
	return unread, nil
}

// MarkUnread marks a chapter whose card did not come after the paid answers of the model, for the helper of now. The helper
// of the account marks none (specs/2026-11-13-desk-account-ai.md AI3.5): its site counts a text once in 30 days, and its own
// refusals are pauses.
func (policy Policy) MarkUnread(ctx context.Context, projectID string, key sagamemoryservice.CardKey, version int, reason string) error {
	helper := policy.chapterHelper()
	if helper == "" {
		return nil
	}
	return policy.Marks.MarkUnread(ctx, projectID, key.ChapterID, key.ContentHash, version, reason, helper, time.Now())
}

// MarksChapters says MarkUnread marks a chapter for the helper of now: «Свой ИИ» does, the helper of the account does not
// (specs/2026-11-15-desk-reading-no-answer.md NA2.1).
func (policy Policy) MarksChapters(context.Context) bool {
	return policy.chapterHelper() != ""
}

// chapterHelper is the helper a chapter is marked for now, "" when none is: the helper of the account marks no chapter.
func (policy Policy) chapterHelper() string {
	ai := policy.Settings.AI()
	if ai.Source == aisettings.SourceAccount {
		return ""
	}
	return domain.HelperOf(ai)
}

// NamesUnread is why the names of the set of the digest are marked, while the mark acts (RR1.2, RR1.4): the same set for the
// helper of now, marked by the Job of any text version — equal sets do not pay twice.
func (policy Policy) NamesUnread(ctx context.Context, projectID, digest string) (string, error) {
	return policy.restUnread(ctx, projectID, func(mark RestMark) bool {
		return mark.Kind == RestNames && mark.Names == digest
	})
}

// ThreadsUnread is why the build of the threads over the stamps now is marked, while the mark acts (RR1.3, RR1.4): the cards
// now have not passed the threshold of a rebuild from the stamps of the marked build — the rule of the queue of the layer,
// domain.Due —, the instructions of the layer are the same, and so is the helper.
func (policy Policy) ThreadsUnread(ctx context.Context, projectID string, stamps []layerdomain.Stamp) (string, error) {
	return policy.restUnread(ctx, projectID, func(mark RestMark) bool {
		return mark.Kind == RestThreads && mark.LayerVersion == layerdomain.InstructionsVersion && !layerdomain.Due(stamps, mark.Stamps)
	})
}

func (policy Policy) restUnread(ctx context.Context, projectID string, same func(RestMark) bool) (string, error) {
	helper, err := policy.helper(ctx, projectID)
	if err != nil || helper == "" {
		return "", err
	}
	marks, err := policy.Marks.RestMarks(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, mark := range marks {
		if mark.Helper == helper && same(mark) {
			return mark.Reason, nil
		}
	}
	return "", nil
}

// MarkNamesUnread marks the names of a set whose register did not come together, for the helper of now — of either source
// (RR1.2, RR1.5).
func (policy Policy) MarkNamesUnread(ctx context.Context, projectID, view, digest, reason string) error {
	return policy.markRest(ctx, projectID, RestMark{Kind: RestNames, View: view, Names: digest, Reason: reason})
}

// MarkThreadsUnread marks the build of the threads over the stamps of its chapters, for the helper of now (RR1.3, RR1.5).
func (policy Policy) MarkThreadsUnread(ctx context.Context, projectID, view string, stamps []layerdomain.Stamp, reason string) error {
	return policy.markRest(ctx, projectID, RestMark{
		Kind: RestThreads, View: view, Stamps: stamps, LayerVersion: layerdomain.InstructionsVersion, Reason: reason,
	})
}

func (policy Policy) markRest(ctx context.Context, projectID string, mark RestMark) error {
	if mark.Helper = domain.HelperOf(policy.Settings.AI()); mark.Helper == "" {
		return nil
	}
	return policy.Marks.MarkRest(ctx, projectID, mark, time.Now())
}
