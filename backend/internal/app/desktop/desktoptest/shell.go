// SPDX-License-Identifier: AGPL-3.0-only

package desktoptest

import (
	"sync"
	"time"
)

// Reply is how the shell of a test answers a secret-save or a secret-delete: it keeps the record as asked or refuses,
// or it stays silent — a store of the system that waits for the author to unlock it.
type Reply int

const (
	Keep Reply = iota
	Refuse
	Silent
)

// Shell plays the shell of the app over the channel of a process: it keeps the records the backend asks it to keep,
// answers secret-save and secret-delete the way its test chose, and hands every other message to the test. Once a test
// serves the process, the shell is the one reader of the channel.
type Shell struct {
	process *Process
	mu      sync.Mutex
	reply   func(Message) Reply
	records map[string]string
	asked   []Message
	others  chan Message
}

// Serve starts the shell of the test with the records it handed over in start; reply decides every answer.
func (process *Process) Serve(records map[string]string, reply func(Message) Reply) *Shell {
	kept := map[string]string{}
	for id, value := range records {
		kept[id] = value
	}
	shell := &Shell{process: process, reply: reply, records: kept, others: make(chan Message, 256)}
	go shell.run()
	return shell
}

// KeepAll answers every change of a secret: kept.
func KeepAll(Message) Reply { return Keep }

func (shell *Shell) run() {
	defer close(shell.others)
	for line := range shell.process.lines {
		message := parse(line)
		if message.Type != "secret-save" && message.Type != "secret-delete" {
			shell.others <- message
			continue
		}
		shell.mu.Lock()
		shell.asked = append(shell.asked, message)
		reply := shell.reply(message)
		if reply == Keep {
			if message.Type == "secret-save" {
				shell.records[message.ID] = message.Value
			} else {
				delete(shell.records, message.ID)
			}
		}
		shell.mu.Unlock()
		if reply != Silent {
			_ = shell.process.Send(map[string]any{"type": "secret-result", "request": message.Request, "ok": reply == Keep})
		}
	}
}

// Answer changes how the shell answers from now on.
func (shell *Shell) Answer(reply func(Message) Reply) {
	shell.mu.Lock()
	defer shell.mu.Unlock()
	shell.reply = reply
}

// Records are the records the shell keeps now: what it would hand over in the next start.
func (shell *Shell) Records() map[string]string {
	shell.mu.Lock()
	defer shell.mu.Unlock()
	records := map[string]string{}
	for id, value := range shell.records {
		records[id] = value
	}
	return records
}

// Asked are the secret-save and secret-delete the backend sent, in order.
func (shell *Shell) Asked() []Message {
	shell.mu.Lock()
	defer shell.mu.Unlock()
	return append([]Message(nil), shell.asked...)
}

// Next is the next message besides the secrets; ErrNoLine when none came in timeout, scaled by WaitsVariable, a message
// without a type once the channel closed.
func (shell *Shell) Next(timeout time.Duration) (Message, error) {
	return shell.nextBy(Deadline(timeout))
}

func (shell *Shell) nextBy(deadline time.Time) (Message, error) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case message, ok := <-shell.others:
		if !ok {
			return Message{}, nil
		}
		return message, nil
	case <-timer.C:
		return Message{}, ErrNoLine
	}
}

// Await waits for a message of a type besides the secrets, past the others.
func (shell *Shell) Await(kind string, timeout time.Duration) (Message, bool) {
	deadline := Deadline(timeout)
	for {
		message, err := shell.nextBy(deadline)
		if err != nil || message.Type == "" {
			return Message{}, false
		}
		if message.Type == kind {
			return message, true
		}
	}
}
