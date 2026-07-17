package proxy

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/config"
	"dkulpa.eu/game-server-snooze/pkg/readiness"
	"dkulpa.eu/game-server-snooze/pkg/server"
	"dkulpa.eu/game-server-snooze/pkg/session"
	"github.com/sirupsen/logrus"
)

type testWakeDetector struct{}

func (testWakeDetector) DetectStart(data []byte) bool { return string(data) == "wake" }

type gatedReadiness struct {
	entered chan struct{}
	release chan struct{}
	result  error
}

func (probe *gatedReadiness) WaitReady(ctx context.Context) error {
	select {
	case probe.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-probe.release:
		return probe.result
	}
}

func (probe *gatedReadiness) Probe(context.Context) error {
	return probe.result
}

type healthReadiness struct {
	mu           sync.Mutex
	results      []error
	probeCalls   int
	probeCalled  chan struct{}
	probeRelease chan struct{}
	waitEntered  chan struct{}
	waitRelease  chan struct{}
}

func (probe *healthReadiness) WaitReady(ctx context.Context) error {
	if probe.waitRelease == nil {
		return nil
	}
	select {
	case probe.waitEntered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-probe.waitRelease:
		return nil
	}
}

func (probe *healthReadiness) Probe(context.Context) error {
	probe.mu.Lock()
	probe.probeCalls++
	var result error
	if len(probe.results) > 0 {
		result = probe.results[0]
		probe.results = probe.results[1:]
	}
	probe.mu.Unlock()
	select {
	case probe.probeCalled <- struct{}{}:
	default:
	}
	if probe.probeRelease != nil {
		<-probe.probeRelease
	}
	return result
}

type blockingHealthReadiness struct {
	entered chan struct{}
	release chan struct{}
	next    chan struct{}
}

type sequencedStatusResult struct {
	state server.State
	err   error
}

type sequencedStatusController struct {
	mu      sync.Mutex
	results []sequencedStatusResult
	called  chan struct{}
	release chan struct{}
}

func (controller *sequencedStatusController) Status(ctx context.Context) (server.State, error) {
	controller.mu.Lock()
	if len(controller.results) == 0 {
		controller.mu.Unlock()
		return server.StateRunning, nil
	}
	result := controller.results[0]
	controller.results = controller.results[1:]
	controller.mu.Unlock()

	select {
	case controller.called <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case <-controller.release:
		return result.state, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (*sequencedStatusController) Start(context.Context) error { return nil }
func (*sequencedStatusController) Stop(context.Context) error  { return nil }

func (probe *blockingHealthReadiness) WaitReady(context.Context) error { return nil }

func (probe *blockingHealthReadiness) Probe(ctx context.Context) error {
	select {
	case probe.entered <- struct{}{}:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-probe.release:
			return errors.New("stale timeout")
		}
	default:
		select {
		case probe.next <- struct{}{}:
		default:
		}
		return nil
	}
}

type fakeController struct {
	mu            sync.Mutex
	state         server.State
	startCalls    int
	stopCalls     int
	statusCalls   int
	startCh       chan struct{}
	stopEntered   chan struct{}
	releaseStop   chan struct{}
	stopResults   []error
	startResults  []error
	statusResults []error
}

func newFakeController(state server.State) *fakeController {
	return &fakeController{state: state, startCh: make(chan struct{}, 1)}
}

func (controller *fakeController) Status(context.Context) (server.State, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.statusCalls++
	if len(controller.statusResults) > 0 {
		result := controller.statusResults[0]
		controller.statusResults = controller.statusResults[1:]
		return "", result
	}
	return controller.state, nil
}

func (controller *fakeController) Start(context.Context) error {
	controller.mu.Lock()
	controller.startCalls++
	var result error
	if len(controller.startResults) > 0 {
		result = controller.startResults[0]
		controller.startResults = controller.startResults[1:]
	}
	if result == nil {
		controller.state = server.StateStarting
	}
	controller.mu.Unlock()
	select {
	case controller.startCh <- struct{}{}:
	default:
	}
	return result
}

func (controller *fakeController) Stop(context.Context) error {
	controller.mu.Lock()
	controller.stopCalls++
	entered := controller.stopEntered
	release := controller.releaseStop
	var result error
	if len(controller.stopResults) > 0 {
		result = controller.stopResults[0]
		controller.stopResults = controller.stopResults[1:]
	}
	controller.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	if result != nil {
		return result
	}
	controller.mu.Lock()
	controller.state = server.StateStopping
	controller.mu.Unlock()
	return nil
}

func (controller *fakeController) setState(state server.State) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.state = state
}

type testPermanentError struct{ message string }

func TestFinishStartupRetriggersReadinessForQueuedRecoveryPacket(t *testing.T) {
	instance := &Proxy{
		startupRunning:    true,
		startupGeneration: 7,
		queues: map[*session.Session]*packetQueue{
			nil: {packets: [][]byte{{0x01}}, bytes: 1},
		},
		queuedBytes: 1,
	}
	if !instance.completeStartup(7) {
		t.Fatal("queued recovery did not request a new readiness generation")
	}
	instance.mu.Lock()
	running := instance.startupRunning
	generation := instance.startupGeneration
	instance.mu.Unlock()
	if running || generation != 7 {
		t.Fatalf("startup completion state is invalid: running=%v generation=%d", running, generation)
	}
}

func TestBackendHealthFailuresResetAcrossReadinessGenerations(t *testing.T) {
	failures := backendHealthFailures{}
	if count := failures.record(7); count != 1 {
		t.Fatalf("first generation failure count = %d, want 1", count)
	}
	if count := failures.record(7); count != 2 {
		t.Fatalf("second same-generation failure count = %d, want 2", count)
	}
	if count := failures.record(9); count != 1 {
		t.Fatalf("first new-generation failure count = %d, want 1", count)
	}
}

func TestBackendHealthFailureClassDoesNotExposeNetworkAddresses(t *testing.T) {
	privateEndpoint := "192.168.50.52:27015"
	err := &net.OpError{
		Op:   "read",
		Net:  "udp",
		Addr: &net.UDPAddr{IP: net.ParseIP("192.168.50.52"), Port: 27015},
		Err:  errors.New("private endpoint " + privateEndpoint),
	}
	classification := backendHealthFailureClass(err)
	if strings.Contains(classification, privateEndpoint) || classification != "probe_failure" {
		t.Fatalf("unsafe or unexpected health failure classification: %q", classification)
	}
}

