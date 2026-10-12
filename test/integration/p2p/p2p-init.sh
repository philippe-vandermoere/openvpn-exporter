#!/bin/sh
set -eu

if [ -f /shared/keys/static.key ]; then
    echo "static key already initialized, skipping"
    exit 0
fi

apk add --no-cache openvpn >/dev/null

mkdir -p /shared/keys
openvpn --genkey secret /shared/keys/static.key
# Never opened by the exporter (which runs as a non-root UID): ParseConfig
# doesn't follow the "secret" directive.
chmod 600 /shared/keys/static.key

echo "static key generated"
