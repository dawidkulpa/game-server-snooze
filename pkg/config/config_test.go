package config

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func validConfig() Config {
	cfg := DefaultConfig()
	cfg.ServerAddr = "127.0.0.1:8211"
	cfg.Pterodactyl = PterodactylConfig{
		BaseURL:  "https://panel.example.com",
		APIToken: "top-secret-token",
		ServerID: "server-id",
	}
	return cfg
}

func TestValidateRejectsPacketSizeAboveUDPMaximum(t *testing.T) {
	cfg := validConfig()
	cfg.MaxPacketSize = 65508

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted a packet size above the UDP payload maximum")
	}
}

func TestValidateAcceptsPterodactylReadinessWithoutA2SAddress(t *testing.T) {
	cfg := validConfig()
	cfg.BackendReadinessMode = "pterodactyl"
	cfg.BackendReadinessAddr = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected Pterodactyl readiness: %v", err)
	}
}

func TestValidateRejectsInvalidRequiredConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"invalid listen address", func(c *Config) { c.ListenAddr = "bad address" }},
		{"missing server address", func(c *Config) { c.ServerAddr = "" }},
		{"invalid server address", func(c *Config) { c.ServerAddr = "bad address" }},
		{"wildcard server address", func(c *Config) { c.ServerAddr = ":8211" }},
		{"non-positive max sessions", func(c *Config) { c.MaxSessions = 0 }},
		{"unsupported game", func(c *Config) { c.Game = "other" }},
		{"non-positive idle timeout", func(c *Config) { c.IdleTimeout = 0 }},
		{"non-positive auto-stop delay", func(c *Config) { c.AutoStopDelay = 0 }},
		{"non-positive startup timeout", func(c *Config) { c.StartupTimeout = 0 }},
		{"non-positive startup poll", func(c *Config) { c.StartupPollInterval = 0 }},
		{"poll not below timeout", func(c *Config) { c.StartupPollInterval = c.StartupTimeout }},
		{"negative settle delay", func(c *Config) { c.StartupSettleDelay = -time.Second }},
		{"settle not below timeout", func(c *Config) { c.StartupSettleDelay = c.StartupTimeout }},
		{"non-positive packet queue", func(c *Config) { c.StartupBufferPackets = 0 }},
		{"non-positive session buffer", func(c *Config) { c.StartupBufferBytesPerSession = 0 }},
		{"session buffer below max packet", func(c *Config) { c.StartupBufferBytesPerSession = c.MaxPacketSize - 1 }},
		{"global buffer below session", func(c *Config) { c.StartupBufferBytesGlobal = c.StartupBufferBytesPerSession - 1 }},
		{"unsupported wake policy", func(c *Config) { c.WakePolicy = "magic" }},
		{"signature policy without signatures", func(c *Config) { c.WakeSignatures = nil }},
		{"invalid hexadecimal signature", func(c *Config) { c.WakeSignatures = []string{"xyz"} }},
		{"diagnostic prefix too small", func(c *Config) { c.DiagnosticPrefixBytes = 0 }},
		{"diagnostic prefix too large", func(c *Config) { c.DiagnosticPrefixBytes = 33 }},
		{"invalid health address", func(c *Config) { c.HealthAddr = ":::" }},
		{"non-loopback health address", func(c *Config) { c.HealthAddr = "0.0.0.0:8080" }},
		{"unsupported readiness mode", func(c *Config) { c.BackendReadinessMode = "magic" }},
		{"invalid readiness address", func(c *Config) { c.BackendReadinessAddr = "bad address" }},
		{"wildcard readiness address", func(c *Config) { c.BackendReadinessAddr = ":27015" }},
		{"invalid Pterodactyl URL", func(c *Config) { c.Pterodactyl.BaseURL = "://bad" }},
		{"plaintext non-loopback Pterodactyl URL", func(c *Config) { c.Pterodactyl.BaseURL = "http://panel.example.com" }},
		{"missing Pterodactyl token", func(c *Config) { c.Pterodactyl.APIToken = "" }},
		{"missing Pterodactyl server ID", func(c *Config) { c.Pterodactyl.ServerID = "" }},
		{"unsupported log level", func(c *Config) { c.LogLevel = "verbose" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid configuration")
			}
		})
	}
}

