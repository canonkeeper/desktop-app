package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/quota"
)

type fakeOverrides struct {
	calls   int
	userID  string
	changes []domain.OverrideChange
	at      time.Time
	sources map[domain.Key]domain.Source
	account domain.Account
}

func (fake *fakeOverrides) SetOverrides(_ context.Context, userID string, changes []domain.OverrideChange, at time.Time) ([]domain.OverrideChanged, error) {
	fake.calls++
	fake.userID, fake.changes, fake.at = userID, changes, at
	changed := make([]domain.OverrideChanged, len(changes))
	for index, change := range changes {
		changed[index] = domain.OverrideChanged{Key: change.Key, After: change.Value}
	}
	return changed, nil
}

func (fake *fakeOverrides) LimitSources(context.Context, string) (map[domain.Key]domain.Source, error) {
	return fake.sources, nil
}

func (fake *fakeOverrides) AccountQuota(_ context.Context, userID string, at time.Time) (domain.Account, error) {
	fake.userID, fake.at = userID, at
	return fake.account, nil
}

func value(number int64) *int64 { return &number }

var quotaNow = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

// writer-quotas R6.2, admin-panel R3.2: one use case for the operator and the page; a known key,
// a value ≥ 0 or the default, each key once; a refused set changes nothing.
func TestSetOverridesChecksTheChangesOnce(t *testing.T) {
	store := &fakeOverrides{}
	useCase := SetOverrides{Writer: store, Now: func() time.Time { return quotaNow }}
	changes := []domain.OverrideChange{{Key: domain.KeyChatTurnsPerDay, Value: value(60)}, {Key: domain.KeyActiveTurns}}
	changed, err := useCase.Execute(t.Context(), " writer-id ", changes)
	if err != nil || len(changed) != 2 || store.userID != "writer-id" || !store.at.Equal(quotaNow) ||
		changed[0].Key != domain.KeyChatTurnsPerDay || *changed[0].After != 60 || changed[1].After != nil {
		t.Fatalf("changed = %+v, %v; store %+v", changed, err, store)
	}
	for name, refused := range map[string][]domain.OverrideChange{
		"no change":    nil,
		"unknown key":  {{Key: "chat_turns", Value: value(5)}},
		"negative":     {{Key: domain.KeyProjects, Value: value(-1)}},
		"repeated key": {{Key: domain.KeyProjects, Value: value(5)}, {Key: domain.KeyProjects}},
	} {
		if _, err := useCase.Execute(t.Context(), "writer-id", refused); !errors.Is(err, domain.ErrInvalidOverride) {
			t.Fatalf("%s: err = %v, want ErrInvalidOverride", name, err)
		}
	}
	if _, err := useCase.Execute(t.Context(), " ", changes); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no writer: err = %v, want ErrNotFound", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want only the valid set", store.calls)
	}
}

// writer-quotas R6.1, admin-panel R3.1, specs/2026-11-08-subscription-tiers.md TR2.6: the limits and their use with
// the source of each — the writer's own value, his plan or the default.
func TestGetWriterQuotaNamesTheSourceOfEachLimit(t *testing.T) {
	store := &fakeOverrides{
		sources: domain.LimitSources(domain.Settings{domain.KeyChatTurnsPerDay: 60}, domain.Settings{domain.KeyAccountBooks: 12}),
		account: domain.Account{Limits: domain.Defaults().With(domain.KeyChatTurnsPerDay, 60), ChatTurns: 4},
	}
	writer, err := GetWriterQuota{Reader: store, Sources: store, Now: func() time.Time { return quotaNow }}.Execute(t.Context(), "writer-id")
	if err != nil || !store.at.Equal(quotaNow) || store.userID != "writer-id" {
		t.Fatalf("writer quota = %+v, %v", writer, err)
	}
	if writer.LimitSources[domain.KeyChatTurnsPerDay] != domain.SourceOwn || writer.LimitSources[domain.KeyAccountBooks] != domain.SourcePlan ||
		writer.LimitSources[domain.KeyProjects] != domain.SourceDefault {
		t.Fatalf("sources = %v", writer.LimitSources)
	}
	if used, ok := writer.Used(domain.KeyChatTurnsPerDay); !ok || used != 4 || writer.Limits.ChatTurnsPerDay != 60 {
		t.Fatalf("chat turns = %d (%v) of %d", used, ok, writer.Limits.ChatTurnsPerDay)
	}
	if _, ok := writer.Used(domain.KeyProjectBooks); ok {
		t.Fatal("a per-project limit has no account use")
	}
	if _, err := (GetWriterQuota{Reader: store, Sources: store, Now: time.Now}).Execute(t.Context(), ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no writer: err = %v, want ErrNotFound", err)
	}
}

type fakeDefaults struct {
	calls    int
	adminID  string
	changes  []domain.OverrideChange
	at       time.Time
	platform domain.PlatformDefaults
}

func (fake *fakeDefaults) PlatformDefaults(context.Context) (domain.PlatformDefaults, error) {
	return fake.platform, nil
}

func (fake *fakeDefaults) SetPlatformDefaults(_ context.Context, adminID string, changes []domain.OverrideChange, at time.Time) ([]domain.OverrideChanged, error) {
	fake.calls++
	fake.adminID, fake.changes, fake.at = adminID, changes, at
	return []domain.OverrideChanged{{Key: changes[0].Key, After: changes[0].Value}}, nil
}

