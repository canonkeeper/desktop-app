// SPDX-License-Identifier: AGPL-3.0-only

package accountlink

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// specs/2026-11-13-desk-account.md AC1.1: the verifier and the state are 32 random bytes in base64url without padding —
// the 43 signs the site takes — and the challenge is their PKCE S256.
func TestTheSecretsOfALinkHaveTheShapeOfTheSite(t *testing.T) {
	random := bytes.NewReader(bytes.Repeat([]byte{0xfb, 0xff, 0x3e}, 40))
	secret, err := NewSecret(random)
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 43 || strings.ContainsAny(secret, "+/=") {
		t.Fatalf("secret = %q", secret)
	}
	if _, err := NewSecret(bytes.NewReader(make([]byte, 10))); err == nil {
		t.Fatal("a short randomness made a secret")
	}
	sum := sha256.Sum256([]byte(secret))
	if challenge := Challenge(secret); challenge != base64.RawURLEncoding.EncodeToString(sum[:]) || len(challenge) != 43 {
		t.Fatalf("challenge = %q", challenge)
	}
	for code, want := range map[string]bool{
		strings.Repeat("A", 43): true, strings.Repeat("-_", 21) + "a": true, strings.Repeat("A", 42): false,
		strings.Repeat("A", 44): false, strings.Repeat("A", 42) + "=": false, strings.Repeat("A", 42) + "+": false, "": false,
	} {
		if CodeShape(code) != want {
			t.Errorf("CodeShape(%q) = %v", code, !want)
		}
	}
}

// AC1.3: the name of the computer goes by the rule of the site (applink.DeviceName): invalid UTF-8 replaced, the signs Cc
// and Cf removed, the spaces at the edges trimmed, at most 80 runes; empty — «Компьютер».
func TestTheNameOfTheComputerIsOneTheSiteTakes(t *testing.T) {
	for host, want := range map[string]string{
		"north-desk":                         "north-desk",
		"  Рабочий стол  ":                   "Рабочий стол",
		"desk\u200b-one":                     "desk-one",
		"desk\x00\x07one\u202e":              "deskone",
		"bad\xffname":                        "bad\uFFFDname",
		"":                                   DefaultName,
		" \t\u200b ":                         DefaultName,
		strings.Repeat("я", 81):              strings.Repeat("я", 80),
		strings.Repeat("я", 79) + " x":       strings.Repeat("я", 79),
		"\u2028line\u00adsoft":               "linesoft",
		strings.Repeat("a", 80) + "\u200bzz": strings.Repeat("a", 80),
	} {
		got := ComputerName(host)
		if got != want {
			t.Errorf("ComputerName(%q) = %q, want %q", host, got, want)
		}
		if got != strings.TrimSpace(got) || got == "" || len([]rune(got)) > 80 {
			t.Errorf("ComputerName(%q) = %q, not a name the site takes", host, got)
		}
	}
}

// AC1.1: a link starts only for a window on 20000–29999, a system the site knows and a build that names its version.
func TestALinkStartsOnlyForAWindowTheSiteCanReturnTo(t *testing.T) {
	if err := Startable(20000, PlatformWindows, "1.3.0"); err != nil {
		t.Fatal(err)
	}
	if err := Startable(29999, PlatformLinux, "1.3.0"); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"port below": Startable(19999, PlatformLinux, "1.3.0"), "port above": Startable(30000, PlatformLinux, "1.3.0"),
		"system": Startable(24000, "darwin", "1.3.0"), "no version": Startable(24000, PlatformLinux, ""),
		"version sign": Startable(24000, PlatformLinux, "1.3 beta"),
	} {
		if !errors.Is(err, ErrCannotStart) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for goos, want := range map[string]Platform{"windows": PlatformWindows, "linux": PlatformLinux, "darwin": ""} {
		if got, _ := PlatformOf(goos); got != want {
			t.Errorf("PlatformOf(%q) = %q", goos, got)
		}
	}
}

// AC1.1: the address of /link carries the challenge, the state, the port and the system, nothing of the author; the
// pages of the site come from the address of the build.
func TestTheAddressOfTheLinkCarriesNothingOfTheAuthor(t *testing.T) {
	address := LinkAddress("https://site.example.test", "challenge-1", "state-1", 24567, PlatformLinux)
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "site.example.test" || parsed.Path != "/link" {
		t.Fatalf("address = %s", address)
	}
	want := url.Values{"challenge": {"challenge-1"}, "state": {"state-1"}, "port": {"24567"}, "platform": {"linux"}}
	if parsed.Query().Encode() != want.Encode() {
		t.Fatalf("query = %v", parsed.Query())
	}
	pages := PagesOf("https://site.example.test")
	if pages.Tariffs != "https://site.example.test/tariffs" || pages.Apps != "https://site.example.test/account#apps" {
		t.Fatalf("pages = %+v", pages)
	}
	if host := HostOf("https://site.example.test"); host != "site.example.test" {
		t.Fatalf("host = %q", host)
	}
}

