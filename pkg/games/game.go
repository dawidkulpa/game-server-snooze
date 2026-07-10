package games

type GameModule interface {
	// DetectStart returns true if this packet indicates
	// a real attempt to connect/join the game.
	DetectStart(data []byte) bool
}
