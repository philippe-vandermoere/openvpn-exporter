#!/bin/sh

set -eu

if [ -f /pki-server/ca.crt ]; then
    echo "PKI already initialized, skipping"
    exit 0
fi

apk add --no-cache easy-rsa >/dev/null

EASYRSA_DIR=/usr/share/easy-rsa
BUILD_DIR=/build

mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"

export EASYRSA_BATCH=1

"$EASYRSA_DIR/easyrsa" init-pki
EASYRSA_REQ_CN=openvpn-exporter-test-ca "$EASYRSA_DIR/easyrsa" build-ca nopass
# build-*-full derive their CommonName from the given short name and reject
# EASYRSA_REQ_CN, so it must not be set here.
"$EASYRSA_DIR/easyrsa" build-server-full server nopass
"$EASYRSA_DIR/easyrsa" build-client-full client1 nopass
"$EASYRSA_DIR/easyrsa" build-client-full client2 nopass

# Server: CA + its own cert/key only.
cp pki/ca.crt /pki-server/ca.crt
cp pki/issued/server.crt /pki-server/tls.crt
cp pki/private/server.key /pki-server/tls.key

# client1: CA + its own cert/key only (never server's or client2's files).
cp pki/ca.crt /pki-client1/ca.crt
cp pki/issued/client1.crt /pki-client1/tls.crt
cp pki/private/client1.key /pki-client1/tls.key

# client2: CA + its own cert/key only.
cp pki/ca.crt /pki-client2/ca.crt
cp pki/issued/client2.crt /pki-client2/tls.crt
cp pki/private/client2.key /pki-client2/tls.key

# Minimal client config for the EXPORTER's own config_path parsing (not a
# working OpenVPN config -- the real client config is generated separately
# by entrypoint.sh inside the openvpn_26/openvpn_25 containers). Relative
# paths resolve against this file's own directory, so ca.crt/tls.crt/tls.key
# are found without an absolute path. Proves the exporter reads ca+cert
# while tls.key (600, root-owned) sits right next to them, unreadable by the
# exporter's non-root UID.
printf 'client\nca ca.crt\ncert tls.crt\nkey tls.key\n' >/pki-client1/client.conf
printf 'client\nca ca.crt\ncert tls.crt\nkey tls.key\n' >/pki-client2/client.conf

chmod 644 /pki-server/ca.crt /pki-server/tls.crt
chmod 600 /pki-server/tls.key
chmod 644 /pki-client1/ca.crt /pki-client1/tls.crt /pki-client1/client.conf
chmod 600 /pki-client1/tls.key
chmod 644 /pki-client2/ca.crt /pki-client2/tls.crt /pki-client2/client.conf
chmod 600 /pki-client2/tls.key

echo "PKI generated: server, client1, client2"
