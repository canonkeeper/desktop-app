// SPDX-License-Identifier: AGPL-3.0-only

// Package coresteps names the calls of the core that give a model its instructions and one input (specs/2026-11-13-desk-proxy.md
// PX2.1; specs/2026-11-12-desktop-target.md DT4.3): the card of a chapter and its pass, the register of names, the steps of
// the thread layer, scan and the mapping of the author's materials. A linked app makes each of them a step of the site with
// the words of its core; the site takes the words only when their SHA-256 is a print of this tree or of a released
// version of the app. The package is the one owner of the names of the steps and of their words for the site and the app:
// the words themselves stay the constants of the core, and a call of the core without a step fails its test.
package coresteps

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// Name names a step.
type Name string

const (
	// Card is the card of one chapter.
	Card Name = "card"
	// CardPass is the pass over a written card: what the card missed.
	CardPass Name = "card_pass"
	// Names is the register of names of a saga.
	Names Name = "names"
	// LayerBook is the open threads of one book of the thread layer.
	LayerBook Name = "layer_book"
	// LayerReduce gathers the threads of all books into one register.
	LayerReduce Name = "layer_reduce"
	// LayerVerify checks one thread of the register by the text of its chapters.
	LayerVerify Name = "layer_verify"
	// LayerGap is the pass of the layer over what the register missed.
	LayerGap Name = "layer_gap"
	// Scan reads a part of a saga for a task of a turn of the chat.
	Scan Name = "scan"
	// Materials maps a section of a file of the author's materials.
	Materials Name = "materials"
)

// Permit is the kind of the permit of the site a step goes under (specs/2026-11-13-desk-account-ai.md AI5.2): the
// reading of the books, the turn of the chat whose scan it is, or the materials of the author. The site and the app read
// it here, from the one owner of the steps.
type Permit string

const (
	PermitReading   Permit = "reading"
	PermitChatTurn  Permit = "chat_turn"
	PermitMaterials Permit = "materials"
)

// Step is one step: its name, the words of the core of this tree and their print, the SHA-256 in hex, and the permit it
// goes under.
type Step struct {
	Name         Name
	Instructions string
	Print        string
	Permit       Permit
}

// steps are the steps of this tree in the order of a reading of a saga, then the turn and the materials.
var steps = func() []Step {
	words := []struct {
		name         Name
		instructions string
		permit       Permit
	}{
		{Card, sagamemory.CurrentCardInstructions.Text, PermitReading},
		{CardPass, sagamemory.CurrentCardInstructions.Pass, PermitReading},
		{Names, sagamemory.NameInstructions, PermitReading},
		{LayerBook, sagalayer.MapInstructions, PermitReading},
		{LayerReduce, sagalayer.ReduceInstructions, PermitReading},
		{LayerVerify, sagalayer.VerifyInstructions, PermitReading},
		{LayerGap, sagalayer.GapInstructions, PermitReading},
		{Scan, sagamemory.ScanInstructions, PermitChatTurn},
		{Materials, authormaterials.MapInstructions, PermitMaterials},
	}
	all := make([]Step, len(words))
	for index, step := range words {
		all[index] = Step{Name: step.name, Instructions: step.instructions, Print: printOf(step.instructions), Permit: step.permit}
	}
	return all
}()

// All are the steps of this tree.
func All() []Step {
	return append([]Step(nil), steps...)
}

// Named finds a step by its name.
func Named(name string) (Step, bool) {
	for _, step := range steps {
		if string(step.Name) == name {
			return step, true
		}
	}
	return Step{}, false
}

// Of finds the step of a call of the core by its words (specs/2026-11-13-desk-gateway.md GW1.7): the local gateway of the
// app names the purpose of a call so, and words that are no step of this tree are refused, never taken as free text.
func Of(instructions string) (Step, bool) {
	sum := printOf(instructions)
	for _, step := range steps {
		if step.Print == sum {
			return step, true
		}
	}
	return Step{}, false
}

// Accepts says whether the site takes the words for the step: the words of this tree, or words whose print a released
// version of the app sends (released.json). No other words: a step never takes free text for its instructions.
func Accepts(name Name, instructions string) bool {
	if step, ok := Named(string(name)); ok && step.Print == printOf(instructions) {
		return true
	}
	return releasedPrints.accepts(name, instructions)
}

// printOf is the SHA-256 of words in hex.
func printOf(instructions string) string {
	sum := sha256.Sum256([]byte(instructions))
	return hex.EncodeToString(sum[:])
}

// released.json lists the prints of the words of the steps of every released version of the app the site still serves
// (specs/2026-11-12-desktop-target.md DT12.4): a release writes its prints here; none is released yet.
//
//go:embed released.json
var releasedFile []byte

var releasedPrints = func() released {
	list, err := parseReleased(releasedFile)
	if err != nil {
		panic(err)
	}
	return list
}()

// released is the list of the prints of the released versions of the app.
type released struct {
	Releases []struct {
		Version string            `json:"version"`
		Prints  map[string]string `json:"prints"`
	} `json:"releases"`
}

func parseReleased(raw []byte) (released, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var list released
	if err := decoder.Decode(&list); err != nil {
		return released{}, fmt.Errorf("coresteps: released prints: %w", err)
	}
	for _, release := range list.Releases {
		if release.Version == "" {
			return released{}, errors.New("coresteps: a released version without its name")
		}
		for name, sum := range release.Prints {
			if _, ok := Named(name); !ok {
				return released{}, fmt.Errorf("coresteps: release %s names an unknown step %s", release.Version, name)
			}
			if decoded, err := hex.DecodeString(sum); err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != sum {
				return released{}, fmt.Errorf("coresteps: release %s, step %s: not a SHA-256", release.Version, name)
			}
		}
	}
	return list, nil
}

func (list released) accepts(name Name, instructions string) bool {
	sum := printOf(instructions)
	for _, release := range list.Releases {
		if release.Prints[string(name)] == sum {
			return true
		}
	}
	return false
}
