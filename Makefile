BINARY := openvpn-exporter
IMAGE := openvpn-exporter:dev

.PHONY: build test vet fmt docker-build compose-test compose-test-explicit compose-test-autodiscovery compose-test-p2p clean

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

compose-test: compose-test-explicit compose-test-autodiscovery compose-test-p2p

compose-test-explicit:
	./test/integration/explicit/run.sh

compose-test-autodiscovery:
	./test/integration/autodiscovery/run.sh

compose-test-p2p:
	./test/integration/p2p/run.sh

clean:
	rm -rf bin/
