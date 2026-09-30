// Package collector implements the prometheus.Collector that scrapes every
// configured tunnel's management interface and certificate files on each
// call to Collect.
package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pvandermoere/openvpn-exporter/internal/certs"
	"github.com/pvandermoere/openvpn-exporter/internal/mgmt"
)

// Tunnel is a single tunnel target to scrape.
//
// Certificate expiry: if ConfigPath is set, it is parsed to find both the CA
// and client certificates. Otherwise, if CertPath is set, only that
// certificate's expiry is tracked. If neither is set, certificate expiry
// isn't checked for this tunnel. ConfigPath takes precedence when both are
// set.
type Tunnel struct {
	Name              string
	ManagementAddress string
	ConfigPath        string
	CertPath          string
	Password          string
	Timeout           time.Duration
}

// Collector scrapes every configured tunnel concurrently on each Collect
// call.
type Collector struct {
	tunnels []Tunnel
	logger  *slog.Logger
}

// New builds a Collector for the given tunnels. logger may be nil, in which
// case scrape errors are discarded.
func New(tunnels []Tunnel, logger *slog.Logger) *Collector {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Collector{tunnels: tunnels, logger: logger}
}

var (
	upDesc = prometheus.NewDesc(
		"openvpn_tunnel_up",
		"Whether the tunnel's management interface was reachable and reported a state (1) or not (0).",
		[]string{"tunnel"}, nil,
	)
	stateDesc = prometheus.NewDesc(
		"openvpn_tunnel_state",
		"Current OpenVPN state reported by the management interface, set to 1 for the active state.",
		[]string{"tunnel", "state"}, nil,
	)
	bytesDesc = prometheus.NewDesc(
		"openvpn_tunnel_bytes_total",
		"Cumulative bytes transferred, reset whenever the OpenVPN process restarts.",
		[]string{"tunnel", "channel", "direction"}, nil,
	)
	certExpiryDesc = prometheus.NewDesc(
		"openvpn_tunnel_cert_expiry_timestamp_seconds",
		"Certificate expiry date as a Unix timestamp.",
		[]string{"tunnel", "role", "subject"}, nil,
	)
	scrapeDurationDesc = prometheus.NewDesc(
		"openvpn_tunnel_scrape_duration_seconds",
		"Duration of the last management interface scrape, including failed attempts.",
		[]string{"tunnel"}, nil,
	)
)

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- upDesc
	ch <- stateDesc
	ch <- bytesDesc
	ch <- certExpiryDesc
	ch <- scrapeDurationDesc
}

// Collect implements prometheus.Collector. Every tunnel is scraped
// concurrently so that one slow or unreachable management interface does
// not delay the others.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, t := range c.tunnels {
		wg.Add(1)
		go func(t Tunnel) {
			defer wg.Done()
			c.collectTunnel(ch, t)
		}(t)
	}
	wg.Wait()
}

func (c *Collector) collectTunnel(ch chan<- prometheus.Metric, t Tunnel) {
	client := &mgmt.Client{Address: t.ManagementAddress, Password: t.Password}

	ctx, cancel := context.WithTimeout(context.Background(), t.Timeout)
	defer cancel()

	start := time.Now()
	stats, err := client.FetchStats(ctx)
	duration := time.Since(start)

	ch <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue, duration.Seconds(), t.Name)

	if err != nil {
		c.logger.Warn("tunnel scrape failed, reporting as down", "tunnel", t.Name, "error", err)
		ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, 0, t.Name)
	} else {
		ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, 1, t.Name)
		ch <- prometheus.MustNewConstMetric(stateDesc, prometheus.GaugeValue, 1, t.Name, stats.State)

		emitBytes := func(channel, direction string, value uint64) {
			ch <- prometheus.MustNewConstMetric(bytesDesc, prometheus.CounterValue, float64(value), t.Name, channel, direction)
		}
		emitBytes("tunnel", "in", stats.TunReadBytes)
		emitBytes("tunnel", "out", stats.TunWriteBytes)
		emitBytes("link", "in", stats.LinkReadBytes)
		emitBytes("link", "out", stats.LinkWriteBytes)
	}

	var certList []certs.Certificate
	switch {
	case t.ConfigPath != "":
		certList, err = certs.Load(t.ConfigPath)
	case t.CertPath != "":
		certList, err = certs.LoadCertOnly(t.CertPath)
	default:
		return
	}
	if err != nil {
		c.logger.Warn("certificate read failed", "tunnel", t.Name, "error", err)
		return
	}
	for _, cert := range certList {
		ch <- prometheus.MustNewConstMetric(
			certExpiryDesc, prometheus.GaugeValue, float64(cert.NotAfter.Unix()),
			t.Name, cert.Role, cert.Subject,
		)
	}
}
