package daemon

import (
	"slices"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

func TestProxyAllowedHosts(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name     string
		cfg      config.Config
		hostname string
		want     []string
		absent   []string
	}{
		{
			name:   "loopback default admits nothing extra",
			cfg:    config.Config{Listen: "127.0.0.1:7878"},
			absent: []string{"tokenops.local", "laptop", "laptop.local"},
		},
		{
			name:     "loopback with mdns forced on admits tokenops.local only",
			cfg:      config.Config{Listen: "127.0.0.1:7878", MDNS: config.MDNSConfig{Enabled: &on}},
			hostname: "Laptop",
			want:     []string{"tokenops.local"},
			absent:   []string{"laptop", "laptop.local"},
		},
		{
			name:     "lan bind admits mdns name and machine names",
			cfg:      config.Config{Listen: "0.0.0.0:7878"},
			hostname: "Laptop.local",
			want:     []string{"tokenops.local", "laptop", "laptop.local"},
		},
		{
			name:     "lan bind with mdns off admits machine names only",
			cfg:      config.Config{Listen: "192.168.1.5:7878", MDNS: config.MDNSConfig{Enabled: &off}},
			hostname: "laptop",
			want:     []string{"laptop", "laptop.local"},
			absent:   []string{"tokenops.local"},
		},
		{
			name: "tls hostnames and allowed_hosts pass through",
			cfg: config.Config{
				Listen:       "127.0.0.1:7878",
				TLS:          config.TLSConfig{Hostnames: []string{"tokenops.internal"}},
				AllowedHosts: []string{"proxy.example:8443"},
			},
			want: []string{"tokenops.internal", "proxy.example:8443"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proxyAllowedHosts(tt.cfg, tt.hostname)
			for _, w := range tt.want {
				if !slices.Contains(got, w) {
					t.Errorf("proxyAllowedHosts = %q, missing %q", got, w)
				}
			}
			for _, a := range tt.absent {
				if slices.Contains(got, a) {
					t.Errorf("proxyAllowedHosts = %q, must not contain %q", got, a)
				}
			}
		})
	}
}
