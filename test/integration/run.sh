#!/usr/bin/env bash
# Integration test: a real OpenVPN server and two real clients running
# different real OpenVPN releases (openvpn_26 -> 2.6.x, openvpn_25 -> 2.5.x,
# see openvpn.Dockerfile), each with its own distinct management password
# and a strictly isolated PKI volume. One exporter per client reads ca+cert
# via config_path, mounting that client's full PKI directory (its private
# key is 600/root-owned and genuinely unreadable by the exporter's non-root
# UID — proving the "key never opened" guarantee against a real file, not
# just a unit test). Requires Docker with NET_ADMIN/tun support.
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
    echo "--- exporter_26 logs ---"; $COMPOSE logs exporter_26 || true
    echo "--- exporter_25 logs ---"; $COMPOSE logs exporter_25 || true
    echo "--- openvpn-server logs ---"; $COMPOSE logs openvpn-server || true
    echo "--- openvpn_26 logs ---"; $COMPOSE logs openvpn_26 || true
    echo "--- openvpn_25 logs ---"; $COMPOSE logs openvpn_25 || true
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

wait_for "openvpn_26 connected" '[ "$(completed_count openvpn_26)" -ge 1 ]' 60
wait_for "openvpn_25 connected" '[ "$(completed_count openvpn_25)" -ge 1 ]' 60

echo "--- checking volume isolation ---"
client1_files=$($COMPOSE exec -T openvpn_26 ls /pki | sort | tr '\n' ' ')
[ "$client1_files" = "ca.crt client.conf tls.crt tls.key " ] || fail "openvpn_26 sees unexpected files in /pki: $client1_files"
echo "openvpn_26: $client1_files -- ok"

client2_files=$($COMPOSE exec -T openvpn_25 ls /pki | sort | tr '\n' ' ')
[ "$client2_files" = "ca.crt client.conf tls.crt tls.key " ] || fail "openvpn_25 sees unexpected files in /pki: $client2_files"
echo "openvpn_25: $client2_files -- ok"

echo "--- confirming the private key really is unreadable by a non-root UID (config_path scenario below relies on this) ---"
client1_key_perms=$($COMPOSE exec -T openvpn_26 stat -c '%a' /pki/tls.key)
[ "$client1_key_perms" = "600" ] || fail "openvpn_26: tls.key is not 600 ($client1_key_perms)"
client2_key_perms=$($COMPOSE exec -T openvpn_25 stat -c '%a' /pki/tls.key)
[ "$client2_key_perms" = "600" ] || fail "openvpn_25: tls.key is not 600 ($client2_key_perms)"
echo "ok ($client1_key_perms / $client2_key_perms)"

echo "--- checking management passwords are distinct and enforced ---"
server_password=$($COMPOSE exec -T openvpn-server cat /run/secrets/openvpn-management | tr -d '\r\n')

mgmt_check openvpn_26 "$CLIENT1_MGMT_PASSWORD" | grep -q "SUCCESS" || fail "openvpn_26 rejected its own password"
echo "openvpn_26 accepts its own password -- ok"
mgmt_check openvpn_26 "$CLIENT2_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "openvpn_26 accepted openvpn_25's password"
echo "openvpn_26 rejects openvpn_25's password -- ok"

mgmt_check openvpn_25 "$CLIENT2_MGMT_PASSWORD" | grep -q "SUCCESS" || fail "openvpn_25 rejected its own password"
echo "openvpn_25 accepts its own password -- ok"
mgmt_check openvpn_25 "$CLIENT1_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "openvpn_25 accepted openvpn_26's password"
echo "openvpn_25 rejects openvpn_26's password -- ok"

mgmt_check openvpn-server "$server_password" | grep -q "SUCCESS" || fail "server rejected its own (random) password"
echo "server accepts its own randomly-generated password -- ok"
mgmt_check openvpn-server "$CLIENT1_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "server accepted openvpn_26's password"
echo "server rejects openvpn_26's password -- ok"

wait_for "exporter_26 tunnel up" 'metrics1 | grep -q '\''openvpn_tunnel_up{tunnel="openvpn_26"} 1'\''' 60
wait_for "exporter_25 tunnel up" 'metrics2 | grep -q '\''openvpn_tunnel_up{tunnel="openvpn_25"} 1'\''' 60

echo "--- checking exporters read ca+cert via config_path, key never opened ---"
metrics1 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="openvpn-exporter-test-ca"' || fail "exporter_26: missing CA cert expiry metric"
metrics1 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="client1"' || fail "exporter_26: missing client cert expiry metric"
metrics2 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="openvpn-exporter-test-ca"' || fail "exporter_25: missing CA cert expiry metric"
metrics2 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="client2"' || fail "exporter_25: missing client cert expiry metric"
echo "ok"

echo "--- checking tunnel version/state-since metrics against two real OpenVPN releases ---"
metrics1 | grep -qE 'openvpn_tunnel_info\{tunnel="openvpn_26",version="2\.6\.[0-9]+"\} 1' || fail "exporter_26: missing/wrong version info metric"
metrics2 | grep -qE 'openvpn_tunnel_info\{tunnel="openvpn_25",version="2\.5\.[0-9]+"\} 1' || fail "exporter_25: missing/wrong version info metric"
metrics1 | grep -q 'openvpn_tunnel_state_since_timestamp_seconds{tunnel="openvpn_26"}' || fail "exporter_26: missing state-since metric"
metrics2 | grep -q 'openvpn_tunnel_state_since_timestamp_seconds{tunnel="openvpn_25"}' || fail "exporter_25: missing state-since metric"
echo "ok"

echo "--- checking traffic counters ---"
metrics1 | grep -q 'openvpn_tunnel_bytes_total{channel="link",direction="in",tunnel="openvpn_26"}' || fail "exporter_26: missing traffic counters"
metrics2 | grep -q 'openvpn_tunnel_bytes_total{channel="link",direction="in",tunnel="openvpn_25"}' || fail "exporter_25: missing traffic counters"
echo "ok"

echo "--- stopping openvpn-server to exercise a reconnection ---"
# The management interfaces stay up throughout (they live in the
# still-running client processes), so openvpn_tunnel_up stays 1 on both —
# it's the state label that reflects the reconnection.
openvpn_26_before=$(completed_count openvpn_26)
openvpn_25_before=$(completed_count openvpn_25)

$COMPOSE stop openvpn-server

wait_for "openvpn_26 state reflects reconnection" 'metrics1 | grep -q '\''openvpn_tunnel_state{state="RECONNECTING",tunnel="openvpn_26"} 1'\''' 30
wait_for "openvpn_25 state reflects reconnection" 'metrics2 | grep -q '\''openvpn_tunnel_state{state="RECONNECTING",tunnel="openvpn_25"} 1'\''' 30

echo "--- starting openvpn-server back up ---"
$COMPOSE start openvpn-server

wait_for "openvpn_26 reconnected (logs)" '[ "$(completed_count openvpn_26)" -gt "'"$openvpn_26_before"'" ]' 60
wait_for "openvpn_25 reconnected (logs)" '[ "$(completed_count openvpn_25)" -gt "'"$openvpn_25_before"'" ]' 60
wait_for "exporter_26 state back to CONNECTED" 'metrics1 | grep -q '\''openvpn_tunnel_state{state="CONNECTED",tunnel="openvpn_26"} 1'\''' 60
wait_for "exporter_25 state back to CONNECTED" 'metrics2 | grep -q '\''openvpn_tunnel_state{state="CONNECTED",tunnel="openvpn_25"} 1'\''' 60

echo "--- all checks passed ---"
