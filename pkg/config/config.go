package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr    string `yaml:"listen_addr"`
	ServerAddr    string `yaml:"server_addr"`
	MaxSessions   int    `yaml:"max_sessions"`
	MaxPacketSize int    `yaml:"max_packet_size"`
	Game          string `yaml:"game"` // game name, currently supported: palworld

	// how long after the last session ends we wait to stop the server.
	AutoStopDelay time.Duration `yaml:"auto_stop_delay"`
	// how long to wait for a session to become idle before closing it.
	IdleTimeout time.Duration `yaml:"idle_timeout"`

	StartupTimeout               time.Duration `yaml:"startup_timeout"`
	StartupPollInterval          time.Duration `yaml:"startup_poll_interval"`
	StartupSettleDelay           time.Duration `yaml:"startup_settle_delay"`
	StartupBufferPackets         int           `yaml:"startup_buffer_packets"`
	StartupBufferBytesPerSession int           `yaml:"startup_buffer_bytes_per_session"`
	StartupBufferBytesGlobal     int           `yaml:"startup_buffer_bytes_global"`

	WakePolicy            string   `yaml:"wake_policy"`
	WakeSignatures        []string `yaml:"wake_signatures"`
	LogUnmatchedPrefixes  bool     `yaml:"log_unmatched_prefixes"`
	DiagnosticPrefixBytes int      `yaml:"diagnostic_prefix_bytes"`
	HealthAddr            string   `yaml:"health_addr"`
	BackendReadinessMode  string   `yaml:"backend_readiness_mode"`
	BackendReadinessAddr  string   `yaml:"backend_readiness_addr"`

	Pterodactyl PterodactylConfig `yaml:"pterodactyl"`
	LogLevel    string            `yaml:"log_level"`
}

type PterodactylConfig struct {
	BaseURL  string `yaml:"base_url"`
	APIToken string `yaml:"api_token"`
	ServerID string `yaml:"server_id"`
}

const maxUDPPayloadSize = 65507

func (cfg Config) RedactedString() string {
	redacted := cfg
	redacted.ServerAddr = "***REDACTED***"
	if redacted.BackendReadinessAddr != "" {
		redacted.BackendReadinessAddr = "***REDACTED***"
	}
	redacted.Pterodactyl.BaseURL = "***REDACTED***"
	redacted.Pterodactyl.APIToken = "***REDACTED***"
	redacted.Pterodactyl.ServerID = "***REDACTED***"
	return fmt.Sprintf("%+v", redacted)
}

