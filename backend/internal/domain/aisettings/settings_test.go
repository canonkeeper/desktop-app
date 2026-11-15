// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

func text(value string) *string { return &value }
func number(value int) *int     { return &value }

// specs/2026-11-13-desk-secrets.md SE2.1: a ready variant gives the address and the protocol by the table Data, a custom
// one takes the author's address and auto unless he chose; «Дополнительно» left out takes its defaults.
func TestAReadyVariantGivesTheAddressAndTheProtocol(t *testing.T) {
	for _, want := range []struct {
		preset   Preset
		baseURL  string
		protocol Protocol
	}{
		{PresetDeepSeek, "https://api.deepseek.com/v1", ProtocolChat},
		{PresetOpenAI, "https://api.openai.com/v1", ProtocolResponses},
		{PresetOllama, "http://127.0.0.1:11434/v1", ProtocolChat},
	} {
		settings, err := Draft{Source: "own", Own: &OwnDraft{Preset: string(want.preset)}}.Settings()
		if err != nil || settings.Source != SourceOwn || settings.Own == nil {
			t.Fatalf("%s = %+v, %v", want.preset, settings, err)
		}
		if settings.Own.BaseURL != want.baseURL || settings.Own.Protocol != want.protocol {
			t.Fatalf("%s gives %s %s", want.preset, settings.Own.BaseURL, settings.Own.Protocol)
		}
		same, err := Draft{Source: "own", Own: &OwnDraft{Preset: string(want.preset), BaseURL: text(want.baseURL), Protocol: text(string(want.protocol))}}.Settings()
		if err != nil || *same.Own != *settings.Own {
			t.Fatalf("%s named with its own address = %+v, %v", want.preset, same.Own, err)
		}
	}
	settings, err := Draft{Source: "own", Own: &OwnDraft{Preset: "custom", BaseURL: text("https://gateway.example/v1")}}.Settings()
	if err != nil || settings.Own.BaseURL != "https://gateway.example/v1" || settings.Own.Protocol != ProtocolAuto {
		t.Fatalf("custom = %+v, %v", settings.Own, err)
	}
	settings, err = Draft{Source: "none", Own: &OwnDraft{Preset: "custom", BaseURL: text("http://192.168.1.5:8080/v1"), Protocol: text("chat")}}.Settings()
	if err != nil || settings.Source != SourceNone || settings.Own.Protocol != ProtocolChat {
		t.Fatalf("custom kept with the source none = %+v, %v", settings, err)
	}
	advanced := settings.Own.Advanced
	if advanced.ContextWindow != 128_000 || advanced.MaxOutput != 16_000 || advanced.Reasoning != ReasoningMedium ||
		advanced.Parallel != 4 || advanced.PriceIn != nil || advanced.PriceOut != nil || settings.Own.ChatModel != "" {
		t.Fatalf("the defaults of «Дополнительно» = %+v", advanced)
	}
	if none, err := (Draft{Source: "none"}).Settings(); err != nil || none.Source != SourceNone || none.Own != nil {
		t.Fatalf("none = %+v, %v", none, err)
	}
}

