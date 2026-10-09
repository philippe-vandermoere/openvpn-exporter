// Package config loads the exporter configuration from a YAML file,
// environment variables, or a combination of both.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pvandermoere/openvpn-exporter/internal/openvpn"
)

const (
	defaultListenAddress = ":9176"
	defaultScrapeTimeout = 5 * time.Second

	envConfigPath        = "OPENVPN_EXPORTER_CONFIG"
	envListenAddress     = "OPENVPN_EXPORTER_LISTEN_ADDRESS"
	envScrapeTimeout     = "OPENVPN_EXPORTER_SCRAPE_TIMEOUT"
	envPassword          = "OPENVPN_EXPORTER_PASSWORD"
	envPasswordFile      = "OPENVPN_EXPORTER_PASSWORD_FILE"
	envTunnelName        = "OPENVPN_EXPORTER_TUNNEL_NAME"
	envTunnelMgmtAddress = "OPENVPN_EXPORTER_TUNNEL_MANAGEMENT_ADDRESS"
	envTunnelConfigPath  = "OPENVPN_EXPORTER_TUNNEL_CONFIG_PATH"
	envTunnelCertPath    = "OPENVPN_EXPORTER_TUNNEL_CERT_PATH"
	envTunnelsGlob       = "OPENVPN_EXPORTER_TUNNELS_GLOB"
	envServerName        = "OPENVPN_EXPORTER_SERVER_NAME"
	envServerMgmtAddress = "OPENVPN_EXPORTER_SERVER_MANAGEMENT_ADDRESS"
)

// Duration wraps time.Duration to support YAML values like "5s" as well as
// plain integers (interpreted as seconds).
type Duration time.Duration

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw interface{}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", v, err)
		}
		*d = Duration(parsed)
	case int:
		*d = Duration(time.Duration(v) * time.Second)
	default:
		return fmt.Errorf("unsupported duration value: %v", raw)
	}
	return nil
}

// Tunnel describes a single OpenVPN client tunnel to monitor.
//
// Certificate expiry: if ConfigPath is set, it is parsed to find both the CA
// and client certificates. Otherwise, if CertPath is set, only that
// certificate's expiry is tracked (no CA). If neither is set, certificate
// expiry is not checked for this tunnel. ConfigPath takes precedence when
// both are set.
type Tunnel struct {
	Name              string `yaml:"name"`
	ManagementAddress string `yaml:"management_address"`
	ConfigPath        string `yaml:"config_path"`
	CertPath          string `yaml:"cert_path"`

	// Password overrides Config.Password for this tunnel specifically.
	// Empty means "use the global password" (the common case). Populated
	// automatically for tunnels discovered via TunnelsGlob when their
	// "management" directive references a readable password file.
	Password string `yaml:"password"`
}

// Server describes a single OpenVPN *server* process to monitor (as opposed
// to a Tunnel, which monitors a client). Exposes a different metric family
// entirely (openvpn_server_*): per-connected-client info/traffic/connection
// time, not tunnel state/certificate expiry.
type Server struct {
	Name              string `yaml:"name"`
	ManagementAddress string `yaml:"management_address"`

	// Password overrides Config.Password for this server specifically.
	// Empty means "use the global password".
	Password string `yaml:"password"`
}

// Config is the fully resolved exporter configuration.
type Config struct {
	ListenAddress string   `yaml:"listen_address"`
	ScrapeTimeout Duration `yaml:"scrape_timeout"`
	PasswordFile  string   `yaml:"password_file"`
	Tunnels       []Tunnel `yaml:"tunnels"`

	// Servers is the list of OpenVPN servers to monitor, independent of
	// Tunnels — an exporter instance may monitor tunnels, servers, or both.
	Servers []Server `yaml:"servers"`

	// TunnelsGlob, if set, is expanded at load time: every matched file
	// becomes an additional tunnel (added to Tunnels, not replacing it).
	// The tunnel name is the filename without its extension, ConfigPath is
	// the matched file itself (reusing the existing ca/cert parsing), and
	// ManagementAddress comes from parsing the file's "management"
	// directive (see internal/openvpn.ParseManagement) — only TCP
	// directives bound to a reachable address are supported.
	TunnelsGlob string `yaml:"tunnels_glob"`

	// Password is the management interface password, resolved at startup
	// from either OPENVPN_EXPORTER_PASSWORD (direct value) or PasswordFile
	// (OPENVPN_EXPORTER_PASSWORD taking precedence if both are set). Empty
	// when neither is set (the management interface is then assumed to be
	// unprotected). Used as the fallback for any tunnel without its own
	// Password.
	Password string `yaml:"-"`

	// Warnings collects non-fatal issues found while loading, meant to be
	// logged by the caller (Load itself never logs). Currently populated
	// when a TunnelsGlob-discovered tunnel's management password file isn't
	// readable and falls back to the global Password instead.
	Warnings []string `yaml:"-"`
}

