package push

import (
	"sync"

	"github.com/SherClockHolmes/webpush-go"
)

// Store keeps push subscriptions in memory keyed by endpoint.
type Store struct {
	mu    sync.RWMutex
	items map[string]*webpush.Subscription
}

// NewStore creates an empty subscription store.
func NewStore() *Store {
	return &Store{
		items: make(map[string]*webpush.Subscription),
	}
}

// Add inserts or replaces a subscription by endpoint.
func (s *Store) Add(subscription *webpush.Subscription) {
	if subscription == nil || subscription.Endpoint == "" {
		return
	}

	clone := *subscription

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[subscription.Endpoint] = &clone
}

// Remove deletes a subscription by endpoint and reports whether it existed.
func (s *Store) Remove(endpoint string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[endpoint]; !ok {
		return false
	}

	delete(s.items, endpoint)
	return true
}

// List returns a snapshot of all known subscriptions.
func (s *Store) List() []*webpush.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*webpush.Subscription, 0, len(s.items))
	for _, item := range s.items {
		clone := *item
		result = append(result, &clone)
	}

	return result
}

// Count reports the number of subscriptions in memory.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}
