// SPDX-License-Identifier: AGPL-3.0-only

package door

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
)

// Limits are the bounds of the door (specs/2026-11-15-desk-mcp.md MC1.7): a line of a request is at most Line bytes and
// comes within LineTime, else the connection closes without an answer; Places calls run at once in the whole app, the others
// wait up to Wait and get «занят»; a call runs at most Run.
type Limits struct {
	Line     int
	LineTime time.Duration
	Places   int
	Wait     time.Duration
	Run      time.Duration
}

// DefaultLimits are the bounds of the app.
var DefaultLimits = Limits{Line: 64 << 10, LineTime: 5 * time.Second, Places: 2, Wait: 10 * time.Second, Run: 60 * time.Second}

// Serving is the door serving one listener until it is shut.
type Serving struct {
	door     *Door
	limits   Limits
	listener net.Listener
	places   chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	calls    sync.WaitGroup

	mu    sync.Mutex
	words string
}

// Serve answers the lines of the bridges on a listener — a local socket of the file system — until Shut.
func (door *Door) Serve(listener net.Listener, limits Limits) *Serving {
	ctx, cancel := context.WithCancel(context.Background())
	serving := &Serving{door: door, limits: limits, listener: listener, places: make(chan struct{}, limits.Places), ctx: ctx, cancel: cancel}
	serving.calls.Add(1)
	go func() {
		defer serving.calls.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			serving.calls.Add(1)
			go func() {
				defer serving.calls.Done()
				serving.serve(conn)
			}()
		}
	}()
	return serving
}

// Shut stops the door taking new calls first and ends the calls that run or wait with the words: «CanonKeeper
// закрывается…» at a stop, an update and an erase, «закрыт или выключено» when the author turns the reading off. It does not
// wait: Wait waits for the handlers within the term of the step of the stop.
func (serving *Serving) Shut(words string) {
	serving.mu.Lock()
	if serving.words == "" {
		serving.words = words
	}
	serving.mu.Unlock()
	_ = serving.listener.Close()
	serving.cancel()
}

// Wait waits for the handlers of the door until ctx ends.
func (serving *Serving) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		serving.calls.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (serving *Serving) shutWords() string {
	serving.mu.Lock()
	defer serving.mu.Unlock()
	return serving.words
}

var errLine = errors.New("door: the line of a request is too long")

// serve answers one connection: one line of a request, one line of an answer.
func (serving *Serving) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(serving.limits.LineTime))
	reader := bufio.NewReaderSize(conn, 4096)
	line, err := readLine(reader, serving.limits.Line)
	if err != nil {
		return
	}
	var request programtools.Request
	if json.Unmarshal(line, &request) != nil {
		return
	}
	if request.Hello {
		serving.door.saw(request)
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithCancel(serving.ctx)
	defer cancel()
	// The connection closed by the bridge ends the call at once and gives its place back.
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		cancel()
	}()
	// The answer of the call, or the words of the shut door at once: a handler that does not end does not hold the answer of
	// the stop; the door waits for it within the term of the stop of the turns.
	answered := make(chan programtools.Answer, 1)
	serving.calls.Add(1)
	go func() {
		defer serving.calls.Done()
		answered <- serving.call(ctx, request)
	}()
	var answer programtools.Answer
	select {
	case answer = <-answered:
	case <-serving.ctx.Done():
	}
	if words := serving.shutWords(); words != "" {
		answer = refused(words)
	} else if ctx.Err() != nil {
		return
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(encoded, '\n'))
}

// call takes a place within the wait and runs the call within its term.
func (serving *Serving) call(ctx context.Context, request programtools.Request) programtools.Answer {
	wait := time.NewTimer(serving.limits.Wait)
	defer wait.Stop()
	select {
	case serving.places <- struct{}{}:
	case <-wait.C:
		serving.door.saw(request)
		return refused(programtools.WordsBusy)
	case <-ctx.Done():
		return refused(programtools.WordsNoAnswer)
	}
	defer func() { <-serving.places }()
	run, cancel := context.WithTimeout(ctx, serving.limits.Run)
	defer cancel()
	answer := serving.door.Answer(run, request)
	if errors.Is(run.Err(), context.DeadlineExceeded) {
		return refused(programtools.WordsNoAnswer)
	}
	return answer
}

// readLine reads one line of at most limit bytes.
func readLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > limit+1 {
			return nil, errLine
		}
		switch {
		case err == nil:
			return line[:len(line)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, err
		}
	}
}
