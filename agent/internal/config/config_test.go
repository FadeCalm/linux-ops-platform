package config

import "testing"

func TestValidateRejectsWildcards(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
	}{
		{"bind all v4", &Config{ListenAddr: "0.0.0.0", ListenPort: 19090, AllowedIPs: []string{"10.0.0.5"}}},
		{"bind all v6", &Config{ListenAddr: "::", ListenPort: 19090, AllowedIPs: []string{"10.0.0.5"}}},
		{"empty allowlist", &Config{ListenAddr: "10.0.0.11", ListenPort: 19090}},
		{"wildcard allowlist", &Config{ListenAddr: "10.0.0.11", ListenPort: 19090, AllowedIPs: []string{"0.0.0.0"}}},
		{"wildcard cidr", &Config{ListenAddr: "10.0.0.11", ListenPort: 19090, AllowedIPs: []string{"0.0.0.0/0"}}},
		{"bad cidr", &Config{ListenAddr: "10.0.0.11", ListenPort: 19090, AllowedIPs: []string{"10.0.0.0/33"}}},
	}
	for _, tc := range cases {
		if err := tc.cfg.Validate(); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
}

func TestShippedConfigLoads(t *testing.T) {
	cfg, err := Load("../../../configs/agent.yaml")
	if err != nil {
		t.Fatalf("Load shipped config: %v", err)
	}
	if len(cfg.AllowedIPs) == 0 {
		t.Fatal("shipped config has no allowed_ips")
	}
}

func TestValidateNormalizesAllowlist(t *testing.T) {
	c := &Config{
		ListenAddr: "10.0.0.11",
		ListenPort: 19090,
		AllowedIPs: []string{" 10.0.0.5 ", "192.168.1.9/24", "fd00::5"},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := []string{"10.0.0.5", "192.168.1.0/24", "fd00::5"}
	for i := range want {
		if c.AllowedIPs[i] != want[i] {
			t.Fatalf("AllowedIPs[%d] = %q, want %q", i, c.AllowedIPs[i], want[i])
		}
	}
}