// Load builds the configuration from an optional YAML file path and
// environment variables. flagConfigPath takes precedence over the
// OPENVPN_EXPORTER_CONFIG environment variable; either may be empty, in
// which case the configuration is built entirely from environment
// variables (single-tunnel mode).
func Load(flagConfigPath string) (*Config, error) {
	path := flagConfigPath
	if path == "" {
		path = os.Getenv(envConfigPath)
	}

	cfg := &Config{}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", path, err)
		}
	}

	if err := applyEnvOverrides(cfg); err != nil {
		return nil, err
	}

	setDefaults(cfg)

	if err := resolvePassword(cfg); err != nil {
		return nil, err
	}

	if err := expandTunnelsGlob(cfg); err != nil {
		return nil, err
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applyEnvOverrides(cfg *Config) error {
	if v := os.Getenv(envListenAddress); v != "" {
		cfg.ListenAddress = v
	}
	if v := os.Getenv(envScrapeTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid %s value %q: %w", envScrapeTimeout, v, err)
		}
		cfg.ScrapeTimeout = Duration(d)
	}
	if v := os.Getenv(envPasswordFile); v != "" {
		cfg.PasswordFile = v
	}
	if v := os.Getenv(envTunnelsGlob); v != "" {
		cfg.TunnelsGlob = v
	}

	if len(cfg.Tunnels) == 0 {
		tunnel, present, err := tunnelFromEnv()
		if err != nil {
			return err
		}
		if present {
			cfg.Tunnels = []Tunnel{tunnel}
		}
	}

	if len(cfg.Servers) == 0 {
		server, present, err := serverFromEnv()
		if err != nil {
			return err
		}
		if present {
			cfg.Servers = []Server{server}
		}
	}

	return nil
}

func tunnelFromEnv() (Tunnel, bool, error) {
	name := os.Getenv(envTunnelName)
	addr := os.Getenv(envTunnelMgmtAddress)
	configPath := os.Getenv(envTunnelConfigPath)
	certPath := os.Getenv(envTunnelCertPath)

	if name == "" && addr == "" && configPath == "" && certPath == "" {
		return Tunnel{}, false, nil
	}
	if name == "" || addr == "" {
		return Tunnel{}, false, fmt.Errorf(
			"%s and %s must both be set to define a tunnel from environment variables",
			envTunnelName, envTunnelMgmtAddress,
		)
	}
	return Tunnel{Name: name, ManagementAddress: addr, ConfigPath: configPath, CertPath: certPath}, true, nil
}

func serverFromEnv() (Server, bool, error) {
	name := os.Getenv(envServerName)
	addr := os.Getenv(envServerMgmtAddress)

	if name == "" && addr == "" {
		return Server{}, false, nil
	}
	if name == "" || addr == "" {
		return Server{}, false, fmt.Errorf(
			"%s and %s must both be set to define a server from environment variables",
			envServerName, envServerMgmtAddress,
		)
	}
	return Server{Name: name, ManagementAddress: addr}, true, nil
}

func setDefaults(cfg *Config) {
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = defaultListenAddress
	}
	if cfg.ScrapeTimeout == 0 {
		cfg.ScrapeTimeout = Duration(defaultScrapeTimeout)
	}
}

// resolvePassword sets cfg.Password. A password given directly via
// OPENVPN_EXPORTER_PASSWORD takes precedence over PasswordFile, for
// deployments where the secret is already injected as an environment
// variable (e.g. a Docker/Kubernetes secret) rather than mounted as a file.
func resolvePassword(cfg *Config) error {
	if v := os.Getenv(envPassword); v != "" {
		cfg.Password = strings.TrimRight(v, "\r\n")
		return nil
	}
	if cfg.PasswordFile == "" {
		return nil
	}
	data, err := os.ReadFile(cfg.PasswordFile)
	if err != nil {
		return fmt.Errorf("reading password file %s: %w", cfg.PasswordFile, err)
	}
	cfg.Password = strings.TrimRight(string(data), "\r\n")
	return nil
}

