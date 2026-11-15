// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// shellOfTheTest hears what the backend writes to the shell and answers secret-save and secret-delete as told.
type shellOfTheTest struct {
	mu     sync.Mutex
	heard  []ControlMessage
	answer func(ControlMessage) (ok, answers bool)
	keeper *shellSecrets
}

func (shell *shellOfTheTest) send(message ControlMessage) error {
	shell.mu.Lock()
	shell.heard = append(shell.heard, message)
	answer := shell.answer
	shell.mu.Unlock()
	if answer == nil {
		return nil
	}
	if ok, answers := answer(message); answers {
		go shell.keeper.answered(message.Request, ok)
	}
	return nil
}

func (shell *shellOfTheTest) messages() []ControlMessage {
	shell.mu.Lock()
	defer shell.mu.Unlock()
	return append([]ControlMessage(nil), shell.heard...)
}

const keyOfTheTest = "key-test-key-of-the-north-pass-7f3a"

// specs/2026-11-13-desk-secrets.md SE1.3, SE1.4: the backend holds the records start handed it; a change goes to the
// shell first and into memory only after its secret-result ok; a refusal or silence leaves memory as it was.
func TestASecretIsHeldOnlyAfterTheShellKeptIt(t *testing.T) {
	shell := &shellOfTheTest{}
	secrets := newShellSecrets(shell.send, domain.SecretsInFile,
		map[string]string{domain.SecretOwnAI: "key-test-from-start", "provider:other": "x"}, time.Second)
	shell.keeper = secrets
	if !secrets.Has(domain.SecretOwnAI) || secrets.Has("provider:other") || secrets.Store() != domain.SecretsInFile {
		t.Fatalf("the secrets of start = own %v, other %v, store %q", secrets.Has(domain.SecretOwnAI), secrets.Has("provider:other"), secrets.Store())
	}

	shell.answer = func(ControlMessage) (bool, bool) { return true, true }
	if err := secrets.Delete(context.Background(), domain.SecretOwnAI); err != nil || secrets.Has(domain.SecretOwnAI) {
		t.Fatalf("delete = %v, held %v", err, secrets.Has(domain.SecretOwnAI))
	}
	if err := secrets.Save(context.Background(), domain.SecretOwnAI, keyOfTheTest); err != nil || !secrets.Has(domain.SecretOwnAI) {
		t.Fatalf("save = %v, held %v", err, secrets.Has(domain.SecretOwnAI))
	}
	// specs/2026-11-13-desk-gateway.md GW1.2: the gateway reads the key the backend holds now.
	if value, held := secrets.Value(domain.SecretOwnAI); !held || value != keyOfTheTest {
		t.Fatalf("the key the gateway reads is held %v", held)
	}
	heard := shell.messages()
	if len(heard) != 2 || heard[0].Type != messageSecretDelete || heard[0].ID != domain.SecretOwnAI || heard[0].Value != "" ||
		heard[1].Type != messageSecretSave || heard[1].ID != domain.SecretOwnAI || heard[1].Value != keyOfTheTest ||
		heard[0].Request == "" || heard[0].Request == heard[1].Request {
		t.Fatalf("the shell heard %+v", heard)
	}

	shell.answer = func(ControlMessage) (bool, bool) { return false, true }
	err := secrets.Save(context.Background(), domain.SecretOwnAI, "key-test-refused-one")
	if !errors.Is(err, domain.ErrSecretNotKept) || strings.Contains(err.Error(), "key-test-refused-one") {
		t.Fatalf("a refused save = %v", err)
	}
	if err := secrets.Delete(context.Background(), domain.SecretOwnAI); !errors.Is(err, domain.ErrSecretNotKept) || !secrets.Has(domain.SecretOwnAI) {
		t.Fatalf("a refused delete = %v, held %v", err, secrets.Has(domain.SecretOwnAI))
	}
}

func TestASilentShellKeepsNothing(t *testing.T) {
	shell := &shellOfTheTest{answer: func(ControlMessage) (bool, bool) { return false, false }}
	secrets := newShellSecrets(shell.send, domain.SecretsInSystem, nil, 50*time.Millisecond)
	shell.keeper = secrets
	started := time.Now()
	err := secrets.Save(context.Background(), domain.SecretOwnAI, keyOfTheTest)
	if !errors.Is(err, domain.ErrSecretNotKept) || secrets.Has(domain.SecretOwnAI) || time.Since(started) > 5*time.Second {
		t.Fatalf("a silent shell = %v, held %v, after %s", err, secrets.Has(domain.SecretOwnAI), time.Since(started))
	}
	// A late answer to a request nobody waits for changes nothing.
	secrets.answered(shell.messages()[0].Request, true)
	if secrets.Has(domain.SecretOwnAI) {
		t.Fatal("a late answer put the key into memory")
	}
	// An answer to a request that was never asked changes nothing either.
	secrets.answered("s999", true)
}

