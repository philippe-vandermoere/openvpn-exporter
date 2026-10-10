package openvpn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generateCert returns a self-signed certificate PEM block for the given
// CommonName and expiry.
func generateCert(t *testing.T, commonName string, notAfter time.Time) string {
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

	buf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(buf)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.conf")
	writeFile(t, path, content)
	return path
}

// --- Certificate parsing (formerly certs_test.go, Load -> ParseConfig) ---

func TestParseConfig_FileReferencedCertificates(t *testing.T) {
	dir := t.TempDir()

	caExpiry := time.Now().Add(365 * 24 * time.Hour)
	clientExpiry := time.Now().Add(30 * 24 * time.Hour)

	writeFile(t, filepath.Join(dir, "ca.crt"), generateCert(t, "Test CA", caExpiry))
	writeFile(t, filepath.Join(dir, "client.crt"), generateCert(t, "Test Client", clientExpiry))

	configContent := "client\n" +
		"remote vpn.example.com 1194\n" +
		"ca ca.crt\n" +
		"cert client.crt\n" +
		"key client.key\n" // must never be opened; the file does not even exist.

	configPath := filepath.Join(dir, "client.conf")
	writeFile(t, configPath, configContent)

	got, err := ParseConfig(configPath)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(got.Certificates) != 2 {
		t.Fatalf("got %d certificates, want 2: %+v", len(got.Certificates), got.Certificates)
	}

	byRole := map[string]Certificate{}
	for _, c := range got.Certificates {
		byRole[c.Role] = c
	}

	ca, ok := byRole[roleCA]
	if !ok || ca.Subject != "Test CA" {
		t.Errorf("ca certificate: %+v", ca)
	}
	if !ca.NotAfter.Equal(caExpiry.Truncate(time.Second)) && ca.NotAfter.Sub(caExpiry).Abs() > time.Minute {
		t.Errorf("ca NotAfter = %v, want ~%v", ca.NotAfter, caExpiry)
	}

	client, ok := byRole[roleClient]
	if !ok || client.Subject != "Test Client" {
		t.Errorf("client certificate: %+v", client)
	}
}

func TestParseConfig_InlineCertificates(t *testing.T) {
	caExpiry := time.Now().Add(365 * 24 * time.Hour)
	clientExpiry := time.Now().Add(30 * 24 * time.Hour)

	configContent := "client\n" +
		"<ca>\n" + generateCert(t, "Inline CA", caExpiry) + "</ca>\n" +
		"<cert>\n" + generateCert(t, "Inline Client", clientExpiry) + "</cert>\n" +
		"<key>\n-----BEGIN PRIVATE KEY-----\nnot-a-real-key-and-never-parsed\n-----END PRIVATE KEY-----\n</key>\n"

	configPath := writeConfig(t, configContent)

	got, err := ParseConfig(configPath)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(got.Certificates) != 2 {
		t.Fatalf("got %d certificates, want 2: %+v", len(got.Certificates), got.Certificates)
	}
}

func TestParseConfig_CABundleWithIntermediate(t *testing.T) {
	dir := t.TempDir()

	root := generateCert(t, "Root CA", time.Now().Add(365*24*time.Hour))
	intermediate := generateCert(t, "Intermediate CA", time.Now().Add(200*24*time.Hour))

	writeFile(t, filepath.Join(dir, "ca.crt"), root+intermediate)
	configPath := filepath.Join(dir, "client.conf")
	writeFile(t, configPath, "ca ca.crt\n")

	got, err := ParseConfig(configPath)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(got.Certificates) != 2 {
		t.Fatalf("got %d certificates from bundle, want 2: %+v", len(got.Certificates), got.Certificates)
	}
	for _, c := range got.Certificates {
		if c.Role != roleCA {
			t.Errorf("certificate %q has role %q, want %q", c.Subject, c.Role, roleCA)
		}
	}
}

func TestParseConfig_RelativePathsResolvedAgainstConfigDir(t *testing.T) {
	configDir := t.TempDir()
	writeFile(t, filepath.Join(configDir, "ca.crt"), generateCert(t, "Relative CA", time.Now().Add(24*time.Hour)))
	configPath := filepath.Join(configDir, "client.conf")
	writeFile(t, configPath, "ca ca.crt\n")

	// Change the process working directory to something else entirely to
	// prove path resolution does not depend on it.
	otherDir := t.TempDir()
	t.Chdir(otherDir)

	got, err := ParseConfig(configPath)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(got.Certificates) != 1 || got.Certificates[0].Subject != "Relative CA" {
		t.Fatalf("got %+v, want a single 'Relative CA' certificate", got.Certificates)
	}
}

func TestParseConfig_NeverOpensKeyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ca.crt"), generateCert(t, "CA", time.Now().Add(24*time.Hour)))
	configPath := filepath.Join(dir, "client.conf")
	// "key" points to a file that does not exist: if the loader ever
	// tried to open it, ParseConfig would fail.
	writeFile(t, configPath, "ca ca.crt\nkey missing-and-must-not-be-opened.key\n")

	if _, err := ParseConfig(configPath); err != nil {
		t.Fatalf("ParseConfig should not fail on a missing key file: %v", err)
	}
}

