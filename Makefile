.PHONY: all build test fmt lint run run-get-vminfo run-cleanup-vms run-user-roles clean

all: build

build:
	go build -o openstack-tool .

test:
	go test -v ./getvminfo ./cleanupvms ./user ./auth

fmt:
	gofmt -w getvminfo/*.go cleanupvms/*.go user/*.go auth/*.go main.go

lint:
	golangci-lint run ./...

run: build
	./openstack-tool

run-get-vminfo: build
	./openstack-tool get-vminfo -v -filter="host=host1,status=ACTIVE,days>7" -output=table

run-cleanup-vms: build
	./openstack-tool cleanup-vms -u=root -p=secret -i=192.168.1.100 -dry-run

run-user-roles: build
	./openstack-tool user-roles -v -action=list-users-in-project -project-name=admin -output=table

clean:
	rm -f openstack-tool
	rm -f flavor_cache.json