func TestBackendHealthMonitorClosesReadyGateAfterConsecutiveFailures(t *testing.T) {
	logger := logrus.StandardLogger()
	oldOutput, oldLevel, oldFormatter := logger.Out, logger.Level, logger.Formatter
	var output bytes.Buffer
	logger.SetOutput(&output)
	logger.SetLevel(logrus.WarnLevel)
	logger.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	t.Cleanup(func() {
		logger.SetOutput(oldOutput)
		logger.SetLevel(oldLevel)
		logger.SetFormatter(oldFormatter)
	})
	privateEndpoint := "192.168.50.52:27015"
	probe := &healthReadiness{
		results: []error{
			errors.New("first timeout from " + privateEndpoint),
			errors.New("second timeout from " + privateEndpoint),
			errors.New("third timeout from " + privateEndpoint),
		},
		probeCalled:  make(chan struct{}, 4),
		probeRelease: make(chan struct{}, 3),
	}
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	instance := &Proxy{
		ctx:            ctx,
		store:          store,
		backendReady:   true,
		backendRunning: true,
		options: Options{
			Readiness:           probe,
			StartupPollInterval: time.Millisecond,
		},
	}
	done := make(chan struct{})
	instance.wg.Add(1)
	go func() {
		instance.runBackendHealthMonitor()
		close(done)
	}()
	for range 3 {
		select {
		case <-probe.probeCalled:
		case <-time.After(time.Second):
			t.Fatal("backend health probe was not called")
		}
		probe.probeRelease <- struct{}{}
	}
	deadline := time.Now().Add(time.Second)
	for {
		instance.mu.Lock()
		ready := instance.backendReady
		instance.mu.Unlock()
		if !ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend readiness gate remained open after consecutive health failures")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("backend health monitor did not stop after cancellation")
	}
	logged := output.String()
	if strings.Contains(logged, privateEndpoint) {
		t.Fatalf("backend health logging exposed private endpoint: %q", logged)
	}
	if !strings.Contains(logged, "failure_class=probe_failure") || !strings.Contains(logged, "failure_count=1") {
		t.Fatalf("backend health logging lacks bounded failure metadata: %q", logged)
	}
}

