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
			if err != nil {
				logrus.Errorf("Error reading from server (session %s): %v", clientAddr, err)
				return
			}
			handleServerPacket(s, proxyConn, buf, n)
		}
	}()

	return s, nil
}

func handleServerPacket(s *session, proxyConn *net.UDPConn, data []byte, n int) {
	s.touchActivity()
	updateLargestServerPacketSize(n)

	if s.module.DetectClose(data[:n]) {
		logrus.Infof("[CLOSE DETECTED] from server for client %s at %s\n  Payload(%d): % X",
			s.clientAddr.String(),
			time.Now().Format(time.RFC3339),
			n,
			data[:n],
		)
		go delayedSessionRemoval(s.clientAddr.String())
	}

	if _, werr := proxyConn.WriteToUDP(data[:n], s.clientAddr); werr != nil {
		logrus.Errorf("Error forwarding to client %s: %v", s.clientAddr, werr)
		return
	}
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

func handleClientPacket(
	proxyConn *net.UDPConn,
	serverAddr *net.UDPAddr,
	clientAddr *net.UDPAddr,
	data []byte,
	cfg *Config,
	module games.GameModule,
) {
	sess, ok := getSession(clientAddr.String())
	if ok {
		sess.touchActivity()
		if _, werr := sess.serverConn.WriteToUDP(data, serverAddr); werr != nil {
			logrus.Errorf("Error forwarding data to server: %v", werr)
		}
		return
	}
	if !module.DetectStart(data) {
		logrus.Debugf("Ignoring new connection from %s: no start signature found", clientAddr)
		return
	}
	if sessionCount() >= cfg.MaxSessions {
		logrus.Infof("Max sessions (%d) reached. Refusing new session for %s", cfg.MaxSessions, clientAddr)
		return
	}
	newSess, err := createSession(clientAddr, serverAddr, proxyConn, cfg.MaxPacketSize, module)
	if err != nil {
		logrus.Errorf("Failed to create session for %s: %v", clientAddr, err)
		return
	}
	addSession(clientAddr.String(), newSess)
	if _, werr := newSess.serverConn.WriteToUDP(data, serverAddr); werr != nil {
		logrus.Errorf("Error forwarding data to server: %v", werr)
	}
}
