BINARY := openvpn-exporter
IMAGE := openvpn-exporter:dev

.PHONY: build test vet fmt docker-build compose-test clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/$(BINARY) ./cmd/openvpn-exporter

test:
	go test ./... -race

vet:
	go vet ./...

fmt:
	gofmt -l .

docker-build:
	docker build -t $(IMAGE) .

compose-test:
	./test/integration/run.sh

clean:
	rm -rf bin/
