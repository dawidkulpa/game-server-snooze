package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/config"
	"dkulpa.eu/game-server-snooze/pkg/server"
)

type readinessController struct{ state server.State }

func (controller readinessController) Status(context.Context) (server.State, error) {
	return controller.state, nil
}
func (readinessController) Start(context.Context) error { return nil }
func (readinessController) Stop(context.Context) error  { return nil }

func TestNewPalworldDetectorUsesConfiguredWakePolicy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WakePolicy = "signature"
	cfg.WakeSignatures = []string{"aabb"}
	cfg.DiagnosticPrefixBytes = 8

	detector, err := newPalworldDetector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !detector.DetectStart([]byte{0xaa, 0xbb, 0xcc}) {
		t.Fatal("configured signature did not reach the Palworld detector")
	}
	if detector.DetectStart([]byte{0x09, 0x08, 0x00, 0x04, 0xbc, 0x59, 0x7e, 0x73}) {
		t.Fatal("detector ignored configured signatures and used legacy defaults")
	}
}

func TestProxyOptionsCarryValidatedRuntimeConfiguration(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxSessions = 17
	cfg.MaxPacketSize = 8192
	cfg.IdleTimeout = 45 * time.Second
	cfg.AutoStopDelay = 30 * time.Minute
	cfg.StartupTimeout = 4 * time.Minute
	cfg.StartupPollInterval = 3 * time.Second
	cfg.StartupSettleDelay = 7 * time.Second
	cfg.StartupBufferPackets = 11
	cfg.StartupBufferBytesPerSession = 12345
	cfg.StartupBufferBytesGlobal = 54321

	probe, err := newBackendReadiness(cfg, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8211}, readinessController{state: server.StateRunning})
	if err != nil {
		t.Fatal(err)
	}
	got := proxyOptionsFromConfig(cfg, (*net.UDPConn)(nil), (*net.UDPAddr)(nil), nil, nil, probe)
	if got.MaxSessions != 17 || got.MaxPacketSize != 8192 || got.IdleTimeout != 45*time.Second || got.AutoStopDelay != 30*time.Minute {
		t.Fatalf("session lifecycle options not carried: %+v", got)
	}
	if got.StartupTimeout != 4*time.Minute || got.StartupPollInterval != 3*time.Second || got.StartupSettleDelay != 7*time.Second {
		t.Fatalf("startup timing options not carried: %+v", got)
	}
	if got.StartupBufferPackets != 11 || got.StartupBufferBytesPerSession != 12345 || got.StartupBufferBytesGlobal != 54321 {
		t.Fatalf("startup buffer options not carried: %+v", got)
	}
	if got.Readiness != probe {
		t.Fatal("backend readiness probe not carried into proxy options")
	}
}

func TestNewBackendReadinessUsesPterodactylByDefaultAndAllowsExplicitFallbacks(t *testing.T) {
	cfg := config.DefaultConfig()
	backend := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8211}
	controller := readinessController{state: server.StateRunning}
	probe, err := newBackendReadiness(cfg, backend, controller)
	if err != nil {
		t.Fatal(err)
	}
	if probe == nil {
		t.Fatal("default Pterodactyl readiness produced no state probe")
	}
	if err := probe.Probe(context.Background()); err != nil {
		t.Fatalf("default Pterodactyl readiness rejected running state: %v", err)
	}
	cfg.BackendReadinessMode = "a2s"
	probe, err = newBackendReadiness(cfg, backend, controller)
	if err != nil || probe == nil {
		t.Fatalf("explicit A2S fallback failed: probe=%v err=%v", probe, err)
	}
	cfg.BackendReadinessMode = "delay"
	probe, err = newBackendReadiness(cfg, backend, controller)
	if err != nil {
		t.Fatal(err)
	}
	if probe != nil {
		t.Fatal("delay mode unexpectedly created an application probe")
	}
}

func TestRunStartsHealthAndShutsDownCleanly(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/client/servers/test-server/resources" {
			t.Fatalf("unexpected Pterodactyl path %q", request.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"attributes":{"current_state":"offline"}}`))
	}))
	defer panel.Close()

	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	healthAddr := reserved.Addr().String()
	_ = reserved.Close()

	t.Setenv("LISTEN_ADDR", "127.0.0.1:0")
	t.Setenv("SERVER_ADDR", "127.0.0.1:8211")
	t.Setenv("HEALTH_ADDR", healthAddr)
	t.Setenv("BACKEND_READINESS_MODE", "delay")
	t.Setenv("PTERO_BASE_URL", panel.URL)
	t.Setenv("PTERO_API_TOKEN", "test-token")
	t.Setenv("PTERO_SERVER_ID", "test-server")
	t.Setenv("LOG_LEVEL", "error")

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- run(ctx) }()

	healthURL := "http://" + healthAddr + "/healthz"
	deadline := time.Now().Add(time.Second)
	for {
		response, requestErr := http.Get(healthURL) // #nosec G107 -- the endpoint is a test-only loopback listener.
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("health endpoint did not become ready: %v", requestErr)
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run() shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run() did not finish graceful shutdown")
	}
}
