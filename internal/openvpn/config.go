// Package openvpn parses OpenVPN client/server configuration files (their
// management interface directive, client-vs-server mode, and CA/certificate
// expiry, all in one pass via ParseConfig — private keys are never opened or
// parsed) and talks to the management interface itself (Target's
// FetchStats/FetchServerStatus). Target ties both together: a single
// client tunnel or server process to monitor.
package openvpn

import (
	"bufio"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	roleCA     = "ca"
	roleClient = "client"
)

// Mode is whether a parsed configuration file is an OpenVPN client or server.
type Mode string

const (
	ModeClient Mode = "client"
	ModeServer Mode = "server"
)

// Certificate is a single certificate found while parsing an OpenVPN
// configuration file.
type Certificate struct {
	// Role is "ca" for certificates found via the "ca" directive/block
	// (which may itself be a bundle of a root CA plus intermediates), or
	// "client" for the certificate found via the "cert" directive/block.
	Role string

	// Subject is the certificate's CommonName.
	Subject string

	// NotAfter is the certificate's expiry date.
	NotAfter time.Time
}

// ParsedConfig is everything observable from one pass over an OpenVPN client
// or server configuration file: its management interface (if any), whether
// it's client- or server-mode, and its CA/certificate(s) (if any). Each
// piece is independently optional — a config with certificates but no
// management directive, or vice versa, is not an error here; callers decide
// what's required for their own purpose. Private keys are never opened.
type ParsedConfig struct {
	// Mode is ModeServer if a "server"/"server-bridge"/"mode server"
	// directive was found, ModeClient otherwise — this also covers
	// PSK/secret configs, which have no server-mode concept at all and are
	// inherently point-to-point. Scope limit: an advanced P2P "tls-server"
	// setup *without* "server"/"mode server" is misclassified as a client.
	Mode Mode

	// ManagementAddress is empty if no "management" directive was found.
	ManagementAddress string

	// ManagementPassword is the content of the management directive's
	// password file (its optional 3rd argument), if one was referenced and
	// is readable. ManagementPasswordFileErr is set instead if one was
	// referenced but couldn't be read. Both are zero if no password file
	// was referenced at all.
	ManagementPassword        string
	ManagementPasswordFileErr error

	// Certificates is every CA/client certificate found (ca/cert
	// directives or inline blocks).
	Certificates []Certificate
}

// ParseConfig reads configPath and returns everything observable from a
// single pass over it (see ParsedConfig). It returns an error only for a
// config file that can't be read/scanned, or a "management" directive that
// is present but invalid (malformed, a unix socket, or bound to 0.0.0.0 —
// see parseManagementLine): these indicate a management interface that is
// present but unusable, not absent. A config with no "management" directive
// at all is not an error — callers that require one check
// ManagementAddress == "" themselves.
func ParseConfig(configPath string) (*ParsedConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", configPath, err)
	}
	baseDir := filepath.Dir(configPath)
	cfg := &ParsedConfig{Mode: ModeClient}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		certs, consumed, err := parseCertDirective(scanner, line, baseDir)
		if err != nil {
			return nil, err
		}
		if consumed {
			cfg.Certificates = append(cfg.Certificates, certs...)
			continue
		}

		if isServerModeDirective(line) {
			cfg.Mode = ModeServer
			continue
		}

		if cfg.ManagementAddress == "" && matchesDirective(line, "management") {
			address, passwordFile, err := parseManagementLine(configPath, line)
			if err != nil {
				return nil, err
			}
			cfg.ManagementAddress = address
			if passwordFile != "" {
				cfg.ManagementPassword, cfg.ManagementPasswordFileErr = readPasswordFile(passwordFile)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning config %s: %w", configPath, err)
	}
	return cfg, nil
}

// parseCertDirective handles one line that might be a <ca>/<cert>/<key>
// inline block or a ca/cert file directive. consumed is false if line
// matched none of these, so the caller tries other directive types.
func parseCertDirective(scanner *bufio.Scanner, line, baseDir string) (certs []Certificate, consumed bool, err error) {
	switch {
	case line == "<ca>":
		return parseInlineCertBlock(scanner, "</ca>", roleCA)
	case line == "<cert>":
		return parseInlineCertBlock(scanner, "</cert>", roleClient)
	case line == "<key>":
		// Never read: skip past the block without parsing its content.
		_, err := readInlineBlock(scanner, "</key>")
		return nil, true, err
	case matchesDirective(line, "ca"):
		certs, err := loadFromFile(directiveValue(line, "ca"), baseDir, roleCA)
		return certs, true, err
	case matchesDirective(line, "cert"):
		// "key" directive intentionally not handled: the private key file
		// is never opened.
		certs, err := loadFromFile(directiveValue(line, "cert"), baseDir, roleClient)
		return certs, true, err
	default:
		return nil, false, nil
	}
}

