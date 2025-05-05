package cleannovastalevms

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/sirupsen/logrus"
	"github.com/sudeeshjohn/openstack-tool/auth"
	"github.com/sudeeshjohn/openstack-tool/util"
	"golang.org/x/crypto/ssh"
)

// Logger for structured logging
var log = logrus.New()

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
func Run(ctx context.Context, client *auth.Client, verbose bool, user, password, ip, outputFormat string, dryRun bool) error {
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if verbose {
		log.SetLevel(logrus.DebugLevel)
	}
	log.Debugf("Starting VM cleanup for IP: %s, User: %s, OutputFormat: %s, DryRun: %v, Verbose: %v", ip, user, outputFormat, dryRun, verbose)

	region := os.Getenv("OS_REGION_NAME")
	if region == "" {
		log.Debug("OS_REGION_NAME environment variable not set")
		return fmt.Errorf("OS_REGION_NAME not set")
	}

	log.Debug("Fetching hypervisor list")
	hypervisorsList, err := fetchHypervisorList(ctx, client)
	if err != nil {
		log.Debugf("Failed to fetch hypervisor list: %v", err)
		return fmt.Errorf("error fetching hypervisor list: %v", err)
	}
	log.Debugf("Found %d hypervisors", len(hypervisorsList))

	log.Debugf("Resolving hostname for IP: %s", ip)
	hypervisorHostname := resolveHostname(ip, hypervisorsList)
	if hypervisorHostname == "" {
		log.Debugf("No hypervisor found for IP: %s", ip)
		return fmt.Errorf("no matching hypervisor found for IP: %s", ip)
	}
	log.Debugf("Resolved hostname: %s", hypervisorHostname)

	var wg sync.WaitGroup
	var openstackInstances []InstanceInfo
	var remoteVMs []VM
	var errOpenStack, errRemote error
	wg.Add(2)
	log.Debug("Launching goroutines for OpenStack and remote VM list fetching")
	go func() {
		defer wg.Done()
		log.Debug("Fetching OpenStack VM list")
		openstackInstances, errOpenStack = fetchOpenStackVMList(ctx, client, hypervisorHostname, region)
	}()
	go func() {
		defer wg.Done()
		log.Debug("Fetching remote VM list via SSH")
		remoteVMs, errRemote = fetchRemoteVMListSSH(user, password, ip)
	}()
	wg.Wait()
	log.Debugf("Fetched OpenStack VMs: %d, Remote VMs: %d", len(openstackInstances), len(remoteVMs))
	if errOpenStack != nil {
		log.Debugf("Error fetching OpenStack VM list: %v", errOpenStack)
		return fmt.Errorf("error fetching OpenStack VM list: %v", errOpenStack)
	}
	if errRemote != nil {
		log.Debugf("Error fetching remote VM list: %v", errRemote)
		return fmt.Errorf("error fetching remote VM list: %v", errRemote)
	}

	// Output results
	if strings.ToLower(outputFormat) == "json" {
		log.Debug("Preparing JSON output")
		data, err := json.MarshalIndent(struct {
			OpenStackVMs []InstanceInfo `json:"openstack_vms"`
			RemoteVMs    []VM           `json:"remote_vms"`
			MissingVMs   []InstanceInfo `json:"missing_vms"`
		}{
			OpenStackVMs: openstackInstances,
			RemoteVMs:    remoteVMs,
			MissingVMs:   findMissingVms(openstackInstances, remoteVMs),
		}, "", "  ")
		if err != nil {
			log.Debugf("Failed to marshal JSON: %v", err)
			return fmt.Errorf("failed to marshal JSON: %v", err)
		}
		fmt.Println(string(data))
	} else {
		log.Debug("Preparing table output")
		fmt.Printf("🔹 OpenStack VM count: %d\n", len(openstackInstances))
		fmt.Printf("🔹 Remote VM count: %d\n", len(remoteVMs))
		fmt.Printf("🔹 Missing VM count: %d\n", len(findMissingVms(openstackInstances, remoteVMs)))
		if len(findMissingVms(openstackInstances, remoteVMs)) == 0 {
			fmt.Println("✅ No missing VMs detected!")
		} else {
			fmt.Println("Missing VMs:")
			for _, vm := range findMissingVms(openstackInstances, remoteVMs) {
				fmt.Printf(" - VM: %s, Tenant: %s, Status: %s\n", vm.InstanceName, vm.TenantName, vm.Status)
			}
		}
	}

	if len(findMissingVms(openstackInstances, remoteVMs)) > 0 {
		log.Debugf("Found %d missing VMs, initiating deletion process", len(findMissingVms(openstackInstances, remoteVMs)))
		deleteAbandonedVMs(user, password, ip, findMissingVms(openstackInstances, remoteVMs), dryRun, outputFormat)
	}
	log.Debug("VM cleanup process completed")
	return nil
}