func TestParseConfig_PSKConfigHasNoCertificates(t *testing.T) {
	// A pre-shared-key (static key) tunnel has no TLS handshake at all, so
	// there's no "ca"/"cert" directive to find — this must not be an error.
	configPath := writeConfig(t, "dev tun\nremote vpn.example.com 1194\nsecret static.key\n")

	got, err := ParseConfig(configPath)
	if err != nil {
		t.Fatalf("ParseConfig should not fail on a PSK config with no certificates: %v", err)
	}
	if len(got.Certificates) != 0 {
		t.Fatalf("expected no certificates, got %v", got.Certificates)
	}
}

func TestLoadCertOnly_SingleCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	writeFile(t, certPath, generateCert(t, "Direct Client", time.Now().Add(30*24*time.Hour)))

	got, err := LoadCertOnly(certPath)
	if err != nil {
		t.Fatalf("LoadCertOnly: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d certificates, want 1: %+v", len(got), got)
	}
	if got[0].Role != roleClient {
		t.Errorf("Role = %q, want %q", got[0].Role, roleClient)
	}
	if got[0].Subject != "Direct Client" {
		t.Errorf("Subject = %q, want %q", got[0].Subject, "Direct Client")
	}
}

func TestLoadCertOnly_Bundle(t *testing.T) {
	dir := t.TempDir()
	leaf := generateCert(t, "Leaf", time.Now().Add(30*24*time.Hour))
	intermediate := generateCert(t, "Intermediate", time.Now().Add(200*24*time.Hour))
	certPath := filepath.Join(dir, "tls.crt")
	writeFile(t, certPath, leaf+intermediate)

	got, err := LoadCertOnly(certPath)
	if err != nil {
		t.Fatalf("LoadCertOnly: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d certificates from bundle, want 2: %+v", len(got), got)
	}
}

func TestLoadCertOnly_MissingFile(t *testing.T) {
	if _, err := LoadCertOnly("/does/not/exist.crt"); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}

// --- Management directive parsing (formerly management_test.go, now folded
// into ParseConfig) ---

func TestParseConfig_ManagementTCP(t *testing.T) {
	path := writeConfig(t, "client\nmanagement 127.0.0.1 7505\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.ManagementAddress != "127.0.0.1:7505" {
		t.Errorf("ManagementAddress = %q, want %q", got.ManagementAddress, "127.0.0.1:7505")
	}
	if got.ManagementPassword != "" || got.ManagementPasswordFileErr != nil {
		t.Errorf("got password %q / err %v, want both zero", got.ManagementPassword, got.ManagementPasswordFileErr)
	}
}

func TestParseConfig_ManagementWithReadablePasswordFile(t *testing.T) {
	dir := t.TempDir()
	passwordFile := filepath.Join(dir, "mgmt.pass")
	writeFile(t, passwordFile, "s3cret\r\n")

	path := filepath.Join(dir, "client.conf")
	writeFile(t, path, "management 127.0.0.1 7505 "+passwordFile+"\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.ManagementPassword != "s3cret" {
		t.Errorf("ManagementPassword = %q, want %q", got.ManagementPassword, "s3cret")
	}
	if got.ManagementPasswordFileErr != nil {
		t.Errorf("ManagementPasswordFileErr = %v, want nil", got.ManagementPasswordFileErr)
	}
}

func TestParseConfig_ManagementWithUnreadablePasswordFile(t *testing.T) {
	path := writeConfig(t, "management 127.0.0.1 7505 /does/not/exist.pass\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.ManagementPasswordFileErr == nil {
		t.Error("expected ManagementPasswordFileErr to be set, got nil")
	}
}

func TestParseConfig_RejectsUnixSocket(t *testing.T) {
	path := writeConfig(t, "management /run/openvpn-client/tun1.sock unix\n")

	if _, err := ParseConfig(path); err == nil {
		t.Fatal("expected an error for a unix socket management directive, got nil")
	}
}

func TestParseConfig_RejectsZeroAddress(t *testing.T) {
	path := writeConfig(t, "management 0.0.0.0 7505\n")

	if _, err := ParseConfig(path); err == nil {
		t.Fatal("expected an error for a 0.0.0.0 management directive, got nil")
	}
}

func TestParseConfig_MissingFile(t *testing.T) {
	if _, err := ParseConfig("/does/not/exist.conf"); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}

// --- Mode detection + cross-concern independence (new) ---

func TestParseConfig_ClientModeDefault(t *testing.T) {
	path := writeConfig(t, "client\nremote vpn.example.com 1194\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.Mode != ModeClient {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeClient)
	}
}

