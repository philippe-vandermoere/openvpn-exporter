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
		envTunnelName, envTunnelMgmtAddress, envTunnelConfigPath, envTunnelCertPath, envTunnelsGlob,
		envServerName, envServerMgmtAddress,
	} {
		t.Setenv(key, "")
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	return writeFileAt(t, filepath.Join(t.TempDir(), name), content)
}

// writeFileAt writes content to an exact path (unlike writeTempFile, it
// doesn't allocate its own directory) — used when several files must land
// in the same caller-provided directory, e.g. for glob tests.
func writeFileAt(t *testing.T, path, content string) string {
	t.Helper()
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

func TestLoad_TunnelsGlob(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	writeFileAt(t, filepath.Join(dir, "tun1.conf"), "client\nmanagement 127.0.0.1 7505\n")
	writeFileAt(t, filepath.Join(dir, "tun2.conf"), "client\nmanagement 127.0.0.1 7506\n")

	yamlContent := "tunnels_glob: " + filepath.Join(dir, "*.conf") + "\n"
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 2 {
		t.Fatalf("got %d tunnels, want 2: %+v", len(cfg.Tunnels), cfg.Tunnels)
	}

	byName := map[string]Tunnel{}
	for _, tun := range cfg.Tunnels {
		byName[tun.Name] = tun
	}
	tun1, ok := byName["tun1"]
	if !ok || tun1.ManagementAddress != "127.0.0.1:7505" || tun1.ConfigPath != filepath.Join(dir, "tun1.conf") {
		t.Errorf("tun1: %+v", tun1)
	}
	tun2, ok := byName["tun2"]
	if !ok || tun2.ManagementAddress != "127.0.0.1:7506" {
		t.Errorf("tun2: %+v", tun2)
	}
}

func TestLoad_TunnelsGlobMergesWithExplicitTunnels(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	writeFileAt(t, filepath.Join(dir, "tun1.conf"), "management 127.0.0.1 7505\n")

	officeConfig := writeTempFile(t, "office.conf", "client\n")
	yamlContent := `
tunnels_glob: ` + filepath.Join(dir, "*.conf") + `
tunnels:
  - name: office
    management_address: 127.0.0.1:9999
    config_path: ` + officeConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 2 {
		t.Fatalf("got %d tunnels, want 2 (1 explicit + 1 discovered): %+v", len(cfg.Tunnels), cfg.Tunnels)
	}
}

func TestLoad_TunnelsGlobDuplicateNameWithExplicitTunnelIsAnError(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	writeFileAt(t, filepath.Join(dir, "office.conf"), "management 127.0.0.1 7505\n")

	otherConfig := writeTempFile(t, "other.conf", "client\n")
	yamlContent := `
tunnels_glob: ` + filepath.Join(dir, "*.conf") + `
tunnels:
  - name: office
    management_address: 127.0.0.1:9999
    config_path: ` + otherConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected a duplicate tunnel name error, got nil")
	}
}

