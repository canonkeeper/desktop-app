// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import "testing"

// specs/2026-11-13-desk-secrets.md SE1.2: the set of records the shell keeps is closed — the key of «Свой ИИ», the pass of
// the link with the account (specs/2026-11-13-desk-account.md AC1.3) and nothing else; the store is the system's or the
// file's.
func TestTheSecretsOfTheAppAreAClosedSet(t *testing.T) {
	if !KnownSecret(SecretOwnAI) || SecretOwnAI != "provider:own" {
		t.Fatalf("the key of «Свой ИИ» = %q", SecretOwnAI)
	}
	if !KnownSecret(SecretAccountPass) || SecretAccountPass != "account:pass" {
		t.Fatalf("the pass of the account = %q", SecretAccountPass)
	}
	for _, id := range []string{"", "provider:other", "provider:own ", "pass", "probe", "account:pass ", "account"} {
		if KnownSecret(id) {
			t.Fatalf("%q is a record of the app", id)
		}
	}
	for name, want := range map[string]SecretsStore{"system": SecretsInSystem, "file": SecretsInFile} {
		if store, ok := ParseSecretsStore(name); !ok || store != want {
			t.Fatalf("ParseSecretsStore(%q) = %q, %v", name, store, ok)
		}
	}
	for _, name := range []string{"", "System", "memory"} {
		if _, ok := ParseSecretsStore(name); ok {
			t.Fatalf("%q is a store of the secrets", name)
		}
	}
}
