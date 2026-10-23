package domain

// ChatMessageSource reuses the search citation projection. It is hydrated only
// after checking the current user's access, including for historical versions.
type ChatMessageSource struct {
	SearchResult
	Position int `json:"position"`
}
