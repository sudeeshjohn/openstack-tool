.PHONY: all build test fmt lint run run-get-vminfo run-clean-nova-stale-vms run-user-roles run-manage-vms run-volume run-volume-list run-images run-images-list run-storage run-storage-vol-list clean install-lint deps

all: build

build: deps
	go build -o openstack-tool . || { echo "Build failed"; exit 1; }

deps:
	go mod tidy
	go mod verify

test:
	go test -v -coverprofile=coverage.out ./... || { echo "Tests failed"; exit 1; }
	go tool cover -html=coverage.out -o coverage.html

fmt:
	gofmt -w getvminfo/*.go cleannovastalevms/*.go user/*.go auth/*.go managevms/*.go util/*.go volume/*.go images/*.go storage/*.go main.go

install-lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "Installing golangci-lint v1.61.0..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.61.0; \
	}

lint: install-lint
	golangci-lint run ./...

run: build
	./openstack-tool

run-get-vminfo: build
	./openstack-tool vm info --verbose --filter="host=host1,status=ACTIVE,days>7" --output=table --timeout=300

run-clean-nova-stale-vms: build
	./openstack-tool clean-nova-stale-vms --user=root --password=secret --ip=192.168.1.100 --dry-run --output=table --timeout=300

run-user-roles: build
	./openstack-tool user-roles --verbose --action=list-users-in-project --project=admin --output=table --timeout=300

run-manage-vms: build
	./openstack-tool vm manage delete --verbose --vm=test-vm1,test-vm2 --project=admin --dry-run --output=table --timeout=300

run-volume: build
	./openstack-tool volume --verbose --action=list-all --output=table --timeout=300

run-volume-list: build
	./openstack-tool volume --verbose --action=list --project=proj1 --output=table --timeout=300

run-images: build
	./openstack-tool images --verbose --action=list-all --output=table --timeout=300

run-images-list: build
	./openstack-tool images --verbose --action=list --project=proj1 --output=table --timeout=300

run-storage: build
	./openstack-tool storage vol list --verbose --ip=192.168.1.100 --username=admin --password=secret --timeout=300

run-storage-vol-list: build
	./openstack-tool storage vol list --verbose --ip=192.168.1.100 --username=admin --password=secret --long --timeout=300

clean:
	rm -f openstack-tool
	rm -f flavor_cache.json
	rm -f coverage.out coverage.html