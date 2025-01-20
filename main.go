package main

import (
	"net"

	"dkulpa.eu/game-server-snooze/games"
	"github.com/sirupsen/logrus"
)

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		logrus.Fatalf("Error loading config: %v", err)
	}

	setGlobalConfig(cfg)

	var gameModule games.GameModule
	switch cfg.Game {
	case "palworld":
		gameModule = games.NewPalworldGame()
	default:
		logrus.Fatalf("Unsupported game: %s", cfg.Game)
	}

	ptero := NewPterodactylController(cfg.Pterodactyl)
	setPterodactylController(ptero)

	status, err := ptero.GetStatus()
	if err != nil {
		logrus.Fatalf("Error getting server status: %v", err)
	}
	logrus.Infof("Game server status: %s", status)

	localAddr, err := net.ResolveUDPAddr("udp", cfg.ListenAddr)
	if err != nil {
		logrus.Fatalf("ResolveUDPAddr error: %v", err)
	}
	proxyConn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		logrus.Fatalf("ListenUDP error: %v", err)
	}
	defer proxyConn.Close()

	logrus.Infof("UDP proxy listening on %s (game=%s)", cfg.ListenAddr, cfg.Game)

	serverAddr, err := net.ResolveUDPAddr("udp", cfg.ServerAddr)
	if err != nil {
		logrus.Fatalf("ResolveUDPAddr (server) error: %v", err)
	}

	buf := make([]byte, cfg.MaxPacketSize)

	for {
		n, clientAddr, err := proxyConn.ReadFromUDP(buf)
		if err != nil {
			logrus.Errorf("Error reading from client: %v", err)
			continue
		}
		updateLargestClientPacketSize(n)

		clientKey := clientAddr.String()
		sess, ok := getSession(clientKey)
		if ok {
			sess.touchActivity()
		} else {
			if !gameModule.DetectStart(buf[:n]) {
				logrus.Debugf("Ignoring new connection from %s: no start signature found", clientAddr)
				continue
			}
			if sessionCount() >= cfg.MaxSessions {
				logrus.Infof("Max sessions (%d) reached. Refusing new session for %s", cfg.MaxSessions, clientKey)
				continue
			}
			newSess, sessErr := createSession(clientAddr, serverAddr, proxyConn, cfg.MaxPacketSize, gameModule)
			if sessErr != nil {
				logrus.Errorf("Failed to create session for %s: %v", clientKey, sessErr)
				continue
			}
			addSession(clientKey, newSess)
			sess = newSess
		}

		if _, werr := sess.serverConn.WriteToUDP(buf[:n], serverAddr); werr != nil {
			logrus.Errorf("Error forwarding data from %s to server: %v", clientAddr, werr)
		}
	}
}
