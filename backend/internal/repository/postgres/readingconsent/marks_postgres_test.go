// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/modeljournal"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	layerdomain "canonkeeper/backend/internal/domain/sagalayer"
	sagamemorydomain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/readingconsent"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"
)

// heldSettings are the settings of the helper of the test, changed between the steps as the author changes them.
type heldSettings struct{ ai *aisettings.Settings }

func (settings heldSettings) AI() aisettings.Settings        { return *settings.ai }
func (settings heldSettings) Calibration() (float64, bool)   { return 0, false }
func (settings heldSettings) Connected(context.Context) bool { return true }
func (heldSettings) SiteHost() string                        { return "site.example.test" }
func (heldSettings) AccountLimits(context.Context) (service.AccountLimits, bool) {
	return service.AccountLimits{}, false
}

// helperOf is «Свой ИИ» at a preset with the model that reads the books and the limit of an answer.
func helperOf(t *testing.T, preset, reader string, limit int) aisettings.Settings {
	t.Helper()
	chat := "north-chat"
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{
		Preset: preset, ChatModel: &chat, CardsModel: &reader, Advanced: aisettings.AdvancedDraft{MaxOutput: &limit},
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func stampsOfThree(hashes ...string) []layerdomain.Stamp {
	stamps := make([]layerdomain.Stamp, len(hashes))
	for i, hash := range hashes {
		stamps[i] = layerdomain.Stamp{Chapter: []string{"c1", "c2", "c3"}[i], Hash: hash, CardVersion: sagamemorydomain.CardVersion}
	}
	return stamps
}

// specs/2026-11-15-desk-reading-rest.md RR1.4, RR1.5, RR1.6: a mark acts only while the consent of the saga acts for the host
// now — allowed or paused —, for the helper of the failure and the same content: another model of the reading, another
// limit of an answer, another host or no consent — the mark does not act. The names are kept by the text version of the Job
// and act for any version whose set is the same; a new failure of the same version replaces the mark; a saga that is not
// there is not marked; the chapters go by the same rule, and a chapter mark without a helper does not act.
func TestAMarkActsOnlyForTheHelperOfTheFailure(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_marks")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ai := helperOf(t, "deepseek", "deepseek-reasoner", 32_000)
	policy := service.Policy{Settings: heldSettings{ai: &ai}, Consents: repository, Marks: repository}
	names := func(digest string) string {
		t.Helper()
		reason, err := policy.NamesUnread(ctx, consentSaga, digest)
		if err != nil {
			t.Fatal(err)
		}
		return reason
	}
	threads := func(stamps []layerdomain.Stamp) string {
		t.Helper()
		reason, err := policy.ThreadsUnread(ctx, consentSaga, stamps)
		if err != nil {
			t.Fatal(err)
		}
		return reason
	}
	chapter := sagamemoryservice.CardKey{ChapterID: "94000000-0000-4000-8000-0000000000c1", ContentHash: "hash-1"}
	version := sagamemorydomain.CurrentCardInstructions.Version
	chapters := func() map[sagamemoryservice.CardKey]bool {
		t.Helper()
		unread, err := policy.Unread(ctx, consentSaga, version)
		if err != nil {
			t.Fatal(err)
		}
		return unread
	}
	if err := policy.MarkNamesUnread(ctx, consentSaga, "published", "set-a", sagamemorydomain.UnreadUnreadable); err != nil {
		t.Fatal(err)
	}
	if err := policy.MarkThreadsUnread(ctx, consentSaga, "published", stampsOfThree("h1", "h2", "h3"), sagamemorydomain.UnreadRefused); err != nil {
		t.Fatal(err)
	}
	if err := policy.MarkUnread(ctx, consentSaga, chapter, version, sagamemorydomain.UnreadCutAnswer); err != nil {
		t.Fatal(err)
	}
	if names("set-a") != "" || threads(stampsOfThree("h1", "h2", "h3")) != "" || len(chapters()) != 0 {
		t.Fatal("marks act without a consent")
	}
	give := func(change domain.Change, host string) {
		t.Helper()
		if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
			return domain.Set(current, change, now, domain.SourceOwn, host, domain.UnitTokens)
		}, false); err != nil {
			t.Fatal(err)
		}
	}
	give(domain.Change{State: domain.StateAllowed}, "api.deepseek.com")
	// The new count forgot the marks written before the consent: they are written again under it.
	if err := policy.MarkNamesUnread(ctx, consentSaga, "published", "set-a", sagamemorydomain.UnreadUnreadable); err != nil {
		t.Fatal(err)
	}
	if err := policy.MarkThreadsUnread(ctx, consentSaga, "published", stampsOfThree("h1", "h2", "h3"), sagamemorydomain.UnreadRefused); err != nil {
		t.Fatal(err)
	}
	if err := policy.MarkUnread(ctx, consentSaga, chapter, version, sagamemorydomain.UnreadCutAnswer); err != nil {
		t.Fatal(err)
	}
	if names("set-a") != sagamemorydomain.UnreadUnreadable || names("set-b") != "" || !chapters()[chapter] {
		t.Fatalf("the marks under the consent: names %q, chapters %v", names("set-a"), chapters())
	}
	if threads(stampsOfThree("h1", "h2", "h3")) != sagamemorydomain.UnreadRefused || threads(stampsOfThree("h1-edited", "h2", "h3")) != sagamemorydomain.UnreadRefused ||
		threads(stampsOfThree("h1-edited", "h2-edited", "h3")) != "" {
		t.Fatal("the mark of the threads does not follow the threshold of a rebuild")
	}
	give(domain.Change{State: domain.StatePaused}, "api.deepseek.com")
	if names("set-a") == "" || len(chapters()) != 1 {
		t.Fatal("a paused consent let the marks go")
	}
	for name, other := range map[string]aisettings.Settings{
		"another model of the reading": helperOf(t, "deepseek", "deepseek-chat", 32_000),
		"another limit of an answer":   helperOf(t, "deepseek", "deepseek-reasoner", 64_000),
		"another host":                 helperOf(t, "openai", "deepseek-reasoner", 32_000),
	} {
		kept := ai
		ai = other
		if names("set-a") != "" || threads(stampsOfThree("h1", "h2", "h3")) != "" || len(chapters()) != 0 {
			t.Fatalf("%s: the marks act", name)
		}
		ai = kept
	}

	// Two text versions keep their marks apart, and equal sets do not pay twice; a new failure of a version replaces its mark.
	if err := policy.MarkNamesUnread(ctx, consentSaga, "draft", "set-b", sagamemorydomain.UnreadNoAnswer); err != nil {
		t.Fatal(err)
	}
	if names("set-a") != sagamemorydomain.UnreadUnreadable || names("set-b") != sagamemorydomain.UnreadNoAnswer {
		t.Fatal("the marks of two text versions do not hold together")
	}
	if err := policy.MarkNamesUnread(ctx, consentSaga, "published", "set-c", sagamemorydomain.UnreadCutAnswer); err != nil {
		t.Fatal(err)
	}
	if names("set-a") != "" || names("set-c") != sagamemorydomain.UnreadCutAnswer || names("set-b") == "" {
		t.Fatal("a new failure of the version did not replace its mark")
	}

	// A saga that is not there is not marked; a chapter mark without a helper — one of the time before — does not act.
	if err := repository.MarkRest(ctx, "94000000-0000-4000-8000-0000000000ff", service.RestMark{
		Kind: service.RestNames, View: "published", Names: "set-a", Reason: "unreadable", Helper: domain.HelperOf(ai),
	}, now); err != nil {
		t.Fatal(err)
	}
	if marks, err := repository.RestMarks(ctx, "94000000-0000-4000-8000-0000000000ff"); err != nil || len(marks) != 0 {
		t.Fatalf("a saga that is not there was marked: %+v, %v", marks, err)
	}
	old := sagamemoryservice.CardKey{ChapterID: "94000000-0000-4000-8000-0000000000c2", ContentHash: "hash-2"}
	if err := repository.MarkUnread(ctx, consentSaga, old.ChapterID, old.ContentHash, version, sagamemorydomain.UnreadUnreadable, "", now); err != nil {
		t.Fatal(err)
	}
	if unread := chapters(); unread[old] || !unread[chapter] {
		t.Fatalf("the chapter marks = %v", unread)
	}
}

