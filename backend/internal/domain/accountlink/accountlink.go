// SPDX-License-Identifier: AGPL-3.0-only

// Package accountlink holds the rules of the link of the app with an account of the site on the side of the app
// (specs/2026-11-13-desk-account.md AC1, AC2; the side of the site is specs/2026-11-13-desk-link.md): the PKCE verifier
// and the state of one waiting, the address of the page /link, the name of the computer the site takes, the shape of a
// code of the return and the states of the link. Nothing here knows HTTP, the shell or the site.
package accountlink

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/quota"
)

const (
	// WaitLifetime is how long a started link waits for the return of the browser (AC1.1).
	WaitLifetime = 10 * time.Minute
	// FreshEvery is how often the account is read from the site without the page asking (AC2.1).
	FreshEvery = time.Minute
	// MinPort and MaxPort bound the port of the window the site returns to (the site's applink.MinPort, MaxPort).
	MinPort = 20000
	MaxPort = 29999
	// DefaultName names a computer whose host name leaves nothing the site takes (AC1.3).
	DefaultName = "Компьютер"
	// maxName is the longest name of a computer the site takes, in runes.
	maxName = 80
	// maxVersion is the longest version of the app the site takes.
	maxVersion = 32
	// secretBytes is the randomness of the verifier and of the state.
	secretBytes = 32
)

var (
	// ErrStateInvalid is a return whose state is not the one waiting: unknown, spent, canceled or late. They are not told
	// apart (AC1.2).
	ErrStateInvalid = errors.New("accountlink: the state of the return is not the one waiting")
	// ErrCannotStart is a build the site cannot return to: a port of the window out of 20000–29999, a system the site
	// does not know or no version of the app (AC1.1).
	ErrCannotStart = errors.New("accountlink: this build cannot be linked")
	// ErrNotNow is a start of a link while the app is linked or a link waits (AC1.1).
	ErrNotNow = errors.New("accountlink: a link is not started now")
	// ErrLinkInvalid is the one refusal of the exchange of a code by the site, app.link_invalid (AC1.3).
	ErrLinkInvalid = errors.New("accountlink: the site refused the code")
	// ErrPassInvalid is the pass the app holds refused by the site, app.pass_invalid: the link is broken (AC2.2).
	ErrPassInvalid = errors.New("accountlink: the site refused the pass")
	// ErrPassReplaced is a refusal of a pass the app no longer holds: a newer link is not touched (AC2.3).
	ErrPassReplaced = errors.New("accountlink: the site refused a pass held before")
	// ErrNotHeld is a read of the account while the app holds no pass — the author unlinked or the site refused it
	// meanwhile: the site was not asked and refused nothing, and nothing changes (AC2.3).
	ErrNotHeld = errors.New("accountlink: the app holds no pass")
	// ErrUnreachable is a site that did not answer as it answers: no connection, a term, 429, 5xx or any other answer
	// (AC2.2).
	ErrUnreachable = errors.New("accountlink: no connection with the site")
	// ErrNotSent marks a request that did not leave the app: the client holds no pass, or waits out the Retry-After the site
	// named. It goes with ErrNotHeld or ErrUnreachable; the journal of what went out says the body was not sent
	// (specs/2026-11-14-desk-journal.md JR2.4).
	ErrNotSent = errors.New("accountlink: the request did not leave the app")
	// ErrAnswerLost is an answer that may have been lost on its way: the connection broke, the term ended or the site
	// answered 5xx. The exchange of a code is asked once more then (AC1.3).
	ErrAnswerLost = errors.Join(ErrUnreachable, errors.New("accountlink: the answer of the site was lost"))
)

// NewSecret draws a verifier or a state: 32 random bytes in base64url without padding, 43 signs (AC1.1).
func NewSecret(random io.Reader) (string, error) {
	drawn := make([]byte, secretBytes)
	if _, err := io.ReadFull(random, drawn); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(drawn), nil
}

// Challenge is the PKCE S256 challenge of a verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// CodeShape reports a value that may be a code of the site: 43 signs of base64url (AC1.2).
func CodeShape(code string) bool {
	if len(code) != 43 {
		return false
	}
	for _, sign := range code {
		if !asciiAlnum(sign) && sign != '-' && sign != '_' {
			return false
		}
	}
	return true
}

