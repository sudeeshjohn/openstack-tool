# OpenStack Tool

`openstack-tool` is a command-line utility written in Go for managing OpenStack-based cloud resources. It provides a suite of functionalities to manage virtual machines (VMs), users, roles, volumes, images, and storage in an OpenStack environment. The tool uses the [Gophercloud](https://github.com/gophercloud/gophercloud) library to interact with OpenStack APIs (Compute v2.1, Identity v3) and supports local module development with Go modules.

## Features

- **VM Management**:
  - Retrieve detailed VM information (e.g., name, email, uptime, status) with filtering and output in table or JSON format.
  - Manage VMs (e.g., delete) with dry-run support.
  - Clean stale VMs on a NovaLink hypervisor via SSH.
- **User and Role Management**: List users and their roles within OpenStack projects.
- **Volume Management**: List volumes, including unassociated ones, with detailed output options.
- **Image Management**: List and manage OpenStack images.
- **Storage Management**: List storage volumes on a specified storage system.


## Prerequisites

- **Go**: Version 1.24.3 or higher recommended ([download](https://golang.org/dl/)).
- **OpenStack Deployment**: Admin access to an OpenStack cloud with Compute (Nova), Identity (Keystone), Volume (Cinder), and Image (Glance) services.
- **NovaLink Host**: For `clean-nova-stale-vms`, a NovaLink host with SSH access.
- **Storage System**: For `storage` subcommand, access to a storage system with credentials.
- **Environment Variables**: OpenStack credentials (see [Configuration](#configuration)).

## Installation

1. **Clone the Repository** (skip if working locally):

   ```bash
   git clone https://github.com/sudeeshjohn/openstack-tool.git
   cd openstack-tool
   make
   ```
#### Install Dependencies:

```bash
go mod tidy
Configuration
Set the following OpenStack environment variables for authentication before running the tool:
```

```bash
export OS_AUTH_URL=https://openstack.example.com:5000/v3
export OS_USERNAME=admin
export OS_PASSWORD=secret
export OS_PROJECT_NAME=admin
export OS_DOMAIN_NAME=Default
export OS_REGION_NAME=RegionOne
```
For subcommands requiring SSH access (e.g., clean-nova-stale-vms, storage), ensure SSH access to the target host. Using SSH keys is recommended for security (see SSH Key Setup).

Usage
The tool supports multiple subcommands for managing OpenStack resources. Run the following to view all options:

```bash
./openstack-tool -h
Note: The -h flag is used with subcommands (e.g., ./openstack-tool vm -h). Running ./openstack-tool -h alone will display the list of subcommands.
```

## Subcommands
### 1. vm

Manages virtual machines in OpenStack with two subcommands: info and manage.

vm info: Retrieves detailed VM information, including name, user email, uptime, project, status, memory, VCPUs, processing units, host, and IP addresses.

Example:

```bash

./openstack-tool vm info --verbose --filter="host=host1,status=ACTIVE,days>7" --output=json --timeout=300
```
Output (JSON):

```bash
[
  {
    "VM_NAME": "vm1",
    "USER_EMAIL": "user1@example.com",
    "UP_FOR_DAYS": 10,
    "PROJECT": "proj1",
    "STATUS": "ACTIVE",
    "MEMORY": 4096,
    "VCPUs": 2,
    "PROC_UNIT": 0.5,
    "HOST": "host1",
    "IP_ADDRESSES": "192.168.1.10,10.0.0.5"
  }
]
```

vm manage: Performs VM management actions, such as deleting VMs.

Example:

```bash

./openstack-tool vm manage delete --vm=test-vm1,test-vm2 --project=admin --dry-run --output=table --timeout=300
```

Output (Table, Dry Run):

```bash

##############
ACTION    VM_NAME    PROJECT    STATUS
DELETE    test-vm1   admin      Pending
DELETE    test-vm2   admin      Pending
##############
Dry-run mode enabled. No VMs deleted.

```

```
Flags:

--verbose: Enable verbose debug output.
--filter: Filter VMs (e.g., host=host1,email=user@example.com,status=ACTIVE,project=proj1,days>7). Supported operators for days: >, <, =, >=, <=.
--output: Output format (table or json). Default: table.
--timeout: Request timeout in seconds. Default: varies by subcommand.
--vm: Comma-separated list of VM names (for manage).
--project: Project name (for manage).
--dry-run: Preview actions without executing (for manage).

```
### 2. clean-nova-stale-vms

Cleans stale (orphaned) VMs on a NovaLink hypervisor by comparing OpenStack’s VM list with the host’s inventory.

Note: Ensure the hypervisor host is in maintenance mode before running this action.

Example:

```bash

./openstack-tool clean-nova-stale-vms --verbose --user=root --ip=192.168.1.100 --dry-run --output=table --timeout=300
```

Output (Table, Dry Run):
```

##############
VM_NAME        TENANT    STATUS
vm-orphaned1   Unknown   Running
vm-orphaned2   Unknown   Running
##############
OpenStack VM count: 50
Remote VM count: 52
Missing VM count: 2
Dry-run mode enabled. VMs that would be deleted: vm-orphaned1, vm-orphaned2

```

```
Flags:
--verbose: Enable verbose debug output.
--user: SSH username (default: root).
--password: SSH password (optional; use SSH keys for security).
--ip: NovaLink host IP (required).
--dry-run: Preview VMs to be deleted without taking action.
--output: Output format (table or json). Default: table.
--timeout: Request timeout in seconds. Default: varies.

```
### 3. user-roles

Manages user roles in OpenStack, such as listing users in a project.

Example:

```bash
./openstack-tool user-roles --action=list-users-in-project --project=admin --output=table --timeout=300

```

Output (Table):

```
##############
USER_NAME    EMAIL                ROLES
user1        user1@example.com    admin,member
user2        user2@example.com    member
##############
```

```
Flags:
--action: Action to perform (e.g., list-users-in-project).
--project: Project name (required for list-users-in-project).
--output: Output format (table or json). Default: table.
--timeout: Request timeout in seconds. Default: varies.
```
### 4. volume

Manages volumes in OpenStack, including listing volumes for a project or all volumes, with options to filter unassociated volumes.

volume list: Lists volumes for a specific project.

Example:

```bash
./openstack-tool volume list --project=proj1 --not-associated --output=table
```

Output (Table):

```
##############
VOLUME_ID    NAME        SIZE    STATUS    PROJECT
vol-001      vol-test    10GB    available proj1
vol-002      vol-backup  20GB    available proj1
##############
```
volume list-all: Lists all volumes across projects with detailed output.

Example:

```bash
./openstack-tool volume list-all --long --not-associated --output=json
```

Output (JSON):

```bash
[
  {
    "VOLUME_ID": "vol-001",
    "NAME": "vol-test",
    "SIZE": "10GB",
    "STATUS": "available",
    "PROJECT": "proj1",
    "CREATED_AT": "2025-05-01T10:00:00Z"
  }
]
```

Flags:
```
--project: Project name (for list).
--not-associated: Show only volumes not attached to VMs.
--long: Include additional details (e.g., creation time) (for list-all).
--output: Output format (table or json). Default: table.
--timeout: Request timeout in seconds. Default: varies.
```

### 5. images

Manages OpenStack images, such as listing images for a project.

Example:

```bash
./openstack-tool images --action=list --project=proj1 --output=table --timeout=300
```
Output (Table):

```

##############
IMAGE_ID    NAME        SIZE    STATUS    PROJECT
img-001     ubuntu-20.04 2GB     active    proj1
img-002     centos-8     3GB     active    proj1
##############

```
```
Flags:
--action: Action to perform (e.g., list).
--project: Project name (required for list).
--output: Output format (table or json). Default: table.
--timeout: Request timeout in seconds. Default: varies.

```
### 6. storage

Manages storage volumes on a specified storage system with the vol subcommand.

storage vol list: Lists storage volumes on a storage system.

Example:

```bash
./openstack-tool storage vol list --ip=192.168.1.100 --username=admin --password=secret --long --timeout=300
```
Output (Table):

```
##############
VOLUME_ID    NAME        SIZE    STATUS    STORAGE_SYSTEM
stor-vol-001 data-vol    100GB   online    192.168.1.100
stor-vol-002 backup-vol  200GB   online    192.168.1.100
##############
```

Flags:
```
--ip: Storage system IP (required).
--username: Storage system username (required).
--password: Storage system password (optional; use secure methods like SSH keys if supported).
--long: Include additional details (e.g., creation time).
--timeout: Request timeout in seconds. Default: varies.
```

SSH Key Setup
For subcommands requiring SSH access (clean-nova-stale-vms, storage), configure SSH key-based authentication for security:

Generate an SSH key pair if you don’t have one:

```bash
ssh-keygen -t rsa -b 4096
```
Copy the public key to the target host:

```bash
ssh-copy-id root@192.168.1.100
```
Run the tool without the --password flag:

```bash
./openstack-tool clean-nova-stale-vms --user=root --ip=192.168.1.100 --dry-run
```

Build

Build the executable with:

```bash
make
```
### Contributing

Contributions are welcome! To contribute:

#### Fork the repository.
Create a feature branch (git checkout -b feature/your-feature).
Commit your changes (git commit -m 'Add your feature').
Push to the branch (git push origin feature/your-feature).
Open a pull request.
Please include tests and update documentation as needed.

#### License
This project is licensed under the MIT License. See the LICENSE file for details.

#### About
Developed by Sudeesh John. For questions or feedback, open an issue on the GitHub repository.
