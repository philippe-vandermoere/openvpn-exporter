#!/bin/sh
set -eu

CONFIG_FILE="${OPENVPN_CONFIG_FILE:-/etc/openvpn/openvpn.conf}"
PKI_DIR="${OPENVPN_PKI_DIR:-/pki}"
MANAGEMENT_HOST="${OPENVPN_MANAGEMENT_HOST:-0.0.0.0}"
MANAGEMENT_PORT="${OPENVPN_MANAGEMENT_PORT:-7504}"

OPENVPN_MANAGEMENT_PASSWORD_FILE=/run/secrets/openvpn-management
mkdir -p "$(dirname "${OPENVPN_MANAGEMENT_PASSWORD_FILE}")"
if [ -n "${OPENVPN_MANAGEMENT_PASSWORD:-}" ]; then
    echo "${OPENVPN_MANAGEMENT_PASSWORD}" >"${OPENVPN_MANAGEMENT_PASSWORD_FILE}"
else
    head -c 18 /dev/urandom | base64 >"${OPENVPN_MANAGEMENT_PASSWORD_FILE}"
fi
chmod 600 "${OPENVPN_MANAGEMENT_PASSWORD_FILE}"

mkdir -p "$(dirname "${CONFIG_FILE}")"
: >"${CONFIG_FILE}"

{
    echo 'proto udp'
    echo 'dev tun'
} | tee -a "${CONFIG_FILE}"

if [ "${OPENVPN_ROLE:-client}" = "server" ]; then
    echo 'port 1194' | tee -a "${CONFIG_FILE}"
else
    {
        echo 'client'
        echo "remote ${OPENVPN_REMOTE}"
        echo 'resolv-retry infinite'
        echo 'nobind'
    } | tee -a "${CONFIG_FILE}"
fi

{
    echo "ca ${PKI_DIR}/ca.crt"
    echo "cert ${PKI_DIR}/tls.crt"
    echo "key ${PKI_DIR}/tls.key"
} | tee -a "${CONFIG_FILE}"

if [ "${OPENVPN_ROLE:-client}" = "server" ]; then
    {
        echo 'dh none'
        echo 'server 10.8.0.0 255.255.255.0'
        echo 'ifconfig-pool-persist /tmp/ipp.txt'
    } | tee -a "${CONFIG_FILE}"
else
    echo 'remote-cert-tls server' | tee -a "${CONFIG_FILE}"
fi

{
    echo 'keepalive 2 8'
    echo 'persist-key'
    echo 'persist-tun'
    echo 'verb 3'
    echo "management ${MANAGEMENT_HOST} ${MANAGEMENT_PORT} ${OPENVPN_MANAGEMENT_PASSWORD_FILE}"
} | tee -a "${CONFIG_FILE}"

exec openvpn --config "${CONFIG_FILE}"
