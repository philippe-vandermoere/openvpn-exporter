package collector

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pvandermoere/openvpn-exporter/internal/openvpn"
)

var (
	tunnelUpDesc = prometheus.NewDesc(
		"openvpn_tunnel_up",
		"Whether the tunnel's management interface was reachable and reported a state (1) or not (0).",
		[]string{"tunnel"}, nil,
	)
	tunnelStateDesc = prometheus.NewDesc(
		"openvpn_tunnel_state",
		"Current OpenVPN state reported by the management interface, set to 1 for the active state.",
		[]string{"tunnel", "state"}, nil,
	)
	tunnelBytesDesc = prometheus.NewDesc(
		"openvpn_tunnel_bytes_total",
		"Cumulative bytes transferred, reset whenever the OpenVPN process restarts.",
		[]string{"tunnel", "channel", "direction"}, nil,
	)
	tunnelCertExpiryDesc = prometheus.NewDesc(
		"openvpn_tunnel_cert_expiry_timestamp_seconds",
		"Certificate expiry date as a Unix timestamp.",
		[]string{"tunnel", "role", "subject"}, nil,
	)
	tunnelScrapeDurationDesc = prometheus.NewDesc(
		"openvpn_tunnel_scrape_duration_seconds",
		"Duration of the last management interface scrape, including failed attempts.",
		[]string{"tunnel"}, nil,
	)
	tunnelInfoDesc = prometheus.NewDesc(
		"openvpn_tunnel_info",
		"Always 1; carries the OpenVPN version running this tunnel as a label.",
		[]string{"tunnel", "version"}, nil,
	)
	tunnelStateSinceDesc = prometheus.NewDesc(
		"openvpn_tunnel_state_since_timestamp_seconds",
		"Unix timestamp at which the tunnel entered its current state (see openvpn_tunnel_state).",
		[]string{"tunnel"}, nil,
	)
)

// describeTunnel sends every openvpn_tunnel_* descriptor, unconditionally
// (required by prometheus.Collector regardless of whether any tunnels are
// currently configured).
func describeTunnel(ch chan<- *prometheus.Desc) {
	ch <- tunnelUpDesc
	ch <- tunnelStateDesc
	ch <- tunnelBytesDesc
	ch <- tunnelCertExpiryDesc
	ch <- tunnelScrapeDurationDesc
	ch <- tunnelInfoDesc
	ch <- tunnelStateSinceDesc
}

func (c *Collector) collectTunnel(ch chan<- prometheus.Metric, t openvpn.Target) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	start := time.Now()
	stats, err := t.FetchStats(ctx)
	duration := time.Since(start)

	ch <- prometheus.MustNewConstMetric(tunnelScrapeDurationDesc, prometheus.GaugeValue, duration.Seconds(), t.Name)

	if err != nil {
		c.logger.Warn("tunnel scrape failed, reporting as down", "tunnel", t.Name, "error", err)
		ch <- prometheus.MustNewConstMetric(tunnelUpDesc, prometheus.GaugeValue, 0, t.Name)
	} else {
		ch <- prometheus.MustNewConstMetric(tunnelUpDesc, prometheus.GaugeValue, 1, t.Name)
		ch <- prometheus.MustNewConstMetric(tunnelStateDesc, prometheus.GaugeValue, 1, t.Name, stats.State)

		emitBytes := func(channel, direction string, value uint64) {
			ch <- prometheus.MustNewConstMetric(tunnelBytesDesc, prometheus.CounterValue, float64(value), t.Name, channel, direction)
		}
		emitBytes("tunnel", "in", stats.TunReadBytes)
		emitBytes("tunnel", "out", stats.TunWriteBytes)
		emitBytes("link", "in", stats.LinkReadBytes)
		emitBytes("link", "out", stats.LinkWriteBytes)

		if stats.Version != "" {
			c.clearVersionWarned(t)
			ch <- prometheus.MustNewConstMetric(tunnelInfoDesc, prometheus.GaugeValue, 1, t.Name, stats.Version)
		} else if stats.VersionErr != nil {
			c.warnVersionOnce(t, stats.VersionErr)
		}

		if !stats.StateSince.IsZero() {
			ch <- prometheus.MustNewConstMetric(tunnelStateSinceDesc, prometheus.GaugeValue, float64(stats.StateSince.Unix()), t.Name)
		} else if stats.StateSinceErr != nil {
			c.logger.Warn("tunnel state timestamp unavailable", "tunnel", t.Name, "error", stats.StateSinceErr)
		}
	}

	certs, err := t.Certificates()
	if err != nil {
		c.logger.Warn("certificate read failed", "tunnel", t.Name, "error", err)
		return
	}
	for _, cert := range certs {
		ch <- prometheus.MustNewConstMetric(
			tunnelCertExpiryDesc, prometheus.GaugeValue, float64(cert.NotAfter.Unix()),
			t.Name, cert.Role, cert.Subject,
		)
	}
}