// ComputerName is the name of the computer as the site takes it (the site's applink.DeviceName; AC1.3): invalid UTF-8
// replaced, the signs Cc and Cf removed, the spaces at the edges trimmed, at most 80 runes; nothing left — «Компьютер».
func ComputerName(host string) string {
	clean := strings.Map(func(sign rune) rune {
		if unicode.Is(unicode.Cc, sign) || unicode.Is(unicode.Cf, sign) {
			return -1
		}
		return sign
	}, strings.ToValidUTF8(host, "\uFFFD"))
	clean = strings.TrimSpace(clean)
	if utf8.RuneCountInString(clean) > maxName {
		clean = strings.TrimSpace(string([]rune(clean)[:maxName]))
	}
	if clean == "" {
		return DefaultName
	}
	return clean
}

// Platform is the system of the computer the site knows.
type Platform string

const (
	PlatformWindows Platform = "windows"
	PlatformLinux   Platform = "linux"
)

// PlatformOf is the system of a GOOS, when the site knows it.
func PlatformOf(goos string) (Platform, bool) {
	switch platform := Platform(goos); platform {
	case PlatformWindows, PlatformLinux:
		return platform, true
	}
	return "", false
}

// Startable says whether the site can return to this build: its window on 20000–29999, a system it knows and a version
// of the app it takes (AC1.1).
func Startable(port int, platform Platform, version string) error {
	if port < MinPort || port > MaxPort {
		return ErrCannotStart
	}
	if _, ok := PlatformOf(string(platform)); !ok {
		return ErrCannotStart
	}
	if version == "" || len(version) > maxVersion {
		return ErrCannotStart
	}
	for _, sign := range version {
		if !asciiAlnum(sign) && sign != '.' && sign != '-' && sign != '+' {
			return ErrCannotStart
		}
	}
	return nil
}

// LinkAddress is the page /link of the site with what the app asks it (the site's link-request.ts): the challenge, the
// state, the port of the return and the system — nothing of the author.
func LinkAddress(site, challenge, state string, port int, platform Platform) string {
	query := url.Values{"challenge": {challenge}, "state": {state}, "port": {strconv.Itoa(port)}, "platform": {string(platform)}}
	return strings.TrimRight(site, "/") + "/link?" + query.Encode()
}

// Pages are the pages of the site the page of the app opens: the tiers and «Приложения на компьютере» of «Аккаунт»
// (AC2.1), built from the address of the build.
type Pages struct {
	Tariffs string
	Apps    string
}

func PagesOf(site string) Pages {
	base := strings.TrimRight(site, "/")
	return Pages{Tariffs: base + "/tariffs", Apps: base + "/account#apps"}
}