func TestRedactedStringOmitsAPIToken(t *testing.T) {
	cfg := validConfig()

	got := cfg.RedactedString()
	if strings.Contains(got, cfg.Pterodactyl.APIToken) {
		t.Fatal("RedactedString() exposed the Pterodactyl API token")
	}
	if !strings.Contains(got, "***REDACTED***") {
		t.Fatal("RedactedString() did not mark the token as redacted")
	}
}

func TestDefaultConfigHasBoundedStartupAndSignaturePolicy(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.StartupTimeout != 5*time.Minute || cfg.StartupPollInterval != 2*time.Second || cfg.StartupSettleDelay != 5*time.Second {
		t.Fatalf("unexpected startup timing defaults: timeout=%v poll=%v settle=%v", cfg.StartupTimeout, cfg.StartupPollInterval, cfg.StartupSettleDelay)
	}
	if cfg.StartupBufferPackets != 32 || cfg.StartupBufferBytesPerSession != 262144 || cfg.StartupBufferBytesGlobal != 4194304 {
		t.Fatalf("unexpected startup buffer defaults: packets=%d per_session=%d global=%d", cfg.StartupBufferPackets, cfg.StartupBufferBytesPerSession, cfg.StartupBufferBytesGlobal)
	}
	if cfg.WakePolicy != "signature" || len(cfg.WakeSignatures) == 0 {
		t.Fatalf("unexpected wake defaults: policy=%q signatures=%v", cfg.WakePolicy, cfg.WakeSignatures)
	}
	if cfg.LogUnmatchedPrefixes || cfg.DiagnosticPrefixBytes != 8 {
		t.Fatalf("unexpected diagnostic defaults: enabled=%v prefix=%d", cfg.LogUnmatchedPrefixes, cfg.DiagnosticPrefixBytes)
	}
	if cfg.HealthAddr != "127.0.0.1:8080" {
		t.Fatalf("unexpected health address: %q", cfg.HealthAddr)
	}
	if cfg.BackendReadinessMode != "pterodactyl" || cfg.BackendReadinessAddr != "" {
		t.Fatalf("unexpected backend readiness defaults: mode=%q address=%q", cfg.BackendReadinessMode, cfg.BackendReadinessAddr)
	}
}

func TestApplyEnvOverridesRejectsInvalidInteger(t *testing.T) {
	t.Setenv("MAX_SESSIONS", "not-an-integer")
	cfg := validConfig()

	if err := applyEnvOverrides(&cfg); err == nil {
		t.Fatal("applyEnvOverrides() silently accepted an invalid integer")
	}
}

func TestApplyEnvOverridesAppliesStartupWakeAndHealthSettings(t *testing.T) {
	t.Setenv("STARTUP_TIMEOUT", "90s")
	t.Setenv("STARTUP_POLL_INTERVAL", "250ms")
	t.Setenv("STARTUP_SETTLE_DELAY", "3s")
	t.Setenv("STARTUP_BUFFER_PACKETS", "12")
	t.Setenv("STARTUP_BUFFER_BYTES_PER_SESSION", "4096")
	t.Setenv("STARTUP_BUFFER_BYTES_GLOBAL", "65536")
	t.Setenv("WAKE_POLICY", "any")
	t.Setenv("WAKE_SIGNATURES", "0102, aabbcc")
	t.Setenv("LOG_UNMATCHED_PREFIXES", "true")
	t.Setenv("DIAGNOSTIC_PREFIX_BYTES", "12")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:9090")
	t.Setenv("BACKEND_READINESS_MODE", "delay")
	t.Setenv("BACKEND_READINESS_ADDR", "127.0.0.1:27016")
	cfg := validConfig()

	if err := applyEnvOverrides(&cfg); err != nil {
		t.Fatalf("applyEnvOverrides() returned error: %v", err)
	}
	if cfg.StartupTimeout != 90*time.Second || cfg.StartupPollInterval != 250*time.Millisecond || cfg.StartupSettleDelay != 3*time.Second {
		t.Fatalf("startup timings not applied: timeout=%v poll=%v settle=%v", cfg.StartupTimeout, cfg.StartupPollInterval, cfg.StartupSettleDelay)
	}
	if cfg.StartupBufferPackets != 12 || cfg.StartupBufferBytesPerSession != 4096 || cfg.StartupBufferBytesGlobal != 65536 {
		t.Fatalf("startup limits not applied: packets=%d per=%d global=%d", cfg.StartupBufferPackets, cfg.StartupBufferBytesPerSession, cfg.StartupBufferBytesGlobal)
	}
	if cfg.WakePolicy != "any" || len(cfg.WakeSignatures) != 2 || cfg.WakeSignatures[1] != "aabbcc" {
		t.Fatalf("wake settings not applied: policy=%q signatures=%v", cfg.WakePolicy, cfg.WakeSignatures)
	}
	if !cfg.LogUnmatchedPrefixes || cfg.DiagnosticPrefixBytes != 12 || cfg.HealthAddr != "127.0.0.1:9090" {
		t.Fatalf("diagnostic/health settings not applied: log=%v prefix=%d health=%q", cfg.LogUnmatchedPrefixes, cfg.DiagnosticPrefixBytes, cfg.HealthAddr)
	}
	if cfg.BackendReadinessMode != "delay" || cfg.BackendReadinessAddr != "127.0.0.1:27016" {
		t.Fatalf("readiness settings not applied: mode=%q address=%q", cfg.BackendReadinessMode, cfg.BackendReadinessAddr)
	}
}

