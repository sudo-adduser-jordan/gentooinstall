BINARY := gentooinstall
BIN_DIR := ./bin

.PHONY: build test vet fmt iso clean build-testkit vm-test

build: vet
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/gentooinstall

iso:
	scripts/iso.sh

# The testkit image build is privileged (debootstrap/grub); use make for a
# one-liner, CI calls scripts/build-testkit.sh directly in its sudo step.
build-testkit:
	sudo scripts/build-testkit.sh

CONFIG ?= builds/default.toml

vm-test: build
	scripts/run-vm-test.sh $(CONFIG)

test: vet
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf $(BIN_DIR)
