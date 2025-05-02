# Simple Makefile for openstack-tool

BINARY_NAME = openstack-tool
GO = go

.PHONY: all build run-get-vminfo run-cleanup-vms clean tidy help

all: build

build:
	$(GO) build -o $(BINARY_NAME) .

run-get-vminfo: build
	./$(BINARY_NAME) -mode=get-vminfo -v -filter="status=ACTIVE" -output=table

run-cleanup-vms: build
	./$(BINARY_NAME) -mode=cleanup-vms -u=root -p=secret -i=192.168.1.100 -dry-run

clean:
	rm -f $(BINARY_NAME)
	$(GO) clean

tidy:
	$(GO) mod tidy

help:
	@echo "Makefile for $(BINARY_NAME)"
	@echo "Targets:"
	@echo "  all               Build the binary"
	@echo "  build             Build the $(BINARY_NAME) binary"
	@echo "  run-get-vminfo    Run in get-vminfo mode"
	@echo "  run-cleanup-vms   Run in cleanup-vms mode (dry-run)"
	@echo "  clean             Remove binary and build artifacts"
	@echo "  tidy              Tidy go.mod and go.sum"
	@echo "  help              Show this help"
