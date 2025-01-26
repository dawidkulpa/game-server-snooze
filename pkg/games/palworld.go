package games

import (
	"bytes"

	"github.com/sirupsen/logrus"
)

type PalworldGame struct {
	startConnectionSig []byte
	closeConnectionSig []byte
}

func NewPalworldGame() *PalworldGame {
	return &PalworldGame{
		startConnectionSig: []byte{0x09, 0x08, 0x00, 0x04, 0xbc, 0x59, 0x7e, 0x73},
		closeConnectionSig: []byte{
			0x43, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x43,
			0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x43, 0x6c,
			0x6f, 0x73, 0x65,
		},
	}
}

func (pw *PalworldGame) DetectStart(data []byte) bool {
	if len(data) >= len(pw.startConnectionSig) &&
		bytes.Equal(data[:len(pw.startConnectionSig)], pw.startConnectionSig) {
		logrus.Debugf("Detected Palworld start connection, sent packet: % X", data)
		return true
	}
	return false
}

func (pw *PalworldGame) DetectClose(data []byte) bool {
	// 128 is here to not parse larger packets, as the close connection packet is small, and 128 feels like a good limit
	if len(data) < 128 && len(data) >= len(pw.closeConnectionSig) &&
		bytes.Contains(data, pw.closeConnectionSig) {
		logrus.Debugf("Detected Palworld stop connection, sent packet: % X", data)
		return true
	}
	return false
}
