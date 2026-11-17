// Package config loads agent configuration. listen_addr: "auto" (the default)
// binds this host's primary non-loopback IPv4 so the same agent.yaml works on
// every host; an explicit address still wins. Cross-host deployments also need
// an explicit source-IP allowlist (allowed_ips). Binding to 0.0.0.0 or using a
// wildcard allowlist is rejected.
package config

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"

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
	// HistoryMode is "inherit" (default; bash may append to the user's
	// ~/.bash_history) or "memory" (history only inside the session, never
	// persisted). The user's existing history file is never modified.
	HistoryMode string `yaml:"history_mode"`
}

type Config struct {
	// ListenAddr is a concrete IP, "auto", or empty (both mean auto). Auto is
	// resolved by Load; after that it always holds a concrete address.
	ListenAddr string `yaml:"listen_addr"`
	// ListenAuto records that ListenAddr was auto-detected, so main can log the
	// chosen address once at startup. Not part of the YAML schema.
	ListenAuto bool `yaml:"-"`
	ListenPort int  `yaml:"listen_port"`
	// AllowedIPs is the source-address allowlist for every HTTP/WebSocket
	// request. Entries are plain IPs (10.0.0.5) or CIDRs (10.0.0.0/24).
	// Empty or wildcard entries are rejected by Validate; the agent fails
	// closed rather than accepting every client.
	AllowedIPs       []string       `yaml:"allowed_ips"`
	WorkDir          string         `yaml:"work_dir"`
	LogDir           string         `yaml:"log_dir"`
	MaxConcurrent    int            `yaml:"max_concurrent"`
	MaxOutputBytes   int64          `yaml:"max_output_bytes"`
	MaxScriptBytes   int            `yaml:"max_script_bytes"`
	DefaultTimeoutMS int64          `yaml:"default_timeout_ms"`
	MaxTimeoutMS     int64          `yaml:"max_timeout_ms"`
	TaskRetentionSec int            `yaml:"task_retention_sec"`
	Terminal         TerminalConfig `yaml:"terminal"`
	// UpdateBaseURL points at the artifact prefix that serves the agent
	// tarballs (and optionally manifest.json / SHA256SUMS). Empty disables
	// self-update. The bucket is scanned with ListObjectsV2, so builds are
	// discovered by filename: wxgzt-ops-agent-<version>-linux-<arch>.tar.gz.
	UpdateBaseURL string `yaml:"update_base_url"`
	// UpdateRequireChecksum refuses to install a release whose sha256 is not
	// published in manifest.json or SHA256SUMS. Off by default so that
	// dropping a correctly named tarball into the bucket is enough.
	UpdateRequireChecksum bool `yaml:"update_require_checksum"`
}

func Default() *Config {
	return &Config{
		ListenAddr:       "auto",
		ListenPort:       19090,
		AllowedIPs:       []string{"127.0.0.1"},
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
			// Keep interactive-session commands out of the target user's
			// ~/.bash_history by default. Set "inherit" to behave like a
			// normal login shell instead.
			HistoryMode: "memory",
		},
		UpdateBaseURL: "http://127.0.0.1:9000/wxgzt-ops",
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
	if v := os.Getenv("AGENT_ALLOWED_IPS"); v != "" {
		cfg.AllowedIPs = splitList(v)
	}
	if v := os.Getenv("AGENT_LOG_DIR"); v != "" {
		cfg.LogDir = v
	}
	if v, ok := os.LookupEnv("AGENT_UPDATE_BASE_URL"); ok {
		cfg.UpdateBaseURL = v
	}
	if err := cfg.resolveListenAddr(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolveListenAddr turns "auto" (or an empty value) into a concrete local IP.
// Auto-detection keeps the agent off 0.0.0.0 while allowing one agent.yaml to
// be deployed unchanged on every host.
func (c *Config) resolveListenAddr() error {
	s := strings.TrimSpace(c.ListenAddr)
	if s != "" && !strings.EqualFold(s, "auto") {
		return nil
	}
	ip, err := detectListenAddr()
	if err != nil {
		return err
	}
	c.ListenAddr = ip
	c.ListenAuto = true
	return nil
}

// detectListenAddr picks a non-loopback IPv4 for listen_addr: auto. It prefers
// the source address the kernel would use for outbound traffic when that is a
// private (RFC1918) address, then any private address on a physical interface.
// Hosts with several management networks should still set listen_addr
// explicitly.
func detectListenAddr() (string, error) {
	routeIP := routeSourceIPv4()
	if routeIP != nil && routeIP.IsPrivate() {
		return routeIP.String(), nil
	}
	if ip := firstInterfaceIPv4(true); ip != nil {
		return ip.String(), nil
	}
	if routeIP != nil {
		return routeIP.String(), nil
	}
	if ip := firstInterfaceIPv4(false); ip != nil {
		return ip.String(), nil
	}
	return "", fmt.Errorf("listen_addr: auto found no non-loopback IPv4 address; set listen_addr explicitly")
}

// routeSourceIPv4 asks the kernel which source address it would use for an
// outbound flow. A UDP "dial" selects the route but sends no packets, and
// TEST-NET-2 is never routed.
func routeSourceIPv4() net.IP {
	conn, err := net.Dial("udp4", "198.51.100.1:9")
	if err != nil {
		return nil
	}
	defer conn.Close()
	ua, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil
	}
	ip := ua.IP.To4()
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return nil
	}
	return ip
}

