package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/server"
	"dkulpa.eu/game-server-snooze/pkg/session"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type WakeDetector interface {
	DetectStart([]byte) bool
}

type BackendReadiness interface {
	WaitReady(context.Context) error
	Probe(context.Context) error
}

type Options struct {
	Listener           *net.UDPConn
	BackendAddr        *net.UDPAddr
	Controller         server.Controller
	Detector           WakeDetector
	Readiness          BackendReadiness
	MaxSessions        int
	MaxPacketSize      int
	IdleTimeout        time.Duration
	SweepInterval      time.Duration
	AutoStopDelay      time.Duration
	AutoStopRetryDelay time.Duration

	StartupTimeout               time.Duration
	StartupPollInterval          time.Duration
	StartupSettleDelay           time.Duration
	StartupFailureCooldown       time.Duration
	StartupBufferPackets         int
	StartupBufferBytesPerSession int
	StartupBufferBytesGlobal     int
}

const (
	maxAutoStopAttempts           = 3
	defaultAutoStopRetryDelay     = 5 * time.Second
	defaultStartupFailureCooldown = 5 * time.Second
	backendHealthFailureThreshold = 3
)

type backendHealthFailures struct {
	generation uint64
	count      int
}

func (failures *backendHealthFailures) reset() {
	failures.generation = 0
	failures.count = 0
}

func (failures *backendHealthFailures) record(generation uint64) int {
	if failures.generation != generation {
		failures.generation = generation
		failures.count = 0
	}
	failures.count++
	return failures.count
}

func backendHealthFailureClass(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return "probe_failure"
}

type packetQueue struct {
	packets        [][]byte
	bytes          int
	overflowLogged bool
}

type Proxy struct {
	options Options
	store   *session.Store
	ready   chan struct{}

	ctx context.Context

	mu                  sync.Mutex
	admissionMu         sync.Mutex
	forwardingMu        sync.RWMutex
	backendReady        bool
	readinessGeneration uint64
	backendRunning      bool
	startupRunning      bool
	startupGeneration   uint64
	startupBlockedUntil time.Time
	shuttingDown        bool
	queues              map[*session.Session]*packetQueue
	queuedBytes         int
	stopTimer           *time.Timer
	stopGeneration      uint64
	lastCapacityWarning time.Time

	wg      sync.WaitGroup
	timerWG sync.WaitGroup
}

func New(options Options) (*Proxy, error) {
	if options.Listener == nil || options.BackendAddr == nil || options.Controller == nil || options.Detector == nil {
		return nil, fmt.Errorf("listener, backend, controller, and detector are required")
	}
	if options.SweepInterval <= 0 {
		options.SweepInterval = options.IdleTimeout / 2
		if options.SweepInterval <= 0 {
			options.SweepInterval = time.Nanosecond
		}
		if options.SweepInterval > 5*time.Second {
			options.SweepInterval = 5 * time.Second
		}
	}
	if options.AutoStopRetryDelay == 0 {
		options.AutoStopRetryDelay = defaultAutoStopRetryDelay
	}
	if options.StartupFailureCooldown == 0 {
		options.StartupFailureCooldown = defaultStartupFailureCooldown
	}
	if options.MaxPacketSize < 1 || options.IdleTimeout <= 0 || options.AutoStopDelay <= 0 || options.AutoStopRetryDelay < 0 || options.StartupFailureCooldown < 0 {
		return nil, fmt.Errorf("packet and lifecycle limits must be positive")
	}
	if options.StartupTimeout <= 0 || options.StartupPollInterval <= 0 || options.StartupPollInterval >= options.StartupTimeout || options.StartupSettleDelay < 0 {
		return nil, fmt.Errorf("invalid startup timing")
	}
	if options.StartupBufferPackets < 1 || options.StartupBufferBytesPerSession < options.MaxPacketSize || options.StartupBufferBytesGlobal < options.StartupBufferBytesPerSession {
		return nil, fmt.Errorf("startup buffers must hold at least one maximum-size packet per session")
	}
	store, err := session.NewStore(options.MaxSessions)
	if err != nil {
		return nil, err
	}
	return &Proxy{
		options: options,
		store:   store,
		ready:   make(chan struct{}),
		queues:  make(map[*session.Session]*packetQueue),
	}, nil
}

