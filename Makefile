BINARY := gentooinstall
BIN_DIR := ./bin

.PHONY: build test vet fmt iso clean

build: vet
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/gentooinstall

iso:
	scripts/iso.sh

test: vet
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf $(BIN_DIR)