func TestLoad_TunnelsGlobNoMatchesIsNotAnErrorByItself(t *testing.T) {
	clearEnv(t)

	officeConfig := writeTempFile(t, "office.conf", "client\n")
	yamlContent := `
tunnels_glob: /does/not/exist/*.conf
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: ` + officeConfig + `
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 1 {
		t.Fatalf("got %d tunnels, want 1 (glob matched nothing): %+v", len(cfg.Tunnels), cfg.Tunnels)
	}
}

func TestLoad_TunnelsGlobRejectsUnconnectableManagement(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	writeFileAt(t, filepath.Join(dir, "tun1.conf"), "management 0.0.0.0 7505\n")

	yamlContent := "tunnels_glob: " + filepath.Join(dir, "*.conf") + "\n"
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error for a 0.0.0.0 management directive, got nil")
	}
}

func TestLoad_TunnelsGlobPasswordFile_Readable(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	passFile := writeFileAt(t, filepath.Join(dir, "tun1.pass"), "tunnel-password\n")
	writeFileAt(t, filepath.Join(dir, "tun1.conf"), "management 127.0.0.1 7505 "+passFile+"\n")

	yamlContent := "tunnels_glob: " + filepath.Join(dir, "tun1.conf") + "\n"
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 1 || cfg.Tunnels[0].Password != "tunnel-password" {
		t.Fatalf("unexpected tunnels: %+v", cfg.Tunnels)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", cfg.Warnings)
	}
}

func TestLoad_TunnelsGlobPasswordFile_UnreadableFallsBackToGlobalPassword(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	writeFileAt(t, filepath.Join(dir, "tun1.conf"), "management 127.0.0.1 7505 /does/not/exist.pass\n")

	yamlContent := "tunnels_glob: " + filepath.Join(dir, "*.conf") + "\n"
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)
	t.Setenv(envPassword, "global-password")

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 1 || cfg.Tunnels[0].Password != "" {
		t.Fatalf("expected the discovered tunnel to have no per-tunnel password (falls back to global at wiring time): %+v", cfg.Tunnels)
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("expected 1 warning about the unreadable password file, got %v", cfg.Warnings)
	}
}

func TestLoad_TunnelsGlobPasswordFile_UnreadableAndNoGlobalPasswordIsAnError(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	writeFileAt(t, filepath.Join(dir, "tun1.conf"), "management 127.0.0.1 7505 /does/not/exist.pass\n")

	yamlContent := "tunnels_glob: " + filepath.Join(dir, "*.conf") + "\n"
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error: password file unreadable and no global password configured")
	}
}

func TestLoad_ServersYAML(t *testing.T) {
	clearEnv(t)

	yamlContent := `
servers:
  - name: vpn-gw-1
    management_address: 127.0.0.1:7600
  - name: vpn-gw-2
    management_address: 127.0.0.1:7601
    password: gw2-password
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(cfg.Servers))
	}
	if cfg.Servers[0].Name != "vpn-gw-1" || cfg.Servers[0].ManagementAddress != "127.0.0.1:7600" {
		t.Errorf("unexpected server[0]: %+v", cfg.Servers[0])
	}
	if cfg.Servers[1].Password != "gw2-password" {
		t.Errorf("server[1].Password = %q, want gw2-password", cfg.Servers[1].Password)
	}
}

func TestLoad_ServerOnlyNoTunnelsIsValid(t *testing.T) {
	clearEnv(t)

	yamlContent := `
servers:
  - name: vpn-gw-1
    management_address: 127.0.0.1:7600
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 0 {
		t.Errorf("got %d tunnels, want 0", len(cfg.Tunnels))
	}
	if len(cfg.Servers) != 1 {
		t.Errorf("got %d servers, want 1", len(cfg.Servers))
	}
}

func TestLoad_DuplicateServerNames(t *testing.T) {
	clearEnv(t)

	yamlContent := `
servers:
  - name: vpn-gw
    management_address: 127.0.0.1:7600
  - name: vpn-gw
    management_address: 127.0.0.1:7601
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error for duplicate server names, got nil")
	}
}

func TestLoad_InvalidServerManagementAddress(t *testing.T) {
	clearEnv(t)

	yamlContent := `
servers:
  - name: vpn-gw
    management_address: "not-a-host-port"
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err == nil {
		t.Fatal("expected an error for invalid server management_address, got nil")
	}
}

func TestLoad_TunnelAndServerNameCollisionIsNotAnError(t *testing.T) {
	clearEnv(t)

	tunnelConfig := writeTempFile(t, "shared.conf", "client\n")
	yamlContent := `
tunnels:
  - name: shared
    management_address: 127.0.0.1:7505
    config_path: ` + tunnelConfig + `
servers:
  - name: shared
    management_address: 127.0.0.1:7600
`
	yamlPath := writeTempFile(t, "config.yaml", yamlContent)

	if _, err := Load(yamlPath); err != nil {
		t.Fatalf("Load: %v (tunnel/server names are separate namespaces)", err)
	}
}

func TestLoad_EnvVarSingleServer(t *testing.T) {
	clearEnv(t)

	t.Setenv(envServerName, "vpn-gw")
	t.Setenv(envServerMgmtAddress, "127.0.0.1:7600")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(cfg.Servers))
	}
	server := cfg.Servers[0]
	if server.Name != "vpn-gw" || server.ManagementAddress != "127.0.0.1:7600" {
		t.Errorf("unexpected server: %+v", server)
	}
}

func TestLoad_EnvVarPartialServerIsAnError(t *testing.T) {
	clearEnv(t)

	t.Setenv(envServerName, "vpn-gw")
	// Management address deliberately left unset.

	if _, err := Load(""); err == nil {
		t.Fatal("expected an error for a partially specified env server, got nil")
	}
}
