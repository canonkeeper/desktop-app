// SPDX-License-Identifier: AGPL-3.0-only

package desktop

// obsidianPoint stops a load of the bridge at a point of its writes (specs/2026-11-14-desk-obsidian-books.md OK1.4): a
// process test builds the program with its own file in place of this one. The build stops nowhere.
var obsidianPoint func(point string) error
