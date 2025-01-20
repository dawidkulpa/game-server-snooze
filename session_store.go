package main

import (
	"sync"

	"github.com/sirupsen/logrus"
)

var (
	sessions   = make(map[string]*session)
	sessionsMu sync.Mutex
)

func addSession(clientKey string, s *session) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	oldCount := len(sessions)
	sessions[clientKey] = s
	newCount := len(sessions)

	logrus.Infof("Session created for %s. Current session count: %d", clientKey, newCount)

	if oldCount == 0 {
		// Start the server if needed
		go startServerIfNotRunning()
		// Cancel the auto-stop timer
		cancelAutoStopTimer()
	}
}

func removeSession(clientKey string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	delete(sessions, clientKey)
	count := len(sessions)
	logrus.Infof("Session removed for %s. Current session count: %d", clientKey, count)

	// If no sessions left, schedule auto-stop
	if count == 0 {
		scheduleAutoStop()
	}
}

func getSession(clientKey string) (*session, bool) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	s, ok := sessions[clientKey]
	return s, ok
}

func sessionCount() int {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	return len(sessions)
}
