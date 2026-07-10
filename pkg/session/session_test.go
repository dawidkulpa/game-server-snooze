package session

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionCloseIsIdempotentAndUnblocksReader(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()

	s, err := NewSession(context.Background(), SessionOptions{
		ClientAddr:    &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 45000},
		BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
		PublicConn:    publicConn,
		MaxPacketSize: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_ = s.Close()
		_ = s.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close() did not unblock the backend reader")
	}

	select {
	case <-s.Done():
	default:
		t.Fatal("Done() was not closed")
	}
	if err := s.ForwardClient([]byte("after-close")); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("ForwardClient() after close error = %v, want ErrSessionClosed", err)
	}
}

func TestSessionTracksClientAndBackendActivity(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var clockNanos atomic.Int64
	clockNanos.Store(time.Unix(100, 0).UnixNano())
	s, err := NewSession(context.Background(), SessionOptions{
		ClientAddr:    client.LocalAddr().(*net.UDPAddr),
		BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
		PublicConn:    publicConn,
		MaxPacketSize: 4096,
		clock:         func() time.Time { return time.Unix(0, clockNanos.Load()) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	clockNanos.Store(time.Unix(101, 0).UnixNano())
	if err := s.ForwardClient([]byte("request")); err != nil {
		t.Fatal(err)
	}
	if got := s.LastActivity(); !got.Equal(time.Unix(101, 0)) {
		t.Fatalf("client activity = %v, want %v", got, time.Unix(101, 0))
	}

	buffer := make([]byte, 64)
	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	n, upstreamAddr, err := backend.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "request" {
		t.Fatalf("backend received %q, want request", buffer[:n])
	}
	clockNanos.Store(time.Unix(102, 0).UnixNano())
	if _, err := backend.WriteToUDP([]byte("response"), upstreamAddr); err != nil {
		t.Fatal(err)
	}

	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err = client.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "response" {
		t.Fatalf("client received %q, want response", buffer[:n])
	}
	if got := s.LastActivity(); !got.Equal(time.Unix(102, 0)) {
		t.Fatalf("backend activity = %v, want %v", got, time.Unix(102, 0))
	}
}

func TestSessionDropsTruncatedBackendDatagram(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	s, err := NewSession(context.Background(), SessionOptions{
		ClientAddr:    client.LocalAddr().(*net.UDPAddr),
		BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
		PublicConn:    publicConn,
		MaxPacketSize: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ForwardClient([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	probe := make([]byte, 16)
	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	_, sessionAddr, err := backend.ReadFromUDP(probe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.WriteToUDP([]byte("oversize"), sessionAddr); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, _, err := client.ReadFromUDP(probe); err == nil {
		t.Fatal("truncated datagram prefix was forwarded to the client")
	}
}

func TestSessionRecordActivityRefreshesWithoutForwarding(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()
	var clockNanos atomic.Int64
	clockNanos.Store(time.Unix(100, 0).UnixNano())
	s, err := NewSession(context.Background(), SessionOptions{
		ClientAddr:    &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 45003},
		BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
		PublicConn:    publicConn,
		MaxPacketSize: 4096,
		clock:         func() time.Time { return time.Unix(0, clockNanos.Load()) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	clockNanos.Store(time.Unix(101, 0).UnixNano())
	if err := s.RecordActivity(); err != nil {
		t.Fatal(err)
	}
	if got := s.LastActivity(); !got.Equal(time.Unix(101, 0)) {
		t.Fatalf("recorded activity = %v, want %v", got, time.Unix(101, 0))
	}
	_ = backend.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, _, err := backend.ReadFromUDP(make([]byte, 8)); err == nil {
		t.Fatal("RecordActivity() forwarded a packet")
	}
}

func TestSessionParentCancellationClosesSocketAndDone(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	s, err := NewSession(ctx, SessionOptions{
		ClientAddr:    &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 45001},
		BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
		PublicConn:    publicConn,
		MaxPacketSize: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	select {
	case <-s.Done():
	case <-time.After(time.Second):
		_ = s.Close()
		t.Fatal("parent cancellation did not terminate the session")
	}
	if err := s.ForwardClient([]byte("after-cancel")); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("ForwardClient() after cancel error = %v, want ErrSessionClosed", err)
	}
}

func TestSessionTryExpireAtomicallyClosesIdleSession(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()

	s, err := NewSession(context.Background(), SessionOptions{
		ClientAddr:    &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 45002},
		BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
		PublicConn:    publicConn,
		MaxPacketSize: 4096,
		clock:         func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if !s.TryExpire(time.Unix(150, 0)) {
		_ = s.Close()
		t.Fatal("TryExpire() did not claim an idle session")
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("TryExpire() did not close the backend reader")
	}
	if err := s.ForwardClient([]byte("after-expiry")); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("ForwardClient() after expiry error = %v, want ErrSessionClosed", err)
	}
}

func TestStoreGetOrCreateAtomicallyEnforcesUniquenessAndCapacity(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()

	store, err := NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.CloseAll()

	var createCount atomic.Int32
	create := func() (*Session, error) {
		createCount.Add(1)
		return NewSession(context.Background(), SessionOptions{
			ClientAddr:    &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 46000},
			BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
			PublicConn:    publicConn,
			MaxPacketSize: 4096,
		})
	}

	const callers = 32
	results := make(chan *Session, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _, _, err := store.GetOrCreate("client", create)
			if err != nil {
				errs <- err
				return
			}
			results <- s
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("GetOrCreate() returned error: %v", err)
	}

	var first *Session
	for got := range results {
		if first == nil {
			first = got
		} else if got != first {
			t.Fatal("GetOrCreate() returned different sessions for the same key")
		}
	}
	if createCount.Load() != 1 || store.Len() != 1 {
		t.Fatalf("create count=%d store length=%d, want 1 and 1", createCount.Load(), store.Len())
	}

	capacityCreateCalled := false
	_, _, _, err = store.GetOrCreate("other", func() (*Session, error) {
		capacityCreateCalled = true
		return nil, errors.New("must not be called")
	})
	if !errors.Is(err, ErrSessionCapacity) || capacityCreateCalled {
		t.Fatalf("capacity result error=%v create_called=%v", err, capacityCreateCalled)
	}
}

func TestStoreRemoveDoesNotDeleteReplacementForStaleSession(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()
	store, err := NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.CloseAll()

	newSession := func(port int) (*Session, error) {
		return NewSession(context.Background(), SessionOptions{
			ClientAddr:    &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port},
			BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
			PublicConn:    publicConn,
			MaxPacketSize: 4096,
		})
	}
	first, _, _, err := store.GetOrCreate("client", func() (*Session, error) { return newSession(47000) })
	if err != nil {
		t.Fatal(err)
	}
	removed, count, err := store.Remove("client", first)
	if err != nil || !removed || count != 0 {
		t.Fatalf("first Remove() = removed %v count %d error %v", removed, count, err)
	}
	second, _, _, err := store.GetOrCreate("client", func() (*Session, error) { return newSession(47001) })
	if err != nil {
		t.Fatal(err)
	}

	removed, count, err = store.Remove("client", first)
	if err != nil || removed || count != 1 {
		t.Fatalf("stale Remove() = removed %v count %d error %v", removed, count, err)
	}
	got, ok := store.Get("client")
	if !ok || got != second {
		t.Fatal("stale Remove() deleted the replacement session")
	}
}

func TestStoreExpireIdleRemovesOnlySessionsBeforeCutoff(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	publicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close()
	store, err := NewStore(2)
	if err != nil {
		t.Fatal(err)
	}
	defer store.CloseAll()

	createAt := func(port int, at time.Time) func() (*Session, error) {
		return func() (*Session, error) {
			return NewSession(context.Background(), SessionOptions{
				ClientAddr:    &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port},
				BackendAddr:   backend.LocalAddr().(*net.UDPAddr),
				PublicConn:    publicConn,
				MaxPacketSize: 4096,
				clock:         func() time.Time { return at },
			})
		}
	}
	old, _, _, err := store.GetOrCreate("old", createAt(48000, time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, _, err := store.GetOrCreate("fresh", createAt(48001, time.Unix(200, 0)))
	if err != nil {
		t.Fatal(err)
	}

	expired, count, err := store.ExpireIdle(time.Unix(150, 0))
	if err != nil || expired != 1 || count != 1 {
		t.Fatalf("ExpireIdle() = expired %d count %d error %v", expired, count, err)
	}
	select {
	case <-old.Done():
	default:
		t.Fatal("expired session was not closed")
	}
	if got, ok := store.Get("fresh"); !ok || got != fresh {
		t.Fatal("fresh session was removed")
	}
}