func TestValidateAllowsPlaintextLoopbackPterodactylURLForTests(t *testing.T) {
	cfg := validConfig()
	cfg.Pterodactyl.BaseURL = "http://127.0.0.1:8080"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loopback HTTP URL rejected: %v", err)
	}
}

func TestLoadConfigRejectsTrailingYAMLDocument(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.WriteFile("config.yaml", []byte("max_sessions: 16\n---\nunknown_field: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVER_ADDR", "127.0.0.1:8211")
	t.Setenv("PTERO_BASE_URL", "https://panel.example.com")
	t.Setenv("PTERO_API_TOKEN", "test-token")
	t.Setenv("PTERO_SERVER_ID", "test-server")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("LoadConfig() trailing-document error = %v", err)
	}
}

func TestLoadConfigEnvironmentOverridesYAML(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	contents := `
server_addr: 127.0.0.1:8211
max_sessions: 2
pterodactyl:
  base_url: https://panel.example.com
  api_token: yaml-token
  server_id: yaml-server
`
	if err := os.WriteFile("config.yaml", []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAX_SESSIONS", "3")
	t.Setenv("PTERO_API_TOKEN", "env-token")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxSessions != 3 || cfg.Pterodactyl.APIToken != "env-token" {
		t.Fatalf("environment did not override YAML: sessions=%d token_match=%v", cfg.MaxSessions, cfg.Pterodactyl.APIToken == "env-token")
	}
}

func TestLoadConfigEmptyEnvironmentOverridesYAML(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	contents := `
server_addr: 127.0.0.1:8211
pterodactyl:
  base_url: https://panel.example.com
  api_token: yaml-token
  server_id: yaml-server
`
	if err := os.WriteFile("config.yaml", []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PTERO_API_TOKEN", "")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "api_token is required") {
		t.Fatalf("empty environment token did not override YAML: %v", err)
	}
}

func TestBooleanEnvironmentOverrideIsStrict(t *testing.T) {
	for _, value := range []string{"1", "t", "TRUE", "False"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LOG_UNMATCHED_PREFIXES", value)
			cfg := DefaultConfig()
			if err := applyEnvOverrides(&cfg); err == nil {
				t.Fatalf("accepted non-canonical boolean %q", value)
			}
		})
	}
}

func TestLoadConfigDebugLogRedactsToken(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	const token = "debug-secret-token"
	contents := `
server_addr: 127.0.0.1:8211
log_level: debug
pterodactyl:
  base_url: https://panel.example.com
  api_token: ` + token + `
  server_id: test-server
`
	if err := os.WriteFile("config.yaml", []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := logrus.StandardLogger()
	oldOutput, oldLevel := logger.Out, logger.Level
	var output bytes.Buffer
	logger.SetOutput(&output)
	t.Cleanup(func() {
		logger.SetOutput(oldOutput)
		logger.SetLevel(oldLevel)
	})
	if _, err := LoadConfig(); err != nil {
		t.Fatal(err)
	}
	logged := output.String()
	for _, private := range []string{token, "127.0.0.1:8211", "https://panel.example.com", "test-server"} {
		if strings.Contains(logged, private) {
			t.Fatalf("debug config log exposed private value %q", private)
		}
	}
	if !strings.Contains(logged, "***REDACTED***") {
		t.Fatalf("debug config log was not redacted: %q", logged)
	}
}

func TestLoadConfigRejectsMissingRequiredValues(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() accepted defaults with missing backend and Pterodactyl values")
	}
}