// expandTunnelsGlob expands cfg.TunnelsGlob (if set) and appends one Tunnel
// per matched file to cfg.Tunnels. A tunnel's management password file not
// being readable is not immediately fatal: it falls back to cfg.Password
// (recording a warning) if one is configured, and only errors otherwise.
func expandTunnelsGlob(cfg *Config) error {
	if cfg.TunnelsGlob == "" {
		return nil
	}

	matches, err := filepath.Glob(cfg.TunnelsGlob)
	if err != nil {
		return fmt.Errorf("invalid tunnels_glob %q: %w", cfg.TunnelsGlob, err)
	}

	for _, match := range matches {
		name := strings.TrimSuffix(filepath.Base(match), filepath.Ext(match))

		directive, err := openvpn.ParseManagement(match)
		if err != nil {
			return fmt.Errorf("discovering tunnel from %s: %w", match, err)
		}

		tunnel := Tunnel{Name: name, ManagementAddress: directive.Address, ConfigPath: match}

		if directive.PasswordFile != "" {
			data, readErr := os.ReadFile(directive.PasswordFile)
			switch {
			case readErr == nil:
				tunnel.Password = strings.TrimRight(string(data), "\r\n")
			case cfg.Password != "":
				cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(
					"tunnel %q: management password file %s is not readable (%s), falling back to the global password",
					name, directive.PasswordFile, readErr,
				))
			default:
				return fmt.Errorf(
					"tunnel %q: management password file %s is not readable and no global password is configured: %w",
					name, directive.PasswordFile, readErr,
				)
			}
		}

		cfg.Tunnels = append(cfg.Tunnels, tunnel)
	}

	return nil
}

func validate(cfg *Config) error {
	if len(cfg.Tunnels) == 0 && len(cfg.Servers) == 0 {
		return fmt.Errorf("no tunnels or servers configured: set tunnels/servers in the config file or %s/%s/%s or %s/%s",
			envTunnelName, envTunnelMgmtAddress, envTunnelConfigPath, envServerName, envServerMgmtAddress)
	}

	seen := make(map[string]bool, len(cfg.Tunnels))
	for i, t := range cfg.Tunnels {
		if t.Name == "" {
			return fmt.Errorf("tunnel[%d]: name is required", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("tunnel[%d]: duplicate tunnel name %q", i, t.Name)
		}
		seen[t.Name] = true

		if t.ManagementAddress == "" {
			return fmt.Errorf("tunnel %q: management_address is required", t.Name)
		}
		if _, _, err := net.SplitHostPort(t.ManagementAddress); err != nil {
			return fmt.Errorf("tunnel %q: invalid management_address %q: %w", t.Name, t.ManagementAddress, err)
		}

		// config_path and cert_path are both optional: if neither is set,
		// certificate expiry simply isn't checked for this tunnel. When
		// set, each must point to an accessible file.
		if t.ConfigPath != "" {
			if _, err := os.Stat(t.ConfigPath); err != nil {
				return fmt.Errorf("tunnel %q: config_path %q is not accessible: %w", t.Name, t.ConfigPath, err)
			}
		}
		if t.CertPath != "" {
			if _, err := os.Stat(t.CertPath); err != nil {
				return fmt.Errorf("tunnel %q: cert_path %q is not accessible: %w", t.Name, t.CertPath, err)
			}
		}
	}

	// Servers are validated the same way as tunnels, but in their own
	// namespace: a server and a tunnel sharing a name is harmless, since
	// they surface as entirely different metrics (openvpn_server_* vs
	// openvpn_tunnel_*).
	seenServers := make(map[string]bool, len(cfg.Servers))
	for i, s := range cfg.Servers {
		if s.Name == "" {
			return fmt.Errorf("server[%d]: name is required", i)
		}
		if seenServers[s.Name] {
			return fmt.Errorf("server[%d]: duplicate server name %q", i, s.Name)
		}
		seenServers[s.Name] = true

		if s.ManagementAddress == "" {
			return fmt.Errorf("server %q: management_address is required", s.Name)
		}
		if _, _, err := net.SplitHostPort(s.ManagementAddress); err != nil {
			return fmt.Errorf("server %q: invalid management_address %q: %w", s.Name, s.ManagementAddress, err)
		}
	}

	return nil
}

// ScrapeTimeoutDuration returns the configured scrape timeout as a
// time.Duration.
func (c *Config) ScrapeTimeoutDuration() time.Duration {
	return time.Duration(c.ScrapeTimeout)
}
