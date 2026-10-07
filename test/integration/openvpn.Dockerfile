# Shared image for openvpn-server/openvpn_26/openvpn_25: the official
# alpine base plus the openvpn package, nothing else. The entrypoint writes
# the management password and the config for the role given via
# OPENVPN_ROLE.
#
# ALPINE_VERSION picks the real OpenVPN release: alpine:3.20 (default) ships
# OpenVPN 2.6.x, alpine:3.16 ships 2.5.x. openvpn_26/openvpn_25 build from
# this same file with different ALPINE_VERSION values, so
# openvpn_tunnel_info's version parsing is exercised against two genuinely
# different, real OpenVPN releases.
ARG ALPINE_VERSION=3.20
FROM alpine:${ALPINE_VERSION}
RUN apk add --no-cache openvpn
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
