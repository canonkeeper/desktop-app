// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"testing"

	domain "canonkeeper/backend/internal/domain/desktop"
)

type shellOfTheTest struct {
	asked int
	fail  error
	// order is what the erasure did, in order.
	order []string
}

func (shell *shellOfTheTest) AskErase() error {
	shell.asked++
	shell.order = append(shell.order, "erase")
	return shell.fail
}

// accountOfTheTest is the link with an account as the erasure sees it.
type accountOfTheTest struct{ shell *shellOfTheTest }

func (account accountOfTheTest) BeforeErase(context.Context) {
	account.shell.order = append(account.shell.order, "unlink")
}

// specs/2026-11-13-desk-erase.md ER1.2: the word asks the shell once; another word asks nothing; a shell that did not
// hear is an error of the operation.
func TestTheErasureAsksTheShellOnlyForTheWord(t *testing.T) {
	shell := &shellOfTheTest{}
	erase := EraseData{Shell: shell, Account: accountOfTheTest{shell: shell}}
	if err := erase.Execute(context.Background(), "стерет"); !errors.Is(err, domain.ErrEraseUnconfirmed) || shell.asked != 0 {
		t.Fatalf("another word = %v, the shell asked %d times", err, shell.asked)
	}
	if err := erase.Execute(context.Background(), " Стереть "); err != nil || shell.asked != 1 {
		t.Fatalf("the word = %v, the shell asked %d times", err, shell.asked)
	}
	shell.fail = errors.New("the channel is closed")
	if err := erase.Execute(context.Background(), "стереть"); err == nil || errors.Is(err, domain.ErrEraseUnconfirmed) {
		t.Fatalf("a shell that did not hear = %v", err)
	}
}

// specs/2026-11-13-desk-account.md AC2.5: the erasure tries to unlink the app on the site before it asks the shell, and
// asks it whatever the site answered; another word unlinks nothing.
func TestTheErasureUnlinksTheAppBeforeItAsksTheShell(t *testing.T) {
	shell := &shellOfTheTest{}
	erase := EraseData{Shell: shell, Account: accountOfTheTest{shell: shell}}
	_ = erase.Execute(context.Background(), "нет")
	if err := erase.Execute(context.Background(), "стереть"); err != nil || len(shell.order) != 2 || shell.order[0] != "unlink" || shell.order[1] != "erase" {
		t.Fatalf("the erasure did %v, %v", shell.order, err)
	}
}
