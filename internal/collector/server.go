package collector

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pvandermoere/openvpn-exporter/internal/openvpn"
)

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
	serverCertExpiryDesc = prometheus.NewDesc(
		"openvpn_server_cert_expiry_timestamp_seconds",
		"Certificate expiry date as a Unix timestamp.",
		[]string{"server", "role", "subject"}, nil,
	)
)

// describeServer sends every openvpn_server_* descriptor, unconditionally
// (required by prometheus.Collector regardless of whether any servers are
// currently configured).
func describeServer(ch chan<- *prometheus.Desc) {
	ch <- serverUpDesc
	ch <- serverScrapeDurationDesc
	ch <- serverClientsConnectedDesc
	ch <- serverClientInfoDesc
	ch <- serverClientBytesDesc
	ch <- serverClientConnectedSinceDesc
	ch <- serverInfoDesc
	ch <- serverCertExpiryDesc
}

func (c *Collector) collectServer(ch chan<- prometheus.Metric, s openvpn.Target) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	start := time.Now()
	status, err := s.FetchServerStatus(ctx)
	duration := time.Since(start)

	ch <- prometheus.MustNewConstMetric(serverScrapeDurationDesc, prometheus.GaugeValue, duration.Seconds(), s.Name)

	if err != nil {
		c.logger.Warn("server scrape failed, reporting as down", "server", s.Name, "error", err)
		ch <- prometheus.MustNewConstMetric(serverUpDesc, prometheus.GaugeValue, 0, s.Name)
	} else {
		clients := authenticatedClients(status.Clients)

		ch <- prometheus.MustNewConstMetric(serverUpDesc, prometheus.GaugeValue, 1, s.Name)
		ch <- prometheus.MustNewConstMetric(serverClientsConnectedDesc, prometheus.GaugeValue, float64(len(clients)), s.Name)

		for _, cl := range clients {
			// real_address (ip:port) is unique per connection, so this
			// series never collides even when common names do.
			ch <- prometheus.MustNewConstMetric(
				serverClientInfoDesc, prometheus.GaugeValue, 1,
				s.Name, cl.CommonName, cl.RealAddress, cl.VirtualAddress, cl.Username, cl.Cipher,
			)
		}

		for _, cl := range mergeClientsByCommonName(clients) {
			ch <- prometheus.MustNewConstMetric(serverClientBytesDesc, prometheus.CounterValue, float64(cl.BytesReceived), s.Name, cl.CommonName, "in")
			ch <- prometheus.MustNewConstMetric(serverClientBytesDesc, prometheus.CounterValue, float64(cl.BytesSent), s.Name, cl.CommonName, "out")
			// Zero for a point-to-point peer whose "state" timestamp couldn't
			// be parsed: omitted rather than reported as year 1.
			if !cl.ConnectedSince.IsZero() {
				ch <- prometheus.MustNewConstMetric(
					serverClientConnectedSinceDesc, prometheus.GaugeValue, float64(cl.ConnectedSince.Unix()), s.Name, cl.CommonName,
				)
			}
		}

		if status.Version != "" {
			c.clearVersionWarned(s)
			ch <- prometheus.MustNewConstMetric(serverInfoDesc, prometheus.GaugeValue, 1, s.Name, status.Version)
		} else if status.VersionErr != nil {
			c.warnVersionOnce(s, status.VersionErr)
		}
	}

	// Certificate reading is independent of the management scrape above: a
	// server whose process is temporarily unreachable still has a
	// certificate on disk that may be about to expire.
	certs, err := s.Certificates()
	if err != nil {
		c.logger.Warn("certificate read failed", "server", s.Name, "error", err)
		return
	}
	for _, cert := range certs {
		ch <- prometheus.MustNewConstMetric(
			serverCertExpiryDesc, prometheus.GaugeValue, float64(cert.NotAfter.Unix()),
			s.Name, cert.Role, cert.Subject,
		)
	}
}

// undefCommonName is the common name OpenVPN reports for a client that has
// not finished its TLS handshake/authentication yet.
const undefCommonName = "UNDEF"

// authenticatedClients drops clients still in the TLS handshake or
// authentication phase: they are not connected yet, and several of them
// would all share the "UNDEF" common name.
func authenticatedClients(clients []openvpn.ClientInfo) []openvpn.ClientInfo {
	authenticated := make([]openvpn.ClientInfo, 0, len(clients))
	for _, cl := range clients {
		if cl.CommonName != undefCommonName {
			authenticated = append(authenticated, cl)
		}
	}
	return authenticated
}

// mergeClientsByCommonName folds connections sharing a common name
// (duplicate-cn) into one entry, since the bytes/connected_since series are
// keyed by common_name only and the registry rejects the whole scrape on a
// duplicate series. Bytes are summed and ConnectedSince is the earliest
// non-zero connection time. Only the CommonName, Bytes* and ConnectedSince
// fields of the result are meaningful.
func mergeClientsByCommonName(clients []openvpn.ClientInfo) []openvpn.ClientInfo {
	merged := make([]openvpn.ClientInfo, 0, len(clients))
	index := make(map[string]int, len(clients))
	for _, cl := range clients {
		i, ok := index[cl.CommonName]
		if !ok {
			index[cl.CommonName] = len(merged)
			merged = append(merged, cl)
			continue
		}
		m := &merged[i]
		m.BytesReceived += cl.BytesReceived
		m.BytesSent += cl.BytesSent
		if m.ConnectedSince.IsZero() || (!cl.ConnectedSince.IsZero() && cl.ConnectedSince.Before(m.ConnectedSince)) {
			m.ConnectedSince = cl.ConnectedSince
		}
	}
	return merged
}
