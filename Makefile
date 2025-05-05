.PHONY: all build test fmt lint run run-get-vminfo run-cleanup-vms run-user-roles clean

# Default target
all: build

# Build the openstack-tool binary
build:
	go build -o openstack-tool .

# Run unit tests for all modules
test:
	go test -v ./getvminfo ./cleanupvms ./user

# Format code using gofmt
fmt:
	gofmt -w getvminfo/*.go cleanupvms/*.go user/*.go main.go

# Run golangci-lint for static analysis
lint:
	golangci-lint run ./...

# Run the binary (default to showing usage)
run: build
	./openstack-tool

# Run the get-vminfo subcommand with example flags
run-get-vminfo: build
	./openstack-tool get-vminfo -v -filter="host=host1,status=ACTIVE,days>7" -output=table

# Run the cleanup-vms subcommand with example flags
run-cleanup-vms: build
	./openstack-tool cleanup-vms -u=root -p=secret -i=192.168.1.100 -dry-run

# Run the user-roles subcommand with example flags
run-user-roles: build
	./openstack-tool user-roles -v -action=list-users-in-project -project-name=admin -output=table

# Clean up generated files and binary
clean:
	rm -f openstack-tool
	rm -f flavor_cache.json