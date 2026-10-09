package mgmt

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

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

// FetchServerStatus opens a new connection to an OpenVPN server's management
// interface, reads the list of connected clients and the server's version,
// and closes the connection. It honors ctx's deadline for the whole
// exchange.
func (c *Client) FetchServerStatus(ctx context.Context) (*ServerStatus, error) {
	r, conn, err := c.dialAndAuthenticate(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	statusLines, err := runCommand(r, conn, "status 3")
	if err != nil {
		return nil, fmt.Errorf("status command: %w", err)
	}
	clients, err := parseServerStatusLines(statusLines)
	if err != nil {
		return nil, fmt.Errorf("status command: %w", err)
	}
	status := &ServerStatus{Clients: clients}

	versionLines, err := runCommand(r, conn, "version")
	if err != nil {
		status.VersionErr = fmt.Errorf("version command: %w", err)
	} else if v, verr := parseVersionLines(versionLines); verr != nil {
		status.VersionErr = fmt.Errorf("version command: %w", verr)
	} else {
		status.Version = v
	}

	return status, nil
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
