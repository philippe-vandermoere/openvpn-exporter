# openvpn-exporter

A single Prometheus exporter for OpenVPN client tunnels: connection state,
traffic counters, and certificate expiry, all under one `openvpn_` metric
prefix and a common `tunnel` label. It replaces the combination of
`node_exporter` + `openvpn_exporter` + `x509-certificate-exporter`, which
between them use inconsistent metric prefixes and, individually, none of
them covers the full need.

- Connection state and traffic counters are read from OpenVPN's management
  interface. A short-lived connection is opened per scrape and closed
  immediately afterwards, since the management interface only accepts one
  client at a time.
- Certificate expiry is read directly from the OpenVPN client configuration
  file (the `ca`/`cert` directives or inline `<ca>`/`<cert>` blocks) — the
  management interface has no command that exposes certificate content.
  Private keys are never opened.
- If the management interface does not respond, the tunnel is reported as
  down (`openvpn_tunnel_up 0`); this covers both "tunnel disconnected" and
  "OpenVPN process not running" with a single signal.

## Metrics

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `openvpn_tunnel_up` | gauge | `tunnel` | 1 if the management interface responded, 0 otherwise. |
| `openvpn_tunnel_state` | gauge | `tunnel`, `state` | Set to 1 for the current OpenVPN state (`CONNECTED`, `RECONNECTING`, ...). Absent when the management interface is unreachable. |
| `openvpn_tunnel_state_since_timestamp_seconds` | gauge | `tunnel` | Unix timestamp at which the tunnel entered its current state (see `openvpn_tunnel_state`) — join on `tunnel` to know which state it refers to. Read from the management interface's `state` command (the same one used for `openvpn_tunnel_state`), no extra round-trip. Absent when the management interface is unreachable, or if that response's timestamp field couldn't be parsed (the state itself is unaffected either way). |
| `openvpn_tunnel_bytes_total` | counter | `tunnel`, `channel` (`tunnel`\|`link`), `direction` (`in`\|`out`) | `channel=tunnel` is the plaintext TUN/TAP counters, `channel=link` is the encrypted TCP/UDP counters. Resets whenever OpenVPN restarts — use `rate()`/`increase()`, not the raw value. Absent when the management interface is unreachable (never partially published). |
| `openvpn_tunnel_cert_expiry_timestamp_seconds` | gauge | `tunnel`, `role` (`ca`\|`client`), `subject` | Certificate expiry as an absolute Unix timestamp (never a day count). Independent of tunnel connectivity. A CA bundle with intermediates yields one series per certificate. Emitted only if `config_path` or `cert_path` is set for the tunnel (see Configuration) — `role="ca"` only ever appears via `config_path`. |
| `openvpn_tunnel_info` | gauge | `tunnel`, `version` | Always 1; `version` is the OpenVPN release running this tunnel (e.g. `2.6.12`), read from the management interface's `version` command. Absent if that command failed or its response couldn't be parsed (e.g. a very old management API) — this never fails the scrape or affects `openvpn_tunnel_up`, and is logged at most once per tunnel until it next succeeds. |
| `openvpn_tunnel_scrape_duration_seconds` | gauge | `tunnel` | Duration of the last management interface scrape, including failed/timed-out attempts — useful for spotting a slow management interface. |

Standard `process_*` metrics (CPU, memory, file descriptors) are also
exposed, for monitoring the exporter's own health — a stuck goroutine or
memory leak in the exporter would otherwise be invisible aside from
`up{job="openvpn_exporter"}` going to 0. Go runtime metrics (`go_*`) are
not registered, to keep the endpoint otherwise limited to `openvpn_*`.

Suggested alerting (not shipped with this repository):
- Tunnel or process down: `openvpn_tunnel_up == 0` for 2 minutes.
- Certificate expiring soon: `openvpn_tunnel_cert_expiry_timestamp_seconds - time() < 30 * 86400`.
- Unstable tunnel: `changes(openvpn_tunnel_up[15m]) > N`.

## Configuration

### YAML file

```yaml
listen_address: ":9176"      # optional, this is the default
scrape_timeout: 5s           # optional, this is the default
password_file: /etc/openvpn-exporter/mgmt.pass   # optional
tunnels:
  - name: office
    management_address: 127.0.0.1:7505
    config_path: /etc/openvpn/client/office.conf   # parses ca + client cert
  - name: backup
    management_address: 127.0.0.1:7506
    cert_path: /etc/openvpn/client/backup.crt      # tracks that one cert only
```

