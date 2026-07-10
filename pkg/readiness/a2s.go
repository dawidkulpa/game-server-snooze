package readiness

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

var a2sInfoQuery = append([]byte{0xff, 0xff, 0xff, 0xff, 0x54}, []byte("Source Engine Query\x00")...)

type A2SProbe struct {
	address      *net.UDPAddr
	pollInterval time.Duration
}

func NewA2SProbe(address string, pollInterval time.Duration) (*A2SProbe, error) {
	if pollInterval <= 0 {
		return nil, fmt.Errorf("A2S poll interval must be positive")
	}
	resolved, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("resolve A2S address: %w", err)
	}
	return &A2SProbe{address: resolved, pollInterval: pollInterval}, nil
}

func (probe *A2SProbe) WaitReady(ctx context.Context) error {
	conn, err := net.DialUDP("udp", nil, probe.address)
	if err != nil {
		return fmt.Errorf("dial A2S endpoint: %w", err)
	}
	defer conn.Close()
	query := append([]byte(nil), a2sInfoQuery...)
	response := make([]byte, 1400)

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for A2S readiness: %w", err)
		}
		if _, err := conn.Write(query); err != nil {
			if err := waitForRetry(ctx, probe.pollInterval); err != nil {
				return fmt.Errorf("wait for A2S readiness: %w", err)
			}
			query = append(query[:0], a2sInfoQuery...)
			continue
		}
		deadline := time.Now().Add(probe.pollInterval)
		if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
			deadline = contextDeadline
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return fmt.Errorf("set A2S read deadline: %w", err)
		}
		n, _, flags, _, err := conn.ReadMsgUDP(response, nil)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("wait for A2S readiness: %w", ctx.Err())
			}
			if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
				if err := waitForRetry(ctx, probe.pollInterval); err != nil {
					return fmt.Errorf("wait for A2S readiness: %w", err)
				}
			}
			query = append(query[:0], a2sInfoQuery...)
			continue
		}
		if flags&unix.MSG_TRUNC != 0 || n < 5 || response[0] != 0xff || response[1] != 0xff || response[2] != 0xff || response[3] != 0xff {
			if err := waitForRetry(ctx, probe.pollInterval); err != nil {
				return fmt.Errorf("wait for A2S readiness: %w", err)
			}
			query = append(query[:0], a2sInfoQuery...)
			continue
		}
		switch response[4] {
		case 0x49:
			if validA2SInfo(response[:n]) {
				return nil
			}
		case 0x41:
			if n >= 9 {
				query = append(query[:0], a2sInfoQuery...)
				query = append(query, response[5:9]...)
				continue
			}
		}
		if err := waitForRetry(ctx, probe.pollInterval); err != nil {
			return fmt.Errorf("wait for A2S readiness: %w", err)
		}
		query = append(query[:0], a2sInfoQuery...)
	}
}

func validA2SInfo(response []byte) bool {
	if len(response) < 6 || response[4] != 0x49 {
		return false
	}
	offset := 6   // header, response type, protocol byte
	for range 4 { // server name, map, folder, game
		end := offset
		for end < len(response) && response[end] != 0 {
			end++
		}
		if end >= len(response) {
			return false
		}
		offset = end + 1
	}
	// app ID plus players, max players, bots, server type, environment,
	// visibility, and VAC fields.
	if len(response)-offset < 9 {
		return false
	}
	offset += 9
	for offset < len(response) && response[offset] != 0 { // version
		offset++
	}
	return offset < len(response)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