// AC1.2: one waiting at a time, by the pattern of the one-time codes of the window: a wrong state does not spend the
// right one, the right one is spent once, a late one and a canceled one are spent for nobody; the same waiting is
// answered while it lives.
func TestAWaitingIsSpentOnceByItsOwnState(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	waits := NewWaits(clock)
	if _, ok := waits.Current(); ok {
		t.Fatal("a waiting before any start")
	}
	waits.Begin(Waiting{Verifier: "verifier-1", State: "state-1", Address: "https://site.example.test/link?x", Expires: now.Add(WaitLifetime)})
	if current, ok := waits.Current(); !ok || current.State != "state-1" || current.Address == "" {
		t.Fatalf("current = %+v, %v", current, ok)
	}
	for _, wrong := range []string{"", "state-2", "state-1 ", "STATE-1"} {
		if _, err := waits.Spend(wrong); !errors.Is(err, ErrStateInvalid) {
			t.Fatalf("Spend(%q) = %v", wrong, err)
		}
	}
	spent, err := waits.Spend("state-1")
	if err != nil || spent.Verifier != "verifier-1" {
		t.Fatalf("Spend = %+v, %v", spent, err)
	}
	if _, err := waits.Spend("state-1"); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("the state spent twice: %v", err)
	}
	if _, ok := waits.Current(); ok {
		t.Fatal("a spent waiting is still current")
	}

	waits.Begin(Waiting{Verifier: "verifier-2", State: "state-2", Expires: now.Add(WaitLifetime)})
	now = now.Add(WaitLifetime)
	if _, ok := waits.Current(); ok {
		t.Fatal("a late waiting is current")
	}
	if _, err := waits.Spend("state-2"); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("a late state: %v", err)
	}

	waits.Begin(Waiting{Verifier: "verifier-3", State: "state-3", Expires: now.Add(WaitLifetime)})
	waits.Cancel()
	if _, err := waits.Spend("state-3"); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("a canceled state: %v", err)
	}

	// Two returns at once with the right state: one spends it.
	waits.Begin(Waiting{Verifier: "verifier-4", State: "state-4", Expires: now.Add(WaitLifetime)})
	var group sync.WaitGroup
	var mu sync.Mutex
	spends := 0
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := waits.Spend("state-4"); err == nil {
				mu.Lock()
				spends++
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	if spends != 1 {
		t.Fatalf("the state was spent %d times", spends)
	}
}

// specs/2026-11-15-desk-account-link-web.md AW1.6: a waiting whose term ended without a return is told once — whether the
// read or a late return met it first — and a new waiting forgets it; a canceled waiting and a spent one are never told.
func TestAWaitingThatRanOutIsToldOnce(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	waits := NewWaits(func() time.Time { return now })
	waits.Begin(Waiting{State: "state-1", Expires: now.Add(WaitLifetime), ChooseHelper: true})
	if current, ok := waits.Current(); !ok || !current.ChooseHelper {
		t.Fatalf("the intent of the waiting = %+v, %v", current, ok)
	}
	if waits.Lapsed() {
		t.Fatal("a waiting that lives is told as ran out")
	}
	now = now.Add(WaitLifetime)
	if !waits.Lapsed() || waits.Lapsed() {
		t.Fatal("a waiting that ran out is not told once")
	}

	waits.Begin(Waiting{State: "state-2", Expires: now.Add(WaitLifetime)})
	now = now.Add(WaitLifetime)
	if _, err := waits.Spend("state-2"); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("a late return = %v", err)
	}
	if !waits.Lapsed() {
		t.Fatal("a late return hid the term that ran out")
	}

	waits.Begin(Waiting{State: "state-3", Expires: now.Add(WaitLifetime)})
	now = now.Add(WaitLifetime)
	waits.Begin(Waiting{State: "state-4", Expires: now.Add(WaitLifetime)})
	if waits.Lapsed() {
		t.Fatal("a new waiting is told as the one before that ran out")
	}
	waits.Cancel()
	now = now.Add(WaitLifetime)
	if waits.Lapsed() {
		t.Fatal("a canceled waiting is told as ran out")
	}
	waits.Begin(Waiting{State: "state-5", Expires: now.Add(WaitLifetime)})
	if _, err := waits.Spend("state-5"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(WaitLifetime)
	if waits.Lapsed() {
		t.Fatal("a spent waiting is told as ran out")
	}
}

// specs/2026-11-15-desk-turns-here.md TH2.1: who holds the places of «разом» is one function of the three numbers — used and
// thisApp the site told at one moment, answering the turns of this link not over here —; the page subtracts nothing. Told
// before a turn began (thisApp below answering), all naughts, and an answer that does not agree with itself (thisApp above
// used) go no lower than nothing.
func TestWhoHoldsThePlacesAtOnce(t *testing.T) {
	for _, case_ := range []struct {
		name                       string
		used, thisApp, answering   int64
		answeringOut, held, others int64
	}{
		{"a turn here in another saga", 1, 1, 1, 1, 0, 0},
		{"a turn over here the site still counts", 1, 1, 0, 0, 1, 0},
		{"a turn of the site", 1, 0, 0, 0, 0, 1},
		{"one of each", 3, 2, 1, 1, 1, 1},
		{"told before the turn here began", 0, 0, 1, 1, 0, 0},
		{"told before the turn here began, a turn of the site", 1, 0, 1, 1, 0, 1},
		{"all naughts", 0, 0, 0, 0, 0, 0},
		{"thisApp above used", 1, 2, 0, 0, 2, 0},
		{"two here, two at the site", 4, 2, 2, 2, 0, 2},
	} {
		got := PlacesOf(case_.used, case_.thisApp, case_.answering)
		if got.Answering != case_.answeringOut || got.Held != case_.held || got.Elsewhere != case_.others {
			t.Errorf("%s: PlacesOf(%d, %d, %d) = %+v", case_.name, case_.used, case_.thisApp, case_.answering, got)
		}
	}
}