func TestTheAuthorsFieldsAreKept(t *testing.T) {
	settings, err := Draft{Source: "own", Own: &OwnDraft{
		Preset: "deepseek", ChatModel: text("deepseek-chat"), CardsModel: text("deepseek-reasoner"),
		Advanced: AdvancedDraft{
			ContextWindow: number(8_000), MaxOutput: number(200_000), Reasoning: text("none"), Parallel: number(32),
			PriceIn: text("0.27"), PriceOut: text("1000"),
		},
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	own := settings.Own
	if own.ChatModel != "deepseek-chat" || own.CardsModel != "deepseek-reasoner" || own.Advanced.ContextWindow != 8_000 ||
		own.Advanced.MaxOutput != 200_000 || own.Advanced.Reasoning != ReasoningNone || own.Advanced.Parallel != 32 ||
		own.Advanced.PriceIn.String() != "0.27" || own.Advanced.PriceOut.String() != "1000" {
		t.Fatalf("the profile = %+v, prices %v %v", own, own.Advanced.PriceIn, own.Advanced.PriceOut)
	}
}

// SE2.2: a field that is not valid is refused by its name, and the error holds nothing the author wrote.
func TestAFieldThatIsNotValidIsRefusedByItsName(t *testing.T) {
	custom := func(change func(*OwnDraft)) Draft {
		own := &OwnDraft{Preset: "custom", BaseURL: text("https://gateway.example/v1")}
		change(own)
		return Draft{Source: "own", Own: own}
	}
	long := "https://gateway.example/" + strings.Repeat("в", 300-len("https://gateway.example/")+1)
	for field, drafts := range map[string][]Draft{
		"source": {{Source: ""}, {Source: "chatgpt"}, {Source: "Own"}},
		"own":    {{Source: "own"}},
		"own.preset": {
			{Source: "own", Own: &OwnDraft{Preset: "openrouter"}}, {Source: "none", Own: &OwnDraft{Preset: ""}},
		},
		"own.baseUrl": {
			{Source: "own", Own: &OwnDraft{Preset: "openai", BaseURL: text("https://api.openai.com/v2")}},
			custom(func(own *OwnDraft) { own.BaseURL = nil }),
			custom(func(own *OwnDraft) { own.BaseURL = text("ftp://gateway.example/v1") }),
			custom(func(own *OwnDraft) { own.BaseURL = text("https://user:password-of-the-test@gateway.example.test/v1") }),
			custom(func(own *OwnDraft) { own.BaseURL = text("https://user@gateway.example.test/v1") }),
			custom(func(own *OwnDraft) { own.BaseURL = text("gateway.example/v1") }),
			custom(func(own *OwnDraft) { own.BaseURL = text("https:///v1") }),
			custom(func(own *OwnDraft) { own.BaseURL = text("https://gateway.example/v 1") }),
			custom(func(own *OwnDraft) { own.BaseURL = text(long) }),
		},
		"own.protocol": {
			{Source: "own", Own: &OwnDraft{Preset: "deepseek", Protocol: text("responses")}},
			custom(func(own *OwnDraft) { own.Protocol = text("websocket") }),
		},
		"own.chatModel": {
			custom(func(own *OwnDraft) { own.ChatModel = text(strings.Repeat("m", 121)) }),
			custom(func(own *OwnDraft) { own.ChatModel = text("chat\nmodel") }),
		},
		"own.cardsModel": {custom(func(own *OwnDraft) { own.CardsModel = text("cards\x00") })},
		"own.advanced.contextWindow": {
			custom(func(own *OwnDraft) { own.Advanced.ContextWindow = number(7_999) }),
			custom(func(own *OwnDraft) { own.Advanced.ContextWindow = number(2_000_001) }),
		},
		"own.advanced.maxOutput": {
			custom(func(own *OwnDraft) { own.Advanced.MaxOutput = number(999) }),
			custom(func(own *OwnDraft) { own.Advanced.MaxOutput = number(200_001) }),
		},
		"own.advanced.reasoning": {custom(func(own *OwnDraft) { own.Advanced.Reasoning = text("max") })},
		"own.advanced.parallel": {
			custom(func(own *OwnDraft) { own.Advanced.Parallel = number(0) }),
			custom(func(own *OwnDraft) { own.Advanced.Parallel = number(33) }),
		},
		"own.advanced.priceInPerMillion": {
			custom(func(own *OwnDraft) { own.Advanced.PriceIn = text("1000000.0001") }),
			custom(func(own *OwnDraft) { own.Advanced.PriceIn = text("-1") }),
			custom(func(own *OwnDraft) { own.Advanced.PriceIn = text("0.00001") }),
			custom(func(own *OwnDraft) { own.Advanced.PriceIn = text("1e2") }),
		},
		"own.advanced.priceOutPerMillion": {custom(func(own *OwnDraft) { own.Advanced.PriceOut = text(".5") })},
	} {
		for _, draft := range drafts {
			_, err := draft.Settings()
			var refused *FieldError
			if !errors.As(err, &refused) || refused.Field != field {
				t.Errorf("%+v %+v = %v; want the field %s", draft, draft.Own, err, field)
			}
			if err != nil && strings.Contains(err.Error(), "password-of-the-test") {
				t.Errorf("the error holds what the author wrote: %v", err)
			}
		}
	}
}

// specs/2026-11-13-desk-consent.md CN1.1: a price goes up to 1 000 000 for a million tokens in any currency.
func TestAPriceHasAtMostFourDigitsAfterThePoint(t *testing.T) {
	for text, want := range map[string]string{"0": "0", "0.27": "0.27", "1.10": "1.1", "0.0001": "0.0001", "1000": "1000", "999.9999": "999.9999", "015": "15",
		"6000": "6000", "1000000": "1000000", "999999.9999": "999999.9999"} {
		price, ok := ParsePrice(text)
		if !ok || price.String() != want {
			t.Errorf("ParsePrice(%q) = %s, %v; want %s", text, price, ok, want)
		}
	}
	for _, text := range []string{"", ".", "1.", ".5", "1000000.01", "10000000", "0.12345", "1,5", "+1", "１"} {
		if _, ok := ParsePrice(text); ok {
			t.Errorf("ParsePrice(%q) took it", text)
		}
	}
}

// SE2.5: a key is up to 1280 bytes without spaces and control characters at its ends or inside — what every store of the
// shell keeps whatever its characters: the Credential Manager of Windows holds 2560 bytes of UTF-16 (SE1.1).
func TestAKeyIsOneWordThatEveryStoreOfTheShellKeeps(t *testing.T) {
	for _, key := range []string{"key-test-0001", "ключ-без-пробелов", strings.Repeat("k", 1280), strings.Repeat("ж", 640), strings.Repeat("𝔨", 320)} {
		if err := CheckKey(key); err != nil {
			t.Errorf("CheckKey(%d bytes) = %v", len(key), err)
		}
		if units := len(utf16.Encode([]rune(key))); units*2 > 2560 {
			t.Errorf("a key of %d bytes takes %d bytes of UTF-16: the Credential Manager does not keep it", len(key), units*2)
		}
	}
	for _, key := range []string{"", " sk-test", "sk-test ", "sk test", "sk-test\n", "sk\ttest", "sk test", "sk\x7ftest", strings.Repeat("k", 1281), strings.Repeat("ж", 641), strings.Repeat("k", 4096), "sk-\xff"} {
		if err := CheckKey(key); !errors.Is(err, ErrKeyInvalid) || strings.Contains(err.Error(), "sk") {
			t.Errorf("CheckKey(%q) = %v", key, err)
		}
	}
}

// specs/2026-11-13-desk-gateway.md GW4.1: the helper is connected by one rule — «Свой ИИ» chosen, both models named and the
// key held when the variant needs one (SE2 Data: DeepSeek and OpenAI need it, Ollama does not, a custom address may).
func TestTheHelperIsConnectedByOneRule(t *testing.T) {
	own := func(preset string, chat, cards *string) Settings {
		settings, err := Draft{Source: "own", Own: &OwnDraft{Preset: preset, BaseURL: text("https://gateway.example/v1"), ChatModel: chat, CardsModel: cards}}.Settings()
		if preset != "custom" {
			settings, err = Draft{Source: "own", Own: &OwnDraft{Preset: preset, ChatModel: chat, CardsModel: cards}}.Settings()
		}
		if err != nil {
			t.Fatal(err)
		}
		return settings
	}
	for _, check := range []struct {
		what      string
		settings  Settings
		key, want bool
	}{
		{"nothing chosen", None(), true, false},
		{"the profile kept but none chosen", Settings{Source: SourceNone, Own: own("ollama", text("a"), text("b")).Own}, true, false},
		{"no model of the chat", own("deepseek", nil, text("b")), true, false},
		{"no model of the reading", own("deepseek", text("a"), text("")), true, false},
		{"DeepSeek without its key", own("deepseek", text("a"), text("b")), false, false},
		{"DeepSeek with its key", own("deepseek", text("a"), text("b")), true, true},
		{"OpenAI without its key", own("openai", text("a"), text("b")), false, false},
		{"Ollama without a key", own("ollama", text("a"), text("b")), false, true},
		{"a custom address without a key", own("custom", text("a"), text("b")), false, true},
		{"a custom address with a key", own("custom", text("a"), text("b")), true, true},
	} {
		if got := check.settings.Connected(Held{Key: check.key}); got != check.want {
			t.Errorf("%s: Connected = %v, want %v", check.what, got, check.want)
		}
	}
}

// specs/2026-11-13-desk-gateway.md GW2.1: the app-server of the chat runs with the window the author set, and with the
// default window before he set one.
func TestTheChatRunsWithTheWindowOfTheAuthor(t *testing.T) {
	if window := None().ContextWindow(); window != 128_000 {
		t.Fatalf("the window without settings = %d", window)
	}
	settings, err := Draft{Source: "own", Own: &OwnDraft{Preset: "ollama", Advanced: AdvancedDraft{ContextWindow: number(32_000)}}}.Settings()
	if err != nil || settings.ContextWindow() != 32_000 {
		t.Fatalf("the window of the author = %d, %v", settings.ContextWindow(), err)
	}
	settings.Source = SourceNone
	if settings.ContextWindow() != 32_000 {
		t.Fatal("the window of a kept profile is lost when the author goes back to none")
	}
}

// specs/2026-11-13-desk-account-ai.md AI1.1, AI1.2, AI1.3: the source «Аккаунт CanonKeeper» is connected while the app is
// linked — the pass held — and the site told its helper; the key of «Свой ИИ» says nothing of it, and the pass nothing of
// «Свой ИИ». The window of the chat and its threshold are the site's.
func TestTheHelperOfTheAccountIsConnectedByTheSameRule(t *testing.T) {
	told := &Account{ChatModel: "gpt-north-chat", CardsModel: "gpt-north-cards", ContextWindow: 1_000_000, CompactLimit: 950_000}
	settings, err := Draft{Source: "account"}.Settings()
	if err != nil || settings.Source != SourceAccount {
		t.Fatalf("the source account: %+v, %v", settings, err)
	}
	for _, check := range []struct {
		what     string
		settings Settings
		held     Held
		want     bool
	}{
		{"the site told nothing", settings, Held{Pass: true, Key: true}, false},
		{"linked", Settings{Source: SourceAccount, Account: told}, Held{Pass: true}, true},
		{"the link broken or undone", Settings{Source: SourceAccount, Account: told}, Held{Key: true}, false},
		{"own with the pass only", Settings{Source: SourceOwn, Own: &Own{Preset: PresetDeepSeek, ChatModel: "a", CardsModel: "b"}, Account: told}, Held{Pass: true}, false},
		{"none linked", Settings{Source: SourceNone, Account: told}, Held{Pass: true, Key: true}, false},
	} {
		if got := check.settings.Connected(check.held); got != check.want {
			t.Errorf("%s: Connected = %v, want %v", check.what, got, check.want)
		}
	}
	linked := Settings{Source: SourceAccount, Account: told, Own: &Own{Advanced: Advanced{ContextWindow: 32_000}}}
	if linked.ContextWindow() != 1_000_000 || linked.CompactLimit() != 950_000 {
		t.Fatalf("the window of the account = %d, %d", linked.ContextWindow(), linked.CompactLimit())
	}
	own := Settings{Source: SourceOwn, Account: told, Own: &Own{Advanced: Advanced{ContextWindow: 32_000}}}
	if own.ContextWindow() != 32_000 || own.CompactLimit() != 28_800 {
		t.Fatalf("the window of «Свой ИИ» = %d, %d", own.ContextWindow(), own.CompactLimit())
	}
	for _, bad := range []Account{
		{ChatModel: "a", CardsModel: "b", ContextWindow: 0, CompactLimit: 0},
		{ChatModel: "a", CardsModel: "b", ContextWindow: 100_000, CompactLimit: 100_000},
		{ChatModel: "", CardsModel: "b", ContextWindow: 100_000, CompactLimit: 90_000},
		{ChatModel: "a", CardsModel: "b", ContextWindow: 3_000_000, CompactLimit: 90_000},
	} {
		if bad.Valid() {
			t.Errorf("an account of the site that cannot run Codex is valid: %+v", bad)
		}
	}
	if !told.Valid() {
		t.Fatal("the helper of the site is not valid")
	}
}

// specs/2026-11-15-desk-account-link-web.md AW3.2: a link made chooses the helper of the account when the author started it to
// choose it, or when no helper is chosen yet; «Свой ИИ» chosen without the intent stays. The profile of «Свой ИИ» stays in
// every case, and without the helper the site told the source does not change (AI1.1, AI1.3).
func TestALinkMadeChoosesTheHelperOfTheAccountByTheIntent(t *testing.T) {
	own := &Own{Preset: PresetDeepSeek, ChatModel: "deepseek-chat", CardsModel: "deepseek-chat", Advanced: Advanced{ContextWindow: 64_000, Currency: CurrencyRUB}}
	told := &Account{ChatModel: "gpt-north-chat", CardsModel: "gpt-north-cards", ContextWindow: 1_000_000, CompactLimit: 950_000}
	for _, test := range []struct {
		name   string
		before Settings
		chose  bool
		source Source
	}{
		{"own with the intent", Settings{Source: SourceOwn, Own: own, Account: told}, true, SourceAccount},
		{"own without the intent", Settings{Source: SourceOwn, Own: own, Account: told}, false, SourceOwn},
		{"none without the intent", Settings{Source: SourceNone, Own: own, Account: told}, false, SourceAccount},
		{"none with the intent", Settings{Source: SourceNone, Account: told}, true, SourceAccount},
		{"account again", Settings{Source: SourceAccount, Own: own, Account: told}, false, SourceAccount},
		{"no helper told", Settings{Source: SourceNone, Own: own}, true, SourceNone},
	} {
		after := test.before.Linked(test.chose)
		if after.Source != test.source || after.Own != test.before.Own || after.Account != test.before.Account {
			t.Errorf("%s: %+v", test.name, after)
		}
	}
}
