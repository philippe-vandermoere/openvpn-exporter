package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/pvandermoere/openvpn-exporter/internal/collector"
	"github.com/pvandermoere/openvpn-exporter/internal/config"
)

func main() {
	configPath := flag.String("config", "", "Path to the YAML config file (optional; falls back to environment variables, see README)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}
	for _, w := range cfg.Warnings {
		logger.Warn(w)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collector.NewCollector(cfg.GetTargets(), cfg.ScrapeTimeoutDuration(), logger),
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>OpenVPN Exporter</title></head>
<body><h1>OpenVPN Exporter</h1><p><a href="/metrics">Metrics</a></p></body></html>`))
	})

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("error during shutdown", "error", err)
		}
	}()

	logger.Info("listening", "address", cfg.ListenAddress, "tunnels", len(cfg.Tunnels), "servers", len(cfg.Servers))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
