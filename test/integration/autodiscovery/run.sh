#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")"

# shellcheck disable=SC1091
. ./.env

COMPOSE="docker compose"

cleanup() {
    echo "--- tearing down stack ---"
    $COMPOSE down -v --remove-orphans
}
trap cleanup EXIT

fail() {
    echo "FAILED: $1"
    echo "--- exporter logs ---"; $COMPOSE logs exporter || true
    echo "--- server-a logs ---"; $COMPOSE logs server-a || true
    echo "--- client-a logs ---"; $COMPOSE logs client-a || true
    echo "--- client-b logs ---"; $COMPOSE logs client-b || true
    exit 1
}

wait_for() {
    local description="$1" predicate="$2" timeout="$3"
    local waited=0
    echo -n "waiting for: $description "
    while ! eval "$predicate"; do
        if [ "$waited" -ge "$timeout" ]; then
            echo "timed out after ${timeout}s"
            fail "$description"
        fi
        sleep 1
        waited=$((waited + 1))
        echo -n "."
    done
    echo "ok (${waited}s)"
}

completed_count() {
    $COMPOSE logs "$1" 2>/dev/null | grep -c "Initialization Sequence Completed" || true
}

# metrics reads the exporter's /metrics from inside server-a (same netns as
# the exporter; server-a's alpine base has busybox wget), rather than
# relying on network_mode: service: + ports: publishing from a joining
# container, which isn't documented either way.
metrics() {
    $COMPOSE exec -T server-a wget -qO- http://127.0.0.1:9176/metrics 2>/dev/null || true
}

echo "--- building and starting stack ---"
$COMPOSE up --build -d

wait_for "server-a started" '[ "$(completed_count server-a)" -ge 1 ]' 60
wait_for "client-a connected" '[ "$(completed_count client-a)" -ge 1 ]' 60
wait_for "client-b connected" '[ "$(completed_count client-b)" -ge 1 ]' 60

echo "--- checking targets_glob auto-discovery and mode routing ---"
wait_for "client-a discovered as a tunnel" 'metrics | grep -q '\''openvpn_tunnel_up{tunnel="client-a"} 1'\''' 60
wait_for "client-b discovered as a tunnel" 'metrics | grep -q '\''openvpn_tunnel_up{tunnel="client-b"} 1'\''' 60
wait_for "server-a discovered as a server" 'metrics | grep -q '\''openvpn_server_up{server="server-a"} 1'\''' 60
echo "ok"

echo "--- checking the unreadable management password file falls back to the global password ---"
# Every identity's password file lives at /run/secrets/openvpn-management
# inside its own container, never shared with the exporter -- so discovery
# must log a fallback warning for all three, yet still authenticate
# successfully using OPENVPN_EXPORTER_PASSWORD (proven by the _up{...} 1
# checks above already having passed).
$COMPOSE logs exporter 2>/dev/null | grep -qc "falling back to the global password" || fail "exporter never logged the expected password-file fallback warning"
echo "ok"

echo "--- checking tunnel certificate expiry (per client, via auto-assigned config_path) ---"
metrics | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="openvpn-exporter-autodiscovery-ca",tunnel="client-a"}' || fail "client-a: missing CA cert expiry metric"
metrics | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="client-a",tunnel="client-a"}' || fail "client-a: missing client cert expiry metric"
metrics | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="openvpn-exporter-autodiscovery-ca",tunnel="client-b"}' || fail "client-b: missing CA cert expiry metric"
metrics | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="client-b",tunnel="client-b"}' || fail "client-b: missing client cert expiry metric"
echo "ok"

echo "--- checking server-side aggregated + per-client view (server-a sees both clients) ---"
wait_for "server-a sees 2 connected clients" 'metrics | grep -q '\''openvpn_server_clients_connected{server="server-a"} 2'\''' 60
metrics | grep -q 'openvpn_server_client_info{.*common_name="client-a".*server="server-a"' || fail "server-a: missing client_info for client-a"
metrics | grep -q 'openvpn_server_client_info{.*common_name="client-b".*server="server-a"' || fail "server-a: missing client_info for client-b"
metrics | grep -q 'openvpn_server_cert_expiry_timestamp_seconds{role="ca",server="server-a",subject="openvpn-exporter-autodiscovery-ca"}' || fail "server-a: missing CA cert expiry metric"
metrics | grep -q 'openvpn_server_cert_expiry_timestamp_seconds{role="client",server="server-a",subject="server-a"}' || fail "server-a: missing server cert expiry metric"
echo "ok"

echo "--- all checks passed ---"
