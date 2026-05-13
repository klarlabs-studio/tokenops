package plans

import "testing"

func TestClassifySignalNoObservations(t *testing.T) {
	q := ClassifySignal(SignalInputs{})
	if q.Level != SignalLevelLow {
		t.Errorf("level=%q want low", q.Level)
	}
	if q.Source != SignalSourceMCPPings {
		t.Errorf("source=%q want mcp_tool_pings", q.Source)
	}
	if q.Caveat == "" {
		t.Error("low-quality signal must carry a caveat so callers can render it")
	}
}

func TestClassifySignalMCPPingsOnly(t *testing.T) {
	q := ClassifySignal(SignalInputs{MCPPingsInWindow: 12})
	if q.Level != SignalLevelLow {
		t.Errorf("mcp-only must stay low, got %q", q.Level)
	}
}

func TestClassifySignalProxyDominant(t *testing.T) {
	q := ClassifySignal(SignalInputs{ProxyEventsInWindow: 50, MCPPingsInWindow: 5})
	if q.Level != SignalLevelHigh {
		t.Errorf("proxy-dominant must be high, got %q", q.Level)
	}
	if q.Source != SignalSourceProxy {
		t.Errorf("source=%q want proxy_traffic", q.Source)
	}
}

func TestClassifySignalProxyPartial(t *testing.T) {
	q := ClassifySignal(SignalInputs{ProxyEventsInWindow: 5, MCPPingsInWindow: 50})
	if q.Level != SignalLevelMedium {
		t.Errorf("partial coverage must be medium, got %q", q.Level)
	}
	if len(q.UpgradePaths) == 0 {
		t.Error("medium quality must suggest upgrade paths")
	}
}

func TestClassifySignalVendorWiredTrumpsAll(t *testing.T) {
	q := ClassifySignal(SignalInputs{
		ProxyEventsInWindow: 1000, MCPPingsInWindow: 0, VendorAPIWired: true,
	})
	if q.Level != SignalLevelHigh {
		t.Errorf("vendor wired must be high, got %q", q.Level)
	}
	if q.Source != SignalSourceVendorAPI {
		t.Errorf("source=%q want vendor_usage_api", q.Source)
	}
}
