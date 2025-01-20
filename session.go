package main

import (
	"fmt"
	"net"
	"time"

	"dkulpa.eu/game-server-snooze/games"
	"github.com/sirupsen/logrus"
)

type session struct {
	clientAddr   *net.UDPAddr
	serverConn   *net.UDPConn
	module       games.GameModule
	lastActivity time.Time
}

// createSession spawns a goroutine to handle server->client traffic
func createSession(
	clientAddr, serverAddr *net.UDPAddr,
	proxyConn *net.UDPConn,
	maxPacketSize int,
	module games.GameModule,
) (*session, error) {

	connToServer, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to listen UDP for server: %v", err)
	}

	s := &session{
		clientAddr:   clientAddr,
		serverConn:   connToServer,
		module:       module,
		lastActivity: time.Now(),
	}

	go s.idleMonitor(clientAddr.String())

	go func() {
		defer func() {
			connToServer.Close()
			logrus.Infof("Server->client loop ended for %s", clientAddr)
		}()

		buf := make([]byte, maxPacketSize)

		for {
			n, _, err := connToServer.ReadFromUDP(buf)
			s.touchActivity()
			if err != nil {
				logrus.Errorf("Error reading from server (session %s): %v", clientAddr, err)
				return
			}

			updateLargestServerPacketSize(n)

			if module.DetectClose(buf[:n]) {
				logrus.Infof("[CLOSE DETECTED] from server for client %s at %s\n  Payload(%d): % X",
					clientAddr.String(),
					time.Now().Format(time.RFC3339),
					n,
					buf[:n],
				)

				go delayedSessionRemoval(clientAddr.String())
				// we do NOT return immediately, to let any final packets forward
			}

			// Forward data back to the client
			_, werr := proxyConn.WriteToUDP(buf[:n], clientAddr)
			if werr != nil {
				logrus.Errorf("Error forwarding to client %s: %v", clientAddr, werr)
				return
			}
		}
	}()

	return s, nil
}

func (s *session) touchActivity() {
	s.lastActivity = time.Now()
}

func (s *session) idleMonitor(clientKey string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if time.Since(s.lastActivity) > globalConfig.IdleTimeout {
			logrus.Infof("Session %s idle for over %v, removing", clientKey, globalConfig.IdleTimeout)
			removeSession(clientKey)
			return
		}
	}
}

func delayedSessionRemoval(clientKey string) {
	time.Sleep(1 * time.Second)
	removeSession(clientKey)
}
