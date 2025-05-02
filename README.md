# OpenStack Tool

`openstack-tool` is a command-line utility written in Go for managing OpenStack based cloud resources. It provides two main functionalities:

1. **Get VM Info**: Retrieves and displays detailed information about virtual machines (VMs) in an OpenStack environment, including VM name, user email, uptime, project, status, memory, VCPUs, processing units, host, and IP addresses. Supports filtering and multiple output formats (table, JSON).
2. **Cleanup VMs**: Identifies and removes orphaned VMs on a NovaLink host by comparing OpenStack’s VM list with the host’s actual VM inventory. Includes a dry-run mode for safety.

The tool uses the [Gophercloud](https://github.com/gophercloud/gophercloud) library to interact with OpenStack APIs (Compute v2.1, Identity v3) and supports local module development.

## Features

- **Get VM Info**:
  - Fetch VM details across all tenants.
  - Filter VMs by host, user email, status, project, or uptime (e.g., `days>7`).
  - Output in table or JSON format.
  - Verbose logging for debugging.
- **Cleanup VMs**:
  - Identify orphaned VMs on a NovaLink host via SSH.
  - Delete orphaned VMs with confirmation (dry-run mode available).
  - Supports SSH authentication with username/password.
- **Concurrent Processing**: Efficiently handles large datasets using goroutines.
- **Error Handling**: Robust retries and detailed error messages.
- **Local Development**: Uses Go modules with a `replace` directive for local testing.

## Prerequisites

- **Go**: Version 1.20 or higher (<https://golang.org/dl/>).
- **OpenStack Deployment**: Access to an OpenStack cloud with Compute (Nova) and Identity (Keystone) services.
- **NovaLink Host**: For `cleanup-vms`, a NovaLink host with SSH access.
- **Environment Variables**: Required OpenStack credentials (see [Configuration](#configuration)).

## Installation

1. **Clone the Repository** (if using a remote repository; skip if working locally):

   ```bash
   git clone https://github.com/sudeeshjohn/openstack-tool.git
   cd openstack-tool

## Configuration

The tool requires OpenStack environment variables for authentication. Set these before running:

```bash
export OS_AUTH_URL=<https://openstack.example.com:5000/v3>
export OS_USERNAME=admin
export OS_PASSWORD=secret
export OS_PROJECT_NAME=admin
export OS_DOMAIN_NAME=Default
export OS_REGION_NAME=RegionOne
```
## Usage
The tool supports two modes, selected via the -mode flag: get-vminfo or cleanup-vms.

```bash
./openstack-tool -h
```
### Mode: Get VM Info

Fetch and display VM information with optional filters and output formats.

Example:
```bash
./openstack-tool -mode=get-vminfo -v -filter="host=host1,status=ACTIVE,project=proj1,days>7" -output=table
```
### Output (Table):
```
##############
VM_NAME        USER_EMAIL            UP_FOR_DAYS    PROJECT    STATUS    MEMORY    VCPUs    PROC_UNIT    HOST      IP_ADDRESSES
vm1            user1@example.com     10             proj1      ACTIVE    4096      2        0.5          host1     192.168.1.10,10.0.0.5
vm2            user2@example.com     15             proj1      ACTIVE    8192      4        1.0          host1     192.168.1.11
##############
Number of VMs: 2
##############
```
### Flags:

```
•  -v: Enable verbose debug output.
•  -filter: Filter VMs (e.g., host=host1,email=user@example.com,status=ACTIVE,project=proj1,days>7). Supported operators for days: >, <, =, >=, <=.
•  -output: Output format (table or json). Default: table.
•  -region: OpenStack region (overrides OS_REGION_NAME).
```
### Mode: Cleanup VMs
Identify and delete orphaned VMs on a NovaLink host. Use -dry-run to preview deletions.

Note: Make sure that the hypervisor host is in maintenance mode when you run this action.

Example:
```bash
./openstack-tool -mode=cleanup-vms -u=root -p=secret -i=192.168.1.100 -dry-run
```
### Output (Dry Run):
```
✅ Identity V3 client created successfully!
🔹 OpenStack VM count: 50
🔹 Remote VM count: 52
🔹 Missing VM count: 2
Missing VMs:
 - VM: vm-orphaned1, Tenant: Unknown, Status: Running
 - VM: vm-orphaned2, Tenant: Unknown, Status: Running
⚠️ Dry-run mode enabled. VMs that would be deleted:
 - VM: vm-orphaned1, Tenant: Unknown, Status: Running
 - VM: vm-orphaned2, Tenant: Unknown, Status: Running
 ```
 ### Flags:
 ```
•  -u: SSH username (default: root).
•  -p: SSH password (optional; consider SSH keys for security).
•  -i: NovaLink host IP (required).
•  -dry-run: Preview VMs to be deleted without taking action.
```

## Build

### Build
```bash
go build -o openstack-tool
```