Pass the file with `--config /path/to/config.yaml`. Startup fails fast if a
tunnel name is duplicated, a `management_address` doesn't parse as
`host:port`, or a `config_path`/`cert_path` that is set isn't readable.

### Discovering tunnels automatically: `tunnels_glob`

Instead of (or alongside) declaring each tunnel under `tunnels:`, point at a
glob of OpenVPN client config files and let the exporter derive one tunnel
per match:

```yaml
tunnels_glob: /etc/openvpn/client/*.conf
```

For each matched file: the tunnel's `name` is the filename without its
extension (`tun1.conf` → `tun1`), `config_path` is the file itself (so `ca`/
`cert` are tracked exactly as with an explicit `config_path`), and
`management_address` is parsed straight out of the file's own `management`
directive — no need to repeat it in values. Only TCP directives bound to a
genuinely connectable address are supported: `management <path> unix`
(socket) and `management 0.0.0.0 <port>` (a bind address, not something a
client can dial) both fail fast with a clear error rather than being
silently misparsed.

If the directive's optional third argument (a password file) is present,
its content is used as that tunnel's password — unless the file isn't
readable, in which case the exporter falls back to the global
`password`/`password_file`/`OPENVPN_EXPORTER_PASSWORD` (logging a warning)
rather than failing outright, since that file is often as tightly
permissioned as the private key sitting next to it. It's only a hard error
if neither is available.

Matches from `tunnels_glob` are merged with any explicit `tunnels:` entries
(duplicate names across the two are rejected like any other duplicate); a
glob matching nothing is not itself an error.

### Certificate source: `config_path` vs `cert_path`

Both are optional, and a tunnel can set either, both, or neither:
- `config_path` set → the OpenVPN client config file is parsed for its `ca`
  and `cert` directives/blocks, and **both** are tracked (`role="ca"` and
  `role="client"`). See "Certificate reading" below.
- `config_path` unset, `cert_path` set → that one certificate file is read
  directly (no config file involved, no CA tracked — only `role="client"`).
- Neither set → certificate expiry isn't checked for that tunnel at all; no
  error, the other metrics are unaffected.
- If both are set, `config_path` takes precedence and `cert_path` is
  ignored.

`cert_path` is the simpler option when you don't want the exporter parsing
OpenVPN's config syntax, or don't want to track the CA's expiry at all (a
CA's own expiry is arguably a PKI lifecycle concern rather than a
per-tunnel one).

A `config_path` pointing at a pre-shared-key (static key, `secret`
directive) tunnel — which has no TLS handshake and therefore no `ca`/`cert`
directive at all — is not an error: `openvpn_tunnel_cert_expiry_timestamp_seconds`
is simply never emitted for that tunnel, exactly as when neither
`config_path` nor `cert_path` is set. This also applies to tunnels
discovered via `tunnels_glob` below.

### Environment variables (container-friendly)

`--config` is optional. These variables apply whether or not a YAML file is
used, and always take precedence over the corresponding YAML value:

| Variable | Equivalent to |
|---|---|
| `OPENVPN_EXPORTER_CONFIG` | `--config` |
| `OPENVPN_EXPORTER_LISTEN_ADDRESS` | `listen_address` |
| `OPENVPN_EXPORTER_SCRAPE_TIMEOUT` | `scrape_timeout` |
| `OPENVPN_EXPORTER_PASSWORD_FILE` | `password_file` |
| `OPENVPN_EXPORTER_PASSWORD` | the password value itself, not a path — takes precedence over `OPENVPN_EXPORTER_PASSWORD_FILE`/`password_file` if both are set. Useful when the secret is already injected as an environment variable (Docker/Kubernetes secret) rather than mounted as a file. |
| `OPENVPN_EXPORTER_TUNNELS_GLOB` | `tunnels_glob` |

If no YAML tunnels are defined, a single tunnel can be declared entirely via
environment variables — a natural fit for a one-exporter-per-sidecar
container deployment:

