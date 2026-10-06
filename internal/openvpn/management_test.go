package openvpn

import (
	"os"
	"path/filepath"
	"testing"
)

func writeManagementConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestParseManagement_TCP(t *testing.T) {
	path := writeManagementConfig(t, "client\nmanagement 127.0.0.1 7505\n")

	got, err := ParseManagement(path)
	if err != nil {
		t.Fatalf("ParseManagement: %v", err)
	}
	if got.Address != "127.0.0.1:7505" {
		t.Errorf("Address = %q, want %q", got.Address, "127.0.0.1:7505")
	}
	if got.PasswordFile != "" {
		t.Errorf("PasswordFile = %q, want empty", got.PasswordFile)
	}
}

func TestParseManagement_WithPasswordFile(t *testing.T) {
	path := writeManagementConfig(t, "management 127.0.0.1 7505 /etc/openvpn/client/tun1.pass\n")

	got, err := ParseManagement(path)
	if err != nil {
		t.Fatalf("ParseManagement: %v", err)
	}
	if got.PasswordFile != "/etc/openvpn/client/tun1.pass" {
		t.Errorf("PasswordFile = %q, want %q", got.PasswordFile, "/etc/openvpn/client/tun1.pass")
	}
}

func TestParseManagement_RejectsUnixSocket(t *testing.T) {
	path := writeManagementConfig(t, "management /run/openvpn-client/tun1.sock unix\n")

	if _, err := ParseManagement(path); err == nil {
		t.Fatal("expected an error for a unix socket management directive, got nil")
	}
}

func TestParseManagement_RejectsZeroAddress(t *testing.T) {
	path := writeManagementConfig(t, "management 0.0.0.0 7505\n")

	if _, err := ParseManagement(path); err == nil {
		t.Fatal("expected an error for a 0.0.0.0 management directive, got nil")
	}
}

func TestParseManagement_NoDirective(t *testing.T) {
	path := writeManagementConfig(t, "client\nca ca.crt\ncert client.crt\n")

	if _, err := ParseManagement(path); err == nil {
		t.Fatal("expected an error when no management directive is present, got nil")
	}
}

func TestParseManagement_MissingFile(t *testing.T) {
	if _, err := ParseManagement("/does/not/exist.conf"); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}
