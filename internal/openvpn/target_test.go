package openvpn

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

// startFakeServer listens on an ephemeral local port and runs handler
// against the first accepted connection, closing it afterwards. It returns
// the address to connect to.
func startFakeServer(t *testing.T, handler func(t *testing.T, conn net.Conn)) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		handler(t, conn)
	}()

	return ln.Addr().String()
}

func serverReadLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Errorf("server: reading line: %v", err)
		return ""
	}
	return line
}

func serveStateAndStatus(t *testing.T, conn net.Conn, r *bufio.Reader, state string) {
	t.Helper()

	if got := serverReadLine(t, r); got != "state\n" {
		t.Errorf("server: expected %q command, got %q", "state", got)
	}
	_, _ = conn.Write([]byte("1700000000," + state + ",SUCCESS,10.8.0.2,203.0.113.5,1194,,\r\nEND\r\n"))

	if got := serverReadLine(t, r); got != "status\n" {
		t.Errorf("server: expected %q command, got %q", "status", got)
	}
	_, _ = conn.Write([]byte(
		"OpenVPN STATISTICS\r\n" +
			"Updated,Mon Jan  1 00:00:00 2024\r\n" +
			"TUN/TAP read bytes,100\r\n" +
			"TUN/TAP write bytes,200\r\n" +
			"TCP/UDP read bytes,300\r\n" +
			"TCP/UDP write bytes,400\r\n" +
			"Auth read bytes,500\r\n" +
			"END\r\n",
	))

	if got := serverReadLine(t, r); got != "version\n" {
		t.Errorf("server: expected %q command, got %q", "version", got)
	}
	// Real OpenVPN 2.6.20 response, captured from a live management interface.
	_, _ = conn.Write([]byte(
		"OpenVPN Version: OpenVPN 2.6.20 x86_64-alpine-linux-musl [SSL (OpenSSL)] [LZO] [LZ4] [EPOLL] [MH/PKTINFO] [AEAD]\r\n" +
			"Management Version: 5\r\n" +
			"END\r\n",
	))
}

func TestTarget_FetchStats_RejectsServerMode(t *testing.T) {
	target := &Target{ManagementAddress: "127.0.0.1:1", Mode: ModeServer}
	if _, err := target.FetchStats(context.Background()); err == nil {
		t.Fatal("expected an error calling FetchStats on a server-mode target, got nil")
	}
}

func TestTarget_FetchStats_NoPasswordRequired(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
		r := bufio.NewReader(conn)
		serveStateAndStatus(t, conn, r, "CONNECTED")
	})

	target := &Target{ManagementAddress: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stats, err := target.FetchStats(ctx)
	if err != nil {
		t.Fatalf("FetchStats: %v", err)
	}
	if stats.State != "CONNECTED" {
		t.Errorf("State = %q, want CONNECTED", stats.State)
	}
	if !stats.StateSince.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("StateSince = %v, want %v", stats.StateSince, time.Unix(1700000000, 0))
	}
	if stats.StateSinceErr != nil {
		t.Errorf("StateSinceErr = %v, want nil", stats.StateSinceErr)
	}
	if stats.Version != "2.6.20" {
		t.Errorf("Version = %q, want 2.6.20", stats.Version)
	}
	if stats.VersionErr != nil {
		t.Errorf("VersionErr = %v, want nil", stats.VersionErr)
	}
	if stats.TunReadBytes != 100 || stats.TunWriteBytes != 200 || stats.LinkReadBytes != 300 || stats.LinkWriteBytes != 400 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

func TestTarget_FetchStats_VersionCommandFails(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
		r := bufio.NewReader(conn)

		if got := serverReadLine(t, r); got != "state\n" {
			t.Errorf("server: expected %q command, got %q", "state", got)
		}
		_, _ = conn.Write([]byte("1700000000,CONNECTED,SUCCESS,10.8.0.2,203.0.113.5,1194,,\r\nEND\r\n"))

		if got := serverReadLine(t, r); got != "status\n" {
			t.Errorf("server: expected %q command, got %q", "status", got)
		}
		_, _ = conn.Write([]byte(
			"TUN/TAP read bytes,100\r\n" +
				"TUN/TAP write bytes,200\r\n" +
				"TCP/UDP read bytes,300\r\n" +
				"TCP/UDP write bytes,400\r\n" +
				"END\r\n",
		))

		if got := serverReadLine(t, r); got != "version\n" {
			t.Errorf("server: expected %q command, got %q", "version", got)
		}
		_, _ = conn.Write([]byte("ERROR: unknown command\r\n"))
	})

	target := &Target{ManagementAddress: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stats, err := target.FetchStats(ctx)
	if err != nil {
		t.Fatalf("FetchStats should not fail when only the version command fails: %v", err)
	}
	if stats.State != "CONNECTED" {
		t.Errorf("State = %q, want CONNECTED", stats.State)
	}
	if stats.Version != "" {
		t.Errorf("Version = %q, want empty", stats.Version)
	}
	if stats.VersionErr == nil {
		t.Error("expected VersionErr to be set, got nil")
	}
}