// HostOf is the host of the address of the site, as «куда ходит приложение» names it (AC2.7).
func HostOf(site string) string {
	parsed, err := url.Parse(site)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// Tier is the tier of the site in force for the account: its code, title and end (none — without term).
type Tier struct {
	Code  string
	Title string
	Until *time.Time
}

// Counter is the use of one limit of the account and the limit.
type Counter struct {
	Used  int64
	Limit int64
}

// Account is what the site says of the account a pass works for (AC2.1): the name of the author, his address hidden
// after its first letter, his tier, the questions of his Moscow day and the characters the helper read for him in 30
// days. Quota is false when the site said nothing of the questions — the answer of the exchange of a code.
type Account struct {
	Name      string
	Email     string
	Tier      Tier
	Questions Counter
	Reading   Counter
	Quota     bool
	// Helper is the helper of the site for the app and Work the work of its reading of 30 days with its bound
	// (specs/2026-11-13-desk-account-ai.md AI5.1); ActiveTurns and Imports are the «разом» and the uploads of the day the
	// site counts — Quota says the answer had them (AI2.11).
	Helper      Helper
	Work        Counter
	ActiveTurns Counter
	Imports     Counter
	Day         string
	ResetsAt    *time.Time
	// ThisApp is the live permits of turns of this app among ActiveTurns.Used, told at the same moment
	// (specs/2026-11-15-desk-turns-here.md TH1.1); nil — the site did not tell it.
	ThisApp *int64
}

// PlacesOf is who holds the places of «разом» (specs/2026-11-15-desk-turns-here.md TH2.1): used and thisApp as the site told
// them at one moment, answering the turns of the account of this link not over here, of any saga. Held are the places the
// site still counts for questions of this app already over here; elsewhere — the site and the other apps of the account. An
// answer told before a turn began or one that does not agree with itself goes no lower than nothing.
func PlacesOf(used, thisApp, answering int64) quota.ActivePlaces {
	answering = max(0, answering)
	return quota.ActivePlaces{Answering: answering, Held: max(0, thisApp-answering), Elsewhere: max(0, used-thisApp)}
}

// Helper is what the site tells of its helper for the app (AI5.1): the names of its models for the page, the window of the
// context of the chat and its threshold of compaction. Zero when the site told none.
type Helper struct {
	ChatModel     string
	CardsModel    string
	ContextWindow int64
	CompactLimit  int64
}

// Exchange is what the app sends the site for its pass: the code of the return, the verifier of its waiting, the name of
// the computer and the version of the app (AC1.3).
type Exchange struct {
	Code       string
	Verifier   string
	Name       string
	AppVersion string
}

// Waiting is a link started and not returned yet: the verifier, the state, until when it waits and the address of /link
// the page opens — again with the same state and term while it lives (AC1.1) —, and whether the author started it to
// choose the helper of the account (specs/2026-11-15-desk-account-link-web.md AW3.2): the intent lives with the waiting.
type Waiting struct {
	Verifier     string
	State        string
	Address      string
	Expires      time.Time
	ChooseHelper bool
}

// Waits keep the one waiting of the app by the pattern of the one-time codes of the window (domain/desktop.OpenCodes):
// only in memory, the state compared in constant time; a wrong state does not spend the right one, the right one is
// spent once before the exchange, and a late or canceled one is spent for nobody (AC1.2). A waiting whose term ended
// without a return is told once (Lapsed, AW1.6).
type Waits struct {
	mu      sync.Mutex
	now     func() time.Time
	waiting *Waiting
	lapsed  bool
}

// NewWaits keep the waiting against the clock now.
func NewWaits(now func() time.Time) *Waits {
	return &Waits{now: now}
}

// Begin makes waiting the one waiting; a waiting before it that ran out is not told any more.
func (waits *Waits) Begin(waiting Waiting) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	waits.waiting, waits.lapsed = &waiting, false
}

// Current is the waiting while it lives.
func (waits *Waits) Current() (Waiting, bool) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.ranOut() || waits.waiting == nil {
		return Waiting{}, false
	}
	return *waits.waiting, true
}

// Lapsed reports once that the waiting ended by its term without a return (specs/2026-11-15-desk-account-link-web.md
// AW1.6); a canceled or a spent one never does.
func (waits *Waits) Lapsed() bool {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	waits.ranOut()
	lapsed := waits.lapsed
	waits.lapsed = false
	return lapsed
}

// ranOut drops a waiting whose term ended and marks it to be told; the lock is held.
func (waits *Waits) ranOut() bool {
	if waits.waiting == nil || waits.now().Before(waits.waiting.Expires) {
		return false
	}
	waits.waiting, waits.lapsed = nil, true
	return true
}

// Cancel ends the waiting.
func (waits *Waits) Cancel() {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	waits.waiting, waits.lapsed = nil, false
}

// Spend spends the waiting of the state: once, while it lives; any other state is ErrStateInvalid and spends nothing. A
// late return is refused and leaves the term that ran out to be told.
func (waits *Waits) Spend(state string) (Waiting, error) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.waiting == nil || subtle.ConstantTimeCompare([]byte(waits.waiting.State), []byte(state)) != 1 {
		return Waiting{}, ErrStateInvalid
	}
	if waits.ranOut() {
		return Waiting{}, ErrStateInvalid
	}
	waiting := *waits.waiting
	waits.waiting = nil
	return waiting, nil
}

func asciiAlnum(sign rune) bool {
	return sign >= 'a' && sign <= 'z' || sign >= 'A' && sign <= 'Z' || sign >= '0' && sign <= '9'
}
