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

func TestLoad_FileReferencedCertificates(t *testing.T) {
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

	got, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d certificates, want 2: %+v", len(got), got)
	}

	byRole := map[string]Certificate{}
	for _, c := range got {
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

func TestLoad_InlineCertificates(t *testing.T) {
	dir := t.TempDir()

	caExpiry := time.Now().Add(365 * 24 * time.Hour)
	clientExpiry := time.Now().Add(30 * 24 * time.Hour)

	configContent := "client\n" +
		"<ca>\n" + generateCert(t, "Inline CA", caExpiry) + "</ca>\n" +
		"<cert>\n" + generateCert(t, "Inline Client", clientExpiry) + "</cert>\n" +
		"<key>\n-----BEGIN PRIVATE KEY-----\nnot-a-real-key-and-never-parsed\n-----END PRIVATE KEY-----\n</key>\n"

	configPath := filepath.Join(dir, "client.conf")
	writeFile(t, configPath, configContent)

	got, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d certificates, want 2: %+v", len(got), got)
	}
}

func TestLoad_CABundleWithIntermediate(t *testing.T) {
	dir := t.TempDir()

	root := generateCert(t, "Root CA", time.Now().Add(365*24*time.Hour))
	intermediate := generateCert(t, "Intermediate CA", time.Now().Add(200*24*time.Hour))

	writeFile(t, filepath.Join(dir, "ca.crt"), root+intermediate)
	configPath := filepath.Join(dir, "client.conf")
	writeFile(t, configPath, "ca ca.crt\n")

	got, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d certificates from bundle, want 2: %+v", len(got), got)
	}
	for _, c := range got {
		if c.Role != roleCA {
			t.Errorf("certificate %q has role %q, want %q", c.Subject, c.Role, roleCA)
		}
	}
}

func TestLoad_RelativePathsResolvedAgainstConfigDir(t *testing.T) {
	configDir := t.TempDir()
	writeFile(t, filepath.Join(configDir, "ca.crt"), generateCert(t, "Relative CA", time.Now().Add(24*time.Hour)))
	configPath := filepath.Join(configDir, "client.conf")
	writeFile(t, configPath, "ca ca.crt\n")

	// Change the process working directory to something else entirely to
	// prove path resolution does not depend on it.
	otherDir := t.TempDir()
	t.Chdir(otherDir)

	got, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Subject != "Relative CA" {
		t.Fatalf("got %+v, want a single 'Relative CA' certificate", got)
	}
}

func TestLoad_NeverOpensKeyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ca.crt"), generateCert(t, "CA", time.Now().Add(24*time.Hour)))
	configPath := filepath.Join(dir, "client.conf")
	// "key" points to a file that does not exist: if the loader ever
	// tried to open it, Load would fail.
	writeFile(t, configPath, "ca ca.crt\nkey missing-and-must-not-be-opened.key\n")

	if _, err := Load(configPath); err != nil {
		t.Fatalf("Load should not fail on a missing key file: %v", err)
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
