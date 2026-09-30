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

# Each exporter gets its client's certificate only — no CA, no key.
cp pki/issued/client1.crt /client1-cert/tls.crt
cp pki/issued/client2.crt /client2-cert/tls.crt

chmod 644 /pki-server/ca.crt /pki-server/tls.crt
chmod 600 /pki-server/tls.key
chmod 644 /pki-client1/ca.crt /pki-client1/tls.crt
chmod 600 /pki-client1/tls.key
chmod 644 /pki-client2/ca.crt /pki-client2/tls.crt
chmod 600 /pki-client2/tls.key
chmod 644 /client1-cert/tls.crt /client2-cert/tls.crt

echo "PKI generated: server, client1, client2 (+ cert-only copies for exporters)"
