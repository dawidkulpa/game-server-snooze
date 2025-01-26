package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

var GlobalConfig Config

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

	Pterodactyl PterodactylConfig `yaml:"pterodactyl"`
	LogLevel    string            `yaml:"log_level"`
}

type PterodactylConfig struct {
	BaseURL  string `yaml:"base_url"`
	APIToken string `yaml:"api_token"`
	ServerID string `yaml:"server_id"`
}

func DefaultConfig() Config {
	return Config{
		ListenAddr:    ":8211",
		ServerAddr:    "",
		MaxSessions:   32,
		MaxPacketSize: 65507, // max UDP packet size(data only), for most games could be much lower, like 4096
		Game:          "palworld",
		AutoStopDelay: time.Minute,
		Pterodactyl: PterodactylConfig{
			BaseURL:  "",
			APIToken: "",
			ServerID: "",
		},
		LogLevel:    "info", // debug, warn, error, fatal
		IdleTimeout: 30 * time.Second,
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

	applyEnvOverrides(&cfg)
	setLogLevel(cfg.LogLevel)
	logrus.Debugf("Final config: %+v", cfg)
	GlobalConfig = cfg
	return cfg, nil
}

func parseYAML(file *os.File, cfg *Config) error {
	d := yaml.NewDecoder(file)
	d.KnownFields(true)
	return d.Decode(cfg)
}

func applyEnvOverrides(cfg *Config) {
	if val := os.Getenv("LISTEN_ADDR"); val != "" {
		cfg.ListenAddr = val
	}
	if val := os.Getenv("SERVER_ADDR"); val != "" {
		cfg.ServerAddr = val
	}
	if val := os.Getenv("MAX_SESSIONS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.MaxSessions = n
		}
	}
	if val := os.Getenv("MAX_PACKET_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.MaxPacketSize = n
		}
	}
	if val := os.Getenv("GAME"); val != "" {
		cfg.Game = val
	}

	if val := os.Getenv("AUTO_STOP_DELAY"); val != "" {
		if dur, err := time.ParseDuration(val); err == nil {
			cfg.AutoStopDelay = dur
		} else {
			logrus.Warnf("Warning: invalid AUTO_STOP_DELAY=%q: %v", val, err)
		}
	}

	if val := os.Getenv("PTERO_BASE_URL"); val != "" {
		cfg.Pterodactyl.BaseURL = val
	}
	if val := os.Getenv("PTERO_API_TOKEN"); val != "" {
		cfg.Pterodactyl.APIToken = val
	}
	if val := os.Getenv("PTERO_SERVER_ID"); val != "" {
		cfg.Pterodactyl.ServerID = val
	}

	if val := os.Getenv("LOG_LEVEL"); val != "" {
		cfg.LogLevel = val
	}

	if val := os.Getenv("IDLE_TIMEOUT"); val != "" {
		if dur, err := time.ParseDuration(val); err == nil {
			cfg.IdleTimeout = dur
		} else {
			logrus.Warnf("Warning: invalid IDLE_TIMEOUT=%q: %v", val, err)
		}
	}
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
