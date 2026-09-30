// Package config loads the exporter configuration from a YAML file,
// environment variables, or a combination of both.
package config

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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
}

// Config is the fully resolved exporter configuration.
type Config struct {
	ListenAddress string   `yaml:"listen_address"`
	ScrapeTimeout Duration `yaml:"scrape_timeout"`
	PasswordFile  string   `yaml:"password_file"`
	Tunnels       []Tunnel `yaml:"tunnels"`

	// Password is the management interface password, resolved at startup
	// from either OPENVPN_EXPORTER_PASSWORD (direct value) or PasswordFile
	// (OPENVPN_EXPORTER_PASSWORD taking precedence if both are set). Empty
	// when neither is set (the management interface is then assumed to be
	// unprotected).
	Password string `yaml:"-"`
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

	if len(cfg.Tunnels) == 0 {
		tunnel, present, err := tunnelFromEnv()
		if err != nil {
			return err
		}
		if present {
			cfg.Tunnels = []Tunnel{tunnel}
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

func validate(cfg *Config) error {
	if len(cfg.Tunnels) == 0 {
		return fmt.Errorf("no tunnels configured: set tunnels in the config file or %s/%s/%s",
			envTunnelName, envTunnelMgmtAddress, envTunnelConfigPath)
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

	return nil
}

// ScrapeTimeoutDuration returns the configured scrape timeout as a
// time.Duration.
func (c *Config) ScrapeTimeoutDuration() time.Duration {
	return time.Duration(c.ScrapeTimeout)
}
