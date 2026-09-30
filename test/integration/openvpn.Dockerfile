# Minimal shared image for the openvpn-server, openvpn-client1 and
# openvpn-client2 test services: the official alpine base plus the openvpn
# package, nothing else. Built once and reused by all three services (same
# image name in docker-compose.yml). The entrypoint writes the management
# password and the config for the role given via OPENVPN_ROLE.
FROM alpine:3.20
RUN apk add --no-cache openvpn
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
