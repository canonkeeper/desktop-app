// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	service "canonkeeper/backend/internal/services/desktop"
)

// specs/2026-11-13-desk-settings.md ST2.2: the place the data take is the sum of the regular files under the data
// directory; a link — to a file or to a directory outside — is not followed and not counted.
func TestThePlaceOfTheDataCountsNoLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links of the test are symbolic links of Unix")
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{filepath.Join(data, "pg", "base"), filepath.Join(data, "codex", "home"), outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(data, "pg", "base", "1"), strings.Repeat("a", 1000))
	writeFile(t, filepath.Join(data, "pg", "base", "2"), strings.Repeat("b", 24))
	writeFile(t, filepath.Join(data, "settings.json"), `{"port":21000}`)
	writeFile(t, filepath.Join(outside, "big"), strings.Repeat("c", 1<<20))
	if err := os.Symlink(outside, filepath.Join(data, "codex", "home", "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "big"), filepath.Join(data, "big")); err != nil {
		t.Fatal(err)
	}
	if got, want := placeOf(data), int64(1000+24+len(`{"port":21000}`)); got != want {
		t.Fatalf("the place of the data = %d, want %d", got, want)
	}
}

// ST2.2: a request waits for the count no longer than its limit; until the count ends it answers the last known place
// with the mark «считается», and once the count ended — the new place. A count that ended a moment ago answers the next
// request without a new one, so the page that asks again while the server counts gets the place it waited for.
func TestTheCountOfThePlaceAnswersWithinItsLimit(t *testing.T) {
	if usageLimit != 2*time.Second {
		t.Fatalf("the limit of a request = %s, want 2s", usageLimit)
	}
	release := make(chan struct{})
	var counts atomic.Int64
	clock := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}
	meter := &usageMeter{root: "/data", limit: 50 * time.Millisecond, now: now, count: func(root string) int64 {
		if root != "/data" {
			t.Errorf("the count walks %s", root)
		}
		<-release
		return counts.Add(1) * 4096
	}}
	// The count is held until release: a request that waited for it would never answer, so an answer here is the limit
	// at work, whatever the load of the machine.
	first := meter.Measure(context.Background())
	if first != (service.DiskUsage{Counting: true}) {
		t.Fatalf("the first request = %+v, want counting with no place known", first)
	}
	// A second request while the count goes on waits for the same count; no second one starts.
	if second := meter.Measure(context.Background()); second != (service.DiskUsage{Counting: true}) {
		t.Fatalf("a request during the count = %+v", second)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		meter.mu.Lock()
		done := meter.running == nil
		meter.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The count ended between two requests: the next one answers it, without a new count.
	if got := meter.Measure(context.Background()); got != (service.DiskUsage{Bytes: 4096, Known: true}) || counts.Load() != 1 {
		t.Fatalf("a request after the count = %+v after %d counts, want the place counted", got, counts.Load())
	}
	// Later the place is counted anew.
	clockMu.Lock()
	clock = clock.Add(usageFresh)
	clockMu.Unlock()
	if got := meter.Measure(context.Background()); got != (service.DiskUsage{Bytes: 8192, Known: true}) || counts.Load() != 2 {
		t.Fatalf("a later request = %+v after %d counts, want a new count", got, counts.Load())
	}
}

// ST2.1, ST2.3: the copies before an update are the complete ones, the newest first and no more than two; each says
// when it was made and the version of the app that made it — a copy made before the version was written down says none.
func TestTheCopiesBeforeAnUpdateNameTheirTimeAndVersion(t *testing.T) {
	backups := t.TempDir()
	makeCopy(t, backups, "pre-139-20261102T080000.000Z", true)
	makeCopy(t, backups, "pre-140-20261112T091500.250Z", true)
	writeFile(t, filepath.Join(backups, "pre-140-20261112T091500.250Z", backupVersion), "1.1.4\n")
	makeCopy(t, backups, "pre-138-20261020T080000.000Z", true)
	writeFile(t, filepath.Join(backups, "pre-138-20261020T080000.000Z", backupVersion), "1.1.2\n")
	makeCopy(t, backups, "pre-141-20261113T080000.000Z", false)
	writeFile(t, filepath.Join(backups, "notes.txt"), "the author's")

	copies, err := backupList{directory: backups}.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []service.Backup{
		{Made: time.Date(2026, 11, 12, 9, 15, 0, 250_000_000, time.UTC), Version: "1.1.4"},
		{Made: time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)},
	}
	if len(copies) != len(want) || !copies[0].Made.Equal(want[0].Made) || copies[0].Version != want[0].Version ||
		!copies[1].Made.Equal(want[1].Made) || copies[1].Version != want[1].Version {
		t.Fatalf("the copies = %+v, want %+v", copies, want)
	}
	none, err := backupList{directory: filepath.Join(backups, "missing")}.List()
	if err != nil || len(none) != 0 {
		t.Fatalf("no backups directory = %+v, %v", none, err)
	}
}