func TestTarget_FetchStats_CorrectPassword(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte("ENTER PASSWORD:"))
		r := bufio.NewReader(conn)
		got := serverReadLine(t, r)
		if got != "hunter2\n" {
			t.Errorf("server: expected password %q, got %q", "hunter2", got)
		}
		_, _ = conn.Write([]byte("SUCCESS: password is correct\r\n"))
		serveStateAndStatus(t, conn, r, "CONNECTED")
	})

	target := &Target{ManagementAddress: addr, Password: "hunter2"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stats, err := target.FetchStats(ctx)
	if err != nil {
		t.Fatalf("FetchStats: %v", err)
	}
	if stats.State != "CONNECTED" {
		t.Errorf("State = %q, want CONNECTED", stats.State)
	}
}

func TestTarget_FetchStats_WrongPassword(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte("ENTER PASSWORD:"))
		r := bufio.NewReader(conn)
		_ = serverReadLine(t, r)
		_, _ = conn.Write([]byte("ERROR: bad password\r\n"))
	})

	target := &Target{ManagementAddress: addr, Password: "wrong"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := target.FetchStats(ctx); err == nil {
		t.Fatal("expected an authentication error, got nil")
	}
}

func TestTarget_FetchStats_PasswordRequiredButNotConfigured(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte("ENTER PASSWORD:"))
	})

	target := &Target{ManagementAddress: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := target.FetchStats(ctx); err == nil {
		t.Fatal("expected an error when no password is configured, got nil")
	}
}

func TestTarget_FetchStats_Timeout(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		// Never write anything; the client should time out waiting for
		// the greeting.
		<-t.Context().Done()
	})

	target := &Target{ManagementAddress: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := target.FetchStats(ctx); err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("FetchStats took too long to time out: %v", elapsed)
	}
}

func TestTarget_FetchStats_ConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens here anymore

	target := &Target{ManagementAddress: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := target.FetchStats(ctx); err == nil {
		t.Fatal("expected a dial error, got nil")
	}
}

func TestTarget_FetchStats_MalformedStatusResponse(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
		r := bufio.NewReader(conn)

		if got := serverReadLine(t, r); got != "state\n" {
			t.Errorf("server: expected %q command, got %q", "state", got)
		}
		_, _ = conn.Write([]byte("1700000000,CONNECTED,SUCCESS,10.8.0.2,203.0.113.5,1194,,\r\nEND\r\n"))

		if got := serverReadLine(t, r); got != "status\n" {
			t.Errorf("server: expected %q command, got %q", "status", got)
		}
		// Missing "TCP/UDP write bytes" on purpose.
		_, _ = conn.Write([]byte(
			"TUN/TAP read bytes,100\r\n" +
				"TUN/TAP write bytes,200\r\n" +
				"TCP/UDP read bytes,300\r\n" +
				"END\r\n",
		))
	})

	target := &Target{ManagementAddress: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := target.FetchStats(ctx); err == nil {
		t.Fatal("expected an error for malformed status response, got nil")
	}
}

// realStatus3Response is a real "status 3" response captured from a live
// OpenVPN 2.6.20 server with three connected clients, as the raw
// CRLF-terminated bytes it actually sends over the wire.
const realStatus3Response = "TITLE\tOpenVPN 2.6.20 x86_64-alpine-linux-musl [SSL (OpenSSL)] [LZO] [LZ4] [EPOLL] [MH/PKTINFO] [AEAD]\r\n" +
	"TIME\t2026-10-09 16:24:56\t1791563096\r\n" +
	"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher\r\n" +
	"CLIENT_LIST\topenvpn_25\t172.18.0.3:49964\t10.8.0.6\t\t5859\t5948\t2026-10-09 16:22:43\t1791562963\tUNDEF\t0\t0\tAES-256-GCM\r\n" +
	"CLIENT_LIST\topenvpn_26\t172.18.0.5:51607\t10.8.0.14\t\t5941\t5968\t2026-10-09 16:22:44\t1791562964\tUNDEF\t2\t2\tAES-256-GCM\r\n" +
	"CLIENT_LIST\topenvpn_27\t172.18.0.4:59830\t10.8.0.10\t\t7184\t6049\t2026-10-09 16:22:43\t1791562963\tUNDEF\t1\t1\tAES-256-GCM\r\n" +
	"HEADER\tROUTING_TABLE\tVirtual Address\tCommon Name\tReal Address\tLast Ref\tLast Ref (time_t)\r\n" +
	"ROUTING_TABLE\t10.8.0.10\topenvpn_27\t172.18.0.4:59830\t2026-10-09 16:22:43\t1791562963\r\n" +
	"GLOBAL_STATS\tMax bcast/mcast queue length\t3\r\n" +
	"GLOBAL_STATS\tdco_enabled\t0\r\n" +
	"END\r\n"