// isVirtualIface skips container/bridge/tunnel interfaces so auto-detection
// does not bind docker0 or a veth address.
func isVirtualIface(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{
		"lo", "docker", "veth", "br-", "virbr", "tun", "tap",
		"cni", "flannel", "kube", "podman", "cali",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// firstInterfaceIPv4 returns the first usable IPv4 on a physical interface.
// privateOnly restricts it to RFC1918 addresses.
func firstInterfaceIPv4(privateOnly bool) net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var public net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualIface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			if ip4.IsPrivate() {
				return ip4
			}
			if public == nil {
				public = ip4
			}
		}
	}
	if privateOnly {
		return nil
	}
	return public
}

func splitList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isWildcard reports addresses that would allow every client. These must never
// appear in allowed_ips or as the bind address.
func isWildcard(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "*", "0.0.0.0", "::", "0.0.0.0/0", "::/0":
		return true
	}
	return false
}

// normalizeAllowedIP validates one allowlist entry and returns a canonical
// form. Bare addresses become /32 (IPv4) or /128 (IPv6).
func normalizeAllowedIP(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if isWildcard(s) {
		return "", fmt.Errorf("allowed_ips entry %q would allow every client; list explicit backend IPs/CIDRs", raw)
	}
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return "", fmt.Errorf("allowed_ips entry %q is not a valid CIDR", raw)
		}
		p = p.Masked()
		if p.Bits() == 0 {
			return "", fmt.Errorf("allowed_ips entry %q would allow every client", raw)
		}
		return p.String(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", fmt.Errorf("allowed_ips entry %q is not a valid IP address", raw)
	}
	a = a.Unmap()
	return a.String(), nil
}

func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if isWildcard(c.ListenAddr) {
		return fmt.Errorf("listen_addr must be a concrete interface address, not %q; bind to the host's management IP (or 127.0.0.1)", c.ListenAddr)
	}
	if _, err := netip.ParseAddr(strings.TrimSpace(c.ListenAddr)); err != nil {
		return fmt.Errorf("listen_addr %q is not a valid IP address", c.ListenAddr)
	}
	if c.ListenPort < 1 || c.ListenPort > 65535 {
		return fmt.Errorf("invalid listen_port")
	}
	if len(c.AllowedIPs) == 0 {
		return fmt.Errorf("allowed_ips is required: list the backend host IP(s) permitted to reach the agent")
	}
	normalized := make([]string, 0, len(c.AllowedIPs))
	for _, raw := range c.AllowedIPs {
		v, err := normalizeAllowedIP(raw)
		if err != nil {
			return err
		}
		normalized = append(normalized, v)
	}
	c.AllowedIPs = normalized
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
	if c.Terminal.HistoryMode == "" {
		c.Terminal.HistoryMode = "memory"
	}
	if c.Terminal.HistoryMode != "inherit" && c.Terminal.HistoryMode != "memory" {
		return fmt.Errorf("terminal.history_mode must be \"inherit\" or \"memory\"")
	}
	if c.Terminal.User != "" && c.Terminal.User != "root" {
		return fmt.Errorf("terminal.user must be empty or \"root\"")
	}
	return nil
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.ListenAddr, c.ListenPort)
}