func parseInlineCertBlock(scanner *bufio.Scanner, endTag, role string) ([]Certificate, bool, error) {
	block, err := readInlineBlock(scanner, endTag)
	if err != nil {
		return nil, true, err
	}
	certs, err := parsePEM([]byte(block), role)
	if err != nil {
		return nil, true, fmt.Errorf("parsing inline %s block: %w", endTag, err)
	}
	return certs, true, nil
}

// isServerModeDirective reports whether line indicates server mode. OpenVPN
// itself supports "<ca>"-style inline file blocks (see package doc): this is
// a fixed, OpenVPN-specific convention (a handful of keyword tags like
// "ca"/"cert"/"key"/"tls-auth"), not general XML/HTML, so matching it is
// still plain fixed-string comparison, same as every other directive.
func isServerModeDirective(line string) bool {
	return matchesDirective(line, "server") || matchesDirective(line, "server-bridge") || line == "mode server"
}

// parseManagementLine parses the value of a "management" directive. Only TCP
// management interfaces are supported:
//   - "management <path> unix" is rejected: Target.dialAndAuthenticate only dials TCP.
//   - "management 0.0.0.0 <port>" is rejected: 0.0.0.0 is a bind address,
//     not something a client can connect to — dialing it is unreliable at
//     best. Rebind to a reachable address instead (e.g. 127.0.0.1, or the
//     host's address when the client and scraper share a network namespace).
func parseManagementLine(configPath, line string) (address, passwordFile string, err error) {
	fields := strings.Fields(directiveValue(line, "management"))
	if len(fields) < 2 {
		return "", "", fmt.Errorf("%s: malformed management directive: %q", configPath, line)
	}
	host, port := fields[0], fields[1]
	if port == "unix" {
		return "", "", fmt.Errorf("%s: unix socket management interfaces (%q) are not supported, only TCP", configPath, line)
	}
	if host == "0.0.0.0" {
		return "", "", fmt.Errorf("%s: management directive binds 0.0.0.0, which isn't a connectable address — rebind to a reachable one (e.g. 127.0.0.1)", configPath)
	}
	address = net.JoinHostPort(host, port)
	if len(fields) >= 3 {
		passwordFile = fields[2]
	}
	return address, passwordFile, nil
}

// readPasswordFile matches the path as written, with no baseDir join (same
// as the rest of the management password handling).
func readPasswordFile(path string) (password string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

// LoadCertOnly reads certPath directly as a PEM certificate file (or bundle)
// and returns its certificate(s), all labeled as the client role. Unlike
// ParseConfig, it does not parse an OpenVPN config file and never looks for
// a CA certificate — callers use this when only a direct certificate path is
// available (no config_path to derive ca/cert from).
func LoadCertOnly(certPath string) ([]Certificate, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("reading cert %s: %w", certPath, err)
	}
	certs, err := parsePEM(data, roleClient)
	if err != nil {
		return nil, fmt.Errorf("parsing cert file %s: %w", certPath, err)
	}
	return certs, nil
}

// matchesDirective reports whether line is a "<name> <value>" directive.
func matchesDirective(line, name string) bool {
	if !strings.HasPrefix(line, name) {
		return false
	}
	rest := line[len(name):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

func directiveValue(line, name string) string {
	value := strings.TrimSpace(line[len(name):])
	value = strings.Trim(value, `"`)
	return value
}

func readInlineBlock(scanner *bufio.Scanner, endTag string) (string, error) {
	var b strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == endTag {
			return b.String(), nil
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("unterminated inline block: missing %s", endTag)
}

func loadFromFile(path, baseDir, role string) ([]Certificate, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path for %q directive", role)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s file %s: %w", role, path, err)
	}
	certs, err := parsePEM(data, role)
	if err != nil {
		return nil, fmt.Errorf("parsing %s file %s: %w", role, path, err)
	}
	return certs, nil
}

// parsePEM decodes every CERTIFICATE block found in data. A single file or
// inline block may bundle several certificates (e.g. a root CA plus
// intermediates), each becoming its own Certificate.
func parsePEM(data []byte, role string) ([]Certificate, error) {
	var out []Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing certificate: %w", err)
		}
		out = append(out, Certificate{
			Role:     role,
			Subject:  cert.Subject.CommonName,
			NotAfter: cert.NotAfter,
		})
	}
	return out, nil
}
