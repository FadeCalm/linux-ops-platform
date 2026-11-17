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

// AuthConfig controls login sessions, bearer tokens and the bootstrap admin
// password. AccessPassword is only used when the admin account does not exist
// yet; an empty value seeds "changeme". It no longer disables authentication.
type AuthConfig struct {
	AccessPassword string `yaml:"access_password"`
	// SessionHours is the lifetime of a browser login cookie.
	SessionHours int `yaml:"session_hours"`
	// JWTSecret signs short-lived bearer tokens for API clients. When empty a
	// random key is generated on first start and persisted in app_settings, so
	// tokens survive restarts. Set JWT_SECRET to rotate it.
	JWTSecret string `yaml:"jwt_secret"`
	// JWTMinutes is the lifetime of an issued JWT (default 120).
	JWTMinutes int `yaml:"jwt_minutes"`
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
	Auth        AuthConfig      `yaml:"auth"`
}

func Default() *Config {
	return &Config{
		ListenAddr:  "127.0.0.1:8089",
		AllowOrigin: "*",
		LogDir:      "./data/logs",
		MySQL: MySQLConfig{
			Host: "127.0.0.1", Port: 3306, User: "ops", DBName: "linux_ops",
			MaxOpenConns: 20, MaxIdleConns: 5, AutoMigrate: true,
		},
		MinIO: MinIOConfig{Bucket: "wxgzt-ops"},
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
		Auth:      AuthConfig{SessionHours: 12, JWTMinutes: 120},
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
	if v := os.Getenv("ACCESS_PASSWORD"); v != "" {
		cfg.Auth.AccessPassword = v
	}
	if v := os.Getenv("SESSION_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Auth.SessionHours = n
		}
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("JWT_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Auth.JWTMinutes = n
		}
	}
	// MinIO settings mirror bin/minio_upload.py so one environment file works
	// for both. The backend itself does not read objects yet; these values
	// keep the deployment configuration in one place.
	if v := os.Getenv("MINIO_ENDPOINT"); v != "" {
		cfg.MinIO.Endpoint = v
	}
	if v := os.Getenv("MINIO_ACCESS_KEY"); v != "" {
		cfg.MinIO.AccessKey = v
	}
	if v := os.Getenv("MINIO_SECRET_KEY"); v != "" {
		cfg.MinIO.SecretKey = v
	}
	if v := os.Getenv("MINIO_BUCKET"); v != "" {
		cfg.MinIO.Bucket = v
	}
	if v := os.Getenv("MINIO_UPDATE_BASE_URL"); v != "" {
		cfg.MinIO.UpdateBaseURL = v
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
	if c.Auth.SessionHours <= 0 {
		c.Auth.SessionHours = 12
	}
	if c.Auth.JWTMinutes <= 0 {
		c.Auth.JWTMinutes = 120
	}
	if c.Auth.JWTMinutes > 1440 {
		c.Auth.JWTMinutes = 1440
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
