package collector

import (
	"bufio"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pvandermoere/openvpn-exporter/internal/openvpn"
)

// startHealthyServerManagementServer runs a minimal, unprotected fake
// OpenVPN *server* management interface reporting two connected clients.
func startHealthyServerManagementServer(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
				r := bufio.NewReader(conn)
				if _, err := r.ReadString('\n'); err != nil { // "status 3"
					return
				}
				_, _ = conn.Write([]byte(
					"TITLE\tOpenVPN 2.6.20\r\n" +
						"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher\r\n" +
						"CLIENT_LIST\topenvpn_26\t172.18.0.5:51607\t10.8.0.14\t\t111\t222\t2026-10-09 16:22:44\t1700000000\tUNDEF\t2\t2\tAES-256-GCM\r\n" +
						"CLIENT_LIST\topenvpn_25\t172.18.0.3:49964\t10.8.0.6\t\t333\t444\t2026-10-09 16:22:43\t1700000001\tUNDEF\t0\t0\tAES-256-GCM\r\n" +
						"END\r\n",
				))
				if _, err := r.ReadString('\n'); err != nil { // "version"
					return
				}
				_, _ = conn.Write([]byte(
					"OpenVPN Version: OpenVPN 2.6.20 x86_64-alpine-linux-musl [SSL (OpenSSL)] [LZO] [LZ4] [EPOLL] [MH/PKTINFO] [AEAD]\r\n" +
						"Management Version: 5\r\n" +
						"END\r\n",
				))
			}()
		}
	}()

	return ln.Addr().String()
}

func TestServerCollector_HealthyServer(t *testing.T) {
	addr := startHealthyServerManagementServer(t)

	c := NewCollector([]openvpn.Target{{
		Name:              "vpn-gw",
		Mode:              openvpn.ModeServer,
		ManagementAddress: addr,
	}}, 2*time.Second, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_server_up{server="vpn-gw"} 1`)
	assertContains(t, metrics, `openvpn_server_clients_connected{server="vpn-gw"} 2`)
	assertContains(t, metrics, `openvpn_server_info{server="vpn-gw",version="2.6.20"} 1`)
	assertContains(t, metrics, `openvpn_server_scrape_duration_seconds{server="vpn-gw"}`)

	assertContains(t, metrics, `openvpn_server_client_info{cipher="AES-256-GCM",common_name="openvpn_26",real_address="172.18.0.5:51607",server="vpn-gw",username="UNDEF",virtual_address="10.8.0.14"} 1`)
	assertContains(t, metrics, `openvpn_server_client_bytes_total{common_name="openvpn_26",direction="in",server="vpn-gw"} 111`)
	assertContains(t, metrics, `openvpn_server_client_bytes_total{common_name="openvpn_26",direction="out",server="vpn-gw"} 222`)
	assertContains(t, metrics, `openvpn_server_client_connected_since_timestamp_seconds{common_name="openvpn_26",server="vpn-gw"} 1.7e+09`)

	assertContains(t, metrics, `openvpn_server_client_info{cipher="AES-256-GCM",common_name="openvpn_25",real_address="172.18.0.3:49964",server="vpn-gw",username="UNDEF",virtual_address="10.8.0.6"} 1`)
	assertContains(t, metrics, `openvpn_server_client_bytes_total{common_name="openvpn_25",direction="in",server="vpn-gw"} 333`)
	assertContains(t, metrics, `openvpn_server_client_bytes_total{common_name="openvpn_25",direction="out",server="vpn-gw"} 444`)
}

func TestServerCollector_NoClientsConnected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
		r := bufio.NewReader(conn)
		if _, err := r.ReadString('\n'); err != nil { // "status 3"
			return
		}
		_, _ = conn.Write([]byte(
			"TITLE\tOpenVPN 2.6.20\r\n" +
				"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher\r\n" +
				"END\r\n",
		))
		if _, err := r.ReadString('\n'); err != nil { // "version"
			return
		}
		_, _ = conn.Write([]byte(
			"OpenVPN Version: OpenVPN 2.6.20 x86_64-alpine-linux-musl [SSL (OpenSSL)]\r\n" +
				"Management Version: 5\r\n" +
				"END\r\n",
		))
	}()

	c := NewCollector([]openvpn.Target{{
		Name:              "vpn-gw",
		Mode:              openvpn.ModeServer,
		ManagementAddress: ln.Addr().String(),
	}}, 2*time.Second, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_server_up{server="vpn-gw"} 1`)
	assertContains(t, metrics, `openvpn_server_clients_connected{server="vpn-gw"} 0`)
	assertNotContains(t, metrics, "openvpn_server_client_info{")
	assertNotContains(t, metrics, "openvpn_server_client_bytes_total{")
	assertNotContains(t, metrics, "openvpn_server_client_connected_since_timestamp_seconds{")
}

