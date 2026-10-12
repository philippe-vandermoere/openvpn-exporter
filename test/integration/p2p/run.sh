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
    echo "--- server logs ---"; $COMPOSE logs server || true
    echo "--- client logs ---"; $COMPOSE logs client || true
    echo "--- metrics ---"; metrics | grep '^openvpn_' || true
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

# metrics reads the exporter's /metrics from inside server (same netns as
# the exporter; its alpine base has busybox wget).
metrics() {
    $COMPOSE exec -T server wget -qO- http://127.0.0.1:9176/metrics 2>/dev/null || true
}

# management sends one command to the server's own management interface
# (authenticating first) and prints the raw response, used to cross-check
# the exported values.
management() {
    $COMPOSE exec -T server sh -c "printf '%s\\n%s\\nexit\\n' '${GLOBAL_MGMT_PASSWORD}' '$1' | nc 127.0.0.1 7505" 2>/dev/null | tr -d '\r'
}

echo "--- building and starting stack ---"
$COMPOSE up --build -d

wait_for "server connected to its peer" '[ "$(completed_count server)" -ge 1 ]' 90
wait_for "client connected to the server" '[ "$(completed_count client)" -ge 1 ]' 90

echo "--- checking mode detection: the static key tcp-server config is a server ---"
wait_for "exporter started" '$COMPOSE logs exporter 2>/dev/null | grep -q "msg=listening"' 30
$COMPOSE logs exporter 2>/dev/null | grep "msg=listening" | grep -q "tunnels=1 servers=1" \
    || fail "expected tunnels=1 servers=1 at startup"
wait_for "server discovered as a server" 'metrics | grep -q '\''openvpn_server_up{server="server"} 1'\''' 60
wait_for "client discovered as a tunnel" 'metrics | grep -q '\''openvpn_tunnel_up{tunnel="client"} 1'\''' 60
metrics | grep -q 'openvpn_tunnel_up{tunnel="server"}' && fail "the p2p server is also exported as a tunnel"
metrics | grep -q 'openvpn_tunnel_state{state="CONNECTED",tunnel="client"} 1' || fail "client: not CONNECTED"
echo "ok"

echo "--- checking the p2p server's single peer ---"
wait_for "server sees 1 connected peer" 'metrics | grep -q '\''openvpn_server_clients_connected{server="server"} 1'\''' 60
# Cross-check against the server's own "state" line:
# timestamp,CONNECTED,SUCCESS,local-ip,remote-ip,remote-port,...
state_line="$(management state | grep ',CONNECTED,')" || fail "server: management state is not CONNECTED"
state_since="$(echo "$state_line" | cut -d, -f1)"
peer_address="$(echo "$state_line" | cut -d, -f5):$(echo "$state_line" | cut -d, -f6)"
echo "server state: ${state_line}"
metrics | grep -qx "openvpn_server_client_info{cipher=\"\",common_name=\"\",real_address=\"${peer_address}\",server=\"server\",username=\"\",virtual_address=\"10.10.0.2\"} 1" \
    || fail "server: client_info doesn't match state (expected real_address=${peer_address}, virtual_address=10.10.0.2 from ifconfig)"
# The peer address is the client's real socket, not just whatever state says.
# (captured first: grep -q exiting early would SIGPIPE netstat under pipefail)
sockets="$($COMPOSE exec -T server netstat -tn 2>/dev/null)"
echo "$sockets" | grep -q "${peer_address}[[:space:]]*127.0.0.1:8443[[:space:]]*ESTABLISHED" \
    || fail "server: real_address ${peer_address} isn't the client's established socket"
metrics_since="$(metrics | grep '^openvpn_server_client_connected_since_timestamp_seconds{common_name="",server="server"}' | awk '{printf "%d", $2}')"
[ "$metrics_since" = "$state_since" ] \
    || fail "server: connected_since ${metrics_since} != state timestamp ${state_since}"
for direction in in out; do
    metrics | grep -Eq "^openvpn_server_client_bytes_total\{common_name=\"\",direction=\"${direction}\",server=\"server\"\} [1-9]" \
        || fail "server: missing or zero ${direction} bytes"
done
openvpn_version="$($COMPOSE exec -T server openvpn --version | head -1 | awk '{print $2}')"
metrics | grep -qx "openvpn_server_info{server=\"server\",version=\"${openvpn_version}\"} 1" \
    || fail "server: openvpn_server_info doesn't report version ${openvpn_version}"
metrics | grep -q 'cert_expiry_timestamp_seconds{' \
    && fail "static key configs must not report certificate expiry"
echo "--- exported metrics ---"
metrics | grep '^openvpn_'
echo "ok"

echo "--- checking the p2p server once its peer goes away ---"
$COMPOSE stop client
wait_for "server sees 0 connected peers" 'metrics | grep -q '\''openvpn_server_clients_connected{server="server"} 0'\''' 60
metrics | grep -q 'openvpn_server_client_info{' && fail "server: client_info still present without a peer"
metrics | grep -q 'openvpn_server_up{server="server"} 1' || fail "server: should stay up while waiting for its peer"
echo "ok"

echo "--- all checks passed ---"
