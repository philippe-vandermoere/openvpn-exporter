# Changelog

All notable changes to the `openvpn-exporter` binary/image are documented in
this file. The Helm chart has its own
[CHANGELOG.md](charts/openvpn-exporter/CHANGELOG.md), since it's versioned
and released independently.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.4.0] - 2026-10-10

### Added

- `openvpn_server_cert_expiry_timestamp_seconds` metric: a server's `ca`/
  `cert` certificates can now be tracked via `config_path`/`cert_path`,
  same as a tunnel's.
- `targets_glob`/`OPENVPN_EXPORTER_TARGETS_GLOB`: auto-discovers **both**
  tunnels and servers from one glob (e.g. `/etc/openvpn/{client,server}/*.conf`,
  brace expansion included), detecting each match's mode from its own
  content (`server`/`server-bridge`/`mode server` directive) instead of
  requiring it to be declared separately. Replaces `tunnels_glob`.
- `test/integration/autodiscovery/`: a new end-to-end stack covering a
  single host running several OpenVPN clients and a server at once,
  discovered entirely through `targets_glob`.

### Changed

- `tunnels_glob`/`OPENVPN_EXPORTER_TUNNELS_GLOB` renamed to `targets_glob`/
  `OPENVPN_EXPORTER_TARGETS_GLOB` (see Added above). Not backwards
  compatible — update any existing config using the old name.
- `internal/openvpn`: `ParseManagement` and `Load` (two separate passes
  over a config file) are consolidated into a single `ParseConfig`, which
  reads the management directive, client/server mode, and certificates in
  one pass. Internal refactor, no behavior change for existing `tunnels`/
  `servers`/`tunnel`/`server` configuration.
- `test/integration/`: split into `explicit/` (single tunnel/server,
  explicitly configured — the existing stack, unchanged behavior) and
  `autodiscovery/` (new, see Added above).
- `internal/mgmt` merged into `internal/openvpn`: the separate `Tunnel`/
  `Server` config types are replaced by a single `openvpn.Target` (with a
  `Mode` field), which now exposes `FetchStats`/`FetchServerStatus`/
  `Certificates` directly as methods instead of going through a separate
  management-interface client type. `FetchStats` now rejects a
  server-mode target and `FetchServerStatus` a client-mode target
  (internal safety guards; the exporter's own collector never triggers
  them). No observable change to any metric, YAML key, or env var.
- `internal/collector`: `TunnelCollector`/`ServerCollector` merged into a
  single `Collector` that dispatches on `Target.Mode`. Internal refactor,
  no observable change.

## [0.3.0] - 2026-10-07

### Added

- OpenVPN **server** monitoring: `servers:`/`OPENVPN_EXPORTER_SERVER_*`,
  a new `openvpn_server_*` metric family (`up`, `clients_connected`,
  `client_info`, `client_bytes_total`, `client_connected_since_timestamp_seconds`,
  `info`, `scrape_duration_seconds`) with one series per connected client.
- `openvpn_tunnel_info`/`openvpn_server_info`: the running OpenVPN version,
  read from the management interface.
- `openvpn_tunnel_state_since_timestamp_seconds`: when the tunnel entered
  its current state.

## [0.2.0] - 2026-10-06

### Added

- `tunnels_glob`/`OPENVPN_EXPORTER_TUNNELS_GLOB`: auto-discover tunnels
  from a glob of OpenVPN client config files instead of declaring each one
  by hand.
- `internal/openvpn.ParseManagement`: parses the `management` directive
  straight out of a client config file, used by `tunnels_glob` discovery.
- Helm templating support (`tpl`) across the chart's name/config/extra*
  values.

### Changed

- `internal/certs` moved to `internal/openvpn` (groups all OpenVPN config
  file parsing in one package).

## [0.1.0] - 2026-09-30

### Added

- Initial release: a Prometheus exporter for a single OpenVPN client
  tunnel — connection state and traffic counters from the management
  interface, certificate expiry from the client config file.

[Unreleased]: https://github.com/philippe-vandermoere/openvpn-exporter/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/philippe-vandermoere/openvpn-exporter/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/philippe-vandermoere/openvpn-exporter/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/philippe-vandermoere/openvpn-exporter/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/philippe-vandermoere/openvpn-exporter/releases/tag/v0.1.0