func TestServerCollector_DownServer(t *testing.T) {
	// Nothing listens here: the management interface is unreachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := NewCollector([]openvpn.Target{{
		Name:              "vpn-gw",
		Mode:              openvpn.ModeServer,
		ManagementAddress: addr,
	}}, 500*time.Millisecond, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_server_up{server="vpn-gw"} 0`)
	assertContains(t, metrics, `openvpn_server_scrape_duration_seconds{server="vpn-gw"}`)

	// No partial series at all when the scrape itself fails.
	assertNotContains(t, metrics, "openvpn_server_clients_connected{")
	assertNotContains(t, metrics, "openvpn_server_client_info{")
	assertNotContains(t, metrics, "openvpn_server_client_bytes_total{")
	assertNotContains(t, metrics, "openvpn_server_info{")
}

// startServerManagementServerWithBadVersion behaves like
// startHealthyServerManagementServer except the "version" command always
// fails, simulating a persistently old/non-conforming management API.
func startServerManagementServerWithBadVersion(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
				r := bufio.NewReader(conn)
				if _, err := r.ReadString('\n'); err != nil { // "status 3"
					return
				}
				_, _ = conn.Write([]byte(
					"TITLE\tOpenVPN 2.6.20\r\n" +
						"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher\r\n" +
						"END\r\n",
				))
				if _, err := r.ReadString('\n'); err != nil { // "version"
					return
				}
				_, _ = conn.Write([]byte("ERROR: unknown command\r\n"))
			}()
		}
	}()

	return ln.Addr().String()
}

func TestServerCollector_VersionFailureWarnsOnlyOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
				r := bufio.NewReader(conn)
				if _, err := r.ReadString('\n'); err != nil { // "status 3"
					return
				}
				_, _ = conn.Write([]byte(
					"TITLE\tOpenVPN 2.6.20\r\n" +
						"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher\r\n" +
						"END\r\n",
				))
				if _, err := r.ReadString('\n'); err != nil { // "version"
					return
				}
				_, _ = conn.Write([]byte("ERROR: unknown command\r\n"))
			}()
		}
	}()

	handler := &countingHandler{}
	c := NewCollector([]openvpn.Target{{
		Name:              "vpn-gw",
		Mode:              openvpn.ModeServer,
		ManagementAddress: ln.Addr().String(),
	}}, 2*time.Second, slog.New(handler))

	if _, err := gather(c); err != nil {
		t.Fatalf("gathering metrics (1st scrape): %v", err)
	}
	if _, err := gather(c); err != nil {
		t.Fatalf("gathering metrics (2nd scrape): %v", err)
	}

	if got := handler.Count(); got != 1 {
		t.Errorf("logger received %d warnings across 2 scrapes, want exactly 1", got)
	}
}

func writeServerConfig(t *testing.T, dir string) string {
	t.Helper()
	writeTempCert(t, dir, "ca.crt", "Test CA", time.Now().Add(365*24*time.Hour))
	writeTempCert(t, dir, "server.crt", "Test Server", time.Now().Add(30*24*time.Hour))
	configPath := filepath.Join(dir, "server.conf")
	content := "server 10.8.0.0 255.255.255.0\nca ca.crt\ncert server.crt\nkey server.key\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return configPath
}

func TestServerCollector_CertExpiryViaConfigPath(t *testing.T) {
	dir := t.TempDir()
	configPath := writeServerConfig(t, dir)
	addr := startHealthyServerManagementServer(t)

	c := NewCollector([]openvpn.Target{{
		Name:              "vpn-gw",
		Mode:              openvpn.ModeServer,
		ManagementAddress: addr,
		ConfigPath:        configPath,
	}}, 2*time.Second, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_server_cert_expiry_timestamp_seconds{role="ca",server="vpn-gw",subject="Test CA"}`)
	assertContains(t, metrics, `openvpn_server_cert_expiry_timestamp_seconds{role="client",server="vpn-gw",subject="Test Server"}`)
}

func TestServerCollector_CertExpiryEmittedEvenWhenServerDown(t *testing.T) {
	dir := t.TempDir()
	configPath := writeServerConfig(t, dir)

	// Nothing listens here: the management interface is unreachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := NewCollector([]openvpn.Target{{
		Name:              "vpn-gw",
		Mode:              openvpn.ModeServer,
		ManagementAddress: addr,
		ConfigPath:        configPath,
	}}, 500*time.Millisecond, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_server_up{server="vpn-gw"} 0`)
	// Certificate metrics are independent of the management interface.
	assertContains(t, metrics, `openvpn_server_cert_expiry_timestamp_seconds{role="ca",server="vpn-gw",subject="Test CA"}`)
	assertContains(t, metrics, `openvpn_server_cert_expiry_timestamp_seconds{role="client",server="vpn-gw",subject="Test Server"}`)
}

func TestServerCollector_NoCertSourceConfigured(t *testing.T) {
	addr := startHealthyServerManagementServer(t)

	c := NewCollector([]openvpn.Target{{
		Name:              "vpn-gw",
		Mode:              openvpn.ModeServer,
		ManagementAddress: addr,
	}}, 2*time.Second, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_server_up{server="vpn-gw"} 1`)
	assertNotContains(t, metrics, "openvpn_server_cert_expiry_timestamp_seconds{")
}