```
OPENVPN_EXPORTER_TUNNEL_NAME=office
OPENVPN_EXPORTER_TUNNEL_MANAGEMENT_ADDRESS=127.0.0.1:7505
OPENVPN_EXPORTER_TUNNEL_CONFIG_PATH=/etc/openvpn/client/office.conf
# or, instead of CONFIG_PATH:
OPENVPN_EXPORTER_TUNNEL_CERT_PATH=/etc/openvpn/client/office.crt
```

`OPENVPN_EXPORTER_TUNNEL_NAME` and `OPENVPN_EXPORTER_TUNNEL_MANAGEMENT_ADDRESS`
must both be set together; `CONFIG_PATH`/`CERT_PATH` are optional, same
precedence rule as the YAML fields above. The multi-tunnel `tunnels:` list
is only available via YAML.

### Filesystem access

The exporter needs read access to whatever certificate source a tunnel
uses: with `config_path`, that's the OpenVPN client config file and every
file it references via `ca`/`cert` (relative paths resolve against the
directory containing `config_path`, not the exporter's working directory);
with `cert_path`, just that one file. If the exporter runs in a different
container or host than the OpenVPN client, that means sharing a read-only
mount for those files — see `test/integration/docker-compose.yml` for a
worked example, including the minimal `cert_path`-only shape (a dedicated
volume containing only the client certificate, nothing else).

## Building

```
make build   # bin/openvpn-exporter, static binary
make test    # go test ./... -race
make vet
```

## Docker image

```
make docker-build   # openvpn-exporter:dev
```

The image is built `FROM scratch`: a static binary running as a non-root
numeric user (`65535:65535`), with no shell, no CA certificate bundle, and
no `/etc/passwd` — none of which the exporter needs, since it makes no
outbound TLS calls and never resolves a UID to a username.

## Testing

Unit tests (`internal/mgmt`, `internal/openvpn`, `internal/config`,
`internal/collector`) run against an in-process fake management server and
don't need Docker:

```
go test ./... -race
```

`test/integration/run.sh` (`make compose-test`) additionally spins up a real
OpenVPN server and two real OpenVPN clients running different real OpenVPN
releases — `openvpn_26` (2.6.x) and `openvpn_25` (2.5.x), via
`test/integration/openvpn.Dockerfile`'s `ALPINE_VERSION` build arg — each
with its own identity and its own management password. One exporter per
client reads `ca`/`cert` via `config_path`, mounting that client's full PKI
directory read-only: `ca.crt`/`tls.crt` are world-readable but `tls.key` is
`600` and root-owned, while the exporter itself runs as a fixed non-root UID
(see the root `Dockerfile`) — a real, not simulated, demonstration that the
private key is never opened. `openvpn-server`/`openvpn_26`/`openvpn_25`
share that one Dockerfile (official `alpine` base plus the `openvpn`
package, nothing else); its entrypoint writes the management password
(given or randomly generated) and renders the config for its role. Only the
exporters are built from this repository's root Dockerfile. The test checks
PKI volume isolation (each client sees only its own cert/key, never another
consumer's), that the private key really is `600` before relying on it,
distinct and enforced management passwords, that both tunnels report up
with certificate expiry, the correct real `openvpn_tunnel_info` version and
`openvpn_tunnel_state_since_timestamp_seconds`, and traffic counters, and
that a server outage is correctly reflected as
`openvpn_tunnel_state{state="RECONNECTING"}` on both (note:
`openvpn_tunnel_up` stays 1 throughout, since the management interface
itself — which lives in the still-running OpenVPN client process — remains
reachable; `up` reflects management interface reachability, not the
`CONNECTED` state specifically). Requires Docker with `NET_ADMIN` and
`/dev/net/tun` support.

## CI/CD

GitHub Actions (`.github/workflows/`): `ci.yml` runs Go lint
(`golangci-lint`), a Dockerfile lint (`hadolint`) on the root `Dockerfile`,
unit tests, and the full integration stack above on every pull request (and
push to `main`). `release.yml`, on `v*` tags, builds linux/amd64+arm64
binaries and a GitHub Release via GoReleaser (`.goreleaser.yaml`), and
builds/pushes a multi-arch image to `ghcr.io/<repo>`. Dependabot
(`.github/dependabot.yml`) keeps Go modules and Actions up to date weekly.
