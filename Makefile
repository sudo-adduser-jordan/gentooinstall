BINARY := gentooinstall
BIN_DIR := ./bin

.PHONY: build test vet fmt iso clean build-testkit vm-test

build: vet
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/gentooinstall

iso:
	scripts/release.sh

CONFIG ?= builds/default.toml

test: vet
	go test ./...

vm-test: vet
	GENTOOINSTALL_E2E=1 go test -count=1 -v -run 'TestISOBoots|TestISOBootNetwork' ./tests/

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf $(BIN_DIR)
