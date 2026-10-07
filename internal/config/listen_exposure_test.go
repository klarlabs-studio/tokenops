package config

import (
	"strings"
	"testing"
)

func TestListenIsLoopback(t *testing.T) {
	tests := []struct {
		listen string
		want   bool
	}{
		{"127.0.0.1:7878", true},
		{"127.9.9.9:7878", true},
		{"[::1]:7878", true},
		{"localhost:7878", true},
		{"LocalHost:7878", true},
		{"0.0.0.0:7878", false},
		{"[::]:7878", false},
		{":7878", false},
		{"192.168.1.5:7878", false},
		{"box.lan:7878", false},
	}
	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			if got := (Config{Listen: tt.listen}).ListenIsLoopback(); got != tt.want {
				t.Errorf("ListenIsLoopback(%q) = %v, want %v", tt.listen, got, tt.want)
			}
		})
	}
}

func TestListenWarnings(t *testing.T) {
	tests := []struct {
		name     string
		listen   string
		tls      bool
		wantWarn bool
	}{
		{"loopback plain", "127.0.0.1:7878", false, false},
		{"loopback tls", "127.0.0.1:7878", true, false},
		{"wildcard plain", "0.0.0.0:7878", false, true},
		{"empty host plain", ":7878", false, true},
		{"lan ip plain", "192.168.1.5:7878", false, true},
		{"lan ip tls", "192.168.1.5:7878", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Listen: tt.listen, TLS: TLSConfig{Enabled: tt.tls}}
			got := cfg.ListenWarnings()
			if (len(got) > 0) != tt.wantWarn {
				t.Fatalf("ListenWarnings() = %q, want warning: %v", got, tt.wantWarn)
			}
			if tt.wantWarn && !strings.Contains(got[0], tt.listen) {
				t.Errorf("warning %q does not name the listen address %q", got[0], tt.listen)
			}
		})
	}
}

func TestValidateAllowedHosts(t *testing.T) {
	tests := []struct {
		name    string
		hosts   []string
		wantErr bool
	}{
		{"none", nil, false},
		{"names and ips", []string{"tokenops.lan", "box.lan:7878", "10.0.0.2", "[fd00::1]"}, false},
		{"empty entry", []string{""}, true},
		{"url not host", []string{"http://box.lan"}, true},
		{"path", []string{"box.lan/x"}, true},
		{"wildcard", []string{"*"}, true},
		{"whitespace", []string{"box lan"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.AllowedHosts = tt.hosts
			if err := cfg.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
