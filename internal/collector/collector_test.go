package collector

import (
	"log/slog"
	"testing"
	"time"

	"github.com/pvandermoere/openvpn-exporter/internal/openvpn"
)

// TestCollector_DispatchesOnMode scrapes a tunnel and a server from a
// single Collector in one Collect call, proving targets are routed to
// collectTunnel/collectServer by Mode rather than by which collector they
// were registered with (there's only one Collector now).
func TestCollector_DispatchesOnMode(t *testing.T) {
	tunnelAddr := startHealthyManagementServer(t)
	serverAddr := startHealthyServerManagementServer(t)

	c := NewCollector([]openvpn.Target{
		{Name: "shared", Mode: openvpn.ModeClient, ManagementAddress: tunnelAddr},
		{Name: "shared", Mode: openvpn.ModeServer, ManagementAddress: serverAddr},
	}, 2*time.Second, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_tunnel_up{tunnel="shared"} 1`)
	assertContains(t, metrics, `openvpn_server_up{server="shared"} 1`)
}

// TestCollector_VersionWarningsAreIndependentPerMode proves the composite
// "mode:name" key used by versionWarned: a tunnel and a server sharing a
// name (independent namespaces, see internal/config.validate) must each
// get their own "version unavailable" warning, instead of one clearing or
// suppressing the other's.
func TestCollector_VersionWarningsAreIndependentPerMode(t *testing.T) {
	tunnelAddr := startManagementServerWithBadVersion(t)
	serverAddr := startServerManagementServerWithBadVersion(t)

	handler := &countingHandler{}
	c := NewCollector([]openvpn.Target{
		{Name: "shared", Mode: openvpn.ModeClient, ManagementAddress: tunnelAddr},
		{Name: "shared", Mode: openvpn.ModeServer, ManagementAddress: serverAddr},
	}, 2*time.Second, slog.New(handler))

	if _, err := gather(c); err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	// Both the tunnel's and the server's "version" failure must be logged:
	// a shared key would have let the second overwrite/suppress the first.
	if got := handler.Count(); got != 2 {
		t.Errorf("logger received %d warnings, want exactly 2 (one per mode)", got)
	}
}