func TestTarget_FetchServerStatus_RejectsClientMode(t *testing.T) {
	target := &Target{ManagementAddress: "127.0.0.1:1", Mode: ModeClient}
	if _, err := target.FetchServerStatus(context.Background()); err == nil {
		t.Fatal("expected an error calling FetchServerStatus on a client-mode target, got nil")
	}
}

func TestTarget_FetchServerStatus_HappyPath(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
		r := bufio.NewReader(conn)

		if got := serverReadLine(t, r); got != "status 3\n" {
			t.Errorf("server: expected %q command, got %q", "status 3", got)
		}
		_, _ = conn.Write([]byte(realStatus3Response))

		if got := serverReadLine(t, r); got != "version\n" {
			t.Errorf("server: expected %q command, got %q", "version", got)
		}
		_, _ = conn.Write([]byte(
			"OpenVPN Version: OpenVPN 2.6.20 x86_64-alpine-linux-musl [SSL (OpenSSL)] [LZO] [LZ4] [EPOLL] [MH/PKTINFO] [AEAD]\r\n" +
				"Management Version: 5\r\n" +
				"END\r\n",
		))
	})

	target := &Target{ManagementAddress: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := target.FetchServerStatus(ctx)
	if err != nil {
		t.Fatalf("FetchServerStatus: %v", err)
	}
	if len(status.Clients) != 3 {
		t.Fatalf("got %d clients, want 3: %+v", len(status.Clients), status.Clients)
	}
	if status.Clients[0].CommonName != "openvpn_25" {
		t.Errorf("Clients[0].CommonName = %q, want openvpn_25", status.Clients[0].CommonName)
	}
	if status.Version != "2.6.20" {
		t.Errorf("Version = %q, want 2.6.20", status.Version)
	}
	if status.VersionErr != nil {
		t.Errorf("VersionErr = %v, want nil", status.VersionErr)
	}
}

func TestTarget_FetchServerStatus_VersionCommandFails(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
		r := bufio.NewReader(conn)

		if got := serverReadLine(t, r); got != "status 3\n" {
			t.Errorf("server: expected %q command, got %q", "status 3", got)
		}
		_, _ = conn.Write([]byte(realStatus3Response))

		if got := serverReadLine(t, r); got != "version\n" {
			t.Errorf("server: expected %q command, got %q", "version", got)
		}
		_, _ = conn.Write([]byte("ERROR: unknown command\r\n"))
	})

	target := &Target{ManagementAddress: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := target.FetchServerStatus(ctx)
	if err != nil {
		t.Fatalf("FetchServerStatus should not fail when only the version command fails: %v", err)
	}
	if len(status.Clients) != 3 {
		t.Errorf("got %d clients, want 3", len(status.Clients))
	}
	if status.Version != "" {
		t.Errorf("Version = %q, want empty", status.Version)
	}
	if status.VersionErr == nil {
		t.Error("expected VersionErr to be set, got nil")
	}
}

func TestTarget_Certificates_ConfigPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/ca.crt", generateCert(t, "Target CA", time.Now().Add(24*time.Hour)))
	configPath := dir + "/client.conf"
	writeFile(t, configPath, "client\nca ca.crt\n")

	target := &Target{ConfigPath: configPath}
	certs, err := target.Certificates()
	if err != nil {
		t.Fatalf("Certificates: %v", err)
	}
	if len(certs) != 1 || certs[0].Subject != "Target CA" {
		t.Fatalf("got %+v, want a single 'Target CA' certificate", certs)
	}
}

func TestTarget_Certificates_CertPath(t *testing.T) {
	dir := t.TempDir()
	certPath := dir + "/tls.crt"
	writeFile(t, certPath, generateCert(t, "Target Client", time.Now().Add(24*time.Hour)))

	target := &Target{CertPath: certPath}
	certs, err := target.Certificates()
	if err != nil {
		t.Fatalf("Certificates: %v", err)
	}
	if len(certs) != 1 || certs[0].Subject != "Target Client" {
		t.Fatalf("got %+v, want a single 'Target Client' certificate", certs)
	}
}

func TestTarget_Certificates_NeitherPathSet(t *testing.T) {
	target := &Target{}
	certs, err := target.Certificates()
	if err != nil {
		t.Fatalf("Certificates: %v", err)
	}
	if certs != nil {
		t.Fatalf("got %+v, want nil", certs)
	}
}
