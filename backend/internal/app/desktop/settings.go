// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
)

// The port of the window (specs/2026-11-12-desk-process.md PR1): the page keeps the place of reading, the theme and
// what is not saved in the storage of its origin, and the origin holds the port, so the backend listens on the same
// port from run to run. The first port is random of 20000–29999 — under the dynamic ports of Windows (49152+) and Linux
// (32768+), and two users of one computer get different ones.
const (
	firstWindowPort = 20000
	lastWindowPort  = 29999
	// windowPortTries new random ports are tried when the remembered one is taken.
	windowPortTries = 20
	// windowPortRetry is the wait before the remembered port is tried again: the backend of the previous run may still
	// be closing it.
	windowPortRetry = 2 * time.Second
	// settingsPort and settingsAI are the keys of settings.json the backend owns: the port of the window and the settings
	// of «Свой ИИ» (specs/2026-11-13-desk-secrets.md SE2.3); the shell and later slices keep their keys beside them.
	settingsPort = "port"
	settingsAI   = "ai"
	// settingsVersion is the version of the app that last applied the migrations or opened the data, settingsUpdated the
	// version the data were opened with before an update, until ready names it (specs/2026-11-13-desk-upgrade.md UG2.2,
	// UG2.5).
	settingsVersion = "version"
	settingsUpdated = "updatedFrom"
	// settingsWelcomed marks «Добро пожаловать» as closed, settingsUpdates holds the choice to check for a new version
	// once a day (specs/2026-11-13-desk-upgrade.md UG5.1): in settings.json, they live through a new port of the window
	// and a new installation; «Стереть все данные» takes them with the data.
	settingsWelcomed = "welcomed"
	settingsUpdates  = "updates"
)

// settings is settings.json of the data directory: the settings of the app that live with the data of the author. It is
// its one writer: the start remembers the port, the window replaces the settings of the AI while the backend runs.
type settings struct {
	mu     sync.Mutex
	path   string
	values map[string]json.RawMessage
	// port is the remembered port of the window, 0 when there is none; lost says the address the window had cannot be
	// told: the file was broken or unreadable, or its port was not a port.
	port int
	lost bool
	// ai are the settings of «Свой ИИ»: none without the section or with one that is broken or not valid.
	ai aisettings.Settings
	// check is the last check of «Свой ИИ», kept in the same section (specs/2026-11-13-desk-probes.md PB2.5).
	check *aisettings.Report
	// version opened the data last; updated is the version before an update the next ready names. Empty — unknown.
	version string
	updated string
	// welcomed: the author closed «Добро пожаловать»; checkUpdates: the app checks for a new version once a day — until
	// the author chose, as the switch of the welcome stands.
	welcomed     bool
	checkUpdates bool
}

// updatesFile is the section updates of settings.json.
type updatesFile struct {
	Daily *bool `json:"daily"`
}

// loadSettings reads settings.json; there is none at the first start. A file that cannot be read or is not a JSON
// object does not stop the start (PR1.4): it is put aside as settings.json.broken-<UTC time> as it was, and the start
// goes on with new settings.
func loadSettings(path string, now time.Time) (*settings, error) {
	current := &settings{path: path, values: map[string]json.RawMessage{}, ai: aisettings.None(), checkUpdates: true}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return current, nil
	}
	if err != nil || json.Unmarshal(raw, &current.values) != nil || current.values == nil {
		current.values, current.lost = map[string]json.RawMessage{}, true
		return current, os.Rename(path, path+".broken-"+now.UTC().Format("20060102T150405Z"))
	}
	if section, ok := current.values[settingsAI]; ok {
		current.ai, current.check = aiOfFile(section), checkOfFile(section)
	}
	current.version, current.updated = versionOfFile(current.values[settingsVersion]), versionOfFile(current.values[settingsUpdated])
	_ = json.Unmarshal(current.values[settingsWelcomed], &current.welcomed)
	var updates updatesFile
	if json.Unmarshal(current.values[settingsUpdates], &updates) == nil && updates.Daily != nil {
		current.checkUpdates = *updates.Daily
	}
	if remembered, ok := current.values[settingsPort]; ok {
		port, err := strconv.Atoi(string(remembered))
		if err != nil || port < 1 || port > 65535 {
			current.lost = true
			return current, nil
		}
		current.port = port
	}
	return current, nil
}