func fetchHypervisorList(ctx context.Context, client *auth.Client) ([]hypervisors.Hypervisor, error) {
	log.Debug("Fetching hypervisor list from OpenStack")
	var hypervisorsList []hypervisors.Hypervisor
	err := util.WithRetry(3, time.Second, func() error {
		log.Debug("Attempting to list hypervisors")
		allPages, err := hypervisors.List(client.Compute, hypervisors.ListOpts{}).AllPages(ctx)
		if err != nil {
			log.Debugf("Failed to list hypervisors: %v", err)
			return fmt.Errorf("failed to list hypervisors: %v", err)
		}
		hypervisorsList, err = hypervisors.ExtractHypervisors(allPages)
		if err != nil {
			log.Debugf("Failed to extract hypervisors: %v", err)
			return fmt.Errorf("failed to extract hypervisors: %v", err)
		}
		log.Debugf("Extracted %d hypervisors", len(hypervisorsList))
		return nil
	})
	if err != nil {
		log.Debugf("Hypervisor list fetch failed after retries: %v", err)
		return nil, err
	}
	log.Debug("Hypervisor list fetch successful")
	return hypervisorsList, nil
}

func resolveHostname(ip string, hypervisorsList []hypervisors.Hypervisor) string {
	log.Debugf("Resolving hostname for IP: %s", ip)
	for _, hypervisor := range hypervisorsList {
		if hypervisor.HostIP == ip {
			log.Debugf("Matched IP %s to hostname %s", ip, hypervisor.HypervisorHostname)
			return hypervisor.HypervisorHostname
		}
	}
	log.Debugf("No hostname found for IP: %s", ip)
	return ""
}

func fetchOpenStackVMList(ctx context.Context, client *auth.Client, hypervisorHostname, region string) ([]InstanceInfo, error) {
	log.Debugf("Fetching OpenStack VM list for hypervisor: %s, region: %s", hypervisorHostname, region)
	projectList, err := fetchAllProjects(ctx, client)
	if err != nil {
		log.Debugf("Failed to fetch projects: %v", err)
		return nil, fmt.Errorf("error fetching projects: %v", err)
	}
	log.Debugf("Fetched %d projects", len(projectList))

	var instanceNames []InstanceInfo
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Limit to 10 concurrent project queries

	for _, project := range projectList {
		wg.Add(1)
		go func(project projects.Project) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			log.Debugf("Fetching VMs for project: %s (ID: %s)", project.Name, project.ID)
			instances, err := fetchVMsForProject(ctx, client, project, hypervisorHostname)
			if err != nil {
				log.Debugf("Error fetching VMs for project %s: %v", project.Name, err)
				fmt.Printf("Error fetching VMs for project %s: %v\n", project.Name, err)
				return
			}
			log.Debugf("Fetched %d VMs for project %s", len(instances), project.Name)
			mu.Lock()
			for _, instance := range instances {
				instanceNames = append(instanceNames, InstanceInfo{
					InstanceName: instance,
					TenantName:   project.Name,
					Status:       "",
				})
			}
			mu.Unlock()
		}(project)
	}
	wg.Wait()
	close(sem)
	log.Debugf("Total OpenStack VMs fetched: %d", len(instanceNames))
	return instanceNames, nil
}

func fetchAllProjects(ctx context.Context, client *auth.Client) ([]projects.Project, error) {
	log.Debug("Fetching all projects from OpenStack")
	var projectList []projects.Project
	err := util.WithRetry(3, time.Second, func() error {
		log.Debug("Attempting to list projects")
		allPages, err := projects.List(client.Identity, projects.ListOpts{}).AllPages(ctx)
		if err != nil {
			log.Debugf("Failed to list projects: %v", err)
			return fmt.Errorf("failed to list projects: %v", err)
		}
		projectList, err = projects.ExtractProjects(allPages)
		if err != nil {
			log.Debugf("Failed to extract projects: %v", err)
			return fmt.Errorf("failed to extract projects: %v", err)
		}
		log.Debugf("Extracted %d projects", len(projectList))
		return nil
	})
	if err != nil {
		log.Debugf("Project list fetch failed after retries: %v", err)
		return nil, err
	}
	log.Debug("Project list fetch successful")
	return projectList, nil
}

