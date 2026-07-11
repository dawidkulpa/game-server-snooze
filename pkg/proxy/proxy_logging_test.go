package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/server"
	"dkulpa.eu/game-server-snooze/pkg/session"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func TestSessionLifecycleEmitsPrivacySafeOperationalLogs(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()

	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	store, err := session.NewStore(2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	instance := &Proxy{
		ctx:          ctx,
		options:      Options{Listener: listener, BackendAddr: backend.LocalAddr().(*net.UDPAddr), MaxPacketSize: 4096},
		store:        store,
		queues:       make(map[*session.Session]*packetQueue),
		backendReady: true,
	}
	clientAddr := &net.UDPAddr{IP: net.ParseIP("198.51.100.42"), Port: 54321}
	instance.handleClientPacket(clientAddr, []byte("current-client-packet"))

	active, ok := store.Get(clientAddr.String())
	if !ok {
		t.Fatal("session was not created")
	}
	instance.mu.Lock()
	instance.backendReady = false
	instance.mu.Unlock()
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	instance.wg.Wait()

	secondClient := &net.UDPAddr{IP: net.ParseIP("203.0.113.9"), Port: 54322}
	instance.mu.Lock()
	instance.backendReady = true
	instance.mu.Unlock()
	instance.handleClientPacket(secondClient, []byte("current-client-packet"))
	instance.mu.Lock()
	instance.backendReady = false
	instance.mu.Unlock()
	instance.expireIdleSessions(time.Now().Add(time.Hour))
	instance.wg.Wait()

	entries := hook.AllEntries()
	assertLogEntry(t, entries, "UDP session opened", map[string]any{"active_sessions": 1})
	assertLogEntry(t, entries, "UDP session closed", map[string]any{"active_sessions": 0})
	assertLogEntry(t, entries, "Idle UDP sessions expired", map[string]any{"expired_sessions": 1, "active_sessions": 0})
	sensitiveValues := []string{
		clientAddr.IP.String(),
		fmt.Sprint(clientAddr.Port),
		clientAddr.String(),
		secondClient.IP.String(),
		fmt.Sprint(secondClient.Port),
		secondClient.String(),
		backend.LocalAddr().String(),
		"current-client-packet",
	}
	for _, entry := range entries {
		if len(entry.Message) > 200 {
			t.Fatalf("unbounded log message length %d", len(entry.Message))
		}
		for _, sensitive := range sensitiveValues {
			if strings.Contains(entry.Message, sensitive) {
				t.Fatalf("log message exposed sensitive runtime data: %q", entry.Message)
			}
			for _, value := range entry.Data {
				encoded := valueToString(value)
				if len(encoded) > 200 {
					t.Fatalf("unbounded log field in %q", entry.Message)
				}
				if strings.Contains(encoded, sensitive) {
					t.Fatalf("log field exposed sensitive runtime data in %q", entry.Message)
				}
			}
		}
	}
}

func TestInitialAndStartLifecycleEmitInfoLogs(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()

	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	initialController := newFakeController(server.StateRunning)
	instance := &Proxy{
		options: Options{
			Listener:      listener,
			Controller:    initialController,
			MaxPacketSize: 4096,
			SweepInterval: time.Hour,
		},
		store:  store,
		ready:  make(chan struct{}),
		queues: make(map[*session.Session]*packetQueue),
	}
	serveCtx, cancelServe := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(serveCtx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}
	cancelServe()
	if err := <-serveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve() = %v", err)
	}
	assertLogEntry(t, hook.AllEntries(), "Initial Pterodactyl state detected", map[string]any{"state": server.StateRunning})

	hook.Reset()
	startController := newFakeController(server.StateOffline)
	startStore, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	startCtx, cancelStart := context.WithCancel(context.Background())
	defer cancelStart()
	startProxy := &Proxy{
		ctx: startCtx,
		options: Options{
			Controller:          startController,
			StartupTimeout:      time.Second,
			StartupPollInterval: time.Millisecond,
			AutoStopDelay:       time.Hour,
		},
		store:  startStore,
		queues: make(map[*session.Session]*packetQueue),
	}
	startDone := make(chan error, 1)
	go func() { startDone <- startProxy.ensureRunning() }()
	select {
	case <-startController.startCh:
	case <-time.After(time.Second):
		t.Fatal("start request was not issued")
	}
	startController.setState(server.StateRunning)
	if err := <-startDone; err != nil {
		t.Fatalf("ensureRunning() = %v", err)
	}
	startProxy.mu.Lock()
	startProxy.stopAutoStopTimerLocked()
	startProxy.mu.Unlock()
	entries := hook.AllEntries()
	assertLogEntry(t, entries, "Requesting Palworld start", nil)
	assertLogEntry(t, entries, "Palworld start request accepted", nil)
	assertLogEntry(t, entries, "Palworld backend ready; UDP forwarding enabled", map[string]any{"active_sessions": 0})
}

func TestAutoStopLifecycleEmitsOperationalLogs(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()

	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	instance := &Proxy{
		ctx: context.Background(),
		options: Options{
			Controller:    controller,
			AutoStopDelay: time.Hour,
		},
		store:  store,
		queues: make(map[*session.Session]*packetQueue),
	}

	instance.openStartupGate()
	instance.cancelAutoStop()
	instance.scheduleAutoStop()
	instance.mu.Lock()
	generation := instance.stopGeneration
	instance.stopAutoStopTimerLocked()
	instance.mu.Unlock()
	instance.runAutoStopAttempt(generation, 1)

	entries := hook.AllEntries()
	assertLogEntry(t, entries, "Palworld backend ready; UDP forwarding enabled", map[string]any{"active_sessions": 0, "queued_sessions": 0, "queued_packets": 0})
	assertLogEntry(t, entries, "Palworld auto-stop scheduled", map[string]any{"active_sessions": 0, "delay": time.Hour.String()})
	assertLogEntry(t, entries, "Palworld auto-stop cancelled by new UDP session", nil)
	assertLogEntry(t, entries, "Requesting Palworld auto-stop", map[string]any{"attempt": 1})
	assertLogEntry(t, entries, "Palworld auto-stop request accepted", map[string]any{"attempt": 1})
}

func assertLogEntry(t *testing.T, entries []*logrus.Entry, message string, fields map[string]any) {
	t.Helper()
	for _, entry := range entries {
		if entry.Message != message {
			continue
		}
		if entry.Level != logrus.InfoLevel {
			t.Fatalf("%s level = %s, want info", message, entry.Level)
		}
		for key, want := range fields {
			if valueToString(entry.Data[key]) != valueToString(want) {
				t.Fatalf("%s field %s = %v, want %v", message, key, entry.Data[key], want)
			}
		}
		return
	}
	t.Fatalf("missing log entry %q", message)
}

func valueToString(value any) string {
	if value == nil {
		return "<nil>"
	}
	return fmt.Sprint(value)
}