func TestBackendHealthLossRequiresWakeSignatureBeforeRecovery(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	public, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	store, err := session.NewStore(2)
	if err != nil {
		t.Fatal(err)
	}
	probe := &healthReadiness{
		results: []error{
			errors.New("first timeout"),
			errors.New("second timeout"),
			errors.New("third timeout"),
		},
		probeCalled: make(chan struct{}, 4),
		waitEntered: make(chan struct{}, 1),
		waitRelease: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	instance := &Proxy{
		ctx:                 ctx,
		store:               store,
		backendReady:        true,
		backendRunning:      true,
		readinessGeneration: 1,
		queues:              make(map[*session.Session]*packetQueue),
		options: Options{
			Listener:                     public,
			BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
			Controller:                   newFakeController(server.StateRunning),
			Detector:                     testWakeDetector{},
			Readiness:                    probe,
			MaxPacketSize:                1024,
			StartupTimeout:               time.Second,
			StartupPollInterval:          5 * time.Millisecond,
			StartupBufferPackets:         4,
			StartupBufferBytesPerSession: 4096,
			StartupBufferBytesGlobal:     8192,
		},
	}
	clientAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 23456}
	instance.handleClientPacket(clientAddr, []byte("before-loss"))
	buffer := make([]byte, 128)
	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := backend.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "before-loss" {
		t.Fatalf("first backend packet = %q", got)
	}

	instance.wg.Add(1)
	go instance.runBackendHealthMonitor()
	for range backendHealthFailureThreshold {
		select {
		case <-probe.probeCalled:
		case <-time.After(time.Second):
			t.Fatal("backend health probe was not called")
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		instance.mu.Lock()
		ready := instance.backendReady
		running := instance.backendRunning
		instance.mu.Unlock()
		if !ready {
			if running {
				t.Fatal("confirmed backend loss left stale running state")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend gate did not close")
		}
		time.Sleep(time.Millisecond)
	}

	instance.handleClientPacket(clientAddr, []byte("after-loss"))
	select {
	case <-probe.waitEntered:
		t.Fatal("non-wake packet restarted recovery after confirmed backend loss")
	case <-time.After(25 * time.Millisecond):
	}
	if store.Len() != 0 {
		t.Fatal("non-wake packet created a session after confirmed backend loss")
	}

	instance.handleClientPacket(clientAddr, []byte("wake"))
	select {
	case <-probe.waitEntered:
	case <-time.After(time.Second):
		t.Fatal("recovery readiness did not start")
	}
	_ = backend.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, _, err := backend.ReadFromUDP(buffer); err == nil {
		t.Fatal("packet reached backend while readiness gate was closed")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("read closed backend: %v", err)
	}
	close(probe.waitRelease)
	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err = backend.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "wake" {
		t.Fatalf("flushed backend packet = %q", got)
	}

	cancel()
	_ = store.CloseAll()
	instance.wg.Wait()
}

func TestFailedPostSettleProbeRequiresWakeSignatureForRetry(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	public, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	probe := &healthReadiness{
		results:     []error{errors.New("backend left running during settle")},
		probeCalled: make(chan struct{}, 1),
	}
	controller := newFakeController(server.StateRunning)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	instance := &Proxy{
		ctx:               ctx,
		store:             store,
		queues:            make(map[*session.Session]*packetQueue),
		startupGeneration: 1,
		options: Options{
			Listener:                     public,
			BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
			Controller:                   controller,
			Detector:                     testWakeDetector{},
			Readiness:                    probe,
			MaxPacketSize:                1024,
			StartupTimeout:               time.Second,
			StartupPollInterval:          time.Millisecond,
			StartupSettleDelay:           time.Millisecond,
			StartupBufferPackets:         4,
			StartupBufferBytesPerSession: 4096,
			StartupBufferBytesGlobal:     8192,
		},
	}
	startupErr := instance.ensureRunning()
	if startupErr == nil {
		t.Fatal("ensureRunning() opened the gate after readiness was lost during settle")
	}
	instance.forwardingMu.RLock()
	failureDone := make(chan struct{})
	go func() {
		instance.failStartup(1, false)
		close(failureDone)
	}()
	select {
	case <-failureDone:
		instance.forwardingMu.RUnlock()
		t.Fatal("startup failure cleanup bypassed active packet forwarding")
	case <-time.After(25 * time.Millisecond):
	}
	instance.forwardingMu.RUnlock()
	select {
	case <-failureDone:
	case <-time.After(time.Second):
		t.Fatal("startup failure cleanup did not finish after forwarding drained")
	}
	instance.mu.Lock()
	ready := instance.backendReady
	running := instance.backendRunning
	instance.mu.Unlock()
	if ready {
		t.Fatal("backend gate opened after final readiness probe failed")
	}
	if running {
		t.Fatal("failed final readiness probe left stale running state")
	}

	controller.mu.Lock()
	controller.state = server.StateOffline
	controller.mu.Unlock()
	clientAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 23457}
	instance.handleClientPacket(clientAddr, []byte("unsigned"))
	if store.Len() != 0 {
		t.Fatal("unsigned packet created a session after failed final readiness probe")
	}
	controller.mu.Lock()
	startCalls := controller.startCalls
	controller.mu.Unlock()
	if startCalls != 0 {
		t.Fatal("unsigned packet restarted backend after failed final readiness probe")
	}

	instance.handleClientPacket(clientAddr, []byte("wake"))
	select {
	case <-controller.startCh:
	case <-time.After(time.Second):
		t.Fatal("signed wake packet did not request backend restart")
	}
	cancel()
	_ = store.CloseAll()
	instance.wg.Wait()
}

func TestBackendHealthMonitorKeepsGateOpenAfterTransientFailure(t *testing.T) {
	probe := &healthReadiness{
		results:     []error{errors.New("transient timeout"), nil},
		probeCalled: make(chan struct{}, 4),
	}
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	instance := &Proxy{
		ctx:            ctx,
		store:          store,
		backendReady:   true,
		backendRunning: true,
		options: Options{
			Readiness:           probe,
			StartupPollInterval: time.Millisecond,
		},
	}
	instance.wg.Add(1)
	go instance.runBackendHealthMonitor()
	for range 2 {
		select {
		case <-probe.probeCalled:
		case <-time.After(time.Second):
			t.Fatal("backend health probe was not called")
		}
	}
	instance.mu.Lock()
	ready := instance.backendReady
	instance.mu.Unlock()
	if !ready {
		t.Fatal("backend readiness gate closed after a single transient failure")
	}
	cancel()
	instance.wg.Wait()
}

func TestBackendHealthMonitorKeepsEstablishedSessionsOpenAfterInconclusivePterodactylFailures(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	public, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	store, err := session.NewStore(2)
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	controller.statusResults = []error{
		context.DeadlineExceeded,
		context.DeadlineExceeded,
		context.DeadlineExceeded,
	}
	probe, err := readiness.NewPterodactylProbe(controller, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	instance := &Proxy{
		ctx:                 ctx,
		store:               store,
		backendReady:        true,
		backendRunning:      true,
		readinessGeneration: 1,
		queues:              make(map[*session.Session]*packetQueue),
		options: Options{
			Listener:            public,
			BackendAddr:         backend.LocalAddr().(*net.UDPAddr),
			Controller:          controller,
			Detector:            testWakeDetector{},
			Readiness:           probe,
			MaxPacketSize:       1024,
			StartupPollInterval: time.Millisecond,
		},
	}
	clientAddr := &net.UDPAddr{IP: net.ParseIP("198.51.100.42"), Port: 23456}
	instance.handleClientPacket(clientAddr, []byte("active-gameplay"))
	active, ok := store.Get(clientAddr.String())
	if !ok {
		t.Fatal("active session was not created")
	}

	instance.wg.Add(1)
	go instance.runBackendHealthMonitor()
	deadline := time.Now().Add(time.Second)
	for {
		controller.mu.Lock()
		calls := controller.statusCalls
		controller.mu.Unlock()
		if calls >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend health monitor did not probe after the inconclusive failures")
		}
		time.Sleep(time.Millisecond)
	}

	instance.mu.Lock()
	ready := instance.backendReady
	running := instance.backendRunning
	instance.mu.Unlock()
	if !ready || !running {
		t.Fatalf("inconclusive Pterodactyl failures changed established state: ready=%t running=%t", ready, running)
	}
	if got, ok := store.Get(clientAddr.String()); !ok || got != active {
		t.Fatal("inconclusive Pterodactyl failures replaced or closed the established session")
	}
	select {
	case <-active.Done():
		t.Fatal("inconclusive Pterodactyl failures closed the established session")
	default:
	}

	cancel()
	_ = store.CloseAll()
	instance.wg.Wait()
}

func TestBackendHealthMonitorRequiresConsecutiveConfirmedPterodactylStates(t *testing.T) {
	tests := []struct {
		name      string
		separator sequencedStatusResult
	}{
		{name: "running resets confirmation", separator: sequencedStatusResult{state: server.StateRunning}},
		{name: "inconclusive resets confirmation", separator: sequencedStatusResult{err: context.DeadlineExceeded}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			public, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
			if err != nil {
				t.Fatal(err)
			}
			defer public.Close()
			store, err := session.NewStore(2)
			if err != nil {
				t.Fatal(err)
			}
			controller := &sequencedStatusController{
				results: []sequencedStatusResult{
					{state: server.StateOffline},
					{state: server.StateStarting},
					test.separator,
					{state: server.StateStopping},
					{state: server.StateOffline},
					{state: server.StateStarting},
				},
				called:  make(chan struct{}),
				release: make(chan struct{}),
			}
			probe, err := readiness.NewPterodactylProbe(controller, 100*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			instance := &Proxy{
				ctx:                 ctx,
				store:               store,
				backendReady:        true,
				backendRunning:      true,
				readinessGeneration: 1,
				queues:              make(map[*session.Session]*packetQueue),
				options: Options{
					Listener:            public,
					BackendAddr:         backend.LocalAddr().(*net.UDPAddr),
					Controller:          controller,
					Detector:            testWakeDetector{},
					Readiness:           probe,
					MaxPacketSize:       1024,
					StartupPollInterval: 100 * time.Millisecond,
				},
			}
			t.Cleanup(func() {
				cancel()
				_ = store.CloseAll()
				instance.wg.Wait()
			})
			clientAddr := &net.UDPAddr{IP: net.ParseIP("198.51.100.42"), Port: 23456}
			instance.handleClientPacket(clientAddr, []byte("active-gameplay"))
			active, ok := store.Get(clientAddr.String())
			if !ok {
				t.Fatal("active session was not created")
			}

			instance.wg.Add(1)
			go instance.runBackendHealthMonitor()
			for call := 0; call < 6; call++ {
				select {
				case <-controller.called:
				case <-time.After(time.Second):
					t.Fatalf("status call %d did not start", call+1)
				}
				if call > 0 {
					instance.mu.Lock()
					ready := instance.backendReady
					instance.mu.Unlock()
					if !ready {
						t.Fatalf("gate closed before the final consecutive confirmation at call %d", call+1)
					}
					if got, ok := store.Get(clientAddr.String()); !ok || got != active {
						t.Fatalf("session closed before the final consecutive confirmation at call %d", call+1)
					}
				}
				select {
				case controller.release <- struct{}{}:
				case <-time.After(time.Second):
					t.Fatalf("status call %d did not accept its release", call+1)
				}
			}

			deadline := time.Now().Add(time.Second)
			for {
				instance.mu.Lock()
				ready := instance.backendReady
				instance.mu.Unlock()
				if !ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("gate remained open after three subsequent consecutive confirmed non-running states")
				}
				time.Sleep(time.Millisecond)
			}
			select {
			case <-active.Done():
			case <-time.After(time.Second):
				t.Fatal("confirmed backend loss did not close the established session")
			}
		})
	}
}

