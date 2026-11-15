// SPDX-License-Identifier: AGPL-3.0-only

// Package aisettings holds the settings of «Свой ИИ» of the desktop app (specs/2026-11-13-desk-secrets.md SE2): one
// profile with ready variants — the variant gives the address and the protocol — the models of the chat and of the
// reading of the books, and the advanced fields with their defaults; the checks of a write and of a key.
package aisettings

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/desktop"
)

// Source is the AI the author chose: none yet, his own, or the helper of his account of the site
// (specs/2026-11-13-desk-account-ai.md AI1.1).
type Source string

const (
	SourceNone    Source = "none"
	SourceOwn     Source = "own"
	SourceAccount Source = "account"
)

// Preset is a ready variant of «Свой ИИ».
type Preset string

const (
	PresetDeepSeek Preset = "deepseek"
	PresetOpenAI   Preset = "openai"
	PresetOllama   Preset = "ollama"
	// PresetCustom is «Другой, совместимый с OpenAI»: the author names the address.
	PresetCustom Preset = "custom"
)

// Protocol is how the provider is spoken to; auto finds it out.
type Protocol string

const (
	ProtocolResponses Protocol = "responses"
	ProtocolChat      Protocol = "chat"
	ProtocolAuto      Protocol = "auto"
)

// Reasoning is how much the model reasons.
type Reasoning string

const (
	ReasoningNone   Reasoning = "none"
	ReasoningLow    Reasoning = "low"
	ReasoningMedium Reasoning = "medium"
	ReasoningHigh   Reasoning = "high"
)

// endpoint is what a ready variant gives (the table Data of SE2).
type endpoint struct {
	baseURL  string
	protocol Protocol
}

var presets = map[Preset]endpoint{
	PresetDeepSeek: {"https://api.deepseek.com/v1", ProtocolChat},
	PresetOpenAI:   {"https://api.openai.com/v1", ProtocolResponses},
	PresetOllama:   {"http://127.0.0.1:11434/v1", ProtocolChat},
}

// The bounds of the fields (SE2.2 and the table Data).
const (
	maxBaseURL       = 300
	maxModel         = 120
	minContextWindow = 8_000
	maxContextWindow = 2_000_000
	minMaxOutput     = 1_000
	maxMaxOutput     = 200_000
	minParallel      = 1
	maxParallel      = MaxParallel
	maxPriceUnits    = 1_000_000 * priceScale
	priceScale       = 10_000
	priceDecimals    = 4
	defaultContext   = 128_000
	defaultMaxOutput = 16_000
	defaultParallel  = 4
	defaultReasoning = ReasoningMedium
	defaultCustom    = ProtocolAuto
)

// MaxParallel is the most requests at once the author may set: the client of the core of the app takes as many places,
// and the local gateway keeps the number the author set (specs/2026-11-13-desk-gateway.md GW1.5).
const MaxParallel = 32

// Currency is the currency of the prices of «Свой ИИ» (specs/2026-11-13-desk-consent.md CN1.1): its ISO 4217 code.
type Currency string

const (
	CurrencyRUB Currency = "RUB"
	CurrencyUSD Currency = "USD"
	CurrencyCNY Currency = "CNY"
	CurrencyEUR Currency = "EUR"
)

// Valid says the currency is one the author may choose.
func (currency Currency) Valid() bool {
	switch currency {
	case CurrencyRUB, CurrencyUSD, CurrencyCNY, CurrencyEUR:
		return true
	default:
		return false
	}
}

// Price is a price for a million tokens in the currency of the settings, kept in ten-thousandths of its unit.
type Price int64

// ParsePrice reads a price as the author writes it: 0 to 1 000 000 in any currency (CN1.1), with at most four digits after
// the point.
func ParsePrice(text string) (Price, bool) {
	whole, fraction, dotted := strings.Cut(text, ".")
	if whole == "" || len(whole) > 7 || dotted && (fraction == "" || len(fraction) > priceDecimals) ||
		!digits(whole) || !digits(fraction) {
		return 0, false
	}
	units, err := strconv.ParseInt(whole+fraction+strings.Repeat("0", priceDecimals-len(fraction)), 10, 64)
	if err != nil || units > maxPriceUnits {
		return 0, false
	}
	return Price(units), true
}

