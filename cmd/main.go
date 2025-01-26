package main

import (
	"net"

	"dkulpa.eu/game-server-snooze/pkg/config"
	"dkulpa.eu/game-server-snooze/pkg/games"
	"dkulpa.eu/game-server-snooze/pkg/server"
	"dkulpa.eu/game-server-snooze/pkg/session"
	"github.com/sirupsen/logrus"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		logrus.Fatalf("Error loading config: %v", err)
	}

	var gameModule games.GameModule
	switch cfg.Game {
	case "palworld":
		gameModule = games.NewPalworldGame()
	default:
		logrus.Fatalf("Unsupported game: %s", cfg.Game)
	}

	ptero := server.NewPterodactylController()

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
		session.UpdateLargestClientPacketSize(n)

		session.HandleClientPacket(
			proxyConn,
			serverAddr,
			clientAddr,
			buf[:n],
			gameModule,
		)
	}
}