func TestBackendHealthMonitorIgnoresFailedProbeFromOlderReadinessGeneration(t *testing.T) {
	probe := &blockingHealthReadiness{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		next:    make(chan struct{}, 1),
	}
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	instance := &Proxy{
		ctx:                 ctx,
		store:               store,
		backendReady:        true,
		readinessGeneration: 1,
		options: Options{
			Readiness:           probe,
			StartupPollInterval: 10 * time.Millisecond,
		},
	}
	instance.wg.Add(1)
	go instance.runBackendHealthMonitor()
	select {
	case <-probe.entered:
	case <-time.After(time.Second):
		t.Fatal("backend health probe did not start")
	}
	instance.mu.Lock()
	instance.backendReady = false
	instance.readinessGeneration++
	instance.backendReady = true
	instance.readinessGeneration++
	instance.mu.Unlock()
	close(probe.release)
	select {
	case <-probe.next:
	case <-time.After(time.Second):
		t.Fatal("backend health monitor did not continue after stale probe")
	}
	instance.mu.Lock()
	ready := instance.backendReady
	instance.mu.Unlock()
	if !ready {
		t.Fatal("stale failed probe closed a newer readiness generation")
	}
	cancel()
	instance.wg.Wait()
}

func (err testPermanentError) Error() string   { return err.message }
func (err testPermanentError) Permanent() bool { return true }

