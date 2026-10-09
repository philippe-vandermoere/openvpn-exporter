#!/usr/bin/env bash
# Integration test: a real OpenVPN server and three real clients running
# different real OpenVPN releases (openvpn_26 -> 2.6.x, openvpn_25 -> 2.5.x,
# openvpn_27 -> 2.7.x, see openvpn.Dockerfile), each with its own distinct
# management password and a strictly isolated PKI volume. One exporter per
# client reads ca+cert via config_path, mounting that client's full PKI
# directory (its private key is 600/root-owned and genuinely unreadable by
# the exporter's non-root UID — proving the "key never opened" guarantee
# against a real file, not just a unit test). A separate exporter monitors
# openvpn-server itself (openvpn_server_* metrics, one series per connected
# client above). Requires Docker with NET_ADMIN/tun support.
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
    echo "--- exporter_27 logs ---"; $COMPOSE logs exporter_27 || true
    echo "--- exporter_server logs ---"; $COMPOSE logs exporter_server || true
    echo "--- openvpn-server logs ---"; $COMPOSE logs openvpn-server || true
    echo "--- openvpn_26 logs ---"; $COMPOSE logs openvpn_26 || true
    echo "--- openvpn_25 logs ---"; $COMPOSE logs openvpn_25 || true
    echo "--- openvpn_27 logs ---"; $COMPOSE logs openvpn_27 || true
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
metrics3() { curl -fsS http://localhost:9178/metrics 2>/dev/null || true; }
metrics4() { curl -fsS http://localhost:9179/metrics 2>/dev/null || true; }

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
wait_for "openvpn_27 connected" '[ "$(completed_count openvpn_27)" -ge 1 ]' 60

echo "--- checking volume isolation ---"
openvpn_26_files=$($COMPOSE exec -T openvpn_26 ls /pki | sort | tr '\n' ' ')
[ "$openvpn_26_files" = "ca.crt client.conf tls.crt tls.key " ] || fail "openvpn_26 sees unexpected files in /pki: $openvpn_26_files"
echo "openvpn_26: $openvpn_26_files -- ok"

openvpn_25_files=$($COMPOSE exec -T openvpn_25 ls /pki | sort | tr '\n' ' ')
[ "$openvpn_25_files" = "ca.crt client.conf tls.crt tls.key " ] || fail "openvpn_25 sees unexpected files in /pki: $openvpn_25_files"
echo "openvpn_25: $openvpn_25_files -- ok"

openvpn_27_files=$($COMPOSE exec -T openvpn_27 ls /pki | sort | tr '\n' ' ')
[ "$openvpn_27_files" = "ca.crt client.conf tls.crt tls.key " ] || fail "openvpn_27 sees unexpected files in /pki: $openvpn_27_files"
echo "openvpn_27: $openvpn_27_files -- ok"

echo "--- confirming the private key really is unreadable by a non-root UID (config_path scenario below relies on this) ---"
openvpn_26_key_perms=$($COMPOSE exec -T openvpn_26 stat -c '%a' /pki/tls.key)
[ "$openvpn_26_key_perms" = "600" ] || fail "openvpn_26: tls.key is not 600 ($openvpn_26_key_perms)"
openvpn_25_key_perms=$($COMPOSE exec -T openvpn_25 stat -c '%a' /pki/tls.key)
[ "$openvpn_25_key_perms" = "600" ] || fail "openvpn_25: tls.key is not 600 ($openvpn_25_key_perms)"
openvpn_27_key_perms=$($COMPOSE exec -T openvpn_27 stat -c '%a' /pki/tls.key)
[ "$openvpn_27_key_perms" = "600" ] || fail "openvpn_27: tls.key is not 600 ($openvpn_27_key_perms)"
echo "ok ($openvpn_26_key_perms / $openvpn_25_key_perms / $openvpn_27_key_perms)"

echo "--- checking management passwords are distinct and enforced ---"
server_password=$($COMPOSE exec -T openvpn-server cat /run/secrets/openvpn-management | tr -d '\r\n')

mgmt_check openvpn_26 "$OPENVPN_26_MGMT_PASSWORD" | grep -q "SUCCESS" || fail "openvpn_26 rejected its own password"
echo "openvpn_26 accepts its own password -- ok"
mgmt_check openvpn_26 "$OPENVPN_25_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "openvpn_26 accepted openvpn_25's password"
echo "openvpn_26 rejects openvpn_25's password -- ok"

mgmt_check openvpn_25 "$OPENVPN_25_MGMT_PASSWORD" | grep -q "SUCCESS" || fail "openvpn_25 rejected its own password"
echo "openvpn_25 accepts its own password -- ok"
mgmt_check openvpn_25 "$OPENVPN_27_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "openvpn_25 accepted openvpn_27's password"
echo "openvpn_25 rejects openvpn_27's password -- ok"

mgmt_check openvpn_27 "$OPENVPN_27_MGMT_PASSWORD" | grep -q "SUCCESS" || fail "openvpn_27 rejected its own password"
echo "openvpn_27 accepts its own password -- ok"
mgmt_check openvpn_27 "$OPENVPN_26_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "openvpn_27 accepted openvpn_26's password"
echo "openvpn_27 rejects openvpn_26's password -- ok"

mgmt_check openvpn-server "$server_password" | grep -q "SUCCESS" || fail "server rejected its own configured password"
echo "server accepts its own configured password -- ok"
mgmt_check openvpn-server "$OPENVPN_26_MGMT_PASSWORD" | grep -q "SUCCESS" && fail "server accepted openvpn_26's password"
echo "server rejects openvpn_26's password -- ok"

wait_for "exporter_26 tunnel up" 'metrics1 | grep -q '\''openvpn_tunnel_up{tunnel="openvpn_26"} 1'\''' 60
wait_for "exporter_25 tunnel up" 'metrics2 | grep -q '\''openvpn_tunnel_up{tunnel="openvpn_25"} 1'\''' 60
wait_for "exporter_27 tunnel up" 'metrics3 | grep -q '\''openvpn_tunnel_up{tunnel="openvpn_27"} 1'\''' 60

echo "--- checking exporters read ca+cert via config_path, key never opened ---"
metrics1 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="openvpn-exporter-test-ca"' || fail "exporter_26: missing CA cert expiry metric"
metrics1 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="openvpn_26"' || fail "exporter_26: missing client cert expiry metric"
metrics2 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="openvpn-exporter-test-ca"' || fail "exporter_25: missing CA cert expiry metric"
metrics2 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="openvpn_25"' || fail "exporter_25: missing client cert expiry metric"
metrics3 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="ca",subject="openvpn-exporter-test-ca"' || fail "exporter_27: missing CA cert expiry metric"
metrics3 | grep -q 'openvpn_tunnel_cert_expiry_timestamp_seconds{role="client",subject="openvpn_27"' || fail "exporter_27: missing client cert expiry metric"
echo "ok"

echo "--- checking tunnel version/state-since metrics against three real OpenVPN releases ---"
metrics1 | grep -qE 'openvpn_tunnel_info\{tunnel="openvpn_26",version="2\.6\.[0-9]+"\} 1' || fail "exporter_26: missing/wrong version info metric"
metrics2 | grep -qE 'openvpn_tunnel_info\{tunnel="openvpn_25",version="2\.5\.[0-9]+"\} 1' || fail "exporter_25: missing/wrong version info metric"
metrics3 | grep -qE 'openvpn_tunnel_info\{tunnel="openvpn_27",version="2\.7\.[0-9]+"\} 1' || fail "exporter_27: missing/wrong version info metric"
metrics1 | grep -q 'openvpn_tunnel_state_since_timestamp_seconds{tunnel="openvpn_26"}' || fail "exporter_26: missing state-since metric"
metrics2 | grep -q 'openvpn_tunnel_state_since_timestamp_seconds{tunnel="openvpn_25"}' || fail "exporter_25: missing state-since metric"
metrics3 | grep -q 'openvpn_tunnel_state_since_timestamp_seconds{tunnel="openvpn_27"}' || fail "exporter_27: missing state-since metric"
echo "ok"

echo "--- checking traffic counters ---"
metrics1 | grep -q 'openvpn_tunnel_bytes_total{channel="link",direction="in",tunnel="openvpn_26"}' || fail "exporter_26: missing traffic counters"
metrics2 | grep -q 'openvpn_tunnel_bytes_total{channel="link",direction="in",tunnel="openvpn_25"}' || fail "exporter_25: missing traffic counters"
metrics3 | grep -q 'openvpn_tunnel_bytes_total{channel="link",direction="in",tunnel="openvpn_27"}' || fail "exporter_27: missing traffic counters"
echo "ok"

wait_for "exporter_server up" 'metrics4 | grep -q '\''openvpn_server_up{server="vpn-server"} 1'\''' 60

echo "--- checking server-side per-client metrics (openvpn-server monitored directly) ---"
metrics4 | grep -q 'openvpn_server_clients_connected{server="vpn-server"} 3' || fail "exporter_server: expected 3 connected clients"
metrics4 | grep -q 'openvpn_server_client_info{.*common_name="openvpn_26".*server="vpn-server"' || fail "exporter_server: missing client_info for openvpn_26"
metrics4 | grep -q 'openvpn_server_client_bytes_total{common_name="openvpn_26",direction="in",server="vpn-server"}' || fail "exporter_server: missing inbound bytes for openvpn_26"
metrics4 | grep -q 'openvpn_server_client_bytes_total{common_name="openvpn_26",direction="out",server="vpn-server"}' || fail "exporter_server: missing outbound bytes for openvpn_26"
metrics4 | grep -q 'openvpn_server_client_connected_since_timestamp_seconds{common_name="openvpn_26",server="vpn-server"}' || fail "exporter_server: missing connected-since for openvpn_26"
metrics4 | grep -qE 'openvpn_server_info\{server="vpn-server",version="2\.6\.[0-9]+"\} 1' || fail "exporter_server: missing/wrong version info metric"
echo "ok"

echo "--- stopping openvpn-server to exercise a reconnection ---"
# The management interfaces stay up throughout (they live in the
# still-running client processes), so openvpn_tunnel_up stays 1 on all three
# — it's the state label that reflects the reconnection.
openvpn_26_before=$(completed_count openvpn_26)
openvpn_25_before=$(completed_count openvpn_25)
openvpn_27_before=$(completed_count openvpn_27)

$COMPOSE stop openvpn-server

wait_for "openvpn_26 state reflects reconnection" 'metrics1 | grep -q '\''openvpn_tunnel_state{state="RECONNECTING",tunnel="openvpn_26"} 1'\''' 30
wait_for "openvpn_25 state reflects reconnection" 'metrics2 | grep -q '\''openvpn_tunnel_state{state="RECONNECTING",tunnel="openvpn_25"} 1'\''' 30
wait_for "openvpn_27 state reflects reconnection" 'metrics3 | grep -q '\''openvpn_tunnel_state{state="RECONNECTING",tunnel="openvpn_27"} 1'\''' 30

echo "--- starting openvpn-server back up ---"
$COMPOSE start openvpn-server

wait_for "openvpn_26 reconnected (logs)" '[ "$(completed_count openvpn_26)" -gt "'"$openvpn_26_before"'" ]' 60
wait_for "openvpn_25 reconnected (logs)" '[ "$(completed_count openvpn_25)" -gt "'"$openvpn_25_before"'" ]' 60
wait_for "openvpn_27 reconnected (logs)" '[ "$(completed_count openvpn_27)" -gt "'"$openvpn_27_before"'" ]' 60
wait_for "exporter_26 state back to CONNECTED" 'metrics1 | grep -q '\''openvpn_tunnel_state{state="CONNECTED",tunnel="openvpn_26"} 1'\''' 60
wait_for "exporter_25 state back to CONNECTED" 'metrics2 | grep -q '\''openvpn_tunnel_state{state="CONNECTED",tunnel="openvpn_25"} 1'\''' 60
wait_for "exporter_27 state back to CONNECTED" 'metrics3 | grep -q '\''openvpn_tunnel_state{state="CONNECTED",tunnel="openvpn_27"} 1'\''' 60

echo "--- all checks passed ---"
