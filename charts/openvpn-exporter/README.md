# openvpn-exporter

Helm chart for [openvpn-exporter](https://github.com/philippe-vandermoere/openvpn-exporter),
a Prometheus exporter for OpenVPN client tunnels and servers.

## Installing

```
helm install openvpn-exporter oci://ghcr.io/philippe-vandermoere/charts/openvpn-exporter --version <version>
```

See [CHANGELOG.md](CHANGELOG.md) for this chart's release history (the
exporter image itself has its own, [../../CHANGELOG.md](../../CHANGELOG.md)).

## Deployment shape: `controller.kind`

- `Deployment` (default): a single exporter instance, talking to one remote
  management interface over the network.
- `DaemonSet`: one exporter per node. Pair with `controller.hostNetwork:
  true` when the OpenVPN client/server on that node binds its management
  interface to `127.0.0.1` only (the common case) — the exporter then
  reaches it via the node's network namespace. See the worked k3s example
  below.

A single release can monitor one tunnel (`config.tunnel`), one server
(`config.server`), a known list of several of either
(`config.tunnels`/`config.servers`), or dynamically discover both from a
single glob of OpenVPN config files (`config.targetsGlob`) — see the
sections below. Installing the chart multiple times (one release per
tunnel/server) is only needed if you also want separate
Deployments/DaemonSets.

Naming fields (`nameOverride`, `fullnameOverride`, `serviceAccount.name`),
the tunnel/server fields under `config`, and `extraArgs`/`extraEnv`/
`extraVolumes`/`extraVolumeMounts` are all passed through Helm's `tpl`, so
they may contain template expressions evaluated against the release — e.g.
`config.tunnel.name: "{{ .Release.Name }}"` to derive the tunnel name from
the release name automatically.

## Configuring a single tunnel: `config.tunnel`

```yaml
config:
  tunnel:
    name: office
    managementAddress: 127.0.0.1:7505
    certPath: /certs/office.crt
```