func (proxy *Proxy) Ready() <-chan struct{} {
	return proxy.ready
}

func (proxy *Proxy) Serve(ctx context.Context) error {
	serveCtx, cancel := context.WithCancel(ctx)
	proxy.ctx = serveCtx
	defer func() {
		cancel()
		proxy.shutdown()
		proxy.timerWG.Wait()
		proxy.wg.Wait()
	}()
	close(proxy.ready)
	state, err := proxy.options.Controller.Status(serveCtx)
	if err != nil {
		logrus.WithError(err).Warn("Could not determine initial Pterodactyl state; keeping wake gate closed")
	} else {
		logrus.WithField("state", state).Info("Initial Pterodactyl state detected")
	}
	if err == nil && state == server.StateRunning {
		proxy.mu.Lock()
		proxy.backendRunning = true
		proxy.backendReady = proxy.options.Readiness == nil
		if proxy.backendReady {
			proxy.readinessGeneration++
		}
		proxy.mu.Unlock()
		if proxy.options.Readiness != nil {
			proxy.startServer()
		}
	}

	proxy.wg.Add(2)
	go proxy.runSweeper(serveCtx)
	if proxy.options.Readiness != nil {
		proxy.wg.Add(1)
		go proxy.runBackendHealthMonitor()
	}
	go func() {
		defer proxy.wg.Done()
		<-serveCtx.Done()
		_ = proxy.options.Listener.Close()
	}()

	buffer := make([]byte, proxy.options.MaxPacketSize)
	for {
		n, _, flags, clientAddr, readErr := proxy.options.Listener.ReadMsgUDP(buffer, nil)
		if readErr != nil {
			if serveCtx.Err() != nil || errors.Is(readErr, net.ErrClosed) {
				break
			}
			return fmt.Errorf("read public UDP packet: %w", readErr)
		}
		if flags&unix.MSG_TRUNC != 0 {
			continue
		}
		packet := append([]byte(nil), buffer[:n]...)
		proxy.handleClientPacket(clientAddr, packet)
	}

	return ctx.Err()
}

func (proxy *Proxy) handleClientPacket(clientAddr *net.UDPAddr, packet []byte) {
	if len(packet) == 0 {
		return
	}
	key := clientAddr.String()
	if existing, ok := proxy.store.Get(key); ok {
		if proxy.forwardOrQueue(existing, packet) {
			return
		}
	}

	proxy.admissionMu.Lock()
	defer proxy.admissionMu.Unlock()
	if existing, ok := proxy.store.Get(key); ok {
		if proxy.forwardOrQueue(existing, packet) {
			return
		}
		_, _, _ = proxy.store.Remove(key, existing)
		proxy.removeQueue(existing)
	}

	proxy.mu.Lock()
	ready := proxy.backendReady
	running := proxy.backendRunning
	startupBlocked := time.Now().Before(proxy.startupBlockedUntil)
	proxy.mu.Unlock()
	if startupBlocked {
		return
	}
	if !ready && !running && !proxy.options.Detector.DetectStart(packet) {
		return
	}

	createdSession, created, activeCount, err := proxy.store.GetOrCreate(key, func() (*session.Session, error) {
		return session.NewSession(proxy.ctx, session.SessionOptions{
			ClientAddr:    clientAddr,
			BackendAddr:   proxy.options.BackendAddr,
			PublicConn:    proxy.options.Listener,
			MaxPacketSize: proxy.options.MaxPacketSize,
		})
	})
	if err != nil {
		if errors.Is(err, session.ErrSessionCapacity) {
			if proxy.shouldLogCapacityWarning(time.Now()) {
				logrus.WithField("max_sessions", proxy.options.MaxSessions).Warn("UDP session capacity reached; dropping new client packet")
			}
		} else {
			logrus.WithError(err).Warn("Could not create UDP session")
		}
		return
	}
	if created {
		logrus.WithField("active_sessions", activeCount).Info("UDP session opened")
		proxy.cancelAutoStop()
		proxy.watchSession(key, createdSession)
	}
	proxy.forwardOrQueue(createdSession, packet)
	if !ready {
		proxy.startServer()
	}
}

