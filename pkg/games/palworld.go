package games

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const defaultPalworldStartSignature = "09080004bc597e73"

type PalworldOptions struct {
	WakePolicy            string
	Signatures            []string
	LogUnmatchedPrefixes  bool
	DiagnosticPrefixBytes int
	DiagnosticInterval    time.Duration
	Diagnostic            func(length int, prefix []byte)
	now                   func() time.Time
}

type PalworldGame struct {
	wakePolicy      string
	startSignatures [][]byte

	logUnmatched       bool
	diagnosticPrefix   int
	diagnosticInterval time.Duration
	diagnostic         func(length int, prefix []byte)
	now                func() time.Time
	diagnosticMu       sync.Mutex
	lastDiagnostic     time.Time
}

func NewPalworldGame() *PalworldGame {
	game, err := NewConfiguredPalworldGame(PalworldOptions{
		WakePolicy: "signature",
		Signatures: []string{defaultPalworldStartSignature},
	})
	if err != nil {
		panic(err)
	}
	return game
}

func NewConfiguredPalworldGame(options PalworldOptions) (*PalworldGame, error) {
	if options.WakePolicy != "signature" && options.WakePolicy != "any" {
		return nil, fmt.Errorf("invalid Palworld wake policy %q", options.WakePolicy)
	}
	if options.WakePolicy == "signature" && len(options.Signatures) == 0 {
		return nil, fmt.Errorf("at least one Palworld wake signature is required")
	}
	if options.DiagnosticPrefixBytes <= 0 {
		options.DiagnosticPrefixBytes = 8
	}
	if options.DiagnosticPrefixBytes > 32 {
		return nil, fmt.Errorf("palworld diagnostic prefix must not exceed 32 bytes")
	}
	if options.DiagnosticInterval <= 0 {
		options.DiagnosticInterval = 30 * time.Second
	}
	if options.now == nil {
		options.now = time.Now
	}
	if options.Diagnostic == nil {
		options.Diagnostic = func(length int, prefix []byte) {
			logrus.Infof("Unmatched Palworld candidate: length=%d prefix=% X", length, prefix)
		}
	}
	game := &PalworldGame{
		wakePolicy:         options.WakePolicy,
		logUnmatched:       options.LogUnmatchedPrefixes,
		diagnosticPrefix:   options.DiagnosticPrefixBytes,
		diagnosticInterval: options.DiagnosticInterval,
		diagnostic:         options.Diagnostic,
		now:                options.now,
	}
	if options.WakePolicy == "signature" {
		for _, encoded := range options.Signatures {
			encoded = strings.TrimSpace(encoded)
			signature, err := hex.DecodeString(encoded)
			if err != nil || len(signature) == 0 {
				return nil, fmt.Errorf("invalid Palworld wake signature %q", encoded)
			}
			game.startSignatures = append(game.startSignatures, signature)
		}
	}
	return game, nil
}

func (pw *PalworldGame) DetectStart(data []byte) bool {
	if pw.wakePolicy == "any" {
		return len(data) > 0
	}
	for _, signature := range pw.startSignatures {
		if len(data) >= len(signature) && bytes.Equal(data[:len(signature)], signature) {
			return true
		}
	}
	pw.diagnoseUnmatched(data)
	return false
}

func (pw *PalworldGame) diagnoseUnmatched(data []byte) {
	if !pw.logUnmatched || len(data) == 0 {
		return
	}
	now := pw.now()
	pw.diagnosticMu.Lock()
	if !pw.lastDiagnostic.IsZero() && now.Sub(pw.lastDiagnostic) < pw.diagnosticInterval {
		pw.diagnosticMu.Unlock()
		return
	}
	pw.lastDiagnostic = now
	pw.diagnosticMu.Unlock()

	prefixLength := min(len(data), pw.diagnosticPrefix)
	prefix := append([]byte(nil), data[:prefixLength]...)
	pw.diagnostic(len(data), prefix)
}
