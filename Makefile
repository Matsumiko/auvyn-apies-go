BINARY := auvyn

.PHONY: build test vet race run import clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/auvyn

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/auvyn

import:
	go run ./cmd/auvyn import-json $(JSON)

clean:
	rm -f $(BINARY)
