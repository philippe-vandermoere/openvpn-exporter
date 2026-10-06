package collector

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func writeTempCert(t *testing.T, dir, name, commonName string, notAfter time.Time) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, name), pemBytes, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func writeTunnelConfig(t *testing.T, dir string) string {
	t.Helper()
	writeTempCert(t, dir, "ca.crt", "Test CA", time.Now().Add(365*24*time.Hour))
	writeTempCert(t, dir, "client.crt", "Test Client", time.Now().Add(30*24*time.Hour))
	configPath := filepath.Join(dir, "client.conf")
	content := "client\nca ca.crt\ncert client.crt\nkey client.key\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return configPath
}

func TestCollector_CertPathOnly(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	writeTempCert(t, dir, "tls.crt", "Direct Client", time.Now().Add(30*24*time.Hour))
	addr := startHealthyManagementServer(t)

	c := New([]Tunnel{{
		Name:              "office",
		ManagementAddress: addr,
		CertPath:          certPath,
		Timeout:           2 * time.Second,
	}}, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="Direct Client",tunnel="office"}`)
	assertNotContains(t, metrics, `role="ca"`)
}

func TestCollector_ConfigPathTakesPrecedenceOverCertPath(t *testing.T) {
	dir := t.TempDir()
	configPath := writeTunnelConfig(t, dir) // "Test CA" + "Test Client"

	otherDir := t.TempDir()
	otherCertPath := filepath.Join(otherDir, "tls.crt")
	writeTempCert(t, otherDir, "tls.crt", "Should Not Be Used", time.Now().Add(30*24*time.Hour))

	addr := startHealthyManagementServer(t)

	c := New([]Tunnel{{
		Name:              "office",
		ManagementAddress: addr,
		ConfigPath:        configPath,
		CertPath:          otherCertPath,
		Timeout:           2 * time.Second,
	}}, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="Test CA",tunnel="office"}`)
	assertContains(t, metrics, `openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="Test Client",tunnel="office"}`)
	assertNotContains(t, metrics, "Should Not Be Used")
}

func TestCollector_NoCertSourceConfigured(t *testing.T) {
	addr := startHealthyManagementServer(t)

	c := New([]Tunnel{{
		Name:              "office",
		ManagementAddress: addr,
		Timeout:           2 * time.Second,
	}}, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_tunnel_up{tunnel="office"} 1`)
	assertNotContains(t, metrics, "openvpn_tunnel_cert_expiry_timestamp_seconds{")
}

func TestCollector_ConfigPathPSKHasNoCertMetrics(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "client.conf")
	// A pre-shared-key (static key) tunnel has no ca/cert to find: this must
	// not be treated as an error, just as "nothing to report".
	content := "dev tun\nremote vpn.example.com 1194\nsecret static.key\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	addr := startHealthyManagementServer(t)

	c := New([]Tunnel{{
		Name:              "office",
		ManagementAddress: addr,
		ConfigPath:        configPath,
		Timeout:           2 * time.Second,
	}}, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_tunnel_up{tunnel="office"} 1`)
	assertNotContains(t, metrics, "openvpn_tunnel_cert_expiry_timestamp_seconds{")
}

// startHealthyManagementServer runs a minimal, unprotected fake OpenVPN
// management interface that always reports CONNECTED with fixed counters.
func startHealthyManagementServer(t *testing.T) string {
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
				if _, err := r.ReadString('\n'); err != nil { // "state"
					return
				}
				_, _ = conn.Write([]byte("1700000000,CONNECTED,SUCCESS,10.8.0.2,203.0.113.5,1194,,\r\nEND\r\n"))
				if _, err := r.ReadString('\n'); err != nil { // "status"
					return
				}
				_, _ = conn.Write([]byte(
					"TUN/TAP read bytes,111\r\n" +
						"TUN/TAP write bytes,222\r\n" +
						"TCP/UDP read bytes,333\r\n" +
						"TCP/UDP write bytes,444\r\n" +
						"END\r\n",
				))
			}()
		}
	}()

	return ln.Addr().String()
}

func TestCollector_HealthyTunnel(t *testing.T) {
	dir := t.TempDir()
	configPath := writeTunnelConfig(t, dir)
	addr := startHealthyManagementServer(t)

	c := New([]Tunnel{{
		Name:              "office",
		ManagementAddress: addr,
		ConfigPath:        configPath,
		Timeout:           2 * time.Second,
	}}, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_tunnel_up{tunnel="office"} 1`)
	assertContains(t, metrics, `openvpn_tunnel_state{state="CONNECTED",tunnel="office"} 1`)
	assertContains(t, metrics, `openvpn_tunnel_bytes_total{channel="tunnel",direction="in",tunnel="office"} 111`)
	assertContains(t, metrics, `openvpn_tunnel_bytes_total{channel="tunnel",direction="out",tunnel="office"} 222`)
	assertContains(t, metrics, `openvpn_tunnel_bytes_total{channel="link",direction="in",tunnel="office"} 333`)
	assertContains(t, metrics, `openvpn_tunnel_bytes_total{channel="link",direction="out",tunnel="office"} 444`)
	assertContains(t, metrics, `openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="Test CA",tunnel="office"}`)
	assertContains(t, metrics, `openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="Test Client",tunnel="office"}`)
	assertContains(t, metrics, `openvpn_tunnel_scrape_duration_seconds{tunnel="office"}`)
}

func TestCollector_DownTunnel(t *testing.T) {
	dir := t.TempDir()
	configPath := writeTunnelConfig(t, dir)

	// Nothing listens here: the management interface is unreachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := New([]Tunnel{{
		Name:              "office",
		ManagementAddress: addr,
		ConfigPath:        configPath,
		Timeout:           500 * time.Millisecond,
	}}, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_tunnel_up{tunnel="office"} 0`)
	assertContains(t, metrics, `openvpn_tunnel_scrape_duration_seconds{tunnel="office"}`)
	// Certificate metrics are independent of the management interface.
	assertContains(t, metrics, `openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="Test CA",tunnel="office"}`)

	// No partial series: state and byte counters must be entirely absent.
	assertNotContains(t, metrics, "openvpn_tunnel_state{")
	assertNotContains(t, metrics, "openvpn_tunnel_bytes_total{")
}

func TestCollector_MultipleTunnelsScrapedConcurrently(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	configPath1 := writeTunnelConfig(t, dir1)
	configPath2 := writeTunnelConfig(t, dir2)
	addr := startHealthyManagementServer(t)

	c := New([]Tunnel{
		{Name: "office", ManagementAddress: addr, ConfigPath: configPath1, Timeout: 2 * time.Second},
		{Name: "backup", ManagementAddress: addr, ConfigPath: configPath2, Timeout: 2 * time.Second},
	}, nil)

	metrics, err := gather(c)
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	assertContains(t, metrics, `openvpn_tunnel_up{tunnel="office"} 1`)
	assertContains(t, metrics, `openvpn_tunnel_up{tunnel="backup"} 1`)
}

// gather registers c on a fresh registry, serves it over HTTP exactly like
// the real exporter does, and returns the scraped body as text.
func gather(c *Collector) (string, error) {
	registry := prometheus.NewRegistry()
	if err := registry.Register(c); err != nil {
		return "", err
	}

	server := httptest.NewServer(promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected metrics output to contain %q\nfull output:\n%s", needle, haystack)
	}
}

func assertNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("expected metrics output to NOT contain %q\nfull output:\n%s", needle, haystack)
	}
}
