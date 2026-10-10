package openvpn

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Stats holds the values read from the management interface for a single
// scrape of a client tunnel.
type Stats struct {
	// State is the raw OpenVPN state string (e.g. CONNECTED, RECONNECTING).
	State string

	// StateSince is when the tunnel entered State, zero if the state
	// response's timestamp field couldn't be parsed. A zero value does not
	// fail FetchStats on its own.
	StateSince time.Time
	// StateSinceErr is non-nil when StateSince couldn't be parsed.
	StateSinceErr error

	// Version is the OpenVPN version (e.g. "2.6.12"), empty if the
	// "version" command failed or its response couldn't be parsed. A
	// missing/old management API not supporting a clean "version" response
	// is not the same as the tunnel being down, so this does not fail
	// FetchStats on its own.
	Version string
	// VersionErr is non-nil when Version couldn't be obtained.
	VersionErr error

	// TunReadBytes/TunWriteBytes are the plaintext tunnel-side counters
	// ("TUN/TAP read/write bytes").
	TunReadBytes  uint64
	TunWriteBytes uint64

	// LinkReadBytes/LinkWriteBytes are the encrypted link-side counters
	// ("TCP/UDP read/write bytes").
	LinkReadBytes  uint64
	LinkWriteBytes uint64
}

// ClientInfo describes a single client connected to an OpenVPN *server*, as
// reported by its "status 3" response.
type ClientInfo struct {
	CommonName     string
	RealAddress    string
	VirtualAddress string
	BytesReceived  uint64
	BytesSent      uint64
	ConnectedSince time.Time
	Username       string
	Cipher         string
}

// ServerStatus holds the values read from an OpenVPN *server's* management
// interface for a single scrape.
type ServerStatus struct {
	// Clients is one entry per currently connected client.
	Clients []ClientInfo

	// Version is the OpenVPN version (e.g. "2.6.20"), empty if the
	// "version" command failed or its response couldn't be parsed. Does
	// not fail FetchServerStatus on its own (same semantics as
	// Stats.Version/VersionErr).
	Version    string
	VersionErr error
}

// stateResult is the outcome of parsing the response to the "state" command.
type stateResult struct {
	State string
	// Since is the Unix timestamp at which the tunnel entered State, zero
	// if the timestamp field couldn't be parsed.
	Since time.Time
	// SinceErr is non-nil if the timestamp field couldn't be parsed; it
	// does not make parseStateLines itself fail, since the state field
	// (the only thing that affects openvpn_tunnel_up/openvpn_tunnel_state)
	// was still read successfully.
	SinceErr error
}

// parseStateLines extracts the state and its timestamp from the response to
// the "state" command. The expected format for a single state line is a
// comma-separated list: timestamp,state,detail,local-ip,remote-ip,... The
// last line is used in case a stray line preceded it.
func parseStateLines(lines []string) (stateResult, error) {
	if len(lines) == 0 {
		return stateResult{}, fmt.Errorf("empty response")
	}
	fields := strings.Split(lines[len(lines)-1], ",")
	if len(fields) < 2 || fields[1] == "" {
		return stateResult{}, fmt.Errorf("unexpected state line format: %q", lines[len(lines)-1])
	}

	result := stateResult{State: fields[1]}
	sec, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		result.SinceErr = fmt.Errorf("parsing state timestamp %q: %w", fields[0], err)
		return result, nil
	}
	result.Since = time.Unix(sec, 0)
	return result, nil
}

// parseVersionLines extracts the OpenVPN version number (e.g. "2.6.12") from
// the response to the "version" command, e.g.:
//
//	OpenVPN Version: OpenVPN 2.6.12 x86_64-pc-linux-gnu [SSL (OpenSSL)] ... built on Jun  4 2024
//	Management Version: 5
//
// Only the version token itself is kept — never the platform/build/flags
// text — to avoid unbounded label cardinality and churn on every rebuild.
func parseVersionLines(lines []string) (string, error) {
	const prefix = "OpenVPN Version: OpenVPN "
	for _, line := range lines {
		rest, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return "", fmt.Errorf("empty version field in line %q", line)
		}
		return fields[0], nil
	}
	return "", fmt.Errorf("no %q line in version response", "OpenVPN Version:")
}

