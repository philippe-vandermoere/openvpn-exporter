// Package config loads the exporter configuration from a YAML file,
// environment variables, or a combination of both.
package config

import (
	"cmp"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
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
	envTargetsGlob       = "OPENVPN_EXPORTER_TARGETS_GLOB"
	envServerName        = "OPENVPN_EXPORTER_SERVER_NAME"
	envServerMgmtAddress = "OPENVPN_EXPORTER_SERVER_MANAGEMENT_ADDRESS"
	envServerConfigPath  = "OPENVPN_EXPORTER_SERVER_CONFIG_PATH"
	envServerCertPath    = "OPENVPN_EXPORTER_SERVER_CERT_PATH"
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

// Config is the fully resolved exporter configuration.
type Config struct {
	ListenAddress string           `yaml:"listen_address"`
	ScrapeTimeout Duration         `yaml:"scrape_timeout"`
	PasswordFile  string           `yaml:"password_file"`
	Tunnels       []openvpn.Target `yaml:"tunnels"`

	// Servers is the list of OpenVPN servers to monitor, independent of
	// Tunnels — an exporter instance may monitor tunnels, servers, or both.
	// Tunnels and Servers share the same openvpn.Target type (certificate
	// expiry: ConfigPath takes precedence over CertPath when both are set,
	// neither is required) — only Mode (set by setTargetModes, never from
	// YAML) and which metric family the collector emits differ.
	Servers []openvpn.Target `yaml:"servers"`

	// TargetsGlob, if set, is expanded at load time: every matched file
	// becomes an additional tunnel or server (added to Tunnels/Servers, not
	// replacing them) depending on its detected mode (see
	// internal/openvpn.ParseConfig). The target's name is the filename
	// without its extension, ConfigPath is the matched file itself (reusing
	// the existing ca/cert parsing), and ManagementAddress comes from
	// parsing the file's own "management" directive — only TCP directives
	// bound to a reachable address are supported.
	TargetsGlob string `yaml:"targets_glob"`

	// Password is the management interface password, resolved at startup
	// from either OPENVPN_EXPORTER_PASSWORD (direct value) or PasswordFile
	// (OPENVPN_EXPORTER_PASSWORD taking precedence if both are set). Empty
	// when neither is set (the management interface is then assumed to be
	// unprotected). Used as the fallback for any tunnel or server without
	// its own Password.
	Password string `yaml:"-"`

	// Warnings collects non-fatal issues found while loading, meant to be
	// logged by the caller (Load itself never logs). Currently populated
	// when a targets_glob-discovered target's management password file
	// isn't readable and falls back to the global Password instead.
	Warnings []string `yaml:"-"`
}

// Load builds the configuration from an optional YAML file path and
// environment variables. flagConfigPath takes precedence over the
// OPENVPN_EXPORTER_CONFIG environment variable; either may be empty, in
// which case the configuration is built entirely from environment
// variables (single-tunnel/single-server mode).
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

	if err := expandTargetsGlob(cfg); err != nil {
		return nil, err
	}

	// setTargetModes runs last, after every source (YAML, env vars,
	// targets_glob) has had a chance to append to Tunnels/Servers: a
	// single place to look, rather than tracking Mode assignment
	// per-source. targets_glob-discovered targets already have Mode set
	// correctly, but re-setting it here is harmless (idempotent).
	setTargetModes(cfg)

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
	if v := os.Getenv(envTargetsGlob); v != "" {
		cfg.TargetsGlob = v
	}

	if len(cfg.Tunnels) == 0 {
		tunnel, present, err := targetFromEnv("tunnel", envTunnelName, envTunnelMgmtAddress, envTunnelConfigPath, envTunnelCertPath)
		if err != nil {
			return err
		}
		if present {
			cfg.Tunnels = []openvpn.Target{tunnel}
		}
	}

	if len(cfg.Servers) == 0 {
		server, present, err := targetFromEnv("server", envServerName, envServerMgmtAddress, envServerConfigPath, envServerCertPath)
		if err != nil {
			return err
		}
		if present {
			cfg.Servers = []openvpn.Target{server}
		}
	}

	return nil
}