func TestParseConfig_ServerMode(t *testing.T) {
	path := writeConfig(t, "port 1194\nserver 10.8.0.0 255.255.255.0\nmanagement 127.0.0.1 7505\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.Mode != ModeServer {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeServer)
	}
	if got.ManagementAddress != "127.0.0.1:7505" {
		t.Errorf("ManagementAddress = %q, want %q", got.ManagementAddress, "127.0.0.1:7505")
	}
}

func TestParseConfig_ServerBridgeMode(t *testing.T) {
	path := writeConfig(t, "server-bridge 10.8.0.4 255.255.255.0 10.8.0.128 10.8.0.254\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.Mode != ModeServer {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeServer)
	}
}

func TestParseConfig_ModeServerDirective(t *testing.T) {
	path := writeConfig(t, "mode server\ntls-server\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.Mode != ModeServer {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeServer)
	}
}

func TestParseConfig_ServerDirectiveBeforeManagement(t *testing.T) {
	// The server-mode directive appears before "management" in the file:
	// proves the whole file is scanned, not stopped at the first match.
	path := writeConfig(t, "port 1194\nserver 10.8.0.0 255.255.255.0\nverb 3\nmanagement 127.0.0.1 7505\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.Mode != ModeServer {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeServer)
	}
	if got.ManagementAddress != "127.0.0.1:7505" {
		t.Errorf("ManagementAddress = %q, want %q", got.ManagementAddress, "127.0.0.1:7505")
	}
}

func TestParseConfig_AllThreeConcernsAtOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ca.crt"), generateCert(t, "Combined CA", time.Now().Add(24*time.Hour)))
	writeFile(t, filepath.Join(dir, "server.crt"), generateCert(t, "Combined Server", time.Now().Add(24*time.Hour)))

	path := filepath.Join(dir, "server.conf")
	writeFile(t, path, "port 1194\nserver 10.8.0.0 255.255.255.0\nca ca.crt\ncert server.crt\nmanagement 127.0.0.1 7505\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.Mode != ModeServer {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeServer)
	}
	if got.ManagementAddress != "127.0.0.1:7505" {
		t.Errorf("ManagementAddress = %q, want %q", got.ManagementAddress, "127.0.0.1:7505")
	}
	if len(got.Certificates) != 2 {
		t.Errorf("got %d certificates, want 2: %+v", len(got.Certificates), got.Certificates)
	}
}

func TestParseConfig_CertsWithoutManagement(t *testing.T) {
	// An explicit config_path (cases 2/3) may have no "management" line at
	// all: the address is already given separately via YAML/env var. This
	// must not be an error — certificate reading is independent.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ca.crt"), generateCert(t, "No Management CA", time.Now().Add(24*time.Hour)))
	path := filepath.Join(dir, "client.conf")
	writeFile(t, path, "client\nca ca.crt\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.ManagementAddress != "" {
		t.Errorf("ManagementAddress = %q, want empty", got.ManagementAddress)
	}
	if len(got.Certificates) != 1 {
		t.Errorf("got %d certificates, want 1: %+v", len(got.Certificates), got.Certificates)
	}
}

func TestParseConfig_ManagementWithoutCerts(t *testing.T) {
	// Discovery (glob) requires only the management directive: a PSK config
	// with no ca/cert at all must still be usable for discovery.
	path := writeConfig(t, "management 127.0.0.1 7505\nsecret static.key\n")

	got, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got.ManagementAddress != "127.0.0.1:7505" {
		t.Errorf("ManagementAddress = %q, want %q", got.ManagementAddress, "127.0.0.1:7505")
	}
	if len(got.Certificates) != 0 {
		t.Errorf("got %d certificates, want 0: %+v", len(got.Certificates), got.Certificates)
	}
}

// --- Direct unit tests on the private helper functions ---

func TestIsServerModeDirective(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"server 10.8.0.0 255.255.255.0", true},
		{"server-bridge 10.8.0.4 255.255.255.0 10.8.0.128 10.8.0.254", true},
		{"mode server", true},
		{"client", false},
		{"servers-are-not-this", false},
		{"mode p2p", false},
	}
	for _, tt := range tests {
		if got := isServerModeDirective(tt.line); got != tt.want {
			t.Errorf("isServerModeDirective(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestParseManagementLine(t *testing.T) {
	address, passwordFile, err := parseManagementLine("test.conf", "management 127.0.0.1 7505 /etc/openvpn/mgmt.pass")
	if err != nil {
		t.Fatalf("parseManagementLine: %v", err)
	}
	if address != "127.0.0.1:7505" {
		t.Errorf("address = %q, want %q", address, "127.0.0.1:7505")
	}
	if passwordFile != "/etc/openvpn/mgmt.pass" {
		t.Errorf("passwordFile = %q, want %q", passwordFile, "/etc/openvpn/mgmt.pass")
	}
}

func TestParseManagementLine_Malformed(t *testing.T) {
	if _, _, err := parseManagementLine("test.conf", "management 127.0.0.1"); err == nil {
		t.Fatal("expected an error for a malformed management directive, got nil")
	}
}
