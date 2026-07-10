package games

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestConfiguredPalworldGameMatchesAnyConfiguredSignature(t *testing.T) {
	game, err := NewConfiguredPalworldGame(PalworldOptions{
		WakePolicy: "signature",
		Signatures: []string{"010203", "aabb"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !game.DetectStart([]byte{0xaa, 0xbb, 0xcc}) {
		t.Fatal("DetectStart() did not match the second configured signature")
	}
	if game.DetectStart([]byte{0x01, 0x02}) {
		t.Fatal("DetectStart() matched a packet shorter than its configured signature")
	}
	if game.DetectStart([]byte{0x99, 0x88, 0x77}) {
		t.Fatal("DetectStart() matched an unrelated packet")
	}
}

func TestAnyWakePolicyAcceptsOnlyNonEmptyPackets(t *testing.T) {
	game, err := NewConfiguredPalworldGame(PalworldOptions{WakePolicy: "any"})
	if err != nil {
		t.Fatal(err)
	}

	if !game.DetectStart([]byte{0x42}) {
		t.Fatal("DetectStart() rejected a non-empty packet under any policy")
	}
	if game.DetectStart(nil) {
		t.Fatal("DetectStart() accepted an empty packet under any policy")
	}
}

func TestUnmatchedDiagnosticIsPrefixBoundedAndRateLimited(t *testing.T) {
	now := time.Unix(100, 0)
	var lengths []int
	var prefixes [][]byte
	game, err := NewConfiguredPalworldGame(PalworldOptions{
		WakePolicy:            "signature",
		Signatures:            []string{"0102"},
		LogUnmatchedPrefixes:  true,
		DiagnosticPrefixBytes: 2,
		DiagnosticInterval:    time.Minute,
		Diagnostic: func(length int, prefix []byte) {
			lengths = append(lengths, length)
			prefixes = append(prefixes, append([]byte(nil), prefix...))
		},
		now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	packet := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	game.DetectStart(packet)
	game.DetectStart(packet)
	if len(lengths) != 1 || lengths[0] != 4 || !bytes.Equal(prefixes[0], []byte{0xaa, 0xbb}) {
		t.Fatalf("unexpected first diagnostic: lengths=%v prefixes=%x", lengths, prefixes)
	}

	now = now.Add(time.Minute)
	game.DetectStart(packet)
	if len(lengths) != 2 {
		t.Fatalf("diagnostic did not resume after interval: count=%d", len(lengths))
	}
}

func TestDefaultPalworldGameMatchesLegacyWakeSignature(t *testing.T) {
	game := NewPalworldGame()
	packet := []byte{0x09, 0x08, 0x00, 0x04, 0xbc, 0x59, 0x7e, 0x73, 0xff}
	if !game.DetectStart(packet) {
		t.Fatal("default detector rejected the legacy Palworld wake signature")
	}
}

func TestConfiguredPalworldGameTrimsSignaturesAndBoundsDiagnostics(t *testing.T) {
	game, err := NewConfiguredPalworldGame(PalworldOptions{
		WakePolicy:            "signature",
		Signatures:            []string{" 0102 "},
		DiagnosticPrefixBytes: 32,
	})
	if err != nil {
		t.Fatalf("trimmed signature rejected: %v", err)
	}
	if !game.DetectStart([]byte{0x01, 0x02}) {
		t.Fatal("trimmed signature did not match")
	}
	if _, err := NewConfiguredPalworldGame(PalworldOptions{
		WakePolicy:            "signature",
		Signatures:            []string{"0102"},
		DiagnosticPrefixBytes: 33,
	}); err == nil {
		t.Fatal("constructor accepted a diagnostic prefix above the hard bound")
	}
}

func TestUnmatchedDiagnosticRateLimitIsConcurrentSafe(t *testing.T) {
	now := time.Unix(100, 0)
	var calls atomic.Int32
	game, err := NewConfiguredPalworldGame(PalworldOptions{
		WakePolicy:            "signature",
		Signatures:            []string{"0102"},
		LogUnmatchedPrefixes:  true,
		DiagnosticPrefixBytes: 2,
		DiagnosticInterval:    time.Minute,
		Diagnostic: func(int, []byte) {
			calls.Add(1)
		},
		now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			game.DetectStart([]byte{0xaa, 0xbb, 0xcc})
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent diagnostics = %d, want 1", got)
	}
}

func TestDefaultUnmatchedDiagnosticIsVisibleAndBounded(t *testing.T) {
	logger := logrus.StandardLogger()
	oldOutput, oldLevel, oldFormatter := logger.Out, logger.Level, logger.Formatter
	var output bytes.Buffer
	logger.SetOutput(&output)
	logger.SetLevel(logrus.InfoLevel)
	logger.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	t.Cleanup(func() {
		logger.SetOutput(oldOutput)
		logger.SetLevel(oldLevel)
		logger.SetFormatter(oldFormatter)
	})
	game, err := NewConfiguredPalworldGame(PalworldOptions{
		WakePolicy:            "signature",
		Signatures:            []string{"0102"},
		LogUnmatchedPrefixes:  true,
		DiagnosticPrefixBytes: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	game.DetectStart([]byte{0xaa, 0xbb, 0xcc})
	logged := output.String()
	if !strings.Contains(logged, "length=3") || !strings.Contains(logged, "AA BB") || strings.Contains(logged, "CC") {
		t.Fatalf("unexpected bounded diagnostic: %q", logged)
	}
}