// RR1.6: a mark is written only for a live saga — a saga in the trash (deleted by the author, still to be restored) gets no
// mark of its names, its threads or its chapters; the marks it had before stay for its return.
func TestASagaInTheTrashIsNotMarked(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_marks_trash")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	const trashed = "94000000-0000-4000-8000-0000000000e1"
	sagas := projectstore.New(db)
	if _, err := sagas.CreateProjectWithOwnerMembership(ctx, projects.CreateProjectStoreInput{
		ProjectID: trashed, OwnerMembershipID: "94000000-0000-4000-8000-0000000000e2", OwnerUserID: consentAuthor,
		Title: "Северный предел", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	helper := domain.HelperOf(helperOf(t, "deepseek", "deepseek-reasoner", 32_000))
	version := sagamemorydomain.CurrentCardInstructions.Version
	names := service.RestMark{Kind: service.RestNames, View: "published", Names: "set-a", Reason: sagamemorydomain.UnreadUnreadable, Helper: helper}
	if err := repository.MarkRest(ctx, trashed, names, now); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkUnread(ctx, trashed, "94000000-0000-4000-8000-0000000000c1", "hash-1", version, sagamemorydomain.UnreadCutAnswer, helper, now); err != nil {
		t.Fatal(err)
	}
	if err := sagas.DeleteProject(ctx, projects.DeleteProjectStoreInput{
		ActorUserID: consentAuthor, ProjectID: trashed, TombstoneID: "94000000-0000-4000-8000-0000000000e3", DeletedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, mark := range []service.RestMark{
		{Kind: service.RestNames, View: "draft", Names: "set-b", Reason: sagamemorydomain.UnreadRefused, Helper: helper},
		{Kind: service.RestThreads, View: "published", Stamps: stampsOfThree("h1", "h2", "h3"), LayerVersion: layerdomain.InstructionsVersion,
			Reason: sagamemorydomain.UnreadNoAnswer, Helper: helper},
	} {
		if err := repository.MarkRest(ctx, trashed, mark, now); err != nil {
			t.Fatalf("%s: %v", mark.Kind, err)
		}
	}
	if err := repository.MarkUnread(ctx, trashed, "94000000-0000-4000-8000-0000000000c2", "hash-2", version, sagamemorydomain.UnreadCutAnswer, helper, now); err != nil {
		t.Fatal(err)
	}
	rest, err := repository.RestMarks(ctx, trashed)
	if err != nil || len(rest) != 1 || rest[0].Kind != service.RestNames || rest[0].View != "published" || rest[0].Names != "set-a" {
		t.Fatalf("the marks of the names and the threads of a saga in the trash = %+v, %v; want only the one of before", rest, err)
	}
	chapters, err := repository.Unread(ctx, trashed, version)
	if err != nil || len(chapters) != 1 || chapters[0].ID != "94000000-0000-4000-8000-0000000000c1" {
		t.Fatalf("the marks of the chapters of a saga in the trash = %+v, %v; want only the one of before", chapters, err)
	}
}

// RR1.7: «Повторить» and a new consent — after the host changed — forget the marks of the chapters, the names and the threads
// of the saga in the transaction of the consent; «Повторить» when the stop sum is spent is refused by its field and keeps
// them; a pause, «Продолжить» and a new sum keep them.
func TestTheRepeatAndANewConsentForgetTheMarks(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_marks_forget")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	ai := helperOf(t, "deepseek", "deepseek-reasoner", 32_000)
	settings := heldSettings{ai: &ai}
	policy := service.Policy{Settings: settings, Consents: repository, Marks: repository}
	set := service.SetConsent{Settings: settings, Owners: repository, Store: repository, Work: workOfTheTest{}, Now: time.Now}
	change := func(change domain.Change) error {
		_, err := set.Execute(ctx, consentAuthor, consentSaga, change)
		return err
	}
	chapter := sagamemoryservice.CardKey{ChapterID: "94000000-0000-4000-8000-0000000000c1", ContentHash: "hash-1"}
	version := sagamemorydomain.CurrentCardInstructions.Version
	mark := func() {
		t.Helper()
		for _, err := range []error{
			policy.MarkUnread(ctx, consentSaga, chapter, version, sagamemorydomain.UnreadCutAnswer),
			policy.MarkNamesUnread(ctx, consentSaga, "published", "set-a", sagamemorydomain.UnreadUnreadable),
			policy.MarkThreadsUnread(ctx, consentSaga, "draft", stampsOfThree("h1", "h2", "h3"), sagamemorydomain.UnreadNoAnswer),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	kept := func() int {
		t.Helper()
		chapters, err := repository.Unread(ctx, consentSaga, version)
		if err != nil {
			t.Fatal(err)
		}
		rest, err := repository.RestMarks(ctx, consentSaga)
		if err != nil {
			t.Fatal(err)
		}
		return len(chapters) + len(rest)
	}
	stop := int64(1_000)
	if err := change(domain.Change{State: domain.StateAllowed, StopAt: &stop}); err != nil {
		t.Fatal(err)
	}
	mark()
	more := int64(2_000)
	for _, step := range []struct {
		name   string
		change domain.Change
	}{
		{"a pause", domain.Change{State: domain.StatePaused}},
		{"Продолжить", domain.Change{State: domain.StateAllowed}},
		{"a new sum", domain.Change{State: domain.StateAllowed, StopAt: &more}},
	} {
		if err := change(step.change); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := kept(); got != 3 {
			t.Fatalf("%s kept %d marks", step.name, got)
		}
	}
	consent, _ := repository.Consent(ctx, consentSaga)
	if err := repository.Settle(ctx, modeljournal.Request{
		At: time.Now(), Purpose: "names", Profile: "own", ProviderHost: "api.deepseek.com", Model: "deepseek-reasoner", RequestBytes: 100,
		Outcome: modeljournal.OutcomeCompleted,
	}, &service.Count{ProjectID: consentSaga, Since: consent.Since, Unit: domain.UnitTokens, Tokens: aisettings.Tokens{Input: 1_500, Output: 600}}); err != nil {
		t.Fatal(err)
	}
	var field *domain.FieldError
	if err := change(domain.Change{State: domain.StateAllowed, RetryUnread: true}); !errors.As(err, &field) || field.Field != "stopAt" {
		t.Fatalf("«Повторить» at the spent sum = %v", err)
	}
	if got := kept(); got != 3 {
		t.Fatalf("the refused «Повторить» kept %d marks", got)
	}
	enough := int64(10_000)
	if err := change(domain.Change{State: domain.StateAllowed, StopAt: &enough}); err != nil || kept() != 3 {
		t.Fatalf("a new sum: %v, %d marks", err, kept())
	}
	if err := change(domain.Change{State: domain.StateAllowed, RetryUnread: true}); err != nil || kept() != 0 {
		t.Fatalf("«Повторить»: %v, %d marks left", err, kept())
	}
	mark()
	ai = helperOf(t, "openai", "deepseek-reasoner", 32_000)
	mark()
	if err := change(domain.Change{State: domain.StateAllowed, NoStop: true}); err != nil || kept() != 0 {
		t.Fatalf("a new consent after the host changed: %v, %d marks left", err, kept())
	}
}

// RR1.8, specs/2026-11-13-desk-account-ai.md AI3.6: the reading of the account that goes on by itself from a pause is no word
// of the author — the same consent and count go on, the marks of the names and the threads stay and still act, so the queue
// it asks for (readingQueues.Queue: ExecuteFor and QueueDue, which read the same rule) has nothing to queue for them.
func TestTheResumeOfTheAccountKeepsTheMarks(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_marks_resume")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ai := helperOf(t, "deepseek", "deepseek-reasoner", 32_000)
	ai.Source = aisettings.SourceAccount
	ai.Account = &aisettings.Account{ChatModel: "site-chat", CardsModel: "site-cards", ContextWindow: 400_000, CompactLimit: 300_000}
	settings := heldSettings{ai: &ai}
	policy := service.Policy{Settings: settings, Consents: repository, Marks: repository}
	consent, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed, NoStop: true}, now, domain.SourceAccount, settings.SiteHost(), domain.UnitTokens)
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	stamps := stampsOfThree("h1", "h2", "h3")
	if err := policy.MarkNamesUnread(ctx, consentSaga, "published", "set-a", sagamemorydomain.UnreadNoAnswer); err != nil {
		t.Fatal(err)
	}
	if err := policy.MarkThreadsUnread(ctx, consentSaga, "published", stamps, sagamemorydomain.UnreadRefused); err != nil {
		t.Fatal(err)
	}
	if paused, err := repository.Pause(ctx, consentSaga, consent.Since, domain.PauseConnection, now); err != nil || !paused {
		t.Fatalf("the pause «связь»: %v, %v", paused, err)
	}
	work := &queuedOfTheTest{}
	resumed, err := service.Resume{Settings: settings, Store: repository, Work: work, Now: time.Now}.Execute(ctx, domain.PauseConnection)
	if err != nil || resumed != 1 || len(work.queued) != 1 || work.queued[0] != consentSaga {
		t.Fatalf("Resume() = %d, %v; queued %v", resumed, err, work.queued)
	}
	after, err := repository.Consent(ctx, consentSaga)
	if err != nil || after == nil || after.State != domain.StateAllowed || !after.Since.Equal(consent.Since) {
		t.Fatalf("the consent after the resume = %+v, %v; want the same one allowed", after, err)
	}
	if rest, err := repository.RestMarks(ctx, consentSaga); err != nil || len(rest) != 2 {
		t.Fatalf("the marks after the resume = %+v, %v", rest, err)
	}
	if reason, err := policy.NamesUnread(ctx, consentSaga, "set-a"); err != nil || reason != sagamemorydomain.UnreadNoAnswer {
		t.Fatalf("the names after the resume = %q, %v; want the mark acting", reason, err)
	}
	if reason, err := policy.ThreadsUnread(ctx, consentSaga, stamps); err != nil || reason != sagamemorydomain.UnreadRefused {
		t.Fatalf("the threads after the resume = %q, %v; want the mark acting", reason, err)
	}
}

// queuedOfTheTest records the sagas whose reading was queued.
type queuedOfTheTest struct{ queued []string }

func (work *queuedOfTheTest) Queue(_ context.Context, projectID string) error {
	work.queued = append(work.queued, projectID)
	return nil
}

func (*queuedOfTheTest) Cancel(context.Context, string, string) error { return nil }

type workOfTheTest struct{}

func (workOfTheTest) Queue(context.Context, string) error          { return nil }
func (workOfTheTest) Cancel(context.Context, string, string) error { return nil }