// statusCounterKeys maps the label used by the management interface's
// "status" command to the Stats field it fills in.
var statusCounterKeys = []string{
	"TUN/TAP read bytes",
	"TUN/TAP write bytes",
	"TCP/UDP read bytes",
	"TCP/UDP write bytes",
}

// parseStatusLines extracts the four traffic counters from the response to
// the "status" command. All four must be present; a response missing any of
// them is treated as an error rather than silently reporting zero, so a
// scrape never reports a partial set of byte counters.
func parseStatusLines(lines []string) (*Stats, error) {
	stats := &Stats{}
	found := make(map[string]bool, len(statusCounterKeys))

	for _, line := range lines {
		key, valStr, ok := strings.Cut(line, ",")
		if !ok {
			continue
		}
		val, err := strconv.ParseUint(strings.TrimSpace(valStr), 10, 64)

		switch key {
		case "TUN/TAP read bytes":
			if err != nil {
				return nil, fmt.Errorf("parsing %q: %w", key, err)
			}
			stats.TunReadBytes = val
			found[key] = true
		case "TUN/TAP write bytes":
			if err != nil {
				return nil, fmt.Errorf("parsing %q: %w", key, err)
			}
			stats.TunWriteBytes = val
			found[key] = true
		case "TCP/UDP read bytes":
			if err != nil {
				return nil, fmt.Errorf("parsing %q: %w", key, err)
			}
			stats.LinkReadBytes = val
			found[key] = true
		case "TCP/UDP write bytes":
			if err != nil {
				return nil, fmt.Errorf("parsing %q: %w", key, err)
			}
			stats.LinkWriteBytes = val
			found[key] = true
		}
	}

	for _, key := range statusCounterKeys {
		if !found[key] {
			return nil, fmt.Errorf("missing %q in status response", key)
		}
	}

	return stats, nil
}

// parseServerStatusLines extracts one ClientInfo per CLIENT_LIST row from
// the response to the "status 3" command, ignoring every other line
// (TITLE/TIME/HEADER/ROUTING_TABLE/GLOBAL_STATS). Example CLIENT_LIST row,
// tab-separated:
//
//	CLIENT_LIST	openvpn_25	172.18.0.3:49964	10.8.0.6		5859	5948	2026-10-09 16:22:43	1791562963	UNDEF	0	0	AES-256-GCM
//	            ^CommonName  ^RealAddress      ^VirtualAddress (IPv6 empty) ^BytesRecv ^BytesSent ^ConnectedSince          ^(time_t)   ^Username ^ClientID ^PeerID ^Cipher
func parseServerStatusLines(lines []string) ([]ClientInfo, error) {
	var clients []ClientInfo
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if fields[0] != "CLIENT_LIST" {
			continue
		}
		if len(fields) < 13 {
			return nil, fmt.Errorf("malformed CLIENT_LIST line: %q", line)
		}

		bytesRecv, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing bytes received for client %q: %w", fields[1], err)
		}
		bytesSent, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing bytes sent for client %q: %w", fields[1], err)
		}
		sec, err := strconv.ParseInt(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing connected-since for client %q: %w", fields[1], err)
		}

		clients = append(clients, ClientInfo{
			CommonName:     fields[1],
			RealAddress:    fields[2],
			VirtualAddress: fields[3],
			BytesReceived:  bytesRecv,
			BytesSent:      bytesSent,
			ConnectedSince: time.Unix(sec, 0),
			Username:       fields[9],
			Cipher:         fields[12],
		})
	}
	return clients, nil
}
