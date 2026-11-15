// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
)

// Write is a write of the bridge through a use case of the core in the name of the author: the key of its step, derived
// from the key of the request (OK1.6, OK2.5), the method and the path of the request and the proof of its session.
type Write struct {
	ActorUserID string
	ProjectID   string
	Key         string
	Method      string
	Path        string
	Proof       auth.SessionWriteProof
}

// step is the write of a step of the request with the key derived for it.
func (write Write) step(name string, parts ...string) Write {
	write.Key = DerivedKey(write.Key, name, parts...)
	return write
}

// DerivedKey is the key of a step of a request of the bridge: «obsidian-<step>-» and 40 signs of the SHA-256 of the key of
// the request and the parts of the step — within the 8 to 128 signs of a key of the core, one key a step.
func DerivedKey(key, name string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(append([]string{key, name}, parts...), "\x00")))
	return "obsidian-" + name + "-" + hex.EncodeToString(sum[:])[:40]
}

// QueueRef is a queue of the volumes of a saga and its status.
type QueueRef struct {
	ID     string
	Status string
}

// QueueVolume is a volume a load puts into the queue: the folder it comes from, the name of its file and the glued text.
type QueueVolume struct {
	Folder   string
	FileName string
	Content  []byte
}

// UpdateJob is «Обновить из файла» of a book with the glued text of its folder: its preview or its application.
type UpdateJob struct {
	BookID   string
	FileName string
	Content  []byte
	Apply    bool
}

var (
	// ErrQueueExists is a queue the core refused because the saga has a live queue of the volumes.
	ErrQueueExists = errors.New("obsidian: the saga has a live queue of the volumes")
	// ErrConflict is a write the core refused as against its state now.
	ErrConflict = errors.New("obsidian: the core refused the write as a conflict")
)

// Shelf are the queue of the volumes and «Обновить из файла» of the core as the bridge calls their use cases
// (specs/2026-11-14-desk-obsidian-books.md OK1.3, OK2.1): nothing of them changes for the bridge.
type Shelf interface {
	// LiveQueue is the live queue of the saga — uploading, draft or running —, if it has one.
	LiveQueue(ctx context.Context, actorUserID, projectID string) (QueueRef, bool, error)
	// CreateQueue makes the draft of a queue of the volumes in their order and gives the ids of its volumes in that order:
	// ErrQueueExists for a saga with a live queue, the exceeded limit of the books of the saga as the core says it.
	CreateQueue(ctx context.Context, write Write, volumes []QueueVolume) (string, []string, error)
	// Upload sends the file of a volume and gives the version of the queue after it.
	Upload(ctx context.Context, write Write, queueID, itemID string, volume QueueVolume) (int64, error)
	// Start starts the queue at its version.
	Start(ctx context.Context, write Write, queueID string, version int64) (QueueRef, error)
	// CancelDraft cancels a draft the bridge made, as the shelf cancels a queue; a queue that is no draft any more stays.
	CancelDraft(ctx context.Context, draft Draft) error
	// QueueOfKey says whether the author made a queue of the saga under the key.
	QueueOfKey(ctx context.Context, actorUserID, projectID, key string) (bool, error)
	// ImportJob starts the import Job of «Обновить из файла» — its preview or its application — as the core answers it.
	ImportJob(ctx context.Context, write Write, update UpdateJob) (jobs.Job, error)
	// JobOfKey is the import Job the author started under the key of the request, if he did.
	JobOfKey(ctx context.Context, write Write) (jobs.Job, bool, error)
}

// Replays keep the answers of the loads by their keys in the replay records of the core (OK1.6).
type Replays interface {
	// Replay is the answer kept for the key: nil for none, ErrReplayConflict for the key of another request.
	Replay(ctx context.Context, key ReplayKey) (*Loaded, error)
	// Keep keeps the answer of the key; a key kept already keeps its answer.
	Keep(ctx context.Context, key ReplayKey, loaded Loaded) error
}

// ReplayKey is the key of a load: the author, the saga, the key of the request and the SHA-256 of what it asks.
type ReplayKey struct {
	ActorUserID string
	ProjectID   string
	Key         string
	Request     string
}

// ErrReplayConflict is a key of the request kept for another request.
var ErrReplayConflict = errors.New("obsidian: the key was used for another request")
