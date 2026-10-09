package mgmt

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

// realStatus3Lines are the lines of a real "status 3" response captured
// from a live OpenVPN 2.6.20 server with three connected clients, already
// split the way runCommand itself would hand them to a parser (CRLF
// stripped, "END" consumed).
var realStatus3Lines = []string{
	"TITLE\tOpenVPN 2.6.20 x86_64-alpine-linux-musl [SSL (OpenSSL)] [LZO] [LZ4] [EPOLL] [MH/PKTINFO] [AEAD]",
	"TIME\t2026-10-09 16:24:56\t1791563096",
	"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher",
	"CLIENT_LIST\topenvpn_25\t172.18.0.3:49964\t10.8.0.6\t\t5859\t5948\t2026-10-09 16:22:43\t1791562963\tUNDEF\t0\t0\tAES-256-GCM",
	"CLIENT_LIST\topenvpn_26\t172.18.0.5:51607\t10.8.0.14\t\t5941\t5968\t2026-10-09 16:22:44\t1791562964\tUNDEF\t2\t2\tAES-256-GCM",
	"CLIENT_LIST\topenvpn_27\t172.18.0.4:59830\t10.8.0.10\t\t7184\t6049\t2026-10-09 16:22:43\t1791562963\tUNDEF\t1\t1\tAES-256-GCM",
	"HEADER\tROUTING_TABLE\tVirtual Address\tCommon Name\tReal Address\tLast Ref\tLast Ref (time_t)",
	"ROUTING_TABLE\t10.8.0.10\topenvpn_27\t172.18.0.4:59830\t2026-10-09 16:22:43\t1791562963",
	"GLOBAL_STATS\tMax bcast/mcast queue length\t3",
	"GLOBAL_STATS\tdco_enabled\t0",
}

// realStatus3Response is the same real capture as realStatus3Lines, as the
// raw CRLF-terminated bytes a real server actually sends over the wire —
// used by the socket-level FetchServerStatus tests below.
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

func TestParseServerStatusLines(t *testing.T) {
	clients, err := parseServerStatusLines(realStatus3Lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clients) != 3 {
		t.Fatalf("got %d clients, want 3: %+v", len(clients), clients)
	}

	want := ClientInfo{
		CommonName:     "openvpn_26",
		RealAddress:    "172.18.0.5:51607",
		VirtualAddress: "10.8.0.14",
		BytesReceived:  5941,
		BytesSent:      5968,
		ConnectedSince: time.Unix(1791562964, 0),
		Username:       "UNDEF",
		Cipher:         "AES-256-GCM",
	}
	got := clients[1]
	if got != want {
		t.Errorf("clients[1] = %+v, want %+v", got, want)
	}
}

func TestParseServerStatusLines_NoClientsConnected(t *testing.T) {
	lines := []string{
		"TITLE\tOpenVPN 2.6.20",
		"TIME\t2026-10-09 16:24:56\t1791563096",
		"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher",
		"HEADER\tROUTING_TABLE\tVirtual Address\tCommon Name\tReal Address\tLast Ref\tLast Ref (time_t)",
		"GLOBAL_STATS\tMax bcast/mcast queue length\t0",
	}

	clients, err := parseServerStatusLines(lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clients) != 0 {
		t.Errorf("got %d clients, want 0: %+v", len(clients), clients)
	}
}

func TestParseServerStatusLines_MalformedClientListLine(t *testing.T) {
	lines := []string{"CLIENT_LIST\topenvpn_26\t172.18.0.5:51607"} // too few fields
	if _, err := parseServerStatusLines(lines); err == nil {
		t.Fatal("expected an error for a truncated CLIENT_LIST line, got nil")
	}
}

func TestParseServerStatusLines_NonNumericBytes(t *testing.T) {
	lines := []string{"CLIENT_LIST\topenvpn_26\t172.18.0.5:51607\t10.8.0.14\t\tnot-a-number\t5968\t2026-10-09 16:22:44\t1791562964\tUNDEF\t2\t2\tAES-256-GCM"}
	if _, err := parseServerStatusLines(lines); err == nil {
		t.Fatal("expected an error for a non-numeric bytes-received field, got nil")
	}
}

func TestParseServerStatusLines_NonNumericConnectedSince(t *testing.T) {
	lines := []string{"CLIENT_LIST\topenvpn_26\t172.18.0.5:51607\t10.8.0.14\t\t5941\t5968\t2026-10-09 16:22:44\tnot-a-timestamp\tUNDEF\t2\t2\tAES-256-GCM"}
	if _, err := parseServerStatusLines(lines); err == nil {
		t.Fatal("expected an error for a non-numeric connected-since field, got nil")
	}
}

func TestFetchServerStatus_HappyPath(t *testing.T) {
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

	client := &Client{Address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := client.FetchServerStatus(ctx)
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

func TestFetchServerStatus_VersionCommandFails(t *testing.T) {
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

	client := &Client{Address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := client.FetchServerStatus(ctx)
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
