package modelapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"canonkeeper/backend/internal/domain/modelthread"
)

// ThreadHeader names the thread of a call to codex-lb (specs/2026-11-17-model-cache.md MC1.1): codex-lb keeps the calls of
// one thread on one account of its pool, and the provider gives their shared beginning from its cache. Only the client of
// the site sends it; no header of a session and no prompt_cache_key go with it.
const ThreadHeader = "Thread-Id"

// threadRefusals are the codes of a refusal 409 of codex-lb whose thread lost its account (MC1.2): its owner left the
// key, two sources of the conversation named two owners, or the quota of the owner ended without a context to move.
var threadRefusals = map[string]bool{
	"previous_response_owner_unavailable": true,
	"conversation_owner_conflict":         true,
	"quota_failover_requires_context":     true,
}

// maxThreads bounds the generations the process keeps; a thread forgotten past it goes with generation 0 again.
const maxThreads = 10_000

// Thread is a thread of calls by its parts (MC1.1): its value is "ck-" and the first 32 hex digits of the SHA-256 of
// "ck-thread", the parts and the generation, joined by a zero byte. The parts are not kept: only their digest.
type Thread struct {
	key  [sha256.Size]byte
	seed string
}

// NewThread is the thread of the parts, in their order.
func NewThread(parts []string) Thread {
	seed := "ck-thread\x00" + strings.Join(parts, "\x00") + "\x00"
	return Thread{key: sha256.Sum256([]byte(seed)), seed: seed}
}

// Value is the value of Thread-Id at a generation.
func (thread Thread) Value(generation int) string {
	sum := sha256.Sum256([]byte(thread.seed + strconv.Itoa(generation)))
	return "ck-" + hex.EncodeToString(sum[:])[:32]
}

// Generation is the generation the calls of the thread go with now.
func (thread Thread) Generation() int { return generations.current(thread.key) }

// Refused moves the thread past a refusal of the generation a call used: the thread goes on with the greater of the
// generation it has and the next of the one used — calls of one thread refused together stay one thread (MC1.2).
func (thread Thread) Refused(used int) int { return generations.refused(thread.key, used) }

// generations are the generations of the threads of the process past 0: one map for the client and the proxy of the
// turns of apps (MC1.2).
var generations = &threadGenerations{of: map[[sha256.Size]byte]int{}}

type threadGenerations struct {
	mu sync.Mutex
	of map[[sha256.Size]byte]int
}

func (all *threadGenerations) current(key [sha256.Size]byte) int {
	all.mu.Lock()
	defer all.mu.Unlock()
	return all.of[key]
}

func (all *threadGenerations) refused(key [sha256.Size]byte, used int) int {
	all.mu.Lock()
	defer all.mu.Unlock()
	next := max(all.of[key], used+1)
	if _, known := all.of[key]; !known && len(all.of) >= maxThreads {
		// One thread is forgotten for the new one: it goes with generation 0 again.
		for forgotten := range all.of {
			delete(all.of, forgotten)
			break
		}
	}
	all.of[key] = next
	return next
}

// ThreadRefused says whether a refusal is one of the thread (MC1.2): a 409 whose JSON names one of the codes of a thread
// that lost its account.
func ThreadRefused(status int, body []byte) bool {
	return status == http.StatusConflict && threadRefusals[refusalOf(body).Code]
}

// refusal is the error of the JSON of a refusal of codex-lb: its type and its code.
type refusal struct {
	Type string `json:"type"`
	Code string `json:"code"`
}

func refusalOf(body []byte) refusal {
	var answer struct {
		Error refusal `json:"error"`
	}
	_ = json.Unmarshal(body, &answer)
	return answer.Error
}

// callThread is the thread one call sends: the generation it goes with and whether a refusal of its thread was sent
// again already. Nil is a call without a thread.
type callThread struct {
	thread     Thread
	generation int
	again      bool
}

// threadOf is the thread of a call of the client: the parts of its context, sent only by the client of the site — the
// client of the app goes to its own gateway, which takes no header of its client to a provider (GW1.4).
func (client *Client) threadOf(ctx context.Context) *callThread {
	if client.config.CarryJob {
		return nil
	}
	parts, ok := modelthread.Of(ctx)
	if !ok {
		return nil
	}
	thread := NewThread(parts)
	return &callThread{thread: thread, generation: thread.Generation()}
}

// name puts the thread on a request: the generation of the call, or the later one another call of the thread moved to.
func (call *callThread) name(header http.Header) {
	if call == nil {
		return
	}
	call.generation = max(call.generation, call.thread.Generation())
	header.Set(ThreadHeader, call.thread.Value(call.generation))
}

// refusedOnce says whether a refusal of the thread goes again at once with the next generation: once a call.
func (call *callThread) refusedOnce() bool {
	if call == nil || call.again {
		return false
	}
	call.again = true
	call.generation = call.thread.Refused(call.generation)
	return true
}
