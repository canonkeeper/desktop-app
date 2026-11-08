package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/quota"
)

type fakeOverrides struct {
	calls     int
	userID    string
	changes   []domain.OverrideChange
	at        time.Time
	overrides map[domain.Key]*int64
	account   domain.Account
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

func (fake *fakeOverrides) Overrides(context.Context, string) (map[domain.Key]*int64, error) {
	return fake.overrides, nil
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

// writer-quotas R6.1, admin-panel R3.1: the limits and their use with the keys the owner changed.
func TestGetWriterQuotaMarksTheChangedKeys(t *testing.T) {
	store := &fakeOverrides{
		overrides: map[domain.Key]*int64{domain.KeyChatTurnsPerDay: value(60), domain.KeyProjects: nil},
		account:   domain.Account{Limits: domain.Defaults().With(domain.KeyChatTurnsPerDay, 60), ChatTurns: 4},
	}
	writer, err := GetWriterQuota{Reader: store, Overrides: store, Now: func() time.Time { return quotaNow }}.Execute(t.Context(), "writer-id")
	if err != nil || !store.at.Equal(quotaNow) || store.userID != "writer-id" {
		t.Fatalf("writer quota = %+v, %v", writer, err)
	}
	if !writer.Overridden[domain.KeyChatTurnsPerDay] || writer.Overridden[domain.KeyProjects] || len(writer.Overridden) != 1 {
		t.Fatalf("overridden = %v", writer.Overridden)
	}
	if used, ok := writer.Used(domain.KeyChatTurnsPerDay); !ok || used != 4 || writer.Limits.ChatTurnsPerDay != 60 {
		t.Fatalf("chat turns = %d (%v) of %d", used, ok, writer.Limits.ChatTurnsPerDay)
	}
	if _, ok := writer.Used(domain.KeyProjectBooks); ok {
		t.Fatal("a per-project limit has no account use")
	}
	if _, err := (GetWriterQuota{Reader: store, Overrides: store, Now: time.Now}).Execute(t.Context(), ""); !errors.Is(err, domain.ErrNotFound) {
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

// specs/2026-10-29-admin-panel.md R7.4: every writer limit in the order of Keys, then the trial
// drawings, each with the code's value, the platform's and the one in force.
func TestGetQuotaDefaultsShowsTheCodePlatformAndEffectiveValues(t *testing.T) {
	changedAt := quotaNow.Add(-time.Hour)
	store := &fakeDefaults{platform: domain.PlatformDefaults{
		Values: domain.Settings{domain.KeyChatTurnsPerDay: 40, domain.KeyTrialDrawings: 9}, UpdatedAt: &changedAt, UpdatedBy: "admin-id",
	}}
	list, err := GetQuotaDefaults{Reader: store}.Execute(t.Context())
	if err != nil || len(list.Items) != len(domain.Keys())+1 || list.UpdatedAt == nil || !list.UpdatedAt.Equal(changedAt) {
		t.Fatalf("defaults = %+v, %v", list, err)
	}
	for index, key := range domain.DefaultKeys() {
		if list.Items[index].Key != key {
			t.Fatalf("item %d = %s, want %s", index, list.Items[index].Key, key)
		}
	}
	chat, projects, trial := list.Items[0], list.Items[3], list.Items[len(list.Items)-1]
	if chat.Code != 30 || chat.Platform == nil || *chat.Platform != 40 || chat.Effective() != 40 {
		t.Fatalf("chat_turns_per_day = %+v", chat)
	}
	if projects.Code != domain.Defaults().Projects || projects.Platform != nil || projects.Effective() != projects.Code {
		t.Fatalf("projects = %+v", projects)
	}
	if trial.Key != domain.KeyTrialDrawings || trial.Code != domain.TrialDrawings || trial.Effective() != 9 {
		t.Fatalf("trial drawings = %+v", trial)
	}
}

// R7.5: the change is checked once before the store, with the administrator and the moment.
func TestSetQuotaDefaultsChecksTheChangeAndPassesTheAdministrator(t *testing.T) {
	store := &fakeDefaults{}
	useCase := SetQuotaDefaults{Writer: store, Now: func() time.Time { return quotaNow }}
	changed, err := useCase.Execute(t.Context(), " admin-id ", []domain.OverrideChange{{Key: domain.KeyTrialDrawings, Value: value(10)}})
	if err != nil || len(changed) != 1 || changed[0].Code != domain.TrialDrawings || store.adminID != "admin-id" || !store.at.Equal(quotaNow) || *store.changes[0].Value != 10 {
		t.Fatalf("changed = %+v, %v; store %+v", changed, err, store)
	}
	for name, refused := range map[string][]domain.OverrideChange{
		"no change":   nil,
		"unknown key": {{Key: "chat_turns", Value: value(5)}},
		"negative":    {{Key: domain.KeyProjects, Value: value(-1)}},
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