func TestEnsureRunningDoesNotStartAfterTransientStatusFailure(t *testing.T) {
	controller := newFakeController(server.StateOffline)
	controller.statusResults = []error{errors.New("temporary status failure")}
	instance := &Proxy{options: Options{
		Controller:          controller,
		StartupTimeout:      time.Second,
		StartupPollInterval: 100 * time.Millisecond,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	instance.ctx = ctx
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- instance.ensureRunning() }()
	time.Sleep(20 * time.Millisecond)
	controller.mu.Lock()
	callsBeforeRetry := controller.startCalls
	controller.mu.Unlock()
	if callsBeforeRetry != 0 {
		t.Fatalf("Start() called %d times after failed status query", callsBeforeRetry)
	}
	select {
	case <-controller.startCh:
	case <-time.After(time.Second):
		t.Fatal("Start() was not called after a later offline status")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ensureRunning() did not honor cancellation")
	}
}

func TestEnsureRunningFailsClosedOnPermanentStatusFailure(t *testing.T) {
	controller := newFakeController(server.StateOffline)
	controller.statusResults = []error{testPermanentError{message: "forbidden"}}
	instance := &Proxy{
		ctx: context.Background(),
		options: Options{
			Controller:          controller,
			StartupTimeout:      time.Second,
			StartupPollInterval: time.Millisecond,
		},
	}
	if err := instance.ensureRunning(); err == nil {
		t.Fatal("ensureRunning() accepted a permanent status failure")
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.startCalls != 0 {
		t.Fatalf("Start() called %d times after a permanent status failure", controller.startCalls)
	}
}

func TestEnsureRunningPollsAfterAmbiguousRetryableStartFailure(t *testing.T) {
	controller := newFakeController(server.StateOffline)
	controller.startResults = []error{errors.New("temporary start failure")}
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	instance := &Proxy{
		ctx: context.Background(),
		options: Options{
			Controller:          controller,
			StartupTimeout:      time.Second,
			StartupPollInterval: 5 * time.Millisecond,
		},
		store:  store,
		queues: make(map[*session.Session]*packetQueue),
	}
	done := make(chan error, 1)
	go func() { done <- instance.ensureRunning() }()
	select {
	case <-controller.startCh:
	case <-time.After(time.Second):
		t.Fatal("Start() was not called")
	}
	controller.setState(server.StateRunning)
	if err := <-done; err != nil {
		t.Fatalf("ensureRunning() after ambiguous start = %v", err)
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.startCalls != 1 {
		t.Fatalf("Start() calls = %d, want exactly 1", controller.startCalls)
	}
}

func TestEnsureRunningFailsImmediatelyOnPermanentStartFailure(t *testing.T) {
	controller := newFakeController(server.StateOffline)
	controller.startResults = []error{testPermanentError{message: "forbidden"}}
	instance := &Proxy{
		ctx: context.Background(),
		options: Options{
			Controller:          controller,
			StartupTimeout:      time.Second,
			StartupPollInterval: time.Millisecond,
		},
	}
	if err := instance.ensureRunning(); err == nil || !server.IsPermanent(err) {
		t.Fatalf("permanent start failure was not returned: %v", err)
	}
}

func TestPermanentStartupFailureAppliesGlobalCooldown(t *testing.T) {
	controller := newFakeController(server.StateOffline)
	controller.statusResults = []error{testPermanentError{message: "forbidden"}}
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	instance := &Proxy{
		ctx: context.Background(),
		options: Options{
			Controller:             controller,
			StartupTimeout:         time.Second,
			StartupPollInterval:    time.Millisecond,
			StartupFailureCooldown: 20 * time.Millisecond,
		},
		store:  store,
		queues: make(map[*session.Session]*packetQueue),
	}
	instance.startServer()
	instance.wg.Wait()
	for range 100 {
		instance.startServer()
	}
	instance.wg.Wait()
	controller.mu.Lock()
	callsDuringCooldown := controller.statusCalls
	controller.mu.Unlock()
	if callsDuringCooldown != 1 {
		t.Fatalf("status calls during cooldown = %d, want 1", callsDuringCooldown)
	}
	time.Sleep(25 * time.Millisecond)
	controller.mu.Lock()
	controller.statusResults = []error{testPermanentError{message: "forbidden"}}
	controller.mu.Unlock()
	instance.startServer()
	instance.wg.Wait()
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.statusCalls != 2 {
		t.Fatalf("status calls after cooldown = %d, want 2", controller.statusCalls)
	}
}

func TestNewDerivesPositiveSweepIntervalWhenOmitted(t *testing.T) {
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
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   newFakeController(server.StateOffline),
		Detector:                     testWakeDetector{},
		MaxSessions:                  1,
		MaxPacketSize:                4096,
		IdleTimeout:                  time.Minute,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          time.Millisecond,
		StartupBufferPackets:         1,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.options.SweepInterval <= 0 || instance.options.SweepInterval > 5*time.Second {
		t.Fatalf("derived sweep interval = %v", instance.options.SweepInterval)
	}
}

func TestCapacityWarningIsRateLimited(t *testing.T) {
	instance := &Proxy{}
	now := time.Unix(100, 0)
	if !instance.shouldLogCapacityWarning(now) {
		t.Fatal("first capacity warning was suppressed")
	}
	if instance.shouldLogCapacityWarning(now.Add(500 * time.Millisecond)) {
		t.Fatal("capacity warning was not rate limited")
	}
	if !instance.shouldLogCapacityWarning(now.Add(time.Second)) {
		t.Fatal("capacity warning did not resume after interval")
	}
}

func TestStartupQueueEnforcesPacketCountBound(t *testing.T) {
	instance := &Proxy{
		options: Options{
			StartupBufferPackets:         2,
			StartupBufferBytesPerSession: 1024,
			StartupBufferBytesGlobal:     4096,
		},
		queues: make(map[*session.Session]*packetQueue),
	}
	active := &session.Session{}
	instance.mu.Lock()
	instance.enqueueLocked(active, []byte("a"))
	instance.enqueueLocked(active, []byte("b"))
	instance.enqueueLocked(active, []byte("c"))
	instance.mu.Unlock()

	queue := instance.queues[active]
	if len(queue.packets) != 2 || queue.bytes != 2 || instance.queuedBytes != 2 {
		t.Fatalf("queue packets=%d bytes=%d global=%d, want 2/2/2", len(queue.packets), queue.bytes, instance.queuedBytes)
	}
}

func TestProxyDropsTruncatedClientDatagramWithoutWake(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateOffline)
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   controller,
		Detector:                     testWakeDetector{},
		MaxSessions:                  1,
		MaxPacketSize:                4,
		IdleTimeout:                  time.Minute,
		SweepInterval:                time.Minute,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          time.Millisecond,
		StartupBufferPackets:         2,
		StartupBufferBytesPerSession: 1024,
		StartupBufferBytesGlobal:     1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}
	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("wake-extra")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controller.startCh:
		t.Fatal("truncated wake prefix started the server")
	case <-time.After(50 * time.Millisecond):
	}
	if instance.store.Len() != 0 {
		t.Fatalf("truncated datagram created %d sessions", instance.store.Len())
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestProxyBuffersPacketsUntilSingleStartupReachesRunning(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateOffline)
	readiness := &gatedReadiness{entered: make(chan struct{}, 1), release: make(chan struct{})}
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   controller,
		Detector:                     testWakeDetector{},
		Readiness:                    readiness,
		MaxSessions:                  16,
		MaxPacketSize:                4096,
		IdleTimeout:                  20 * time.Millisecond,
		SweepInterval:                5 * time.Millisecond,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          5 * time.Millisecond,
		StartupSettleDelay:           0,
		StartupBufferPackets:         8,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     65536,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}

	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controller.startCh:
	case <-time.After(time.Second):
		t.Fatal("wake packet did not start the server")
	}
	if _, err := client.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}

	buffer := make([]byte, 64)
	_ = backend.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, _, err := backend.ReadFromUDP(buffer); err == nil {
		t.Fatal("backend received a packet before Pterodactyl reached running")
	}
	controller.setState(server.StateRunning)
	select {
	case <-readiness.entered:
	case <-time.After(time.Second):
		t.Fatal("backend readiness probe was not started after Pterodactyl reached running")
	}
	_ = backend.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, _, err := backend.ReadFromUDP(buffer); err == nil {
		t.Fatal("backend received buffered traffic before application readiness")
	}
	close(readiness.release)
	if _, err := client.Write([]byte("third")); err != nil {
		t.Fatal(err)
	}

	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	var got []string
	for range 3 {
		n, _, err := backend.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(buffer[:n]))
	}
	if got[0] != "wake" || got[1] != "second" || got[2] != "third" {
		t.Fatalf("flushed packets = %v", got)
	}
	controller.mu.Lock()
	startCalls := controller.startCalls
	controller.mu.Unlock()
	if startCalls != 1 {
		t.Fatalf("Start() calls = %d, want 1", startCalls)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve() shutdown error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestProxyRestartAdoptsTrafficWhenBackendIsAlreadyRunning(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   controller,
		Detector:                     testWakeDetector{},
		MaxSessions:                  16,
		MaxPacketSize:                4096,
		IdleTimeout:                  time.Minute,
		SweepInterval:                time.Minute,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          5 * time.Millisecond,
		StartupSettleDelay:           0,
		StartupBufferPackets:         8,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     65536,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}

	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("continuation-without-wake-signature")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := backend.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "continuation-without-wake-signature" {
		t.Fatalf("backend received %q", buffer[:n])
	}
	controller.mu.Lock()
	startCalls := controller.startCalls
	controller.mu.Unlock()
	if startCalls != 0 {
		t.Fatalf("Start() calls = %d, want 0", startCalls)
	}

	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestEmptyDatagramDoesNotCreateSessionWhileBackendRuns(t *testing.T) {
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	instance := &Proxy{
		options:        Options{Detector: testWakeDetector{}},
		store:          store,
		backendRunning: true,
	}
	instance.handleClientPacket(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}, nil)
	if store.Len() != 0 {
		t.Fatalf("empty datagram created %d session(s)", store.Len())
	}
}

