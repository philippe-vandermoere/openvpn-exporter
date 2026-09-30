package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// clearEnv resets every environment variable the config package reads, and
// restores the previous value once the test finishes (via t.Setenv). The
// package treats an empty string the same as an unset variable.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		envConfigPath, envListenAddress, envScrapeTimeout, envPassword, envPasswordFile,
		envTunnelName, envTunnelMgmtAddress, envTunnelConfigPath, envTunnelCertPath,
	} {
		t.Setenv(key, "")
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestLoad_YAMLFileWithDefaults(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	yamlContent := `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: ` + tunnelConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ListenAddress != defaultListenAddress {
		t.Errorf("ListenAddress = %q, want default %q", cfg.ListenAddress, defaultListenAddress)
	}
	if cfg.ScrapeTimeoutDuration() != defaultScrapeTimeout {
		t.Errorf("ScrapeTimeout = %v, want default %v", cfg.ScrapeTimeoutDuration(), defaultScrapeTimeout)
	}
	if len(cfg.Tunnels) != 1 || cfg.Tunnels[0].Name != "office" {
		t.Fatalf("unexpected tunnels: %+v", cfg.Tunnels)
	}
}

func TestLoad_YAMLWithExplicitValues(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	passwordFile := writeTempFile(t, "mgmt.pass", "s3cret\n")

	yamlContent := `
listen_address: ":9999"
scrape_timeout: 2s
password_file: ` + passwordFile + `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: ` + tunnelConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ListenAddress != ":9999" {
		t.Errorf("ListenAddress = %q, want :9999", cfg.ListenAddress)
	}
	if cfg.ScrapeTimeoutDuration() != 2*time.Second {
		t.Errorf("ScrapeTimeout = %v, want 2s", cfg.ScrapeTimeoutDuration())
	}
	if cfg.Password != "s3cret" {
		t.Errorf("Password = %q, want %q (trailing newline trimmed)", cfg.Password, "s3cret")
	}
}

func TestLoad_DuplicateTunnelNames(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	yamlContent := `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: ` + tunnelConfig + `
  - name: office
    management_address: 127.0.0.1:7506
    config_path: ` + tunnelConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error for duplicate tunnel names, got nil")
	}
}

func TestLoad_InvalidManagementAddress(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	yamlContent := `
tunnels:
  - name: office
    management_address: "not-a-host-port"
    config_path: ` + tunnelConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error for invalid management_address, got nil")
	}
}

func TestLoad_MissingConfigPath(t *testing.T) {
	clearEnv(t)

	yamlContent := `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: /does/not/exist.conf
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error for a non-existent config_path, got nil")
	}
}

func TestLoad_CertPathOnly(t *testing.T) {
	clearEnv(t)

	certPath := writeTempFile(t, "client.crt", "not-a-real-cert-but-just-needs-to-exist\n")
	yamlContent := `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    cert_path: ` + certPath + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tunnels[0].ConfigPath != "" {
		t.Errorf("ConfigPath = %q, want empty", cfg.Tunnels[0].ConfigPath)
	}
	if cfg.Tunnels[0].CertPath != certPath {
		t.Errorf("CertPath = %q, want %q", cfg.Tunnels[0].CertPath, certPath)
	}
}

func TestLoad_MissingCertPath(t *testing.T) {
	clearEnv(t)

	yamlContent := `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    cert_path: /does/not/exist.crt
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error for a non-existent cert_path, got nil")
	}
}

func TestLoad_NeitherConfigPathNorCertPathIsValid(t *testing.T) {
	clearEnv(t)

	yamlContent := `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tunnels[0].ConfigPath != "" || cfg.Tunnels[0].CertPath != "" {
		t.Errorf("expected both paths empty, got %+v", cfg.Tunnels[0])
	}
}

func TestLoad_NoTunnelsConfigured(t *testing.T) {
	clearEnv(t)

	yamlPath := writeTempFile(t, "config.yaml", "listen_address: \":9176\"\n")

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error when no tunnels are configured, got nil")
	}
}

func TestLoad_EnvVarSingleTunnel(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	t.Setenv(envTunnelName, "office")
	t.Setenv(envTunnelMgmtAddress, "127.0.0.1:7505")
	t.Setenv(envTunnelConfigPath, tunnelConfig)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 1 {
		t.Fatalf("got %d tunnels, want 1", len(cfg.Tunnels))
	}
	tunnel := cfg.Tunnels[0]
	if tunnel.Name != "office" || tunnel.ManagementAddress != "127.0.0.1:7505" || tunnel.ConfigPath != tunnelConfig {
		t.Errorf("unexpected tunnel: %+v", tunnel)
	}
}

func TestLoad_EnvVarSingleTunnelWithCertPath(t *testing.T) {
	clearEnv(t)

	certPath := writeTempFile(t, "client.crt", "not-a-real-cert-but-just-needs-to-exist\n")
	t.Setenv(envTunnelName, "office")
	t.Setenv(envTunnelMgmtAddress, "127.0.0.1:7505")
	t.Setenv(envTunnelCertPath, certPath)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tunnel := cfg.Tunnels[0]
	if tunnel.ConfigPath != "" {
		t.Errorf("ConfigPath = %q, want empty", tunnel.ConfigPath)
	}
	if tunnel.CertPath != certPath {
		t.Errorf("CertPath = %q, want %q", tunnel.CertPath, certPath)
	}
}

func TestLoad_EnvVarPartialTunnelIsAnError(t *testing.T) {
	clearEnv(t)

	t.Setenv(envTunnelName, "office")
	// Management address and config path deliberately left unset.

	if _, err := Load(""); err == nil {
		t.Fatal("expected an error for a partially specified env tunnel, got nil")
	}
}

func TestLoad_EnvVarsOverrideYAML(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	yamlContent := `
listen_address: ":9999"
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: ` + tunnelConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)
	t.Setenv(envListenAddress, ":8888")

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddress != ":8888" {
		t.Errorf("ListenAddress = %q, want env override :8888", cfg.ListenAddress)
	}
}

func TestLoad_DirectPasswordEnvVar(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	yamlContent := `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: ` + tunnelConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)
	t.Setenv(envPassword, "s3cret-from-env")

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Password != "s3cret-from-env" {
		t.Errorf("Password = %q, want %q", cfg.Password, "s3cret-from-env")
	}
}

func TestLoad_DirectPasswordEnvVarOverridesPasswordFile(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	passwordFile := writeTempFile(t, "mgmt.pass", "from-file\n")
	yamlContent := `
password_file: ` + passwordFile + `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: ` + tunnelConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)
	t.Setenv(envPassword, "from-env")

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Password != "from-env" {
		t.Errorf("Password = %q, want %q (env var must take precedence over password_file)", cfg.Password, "from-env")
	}
}

func TestLoad_ConfigPathFromEnvVar(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "office.conf", "client\n")
	yamlContent := `
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: ` + tunnelConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)
	t.Setenv(envConfigPath, yamlPath)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 1 || cfg.Tunnels[0].Name != "office" {
		t.Fatalf("unexpected tunnels: %+v", cfg.Tunnels)
	}
}