See the [project README](../../README.md#certificate-source-config_path-vs-cert_path)
for the `config_path` vs `cert_path` precedence rule.

The management interface password is always read from an existing Secret
(`config.passwordSecretName`/`config.passwordSecretKey`), never stored in
values.

Certificates (and `configPath`'s file, if used) are not shipped by this
chart: mount them via `extraVolumes`/`extraVolumeMounts` (a `hostPath`
volume in `DaemonSet` mode, or a `Secret`/`ConfigMap` volume in `Deployment`
mode).

## Configuring several known tunnels: `config.tunnels`

```yaml
config:
  tunnels:
    - name: tun1
      managementAddress: 127.0.0.1:17501
      certPath: /etc/openvpn/client/tun1.crt
    - name: tun2
      managementAddress: 127.0.0.1:17502
      certPath: /etc/openvpn/client/tun2.crt
```

When `config.tunnels` is non-empty, the chart renders it into a ConfigMap
(one entry per tunnel, same `name`/`managementAddress`/`certPath`/
`configPath` fields as `config.tunnel`), mounts it at
`/etc/openvpn-exporter/config.yaml`, and passes `--config` to the binary —
`config.tunnel`'s own env vars are not emitted in this mode, to avoid two
conflicting sources of truth. A `checksum/config` pod annotation makes the
Deployment/DaemonSet roll its pods whenever the list changes. As with
`config.tunnel`, certificates themselves are not shipped by this chart —
mount them via `extraVolumes`/`extraVolumeMounts`.

## Configuring a single server: `config.server`

```yaml
config:
  server:
    name: vpn-gw-1
    managementAddress: 127.0.0.1:7600
    configPath: /etc/openvpn/server/vpn-gw-1.conf
```

Same fields, same `config_path`/`cert_path` precedence rule, and the same
"certificates aren't shipped by this chart" note as `config.tunnel` above —
see the
[project README](../../README.md#certificate-source-config_path-vs-cert_path).
The management interface password (`config.passwordSecretName`/
`config.passwordSecretKey`) is shared with tunnels — one release can
monitor a tunnel and a server at once, each with its own name but the same
password Secret.

## Configuring several known servers: `config.servers`

```yaml
config:
  servers:
    - name: vpn-gw-1
      managementAddress: 127.0.0.1:17601
      certPath: /etc/openvpn/server/vpn-gw-1.crt
    - name: vpn-gw-2
      managementAddress: 127.0.0.1:17602
      certPath: /etc/openvpn/server/vpn-gw-2.crt
```

Same ConfigMap/`--config` wiring as `config.tunnels` (see above) — a
release can set `config.tunnels`, `config.servers`, both, or neither; either
list being non-empty switches the exporter to `--config` mode and disables
the corresponding single-tunnel/single-server env vars.

## Discovering tunnels and servers automatically: `config.targetsGlob`

For the common case of a directory tree already managed outside Kubernetes
(e.g. systemd `openvpn-client@.service`/`openvpn-server@.service` units
writing to `/etc/openvpn/client/`/`/etc/openvpn/server/`), point the
exporter at a single glob instead of listing tunnels/servers by hand:

```yaml
config:
  targetsGlob: /etc/openvpn/{client,server}/*.conf
```

Brace expansion (`{client,server}`) is supported. This only sets
`OPENVPN_EXPORTER_TARGETS_GLOB` — no ConfigMap is involved, since the
exporter reads the `.conf` files directly and auto-detects each match as a
tunnel or a server from its own content. Mount the directory yourself via
`extraVolumes`/`extraVolumeMounts`; see the worked example below. Full
behaviour (name derivation, mode detection, management address parsing, the
per-target password file fallback) is documented in the
[project README](../../README.md#discovering-tunnels-and-servers-automatically-targets_glob).

## Example: DaemonSet on k3s, OpenVPN client running on each node

Common topology: the OpenVPN client runs as a system service on every node
(outside Kubernetes), its management interface bound to `127.0.0.1` only
(the secure default — see the main project README). The exporter then needs
two things it can't get from the cluster network: the node's loopback
interface (`hostNetwork: true`), and read access to the node's certificate
file (a `hostPath` mount).

Least-privilege option (recommended, matches this project's own philosophy
of giving the exporter nothing beyond the certificate it needs): mount a
single file with `hostPath` `type: File`, and use `cert_path` so the CA and
the private key are never exposed to the pod at all:

```yaml
controller:
  kind: DaemonSet
  hostNetwork: true

config:
  tunnel:
    name: node-tunnel
    managementAddress: 127.0.0.1:7505
    certPath: /etc/openvpn/client/client.crt

extraVolumes:
  - name: openvpn-cert
    hostPath:
      path: /etc/openvpn/client/client.crt
      type: File

extraVolumeMounts:
  - name: openvpn-cert
    mountPath: /etc/openvpn/client/client.crt
    readOnly: true
```

The mount path matches the host path exactly on purpose: it avoids any
ambiguity if you later switch to `configPath` with a real OpenVPN config
file that references certificates by absolute path.

Alternative, if you also want the CA's expiry tracked (`config_path` mode):
mount the whole client directory instead, at the trade-off of also exposing
the private key file to the pod's filesystem (still unused by the exporter,
but readable by anything else running in that container):

```yaml
config:
  tunnel:
    name: node-tunnel
    managementAddress: 127.0.0.1:7505
    configPath: /etc/openvpn/client/client.conf

extraVolumes:
  - name: openvpn-client
    hostPath:
      path: /etc/openvpn/client
      type: Directory

extraVolumeMounts:
  - name: openvpn-client
    mountPath: /etc/openvpn/client
    readOnly: true
```

If anything in your setup still needs cluster DNS resolution from inside the
pod (unlikely here, since `managementAddress` above is a literal IP), add
`controller.dnsPolicy: ClusterFirstWithHostNet` — `hostNetwork: true`
otherwise makes the pod use the node's own DNS resolution.

## Example: several systemd-managed tunnels/servers on one node, discovered dynamically

Topology this chart's `config.targetsGlob` was built for: a node runs
several OpenVPN clients and/or servers as systemd
`openvpn-client@.service`/`openvpn-server@.service` units, each with its
own config/cert/key under `/etc/openvpn/client/`/`/etc/openvpn/server/`
(e.g. `tun1.conf`/`tun1.crt`/`tun1.key`, `tun2.conf`/`tun2.crt`/`tun2.key`,
plus a shared `ca.crt`), each `.conf` already containing its own
`management 127.0.0.1 <port>` line. This chart is installed once, as a
subchart of a monitoring stack, rather than once per tunnel/server:

```yaml
controller:
  kind: DaemonSet
  hostNetwork: true

config:
  targetsGlob: /etc/openvpn/{client,server}/*.conf

extraVolumes:
  - name: openvpn
    hostPath:
      path: /etc/openvpn
      type: Directory

extraVolumeMounts:
  - name: openvpn
    mountPath: /etc/openvpn
    readOnly: true
```

Mounting the whole tree is safe even though the `.key` files are typically
`0600 root` and unreadable by the pod's non-root UID: the exporter never
opens key files at all (see the project README), so a `.key` it can't read
is simply certificate material it was never going to touch anyway. The one
thing to check on the host side is that `/etc/openvpn`,
`/etc/openvpn/client`, and `/etc/openvpn/server` are all traversable by an
arbitrary UID (`o+x`, the Debian/Ubuntu default for that path) — otherwise
even the `0644` `.conf`/`.crt` files become unreachable through it.

## Prometheus Operator integration

`serviceMonitor.enabled` is `false` by default, since the
`monitoring.coreos.com` CRDs aren't guaranteed to be installed in every
target cluster. Set it to `true` (and optionally `serviceMonitor.labels` to
match your Prometheus Operator's `serviceMonitorSelector`) to register a
`ServiceMonitor`.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| config.listenAddress | string | `":9176"` | Value for `OPENVPN_EXPORTER_LISTEN_ADDRESS`. |
| config.passwordSecretKey | string | `"password"` | Key within `passwordSecretName` holding the password. Supports Helm templating. |
| config.passwordSecretName | string | `""` | Name of an existing Secret holding the management interface password. Never store the password directly in values. Left empty for an unprotected management interface. Supports Helm templating. |
| config.scrapeTimeout | string | `"5s"` | Value for `OPENVPN_EXPORTER_SCRAPE_TIMEOUT`. |
| config.server.certPath | string | `""` | Direct path to the server certificate. See the project README for the `config_path` vs `cert_path` precedence rule. Supports Helm templating. |
| config.server.configPath | string | `""` | Path to an OpenVPN server config file to parse for `ca`/`cert`. Takes precedence over `certPath` when both are set. Supports Helm templating. |
| config.server.managementAddress | string | `""` | Management interface address. Supports Helm templating. |
| config.server.name | string | `""` | Server name. Must be set together with `managementAddress`. Supports Helm templating. Ignored when `config.servers` is non-empty. |
| config.servers | list | `[]` | Multiple servers, as a list of `{name, managementAddress, certPath, configPath}` (same fields as `config.server`, plural). Same ConfigMap/ `--config` wiring as `config.tunnels` — a release can set `tunnels`, `servers`, both, or neither; either list being non-empty is enough to switch the exporter to `--config` mode. Each field supports Helm templating (see `extraArgs`). |
| config.targetsGlob | string | `""` | Value for `OPENVPN_EXPORTER_TARGETS_GLOB`: a glob of OpenVPN client *and* server config files (e.g. `/etc/openvpn/{client,server}/*.conf` — brace expansion is supported) to auto-discover both from — each match becomes a tunnel or a server (auto-detected from its own content) named after its filename, with its certificate and management address parsed straight out of the file. See the project README for the full behaviour (including the per-target management password file fallback). Combines with `config.tunnel(s)`/`config.server(s)` rather than replacing them; mount the directory yourself via `extraVolumes`/`extraVolumeMounts`. Supports Helm templating. |
| config.tunnel.certPath | string | `""` | Direct path to the client certificate. See the project README for the `config_path` vs `cert_path` precedence rule. Supports Helm templating. |
| config.tunnel.configPath | string | `""` | Path to an OpenVPN client config file to parse for `ca`/`cert`. Takes precedence over `certPath` when both are set. Supports Helm templating. |
| config.tunnel.managementAddress | string | `""` | Management interface address. Supports Helm templating. |
| config.tunnel.name | string | `""` | Tunnel name. Must be set together with `managementAddress`. Supports Helm templating, e.g. `"{{ .Release.Name }}"` — handy since the chart recommends one release per tunnel. Ignored when `config.tunnels` is non-empty. |
| config.tunnels | list | `[]` | Multiple tunnels, as a list of `{name, managementAddress, certPath, configPath}` (same fields as `config.tunnel`, plural). When non-empty, takes over entirely from `config.tunnel`: the chart renders this list into a ConfigMap, mounts it, and passes `--config` to the binary instead of emitting the single-tunnel `OPENVPN_EXPORTER_TUNNEL_*` env vars. Use this for a subchart-of-a-monitoring-stack deployment where a single release needs to watch several tunnels. Each field supports Helm templating (see `extraArgs`). |
| controller.affinity | object | `{}` | Affinity rules. |
| controller.annotations | object | `{}` | Annotations for the Deployment/DaemonSet object itself. |
| controller.containerSecurityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container-level securityContext. |
| controller.dnsPolicy | string | `""` | Pod DNS policy. Set to `ClusterFirstWithHostNet` when `hostNetwork` is true and the pod still needs to resolve cluster-internal names. |
| controller.hostNetwork | bool | `false` | Use the host's network namespace. Needed in `DaemonSet` mode to reach a management interface bound to 127.0.0.1 on the node. |
| controller.kind | string | `"Deployment"` | How the exporter is deployed: `Deployment` (a single exporter instance talking to one remote management interface) or `DaemonSet` (one exporter per node, typically paired with `hostNetwork: true` when the OpenVPN client/server on that node only binds its management interface to 127.0.0.1 — see the DaemonSet/k3s example below). A single release covers as many tunnels/servers as you like via `config.tunnels`/`config.servers`/`config.targetsGlob` (or `config.tunnel`/`config.server` for just one of each); installing multiple releases is only needed if you also want separate Deployments/DaemonSets. |
| controller.nodeSelector | object | `{}` | Node selector. |
| controller.podAnnotations | object | `{}` | Annotations for the pod template. |
| controller.podLabels | object | `{}` | Labels for the pod template. |
| controller.podSecurityContext | object | `{"fsGroup":65535,"runAsGroup":65535,"runAsNonRoot":true,"runAsUser":65535}` | Pod-level securityContext. Defaults match the image, which already runs as a fixed non-root UID/GID. |
| controller.replicas | int | `1` | Replica count. Ignored when `controller.kind` is `DaemonSet`. |
| controller.resources | object | `{}` | Resource requests/limits for the exporter container. |
| controller.tolerations | list | `[]` | Tolerations. |
| controller.updateStrategy | object | `{}` | Update strategy (`strategy` for a Deployment, `updateStrategy` for a DaemonSet). |
| extraArgs | list | `[]` | Extra command-line args for the exporter binary. For a multi-tunnel YAML config beyond what `config.tunnels` models, pair with `extraVolumes`/`extraVolumeMounts` to mount your own file and pass `--config=/path/to/config.yaml` here. Rendered through Helm templating as a whole, so any entry may contain expressions like `"{{ .Release.Name }}"`. |
| extraEnv | list | `[]` | Extra environment variables, e.g. `OPENVPN_EXPORTER_PASSWORD_FILE` pointing at a path from `extraVolumeMounts` below. Supports Helm templating (see `extraArgs` above). |
| extraVolumeMounts | list | `[]` | Extra volume mounts, paired with `extraVolumes`. Supports Helm templating. |
| extraVolumes | list | `[]` | Extra volumes — the escape hatch for mounting certificates (a hostPath volume in `DaemonSet` mode, or a Secret/ConfigMap volume in `Deployment` mode) or a full config file for use with `extraArgs` above. Referenced by `config.tunnel.certPath`/`configPath` or your own `extraArgs`. Supports Helm templating (see `extraArgs` above). |
| fullnameOverride | string | `""` | Override the fully qualified object name entirely (otherwise built from the release name and chart name). Supports Helm templating. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/philippe-vandermoere/openvpn-exporter"` | Image repository. |
| image.tag | string | `""` | Image tag. Defaults to the chart's appVersion if unset. |
| imagePullSecrets | list | `[]` | Image pull secrets, for private registries. |
| livenessProbe.enabled | bool | `true` | Enable the liveness probe. |
| livenessProbe.failureThreshold | int | `3` | Consecutive failures before the container is considered unhealthy. |
| livenessProbe.httpGet.path | string | `"/"` | Path the probe checks. |
| livenessProbe.httpGet.port | string | `"http"` | Port the probe checks (the container port name). |
| livenessProbe.initialDelaySeconds | int | `5` | Seconds before the first probe. |
| livenessProbe.periodSeconds | int | `30` | Seconds between probes. |
| livenessProbe.timeoutSeconds | int | `5` | Probe timeout in seconds. |
| nameOverride | string | `""` | Override the chart name used to build object names (see `fullnameOverride` for the common case of controlling the whole name directly). Supports Helm templating, e.g. `"{{ .Release.Namespace }}"`. |
| readinessProbe.enabled | bool | `true` | Enable the readiness probe. |
| readinessProbe.failureThreshold | int | `3` | Consecutive failures before the pod is removed from Service endpoints. |
| readinessProbe.httpGet.path | string | `"/"` | Path the probe checks. See the comment on livenessProbe.httpGet.path above — kept as `/`, not `/metrics`, for the same reason. |
| readinessProbe.httpGet.port | string | `"http"` | Port the probe checks (the container port name). |
| readinessProbe.initialDelaySeconds | int | `5` | Seconds before the first probe. |
| readinessProbe.periodSeconds | int | `10` | Seconds between probes. |
| readinessProbe.timeoutSeconds | int | `5` | Probe timeout in seconds. |
| service.annotations | object | `{}` | Annotations for the Service. |
| service.port | int | `9176` | Service port (and the exporter's listen port, see `config.listenAddress`). |
| serviceAccount.annotations | object | `{}` | Annotations for the ServiceAccount. |
| serviceAccount.create | bool | `true` | Create a ServiceAccount. The exporter never calls the Kubernetes API, so no RBAC is attached either way. |
| serviceAccount.name | string | `""` | Name of the ServiceAccount to use. Defaults to the chart's fullname when `create` is true. Supports Helm templating. |
| serviceMonitor.enabled | bool | `false` | Create a Prometheus Operator ServiceMonitor. Disabled by default: the `monitoring.coreos.com` CRDs aren't guaranteed to be installed in every target cluster. |
| serviceMonitor.interval | string | `"30s"` | Scrape interval. |
| serviceMonitor.labels | object | `{}` | Extra labels, so the ServiceMonitor matches your Prometheus Operator's `serviceMonitorSelector`. |
| serviceMonitor.metricRelabelings | list | `[]` | `metricRelabelings` on the scrape endpoint. |
| serviceMonitor.namespace | string | `""` | Namespace for the ServiceMonitor. Defaults to the release namespace. |
| serviceMonitor.relabelings | list | `[]` | `relabelings` on the scrape endpoint. |
| serviceMonitor.scrapeTimeout | string | `"10s"` | Scrape timeout. |