// ST2.3: the copy of an update holds the version of the app that made it; a program that does not know its version
// writes none.
func TestTheCopyOfAnUpdateHoldsTheVersionThatMadeIt(t *testing.T) {
	for _, version := range []string{"1.1.4", ""} {
		directory := t.TempDir()
		cluster := &Cluster{data: DataDir{Root: t.TempDir()}, version: version}
		if err := cluster.writeVersion(directory); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(directory, backupVersion))
		switch {
		case version == "" && !os.IsNotExist(err):
			t.Fatalf("a program without a version wrote %q, %v", raw, err)
		case version != "" && (err != nil || string(raw) != version+"\n"):
			t.Fatalf("the version of the copy = %q, %v", raw, err)
		}
	}
}

// reaching is a link with an account that goes to the site or not.
type reaching bool

func (reach reaching) Reaches() bool { return bool(reach) }

// ST4.3: the open app goes nowhere in the network until a slice adds an address to the list; specs/2026-11-13-desk-account.md
// AC2.7: a linked app, or one whose link started, goes to the server CanonKeeper of its build for the link.
func TestTheOpenAppGoesNowhereInTheNetwork(t *testing.T) {
	if uses := networkUses(reaching(false), "https://site.example.test")(); len(uses) != 0 {
		t.Fatalf("the network of the app = %+v", uses)
	}
	uses := networkUses(reaching(true), "https://site.example.test")()
	if len(uses) != 1 || uses[0].Host != "site.example.test" || uses[0].Purpose != "связь с аккаунтом: имя, тариф и остатки" {
		t.Fatalf("the network of a linked app = %+v", uses)
	}
}

// One line a host (ST4.3): a linked app that updates itself goes to the server CanonKeeper for both, in its one line.
func TestTheNetworkOfTheAppNamesAHostOnce(t *testing.T) {
	updates := func() []service.NetworkUse {
		return []service.NetworkUse{{Host: "site.example.test", Purpose: "узнать о новой версии"}, {Host: "files.example.test", Purpose: "скачать её"}}
	}
	uses := networkUses(reaching(true), "https://site.example.test", updates)()
	if len(uses) != 2 || uses[0].Host != "site.example.test" || uses[1].Host != "files.example.test" ||
		uses[0].Purpose != "связь с аккаунтом: имя, тариф и остатки; узнать о новой версии" || uses[1].Purpose != "скачать её" {
		t.Fatalf("the network of a linked app that updates itself = %+v", uses)
	}
	if uses := networkUses(reaching(false), "https://site.example.test", updates)(); len(uses) != 2 || uses[0].Purpose != "узнать о новой версии" {
		t.Fatalf("the network of an app that only updates itself = %+v", uses)
	}
}

