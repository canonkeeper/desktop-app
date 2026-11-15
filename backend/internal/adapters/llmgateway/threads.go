// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import "sync"

// Threads is the registry «нить → Job» (GW1.7, GW2.4): a turn of the chat puts its root thread and its Job for the time
// of the turn, and the gateway names the Job of a request of Codex by its header thread-id.
type Threads struct {
	mu   sync.Mutex
	jobs map[string]string
}

func NewThreads() *Threads {
	return &Threads{jobs: map[string]string{}}
}

// Bind puts the pair until the returned release; a release of an older pair leaves a newer one of the thread.
func (threads *Threads) Bind(threadID, jobID string) func() {
	if threadID == "" || jobID == "" {
		return func() {}
	}
	threads.mu.Lock()
	threads.jobs[threadID] = jobID
	threads.mu.Unlock()
	return func() {
		threads.mu.Lock()
		defer threads.mu.Unlock()
		if threads.jobs[threadID] == jobID {
			delete(threads.jobs, threadID)
		}
	}
}

// Job is the Job of the thread's turn, empty outside a turn.
func (threads *Threads) Job(threadID string) string {
	if threadID == "" {
		return ""
	}
	threads.mu.Lock()
	defer threads.mu.Unlock()
	return threads.jobs[threadID]
}