// targetFromEnv builds a single openvpn.Target from a tunnel's or server's
// four environment variables (kind is "tunnel" or "server", used only for
// the error message — Mode itself is assigned later by setTargetModes).
func targetFromEnv(kind, nameVar, addrVar, configVar, certVar string) (openvpn.Target, bool, error) {
	name := os.Getenv(nameVar)
	addr := os.Getenv(addrVar)
	configPath := os.Getenv(configVar)
	certPath := os.Getenv(certVar)

	if name == "" && addr == "" && configPath == "" && certPath == "" {
		return openvpn.Target{}, false, nil
	}
	if name == "" || addr == "" {
		return openvpn.Target{}, false, fmt.Errorf(
			"%s and %s must both be set to define a %s from environment variables",
			nameVar, addrVar, kind,
		)
	}
	return openvpn.Target{Name: name, ManagementAddress: addr, ConfigPath: configPath, CertPath: certPath}, true, nil
}

// setTargetModes assigns Mode to every Target, regardless of how it was
// populated (YAML list, env var, or targets_glob) -- a single place to
// look, rather than tracked per source.
func setTargetModes(cfg *Config) {
	for i := range cfg.Tunnels {
		cfg.Tunnels[i].Mode = openvpn.ModeClient
	}
	for i := range cfg.Servers {
		cfg.Servers[i].Mode = openvpn.ModeServer
	}
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

// expandTargetsGlob expands cfg.TargetsGlob (if set) and appends one Tunnel
// or Server per matched file, depending on its detected mode, to
// cfg.Tunnels/cfg.Servers. A target's management password file not being
// readable is not immediately fatal: it falls back to cfg.Password
// (recording a warning) if one is configured, and only errors otherwise.
func expandTargetsGlob(cfg *Config) error {
	if cfg.TargetsGlob == "" {
		return nil
	}

	matches, err := doublestar.FilepathGlob(cfg.TargetsGlob)
	if err != nil {
		return fmt.Errorf("invalid targets_glob %q: %w", cfg.TargetsGlob, err)
	}

	for _, match := range matches {
		name := strings.TrimSuffix(filepath.Base(match), filepath.Ext(match))

		parsed, err := openvpn.ParseConfig(match)
		if err != nil {
			return fmt.Errorf("discovering target from %s: %w", match, err)
		}
		if parsed.ManagementAddress == "" {
			return fmt.Errorf("discovering target from %s: no management directive found", match)
		}

		password, err := resolveGlobPassword(cfg, name, parsed.ManagementPassword, parsed.ManagementPasswordFileErr)
		if err != nil {
			return err
		}

		switch parsed.Mode {
		case openvpn.ModeServer:
			cfg.Servers = append(cfg.Servers, openvpn.Target{Name: name, Mode: openvpn.ModeServer, ManagementAddress: parsed.ManagementAddress, ConfigPath: match, Password: password})
		default:
			cfg.Tunnels = append(cfg.Tunnels, openvpn.Target{Name: name, Mode: openvpn.ModeClient, ManagementAddress: parsed.ManagementAddress, ConfigPath: match, Password: password})
		}
	}

	return nil
}

// resolveGlobPassword decides the final per-target password from what
// ParseConfig already read (no file I/O happens here — that happened once,
// inside ParseConfig): the resolved password if its file was readable, a
// warning-logged fallback to the global password if the file was referenced
// but unreadable, or a hard error if there's no fallback either. Shared by
// both tunnel and server discovery.
func resolveGlobPassword(cfg *Config, name, password string, fileErr error) (string, error) {
	switch {
	case fileErr == nil:
		return password, nil
	case cfg.Password != "":
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(
			"target %q: management password file is not readable (%s), falling back to the global password",
			name, fileErr,
		))
		return "", nil
	default:
		return "", fmt.Errorf(
			"target %q: management password file is not readable and no global password is configured: %w",
			name, fileErr,
		)
	}
}

