// Package config loads agent configuration. The agent binds to 127.0.0.1 by
// default to avoid accidental exposure; cross-host deployments must opt into a
// LAN address plus network ACLs.
package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// TerminalConfig controls interactive PTY sessions (M4).
type TerminalConfig struct {
	// Shell is started inside the PTY.
	Shell string `yaml:"shell"`
	// WorkDir is the shell's initial directory.
	WorkDir string `yaml:"work_dir"`
	// User is empty to inherit the agent process user, or "root" to run the
	// shell as UID/GID 0. root only works when the agent itself is privileged.
	User string `yaml:"user"`
	// MaxSessions caps concurrent interactive terminals on this host.
	MaxSessions int `yaml:"max_sessions"`
	// IdleTimeoutSec closes a session with no input/output for this long.
	// 0 disables the check.
	IdleTimeoutSec int `yaml:"idle_timeout_sec"`
	// MaxDurationSec closes a session after this long in total. 0 disables.
	MaxDurationSec int `yaml:"max_duration_sec"`
}

type Config struct {
	ListenAddr       string         `yaml:"listen_addr"`
	ListenPort       int            `yaml:"listen_port"`
	WorkDir          string         `yaml:"work_dir"`
	LogDir           string         `yaml:"log_dir"`
	MaxConcurrent    int            `yaml:"max_concurrent"`
	MaxOutputBytes   int64          `yaml:"max_output_bytes"`
	MaxScriptBytes   int            `yaml:"max_script_bytes"`
	DefaultTimeoutMS int64          `yaml:"default_timeout_ms"`
	MaxTimeoutMS     int64          `yaml:"max_timeout_ms"`
	TaskRetentionSec int            `yaml:"task_retention_sec"`
	Terminal         TerminalConfig `yaml:"terminal"`
	// UpdateBaseURL points at the artifact prefix that serves manifest.json
	// and the agent tarball. Empty disables self-update.
	UpdateBaseURL string `yaml:"update_base_url"`
}

func Default() *Config {
	return &Config{
		ListenAddr:       "127.0.0.1",
		ListenPort:       19090,
		WorkDir:          "/tmp",
		LogDir:           "./data/agent-logs",
		MaxConcurrent:    8,
		MaxOutputBytes:   10 * 1024 * 1024,
		MaxScriptBytes:   1024 * 1024,
		DefaultTimeoutMS: 300000,
		MaxTimeoutMS:     3600000,
		TaskRetentionSec: 3600,
		Terminal: TerminalConfig{
			Shell:          "/bin/bash",
			WorkDir:        "/",
			MaxSessions:    4,
			IdleTimeoutSec: 600,
			MaxDurationSec: 3600,
		},
		UpdateBaseURL: "http://47.114.120.205:9000/linux-ops-agent",
	}
}

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
	if v := os.Getenv("AGENT_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("AGENT_LISTEN_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.ListenPort = p
		}
	}
	if v := os.Getenv("AGENT_LOG_DIR"); v != "" {
		cfg.LogDir = v
	}
	if v, ok := os.LookupEnv("AGENT_UPDATE_BASE_URL"); ok {
		cfg.UpdateBaseURL = v
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if c.ListenPort < 1 || c.ListenPort > 65535 {
		return fmt.Errorf("invalid listen_port")
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 8
	}
	if c.MaxOutputBytes <= 0 {
		c.MaxOutputBytes = 10 * 1024 * 1024
	}
	if c.MaxScriptBytes <= 0 {
		c.MaxScriptBytes = 1024 * 1024
	}
	if c.DefaultTimeoutMS <= 0 {
		c.DefaultTimeoutMS = 300000
	}
	if c.MaxTimeoutMS <= 0 {
		c.MaxTimeoutMS = 3600000
	}
	if c.TaskRetentionSec <= 0 {
		c.TaskRetentionSec = 3600
	}
	if c.Terminal.Shell == "" {
		c.Terminal.Shell = "/bin/bash"
	}
	if c.Terminal.WorkDir == "" {
		c.Terminal.WorkDir = "/"
	}
	if c.Terminal.MaxSessions <= 0 {
		c.Terminal.MaxSessions = 4
	}
	if c.Terminal.IdleTimeoutSec < 0 {
		c.Terminal.IdleTimeoutSec = 0
	}
	if c.Terminal.MaxDurationSec < 0 {
		c.Terminal.MaxDurationSec = 0
	}
	if c.Terminal.User != "" && c.Terminal.User != "root" {
		return fmt.Errorf("terminal.user must be empty or \"root\"")
	}
	return nil
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.ListenAddr, c.ListenPort)
}
