package domain

import "time"

// User is a Calendium user keyed by the Better Auth JWT `sub` claim.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      *string   `json:"name"`
	AvatarURL *string   `json:"avatarUrl"`
	CreatedAt time.Time `json:"createdAt"`
}