func (proxy *Proxy) shouldLogCapacityWarning(now time.Time) bool {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	if !proxy.lastCapacityWarning.IsZero() && now.Sub(proxy.lastCapacityWarning) < time.Second {
		return false
	}
	proxy.lastCapacityWarning = now
	return true
}

func (proxy *Proxy) watchSession(key string, active *session.Session) {
	proxy.wg.Add(1)
	go func() {
		defer proxy.wg.Done()
		<-active.Done()
		removed, count, err := proxy.store.Remove(key, active)
		if err != nil {
			logrus.WithError(err).Warn("Could not remove closed UDP session")
		}
		proxy.removeQueue(active)
		if removed {
			logrus.WithField("active_sessions", count).Info("UDP session closed")
		}
		if removed && count == 0 && proxy.ctx.Err() == nil {
			proxy.scheduleAutoStop()
		}
	}()
}

func (proxy *Proxy) removeQueue(active *session.Session) {
	proxy.mu.Lock()
	if queue, ok := proxy.queues[active]; ok {
		delete(proxy.queues, active)
		proxy.queuedBytes -= queue.bytes
	}
	proxy.mu.Unlock()
}

func (proxy *Proxy) forwardOrQueue(active *session.Session, packet []byte) bool {
	proxy.forwardingMu.RLock()
	defer proxy.forwardingMu.RUnlock()
	proxy.mu.Lock()
	if !proxy.backendReady {
		if err := active.RecordActivity(); err != nil {
			proxy.mu.Unlock()
			return false
		}
		proxy.enqueueLocked(active, packet)
		proxy.mu.Unlock()
		proxy.startServer()
		return true
	}
	proxy.mu.Unlock()
	if err := active.ForwardClient(packet); err != nil {
		if !errors.Is(err, session.ErrSessionClosed) {
			logrus.WithError(err).Warn("Could not forward client UDP packet")
		}
		return false
	}
	return true
}

func (proxy *Proxy) enqueueLocked(active *session.Session, packet []byte) {
	queue := proxy.queues[active]
	if queue == nil {
		queue = &packetQueue{}
		proxy.queues[active] = queue
	}
	if len(queue.packets) >= proxy.options.StartupBufferPackets || queue.bytes+len(packet) > proxy.options.StartupBufferBytesPerSession || proxy.queuedBytes+len(packet) > proxy.options.StartupBufferBytesGlobal {
		if !queue.overflowLogged {
			queue.overflowLogged = true
			logrus.Warn("Dropping startup packets because the bounded startup queue is full")
		}
		return
	}
	copyOfPacket := append([]byte(nil), packet...)
	queue.packets = append(queue.packets, copyOfPacket)
	queue.bytes += len(copyOfPacket)
	proxy.queuedBytes += len(copyOfPacket)
}

func (proxy *Proxy) startServer() {
	proxy.mu.Lock()
	if proxy.shuttingDown || proxy.backendReady || proxy.startupRunning || time.Now().Before(proxy.startupBlockedUntil) {
		proxy.mu.Unlock()
		return
	}
	proxy.startupRunning = true
	proxy.startupGeneration++
	generation := proxy.startupGeneration
	proxy.mu.Unlock()

	proxy.wg.Add(1)
	go func() {
		defer proxy.wg.Done()
		if err := proxy.ensureRunning(); err != nil && proxy.ctx.Err() == nil {
			proxy.failStartup(generation, server.IsPermanent(err))
			logrus.WithError(err).Error("Palworld startup gate failed")
			return
		}
		proxy.finishStartup(generation)
	}()
}

func (proxy *Proxy) finishStartup(generation uint64) {
	if proxy.completeStartup(generation) {
		proxy.startServer()
	}
}

func (proxy *Proxy) completeStartup(generation uint64) bool {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	if proxy.startupGeneration != generation {
		return false
	}
	proxy.startupRunning = false
	return !proxy.shuttingDown && !proxy.backendReady && len(proxy.queues) > 0 && !time.Now().Before(proxy.startupBlockedUntil)
}