func TestPendingStartupSessionSurvivesNormalIdleTimeout(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateOffline)
	readiness := &gatedReadiness{entered: make(chan struct{}, 1), release: make(chan struct{})}
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   controller,
		Detector:                     testWakeDetector{},
		Readiness:                    readiness,
		MaxSessions:                  2,
		MaxPacketSize:                4096,
		IdleTimeout:                  15 * time.Millisecond,
		SweepInterval:                2 * time.Millisecond,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          time.Millisecond,
		StartupBufferPackets:         4,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}
	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controller.startCh:
	case <-time.After(time.Second):
		t.Fatal("Start() was not called")
	}
	controller.setState(server.StateRunning)
	select {
	case <-readiness.entered:
	case <-time.After(time.Second):
		t.Fatal("readiness probe did not start")
	}
	time.Sleep(60 * time.Millisecond)
	if instance.store.Len() != 1 {
		t.Fatalf("pending session expired during startup: count=%d", instance.store.Len())
	}
	close(readiness.release)
	buffer := make([]byte, 32)
	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := backend.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "wake" {
		t.Fatalf("backend received %q", buffer[:n])
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestClosedSessionIsRemovedAndSameEndpointCanReconnect(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   controller,
		Detector:                     testWakeDetector{},
		MaxSessions:                  2,
		MaxPacketSize:                4096,
		IdleTimeout:                  time.Minute,
		SweepInterval:                time.Minute,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          time.Millisecond,
		StartupBufferPackets:         4,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}
	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := client.LocalAddr().String()
	if _, err := client.Write([]byte("continuation")); err != nil {
		t.Fatal(err)
	}
	var first *session.Session
	deadline := time.Now().Add(time.Second)
	for first == nil {
		first, _ = instance.store.Get(key)
		if time.Now().After(deadline) {
			t.Fatal("initial session was not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	for instance.store.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("closed session remained in the store")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := client.Write([]byte("continuation")); err != nil {
		t.Fatal(err)
	}
	var replacement *session.Session
	deadline = time.Now().Add(time.Second)
	for replacement == nil {
		replacement, _ = instance.store.Get(key)
		if time.Now().After(deadline) {
			t.Fatal("replacement session was not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	if replacement == first {
		t.Fatal("closed session was reused instead of replaced")
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestClosedSessionStillInStoreIsReplacedWithoutDroppingPacket(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	public, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	clientAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 23456}
	store, err := session.NewStore(2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stale, _, _, err := store.GetOrCreate(clientAddr.String(), func() (*session.Session, error) {
		return session.NewSession(ctx, session.SessionOptions{ClientAddr: clientAddr, BackendAddr: backend.LocalAddr().(*net.UDPAddr), PublicConn: public, MaxPacketSize: 1024})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	instance := &Proxy{
		ctx:     ctx,
		options: Options{BackendAddr: backend.LocalAddr().(*net.UDPAddr), Listener: public, Detector: testWakeDetector{}, MaxPacketSize: 1024},
		store:   store, queues: make(map[*session.Session]*packetQueue), backendRunning: true, backendReady: true,
	}
	instance.handleClientPacket(clientAddr, []byte("replacement-packet"))
	replacement, ok := store.Get(clientAddr.String())
	if !ok || replacement == stale {
		t.Fatal("stale session was not replaced")
	}
	buffer := make([]byte, 64)
	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := backend.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "replacement-packet" {
		t.Fatalf("backend got %q", got)
	}
	cancel()
	_ = store.CloseAll()
	instance.wg.Wait()
}

func TestReadinessFailureClosesPendingSessionsAndReleasesBuffers(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	readiness := &gatedReadiness{entered: make(chan struct{}, 1), release: make(chan struct{}), result: errors.New("not ready")}
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   newFakeController(server.StateRunning),
		Detector:                     testWakeDetector{},
		Readiness:                    readiness,
		MaxSessions:                  4,
		MaxPacketSize:                4096,
		IdleTimeout:                  time.Minute,
		SweepInterval:                time.Minute,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          time.Millisecond,
		StartupBufferPackets:         4,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}
	select {
	case <-readiness.entered:
	case <-time.After(time.Second):
		t.Fatal("readiness probe did not start")
	}
	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("continuation")); err != nil {
		t.Fatal(err)
	}
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	admissionDeadline := time.NewTimer(time.Second)
	for instance.store.Len() != 1 {
		select {
		case <-poll.C:
		case <-admissionDeadline.C:
			t.Fatal("pending session was not admitted")
		}
	}
	if !admissionDeadline.Stop() {
		select {
		case <-admissionDeadline.C:
		default:
		}
	}
	close(readiness.release)
	cleanupDeadline := time.NewTimer(time.Second)
	for instance.store.Len() != 0 {
		select {
		case <-poll.C:
		case <-cleanupDeadline.C:
			t.Fatal("pending session survived readiness failure")
		}
	}
	if !cleanupDeadline.Stop() {
		select {
		case <-cleanupDeadline.C:
		default:
		}
	}
	instance.mu.Lock()
	queuedSessions := len(instance.queues)
	queuedBytes := instance.queuedBytes
	instance.mu.Unlock()
	if queuedSessions != 0 || queuedBytes != 0 {
		t.Fatalf("startup buffers survived failure: sessions=%d bytes=%d", queuedSessions, queuedBytes)
	}
	if _, err := client.Write([]byte("continuation")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readiness.entered:
		t.Fatal("unsigned packet restarted readiness after startup failure")
	case <-time.After(25 * time.Millisecond):
	}
	if instance.store.Len() != 0 {
		t.Fatal("unsigned packet created a session after startup failure")
	}
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readiness.entered:
	case <-time.After(time.Second):
		t.Fatal("signed wake packet after startup failure did not start a fresh readiness attempt")
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestAutoStopSerializesAgainstNewSessionAdmission(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	controller.stopEntered = make(chan struct{}, 1)
	controller.releaseStop = make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(controller.releaseStop) }) }
	t.Cleanup(release)

	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   controller,
		Detector:                     testWakeDetector{},
		MaxSessions:                  16,
		MaxPacketSize:                4096,
		IdleTimeout:                  time.Minute,
		SweepInterval:                time.Minute,
		AutoStopDelay:                5 * time.Millisecond,
		StartupTimeout:               time.Second,
		StartupPollInterval:          5 * time.Millisecond,
		StartupSettleDelay:           0,
		StartupBufferPackets:         8,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     65536,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}
	instance.mu.Lock()
	instance.backendReady = true
	instance.backendRunning = true
	instance.mu.Unlock()
	instance.scheduleAutoStop()
	select {
	case <-controller.stopEntered:
	case <-time.After(time.Second):
		t.Fatal("auto-stop did not call Stop()")
	}

	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatal(err)
	}

	assertWindow := time.NewTimer(40 * time.Millisecond)
	checkTicker := time.NewTicker(time.Millisecond)
	for {
		select {
		case <-checkTicker.C:
			if instance.store.Len() != 0 {
				assertWindow.Stop()
				checkTicker.Stop()
				t.Fatal("session was admitted while a stop signal was in flight")
			}
		case <-assertWindow.C:
			checkTicker.Stop()
			goto releaseStop
		}
	}

releaseStop:
	release()
	deadline := time.NewTimer(time.Second)
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for instance.store.Len() != 1 {
		select {
		case <-deadline.C:
			t.Fatal("wake packet was not admitted after stop completed")
		case <-poll.C:
		}
	}
	if !deadline.Stop() {
		select {
		case <-deadline.C:
		default:
		}
	}

	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestQueuedRetriesKeepStartingSessionActive(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateOffline)
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   controller,
		Detector:                     testWakeDetector{},
		MaxSessions:                  16,
		MaxPacketSize:                4096,
		IdleTimeout:                  25 * time.Millisecond,
		SweepInterval:                5 * time.Millisecond,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          5 * time.Millisecond,
		StartupSettleDelay:           0,
		StartupBufferPackets:         32,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     65536,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}
	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controller.startCh:
	case <-time.After(time.Second):
		t.Fatal("server did not begin startup")
	}

	retryTicker := time.NewTicker(10 * time.Millisecond)
	keepAliveWindow := time.NewTimer(100 * time.Millisecond)
keepAlive:
	for {
		select {
		case <-retryTicker.C:
			if _, err := client.Write([]byte("retry")); err != nil {
				t.Fatal(err)
			}
		case <-keepAliveWindow.C:
			retryTicker.Stop()
			break keepAlive
		}
	}
	if instance.store.Len() != 1 {
		t.Fatalf("session count = %d, want 1 while queued retries continue", instance.store.Len())
	}

	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestConcurrentColdStartAdmitsIndependentFlowsWithOneStart(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateOffline)
	readiness := &gatedReadiness{entered: make(chan struct{}, 1), release: make(chan struct{})}
	instance, err := New(Options{
		Listener:                     listener,
		BackendAddr:                  backend.LocalAddr().(*net.UDPAddr),
		Controller:                   controller,
		Detector:                     testWakeDetector{},
		Readiness:                    readiness,
		MaxSessions:                  16,
		MaxPacketSize:                4096,
		IdleTimeout:                  time.Minute,
		SweepInterval:                time.Minute,
		AutoStopDelay:                time.Hour,
		StartupTimeout:               time.Second,
		StartupPollInterval:          time.Millisecond,
		StartupBufferPackets:         4,
		StartupBufferBytesPerSession: 4096,
		StartupBufferBytesGlobal:     65536,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	select {
	case <-instance.Ready():
	case <-time.After(time.Second):
		t.Fatal("proxy did not become ready")
	}
	clients := make([]*net.UDPConn, 0, 16)
	for range 16 {
		client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
		if _, err := client.Write([]byte("wake")); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}()
	deadline := time.NewTimer(time.Second)
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for instance.store.Len() != 16 {
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("admitted sessions = %d, want 16", instance.store.Len())
		}
	}
	if !deadline.Stop() {
		select {
		case <-deadline.C:
		default:
		}
	}
	select {
	case <-controller.startCh:
	case <-time.After(time.Second):
		t.Fatal("Start() was not called")
	}
	controller.mu.Lock()
	startCalls := controller.startCalls
	controller.mu.Unlock()
	if startCalls != 1 {
		t.Fatalf("Start() calls = %d, want 1", startCalls)
	}
	controller.setState(server.StateRunning)
	select {
	case <-readiness.entered:
	case <-time.After(time.Second):
		t.Fatal("readiness probe did not start")
	}
	close(readiness.release)
	buffer := make([]byte, 16)
	_ = backend.SetReadDeadline(time.Now().Add(time.Second))
	for range 16 {
		n, _, err := backend.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if string(buffer[:n]) != "wake" {
			t.Fatalf("backend received %q", buffer[:n])
		}
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestShutdownWaitsForInFlightAutoStop(t *testing.T) {
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	controller.stopEntered = make(chan struct{}, 1)
	controller.releaseStop = make(chan struct{})
	instance := &Proxy{
		options: Options{
			Controller:         controller,
			AutoStopDelay:      time.Millisecond,
			AutoStopRetryDelay: time.Millisecond,
		},
		store:        store,
		backendReady: true,
	}
	instance.scheduleAutoStop()
	select {
	case <-controller.stopEntered:
	case <-time.After(time.Second):
		t.Fatal("auto-stop did not begin")
	}
	shutdownDone := make(chan struct{})
	go func() {
		instance.shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned while auto-stop was still in flight")
	case <-time.After(20 * time.Millisecond):
	}
	close(controller.releaseStop)
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after auto-stop returned")
	}
}

func TestAutoStopWaitsForBackendReadiness(t *testing.T) {
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	instance := &Proxy{
		options: Options{
			Controller:         controller,
			AutoStopDelay:      5 * time.Millisecond,
			AutoStopRetryDelay: time.Millisecond,
		},
		store:  store,
		queues: make(map[*session.Session]*packetQueue),
	}
	instance.scheduleAutoStop()
	time.Sleep(15 * time.Millisecond)
	controller.mu.Lock()
	callsBeforeReady := controller.stopCalls
	controller.mu.Unlock()
	if callsBeforeReady != 0 {
		t.Fatalf("Stop() called %d times before readiness", callsBeforeReady)
	}

	instance.openStartupGate()
	deadline := time.Now().Add(time.Second)
	for {
		controller.mu.Lock()
		calls := controller.stopCalls
		controller.mu.Unlock()
		if calls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Stop() calls = %d after readiness, want 1", calls)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestColdStartProxyIntegration(t *testing.T) {
	var panelMu sync.Mutex
	panelState := server.StateOffline
	startCalled := make(chan struct{}, 1)
	stopCalled := make(chan struct{}, 1)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet:
			panelMu.Lock()
			state := panelState
			panelMu.Unlock()
			_, _ = w.Write([]byte(`{"attributes":{"current_state":"` + string(state) + `"}}`))
		case request.Method == http.MethodPost:
			panelMu.Lock()
			if panelState == server.StateOffline {
				panelState = server.StateRunning
				select {
				case startCalled <- struct{}{}:
				default:
				}
			} else {
				panelState = server.StateStopping
				select {
				case stopCalled <- struct{}{}:
				default:
				}
			}
			panelMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	defer panel.Close()
	controller, err := server.NewPterodactylClient(config.PterodactylConfig{
		BaseURL: panel.URL, APIToken: "test-token", ServerID: "test-server",
	}, panel.Client())
	if err != nil {
		t.Fatal(err)
	}

	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	backendPackets := make(chan []byte, 1)
	go func() {
		buffer := make([]byte, 1024)
		n, peer, readErr := backend.ReadFromUDP(buffer)
		if readErr != nil {
			return
		}
		packet := append([]byte(nil), buffer[:n]...)
		backendPackets <- packet
		_, _ = backend.WriteToUDP(append([]byte("echo:"), packet...), peer)
	}()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	probe := &gatedReadiness{entered: make(chan struct{}, 1), release: make(chan struct{})}
	instance, err := New(Options{
		Listener: listener, BackendAddr: backend.LocalAddr().(*net.UDPAddr), Controller: controller,
		Detector: testWakeDetector{}, Readiness: probe, MaxSessions: 2, MaxPacketSize: 1024,
		IdleTimeout: 30 * time.Millisecond, SweepInterval: 5 * time.Millisecond,
		AutoStopDelay: 10 * time.Millisecond, AutoStopRetryDelay: time.Millisecond,
		StartupTimeout: time.Second, StartupPollInterval: 5 * time.Millisecond,
		StartupBufferPackets: 4, StartupBufferBytesPerSession: 1024, StartupBufferBytesGlobal: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- instance.Serve(ctx) }()
	<-instance.Ready()
	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-startCalled:
	case <-time.After(time.Second):
		t.Fatal("start was not requested")
	}
	select {
	case packet := <-backendPackets:
		t.Fatalf("packet reached backend before readiness: %q", packet)
	case <-time.After(20 * time.Millisecond):
	}
	close(probe.release)
	response := make([]byte, 1024)
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := client.Read(response)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(response[:n]); got != "echo:wake" {
		t.Fatalf("response = %q", got)
	}
	select {
	case <-stopCalled:
	case <-time.After(time.Second):
		t.Fatal("idle delayed stop was not requested")
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy did not stop")
	}
}

func TestAutoStopRetriesAndEventuallySucceeds(t *testing.T) {
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	controller.stopResults = []error{errors.New("temporary one"), errors.New("temporary two")}
	instance := &Proxy{
		options: Options{
			Controller:         controller,
			AutoStopDelay:      time.Millisecond,
			AutoStopRetryDelay: time.Millisecond,
		},
		store:        store,
		backendReady: true,
	}
	instance.scheduleAutoStop()

	deadline := time.Now().Add(time.Second)
	for {
		controller.mu.Lock()
		calls := controller.stopCalls
		state := controller.state
		controller.mu.Unlock()
		if calls == 3 {
			if state != server.StateStopping {
				t.Fatalf("state = %q after successful retry", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Stop() calls = %d, want 3", calls)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAutoStopStopsRetryingAfterBound(t *testing.T) {
	store, err := session.NewStore(1)
	if err != nil {
		t.Fatal(err)
	}
	controller := newFakeController(server.StateRunning)
	controller.stopResults = []error{errors.New("one"), errors.New("two"), errors.New("three"), errors.New("four")}
	controller.stopEntered = make(chan struct{}, maxAutoStopAttempts+1)
	instance := &Proxy{
		options: Options{
			Controller:         controller,
			AutoStopDelay:      time.Millisecond,
			AutoStopRetryDelay: time.Millisecond,
		},
		store:        store,
		backendReady: true,
	}
	instance.scheduleAutoStop()

	for attempt := 1; attempt <= maxAutoStopAttempts; attempt++ {
		select {
		case <-controller.stopEntered:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for Stop() attempt %d", attempt)
		}
	}
	select {
	case <-controller.stopEntered:
		t.Fatal("auto-stop exceeded its retry bound")
	case <-time.After(20 * time.Millisecond):
	}
	controller.mu.Lock()
	calls := controller.stopCalls
	controller.mu.Unlock()
	if calls != maxAutoStopAttempts {
		t.Fatalf("Stop() calls = %d, want bounded %d", calls, maxAutoStopAttempts)
	}
}
