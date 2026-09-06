BINARY := gentooinstall
BIN_DIR := ./bin

.PHONY: build test test-short cover vet fmt iso clean build-testkit vm-test vm-test-boot vm-test-net

build: vet
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/gentooinstall

iso:
	scripts/release.sh

CONFIG ?= builds/default.toml

test: vet
	go test -count=1 -race -coverprofile=coverage.out ./...

test-short: vet
	go test -short -count=1 ./...

cover: test
	go tool cover -html=coverage.out -o coverage.html

vm-test: vet
	GENTOOINSTALL_E2E=1 go test -count=1 -v -run 'TestISOBoots|TestISOBootNetwork' ./tests/

vm-test-boot: vet
	GENTOOINSTALL_E2E=1 go test -count=1 -v -run 'TestISOBoots$' ./tests/

vm-test-net: vet
	GENTOOINSTALL_E2E=1 go test -count=1 -v -run 'TestISOBootNetwork$' ./tests/

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf $(BIN_DIR)