func validate(cfg *Config) error {
	if len(cfg.Tunnels) == 0 && len(cfg.Servers) == 0 {
		return fmt.Errorf("no tunnels or servers configured: set tunnels/servers in the config file or %s/%s/%s or %s/%s",
			envTunnelName, envTunnelMgmtAddress, envTunnelConfigPath, envServerName, envServerMgmtAddress)
	}

	seen := make(map[string]bool, len(cfg.Tunnels))
	for i, t := range cfg.Tunnels {
		if err := validateTarget("tunnel", i, seen, t); err != nil {
			return err
		}
	}

	// Servers are validated the same way as tunnels, but in their own
	// namespace: a server and a tunnel sharing a name is harmless, since
	// they surface as entirely different metrics (openvpn_server_* vs
	// openvpn_tunnel_*).
	seenServers := make(map[string]bool, len(cfg.Servers))
	for i, s := range cfg.Servers {
		if err := validateTarget("server", i, seenServers, s); err != nil {
			return err
		}
	}

	return nil
}

// validateTarget validates a single Tunnel/Server entry: name
// required/unique within seen, management_address required and valid, and
// (via validateCertSource) config_path/cert_path accessible if set. kind is
// "tunnel" or "server", used only for error messages.
func validateTarget(kind string, i int, seen map[string]bool, t openvpn.Target) error {
	if t.Name == "" {
		return fmt.Errorf("%s[%d]: name is required", kind, i)
	}
	if seen[t.Name] {
		return fmt.Errorf("%s[%d]: duplicate %s name %q", kind, i, kind, t.Name)
	}
	seen[t.Name] = true

	if t.ManagementAddress == "" {
		return fmt.Errorf("%s %q: management_address is required", kind, t.Name)
	}
	if _, _, err := net.SplitHostPort(t.ManagementAddress); err != nil {
		return fmt.Errorf("%s %q: invalid management_address %q: %w", kind, t.Name, t.ManagementAddress, err)
	}

	// config_path and cert_path are both optional: if neither is set,
	// certificate expiry simply isn't checked for this target. When set,
	// each must point to an accessible file.
	return validateCertSource(kind, t.Name, t.ConfigPath, t.CertPath)
}

// validateCertSource checks that config_path/cert_path, if set, point to an
// accessible file. Shared between tunnels and servers, which both support
// the same two optional certificate sources (see openvpn.Target's doc
// comment).
func validateCertSource(kind, name, configPath, certPath string) error {
	if configPath != "" {
		if _, err := os.Stat(configPath); err != nil {
			return fmt.Errorf("%s %q: config_path %q is not accessible: %w", kind, name, configPath, err)
		}
	}
	if certPath != "" {
		if _, err := os.Stat(certPath); err != nil {
			return fmt.Errorf("%s %q: cert_path %q is not accessible: %w", kind, name, certPath, err)
		}
	}
	return nil
}

// GetTargets returns every configured tunnel and server as a single
// []openvpn.Target, ready to hand to collector.NewCollector: each target's
// Password is resolved against the global fallback (Config.Password) here,
// so callers don't need to repeat that logic themselves.
func (c *Config) GetTargets() []openvpn.Target {
	targets := make([]openvpn.Target, 0, len(c.Tunnels)+len(c.Servers))
	for _, t := range c.Tunnels {
		t.Password = cmp.Or(t.Password, c.Password)
		targets = append(targets, t)
	}
	for _, s := range c.Servers {
		s.Password = cmp.Or(s.Password, c.Password)
		targets = append(targets, s)
	}
	return targets
}

// ScrapeTimeoutDuration returns the configured scrape timeout as a
// time.Duration.
func (c *Config) ScrapeTimeoutDuration() time.Duration {
	return time.Duration(c.ScrapeTimeout)
}
