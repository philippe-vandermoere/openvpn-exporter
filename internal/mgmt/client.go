// Package mgmt implements a minimal client for the OpenVPN management
// interface, enough to read the current tunnel state and traffic counters.
//
// The management interface accepts only one client connection at a time, so
// callers are expected to open a short-lived connection per scrape and close
// it immediately afterwards.
package mgmt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Stats holds the values read from the management interface for a single
// scrape.
type Stats struct {
	// State is the raw OpenVPN state string (e.g. CONNECTED, RECONNECTING).
	State string

	// TunReadBytes/TunWriteBytes are the plaintext tunnel-side counters
	// ("TUN/TAP read/write bytes").
	TunReadBytes  uint64
	TunWriteBytes uint64

	// LinkReadBytes/LinkWriteBytes are the encrypted link-side counters
	// ("TCP/UDP read/write bytes").
	LinkReadBytes  uint64
	LinkWriteBytes uint64
}

// Client talks to a single OpenVPN management interface endpoint.
type Client struct {
	// Address is the management interface address, e.g. "127.0.0.1:7505".
	Address string

	// Password is sent if the management interface prompts for one. Left
	// empty for unprotected management interfaces.
	Password string
}

// FetchStats opens a new connection to the management interface, reads the
// current state and traffic counters, and closes the connection. It honors
// ctx's deadline for the whole exchange.
func (c *Client) FetchStats(ctx context.Context) (*Stats, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", c.Address, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set deadline: %w", err)
		}
	}

	r := bufio.NewReader(conn)

	if err := c.authenticate(r, conn); err != nil {
		return nil, err
	}

	stateLines, err := runCommand(r, conn, "state")
	if err != nil {
		return nil, fmt.Errorf("state command: %w", err)
	}
	state, err := parseStateLines(stateLines)
	if err != nil {
		return nil, fmt.Errorf("state command: %w", err)
	}

	statusLines, err := runCommand(r, conn, "status")
	if err != nil {
		return nil, fmt.Errorf("status command: %w", err)
	}
	stats, err := parseStatusLines(statusLines)
	if err != nil {
		return nil, fmt.Errorf("status command: %w", err)
	}

	stats.State = state
	return stats, nil
}

// authenticate consumes the management interface's initial greeting and, if
// it turns out to be a password prompt, sends the configured password and
// checks the acknowledgement.
//
// The prompt ("ENTER PASSWORD:") is not newline-terminated, unlike every
// other line the management interface sends, so the greeting is read one
// byte at a time until either a newline (plain banner, no auth needed) or
// the prompt suffix is seen.
func (c *Client) authenticate(r *bufio.Reader, w net.Conn) error {
	needsPassword, err := readGreeting(r)
	if err != nil {
		return fmt.Errorf("reading management interface greeting: %w", err)
	}
	if !needsPassword {
		return nil
	}
	if c.Password == "" {
		return errors.New("management interface requires a password but none is configured")
	}

	if _, err := w.Write([]byte(c.Password + "\n")); err != nil {
		return fmt.Errorf("sending password: %w", err)
	}

	ack, err := r.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading authentication response: %w", err)
	}
	ack = strings.TrimSpace(ack)
	if !strings.HasPrefix(ack, "SUCCESS") {
		return fmt.Errorf("authentication failed: %s", ack)
	}
	return nil
}

const passwordPrompt = "ENTER PASSWORD:"

func readGreeting(r *bufio.Reader) (needsPassword bool, err error) {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return false, err
		}
		buf = append(buf, b)
		if b == '\n' {
			return false, nil
		}
		if strings.HasSuffix(string(buf), passwordPrompt) {
			return true, nil
		}
	}
}

// runCommand sends cmd followed by a newline and collects the response
// lines up to the terminating "END" line, skipping asynchronous
// notification lines (prefixed with ">") that may be interleaved.
func runCommand(r *bufio.Reader, w net.Conn, cmd string) ([]string, error) {
	if _, err := w.Write([]byte(cmd + "\n")); err != nil {
		return nil, fmt.Errorf("sending command %q: %w", cmd, err)
	}

	var lines []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading response: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")

		switch {
		case strings.HasPrefix(line, ">"):
			continue
		case line == "END":
			return lines, nil
		case strings.HasPrefix(line, "ERROR:"):
			return nil, fmt.Errorf("command %q failed: %s", cmd, line)
		default:
			lines = append(lines, line)
		}
	}
}