// specs/2026-10-29-admin-panel.md R7.4: every writer limit in the order of Keys, each with the code's value, the
// platform's and the one in force; the trial drawings are the tier's (specs/2026-11-08-subscription-tiers.md TR6.6).
func TestGetQuotaDefaultsShowsTheCodePlatformAndEffectiveValues(t *testing.T) {
	changedAt := quotaNow.Add(-time.Hour)
	store := &fakeDefaults{platform: domain.PlatformDefaults{
		Values: domain.Settings{domain.KeyChatTurnsPerDay: 40, domain.KeyImagesPerDay: 9}, UpdatedAt: &changedAt, UpdatedBy: "admin-id",
	}}
	list, err := GetQuotaDefaults{Reader: store}.Execute(t.Context())
	if err != nil || len(list.Items) != len(domain.Keys()) || list.UpdatedAt == nil || !list.UpdatedAt.Equal(changedAt) {
		t.Fatalf("defaults = %+v, %v", list, err)
	}
	for index, key := range domain.Keys() {
		if list.Items[index].Key != key {
			t.Fatalf("item %d = %s, want %s", index, list.Items[index].Key, key)
		}
	}
	chat, projects, images := list.Items[0], list.Items[3], list.Items[10]
	if chat.Code != 30 || chat.Platform == nil || *chat.Platform != 40 || chat.Effective() != 40 {
		t.Fatalf("chat_turns_per_day = %+v", chat)
	}
	if projects.Code != domain.Defaults().Projects || projects.Platform != nil || projects.Effective() != projects.Code {
		t.Fatalf("projects = %+v", projects)
	}
	if images.Key != domain.KeyImagesPerDay || images.Code != domain.Defaults().ImagesPerDay || images.Effective() != 9 {
		t.Fatalf("images_per_day = %+v", images)
	}
}

// R7.5: the change is checked once before the store, with the administrator and the moment.
func TestSetQuotaDefaultsChecksTheChangeAndPassesTheAdministrator(t *testing.T) {
	store := &fakeDefaults{}
	useCase := SetQuotaDefaults{Writer: store, Now: func() time.Time { return quotaNow }}
	changed, err := useCase.Execute(t.Context(), " admin-id ", []domain.OverrideChange{{Key: domain.KeyImagesPerDay, Value: value(10)}})
	if err != nil || len(changed) != 1 || changed[0].Code != domain.Defaults().ImagesPerDay || store.adminID != "admin-id" || !store.at.Equal(quotaNow) || *store.changes[0].Value != 10 {
		t.Fatalf("changed = %+v, %v; store %+v", changed, err, store)
	}
	for name, refused := range map[string][]domain.OverrideChange{
		"no change":   nil,
		"unknown key": {{Key: "chat_turns", Value: value(5)}},
		"negative":    {{Key: domain.KeyProjects, Value: value(-1)}},
		"trial":       {{Key: "trial_drawings", Value: value(5)}},
	} {
		if _, err := useCase.Execute(t.Context(), "admin-id", refused); !errors.Is(err, domain.ErrInvalidOverride) {
			t.Fatalf("%s: err = %v, want ErrInvalidOverride", name, err)
		}
	}
	if _, err := useCase.Execute(t.Context(), " ", []domain.OverrideChange{{Key: domain.KeyProjects}}); !errors.Is(err, domain.ErrInvalidOverride) {
		t.Fatalf("no administrator: err = %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want only the valid change", store.calls)
	}
}

// specs/2026-11-08-admin-redesign.md AR7.1: the keys a plan of the build sets have no platform default — the list leaves
// them out and a change of any of them is refused before the store; without a plan (the desktop) every key stays.
func TestTheKeysOfThePlanHaveNoPlatformDefault(t *testing.T) {
	plan := []domain.Key{domain.KeyProjects, domain.KeyAccountBooks, domain.KeyChatTurnsPerDay, domain.KeyImagesPerDay}
	store := &fakeDefaults{platform: domain.PlatformDefaults{Values: domain.Settings{domain.KeyImportsPerDay: 9}}}
	list, err := GetQuotaDefaults{Reader: store, Plan: plan}.Execute(t.Context())
	if err != nil || len(list.Items) != len(domain.Keys())-len(plan) {
		t.Fatalf("defaults with a plan = %d items, %v; want %d", len(list.Items), err, len(domain.Keys())-len(plan))
	}
	for _, item := range list.Items {
		for _, key := range plan {
			if item.Key == key {
				t.Fatalf("the key of the plan %s is listed", key)
			}
		}
	}
	if whole, err := (GetQuotaDefaults{Reader: store}).Execute(t.Context()); err != nil || len(whole.Items) != len(domain.Keys()) {
		t.Fatalf("defaults without a plan = %d items, %v", len(whole.Items), err)
	}
	useCase := SetQuotaDefaults{Writer: store, Now: func() time.Time { return quotaNow }, Plan: plan}
	for _, key := range plan {
		if _, err := useCase.Execute(t.Context(), "admin-id", []domain.OverrideChange{{Key: domain.KeyImportsPerDay, Value: value(5)}, {Key: key, Value: value(2)}}); !errors.Is(err, domain.ErrInvalidOverride) {
			t.Fatalf("%s: err = %v, want ErrInvalidOverride", key, err)
		}
		if _, err := useCase.Execute(t.Context(), "admin-id", []domain.OverrideChange{{Key: key}}); !errors.Is(err, domain.ErrInvalidOverride) {
			t.Fatalf("%s back to the code: err = %v, want ErrInvalidOverride", key, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want none for a key of the plan", store.calls)
	}
	if _, err := useCase.Execute(t.Context(), "admin-id", []domain.OverrideChange{{Key: domain.KeyImportsPerDay, Value: value(5)}}); err != nil || store.calls != 1 {
		t.Fatalf("a general key: err = %v, calls = %d", err, store.calls)
	}
}
