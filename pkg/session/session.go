package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

var ErrSessionClosed = errors.New("session closed")

type SessionOptions struct {
	ClientAddr    *net.UDPAddr
	BackendAddr   *net.UDPAddr
	PublicConn    *net.UDPConn
	MaxPacketSize int
	clock         func() time.Time
}

type Session struct {
	clientAddr *net.UDPAddr
	publicConn *net.UDPConn
	upstream   *net.UDPConn
	maxPacket  int
	clock      func() time.Time

	lastActivity atomic.Int64
	closed       atomic.Bool
	activityMu   sync.Mutex
	closeOnce    sync.Once
	goroutineWG  sync.WaitGroup
	cancel       context.CancelFunc
	done         chan struct{}
}

func NewSession(parent context.Context, options SessionOptions) (*Session, error) {
	if options.ClientAddr == nil || options.BackendAddr == nil || options.PublicConn == nil {
		return nil, fmt.Errorf("client, backend, and public UDP addresses are required")
	}
	if options.MaxPacketSize < 1 {
		return nil, fmt.Errorf("max packet size must be positive")
	}
	if options.clock == nil {
		options.clock = time.Now
	}

	upstream, err := net.DialUDP("udp", nil, options.BackendAddr)
	if err != nil {
		return nil, fmt.Errorf("connect backend UDP socket: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	s := &Session{
		clientAddr: options.ClientAddr,
		publicConn: options.PublicConn,
		upstream:   upstream,
		maxPacket:  options.MaxPacketSize,
		clock:      options.clock,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	s.touch()
	s.goroutineWG.Add(2)
	go s.readBackend()
	go s.watchContext(ctx)
	return s, nil
}

func (s *Session) watchContext(ctx context.Context) {
	defer s.goroutineWG.Done()
	select {
	case <-ctx.Done():
		s.closeResources()
	case <-s.done:
	}
}

func (s *Session) readBackend() {
	defer s.goroutineWG.Done()
	defer close(s.done)
	defer s.closeResources()

	buffer := make([]byte, s.maxPacket)
	for {
		n, _, flags, _, err := s.upstream.ReadMsgUDP(buffer, nil)
		if err != nil {
			return
		}
		if n == 0 || flags&unix.MSG_TRUNC != 0 {
			continue
		}
		if !s.touch() {
			return
		}
		if _, err := s.publicConn.WriteToUDP(buffer[:n], s.clientAddr); err != nil {
			return
		}
	}
}

func (s *Session) RecordActivity() error {
	if !s.touch() {
		return ErrSessionClosed
	}
	return nil
}

func (s *Session) ForwardClient(data []byte) error {
	if err := s.RecordActivity(); err != nil {
		return err
	}
	if _, err := s.upstream.Write(data); err != nil {
		if s.closed.Load() {
			return ErrSessionClosed
		}
		s.closeResources()
		return fmt.Errorf("forward client packet: %w", err)
	}
	return nil
}

func (s *Session) touch() bool {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	if s.closed.Load() {
		return false
	}
	s.lastActivity.Store(s.clock().UnixNano())
	return true
}

func (s *Session) LastActivity() time.Time {
	return time.Unix(0, s.lastActivity.Load())
}

func (s *Session) TryExpire(cutoff time.Time) bool {
	s.activityMu.Lock()
	if s.closed.Load() || !s.LastActivity().Before(cutoff) {
		s.activityMu.Unlock()
		return false
	}
	s.closed.Store(true)
	s.activityMu.Unlock()

	s.closeOnce.Do(s.closeSocket)
	return true
}

func (s *Session) Done() <-chan struct{} {
	return s.done
}

func (s *Session) closeSocket() {
	s.cancel()
	_ = s.upstream.Close()
}

func (s *Session) closeResources() {
	s.closeOnce.Do(func() {
		s.activityMu.Lock()
		s.closed.Store(true)
		s.activityMu.Unlock()
		s.closeSocket()
	})
}

func (s *Session) Close() error {
	s.closeResources()
	s.goroutineWG.Wait()
	return nil
}
