package openvpn

import (
	"testing"
	"time"
)

func TestParseStateLines(t *testing.T) {
	tests := []struct {
		name      string
		lines     []string
		want      string
		wantSince time.Time
		wantErr   bool
	}{
		{
			name:      "connected",
			lines:     []string{"1622547600,CONNECTED,SUCCESS,10.8.0.2,203.0.113.5,1194,,"},
			want:      "CONNECTED",
			wantSince: time.Unix(1622547600, 0),
		},
		{
			name:      "reconnecting",
			lines:     []string{"1622547600,RECONNECTING,internal-error,,,,,"},
			want:      "RECONNECTING",
			wantSince: time.Unix(1622547600, 0),
		},
		{
			name:      "uses last line when several are present",
			lines:     []string{"1622547500,CONNECTING,,,,,,", "1622547600,CONNECTED,SUCCESS,10.8.0.2,203.0.113.5,1194,,"},
			want:      "CONNECTED",
			wantSince: time.Unix(1622547600, 0),
		},
		{
			name:    "empty response",
			lines:   nil,
			wantErr: true,
		},
		{
			name:    "malformed line",
			lines:   []string{"not-a-state-line"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStateLines(tt.lines)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got state %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.State != tt.want {
				t.Errorf("State = %q, want %q", got.State, tt.want)
			}
			if !got.Since.Equal(tt.wantSince) {
				t.Errorf("Since = %v, want %v", got.Since, tt.wantSince)
			}
			if got.SinceErr != nil {
				t.Errorf("SinceErr = %v, want nil", got.SinceErr)
			}
		})
	}
}

func TestParseStateLines_UnparsableTimestampOnlyDropsSince(t *testing.T) {
	got, err := parseStateLines([]string{"not-a-timestamp,CONNECTED,SUCCESS,10.8.0.2,203.0.113.5,1194,,"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.State != "CONNECTED" {
		t.Errorf("State = %q, want CONNECTED", got.State)
	}
	if !got.Since.IsZero() {
		t.Errorf("Since = %v, want zero", got.Since)
	}
	if got.SinceErr == nil {
		t.Error("expected SinceErr to be set, got nil")
	}
}

func TestParseVersionLines(t *testing.T) {
	tests := []struct {
		name    string
		lines   []string
		want    string
		wantErr bool
	}{
		{
			name:  "real OpenVPN 2.6.20 response",
			lines: []string{"OpenVPN Version: OpenVPN 2.6.20 x86_64-alpine-linux-musl [SSL (OpenSSL)] [LZO] [LZ4] [EPOLL] [MH/PKTINFO] [AEAD]", "Management Version: 5"},
			want:  "2.6.20",
		},
		{
			name:  "real OpenVPN 2.5.6 response",
			lines: []string{"OpenVPN Version: OpenVPN 2.5.6 x86_64-alpine-linux-musl [SSL (OpenSSL)] [LZO] [LZ4] [EPOLL] [MH/PKTINFO] [AEAD] built on Apr 17 2022", "Management Version: 3"},
			want:  "2.5.6",
		},
		{
			name:    "missing OpenVPN Version line",
			lines:   []string{"Management Version: 5"},
			wantErr: true,
		},
		{
			name:    "empty response",
			lines:   nil,
			wantErr: true,
		},
		{
			name:    "prefix with nothing after it",
			lines:   []string{"OpenVPN Version: OpenVPN "},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVersionLines(tt.lines)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got version %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseStatusLines(t *testing.T) {
	lines := []string{
		"OpenVPN STATISTICS",
		"Updated,Mon Jan  1 00:00:00 2024",
		"TUN/TAP read bytes,100",
		"TUN/TAP write bytes,200",
		"TCP/UDP read bytes,300",
		"TCP/UDP write bytes,400",
		"Auth read bytes,500",
	}

	stats, err := parseStatusLines(lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TunReadBytes != 100 {
		t.Errorf("TunReadBytes = %d, want 100", stats.TunReadBytes)
	}
	if stats.TunWriteBytes != 200 {
		t.Errorf("TunWriteBytes = %d, want 200", stats.TunWriteBytes)
	}
	if stats.LinkReadBytes != 300 {
		t.Errorf("LinkReadBytes = %d, want 300", stats.LinkReadBytes)
	}
	if stats.LinkWriteBytes != 400 {
		t.Errorf("LinkWriteBytes = %d, want 400", stats.LinkWriteBytes)
	}
}

func TestParseStatusLinesMissingCounter(t *testing.T) {
	// Missing "TCP/UDP write bytes": must fail rather than return a
	// partial Stats with a zero value for it.
	lines := []string{
		"TUN/TAP read bytes,100",
		"TUN/TAP write bytes,200",
		"TCP/UDP read bytes,300",
	}

	if _, err := parseStatusLines(lines); err == nil {
		t.Fatal("expected an error for missing counter, got nil")
	}
}

func TestParseStatusLinesInvalidNumber(t *testing.T) {
	lines := []string{
		"TUN/TAP read bytes,not-a-number",
		"TUN/TAP write bytes,200",
		"TCP/UDP read bytes,300",
		"TCP/UDP write bytes,400",
	}

	if _, err := parseStatusLines(lines); err == nil {
		t.Fatal("expected an error for invalid number, got nil")
	}
}

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