func (cfg Config) Validate() error {
	if _, err := net.ResolveUDPAddr("udp", cfg.ListenAddr); err != nil {
		return fmt.Errorf("invalid listen_addr: %w", err)
	}
	if strings.TrimSpace(cfg.ServerAddr) == "" {
		return fmt.Errorf("server_addr is required")
	}
	serverAddr, err := net.ResolveUDPAddr("udp", cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("invalid server_addr: %w", err)
	}
	if serverAddr.IP == nil || serverAddr.IP.IsUnspecified() {
		return fmt.Errorf("server_addr must identify a specific backend host")
	}
	if cfg.MaxSessions < 1 {
		return fmt.Errorf("max_sessions must be positive")
	}
	if cfg.MaxPacketSize < 1 || cfg.MaxPacketSize > maxUDPPayloadSize {
		return fmt.Errorf("max_packet_size must be between 1 and %d", maxUDPPayloadSize)
	}
	if cfg.Game != "palworld" {
		return fmt.Errorf("game must be palworld")
	}
	if cfg.IdleTimeout <= 0 {
		return fmt.Errorf("idle_timeout must be positive")
	}
	if cfg.AutoStopDelay <= 0 {
		return fmt.Errorf("auto_stop_delay must be positive")
	}
	if cfg.StartupTimeout <= 0 {
		return fmt.Errorf("startup_timeout must be positive")
	}
	if cfg.StartupPollInterval <= 0 || cfg.StartupPollInterval >= cfg.StartupTimeout {
		return fmt.Errorf("startup_poll_interval must be positive and shorter than startup_timeout")
	}
	if cfg.StartupSettleDelay < 0 || cfg.StartupSettleDelay >= cfg.StartupTimeout {
		return fmt.Errorf("startup_settle_delay must be non-negative and shorter than startup_timeout")
	}
	if cfg.StartupBufferPackets < 1 {
		return fmt.Errorf("startup_buffer_packets must be positive")
	}
	if cfg.StartupBufferBytesPerSession < cfg.MaxPacketSize {
		return fmt.Errorf("startup_buffer_bytes_per_session must hold at least one max_packet_size datagram")
	}
	if cfg.StartupBufferBytesGlobal < cfg.StartupBufferBytesPerSession {
		return fmt.Errorf("startup_buffer_bytes_global must be at least startup_buffer_bytes_per_session")
	}
	if cfg.WakePolicy != "signature" && cfg.WakePolicy != "any" {
		return fmt.Errorf("wake_policy must be signature or any")
	}
	if cfg.WakePolicy == "signature" && len(cfg.WakeSignatures) == 0 {
		return fmt.Errorf("wake_signatures must not be empty under signature policy")
	}
	for _, signature := range cfg.WakeSignatures {
		decoded, err := hex.DecodeString(strings.TrimSpace(signature))
		if err != nil || len(decoded) == 0 {
			return fmt.Errorf("wake_signatures must contain non-empty hexadecimal prefixes")
		}
	}
	if cfg.DiagnosticPrefixBytes < 1 || cfg.DiagnosticPrefixBytes > 32 {
		return fmt.Errorf("diagnostic_prefix_bytes must be between 1 and 32")
	}
	healthAddr, err := net.ResolveTCPAddr("tcp", cfg.HealthAddr)
	if err != nil {
		return fmt.Errorf("invalid health_addr: %w", err)
	}
	if healthAddr.IP == nil || !healthAddr.IP.IsLoopback() {
		return fmt.Errorf("health_addr must use a loopback address")
	}
	if cfg.BackendReadinessMode != "pterodactyl" && cfg.BackendReadinessMode != "a2s" && cfg.BackendReadinessMode != "delay" {
		return fmt.Errorf("backend_readiness_mode must be pterodactyl, a2s, or delay")
	}
	if cfg.BackendReadinessAddr != "" {
		if cfg.BackendReadinessMode != "a2s" {
			return fmt.Errorf("backend_readiness_addr is only valid in a2s mode")
		}
		readinessAddr, err := net.ResolveUDPAddr("udp", cfg.BackendReadinessAddr)
		if err != nil {
			return fmt.Errorf("invalid backend_readiness_addr: %w", err)
		}
		if readinessAddr.IP == nil || readinessAddr.IP.IsUnspecified() {
			return fmt.Errorf("backend_readiness_addr must identify a specific host")
		}
	}
	panelURL, err := url.Parse(cfg.Pterodactyl.BaseURL)
	if err != nil || (panelURL.Scheme != "http" && panelURL.Scheme != "https") || panelURL.Host == "" || panelURL.User != nil {
		return fmt.Errorf("pterodactyl.base_url must be a valid HTTP or HTTPS URL without user info")
	}
	if panelURL.Scheme == "http" {
		host := panelURL.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("pterodactyl.base_url may use plaintext HTTP only for loopback tests")
		}
	}
	if strings.TrimSpace(cfg.Pterodactyl.APIToken) == "" {
		return fmt.Errorf("pterodactyl.api_token is required")
	}
	if strings.TrimSpace(cfg.Pterodactyl.ServerID) == "" {
		return fmt.Errorf("pterodactyl.server_id is required")
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error", "fatal":
	default:
		return fmt.Errorf("log_level must be debug, info, warn, error, or fatal")
	}
	return nil
}

