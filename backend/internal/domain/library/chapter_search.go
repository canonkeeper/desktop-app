package library

// ChapterNavigationLocator opens manuscript text, not an indexed citation.
type ChapterNavigationLocator struct {
	ProjectID  string `json:"projectId"`
	BookID     string `json:"bookId"`
	ChapterID  string `json:"chapterId"`
	TargetView string `json:"targetView"`
	Revision   int    `json:"revision"`
}

type ChapterNavigationSearchResult struct {
	ProjectTitle  string                   `json:"projectTitle"`
	BookTitle     string                   `json:"bookTitle"`
	ChapterTitle  string                   `json:"chapterTitle"`
	ChapterNumber string                   `json:"chapterNumber"`
	Locator       ChapterNavigationLocator `json:"locator"`
}

type ChapterSearchRequest struct {
	Query      string `json:"query"`
	SourceView string `json:"sourceView"`
	Limit      int    `json:"limit,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
}

type ChapterSearchPage struct {
	Data []ChapterNavigationSearchResult `json:"data"`
	Meta ChapterSearchPageMeta           `json:"meta"`
}

type ChapterSearchPageMeta struct {
	HasMore    bool   `json:"hasMore"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type ChapterSearchKey struct {
	ProjectID string `json:"projectId"`
	BookID    string `json:"bookId"`
	ChapterID string `json:"chapterId"`
}

type ChapterSearchInput struct {
	Actor      Actor
	Query      string
	Number     string
	SourceView string
	Limit      int
	After      *ChapterSearchKey
}
