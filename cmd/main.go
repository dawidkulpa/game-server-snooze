package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/config"
	"dkulpa.eu/game-server-snooze/pkg/games"
	"dkulpa.eu/game-server-snooze/pkg/proxy"
	"dkulpa.eu/game-server-snooze/pkg/readiness"
	"dkulpa.eu/game-server-snooze/pkg/server"
	"github.com/sirupsen/logrus"
)

func newPalworldDetector(cfg config.Config) (*games.PalworldGame, error) {
	return games.NewConfiguredPalworldGame(games.PalworldOptions{
		WakePolicy:            cfg.WakePolicy,
		Signatures:            cfg.WakeSignatures,
		LogUnmatchedPrefixes:  cfg.LogUnmatchedPrefixes,
		DiagnosticPrefixBytes: cfg.DiagnosticPrefixBytes,
	})
}

func newBackendReadiness(cfg config.Config, backend *net.UDPAddr) (proxy.BackendReadiness, error) {
	if cfg.BackendReadinessMode == "delay" {
		return nil, nil
	}
	if cfg.BackendReadinessMode != "a2s" {
		return nil, fmt.Errorf("unsupported backend readiness mode %q", cfg.BackendReadinessMode)
	}
	address := cfg.BackendReadinessAddr
	if address == "" {
		address = net.JoinHostPort(backend.IP.String(), "27015")
	}
	return readiness.NewA2SProbe(address, cfg.StartupPollInterval)
}

func proxyOptionsFromConfig(cfg config.Config, listener *net.UDPConn, backend *net.UDPAddr, controller server.Controller, detector proxy.WakeDetector, backendReadiness proxy.BackendReadiness) proxy.Options {
	return proxy.Options{
		Listener:                     listener,
		BackendAddr:                  backend,
		Controller:                   controller,
		Detector:                     detector,
		Readiness:                    backendReadiness,
		MaxSessions:                  cfg.MaxSessions,
		MaxPacketSize:                cfg.MaxPacketSize,
		IdleTimeout:                  cfg.IdleTimeout,
		AutoStopDelay:                cfg.AutoStopDelay,
		StartupTimeout:               cfg.StartupTimeout,
		StartupPollInterval:          cfg.StartupPollInterval,
		StartupSettleDelay:           cfg.StartupSettleDelay,
		StartupBufferPackets:         cfg.StartupBufferPackets,
		StartupBufferBytesPerSession: cfg.StartupBufferBytesPerSession,
		StartupBufferBytesGlobal:     cfg.StartupBufferBytesGlobal,
	}
}

func run(parent context.Context) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Game != "palworld" {
		return fmt.Errorf("unsupported game %q", cfg.Game)
	}
	detector, err := newPalworldDetector(cfg)
	if err != nil {
		return fmt.Errorf("configure Palworld detector: %w", err)
	}
	controller, err := server.NewPterodactylClient(cfg.Pterodactyl, nil)
	if err != nil {
		return fmt.Errorf("configure Pterodactyl client: %w", err)
	}
	listenAddr, err := net.ResolveUDPAddr("udp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("resolve UDP listener: %w", err)
	}
	backendAddr, err := net.ResolveUDPAddr("udp", cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("resolve Palworld backend: %w", err)
	}
	backendReadiness, err := newBackendReadiness(cfg, backendAddr)
	if err != nil {
		return fmt.Errorf("configure Palworld readiness: %w", err)
	}
	listener, err := net.ListenUDP("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen for Palworld UDP: %w", err)
	}
	instance, err := proxy.New(proxyOptionsFromConfig(cfg, listener, backendAddr, controller, detector, backendReadiness))
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("configure UDP proxy: %w", err)
	}
	healthListener, err := net.Listen("tcp", cfg.HealthAddr)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("listen for health checks: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	healthState := &proxy.HealthState{}
	healthServer := &http.Server{
		Handler:           proxy.NewHealthHandler(healthState),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	healthErrors := make(chan error, 1)
	proxyErrors := make(chan error, 1)
	healthState.SetReady(true)
	go func() { healthErrors <- healthServer.Serve(healthListener) }()
	go func() { proxyErrors <- instance.Serve(ctx) }()
	logrus.Infof("Palworld snooze proxy listening on %s", cfg.ListenAddr)

	var result error
	proxyDone := false
	healthDone := false
	select {
	case <-parent.Done():
	case err := <-proxyErrors:
		proxyDone = true
		if err != nil && !errors.Is(err, context.Canceled) {
			result = fmt.Errorf("serve UDP proxy: %w", err)
		}
	case err := <-healthErrors:
		healthDone = true
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			result = fmt.Errorf("serve health endpoint: %w", err)
		}
	}
	healthState.SetReady(false)
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		result = errors.Join(result, fmt.Errorf("shutdown health endpoint: %w", err))
	}
	_ = listener.Close()
	if !proxyDone {
		select {
		case err := <-proxyErrors:
			if err != nil && !errors.Is(err, context.Canceled) {
				result = errors.Join(result, fmt.Errorf("shutdown UDP proxy: %w", err))
			}
		case <-shutdownCtx.Done():
			result = errors.Join(result, fmt.Errorf("shutdown UDP proxy: %w", shutdownCtx.Err()))
		}
	}
	if !healthDone {
		select {
		case err := <-healthErrors:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				result = errors.Join(result, fmt.Errorf("shutdown health endpoint: %w", err))
			}
		case <-shutdownCtx.Done():
			result = errors.Join(result, fmt.Errorf("wait for health endpoint shutdown: %w", shutdownCtx.Err()))
		}
	}
	return result
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		address := "http://127.0.0.1:8080/healthz"
		if len(os.Args) > 2 {
			address = os.Args[2]
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := checkHealth(ctx, address); err != nil {
			logrus.Fatal(err)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		logrus.Fatal(err)
	}
}