// keep remembers the port the window listens on (PR1.1) and says whether the address of the window changed — the page
// then finds the storage of its origin empty (PR1.2, PR1.4). The file is replaced whole: written beside it, then renamed
// over it, so a stop in the middle leaves the old file or the new one; the keys the backend does not know stay.
func (current *settings) keep(port int) (bool, error) {
	current.mu.Lock()
	defer current.mu.Unlock()
	changed := current.lost || current.port != 0 && current.port != port
	if current.port == port && !current.lost {
		return false, nil
	}
	if err := current.write(settingsPort, json.RawMessage(strconv.Itoa(port))); err != nil {
		return changed, err
	}
	current.port, current.lost = port, false
	return changed, nil
}

// AI are the settings of «Свой ИИ» the file keeps.
func (current *settings) AI() aisettings.Settings {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.ai
}

// KeepAI replaces the settings of «Свой ИИ» whole, the last check beside them stays; a write that fails leaves the file
// and the settings as they were.
func (current *settings) KeepAI(ai aisettings.Settings) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	encoded, err := json.Marshal(sectionOf(ai, current.check))
	if err != nil {
		return err
	}
	if err := current.write(settingsAI, encoded); err != nil {
		return err
	}
	current.ai = ai
	return nil
}

// Welcome says whether «Добро пожаловать» is still to be shown and whether the app checks for a new version once a day
// (UG5.1).
func (current *settings) Welcome() (bool, bool) {
	current.mu.Lock()
	defer current.mu.Unlock()
	return !current.welcomed, current.checkUpdates
}

// DismissWelcome marks «Добро пожаловать» as closed with the choice of the author; other keys of the section updates stay.
func (current *settings) DismissWelcome(checkUpdates bool) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	section := map[string]json.RawMessage{}
	_ = json.Unmarshal(current.values[settingsUpdates], &section)
	if section == nil {
		section = map[string]json.RawMessage{}
	}
	section["daily"], _ = json.Marshal(checkUpdates)
	updates, err := json.Marshal(section)
	if err != nil {
		return err
	}
	if err := current.writeAll(map[string]json.RawMessage{settingsWelcomed: json.RawMessage("true"), settingsUpdates: updates}); err != nil {
		return err
	}
	current.welcomed, current.checkUpdates = true, checkUpdates
	return nil
}

// Version is the version of the app that last applied the migrations or opened the data; empty when unknown (UG2.2).
func (current *settings) Version() string {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.version
}

// keepVersion remembers version as the one that opened the data — right after its migrations committed and after a start
// that went well (UG2.2) — and returns the version the data were opened with before an update to it: the earlier
// version when this one is later, kept until forgetUpdate, so a start that falls before ready names it at the next one
// (UG2.5). A program that does not know its version changes nothing.
func (current *settings) keepVersion(version string) (string, error) {
	current.mu.Lock()
	defer current.mu.Unlock()
	if plainVersion(version) == "" {
		return "", nil
	}
	updated := current.updated
	if newerVersion(version, current.version) {
		updated = current.version
	}
	if !newerVersion(version, updated) {
		updated = ""
	}
	if version == current.version && updated == current.updated {
		return updated, nil
	}
	encoded, _ := json.Marshal(version)
	changes := map[string]json.RawMessage{settingsVersion: encoded, settingsUpdated: nil}
	if updated != "" {
		changes[settingsUpdated], _ = json.Marshal(updated)
	}
	if err := current.writeAll(changes); err != nil {
		return "", err
	}
	current.version, current.updated = version, updated
	return updated, nil
}

