package quota

import "errors"

// ErrInvalidOverride refuses an unknown or repeated key, a value below 0 or above what the key
// keeps, or no change at all; nothing is changed (UC6, R6.2; admin-panel R7.5).
var ErrInvalidOverride = errors.New("quota: invalid override")

// OverrideChange sets one limit of a writer; a nil Value returns it to the default.
type OverrideChange struct {
	Key   Key
	Value *int64
}

// OverrideChanged is one changed limit: nil means the default.
type OverrideChanged struct {
	Key    Key
	Before *int64
	After  *int64
}

// WriterQuota is what the owner sees of one writer's quotas (UC6 R6.1, admin-panel R3.1): the
// limits in force with their use and the source of each (specs/2026-11-08-subscription-tiers.md TR2.6). The field is
// not named Sources: Account.Sources, the text of the owned projects, is promoted here.
type WriterQuota struct {
	Account
	LimitSources map[Key]Source
}

// Used is the use of one limit the account counts: the day's questions, uploads, drawings and runs of writing, questions in
// work, live projects and the text of all owned projects. A per-project limit has none.
func (account Account) Used(key Key) (int64, bool) {
	switch key {
	case KeyChatTurnsPerDay:
		return account.ChatTurns, true
	case KeyActiveTurns:
		return account.ActiveTurns, true
	case KeyImportsPerDay:
		return account.Imports, true
	case KeyImagesPerDay:
		return account.Images, true
	case KeyImageUploadsPerDay:
		return account.ImageUploads, true
	case KeyChapterDraftsPerDay:
		return account.ChapterDrafts, true
	case KeyDrawingTurnsPerDay:
		return account.DrawingTurns, true
	case KeyDraftTurnsPerDay:
		return account.DraftTurns, true
	case KeyDraftRewritesPerDay:
		return account.DraftRewrites, true
	case KeyProjects:
		return account.Projects, true
	case KeyAccountBooks:
		return account.Sources.Books, true
	case KeyAccountChapters:
		return account.Sources.Chapters, true
	case KeyAccountCharacters:
		return account.Sources.Characters, true
	default:
		return 0, false
	}
}
