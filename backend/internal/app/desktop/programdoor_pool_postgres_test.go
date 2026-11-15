// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/door"
	"canonkeeper/backend/internal/delivery/mcp/programtools"
	"canonkeeper/backend/internal/domain/agent"
	researchservice "canonkeeper/backend/internal/services/researchchat"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
)

// specs/2026-11-15-desk-mcp.md MC1.7: the door holds at most two connections of the pool of the app at once — its places
// by default —, and an operation of the window and a reading of the chat end while both are taken; a third call of a program
// waits its term and is «занят». The pool of the test has one connection besides the two places: a door that took a third
// would leave the window waiting.
func TestTheDoorLeavesThePoolToTheWindowPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_desktop_door_pool")
	config := db.Pool.Config()
	config.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	held := make(chan struct{}, 4)
	release := make(chan struct{})
	doors := newProgramDoor(t.TempDir(), log.New(io.Discard, "", 0))
	doors.limits.Wait = 200 * time.Millisecond
	doors.use(door.New(oneSaga{}, func(string, researchservice.Naming) door.Handler {
		return func(ctx context.Context, _ agent.ToolCall) (agent.ToolResult, error) {
			conn, err := pool.Acquire(ctx)
			if err != nil {
				return agent.ToolResult{}, err
			}
			defer conn.Release()
			held <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return agent.ToolResult{Success: true, Payload: json.RawMessage("текст")}, nil
		}
	}, "1.4.0"))
	if err := doors.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer doors.Close()
	file, _ := doorFileOf(t, doors.dataDir)
	call := func() chan string {
		answered := make(chan string, 1)
		go func() {
			conn, err := net.Dial("unix", file.Socket)
			if err != nil {
				answered <- err.Error()
				return
			}
			defer conn.Close()
			line, _ := json.Marshal(programtools.Request{Fingerprint: programtools.Fingerprint(), Version: "1.4.0", Kind: programtools.KindInstall, Tool: "read", Arguments: json.RawMessage(`{}`)})
			_, _ = conn.Write(append(line, '\n'))
			answer, _ := bufio.NewReader(conn).ReadBytes('\n')
			var decoded programtools.Answer
			_ = json.Unmarshal(answer, &decoded)
			answered <- decoded.Text
		}()
		return answered
	}
	first, second := call(), call()
	<-held
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, what := range []string{"an operation of the window", "a reading of the chat"} {
		var one int
		if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatalf("%s while the door holds its places: %v", what, err)
		}
	}
	if third := <-call(); third != programtools.WordsBusy {
		t.Fatalf("the third call: %q", third)
	}
	close(release)
	if <-first != "текст" || <-second != "текст" {
		t.Fatal("the held calls did not answer")
	}
}