// forgetUpdate takes the version before an update away once ready named it.
func (current *settings) forgetUpdate() error {
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.updated == "" {
		return nil
	}
	if err := current.writeAll(map[string]json.RawMessage{settingsUpdated: nil}); err != nil {
		return err
	}
	current.updated = ""
	return nil
}

// versionOfFile reads a version the file keeps; anything that is not a string of a version is none.
func versionOfFile(raw json.RawMessage) string {
	var version string
	if raw == nil || json.Unmarshal(raw, &version) != nil {
		return ""
	}
	return plainVersion(version)
}

// write replaces the file whole with one key changed; the keys the backend does not know stay.
func (current *settings) write(key string, value json.RawMessage) error {
	return current.writeAll(map[string]json.RawMessage{key: value})
}

// writeAll replaces the file whole with the keys of changes changed — a nil value takes its key away; the keys the backend
// does not know stay.
func (current *settings) writeAll(changes map[string]json.RawMessage) error {
	values := make(map[string]json.RawMessage, len(current.values)+len(changes))
	for name, kept := range current.values {
		values[name] = kept
	}
	for name, value := range changes {
		if value == nil {
			delete(values, name)
			continue
		}
		values[name] = value
	}
	encoded, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	if err := replaceFile(current.path, append(encoded, '\n')); err != nil {
		return err
	}
	current.values = values
	return nil
}

// aiFile is the section ai of settings.json: the settings of «Свой ИИ» in the words of the contract.
type aiFile struct {
	Source string     `json:"source"`
	Own    *ownAIFile `json:"own"`
	// Account is the helper of the account the site told last (specs/2026-11-13-desk-account-ai.md AI1.3).
	Account *accountAIFile `json:"account,omitempty"`
}

type accountAIFile struct {
	ChatModel     string `json:"chatModel"`
	CardsModel    string `json:"cardsModel"`
	ContextWindow int    `json:"contextWindow"`
	CompactLimit  int    `json:"compactLimit"`
}

type ownAIFile struct {
	Preset     string         `json:"preset"`
	BaseURL    *string        `json:"baseUrl"`
	Protocol   *string        `json:"protocol"`
	ChatModel  *string        `json:"chatModel"`
	CardsModel *string        `json:"cardsModel"`
	Advanced   advancedAIFile `json:"advanced"`
}

type advancedAIFile struct {
	ContextWindow *int         `json:"contextWindow"`
	MaxOutput     *int         `json:"maxOutput"`
	Reasoning     *string      `json:"reasoning"`
	Parallel      *int         `json:"parallel"`
	PriceIn       *json.Number `json:"priceInPerMillion"`
	PriceOut      *json.Number `json:"priceOutPerMillion"`
	// Currency and PriceCache are the currency of the prices and the price of the cache (specs/2026-11-13-desk-consent.md
	// CN1.1); a file without them has dollars and no price of the cache.
	Currency   *string      `json:"currency,omitempty"`
	PriceCache *json.Number `json:"priceCachedInPerMillion,omitempty"`
}

