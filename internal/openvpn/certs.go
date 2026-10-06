// Package openvpn parses OpenVPN client configuration files: certificate
// expiry (this file) and the management interface directive (management.go).
// Private keys are never opened or parsed.
package openvpn

import (
	"bufio"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	roleCA     = "ca"
	roleClient = "client"
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

// Load reads configPath and returns every CA and client certificate it
// references, whether inline or as separate files. Relative file paths are
// resolved against the directory containing configPath, not the process's
// current working directory.
func Load(configPath string) ([]Certificate, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", configPath, err)
	}
	return parseConfig(string(data), filepath.Dir(configPath))
}

// LoadCertOnly reads certPath directly as a PEM certificate file (or bundle)
// and returns its certificate(s), all labeled as the client role. Unlike
// Load, it does not parse an OpenVPN config file and never looks for a CA
// certificate — callers use this when only a direct certificate path is
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

func parseConfig(content, baseDir string) ([]Certificate, error) {
	var out []Certificate

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		switch {
		case line == "<ca>":
			block, err := readInlineBlock(scanner, "</ca>")
			if err != nil {
				return nil, err
			}
			certs, err := parsePEM([]byte(block), roleCA)
			if err != nil {
				return nil, fmt.Errorf("parsing inline <ca> block: %w", err)
			}
			out = append(out, certs...)

		case line == "<cert>":
			block, err := readInlineBlock(scanner, "</cert>")
			if err != nil {
				return nil, err
			}
			certs, err := parsePEM([]byte(block), roleClient)
			if err != nil {
				return nil, fmt.Errorf("parsing inline <cert> block: %w", err)
			}
			out = append(out, certs...)

		case line == "<key>":
			// Never read: skip past the block without parsing its content.
			if _, err := readInlineBlock(scanner, "</key>"); err != nil {
				return nil, err
			}

		case matchesDirective(line, "ca"):
			certs, err := loadFromFile(directiveValue(line, "ca"), baseDir, roleCA)
			if err != nil {
				return nil, err
			}
			out = append(out, certs...)

		case matchesDirective(line, "cert"):
			certs, err := loadFromFile(directiveValue(line, "cert"), baseDir, roleClient)
			if err != nil {
				return nil, err
			}
			out = append(out, certs...)

			// "key" directive intentionally not handled: the private key
			// file is never opened.
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning config: %w", err)
	}

	return out, nil
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
