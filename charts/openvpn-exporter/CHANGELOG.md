# Changelog

All notable changes to the `openvpn-exporter` Helm chart are documented in
this file. The exporter binary/image has its own
[CHANGELOG.md](../../CHANGELOG.md), since it's versioned and released
independently (`appVersion` here tracks which image version the chart
defaults to).

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `config.server`/`config.servers`: monitor one or several OpenVPN
  servers, mirroring `config.tunnel`/`config.tunnels` (same
  `name`/`managementAddress`/`certPath`/`configPath` fields, same
  ConfigMap/`--config` wiring when either list is non-empty). The chart
  previously had no way to configure server monitoring at all, even though
  the exporter itself has supported it since v0.3.0.

### Changed

- `config.tunnelsGlob`/`OPENVPN_EXPORTER_TUNNELS_GLOB` renamed to
  `config.targetsGlob`/`OPENVPN_EXPORTER_TARGETS_GLOB`: now discovers
  tunnels **and** servers from one glob (e.g.
  `/etc/openvpn/{client,server}/*.conf`), matching the exporter's own
  rename. Not backwards compatible — update any existing values using the
  old name.

## [0.5.0] - 2026-10-07

### Changed

- Bumped `appVersion` to 0.3.0 (server monitoring, OpenVPN version/state-since metrics — see the exporter changelog). No chart template changes; server monitoring configuration was not yet exposed (see Unreleased above).

## [0.4.0] - 2026-10-06

### Added

- `config.tunnels`: a known list of several tunnels, rendered into a
  ConfigMap and mounted via `--config`, for a single release watching more
  than one tunnel.
- `config.tunnelsGlob`/`OPENVPN_EXPORTER_TUNNELS_GLOB`: auto-discover
  tunnels from a glob of OpenVPN client config files.

### Changed

- Bumped `appVersion` to 0.2.0.

## [0.2.0] - 2026-10-05

### Changed

- All name/config/`extra*` values now support Helm templating (`tpl`), so
  they may contain expressions like `"{{ .Release.Name }}"` evaluated
  against the release.

## [0.1.0] - 2026-10-02

### Added

- Initial release: `Deployment`/`DaemonSet` controller, single-tunnel
  configuration (`config.tunnel`), password Secret reference, Service,
  optional Prometheus Operator `ServiceMonitor`, liveness/readiness probes.

[Unreleased]: https://github.com/philippe-vandermoere/openvpn-exporter/compare/chart-v0.5.0...HEAD
[0.5.0]: https://github.com/philippe-vandermoere/openvpn-exporter/compare/chart-v0.4.0...chart-v0.5.0
[0.4.0]: https://github.com/philippe-vandermoere/openvpn-exporter/compare/chart-v0.2.0...chart-v0.4.0
[0.2.0]: https://github.com/philippe-vandermoere/openvpn-exporter/compare/chart-v0.1.0...chart-v0.2.0
[0.1.0]: https://github.com/philippe-vandermoere/openvpn-exporter/releases/tag/chart-v0.1.0
