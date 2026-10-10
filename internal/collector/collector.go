// Package collector implements the prometheus.Collector that scrapes every
// configured tunnel's and server's management interface and certificate
// files on each call to Collect, dispatching on openvpn.Target.Mode.
package collector

import (
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pvandermoere/openvpn-exporter/internal/openvpn"
)

// Collector scrapes every configured target (tunnel or server) concurrently
// on each Collect call.
type Collector struct {
	targets []openvpn.Target
	timeout time.Duration
	logger  *slog.Logger

	// versionWarned tracks, per target, whether a "version" command
	// failure has already been logged, so a persistently old/non-conforming
	// management API warns once rather than on every scrape. Keyed by
	// "mode:name" rather than just name: a tunnel and a server may
	// legitimately share a name (independent namespaces, see
	// internal/config.validate), so a plain name key could let one mask or
	// clear the other's warning. Guarded by a mutex since Collect scrapes
	// targets concurrently.
	versionWarnedMu sync.Mutex
	versionWarned   map[string]bool
}

// NewCollector builds a Collector for the given targets, scraped with the
// given timeout. logger may be nil, in which case scrape errors are
// discarded.
func NewCollector(targets []openvpn.Target, timeout time.Duration, logger *slog.Logger) *Collector {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Collector{targets: targets, timeout: timeout, logger: logger, versionWarned: make(map[string]bool)}
}

func versionWarnedKey(t openvpn.Target) string {
	return string(t.Mode) + ":" + t.Name
}

// warnVersionOnce logs a "version" command failure for t at most once until
// it next succeeds (see clearVersionWarned).
func (c *Collector) warnVersionOnce(t openvpn.Target, err error) {
	key := versionWarnedKey(t)
	c.versionWarnedMu.Lock()
	alreadyWarned := c.versionWarned[key]
	c.versionWarned[key] = true
	c.versionWarnedMu.Unlock()
	if !alreadyWarned {
		if t.Mode == openvpn.ModeServer {
			c.logger.Warn("server version unavailable", "server", t.Name, "error", err)
		} else {
			c.logger.Warn("tunnel version unavailable", "tunnel", t.Name, "error", err)
		}
	}
}

// clearVersionWarned re-arms warnVersionOnce for t after a successful
// "version" command.
func (c *Collector) clearVersionWarned(t openvpn.Target) {
	c.versionWarnedMu.Lock()
	delete(c.versionWarned, versionWarnedKey(t))
	c.versionWarnedMu.Unlock()
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	describeTunnel(ch)
	describeServer(ch)
}

// Collect implements prometheus.Collector. Every target is scraped
// concurrently so that one slow or unreachable management interface does
// not delay the others.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, t := range c.targets {
		wg.Add(1)
		go func(t openvpn.Target) {
			defer wg.Done()
			if t.Mode == openvpn.ModeServer {
				c.collectServer(ch, t)
			} else {
				c.collectTunnel(ch, t)
			}
		}(t)
	}
	wg.Wait()
}
