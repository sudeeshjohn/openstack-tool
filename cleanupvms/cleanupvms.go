package cleanupvms

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"golang.org/x/crypto/ssh"
)

// InstanceInfo holds the instance name, tenant name, and status for a VM.
type InstanceInfo struct {
	InstanceName string
	TenantName   string
	Status       string
}

// VM represents a virtual machine with Name and Status fields
type VM struct {
	Name   string
	Status string
}

// Run executes the VM cleanup logic
func Run(user, password, ip string, dryRun bool) error {
	provider, err := authenticateOpenStack()
	if err != nil {
		return fmt.Errorf("error authenticating OpenStack: %v", err)
	}
	if err := verifyOpenStackAuthentication(provider); err != nil {
		return fmt.Errorf("authentication error: %v", err)
	}
	hypervisorsList, err := fetchHypervisorList(provider)
	if err != nil {
		return fmt.Errorf("error fetching hypervisor list: %v", err)
	}
	hypervisorHostname := resolveHostname(ip, hypervisorsList)
	if hypervisorHostname == "" {
		return fmt.Errorf("no matching hypervisor found for IP: %s", ip)
	}
	var wg sync.WaitGroup
	var openstackInstances []InstanceInfo
	var remoteVMs []VM
	var errOpenStack, errRemote error
	wg.Add(2)
	go func() {
		defer wg.Done()
		openstackInstances, errOpenStack = fetchOpenStackVMList(provider, hypervisorHostname)
	}()
	go func() {
		defer wg.Done()
		remoteVMs, errRemote = fetchRemoteVMListSSH(user, password, ip)
	}()
	wg.Wait()
	if errOpenStack != nil {
		return fmt.Errorf("error fetching OpenStack VM list: %v", errOpenStack)
	}
	if errRemote != nil {
		return fmt.Errorf("error fetching remote VM list: %v", errRemote)
	}
	fmt.Printf("🔹 OpenStack VM count: %d\n", len(openstackInstances))
	fmt.Printf("🔹 Remote VM count: %d\n", len(remoteVMs))
	missingVMs := findMissingVms(openstackInstances, remoteVMs)
	fmt.Printf("🔹 Missing VM count: %d\n", len(missingVMs))
	if len(missingVMs) == 0 {
		fmt.Println("✅ No missing VMs detected!")
	} else {
		fmt.Println("Missing VMs:")
		for _, vm := range missingVMs {
			fmt.Printf(" - VM: %s, Tenant: %s, Status: %s\n", vm.InstanceName, vm.TenantName, vm.Status)
		}
		deleteAbandonedVMs(user, password, ip, missingVMs, dryRun)
	}
	return nil
}

func authenticateOpenStack() (*gophercloud.ProviderClient, error) {
	opts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load auth options: %v", err)
	}
	provider, err := openstack.AuthenticatedClient(context.Background(), opts)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %v", err)
	}
	return provider, nil
}

func verifyOpenStackAuthentication(provider *gophercloud.ProviderClient) error {
	opts := gophercloud.EndpointOpts{}
	_, err := openstack.NewIdentityV3(provider, opts)
	if err != nil {
		return fmt.Errorf("failed to create identity v3 client: %v", err)
	}
	fmt.Println("✅ Identity V3 client created successfully!")
	return nil
}

func fetchHypervisorList(provider *gophercloud.ProviderClient) ([]hypervisors.Hypervisor, error) {
	computeClient, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %v", err)
	}
	ctx := context.Background()
	allPages, err := hypervisors.List(computeClient, hypervisors.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list hypervisors: %v", err)
	}
	hypervisorsList, err := hypervisors.ExtractHypervisors(allPages)
	if err != nil {
		return nil, fmt.Errorf("failed to extract hypervisors: %v", err)
	}
	return hypervisorsList, nil
}

func resolveHostname(ip string, hypervisorsList []hypervisors.Hypervisor) string {
	for _, hypervisor := range hypervisorsList {
		if hypervisor.HostIP == ip {
			return hypervisor.HypervisorHostname
		}
	}
	return ""
}

func fetchOpenStackVMList(provider *gophercloud.ProviderClient, hypervisorHostname string) ([]InstanceInfo, error) {
	region := os.Getenv("OS_REGION_NAME")
	if region == "" {
		return nil, fmt.Errorf("OS_REGION_NAME not set")
	}
	client, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %v", err)
	}
	projectList, err := fetchAllProjects(provider)
	if err != nil {
		return nil, fmt.Errorf("error fetching projects: %v", err)
	}
	var instanceNames []InstanceInfo
	for _, project := range projectList {
		instances, err := fetchVMsForProject(client, project, hypervisorHostname)
		if err != nil {
			fmt.Printf("Error fetching VMs for project %s: %v\n", project.Name, err)
			continue
		}
		for _, instance := range instances {
			instanceNames = append(instanceNames, InstanceInfo{
				InstanceName: instance,
				TenantName:   project.Name,
				Status:       "",
			})
		}
	}
	return instanceNames, nil
}