// aiOfFile reads the section by the rules of a write: a section that is broken or not valid is none (SE2.3).
func aiOfFile(section json.RawMessage) aisettings.Settings {
	var file aiFile
	if json.Unmarshal(section, &file) != nil {
		return aisettings.None()
	}
	draft := aisettings.Draft{Source: file.Source}
	if file.Own != nil {
		advanced := file.Own.Advanced
		draft.Own = &aisettings.OwnDraft{
			Preset: file.Own.Preset, BaseURL: file.Own.BaseURL, Protocol: file.Own.Protocol,
			ChatModel: file.Own.ChatModel, CardsModel: file.Own.CardsModel,
			Advanced: aisettings.AdvancedDraft{
				ContextWindow: advanced.ContextWindow, MaxOutput: advanced.MaxOutput, Reasoning: advanced.Reasoning,
				Parallel: advanced.Parallel, PriceIn: (*string)(advanced.PriceIn), PriceOut: (*string)(advanced.PriceOut),
				Currency: advanced.Currency, PriceCache: (*string)(advanced.PriceCache),
			},
		}
	}
	settings, err := draft.Settings()
	if err != nil {
		return aisettings.None()
	}
	if told := file.Account; told != nil {
		account := aisettings.Account{ChatModel: told.ChatModel, CardsModel: told.CardsModel, ContextWindow: told.ContextWindow, CompactLimit: told.CompactLimit}
		if account.Valid() {
			settings.Account = &account
		}
	}
	if settings.Source == aisettings.SourceAccount && settings.Account == nil {
		// The helper of the account runs only with what the site told (specs/2026-11-13-desk-account-ai.md AI1.3): a section
		// that chose it without is not valid.
		return aisettings.None()
	}
	return settings
}

func aiFileOf(settings aisettings.Settings) aiFile {
	file := aiFile{Source: string(settings.Source)}
	if told := settings.Account; told != nil {
		file.Account = &accountAIFile{ChatModel: told.ChatModel, CardsModel: told.CardsModel, ContextWindow: told.ContextWindow, CompactLimit: told.CompactLimit}
	}
	if own := settings.Own; own != nil {
		protocol, reasoning, currency := string(own.Protocol), string(own.Advanced.Reasoning), string(own.Advanced.Currency)
		file.Own = &ownAIFile{
			Preset: string(own.Preset), BaseURL: &own.BaseURL, Protocol: &protocol, ChatModel: &own.ChatModel, CardsModel: &own.CardsModel,
			Advanced: advancedAIFile{
				ContextWindow: &own.Advanced.ContextWindow, MaxOutput: &own.Advanced.MaxOutput, Reasoning: &reasoning,
				Parallel: &own.Advanced.Parallel, PriceIn: priceOfFile(own.Advanced.PriceIn), PriceOut: priceOfFile(own.Advanced.PriceOut),
				Currency: &currency, PriceCache: priceOfFile(own.Advanced.PriceCache),
			},
		}
	}
	return file
}

func priceOfFile(price *aisettings.Price) *json.Number {
	if price == nil {
		return nil
	}
	number := json.Number(price.String())
	return &number
}

// replaceFile writes a file of the user alone (0600) beside path and renames it over path.
func replaceFile(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	written := false
	defer func() {
		if !written {
			_ = os.Remove(file.Name())
		}
	}()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	written = true
	return nil
}

// windowPorts are how the window gets its listener: listen on a port of 127.0.0.1, a random port of the range, a wait.
type windowPorts struct {
	listen func(port int) (net.Listener, error)
	random func() int
	wait   func(time.Duration)
}

// systemWindowPorts listen only on 127.0.0.1 (PR1.3, DA3.1): never 0.0.0.0, :: or localhost.
var systemWindowPorts = windowPorts{
	listen: func(port int) (net.Listener, error) {
		return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	},
	random: func() int { return firstWindowPort + rand.IntN(lastWindowPort-firstWindowPort+1) },
	wait:   time.Sleep,
}

// open listens on the remembered port, on it again after windowPortRetry when it is busy or the system refused it, then
// on up to windowPortTries random ports (PR1.2); without a remembered port it starts with the random ones. When every
// attempt fails, the error is the listener's last.
func (ports windowPorts) open(remembered int) (net.Listener, error) {
	var err error
	if remembered != 0 {
		var listener net.Listener
		if listener, err = ports.listen(remembered); err == nil {
			return listener, nil
		}
		ports.wait(windowPortRetry)
		if listener, err = ports.listen(remembered); err == nil {
			return listener, nil
		}
	}
	for range windowPortTries {
		var listener net.Listener
		if listener, err = ports.listen(ports.random()); err == nil {
			return listener, nil
		}
	}
	return nil, err
}