func digits(text string) bool {
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// String writes the price without the zeros that say nothing: "0.27", "1000".
func (price Price) String() string {
	whole, fraction := int64(price)/priceScale, int64(price)%priceScale
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strconv.FormatInt(whole, 10) + "." + strings.TrimRight(strconv.FormatInt(priceScale+fraction, 10)[1:], "0")
}

// Advanced are the fields under «Дополнительно».
type Advanced struct {
	ContextWindow int
	MaxOutput     int
	Reasoning     Reasoning
	Parallel      int
	// Currency is the currency of the prices, the dollar by default (CN1.1).
	Currency Currency
	// PriceIn and PriceOut are the prices of a million tokens in and out, when the author named them; PriceCache the
	// price of a million cached tokens in, when he named it — without it the cache goes at PriceIn.
	PriceIn, PriceOut, PriceCache *Price
}

// Own is the profile of «Свой ИИ».
type Own struct {
	Preset   Preset
	BaseURL  string
	Protocol Protocol
	// ChatModel answers the chat, CardsModel reads the books; empty — not chosen yet.
	ChatModel  string
	CardsModel string
	Advanced   Advanced
}

// Settings are the settings of the AI of the app: the source and the profile of «Свой ИИ», which stays when the author
// goes back to none, and the helper of the account as the site told it last.
type Settings struct {
	Source Source
	Own    *Own
	// Account is the helper «Аккаунт CanonKeeper» the site told last (specs/2026-11-13-desk-account-ai.md AI1.1, AI1.3):
	// the author sets nothing of it; nil until the site told it.
	Account *Account
}

// Account is what the site tells of its helper for the app (AI1.1, AI1.3; the site's AI5.1): the names of the model of the
// chat and of the reading of the books, for the page, and the window of the context of the chat and its threshold of
// compaction Codex of the app runs with.
type Account struct {
	ChatModel     string
	CardsModel    string
	ContextWindow int
	CompactLimit  int
}

// Valid says Codex of the app can run with the helper the site told: both models named, a window within the bounds of a
// window of the author and a threshold of compaction below it.
func (account Account) Valid() bool {
	_, chat := model(&account.ChatModel)
	_, cards := model(&account.CardsModel)
	return chat && cards && account.ChatModel != "" && account.CardsModel != "" &&
		account.ContextWindow >= minContextWindow && account.ContextWindow <= maxContextWindow &&
		account.CompactLimit > 0 && account.CompactLimit < account.ContextWindow
}

// Held is what the backend holds for the helper of the app: the key of «Свой ИИ» and the pass of the link with an account.
type Held struct {
	Key  bool
	Pass bool
}

// None are the settings of an app whose author chose nothing yet.
func None() Settings {
	return Settings{Source: SourceNone}
}

// ContextWindow is the window of the chat the app-server of the app runs with (specs/2026-11-13-desk-gateway.md GW2.1): the
// site's for the helper of the account (specs/2026-11-13-desk-account-ai.md AI1.3), else the author's, or the default one
// before he set any.
func (settings Settings) ContextWindow() int {
	if settings.Source == SourceAccount && settings.Account != nil {
		return settings.Account.ContextWindow
	}
	if settings.Own != nil {
		return settings.Own.Advanced.ContextWindow
	}
	return defaultContext
}

// CompactLimit is the threshold of compaction of the history of the chat: the site's for the helper of the account (AI1.3),
// otherwise 90 % of the window (GW2.1).
func (settings Settings) CompactLimit() int {
	if settings.Source == SourceAccount && settings.Account != nil {
		return settings.Account.CompactLimit
	}
	return settings.ContextWindow() * 9 / 10
}

// NeedsKey says whether a variant answers only with the author's key (SE2 Data): DeepSeek and OpenAI do; Ollama on the
// computer of the author takes none, and an address of his own may take one or not.
func (preset Preset) NeedsKey() bool {
	return preset == PresetDeepSeek || preset == PresetOpenAI
}

// Connected is the one rule «помощник подключён» (specs/2026-11-13-desk-gateway.md GW4.1): «Свой ИИ» is chosen, both
// models are named and the backend holds the key when the variant needs one; the helper of the account is chosen, the
// site told it and the app is linked — the pass held (specs/2026-11-13-desk-account-ai.md AI1.2): no answer of the site
// does not undo it, a broken link and an unlinking do. The status of a saga, the queue of the cards Job, the gateway and
// the turn of the chat read it.
func (settings Settings) Connected(held Held) bool {
	switch settings.Source {
	case SourceOwn:
		own := settings.Own
		return own != nil && own.ChatModel != "" && own.CardsModel != "" && (held.Key || !own.Preset.NeedsKey())
	case SourceAccount:
		return settings.Account != nil && held.Pass
	default:
		return false
	}
}

// Linked are the settings after a link with an account made (specs/2026-11-15-desk-account-link-web.md AW3.2): the helper
// of the account is chosen when the author started the link to choose it or chose no helper yet; «Свой ИИ» chosen without
// that intent stays — he linked for the account alone or for later. The profile of «Свой ИИ» stays in every case, and
// without the helper the site told the source does not change (AI1.1, AI1.3).
func (settings Settings) Linked(chose bool) Settings {
	if settings.Account != nil && (chose || settings.Source == SourceNone) {
		settings.Source = SourceAccount
	}
	return settings
}

// Draft is a write of the settings as the author sent it: a field he left out is nil — the variant gives the address and
// the protocol, «Дополнительно» its defaults, a model is not chosen.
type Draft struct {
	Source string
	Own    *OwnDraft
}

// OwnDraft is the profile of a Draft.
type OwnDraft struct {
	Preset     string
	BaseURL    *string
	Protocol   *string
	ChatModel  *string
	CardsModel *string
	Advanced   AdvancedDraft
}

// AdvancedDraft is «Дополнительно» of a Draft; a price is the text of its number.
type AdvancedDraft struct {
	ContextWindow *int
	MaxOutput     *int
	Reasoning     *string
	Parallel      *int
	PriceIn       *string
	PriceOut      *string
	// Currency and PriceCache left out keep what the settings had (SettingsOver); ClearPriceCache removes the price of the
	// cache (specs/2026-11-13-desk-consent.md CN1.1).
	Currency        *string
	PriceCache      *string
	ClearPriceCache bool
}

// FieldError names the field of a write that is not valid by its name in the contract ("own.baseUrl"); nothing of what
// the author wrote is in it.
type FieldError struct {
	Field string
}

func (err *FieldError) Error() string {
	return "aisettings: the field " + err.Field + " is not valid"
}

func invalid(field string) error {
	return &FieldError{Field: field}
}

// SettingsOver checks a write of the author over the settings he had (specs/2026-11-13-desk-consent.md CN1.1): the currency
// and the price of the cache he left out are kept — a page that writes «Дополнительно» without them does not reset them —
// and every other field is as Settings gives it.
func (draft Draft) SettingsOver(previous Settings) (Settings, error) {
	if draft.Own != nil && previous.Own != nil {
		advanced, kept := draft.Own.Advanced, previous.Own.Advanced
		if advanced.Currency == nil {
			currency := string(kept.Currency)
			advanced.Currency = &currency
		}
		if advanced.PriceCache == nil && !advanced.ClearPriceCache && kept.PriceCache != nil {
			price := kept.PriceCache.String()
			advanced.PriceCache = &price
		}
		own := *draft.Own
		own.Advanced = advanced
		draft.Own = &own
	}
	return draft.Settings()
}

// Settings checks the draft (SE2.1, SE2.2) and gives the settings as they are kept: a ready variant gives the address
// and the protocol — a draft that names others is refused — and a field left out takes its default.
func (draft Draft) Settings() (Settings, error) {
	settings := Settings{Source: Source(draft.Source)}
	if settings.Source != SourceNone && settings.Source != SourceOwn && settings.Source != SourceAccount {
		return Settings{}, invalid("source")
	}
	if draft.Own == nil {
		if settings.Source == SourceOwn {
			return Settings{}, invalid("own")
		}
		return settings, nil
	}
	own, err := draft.Own.own()
	if err != nil {
		return Settings{}, err
	}
	settings.Own = &own
	return settings, nil
}

func (draft OwnDraft) own() (Own, error) {
	own := Own{Preset: Preset(draft.Preset)}
	ready, isReady := presets[own.Preset]
	switch {
	case isReady:
		own.BaseURL, own.Protocol = ready.baseURL, ready.protocol
		if draft.BaseURL != nil && *draft.BaseURL != ready.baseURL {
			return Own{}, invalid("own.baseUrl")
		}
		if draft.Protocol != nil && Protocol(*draft.Protocol) != ready.protocol {
			return Own{}, invalid("own.protocol")
		}
	case own.Preset == PresetCustom:
		if draft.BaseURL == nil || !baseURL(*draft.BaseURL) {
			return Own{}, invalid("own.baseUrl")
		}
		own.BaseURL, own.Protocol = *draft.BaseURL, defaultCustom
		if draft.Protocol != nil {
			own.Protocol = Protocol(*draft.Protocol)
			if own.Protocol != ProtocolResponses && own.Protocol != ProtocolChat && own.Protocol != ProtocolAuto {
				return Own{}, invalid("own.protocol")
			}
		}
	default:
		return Own{}, invalid("own.preset")
	}
	var ok bool
	if own.ChatModel, ok = model(draft.ChatModel); !ok {
		return Own{}, invalid("own.chatModel")
	}
	if own.CardsModel, ok = model(draft.CardsModel); !ok {
		return Own{}, invalid("own.cardsModel")
	}
	advanced, err := draft.Advanced.advanced()
	if err != nil {
		return Own{}, err
	}
	own.Advanced = advanced
	return own, nil
}

func (draft AdvancedDraft) advanced() (Advanced, error) {
	advanced := Advanced{
		ContextWindow: defaultContext, MaxOutput: defaultMaxOutput, Reasoning: defaultReasoning, Parallel: defaultParallel,
		Currency: CurrencyUSD,
	}
	if !within(draft.ContextWindow, &advanced.ContextWindow, minContextWindow, maxContextWindow) {
		return Advanced{}, invalid("own.advanced.contextWindow")
	}
	if !within(draft.MaxOutput, &advanced.MaxOutput, minMaxOutput, maxMaxOutput) {
		return Advanced{}, invalid("own.advanced.maxOutput")
	}
	if draft.Reasoning != nil {
		advanced.Reasoning = Reasoning(*draft.Reasoning)
		switch advanced.Reasoning {
		case ReasoningNone, ReasoningLow, ReasoningMedium, ReasoningHigh:
		default:
			return Advanced{}, invalid("own.advanced.reasoning")
		}
	}
	if !within(draft.Parallel, &advanced.Parallel, minParallel, maxParallel) {
		return Advanced{}, invalid("own.advanced.parallel")
	}
	var ok bool
	if advanced.PriceIn, ok = price(draft.PriceIn); !ok {
		return Advanced{}, invalid("own.advanced.priceInPerMillion")
	}
	if advanced.PriceOut, ok = price(draft.PriceOut); !ok {
		return Advanced{}, invalid("own.advanced.priceOutPerMillion")
	}
	if draft.Currency != nil {
		if advanced.Currency = Currency(*draft.Currency); !advanced.Currency.Valid() {
			return Advanced{}, invalid("own.advanced.currency")
		}
	}
	if !draft.ClearPriceCache {
		if advanced.PriceCache, ok = price(draft.PriceCache); !ok {
			return Advanced{}, invalid("own.advanced.priceCachedInPerMillion")
		}
	}
	return advanced, nil
}

func within(value *int, target *int, low, high int) bool {
	if value == nil {
		return true
	}
	*target = *value
	return *value >= low && *value <= high
}

func price(text *string) (*Price, bool) {
	if text == nil {
		return nil, true
	}
	parsed, ok := ParsePrice(*text)
	return &parsed, ok
}

// baseURL is an address of a provider: http or https to a host, without a name and a password, up to 300 characters.
func baseURL(text string) bool {
	if text == "" || utf8.RuneCountInString(text) > maxBaseURL || strings.ContainsFunc(text, unicode.IsSpace) {
		return false
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// model is the name of a model: up to 120 characters without control characters; left out — not chosen.
func model(name *string) (string, bool) {
	if name == nil {
		return "", true
	}
	if !utf8.ValidString(*name) || utf8.RuneCountInString(*name) > maxModel || strings.ContainsFunc(*name, unicode.IsControl) {
		return "", false
	}
	return *name, true
}

// ErrKeyInvalid is a key that is empty, longer than desktop.MaxSecretBytes — what every store of the shell keeps — or
// holds a space or a control character.
var ErrKeyInvalid = errors.New("aisettings: the key is not valid")

// CheckKey checks a key of a provider before the shell keeps it (SE2.5); the error never holds the key.
func CheckKey(key string) error {
	if key == "" || len(key) > desktop.MaxSecretBytes || !utf8.ValidString(key) ||
		strings.ContainsFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return ErrKeyInvalid
	}
	return nil
}

// Host is the host of the provider of the profile, with its port: the address the text of the books goes to.
func (own Own) Host() string {
	parsed, err := url.Parse(own.BaseURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// Local says the model works on the computer of the author (specs/2026-11-13-desk-consent.md CN1.3): the variant Ollama or
// an address on the loopback — the text of the books does not leave the computer.
func (own Own) Local() bool {
	if own.Preset == PresetOllama {
		return true
	}
	parsed, err := url.Parse(own.BaseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
