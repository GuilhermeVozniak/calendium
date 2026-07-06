package service

import (
	"context"
	"fmt"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const searchLimit = 20

// SearchService implements port.SearchService: unified search over the
// locally mirrored threads and events.
type SearchService struct {
	ent     entitlement
	threads port.ThreadRepo
	events  port.EventRepo
}

var _ port.SearchService = (*SearchService)(nil)

func NewSearchService(subs port.SubscriptionRepo, threads port.ThreadRepo, events port.EventRepo, clock port.Clock) *SearchService {
	return &SearchService{
		ent:     entitlement{subs: subs, clock: clock},
		threads: threads,
		events:  events,
	}
}

func (s *SearchService) Search(ctx context.Context, userID, query string) (port.SearchResult, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return port.SearchResult{}, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return port.SearchResult{}, fmt.Errorf("%w: query is required", domain.ErrValidation)
	}
	threads, err := s.threads.Search(ctx, userID, query, searchLimit)
	if err != nil {
		return port.SearchResult{}, err
	}
	events, err := s.events.Search(ctx, userID, query, searchLimit)
	if err != nil {
		return port.SearchResult{}, err
	}
	if threads == nil {
		threads = []domain.Thread{}
	}
	if events == nil {
		events = []domain.Event{}
	}
	return port.SearchResult{Threads: threads, Events: events}, nil
}