func fetchAllProjects(provider *gophercloud.ProviderClient) ([]projects.Project, error) {
	identityClient, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to create identity client: %v", err)
	}
	ctx := context.Background()
	allPages, err := projects.List(identityClient, projects.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %v", err)
	}
	projectList, err := projects.ExtractProjects(allPages)
	if err != nil {
		return nil, fmt.Errorf("failed to extract projects: %v", err)
	}
	return projectList, nil
}

func fetchVMsForProject(client *gophercloud.ServiceClient, project projects.Project, hypervisorHostname string) ([]string, error) {
	opts := servers.ListOpts{
		AllTenants: true,
		TenantID:   project.ID,
	}
	ctx := context.Background()
	allPages, err := servers.List(client, opts).AllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list servers: %v", err)
	}
	serversList, err := servers.ExtractServers(allPages)
	if err != nil {
		return nil, fmt.Errorf("failed to extract servers: %v", err)
	}
	var filteredInstances []string
	for _, server := range serversList {
		if strings.EqualFold(server.HypervisorHostname, hypervisorHostname) {
			if server.InstanceName != "" {
				filteredInstances = append(filteredInstances, server.InstanceName)
			} else {
				fmt.Printf("Server %s missing OS-EXT-SRV-ATTR:instance_name\n", server.Name)
			}
		}
	}
	return filteredInstances, nil
}

func fetchRemoteVMListSSH(user, password, ip string) ([]VM, error) {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	client, err := ssh.Dial("tcp", ip+":22", config)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %v", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("SSH session failed: %v", err)
	}
	defer session.Close()
	cmd := "export TERM=xterm; pvmctl vm list --display-fields LogicalPartition.name LogicalPartition.state | awk '!/ltc.*-nova/'"
	output, err := session.Output(cmd)
	if err != nil {
		return nil, fmt.Errorf("command failed: %v", err)
	}
	var remoteVMs []VM
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		vmInfo := make(map[string]string)
		for _, field := range fields {
			parts := strings.Split(field, "=")
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				vmInfo[key] = value
			}
		}
		if name, exists := vmInfo["name"]; exists {
			if state, exists := vmInfo["state"]; exists {
				remoteVMs = append(remoteVMs, VM{Name: name, Status: state})
			}
		}
	}
	return remoteVMs, nil
}

func findMissingVms(vmInstances []InstanceInfo, remoteVMs []VM) []InstanceInfo {
	var missing []InstanceInfo
	for _, remoteVM := range remoteVMs {
		found := false
		for _, instance := range vmInstances {
			if instance.InstanceName == remoteVM.Name {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, InstanceInfo{
				InstanceName: remoteVM.Name,
				TenantName:   "Unknown",
				Status:       remoteVM.Status,
			})
		}
	}
	return missing
}

func deleteAbandonedVMs(user, password, ip string, abandonedVMs []InstanceInfo, dryRun bool) {
	if len(abandonedVMs) == 0 {
		fmt.Println("✅ No abandoned VMs to delete.")
		return
	}
	if dryRun {
		fmt.Println("⚠️ Dry-run mode enabled. VMs that would be deleted:")
		for _, vm := range abandonedVMs {
			fmt.Printf(" - VM: %s, Tenant: %s, Status: %s\n", vm.InstanceName, vm.TenantName, vm.Status)
		}
		return
	}
	fmt.Printf("⚠️ Do you want to delete %d VMs? (yes/no): ", len(abandonedVMs))
	var response string
	fmt.Scanln(&response)
	if strings.ToLower(response) != "yes" {
		fmt.Println("❌ Deletion aborted by user.")
		return
	}
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	client, err := ssh.Dial("tcp", ip+":22", config)
	if err != nil {
		fmt.Println("SSH connection error:", err)
		return
	}
	defer client.Close()
	for _, vm := range abandonedVMs {
		session, err := client.NewSession()
		if err != nil {
			fmt.Printf("❌ SSH session failed for %s: %v\n", vm.InstanceName, err)
			continue
		}
		defer session.Close()
		cmd := fmt.Sprintf("pvmctl LogicalPartition delete --object-id name=%s", vm.InstanceName)
		output, err := session.CombinedOutput(cmd)
		if err != nil {
			fmt.Printf("❌ Failed to delete VM %s (Tenant: %s): %v, Output: %s\n", vm.InstanceName, vm.TenantName, err, output)
		} else {
			fmt.Printf("✅ VM %s (Tenant: %s) deleted successfully!\n", vm.InstanceName, vm.TenantName)
		}
	}
}
