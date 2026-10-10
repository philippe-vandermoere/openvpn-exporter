package openvpn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Target is a single OpenVPN client tunnel or server process to monitor:
// its identity (Name/Mode), where to reach its management interface
// (ManagementAddress/Password), and where to read its certificates from
// (ConfigPath/CertPath, see Certificates).
//
// Mode is excluded from YAML (yaml:"-") deliberately: it's derived from
// which list (tunnels:/servers:) or discovery path a Target came from,
// never set by the user directly — a contradictory "mode: client" under
// servers: would otherwise be possible.
type Target struct {
	Name              string `yaml:"name"`
	Mode              Mode   `yaml:"-"`
	ManagementAddress string `yaml:"management_address"`
	ConfigPath        string `yaml:"config_path"`
	CertPath          string `yaml:"cert_path"`
	Password          string `yaml:"password"`
}

// Certificates returns this target's CA/certificate(s): parsed from
// ConfigPath if set, read directly from CertPath otherwise, or (nil, nil)
// if neither is set.
func (t *Target) Certificates() ([]Certificate, error) {
	switch {
	case t.ConfigPath != "":
		parsed, err := ParseConfig(t.ConfigPath)
		if err != nil {
			return nil, err
		}
		return parsed.Certificates, nil
	case t.CertPath != "":
		return LoadCertOnly(t.CertPath)
	default:
		return nil, nil
	}
}

// FetchStats opens a new connection to the management interface, reads the
// current state and traffic counters, and closes the connection. It honors
// ctx's deadline for the whole exchange.
func (t *Target) FetchStats(ctx context.Context) (*Stats, error) {
	if t.Mode == ModeServer {
		return nil, fmt.Errorf("%s: FetchStats called on a server-mode target, use FetchServerStatus instead", t.Name)
	}

	r, conn, err := t.dialAndAuthenticate(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	stateLines, err := runCommand(r, conn, "state")
	if err != nil {
		return nil, fmt.Errorf("state command: %w", err)
	}
	stateRes, err := parseStateLines(stateLines)
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

	stats.State = stateRes.State
	stats.StateSince = stateRes.Since
	stats.StateSinceErr = stateRes.SinceErr

	// A failed/unparsable "version" command is not fatal: it's stored on
	// Stats rather than returned, so the caller can decide how to log it
	// without the tunnel being reported as down.
	versionLines, err := runCommand(r, conn, "version")
	if err != nil {
		stats.VersionErr = fmt.Errorf("version command: %w", err)
	} else if v, verr := parseVersionLines(versionLines); verr != nil {
		stats.VersionErr = fmt.Errorf("version command: %w", verr)
	} else {
		stats.Version = v
	}

	return stats, nil
}

// FetchServerStatus opens a new connection to an OpenVPN server's management
// interface, reads the list of connected clients and the server's version,
// and closes the connection. It honors ctx's deadline for the whole
// exchange.
func (t *Target) FetchServerStatus(ctx context.Context) (*ServerStatus, error) {
	if t.Mode == ModeClient {
		return nil, fmt.Errorf("%s: FetchServerStatus called on a client-mode target, use FetchStats instead", t.Name)
	}

	r, conn, err := t.dialAndAuthenticate(ctx)
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

// dialAndAuthenticate opens a new connection to the management interface,
// applies ctx's deadline to it, and authenticates (if the interface requires
// a password). The caller owns the returned connection and must close it.
func (t *Target) dialAndAuthenticate(ctx context.Context) (*bufio.Reader, net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", t.ManagementAddress)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", t.ManagementAddress, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("set deadline: %w", err)
		}
	}

	r := bufio.NewReader(conn)

	if err := t.authenticate(r, conn); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	return r, conn, nil
}

// authenticate consumes the management interface's initial greeting and, if
// it turns out to be a password prompt, sends the configured password and
// checks the acknowledgement.
//
// The prompt ("ENTER PASSWORD:") is not newline-terminated, unlike every
// other line the management interface sends, so the greeting is read one
// byte at a time until either a newline (plain banner, no auth needed) or
// the prompt suffix is seen.
func (t *Target) authenticate(r *bufio.Reader, w net.Conn) error {
	needsPassword, err := readGreeting(r)
	if err != nil {
		return fmt.Errorf("reading management interface greeting: %w", err)
	}
	if !needsPassword {
		return nil
	}
	if t.Password == "" {
		return errors.New("management interface requires a password but none is configured")
	}

	if _, err := w.Write([]byte(t.Password + "\n")); err != nil {
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
