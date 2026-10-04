package httpapi

import (
	"fmt"
	"unicode/utf8"

	"calendium/backend/internal/domain"
)

// Handler-layer field limits (spec decision 6). Titles/names/subjects/
// slugs/labels count runes; long text counts bytes; arrays count items.
// Stricter domain limits (team name 120, comment body) stay authoritative
// because they run after these in the service.
const (
	maxTitleRunes = 500
	maxTextBytes  = 64 << 10
	maxEmailRunes = 320
	maxURLRunes   = 2048
	maxArrayItems = 500
)

// limitError is the ErrValidation-shaped failure writeError turns into
// 400 validation_failed with details {"field","limit"}.
type limitError struct {
	field string
	limit int
}

func (e *limitError) Error() string {
	return fmt.Sprintf("%v: %s exceeds %d", domain.ErrValidation, e.field, e.limit)
}

// Is makes errors.Is(err, domain.ErrValidation) true.
func (e *limitError) Is(target error) bool { return target == domain.ErrValidation }

// fieldCheck collects the FIRST limit violation of a decoded payload.
type fieldCheck struct{ first error }

func (c *fieldCheck) fail(field string, limit int) {
	if c.first == nil {
		c.first = &limitError{field: field, limit: limit}
	}
}

func (c *fieldCheck) title(field, v string) {
	if utf8.RuneCountInString(v) > maxTitleRunes {
		c.fail(field, maxTitleRunes)
	}
}

func (c *fieldCheck) text(field, v string) {
	if len(v) > maxTextBytes {
		c.fail(field, maxTextBytes)
	}
}

// textUpTo is text with a route-specific byte limit.
func (c *fieldCheck) textUpTo(field, v string, limit int) {
	if len(v) > limit {
		c.fail(field, limit)
	}
}

func (c *fieldCheck) email(field, v string) {
	if utf8.RuneCountInString(v) > maxEmailRunes {
		c.fail(field, maxEmailRunes)
	}
}

func (c *fieldCheck) url(field, v string) {
	if utf8.RuneCountInString(v) > maxURLRunes {
		c.fail(field, maxURLRunes)
	}
}

func (c *fieldCheck) list(field string, n int) {
	if n > maxArrayItems {
		c.fail(field, maxArrayItems)
	}
}

// emails checks the count and every element of an address list.
func (c *fieldCheck) emails(field string, vs []string) {
	c.list(field, len(vs))
	for _, v := range vs {
		c.email(field, v)
	}
}

// urls checks the count and every element of a URL list.
func (c *fieldCheck) urls(field string, vs []string) {
	c.list(field, len(vs))
	for _, v := range vs {
		c.url(field, v)
	}
}

func (c *fieldCheck) optTitle(field string, v *string) {
	if v != nil {
		c.title(field, *v)
	}
}

func (c *fieldCheck) optText(field string, v *string) {
	if v != nil {
		c.text(field, *v)
	}
}

// err returns the first violation, or nil.
func (c *fieldCheck) err() error { return c.first }
