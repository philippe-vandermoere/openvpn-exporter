package mgmt

import (
	"fmt"
	"strconv"
	"strings"
)

// parseStateLines extracts the state field from the response to the "state"
// command. The expected format for a single state line is a comma-separated
// list: timestamp,state,detail,local-ip,remote-ip,... The last line is used
// in case a stray line preceded it.
func parseStateLines(lines []string) (string, error) {
	if len(lines) == 0 {
		return "", fmt.Errorf("empty response")
	}
	fields := strings.Split(lines[len(lines)-1], ",")
	if len(fields) < 2 || fields[1] == "" {
		return "", fmt.Errorf("unexpected state line format: %q", lines[len(lines)-1])
	}
	return fields[1], nil
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