// SE1.4, SE2.5: the shell keeps records one after another, and the shell restarts a backend that fell — so the answer to
// a request of the backend before may come to the next one while the shell has not yet kept what the next one asked. It
// answers nothing there: the next backend holds a record only after the answer to its own request.
func TestAnAnswerOwedToTheBackendBeforeDoesNotAnswerTheNextOne(t *testing.T) {
	before := &shellOfTheTest{}
	fell := newShellSecrets(before.send, domain.SecretsInSystem, nil, 50*time.Millisecond)
	before.keeper = fell
	if err := fell.Save(context.Background(), domain.SecretOwnAI, "key-test-of-the-backend-before"); !errors.Is(err, domain.ErrSecretNotKept) {
		t.Fatalf("a save the shell is still keeping = %v", err)
	}
	owed := before.messages()[0].Request

	shell := &shellOfTheTest{}
	next := newShellSecrets(shell.send, domain.SecretsInSystem, nil, time.Second)
	shell.keeper = next
	shell.answer = func(ControlMessage) (bool, bool) {
		// The store kept the record of the backend before, then refuses the one of the next.
		next.answered(owed, true)
		return false, true
	}
	err := next.Save(context.Background(), domain.SecretOwnAI, keyOfTheTest)
	if !errors.Is(err, domain.ErrSecretNotKept) || next.Has(domain.SecretOwnAI) {
		t.Fatalf("the next backend took the answer owed to the one before: save = %v, held %v, requests %q and %q",
			err, next.Has(domain.SecretOwnAI), owed, shell.messages()[0].Request)
	}
}

func TestAnIDOutsideTheSetIsNotAskedFor(t *testing.T) {
	shell := &shellOfTheTest{answer: func(ControlMessage) (bool, bool) { return true, true }}
	secrets := newShellSecrets(shell.send, domain.SecretsInSystem, nil, time.Second)
	shell.keeper = secrets
	if err := secrets.Save(context.Background(), "provider:other", keyOfTheTest); !errors.Is(err, domain.ErrSecretNotKept) {
		t.Fatalf("save of another id = %v", err)
	}
	if heard := shell.messages(); len(heard) != 0 {
		t.Fatalf("the shell heard %+v", heard)
	}
}

// SE1.4: the lines of the channel — the shell's start with its records and store and its secret-result, the backend's
// secret-save and secret-delete; secret has no sender and is gone.
func TestTheChannelCarriesTheSecretsOfTheApp(t *testing.T) {
	in := strings.NewReader(`{"type":"start","dataDir":"/d","resources":"/r","secrets":{"provider:own":"key-test-north"},"secretsStore":"system"}` + "\n" +
		`{"type":"secret-result","request":"s1","ok":true}` + "\n" + `{"type":"secret-result","request":"s2"}` + "\n")
	var out bytes.Buffer
	channel := newControl(in, &out)
	start, err := channel.receive()
	if err != nil || start.Type != messageStart || start.Secrets[domain.SecretOwnAI] != "key-test-north" || start.SecretsStore != "system" {
		t.Fatalf("start = %+v, %v", start, err)
	}
	if result, err := channel.receive(); err != nil || result.Type != messageSecretResult || result.Request != "s1" || !result.OK {
		t.Fatalf("secret-result = %+v, %v", result, err)
	}
	if result, err := channel.receive(); err != nil || result.Request != "s2" || result.OK {
		t.Fatalf("secret-result without ok = %+v, %v", result, err)
	}
	if err := channel.send(ControlMessage{Type: messageSecretSave, ID: domain.SecretOwnAI, Value: "key-test-north", Request: "s3"}); err != nil {
		t.Fatal(err)
	}
	if err := channel.send(ControlMessage{Type: messageSecretDelete, ID: domain.SecretOwnAI, Request: "s4"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var save, remove map[string]any
	if len(lines) != 2 || json.Unmarshal([]byte(lines[0]), &save) != nil || json.Unmarshal([]byte(lines[1]), &remove) != nil {
		t.Fatalf("the backend wrote %q", out.String())
	}
	if save["type"] != "secret-save" || save["id"] != "provider:own" || save["value"] != "key-test-north" || save["request"] != "s3" || len(save) != 4 {
		t.Fatalf("secret-save = %v", save)
	}
	if remove["type"] != "secret-delete" || remove["id"] != "provider:own" || remove["request"] != "s4" || len(remove) != 3 {
		t.Fatalf("secret-delete = %v", remove)
	}
}

// SE1.3: a start that does not say where the shell keeps the secrets is refused before anything starts.
func TestAStartWithoutTheStoreOfTheSecretsIsRefused(t *testing.T) {
	for _, store := range []string{"", "memory"} {
		line, _ := json.Marshal(ControlMessage{Type: messageStart, DataDir: t.TempDir(), Resources: t.TempDir(), SecretsStore: store})
		var out bytes.Buffer
		err := Run(context.Background(), Options{In: strings.NewReader(string(line) + "\n"), Control: &out})
		var fatal ControlMessage
		if err == nil || json.Unmarshal(out.Bytes(), &fatal) != nil || fatal.Type != messageFatal || fatal.Reason != reasonStart {
			t.Fatalf("a start with the store %q = %v, wrote %q", store, err, out.String())
		}
	}
	if err := Run(context.Background(), Options{In: strings.NewReader(""), Control: io.Discard}); err != nil {
		t.Fatalf("a closed channel before start = %v", err)
	}
}
