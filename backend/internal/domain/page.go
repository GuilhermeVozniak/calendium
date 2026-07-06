package domain

// Page is the standard list envelope: `{ items, nextCursor }`.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}
