package games

type GameModule interface {
	// DetectStart returns true if this packet indicates
	// a real attempt to connect/join the game.
	DetectStart(data []byte) bool

	// DetectClose returns true if this packet indicates
	// the server is closing (sent server->client),
	// or if the client is disconnecting (client->server).
	DetectClose(data []byte) bool
}
