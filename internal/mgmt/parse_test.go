package mgmt

import "testing"

func TestParseStateLines(t *testing.T) {
	tests := []struct {
		name    string
		lines   []string
		want    string
		wantErr bool
	}{
		{
			name:  "connected",
			lines: []string{"1622547600,CONNECTED,SUCCESS,10.8.0.2,203.0.113.5,1194,,"},
			want:  "CONNECTED",
		},
		{
			name:  "reconnecting",
			lines: []string{"1622547600,RECONNECTING,internal-error,,,,,"},
			want:  "RECONNECTING",
		},
		{
			name:  "uses last line when several are present",
			lines: []string{"1622547500,CONNECTING,,,,,,", "1622547600,CONNECTED,SUCCESS,10.8.0.2,203.0.113.5,1194,,"},
			want:  "CONNECTED",
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
					t.Fatalf("expected an error, got state %q", got)
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
