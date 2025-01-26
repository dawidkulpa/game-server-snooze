package session

import (
	"sync"

	"dkulpa.eu/game-server-snooze/pkg/server"
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
	server.SetSessionCount(newCount)

	if oldCount == 0 {
		go server.StartServerIfNotRunning()
		server.CancelAutoStopTimer()
	}
}

func removeSession(clientKey string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	delete(sessions, clientKey)
	count := len(sessions)
	logrus.Infof("Session removed for %s. Current session count: %d", clientKey, count)
	server.SetSessionCount(count)

	if count == 0 {
		server.ScheduleAutoStop()
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