// specs/2026-11-14-desk-journal.md JR4.1, JR4.3: the provider of «Свой ИИ» stands while its address is kept — an address of this
// computer as a program of this computer —, nothing without a helper, a link and updates, one line a host, and the proxy of
// the environment by its host alone.
func TestTheNetworkNamesTheProviderOfTheHelperAndTheProxy(t *testing.T) {
	none := func() aisettings.Settings { return aisettings.None() }
	if uses := networkUses(reaching(false), "https://site.example.test", ownNetwork(none))(); len(uses) != 0 {
		t.Fatalf("the network without anything = %+v", uses)
	}
	of := func(draft aisettings.OwnDraft) func() aisettings.Settings {
		settings, err := aisettings.Draft{Source: "own", Own: &draft}.Settings()
		if err != nil {
			t.Fatal(err)
		}
		return func() aisettings.Settings { return settings }
	}
	address, local, chat, cards := "https://provider.example.com/v1", "http://127.0.0.1:11434/v1", "north-chat", "north-cards"
	external := of(aisettings.OwnDraft{Preset: "custom", BaseURL: &address, ChatModel: &chat, CardsModel: &cards})
	if uses := networkUses(reaching(false), "https://site.example.test", ownNetwork(external))(); len(uses) != 1 ||
		uses[0].Host != "provider.example.com" || uses[0].Purpose != "помощник через «Свой ИИ»" {
		t.Fatalf("the network of «Свой ИИ» = %+v", uses)
	}
	ollama := of(aisettings.OwnDraft{Preset: "custom", BaseURL: &local, ChatModel: &chat, CardsModel: &cards})
	if uses := networkUses(reaching(false), "https://site.example.test", ownNetwork(ollama))(); len(uses) != 1 ||
		uses[0].Host != "127.0.0.1:11434" || uses[0].Purpose != "программа на этом компьютере, помощник через «Свой ИИ»" {
		t.Fatalf("the network of a helper of this computer = %+v", uses)
	}
	updates := func() []service.NetworkUse {
		return []service.NetworkUse{{Host: "site.example.test", Purpose: "узнать о новой версии приложения и скачать её"}}
	}
	if uses := networkUses(reaching(true), "https://site.example.test", ownNetwork(none), updates)(); len(uses) != 1 {
		t.Fatalf("the link and the updates at one host = %+v", uses)
	}
	// JR4.3: the line of the proxy stands only while an address of the list goes through it — by its host alone, without a
	// name or a password; nothing goes through it without a helper and a link, a helper of this computer goes straight, and
	// so does a host NO_PROXY names; an address of https goes through the proxy of https only.
	proxyOf := func(environment map[string]string, account bool, ai func() aisettings.Settings) string {
		return proxyOfTheNetwork(func(name string) string { return environment[name] }, reaching(account), "https://site.example.test", ai)()
	}
	both := map[string]string{"https_proxy": "http://user:secret@proxy.example.test:3128", "HTTP_PROXY": "proxy.example.com:8080"}
	for _, example := range []struct {
		name        string
		environment map[string]string
		account     bool
		ai          func() aisettings.Settings
		want        string
	}{
		{"nothing in the list", both, false, none, ""},
		{"a helper of this computer", both, false, ollama, ""},
		{"the provider of «Свой ИИ»", both, false, external, "proxy.example.test:3128"},
		{"the server of the link, a proxy without a scheme", map[string]string{"HTTPS_PROXY": "proxy.example.com:8080"}, true, none, "proxy.example.com:8080"},
		{"an address of https and a proxy of http alone", map[string]string{"HTTP_PROXY": "proxy.example.com:8080"}, true, external, ""},
		{"the provider in NO_PROXY", map[string]string{"HTTPS_PROXY": "proxy.example.com:8080", "NO_PROXY": "localhost, provider.example.com"}, false, external, ""},
		{"the subdomains in NO_PROXY", map[string]string{"HTTPS_PROXY": "proxy.example.com:8080", "no_proxy": ".example.com"}, false, external, ""},
		{"everything in NO_PROXY", map[string]string{"HTTPS_PROXY": "proxy.example.com:8080", "NO_PROXY": "*"}, true, external, ""},
		{"another host in NO_PROXY", map[string]string{"HTTPS_PROXY": "proxy.example.com:8080", "NO_PROXY": "other.example.com,10.0.0.0/8"}, false, external, "proxy.example.com:8080"},
		{"the server of the link while the provider goes straight", map[string]string{"HTTPS_PROXY": "proxy.example.com:8080", "NO_PROXY": "provider.example.com"}, true, external, "proxy.example.com:8080"},
		{"no proxy", map[string]string{}, true, external, ""},
	} {
		if got := proxyOf(example.environment, example.account, example.ai); got != example.want {
			t.Errorf("%s: the proxy = %q, want %q", example.name, got, example.want)
		}
	}
}
