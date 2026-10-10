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
"$EASYRSA_DIR/easyrsa" build-client-full openvpn_26 nopass
"$EASYRSA_DIR/easyrsa" build-client-full openvpn_25 nopass
"$EASYRSA_DIR/easyrsa" build-client-full openvpn_27 nopass

# Server: CA + its own cert/key only.
cp pki/ca.crt /pki-server/ca.crt
cp pki/issued/server.crt /pki-server/tls.crt
cp pki/private/server.key /pki-server/tls.key

# openvpn_26: CA + its own cert/key only (never server's or another tunnel's files).
cp pki/ca.crt /pki-openvpn_26/ca.crt
cp pki/issued/openvpn_26.crt /pki-openvpn_26/tls.crt
cp pki/private/openvpn_26.key /pki-openvpn_26/tls.key

# openvpn_25: CA + its own cert/key only.
cp pki/ca.crt /pki-openvpn_25/ca.crt
cp pki/issued/openvpn_25.crt /pki-openvpn_25/tls.crt
cp pki/private/openvpn_25.key /pki-openvpn_25/tls.key

# openvpn_27: CA + its own cert/key only.
cp pki/ca.crt /pki-openvpn_27/ca.crt
cp pki/issued/openvpn_27.crt /pki-openvpn_27/tls.crt
cp pki/private/openvpn_27.key /pki-openvpn_27/tls.key

printf 'client\nca ca.crt\ncert tls.crt\nkey tls.key\n' >/pki-openvpn_26/client.conf
printf 'client\nca ca.crt\ncert tls.crt\nkey tls.key\n' >/pki-openvpn_25/client.conf
printf 'client\nca ca.crt\ncert tls.crt\nkey tls.key\n' >/pki-openvpn_27/client.conf

chmod 644 /pki-server/ca.crt /pki-server/tls.crt
chmod 600 /pki-server/tls.key
chmod 644 /pki-openvpn_26/ca.crt /pki-openvpn_26/tls.crt /pki-openvpn_26/client.conf
chmod 600 /pki-openvpn_26/tls.key
chmod 644 /pki-openvpn_25/ca.crt /pki-openvpn_25/tls.crt /pki-openvpn_25/client.conf
chmod 600 /pki-openvpn_25/tls.key
chmod 644 /pki-openvpn_27/ca.crt /pki-openvpn_27/tls.crt /pki-openvpn_27/client.conf
chmod 600 /pki-openvpn_27/tls.key

echo "PKI generated: server, openvpn_26, openvpn_25, openvpn_27"
