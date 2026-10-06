package openvpn

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

// ManagementDirective is the parsed "management" line of an OpenVPN client
// configuration file.
type ManagementDirective struct {
	// Address is the management interface's "host:port", ready to use as a
	// Tunnel's ManagementAddress.
	Address string

	// PasswordFile is the path to the management password file, taken from
	// the directive's optional third argument. Empty if the directive has no
	// password file.
	PasswordFile string
}

// ParseManagement scans configPath for its "management" directive and
// returns the address to connect to (and, if present, its password file).
// Only TCP management interfaces are supported:
//   - "management <path> unix" is rejected: internal/mgmt only dials TCP.
//   - "management 0.0.0.0 <port>" is rejected: 0.0.0.0 is a bind address,
//     not something a client can connect to — dialing it is unreliable at
//     best. Rebind to a reachable address instead (e.g. 127.0.0.1, or the
//     host's address when the client and scraper share a network namespace).
//
// Returns an error if no "management" directive is found.
func ParseManagement(configPath string) (*ManagementDirective, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", configPath, err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !matchesDirective(line, "management") {
			continue
		}

		fields := strings.Fields(directiveValue(line, "management"))
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s: malformed management directive: %q", configPath, line)
		}

		host, port := fields[0], fields[1]
		if port == "unix" {
			return nil, fmt.Errorf("%s: unix socket management interfaces (%q) are not supported, only TCP", configPath, line)
		}
		if host == "0.0.0.0" {
			return nil, fmt.Errorf("%s: management directive binds 0.0.0.0, which isn't a connectable address — rebind to a reachable one (e.g. 127.0.0.1)", configPath)
		}

		directive := &ManagementDirective{Address: net.JoinHostPort(host, port)}
		if len(fields) >= 3 {
			directive.PasswordFile = fields[2]
		}
		return directive, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning config %s: %w", configPath, err)
	}

	return nil, fmt.Errorf("%s: no management directive found", configPath)
}
