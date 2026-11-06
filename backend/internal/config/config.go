// Package config loads backend configuration from a YAML file with a small
// set of environment-variable overrides for secrets.
package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type MySQLConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	User         string `yaml:"user"`
	Password     string `yaml:"password"`
	DBName       string `yaml:"dbname"`
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
	AutoMigrate  bool   `yaml:"auto_migrate"`
	Seed         bool   `yaml:"seed"`
}

type SchedulerConfig struct {
	HeartbeatIntervalSec int   `yaml:"heartbeat_interval_sec"`
	DispatchConcurrency  int   `yaml:"dispatch_concurrency"`
	PollIntervalMS       int   `yaml:"poll_interval_ms"`
	DefaultTimeoutMS     int64 `yaml:"default_timeout_ms"`
	MaxTimeoutMS         int64 `yaml:"max_timeout_ms"`
	MaxScriptBytes       int   `yaml:"max_script_bytes"`
	MaxOutputBytes       int64 `yaml:"max_output_bytes"`
	AgentRequestTimeoutS int   `yaml:"agent_request_timeout_sec"`
}

type WebSocketConfig struct {
	MaxConnections  int `yaml:"max_connections"`
	ReplayBufferLen int `yaml:"replay_buffer_len"`
	// MaxTerminalSessions caps concurrent interactive terminals proxied by
	// this backend (M4).
	MaxTerminalSessions int `yaml:"max_terminal_sessions"`
}

// MinIOConfig holds the object store used for agent packages (and large logs
// in M6). The backend does not read objects itself yet; the same section is
// consumed by bin/minio_upload.py so credentials live in one place.
type MinIOConfig struct {
	Endpoint      string `yaml:"endpoint"`
	AccessKey     string `yaml:"access_key"`
	SecretKey     string `yaml:"secret_key"`
	Bucket        string `yaml:"bucket"`
	UpdateBaseURL string `yaml:"update_base_url"`
}

type Config struct {
	ListenAddr  string          `yaml:"listen_addr"`
	AllowOrigin string          `yaml:"allow_origin"`
	FrontendDir string          `yaml:"frontend_dir"`
	MySQL       MySQLConfig     `yaml:"mysql"`
	MinIO       MinIOConfig     `yaml:"minio"`
	LogDir      string          `yaml:"log_dir"`
	Scheduler   SchedulerConfig `yaml:"scheduler"`
	WebSocket   WebSocketConfig `yaml:"websocket"`
}

func Default() *Config {
	return &Config{
		ListenAddr:  "127.0.0.1:8080",
		AllowOrigin: "*",
		LogDir:      "./data/logs",
		MySQL: MySQLConfig{
			Host: "127.0.0.1", Port: 3306, User: "ops", DBName: "linux_ops",
			MaxOpenConns: 20, MaxIdleConns: 5, AutoMigrate: true,
		},
		MinIO: MinIOConfig{Bucket: "linux-ops-agent"},
		Scheduler: SchedulerConfig{
			HeartbeatIntervalSec: 20,
			DispatchConcurrency:  8,
			PollIntervalMS:       300,
			DefaultTimeoutMS:     300000,
			MaxTimeoutMS:         3600000,
			MaxScriptBytes:       65536,
			MaxOutputBytes:       10485760,
			AgentRequestTimeoutS: 10,
		},
		WebSocket: WebSocketConfig{MaxConnections: 200, ReplayBufferLen: 2000, MaxTerminalSessions: 16},
	}
}

// Load reads YAML from path (if non-empty) and applies env overrides.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
		} else if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	applyEnv(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.MySQL.Password = v
	}
	if v := os.Getenv("DB_DSN"); v != "" {
		cfg.MySQL.DSN = v
	}
	if v := os.Getenv("DB_HOST"); v != "" {
		cfg.MySQL.Host = v
	}
	if v := os.Getenv("DB_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.MySQL.Port = p
		}
	}
	if v := os.Getenv("DB_USER"); v != "" {
		cfg.MySQL.User = v
	}
	if v := os.Getenv("DB_NAME"); v != "" {
		cfg.MySQL.DBName = v
	}
	if v := os.Getenv("LOG_DIR"); v != "" {
		cfg.LogDir = v
	}
	if v := os.Getenv("ALLOW_ORIGIN"); v != "" {
		cfg.AllowOrigin = v
	}
	if v := os.Getenv("FRONTEND_DIR"); v != "" {
		cfg.FrontendDir = v
	}
}

func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if c.MySQL.DSN == "" {
		if c.MySQL.Host == "" || c.MySQL.User == "" || c.MySQL.DBName == "" {
			return fmt.Errorf("mysql host/user/dbname (or dsn) is required")
		}
	}
	if c.Scheduler.DispatchConcurrency <= 0 {
		c.Scheduler.DispatchConcurrency = 8
	}
	if c.Scheduler.PollIntervalMS <= 0 {
		c.Scheduler.PollIntervalMS = 300
	}
	if c.Scheduler.DefaultTimeoutMS <= 0 {
		c.Scheduler.DefaultTimeoutMS = 300000
	}
	if c.Scheduler.MaxTimeoutMS <= 0 {
		c.Scheduler.MaxTimeoutMS = 3600000
	}
	if c.Scheduler.MaxScriptBytes <= 0 {
		c.Scheduler.MaxScriptBytes = 65536
	}
	if c.WebSocket.ReplayBufferLen <= 0 {
		c.WebSocket.ReplayBufferLen = 2000
	}
	if c.WebSocket.MaxConnections <= 0 {
		c.WebSocket.MaxConnections = 200
	}
	return nil
}

// DSN returns the effective database DSN, building one from parts if needed.
func (c *Config) DSN() string {
	if c.MySQL.DSN != "" {
		return c.MySQL.DSN
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=UTC",
		c.MySQL.User, c.MySQL.Password, c.MySQL.Host, c.MySQL.Port, c.MySQL.DBName)
}

// DefaultTimeoutMS returns the effective default timeout.
func (c *Config) DefaultTimeoutMS() int64 { return c.Scheduler.DefaultTimeoutMS }
