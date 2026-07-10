package session

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrSessionCapacity = errors.New("session capacity reached")

type Store struct {
	mu       sync.RWMutex
	max      int
	sessions map[string]*Session
}

func NewStore(max int) (*Store, error) {
	if max < 1 {
		return nil, fmt.Errorf("session capacity must be positive")
	}
	return &Store{
		max:      max,
		sessions: make(map[string]*Session),
	}, nil
}

func (s *Store) GetOrCreate(key string, create func() (*Session, error)) (*Session, bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.sessions[key]; ok {
		return existing, false, len(s.sessions), nil
	}
	if len(s.sessions) >= s.max {
		return nil, false, len(s.sessions), ErrSessionCapacity
	}
	created, err := create()
	if err != nil {
		return nil, false, len(s.sessions), err
	}
	if created == nil {
		return nil, false, len(s.sessions), fmt.Errorf("session factory returned nil")
	}
	s.sessions[key] = created
	return created, true, len(s.sessions), nil
}

func (s *Store) Get(key string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.sessions[key]
	return value, ok
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

func (s *Store) Remove(key string, expected *Session) (bool, int, error) {
	s.mu.Lock()
	current, ok := s.sessions[key]
	if !ok || current != expected {
		count := len(s.sessions)
		s.mu.Unlock()
		return false, count, nil
	}
	delete(s.sessions, key)
	count := len(s.sessions)
	s.mu.Unlock()

	return true, count, current.Close()
}

func (s *Store) ExpireIdle(cutoff time.Time) (int, int, error) {
	s.mu.Lock()
	toClose := make([]*Session, 0)
	for key, value := range s.sessions {
		if value.TryExpire(cutoff) {
			delete(s.sessions, key)
			toClose = append(toClose, value)
		}
	}
	count := len(s.sessions)
	s.mu.Unlock()

	var result error
	for _, value := range toClose {
		result = errors.Join(result, value.Close())
	}
	return len(toClose), count, result
}

func (s *Store) CloseAll() error {
	s.mu.Lock()
	toClose := make([]*Session, 0, len(s.sessions))
	for key, value := range s.sessions {
		delete(s.sessions, key)
		toClose = append(toClose, value)
	}
	s.mu.Unlock()

	var result error
	for _, value := range toClose {
		result = errors.Join(result, value.Close())
	}
	return result
}
