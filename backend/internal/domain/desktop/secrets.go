// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"errors"
	"slices"
)

// The secrets of the app (specs/2026-11-13-desk-secrets.md SE1): the shell keeps them in the store of the system, or in
// a file of the data directory where the system has none, and hands them to the backend in start; the backend asks the
// shell to keep or remove one and holds it only in memory.

// SecretOwnAI is the record of the key of «Свой ИИ».
const SecretOwnAI = "provider:own"

// SecretAccountPass is the record of the pass of the link of the app with an account of the site
// (specs/2026-11-13-desk-account.md AC1.3).
const SecretAccountPass = "account:pass"

// secretRecords is the closed set of records the shell keeps for the app: the shell names the same ids in
// desktop/src-tauri/crates/core/src/secrets.rs (RECORDS), and scripts/desktop-app.test.mjs holds the two equal.
var secretRecords = []string{SecretOwnAI, SecretAccountPass}

// MaxSecretBytes is the longest value of a record, in bytes of UTF-8, every store of the shell keeps whatever its
// characters: the Credential Manager of Windows holds 2560 bytes of UTF-16, and no text takes more than twice its bytes
// of UTF-8 in UTF-16. The shell refuses a longer one in every store (MAX_VALUE in secrets.rs; scripts/desktop-app.test.mjs
// holds the two equal).
const MaxSecretBytes = 1280

// KnownSecret says whether an id is one of the records of the app; the backend neither asks for nor holds another.
func KnownSecret(id string) bool {
	return slices.Contains(secretRecords, id)
}

// SecretsStore says where the shell keeps the secrets this run.
type SecretsStore string

const (
	// SecretsInSystem: the Credential Manager of Windows or the Secret Service of Linux.
	SecretsInSystem SecretsStore = "system"
	// SecretsInFile: secrets.json of the data directory, readable by the user alone — the system has no store that works.
	SecretsInFile SecretsStore = "file"
)

// ParseSecretsStore reads the store start names.
func ParseSecretsStore(name string) (SecretsStore, bool) {
	switch store := SecretsStore(name); store {
	case SecretsInSystem, SecretsInFile:
		return store, true
	}
	return "", false
}

// ErrSecretNotKept is a change of a secret the shell refused or did not answer in time: the backend holds what it held.
var ErrSecretNotKept = errors.New("desktop: the shell did not keep the secret")
