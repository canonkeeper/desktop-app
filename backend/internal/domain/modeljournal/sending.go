// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
)

// SendPurposes are the purposes a record of a sending may have (specs/2026-11-14-desk-journal.md JR1.8): the purposes of a
// request and the reading of a program of the author through the bridge (specs/2026-11-15-desk-mcp.md MC4.4).
func SendPurposes() []string { return append(Purposes(), PurposeProgram) }

// Sending is the record of one sending the gateway writes before the body goes (JR1.1): its number — the number of the row
// of its request too —, its time, the Job and the thread of Codex it went for, the number of the check of a probe, its
// purpose, to whom it went in fact and how many bytes, and what of the saga it gives that went with none before it —
// never a word of the text. FirstTime: the first sending of the thread to a helper it never went to while it went to
// another (JR1.5); the journal sets it.
type Sending struct {
	ID        string
	At        time.Time
	JobID     string
	ThreadID  string
	CheckID   string
	Purpose   string
	Profile   string
	Host      string
	Bytes     int64
	Named     disclosure.Named
	FirstTime bool
}

// Kind is the work a row of the journal is (JR2.1): a question of the chat, the reading of the books, the parsing of the
// author's materials, a check of the connection, the readings of a program of the author (specs/2026-11-15-desk-mcp.md MC4.4).
type Kind string

const (
	KindQuestion  Kind = "question"
	KindReading   Kind = "reading"
	KindMaterials Kind = "materials"
	KindCheck     Kind = "check"
	KindProgram   Kind = "program"
)

// Kinds are the kinds of the rows in the order of the choice of the page.
func Kinds() []Kind { return []Kind{KindQuestion, KindReading, KindMaterials, KindCheck, KindProgram} }

// KindOf is the kind of the work of a purpose: the chat and its scan are the question, the probe the check, the materials
// their parsing, the reading of a program its own, every other step the reading of the books.
func KindOf(purpose string) Kind {
	switch purpose {
	case PurposeChat, "scan":
		return KindQuestion
	case PurposeProbe:
		return KindCheck
	case PurposeProgram:
		return KindProgram
	case "materials":
		return KindMaterials
	default:
		return KindReading
	}
}

// PurposesOf are the purposes of the sendings of a kind.
func PurposesOf(kind Kind) []string {
	var purposes []string
	for _, purpose := range SendPurposes() {
		if KindOf(purpose) == kind {
			purposes = append(purposes, purpose)
		}
	}
	return purposes
}

// RecipientWords names to whom sendings went (Data «получатель»): the server CanonKeeper for the profile account, a program
// of this computer by a host of the loopback, else the host of the provider as it is.
func RecipientWords(profile, host string) string {
	switch {
	case profile == "account":
		return "сервер CanonKeeper"
	case loopback(host):
		return "программа на этом компьютере (" + host + ")"
	default:
		return host
	}
}

// loopback says a host names this computer: localhost or an address of the loopback, with a port or without.
func loopback(host string) bool {
	name := host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		name = parsed
	}
	name = strings.Trim(name, "[]")
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// NewID is the number of a record of a sending, a random UUIDv4: the gateway gives it before the body goes, and the row of
// its request carries it as its own.
func NewID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
