.PHONY: build check

build:
	go build -o bin/radipod ./cmd/radipod

check:
	go vet ./...
	go test ./...
