package mgmt

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

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
