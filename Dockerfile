FROM docker.io/golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /build/openvpn-exporter ./cmd/openvpn-exporter

FROM scratch

COPY --from=build /build/openvpn-exporter /usr/local/bin/openvpn-exporter

USER 65535:65535

EXPOSE 9176

ENTRYPOINT ["/usr/local/bin/openvpn-exporter"]
