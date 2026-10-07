package mgmt

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
