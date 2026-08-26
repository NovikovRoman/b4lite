BIN := mtproto-proxy
DIST := dist

.PHONY: build test run clean install-service install-service-from-dist

build:
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o $(DIST)/$(BIN) ./cmd/mtproto-proxy

test:
	go test ./...

run: build
	./$(DIST)/$(BIN)

clean:
	rm -rf $(DIST)

install-service:
	sudo ./scripts/install-service.sh

install-service-from-dist:
	sudo ./scripts/install-from-dist.sh
