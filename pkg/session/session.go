package session

import (
	"fmt"
	"net"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/config"
	"dkulpa.eu/game-server-snooze/pkg/games"
	"github.com/sirupsen/logrus"
)

var (
	largestClientPacketSize int
	largestServerPacketSize int
)

type session struct {
	clientAddr   *net.UDPAddr
	serverConn   *net.UDPConn
	module       games.GameModule
	lastActivity time.Time
	stopMonitor  chan struct{}
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
		stopMonitor:  make(chan struct{}),
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
	logrus.Debugf("Received %d bytes from server for client %s", n, s.clientAddr.String())
	// logrus.Debugf("Payload: % X", data)
	s.touchActivity()
	updateLargestServerPacketSize(n)

	if s.module.DetectClose(data[:n]) {
		logrus.Infof("Closing connection for client %s",
			s.clientAddr.String(),
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

	for {
		select {
		case <-ticker.C:
			if time.Since(s.lastActivity) > config.GlobalConfig.IdleTimeout {
				logrus.Infof("Session %s idle for over %v, removing", clientKey, config.GlobalConfig.IdleTimeout)
				removeSession(clientKey)
				return
			}
		case <-s.stopMonitor:
			logrus.Infof("Stopping idle monitor for %s", clientKey)
			return
		}
	}
}

func delayedSessionRemoval(clientKey string) {
	time.Sleep(1 * time.Second)
	sess, ok := getSession(clientKey)
	if ok {
		close(sess.stopMonitor)
	}
	removeSession(clientKey)
}

func HandleClientPacket(
	proxyConn *net.UDPConn,
	serverAddr *net.UDPAddr,
	clientAddr *net.UDPAddr,
	data []byte,
	module games.GameModule,
) {
	logrus.Debugf("Received %d bytes from client %s", len(data), clientAddr)
	// logrus.Debugf("Payload: % X", data)
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
	if sessionCount() >= config.GlobalConfig.MaxSessions {
		logrus.Infof("Max sessions (%d) reached. Refusing new session for %s", config.GlobalConfig.MaxSessions, clientAddr)
		return
	}
	logrus.Infof("Creating new session for %s", clientAddr)
	newSess, err := createSession(clientAddr, serverAddr, proxyConn, config.GlobalConfig.MaxPacketSize, module)
	if err != nil {
		logrus.Errorf("Failed to create session for %s: %v", clientAddr, err)
		return
	}
	addSession(clientAddr.String(), newSess)
	if _, werr := newSess.serverConn.WriteToUDP(data, serverAddr); werr != nil {
		logrus.Errorf("Error forwarding data to server: %v", werr)
	}
}

func UpdateLargestClientPacketSize(n int) {
	if n > largestClientPacketSize {
		largestClientPacketSize = n
		logrus.Debugf("Largest client packet size: %d", largestClientPacketSize)
	}
}

func updateLargestServerPacketSize(n int) {
	if n > largestServerPacketSize {
		largestServerPacketSize = n
		logrus.Debugf("Largest server packet size: %d", largestServerPacketSize)
	}
}
