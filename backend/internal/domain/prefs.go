package domain

import "fmt"

type UserPrefs struct {
	SplitOrder []InboxSplit `json:"splitOrder"`
}

// Validate rejects unknown or duplicate splits; an empty order is valid
// ("use the default").
func (p UserPrefs) Validate() error {
	seen := make(map[InboxSplit]struct{}, len(p.SplitOrder))
	for _, s := range p.SplitOrder {
		if _, err := ParseInboxSplit(string(s)); err != nil {
			return err
		}
		if _, dup := seen[s]; dup {
			return fmt.Errorf("%w: duplicate split %q in splitOrder", ErrValidation, s)
		}
		seen[s] = struct{}{}
	}
	return nil
}
