package mgmt

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

func TestFetchStats_NoPasswordRequired(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte(">INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info\r\n"))
		r := bufio.NewReader(conn)
		serveStateAndStatus(t, conn, r, "CONNECTED")
	})

	client := &Client{Address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stats, err := client.FetchStats(ctx)
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

func TestFetchStats_VersionCommandFails(t *testing.T) {
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

	client := &Client{Address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stats, err := client.FetchStats(ctx)
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

func TestFetchStats_CorrectPassword(t *testing.T) {
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

	client := &Client{Address: addr, Password: "hunter2"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stats, err := client.FetchStats(ctx)
	if err != nil {
		t.Fatalf("FetchStats: %v", err)
	}
	if stats.State != "CONNECTED" {
		t.Errorf("State = %q, want CONNECTED", stats.State)
	}
}

func TestFetchStats_WrongPassword(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte("ENTER PASSWORD:"))
		r := bufio.NewReader(conn)
		_ = serverReadLine(t, r)
		_, _ = conn.Write([]byte("ERROR: bad password\r\n"))
	})

	client := &Client{Address: addr, Password: "wrong"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.FetchStats(ctx); err == nil {
		t.Fatal("expected an authentication error, got nil")
	}
}

func TestFetchStats_PasswordRequiredButNotConfigured(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		_, _ = conn.Write([]byte("ENTER PASSWORD:"))
	})

	client := &Client{Address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.FetchStats(ctx); err == nil {
		t.Fatal("expected an error when no password is configured, got nil")
	}
}

func TestFetchStats_Timeout(t *testing.T) {
	addr := startFakeServer(t, func(t *testing.T, conn net.Conn) {
		// Never write anything; the client should time out waiting for
		// the greeting.
		<-t.Context().Done()
	})

	client := &Client{Address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := client.FetchStats(ctx); err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("FetchStats took too long to time out: %v", elapsed)
	}
}

func TestFetchStats_ConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens here anymore

	client := &Client{Address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.FetchStats(ctx); err == nil {
		t.Fatal("expected a dial error, got nil")
	}
}

func TestFetchStats_MalformedStatusResponse(t *testing.T) {
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

	client := &Client{Address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.FetchStats(ctx); err == nil {
		t.Fatal("expected an error for malformed status response, got nil")
	}
}
