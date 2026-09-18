package daemon

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// The daemon binds 127.0.0.1 by default, and a loopback address is not
// reachable from the LAN. Advertising it broadcast the machine's hostname to
// every network the operator joined, in exchange for a record pointing at an
// address only that machine can use.
func TestMDNSSkippedOnALoopbackBind(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:7878", "[::1]:7878"} {
		d := mdnsDecision(config.MDNSConfig{}, addr)
		if d.Advertise {
			t.Errorf("%s: advertised a loopback bind", addr)
		}
		if d.Reason == "" {
			t.Errorf("%s: want a logged reason", addr)
		}
	}
}

// Binding a LAN address is the case the feature exists for: there is
// something for a peer to reach, so tokenops.local earns its keep.
func TestMDNSAdvertisesALANBind(t *testing.T) {
	if d := mdnsDecision(config.MDNSConfig{}, "0.0.0.0:7878"); !d.Advertise {
		t.Fatalf("a wildcard bind should advertise: %+v", d)
	}
	if d := mdnsDecision(config.MDNSConfig{}, "192.168.1.20:7878"); !d.Advertise {
		t.Fatalf("a LAN bind should advertise: %+v", d)
	}
}

// Explicit config outranks the heuristic in both directions.
func TestMDNSConfigOverridesTheHeuristic(t *testing.T) {
	on := true
	if d := mdnsDecision(config.MDNSConfig{Enabled: &on}, "127.0.0.1:7878"); !d.Advertise {
		t.Error("enabled: true should advertise even on loopback")
	}
	off := false
	if d := mdnsDecision(config.MDNSConfig{Enabled: &off}, "0.0.0.0:7878"); d.Advertise {
		t.Error("enabled: false must never advertise")
	}
}

// The instance name carries the machine's hostname. An operator who wants
// the convenience without the name can supply their own.
func TestMDNSInstanceNameCanBeOverridden(t *testing.T) {
	d := mdnsDecision(config.MDNSConfig{InstanceName: "workstation"}, "0.0.0.0:7878")
	if !d.Advertise {
		t.Fatalf("want advertise: %+v", d)
	}
	if d.InstanceName != "workstation" {
		t.Fatalf("InstanceName = %q, want the configured name", d.InstanceName)
	}
}

// The TXT record hardcoded version=v0.10.0 and had been broadcasting it for
// forty-eight releases.
func TestMDNSTXTCarriesTheRunningVersion(t *testing.T) {
	txt := mdnsTXT()
	var ver string
	for _, entry := range txt {
		if strings.HasPrefix(entry, "version=") {
			ver = strings.TrimPrefix(entry, "version=")
		}
	}
	if ver == "" {
		t.Fatal("no version in the TXT record")
	}
	if ver == "v0.10.0" {
		t.Fatal("still advertising the hardcoded v0.10.0")
	}
}
