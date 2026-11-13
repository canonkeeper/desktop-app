//go:build unix

// SPDX-License-Identifier: AGPL-3.0-only

package codex

// heldByAnother: a file another process holds open does not stop a rename on Unix, so nothing is waited for.
func heldByAnother(error) bool { return false }