func fetchVMsForProject(ctx context.Context, client *auth.Client, project projects.Project, hypervisorHostname string) ([]string, error) {
	log.Debugf("Fetching VMs for project %s (ID: %s) on hypervisor %s", project.Name, project.ID, hypervisorHostname)
	var filteredInstances []string
	err := util.WithRetry(3, time.Second, func() error {
		log.Debug("Attempting to list servers for project")
		opts := servers.ListOpts{
			AllTenants: true,
			TenantID:   project.ID,
		}
		allPages, err := servers.List(client.Compute, opts).AllPages(ctx)
		if err != nil {
			log.Debugf("Failed to list servers: %v", err)
			return fmt.Errorf("failed to list servers: %v", err)
		}
		serversList, err := servers.ExtractServers(allPages)
		if err != nil {
			log.Debugf("Failed to extract servers: %v", err)
			return fmt.Errorf("failed to extract servers: %v", err)
		}
		log.Debugf("Extracted %d servers", len(serversList))
		filteredInstances = nil // Reset in case of retry
		for _, server := range serversList {
			if strings.EqualFold(server.HypervisorHostname, hypervisorHostname) {
				if server.InstanceName != "" {
					log.Debugf("Adding VM %s to filtered list", server.InstanceName)
					filteredInstances = append(filteredInstances, server.InstanceName)
				} else {
					log.Debugf("Server %s missing OS-EXT-SRV-ATTR:instance_name", server.Name)
					fmt.Printf("Server %s missing OS-EXT-SRV-ATTR:instance_name\n", server.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Debugf("VM fetch for project %s failed after retries: %v", project.Name, err)
		return nil, err
	}
	log.Debugf("Fetched %d VMs for project %s", len(filteredInstances), project.Name)
	return filteredInstances, nil
}

func fetchRemoteVMListSSH(user, password, ip string) ([]VM, error) {
	log.Debugf("Fetching remote VM list via SSH for user: %s, IP: %s", user, ip)
	var remoteVMs []VM
	err := util.WithRetry(3, time.Second, func() error {
		log.Debug("Establishing SSH connection")
		config := &ssh.ClientConfig{
			User: user,
			Auth: []ssh.AuthMethod{
				ssh.Password(password),
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}
		client, err := ssh.Dial("tcp", ip+":22", config)
		if err != nil {
			log.Debugf("SSH connection failed: %v", err)
			return fmt.Errorf("SSH connection failed: %v", err)
		}
		defer client.Close()
		log.Debug("SSH connection established")

		log.Debug("Creating SSH session")
		session, err := client.NewSession()
		if err != nil {
			log.Debugf("SSH session failed: %v", err)
			return fmt.Errorf("SSH session failed: %v", err)
		}
		defer session.Close()
		log.Debug("SSH session created")

		log.Debug("Executing pvmctl command")
		cmd := "export TERM=xterm; pvmctl vm list --display-fields LogicalPartition.name LogicalPartition.state | awk '!/ltc.*-nova/'"
		output, err := session.Output(cmd)
		if err != nil {
			log.Debugf("Command failed: %v - output: %s", err, output)
			return fmt.Errorf("command failed: %v - output: %s", err, output)
		}
		log.Debugf("Command output: %s", string(output))
		remoteVMs = nil // Reset in case of retry
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			log.Debugf("Processing line: %s", line)
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
					log.Debugf("Adding VM: Name=%s, State=%s", name, state)
					remoteVMs = append(remoteVMs, VM{Name: name, Status: state})
				}
			}
		}
		log.Debugf("Fetched %d remote VMs", len(remoteVMs))
		return nil
	})
	if err != nil {
		log.Debugf("Remote VM list fetch failed after retries: %v", err)
		return nil, err
	}
	log.Debug("Remote VM list fetch successful")
	return remoteVMs, nil
}

func findMissingVms(vmInstances []InstanceInfo, remoteVMs []VM) []InstanceInfo {
	log.Debug("Identifying missing VMs")
	var missing []InstanceInfo
	for _, remoteVM := range remoteVMs {
		found := false
		for _, instance := range vmInstances {
			if strings.EqualFold(instance.InstanceName, remoteVM.Name) {
				log.Debugf("Found match for remote VM %s in OpenStack", remoteVM.Name)
				found = true
				break
			}
		}
		if !found {
			log.Debugf("Adding missing VM: %s", remoteVM.Name)
			missing = append(missing, InstanceInfo{
				InstanceName: remoteVM.Name,
				TenantName:   "Unknown",
				Status:       remoteVM.Status,
			})
		}
	}
	log.Debugf("Found %d missing VMs", len(missing))
	return missing
}

func deleteAbandonedVMs(user, password, ip string, abandonedVMs []InstanceInfo, dryRun bool, outputFormat string) {
	log.Debugf("Starting deletion of %d abandoned VMs, DryRun: %v", len(abandonedVMs), dryRun)
	if len(abandonedVMs) == 0 {
		if strings.ToLower(outputFormat) == "json" {
			log.Debug("No abandoned VMs to delete, outputting empty JSON")
			fmt.Println("[]")
		} else {
			log.Debug("No abandoned VMs to delete, outputting message")
			fmt.Println("✅ No abandoned VMs to delete.")
		}
		return
	}
	if dryRun {
		if strings.ToLower(outputFormat) == "json" {
			log.Debug("Dry run mode, marshaling abandoned VMs to JSON")
			data, err := json.MarshalIndent(abandonedVMs, "", "  ")
			if err != nil {
				log.Debugf("Error marshaling JSON: %v", err)
				fmt.Printf("Error marshaling JSON: %v\n", err)
				return
			}
			fmt.Println(string(data))
		} else {
			log.Debug("Dry run mode, listing VMs that would be deleted")
			fmt.Println("⚠️ Dry-run mode enabled. VMs that would be deleted:")
			for _, vm := range abandonedVMs {
				fmt.Printf(" - VM: %s, Tenant: %s, Status: %s\n", vm.InstanceName, vm.TenantName, vm.Status)
			}
		}
		return
	}
	if strings.ToLower(outputFormat) == "json" {
		log.Debugf("Prompting for confirmation to delete %d VMs", len(abandonedVMs))
		fmt.Printf("{\"status\": \"prompt\", \"message\": \"Type 'confirm' to delete %d VMs\"}\n", len(abandonedVMs))
	} else {
		log.Debugf("Prompting for confirmation to delete %d VMs", len(abandonedVMs))
		fmt.Printf("Type 'confirm' to delete %d VMs: ", len(abandonedVMs))
	}
	var response string
	fmt.Scanln(&response)
	if strings.ToLower(response) != "confirm" {
		if strings.ToLower(outputFormat) == "json" {
			log.Debug("Deletion aborted by user, outputting JSON response")
			fmt.Println("{\"status\": \"aborted\", \"message\": \"Deletion aborted by user.\"}")
		} else {
			log.Debug("Deletion aborted by user, outputting message")
			fmt.Println("❌ Deletion aborted by user.")
		}
		return
	}
	log.Debug("User confirmed deletion, establishing SSH connection")
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	client, err := ssh.Dial("tcp", ip+":22", config)
	if err != nil {
		log.Debugf("SSH connection error: %v", err)
		if strings.ToLower(outputFormat) == "json" {
			fmt.Printf("{\"status\": \"error\", \"message\": \"SSH connection error: %v\"}\n", err)
		} else {
			fmt.Println("SSH connection error:", err)
		}
		return
	}
	defer client.Close()
	log.Debug("SSH connection established, starting VM deletion loop")
	for _, vm := range abandonedVMs {
		session, err := client.NewSession()
		if err != nil {
			log.Debugf("SSH session failed for VM %s: %v", vm.InstanceName, err)
			if strings.ToLower(outputFormat) == "json" {
				fmt.Printf("{\"status\": \"error\", \"vm\": %q, \"message\": \"SSH session failed: %v\"}\n", vm.InstanceName, err)
			} else {
				fmt.Printf("❌ SSH session failed for %s: %v\n", vm.InstanceName, err)
			}
			continue
		}
		cmd := fmt.Sprintf("pvmctl LogicalPartition delete --object-id name=%s", vm.InstanceName)
		log.Debugf("Executing deletion command for VM %s: %s", vm.InstanceName, cmd)
		output, err := session.CombinedOutput(cmd)
		session.Close()
		if err != nil {
			log.Debugf("Failed to delete VM %s: %v, Output: %s", vm.InstanceName, err, output)
			if strings.ToLower(outputFormat) == "json" {
				fmt.Printf("{\"status\": \"error\", \"vm\": %q, \"message\": \"Failed to delete VM: %v, Output: %s\"}\n", vm.InstanceName, err, output)
			} else {
				fmt.Printf("❌ Failed to delete VM %s (Tenant: %s): %v, Output: %s\n", vm.InstanceName, vm.TenantName, err, output)
			}
		} else {
			log.Debugf("Successfully deleted VM %s", vm.InstanceName)
			if strings.ToLower(outputFormat) == "json" {
				fmt.Printf("{\"status\": \"success\", \"vm\": %q, \"tenant\": %q, \"command\": %q}\n", vm.InstanceName, vm.TenantName, cmd)
			} else {
				fmt.Printf(" - VM: %s, Tenant: %s, Status: %s → Command: %s\n", vm.InstanceName, vm.TenantName, vm.Status, cmd)
			}
		}
	}
	log.Debug("Abandoned VM deletion process completed")
}