func (proxy *Proxy) failStartup(generation uint64, permanent bool) {
	proxy.admissionMu.Lock()
	defer proxy.admissionMu.Unlock()
	proxy.mu.Lock()
	proxy.queues = make(map[*session.Session]*packetQueue)
	proxy.queuedBytes = 0
	if proxy.startupGeneration == generation {
		proxy.startupRunning = false
		if permanent {
			proxy.startupBlockedUntil = time.Now().Add(proxy.options.StartupFailureCooldown)
		}
	}
	proxy.mu.Unlock()
	if err := proxy.store.CloseAll(); err != nil {
		logrus.WithError(err).Warn("Could not close every pending UDP session after startup failure")
	}
}

func (proxy *Proxy) ensureRunning() error {
	ctx, cancel := context.WithTimeout(proxy.ctx, proxy.options.StartupTimeout)
	defer cancel()
	startSent := false
	ticker := time.NewTicker(proxy.options.StartupPollInterval)
	defer ticker.Stop()

	for {
		state, err := proxy.options.Controller.Status(ctx)
		if err == nil {
			switch state {
			case server.StateRunning:
				proxy.mu.Lock()
				proxy.backendRunning = true
				proxy.mu.Unlock()
				if proxy.options.Readiness != nil {
					if err := proxy.options.Readiness.WaitReady(ctx); err != nil {
						return fmt.Errorf("wait for Palworld application readiness: %w", err)
					}
				}
				if proxy.options.StartupSettleDelay > 0 {
					timer := time.NewTimer(proxy.options.StartupSettleDelay)
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
					}
				}
				if proxy.options.Readiness != nil {
					if err := proxy.options.Readiness.Probe(ctx); err != nil {
						return fmt.Errorf("recheck Palworld application readiness after settle: %w", err)
					}
				}
				proxy.openStartupGate()
				return nil
			case server.StateOffline:
				proxy.markBackendNotRunning()
				if !startSent {
					logrus.Info("Requesting Palworld start")
					startErr := proxy.options.Controller.Start(ctx)
					if startErr == nil {
						logrus.Info("Palworld start request accepted")
					}
					if startErr != nil && server.IsPermanent(startErr) {
						return fmt.Errorf("start Pterodactyl server: %w", startErr)
					}
					// A retryable transport/5xx response is ambiguous: the panel may
					// have accepted the request. Never send a duplicate start; keep
					// polling status within the startup deadline.
					startSent = true
				}
			case server.StateStarting:
				proxy.markBackendNotRunning()
				startSent = true
			case server.StateStopping:
				proxy.markBackendNotRunning()
				startSent = false
			}
		} else if server.IsPermanent(err) {
			return fmt.Errorf("query Pterodactyl status: %w", err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Pterodactyl running: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (proxy *Proxy) markBackendNotRunning() {
	proxy.forwardingMu.Lock()
	defer proxy.forwardingMu.Unlock()
	proxy.mu.Lock()
	proxy.backendRunning = false
	if proxy.backendReady {
		proxy.backendReady = false
		proxy.readinessGeneration++
	}
	proxy.mu.Unlock()
}

func (proxy *Proxy) openStartupGate() {
	proxy.admissionMu.Lock()
	defer proxy.admissionMu.Unlock()
	proxy.forwardingMu.Lock()
	defer proxy.forwardingMu.Unlock()
	proxy.mu.Lock()
	if proxy.shuttingDown {
		proxy.mu.Unlock()
		return
	}
	proxy.backendRunning = true
	// Keep ingress gated while flushing so a newly arrived datagram cannot
	// overtake an older buffered handshake datagram for the same session.
	queuedSessions := len(proxy.queues)
	queuedPackets := 0
	for active, queue := range proxy.queues {
		queuedPackets += len(queue.packets)
		for _, packet := range queue.packets {
			if err := active.ForwardClient(packet); err != nil {
				break
			}
		}
	}
	proxy.queues = make(map[*session.Session]*packetQueue)
	proxy.queuedBytes = 0
	if !proxy.backendReady {
		proxy.backendReady = true
		proxy.readinessGeneration++
	}
	proxy.mu.Unlock()

	activeSessions := proxy.store.Len()
	logrus.WithFields(logrus.Fields{
		"active_sessions": activeSessions,
		"queued_sessions": queuedSessions,
		"queued_packets":  queuedPackets,
	}).Info("Palworld backend ready; UDP forwarding enabled")

	if activeSessions == 0 {
		proxy.scheduleAutoStop()
	}
}

func (proxy *Proxy) refreshPendingSessions() {
	proxy.mu.Lock()
	pending := make([]*session.Session, 0, len(proxy.queues))
	for active := range proxy.queues {
		pending = append(pending, active)
	}
	proxy.mu.Unlock()
	for _, active := range pending {
		_ = active.RecordActivity()
	}
}

func (proxy *Proxy) runBackendHealthMonitor() {
	defer proxy.wg.Done()
	ticker := time.NewTicker(proxy.options.StartupPollInterval)
	defer ticker.Stop()
	failures := backendHealthFailures{}
	for {
		select {
		case <-proxy.ctx.Done():
			return
		case <-ticker.C:
		}

		proxy.mu.Lock()
		ready := proxy.backendReady
		generation := proxy.readinessGeneration
		proxy.mu.Unlock()
		if !ready {
			failures.reset()
			continue
		}

		probeCtx, cancel := context.WithTimeout(proxy.ctx, proxy.options.StartupPollInterval)
		err := proxy.options.Readiness.Probe(probeCtx)
		cancel()
		if proxy.ctx.Err() != nil {
			return
		}
		proxy.mu.Lock()
		generationCurrent := proxy.backendReady && proxy.readinessGeneration == generation
		proxy.mu.Unlock()
		if !generationCurrent {
			failures.reset()
			continue
		}
		if err == nil {
			failures.reset()
			continue
		}
		failureClass := backendHealthFailureClass(err)
		failureCount := failures.record(generation)
		if failureCount < backendHealthFailureThreshold {
			logrus.WithFields(logrus.Fields{
				"failure_class": failureClass,
				"failure_count": failureCount,
			}).Warn("Palworld backend health probe failed; waiting for confirmation")
			continue
		}
		failures.reset()
		proxy.closeBackendGate(generation, failureClass)
	}
}

func (proxy *Proxy) closeBackendGate(generation uint64, failureClass string) {
	proxy.admissionMu.Lock()
	defer proxy.admissionMu.Unlock()
	proxy.forwardingMu.Lock()
	defer proxy.forwardingMu.Unlock()

	proxy.mu.Lock()
	if !proxy.backendReady || proxy.readinessGeneration != generation {
		proxy.mu.Unlock()
		return
	}
	proxy.backendReady = false
	proxy.backendRunning = false
	proxy.readinessGeneration++
	proxy.stopGeneration++
	proxy.stopAutoStopTimerLocked()
	proxy.queues = make(map[*session.Session]*packetQueue)
	proxy.queuedBytes = 0
	proxy.mu.Unlock()

	activeSessions := proxy.store.Len()
	if err := proxy.store.CloseAll(); err != nil {
		logrus.WithError(err).Warn("Could not close every UDP session after backend readiness was lost")
	}
	logrus.WithFields(logrus.Fields{
		"active_sessions": activeSessions,
		"failure_class":   failureClass,
	}).Warn("Palworld backend readiness lost; UDP forwarding gated")
}

func (proxy *Proxy) runSweeper(ctx context.Context) {
	defer proxy.wg.Done()
	ticker := time.NewTicker(proxy.options.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			proxy.expireIdleSessions(now)
		}
	}
}

func (proxy *Proxy) expireIdleSessions(now time.Time) {
	proxy.refreshPendingSessions()
	expired, count, err := proxy.store.ExpireIdle(now.Add(-proxy.options.IdleTimeout))
	if err != nil {
		logrus.WithError(err).Warn("Could not close every idle session")
	}
	if expired > 0 {
		proxy.removeClosedQueues()
		logrus.WithFields(logrus.Fields{
			"expired_sessions": expired,
			"active_sessions":  count,
		}).Info("Idle UDP sessions expired")
	}
	if expired > 0 && count == 0 {
		proxy.scheduleAutoStop()
	}
}

func (proxy *Proxy) removeClosedQueues() {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	for active, queue := range proxy.queues {
		select {
		case <-active.Done():
			delete(proxy.queues, active)
			proxy.queuedBytes -= queue.bytes
		default:
		}
	}
}

func (proxy *Proxy) cancelAutoStop() {
	proxy.mu.Lock()
	hadPendingTimer := proxy.stopTimer != nil
	proxy.stopGeneration++
	proxy.stopAutoStopTimerLocked()
	proxy.mu.Unlock()
	if hadPendingTimer {
		logrus.Info("Palworld auto-stop cancelled by new UDP session")
	}
}

func (proxy *Proxy) scheduleAutoStop() {
	proxy.mu.Lock()
	if proxy.shuttingDown || !proxy.backendReady {
		proxy.mu.Unlock()
		return
	}
	proxy.stopGeneration++
	generation := proxy.stopGeneration
	proxy.stopAutoStopTimerLocked()
	proxy.scheduleAutoStopAttemptLocked(generation, 1, proxy.options.AutoStopDelay)
	delay := proxy.options.AutoStopDelay
	proxy.mu.Unlock()
	logrus.WithFields(logrus.Fields{
		"active_sessions": 0,
		"delay":           delay.String(),
	}).Info("Palworld auto-stop scheduled")
}

func (proxy *Proxy) stopAutoStopTimerLocked() {
	if proxy.stopTimer == nil {
		return
	}
	if proxy.stopTimer.Stop() {
		proxy.timerWG.Done()
	}
	proxy.stopTimer = nil
}

func (proxy *Proxy) scheduleAutoStopAttemptLocked(generation uint64, attempt int, delay time.Duration) {
	proxy.timerWG.Add(1)
	proxy.stopTimer = time.AfterFunc(delay, func() {
		defer proxy.timerWG.Done()
		proxy.runAutoStopAttempt(generation, attempt)
	})
}

func (proxy *Proxy) runAutoStopAttempt(generation uint64, attempt int) {
	proxy.admissionMu.Lock()
	defer proxy.admissionMu.Unlock()
	if proxy.store.Len() != 0 {
		return
	}
	proxy.mu.Lock()
	if generation != proxy.stopGeneration || !proxy.backendReady {
		proxy.mu.Unlock()
		return
	}
	proxy.mu.Unlock()

	logrus.WithField("attempt", attempt).Info("Requesting Palworld auto-stop")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := proxy.options.Controller.Stop(ctx)
	cancel()
	if err == nil {
		logrus.WithField("attempt", attempt).Info("Palworld auto-stop request accepted")
		proxy.markBackendNotRunning()
		proxy.mu.Lock()
		if generation == proxy.stopGeneration {
			proxy.stopTimer = nil
		}
		proxy.mu.Unlock()
		return
	}

	if attempt >= maxAutoStopAttempts {
		logrus.WithError(err).Errorf("Could not auto-stop Palworld after %d attempts", attempt)
		proxy.mu.Lock()
		if generation == proxy.stopGeneration {
			proxy.stopTimer = nil
		}
		proxy.mu.Unlock()
		return
	}
	logrus.WithError(err).Warnf("Could not auto-stop Palworld on attempt %d/%d; retrying", attempt, maxAutoStopAttempts)
	proxy.mu.Lock()
	if generation == proxy.stopGeneration && proxy.backendReady {
		delay := proxy.options.AutoStopRetryDelay * time.Duration(1<<(attempt-1))
		proxy.scheduleAutoStopAttemptLocked(generation, attempt+1, delay)
	}
	proxy.mu.Unlock()
}

func (proxy *Proxy) shutdown() {
	proxy.admissionMu.Lock()
	defer proxy.admissionMu.Unlock()
	proxy.forwardingMu.Lock()
	defer proxy.forwardingMu.Unlock()
	proxy.mu.Lock()
	proxy.shuttingDown = true
	if proxy.backendReady {
		proxy.backendReady = false
		proxy.readinessGeneration++
	}
	proxy.backendRunning = false
	proxy.startupRunning = false
	proxy.startupGeneration++
	proxy.stopGeneration++
	proxy.stopAutoStopTimerLocked()
	proxy.mu.Unlock()
	if err := proxy.store.CloseAll(); err != nil {
		logrus.WithError(err).Warn("Could not close every UDP session")
	}
}
