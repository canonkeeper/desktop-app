// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"crypto/rand"
	"strconv"
	"sync"
	"time"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// secretAnswerTime is how long the backend waits for the shell to keep or remove a secret (specs/2026-11-13-desk-secrets.md
// SE2.5): a store of the system that asks the author to unlock it may take a while, a request does not wait longer.
const secretAnswerTime = 5 * time.Second

// shellSecrets are the secrets of the app as the backend holds them (SE1.3, SE1.4): the records of the set start handed
// over, in memory until the backend exits. A change goes to the shell first — secret-save or secret-delete — and into
// memory only after its secret-result ok; one change at a time, so memory follows the order the shell kept them in.
// A request is named s<run>-<N>: the shell keeps records one after another and restarts a backend that fell, so the
// answer to a request of the backend before may come to this one, and the run of this process tells it apart.
type shellSecrets struct {
	send    func(ControlMessage) error
	store   domain.SecretsStore
	timeout time.Duration
	run     string

	changing sync.Mutex
	mu       sync.Mutex
	values   map[string]string
	asked    uint64
	waiting  map[string]chan bool
}

// newShellSecrets holds the records of start that belong to the set; another id is not held.
func newShellSecrets(send func(ControlMessage) error, store domain.SecretsStore, records map[string]string, timeout time.Duration) *shellSecrets {
	values := map[string]string{}
	for id, value := range records {
		if domain.KnownSecret(id) && value != "" {
			values[id] = value
		}
	}
	return &shellSecrets{send: send, store: store, timeout: timeout, run: rand.Text(), values: values, waiting: map[string]chan bool{}}
}

// Store says where the shell keeps the secrets this run.
func (secrets *shellSecrets) Store() domain.SecretsStore { return secrets.store }

// Has says whether the backend holds the record.
func (secrets *shellSecrets) Has(id string) bool {
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	_, ok := secrets.values[id]
	return ok
}

// Value is the record the backend holds, for the one reader of a key: the local gateway puts the key of «Свой ИИ» into a
// request to the provider (specs/2026-11-13-desk-gateway.md GW1.2). It goes nowhere else.
func (secrets *shellSecrets) Value(id string) (string, bool) {
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	value, ok := secrets.values[id]
	return value, ok
}

// Save asks the shell to keep a record and holds it once the shell did; otherwise domain.ErrSecretNotKept.
func (secrets *shellSecrets) Save(ctx context.Context, id, value string) error {
	return secrets.change(ctx, id, &value)
}

// Delete asks the shell to remove a record and stops holding it once the shell did; otherwise domain.ErrSecretNotKept.
func (secrets *shellSecrets) Delete(ctx context.Context, id string) error {
	return secrets.change(ctx, id, nil)
}

func (secrets *shellSecrets) change(ctx context.Context, id string, value *string) error {
	if !domain.KnownSecret(id) {
		return domain.ErrSecretNotKept
	}
	secrets.changing.Lock()
	defer secrets.changing.Unlock()
	secrets.mu.Lock()
	secrets.asked++
	request := "s" + secrets.run + "-" + strconv.FormatUint(secrets.asked, 10)
	answer := make(chan bool, 1)
	secrets.waiting[request] = answer
	secrets.mu.Unlock()
	defer func() {
		secrets.mu.Lock()
		delete(secrets.waiting, request)
		secrets.mu.Unlock()
	}()

	message := ControlMessage{Type: messageSecretDelete, ID: id, Request: request}
	if value != nil {
		message.Type, message.Value = messageSecretSave, *value
	}
	if secrets.send(message) != nil {
		return domain.ErrSecretNotKept
	}
	timer := time.NewTimer(secrets.timeout)
	defer timer.Stop()
	select {
	case kept := <-answer:
		if !kept {
			return domain.ErrSecretNotKept
		}
	case <-timer.C:
		return domain.ErrSecretNotKept
	case <-ctx.Done():
		return domain.ErrSecretNotKept
	}
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	if value != nil {
		secrets.values[id] = *value
	} else {
		delete(secrets.values, id)
	}
	return nil
}

// answered is the secret-result of the shell; an answer nobody waits for — late, never asked or owed to the backend
// before — changes nothing.
func (secrets *shellSecrets) answered(request string, ok bool) {
	secrets.mu.Lock()
	answer := secrets.waiting[request]
	secrets.mu.Unlock()
	if answer == nil {
		return
	}
	select {
	case answer <- ok:
	default:
	}
}
