#!/usr/bin/env bash
# Integration test: a real OpenVPN server, two real clients each with their
# own distinct management password and a strictly isolated PKI volume, and
# one exporter per client using the minimal cert_path-only interface (a
# dedicated volume containing just that client's certificate, nothing else
# — no CA, no key, no config file). Requires Docker with NET_ADMIN/tun
# support.
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
    echo "--- exporter1 logs ---"; $COMPOSE logs exporter1 || true
    echo "--- exporter2 logs ---"; $COMPOSE logs exporter2 || true
    echo "--- openvpn-server logs ---"; $COMPOSE logs openvpn-server || true
    echo "--- openvpn-client1 logs ---"; $COMPOSE logs openvpn-client1 || true
    echo "--- openvpn-client2 logs ---"; $COMPOSE logs openvpn-client2 || true
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

metrics1() { curl -fsS http://localhost:9176/metrics 2>/dev/null || true; }
metrics2() { curl -fsS http://localhost:9177/metrics 2>/dev/null || true; }

# mgmt_check runs against a service's own management interface via its
# loopback (nc is reachable inside the container regardless of host port
# publishing). Prints whatever comes back so the caller can grep for
# SUCCESS.
mgmt_check() {
    local service="$1" password="$2"
    $COMPOSE exec -T "$service" sh -c "printf '%s\nstate\n' '$password' | timeout 2 nc 127.0.0.1 7504" 2>/dev/null || true
}

echo "--- building and starting stack ---"
$COMPOSE up --build -d

wait_for "client1 connected" '[ "$(completed_count openvpn-client1)" -ge 1 ]' 60
wait_for "client2 connected" '[ "$(completed_count openvpn-client2)" -ge 1 ]' 60

echo "--- checking volume isolation ---"
client1_files=$($COMPOSE exec -T openvpn-client1 ls /pki | sort | tr '\n' ' ')
[ "$client1_files" = "ca.crt tls.crt tls.key " ] || fail "openvpn-client1 sees unexpected files in /pki: $client1_files"
echo "openvpn-client1: $client1_files -- ok"

client2_files=$($COMPOSE exec -T openvpn-client2 ls /pki | sort | tr '\n' ' ')
[ "$client2_files" = "ca.crt tls.crt tls.key " ] || fail "openvpn-client2 sees unexpected files in /pki: $client2_files"
echo "openvpn-client2: $client2_files -- ok"

echo "--- checking management passwords are distinct and enforced ---"
server_password=$($COMPOSE exec -T openvpn-server cat /run/secrets/openvpn-management | tr -d '\r\n')

mgmt_check openvpn-client1 "$CLIENT1_MGMT_PASSWORD" | grep -q "SUCCESS" || fail "client1 rejected its own password"
echo "client1 accepts its own password -- ok"
mgmt_check openvpn-client1 "$CLIENT2_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "client1 accepted client2's password"
echo "client1 rejects client2's password -- ok"

mgmt_check openvpn-client2 "$CLIENT2_MGMT_PASSWORD" | grep -q "SUCCESS" || fail "client2 rejected its own password"
echo "client2 accepts its own password -- ok"
mgmt_check openvpn-client2 "$CLIENT1_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "client2 accepted client1's password"
echo "client2 rejects client1's password -- ok"

mgmt_check openvpn-server "$server_password" | grep -q "SUCCESS" || fail "server rejected its own (random) password"
echo "server accepts its own randomly-generated password -- ok"
mgmt_check openvpn-server "$CLIENT1_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "server accepted client1's password"
echo "server rejects client1's password -- ok"

wait_for "exporter1 tunnel up" 'metrics1 | grep -q '\''openvpn_tunnel_up{tunnel="client1"} 1'\''' 60
wait_for "exporter2 tunnel up" 'metrics2 | grep -q '\''openvpn_tunnel_up{tunnel="client2"} 1'\''' 60

echo "--- checking exporters only expose the client cert (no CA, cert_path-only mode) ---"
metrics1 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="client1"' || fail "exporter1: missing client cert expiry metric"
metrics1 | grep -q 'role="ca"' && fail "exporter1: unexpectedly reports a CA certificate (cert_path mode should never track one)"
metrics2 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="client2"' || fail "exporter2: missing client cert expiry metric"
metrics2 | grep -q 'role="ca"' && fail "exporter2: unexpectedly reports a CA certificate"
echo "ok"

echo "--- checking traffic counters ---"
metrics1 | grep -q 'openvpn_tunnel_bytes_total{channel="link",direction="in",tunnel="client1"}' || fail "exporter1: missing traffic counters"
metrics2 | grep -q 'openvpn_tunnel_bytes_total{channel="link",direction="in",tunnel="client2"}' || fail "exporter2: missing traffic counters"
echo "ok"

echo "--- stopping openvpn-server to exercise a reconnection ---"
# The management interfaces stay up throughout (they live in the
# still-running client processes), so openvpn_tunnel_up stays 1 on both —
# it's the state label that reflects the reconnection.
client1_before=$(completed_count openvpn-client1)
client2_before=$(completed_count openvpn-client2)

$COMPOSE stop openvpn-server

wait_for "client1 state reflects reconnection" 'metrics1 | grep -q '\''openvpn_tunnel_state{state="RECONNECTING",tunnel="client1"} 1'\''' 30
wait_for "client2 state reflects reconnection" 'metrics2 | grep -q '\''openvpn_tunnel_state{state="RECONNECTING",tunnel="client2"} 1'\''' 30

echo "--- starting openvpn-server back up ---"
$COMPOSE start openvpn-server

wait_for "client1 reconnected (logs)" '[ "$(completed_count openvpn-client1)" -gt "'"$client1_before"'" ]' 60
wait_for "client2 reconnected (logs)" '[ "$(completed_count openvpn-client2)" -gt "'"$client2_before"'" ]' 60
wait_for "exporter1 state back to CONNECTED" 'metrics1 | grep -q '\''openvpn_tunnel_state{state="CONNECTED",tunnel="client1"} 1'\''' 60
wait_for "exporter2 state back to CONNECTED" 'metrics2 | grep -q '\''openvpn_tunnel_state{state="CONNECTED",tunnel="client2"} 1'\''' 60

echo "--- all checks passed ---"
