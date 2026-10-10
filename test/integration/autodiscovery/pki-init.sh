#!/bin/sh
set -eu

if [ -f /shared/server/server-a/ca.crt ]; then
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
EASYRSA_REQ_CN=openvpn-exporter-autodiscovery-ca "$EASYRSA_DIR/easyrsa" build-ca nopass
"$EASYRSA_DIR/easyrsa" build-server-full server-a nopass
"$EASYRSA_DIR/easyrsa" build-client-full client-a nopass
"$EASYRSA_DIR/easyrsa" build-client-full client-b nopass

mkdir -p /shared/server/server-a /shared/client/client-a /shared/client/client-b

cp pki/ca.crt /shared/server/server-a/ca.crt
cp pki/issued/server-a.crt /shared/server/server-a/tls.crt
cp pki/private/server-a.key /shared/server/server-a/tls.key

cp pki/ca.crt /shared/client/client-a/ca.crt
cp pki/issued/client-a.crt /shared/client/client-a/tls.crt
cp pki/private/client-a.key /shared/client/client-a/tls.key

cp pki/ca.crt /shared/client/client-b/ca.crt
cp pki/issued/client-b.crt /shared/client/client-b/tls.crt
cp pki/private/client-b.key /shared/client/client-b/tls.key

chmod 644 /shared/server/server-a/ca.crt /shared/server/server-a/tls.crt
chmod 600 /shared/server/server-a/tls.key
chmod 644 /shared/client/client-a/ca.crt /shared/client/client-a/tls.crt
chmod 600 /shared/client/client-a/tls.key
chmod 644 /shared/client/client-b/ca.crt /shared/client/client-b/tls.crt
chmod 600 /shared/client/client-b/tls.key

echo "PKI generated: server-a, client-a, client-b"