func DefaultConfig() Config {
	return Config{
		ListenAddr:                   ":8211",
		ServerAddr:                   "",
		MaxSessions:                  32,
		MaxPacketSize:                maxUDPPayloadSize,
		Game:                         "palworld",
		AutoStopDelay:                time.Minute,
		IdleTimeout:                  30 * time.Second,
		StartupTimeout:               5 * time.Minute,
		StartupPollInterval:          2 * time.Second,
		StartupSettleDelay:           5 * time.Second,
		StartupBufferPackets:         32,
		StartupBufferBytesPerSession: 262144,
		StartupBufferBytesGlobal:     4194304,
		WakePolicy:                   "signature",
		WakeSignatures:               []string{"09080004bc597e73"},
		LogUnmatchedPrefixes:         false,
		DiagnosticPrefixBytes:        8,
		HealthAddr:                   "127.0.0.1:8080",
		BackendReadinessMode:         "pterodactyl",
		BackendReadinessAddr:         "",
		Pterodactyl: PterodactylConfig{
			BaseURL:  "",
			APIToken: "",
			ServerID: "",
		},
		LogLevel: "info", // debug, warn, error, fatal
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()

	fileName := "config.yaml"
	if f, err := os.Open(fileName); err == nil {
		defer f.Close()
		if err := parseYAML(f, &cfg); err != nil {
			return cfg, fmt.Errorf("could not parse %s: %v", fileName, err)
		}
	} else if !os.IsNotExist(err) {
		fmt.Printf("Config does not exist %s: %v", fileName, err)
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("invalid configuration: %w", err)
	}
	setLogLevel(cfg.LogLevel)
	logrus.Debugf("Final config: %s", cfg.RedactedString())
	return cfg, nil
}

func parseYAML(file *os.File, cfg *Config) error {
	d := yaml.NewDecoder(file)
	d.KnownFields(true)
	if err := d.Decode(cfg); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple YAML documents are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func applyEnvOverrides(cfg *Config) error {
	if val, ok := os.LookupEnv("LISTEN_ADDR"); ok {
		cfg.ListenAddr = val
	}
	if val, ok := os.LookupEnv("SERVER_ADDR"); ok {
		cfg.ServerAddr = val
	}
	if val, ok := os.LookupEnv("MAX_SESSIONS"); ok {
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid MAX_SESSIONS=%q: %w", val, err)
		}
		cfg.MaxSessions = n
	}
	if val, ok := os.LookupEnv("MAX_PACKET_SIZE"); ok {
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid MAX_PACKET_SIZE=%q: %w", val, err)
		}
		cfg.MaxPacketSize = n
	}
	if val, ok := os.LookupEnv("GAME"); ok {
		cfg.Game = val
	}
	if val, ok := os.LookupEnv("AUTO_STOP_DELAY"); ok {
		dur, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("invalid AUTO_STOP_DELAY=%q: %w", val, err)
		}
		cfg.AutoStopDelay = dur
	}
	if val, ok := os.LookupEnv("PTERO_BASE_URL"); ok {
		cfg.Pterodactyl.BaseURL = val
	}
	if val, ok := os.LookupEnv("PTERO_API_TOKEN"); ok {
		cfg.Pterodactyl.APIToken = val
	}
	if val, ok := os.LookupEnv("PTERO_SERVER_ID"); ok {
		cfg.Pterodactyl.ServerID = val
	}
	if val, ok := os.LookupEnv("LOG_LEVEL"); ok {
		cfg.LogLevel = val
	}
	if val, ok := os.LookupEnv("IDLE_TIMEOUT"); ok {
		dur, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("invalid IDLE_TIMEOUT=%q: %w", val, err)
		}
		cfg.IdleTimeout = dur
	}
	for name, target := range map[string]*time.Duration{
		"STARTUP_TIMEOUT":       &cfg.StartupTimeout,
		"STARTUP_POLL_INTERVAL": &cfg.StartupPollInterval,
		"STARTUP_SETTLE_DELAY":  &cfg.StartupSettleDelay,
	} {
		if val, ok := os.LookupEnv(name); ok {
			dur, err := time.ParseDuration(val)
			if err != nil {
				return fmt.Errorf("invalid %s=%q: %w", name, val, err)
			}
			*target = dur
		}
	}
	for name, target := range map[string]*int{
		"STARTUP_BUFFER_PACKETS":           &cfg.StartupBufferPackets,
		"STARTUP_BUFFER_BYTES_PER_SESSION": &cfg.StartupBufferBytesPerSession,
		"STARTUP_BUFFER_BYTES_GLOBAL":      &cfg.StartupBufferBytesGlobal,
		"DIAGNOSTIC_PREFIX_BYTES":          &cfg.DiagnosticPrefixBytes,
	} {
		if val, ok := os.LookupEnv(name); ok {
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("invalid %s=%q: %w", name, val, err)
			}
			*target = n
		}
	}
	if val, ok := os.LookupEnv("WAKE_POLICY"); ok {
		cfg.WakePolicy = strings.TrimSpace(val)
	}
	if val, ok := os.LookupEnv("WAKE_SIGNATURES"); ok {
		parts := strings.Split(val, ",")
		cfg.WakeSignatures = make([]string, len(parts))
		for i, part := range parts {
			cfg.WakeSignatures[i] = strings.TrimSpace(part)
		}
	}
	if val, ok := os.LookupEnv("LOG_UNMATCHED_PREFIXES"); ok {
		switch val {
		case "true":
			cfg.LogUnmatchedPrefixes = true
		case "false":
			cfg.LogUnmatchedPrefixes = false
		default:
			return fmt.Errorf("invalid LOG_UNMATCHED_PREFIXES=%q: expected true or false", val)
		}
	}
	if val, ok := os.LookupEnv("HEALTH_ADDR"); ok {
		cfg.HealthAddr = val
	}
	if val, ok := os.LookupEnv("BACKEND_READINESS_MODE"); ok {
		cfg.BackendReadinessMode = strings.TrimSpace(val)
	}
	if val, ok := os.LookupEnv("BACKEND_READINESS_ADDR"); ok {
		cfg.BackendReadinessAddr = strings.TrimSpace(val)
	}
	return nil
}

func setLogLevel(logLevel string) {
	switch logLevel {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "warn":
		logrus.SetLevel(logrus.WarnLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	case "fatal":
		logrus.SetLevel(logrus.FatalLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}
}
