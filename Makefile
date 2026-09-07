BINARY := gentooinstall
BIN_DIR := ./bin

# Colorized test runner (gotestsum, pinned for reproducibility). `go run`
# resolves it on demand: one-time network, then cached in GOMODCACHE, so
# later runs work offline. Fully offline hosts can run plain
# `go test -count=1 ./...` directly instead of these targets.
GOTESTSUM ?= go run gotest.tools/gotestsum@v1.13.0
GOTESTSUM_FLAGS ?= --format pkgname --hide-summary skipped

.PHONY: build test test-short cover vet fmt iso clean vm-test vm-test-net vm-install

build: vet
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/gentooinstall

iso:
	scripts/release.sh

CONFIG ?= builds/default.toml

test: vet
	mkdir -p $(BIN_DIR)
	$(GOTESTSUM) $(GOTESTSUM_FLAGS) -- -count=1 -race -coverprofile=$(BIN_DIR)/coverage.out ./...

test-short: vet
	$(GOTESTSUM) --format short-verbose -- -short -count=1 ./...

cover: test
	go tool cover -html=$(BIN_DIR)/coverage.out -o $(BIN_DIR)/coverage.html

vm-test: vet
	GENTOOINSTALL_E2E=1 $(GOTESTSUM) --format testname -- -count=1 -v -run 'TestISOBoots$' ./tests/

# Full installs inside the VM for every shipped build template. Very long
# (stage3 download + chroot + kernel per file); opt-in, local-only.
vm-install: vet
	GENTOOINSTALL_E2E=1 GENTOOINSTALL_E2E_INSTALL=1 $(GOTESTSUM) --format testname -- -count=1 -v -run 'TestInstallInVM$' ./tests/

vm-test-net: vet
	GENTOOINSTALL_E2E=1 GENTOOINSTALL_E2E_NET=1 $(GOTESTSUM) --format testname -- -count=1 -v -run 'TestISOBootNetwork$' ./tests/

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf $(BIN_DIR)
