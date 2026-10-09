package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pvandermoere/openvpn-exporter/internal/mgmt"
)

// Server is a single OpenVPN server target to scrape.
type Server struct {
	Name              string
	ManagementAddress string
	Password          string
	Timeout           time.Duration
}

// ServerCollector scrapes every configured OpenVPN server concurrently on
// each Collect call. Kept separate from Collector: a server's management
// interface describes a fundamentally different entity (a variable number
// of connected clients) than a tunnel's (a fixed per-tunnel metric set).
type ServerCollector struct {
	servers []Server
	logger  *slog.Logger

	// versionWarned mirrors Collector's own field (same warn-once-per-name
	// mechanism for "version" command failures) but isn't shared with it:
	// the two collector types are otherwise unrelated.
	versionWarnedMu sync.Mutex
	versionWarned   map[string]bool
}

// NewServerCollector builds a ServerCollector for the given servers. logger
// may be nil, in which case scrape errors are discarded.
func NewServerCollector(servers []Server, logger *slog.Logger) *ServerCollector {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &ServerCollector{servers: servers, logger: logger, versionWarned: make(map[string]bool)}
}

func (c *ServerCollector) warnVersionOnce(server string, err error) {
	c.versionWarnedMu.Lock()
	alreadyWarned := c.versionWarned[server]
	c.versionWarned[server] = true
	c.versionWarnedMu.Unlock()
	if !alreadyWarned {
		c.logger.Warn("server version unavailable", "server", server, "error", err)
	}
}

func (c *ServerCollector) clearVersionWarned(server string) {
	c.versionWarnedMu.Lock()
	delete(c.versionWarned, server)
	c.versionWarnedMu.Unlock()
}

var (
	serverUpDesc = prometheus.NewDesc(
		"openvpn_server_up",
		"Whether the server's management interface was reachable (1) or not (0).",
		[]string{"server"}, nil,
	)
	serverScrapeDurationDesc = prometheus.NewDesc(
		"openvpn_server_scrape_duration_seconds",
		"Duration of the last management interface scrape, including failed attempts.",
		[]string{"server"}, nil,
	)
	serverClientsConnectedDesc = prometheus.NewDesc(
		"openvpn_server_clients_connected",
		"Number of clients currently connected to the server.",
		[]string{"server"}, nil,
	)
	serverClientInfoDesc = prometheus.NewDesc(
		"openvpn_server_client_info",
		"Always 1; carries descriptive info about a connected client as labels.",
		[]string{"server", "common_name", "real_address", "virtual_address", "username", "cipher"}, nil,
	)
	serverClientBytesDesc = prometheus.NewDesc(
		"openvpn_server_client_bytes_total",
		"Cumulative bytes transferred for this client, reset whenever it reconnects.",
		[]string{"server", "common_name", "direction"}, nil,
	)
	serverClientConnectedSinceDesc = prometheus.NewDesc(
		"openvpn_server_client_connected_since_timestamp_seconds",
		"Unix timestamp at which this client connected.",
		[]string{"server", "common_name"}, nil,
	)
	serverInfoDesc = prometheus.NewDesc(
		"openvpn_server_info",
		"Always 1; carries the OpenVPN version running this server as a label.",
		[]string{"server", "version"}, nil,
	)
)

// Describe implements prometheus.Collector.
func (c *ServerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- serverUpDesc
	ch <- serverScrapeDurationDesc
	ch <- serverClientsConnectedDesc
	ch <- serverClientInfoDesc
	ch <- serverClientBytesDesc
	ch <- serverClientConnectedSinceDesc
	ch <- serverInfoDesc
}

// Collect implements prometheus.Collector. Every server is scraped
// concurrently so that one slow or unreachable management interface does
// not delay the others.
func (c *ServerCollector) Collect(ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, s := range c.servers {
		wg.Add(1)
		go func(s Server) {
			defer wg.Done()
			c.collectServer(ch, s)
		}(s)
	}
	wg.Wait()
}

func (c *ServerCollector) collectServer(ch chan<- prometheus.Metric, s Server) {
	client := &mgmt.Client{Address: s.ManagementAddress, Password: s.Password}

	ctx, cancel := context.WithTimeout(context.Background(), s.Timeout)
	defer cancel()

	start := time.Now()
	status, err := client.FetchServerStatus(ctx)
	duration := time.Since(start)

	ch <- prometheus.MustNewConstMetric(serverScrapeDurationDesc, prometheus.GaugeValue, duration.Seconds(), s.Name)

	if err != nil {
		c.logger.Warn("server scrape failed, reporting as down", "server", s.Name, "error", err)
		ch <- prometheus.MustNewConstMetric(serverUpDesc, prometheus.GaugeValue, 0, s.Name)
		return
	}

	ch <- prometheus.MustNewConstMetric(serverUpDesc, prometheus.GaugeValue, 1, s.Name)
	ch <- prometheus.MustNewConstMetric(serverClientsConnectedDesc, prometheus.GaugeValue, float64(len(status.Clients)), s.Name)

	for _, cl := range status.Clients {
		ch <- prometheus.MustNewConstMetric(
			serverClientInfoDesc, prometheus.GaugeValue, 1,
			s.Name, cl.CommonName, cl.RealAddress, cl.VirtualAddress, cl.Username, cl.Cipher,
		)
		ch <- prometheus.MustNewConstMetric(serverClientBytesDesc, prometheus.CounterValue, float64(cl.BytesReceived), s.Name, cl.CommonName, "in")
		ch <- prometheus.MustNewConstMetric(serverClientBytesDesc, prometheus.CounterValue, float64(cl.BytesSent), s.Name, cl.CommonName, "out")
		ch <- prometheus.MustNewConstMetric(
			serverClientConnectedSinceDesc, prometheus.GaugeValue, float64(cl.ConnectedSince.Unix()), s.Name, cl.CommonName,
		)
	}

	if status.Version != "" {
		c.clearVersionWarned(s.Name)
		ch <- prometheus.MustNewConstMetric(serverInfoDesc, prometheus.GaugeValue, 1, s.Name, status.Version)
	} else if status.VersionErr != nil {
		c.warnVersionOnce(s.Name, status.VersionErr)
	}
}
