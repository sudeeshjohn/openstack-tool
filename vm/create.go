package vm

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/availabilityzones"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
)

// CreateVM handles the interactive creation of a new VM.
func CreateVM(ctx context.Context) error {
	// Check required environment variables
	requiredEnvVars := []string{"OS_AUTH_URL", "OS_USERNAME", "OS_PASSWORD", "OS_REGION_NAME"}
	for _, env := range requiredEnvVars {
		if os.Getenv(env) == "" {
			return fmt.Errorf("missing required environment variable: %s", env)
		}
	}

	// Auth from ENV
	opts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		return fmt.Errorf("auth from env: %v", err)
	}

	// Unscoped auth to list projects
	unauthProvider, err := openstack.NewClient(opts.IdentityEndpoint)
	if err != nil {
		return fmt.Errorf("unauth provider: %v", err)
	}

	err = openstack.Authenticate(ctx, unauthProvider, opts)
	if err != nil {
		return fmt.Errorf("unauth provider auth: %v", err)
	}

	identityClient, err := openstack.NewIdentityV3(unauthProvider, gophercloud.EndpointOpts{})
	if err != nil {
		return fmt.Errorf("identity v3: %v", err)
	}

	projectID := selectProject(ctx, identityClient)
	opts.TenantID = projectID

	// Auth with selected project (scoped)
	provider, err := openstack.AuthenticatedClient(ctx, opts)
	if err != nil {
		return fmt.Errorf("scoped auth: %v", err)
	}

	computeClient, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{})
	if err != nil {
		return fmt.Errorf("compute client: %v", err)
	}

	imageClient, err := openstack.NewImageV2(provider, gophercloud.EndpointOpts{})
	if err != nil {
		return fmt.Errorf("image client: %v", err)
	}

	networkClient, err := openstack.NewNetworkV2(provider, gophercloud.EndpointOpts{})
	if err != nil {
		return fmt.Errorf("network client: %v", err)
	}
	// Interactive input
	fmt.Println("==== OpenStack VM Creator ====")
	name := prompt("Enter VM name: ")
	if len(name) == 0 || len(name) > 255 {
		return fmt.Errorf("invalid VM name: must be between 1 and 255 characters")
	}
	zone := selectAvailabilityZone(ctx, computeClient)
	host := selectComputeHost(ctx, computeClient, zone)
	fmt.Printf("Selected availability zone: %s, compute host: %s (host used for info only, zone applied to VM creation)\n", zone, host)
	imageID := selectImage(ctx, imageClient)
	flavorID := selectFlavor(ctx, computeClient)
	networkID := selectNetwork(ctx, networkClient)
	keypair := selectKeyPair(ctx, computeClient)

	// Create VM
	createOpts := servers.CreateOpts{
		Name:             name,
		ImageRef:         imageID,
		FlavorRef:        flavorID,
		Networks:         []servers.Network{{UUID: networkID}},
		AvailabilityZone: zone,
	}
	// Add key pair
	createOptsExt := keypairs.CreateOptsExt{
		CreateOptsBuilder: createOpts,
		KeyName:           keypair,
	}
	fmt.Println("Creating VM...")
	server, err := servers.Create(ctx, computeClient, createOptsExt, nil).Extract()
	if err != nil {
		return fmt.Errorf("create VM: %v", err)
	}

	fmt.Printf("✅ Created: %s (ID: %s)\n", server.Name, server.ID)

	// Poll VM status
	fmt.Println("Checking VM status...")
	for i := 0; i < 30; i++ { // Timeout after ~60 seconds
		server, err := servers.Get(ctx, computeClient, server.ID).Extract()
		if err != nil {
			return fmt.Errorf("get VM status: %v", err)
		}
		if server.Status == "ACTIVE" || server.Status == "ERROR" {
			break
		}
		fmt.Printf("Current status: %s,  waiting...\n", server.Status)
		time.Sleep(10 * time.Second)
	}
	var ipAddress string
	server, err = servers.Get(ctx, computeClient, server.ID).Extract()
	addresses := server.Addresses
	for _, network := range addresses {
		networkList, ok := network.([]interface{})
		if !ok {
			continue
		}
		for _, addr := range networkList {
			addrMap, ok := addr.(map[string]interface{})
			if !ok {
				continue
			}
			if ip, ok := addrMap["addr"].(string); ok {
				ip = strings.Split(ip, "%")[0]
				ipAddress = ip
				break
			}
		}
		if ipAddress != "" {
			break
		}
	}
	fmt.Printf("IP ADDRESS IS: %s", ipAddress)
	return nil
}

func checkErr(context string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %s: %v\n", context, err)
		os.Exit(1)
	}
}

func prompt(msg string) string {
	fmt.Print(msg)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	return strings.TrimSpace(scanner.Text())
}

func toChoice(input string, max int) int {
	i, err := strconv.Atoi(input)
	if err != nil || i == 0 {
		fmt.Println("Skipped.")
		return 0
	}
	if err != nil || i < 1 || i > max {
		fmt.Println("Invalid choice.")
		return -1
	}
	return i
}

func selectProject(ctx context.Context, identityClient *gophercloud.ServiceClient) string {
	pages, err := projects.List(identityClient, nil).AllPages(ctx)
	if err != nil {
		checkErr("list projects", err)
	}

	allProjects, err := projects.ExtractProjects(pages)
	if err != nil {
		checkErr("extract projects", err)
	}

	for i, p := range allProjects {
		fmt.Printf("%d) %s (%s)\n", i+1, p.Name, p.ID)
	}
	for retries := 0; retries < 3; retries++ {
		idx := toChoice(prompt("Choose project: "), len(allProjects)+1)
		if idx >= 0 {
			fmt.Printf("You Chose: %s\n", allProjects[idx-1].Name)
			return allProjects[idx-1].ID
		}
		fmt.Printf("Invalid choice. %d retries left.\n", 2-retries)
	}
	fmt.Println("❌ Too many invalid attempts. Exiting.")
	os.Exit(1)
	return ""
}

func selectAvailabilityZone(ctx context.Context, client *gophercloud.ServiceClient) string {
	zones, err := availabilityzones.ListDetail(client).AllPages(ctx)
	if err != nil {
		checkErr("list availability zones", err)
	}

	allZones, err := availabilityzones.ExtractAvailabilityZones(zones)
	if err != nil {
		checkErr("extract availability zones", err)
	}

	if len(allZones) == 0 {
		fmt.Println("⚠️ No availability zones found. Proceeding without a zone.")
		return ""
	}

	// Collect only available zones, excluding 'internal'
	var availableZones []availabilityzones.AvailabilityZone
	for _, zone := range allZones {
		if zone.ZoneState.Available && zone.ZoneName != "internal" {
			availableZones = append(availableZones, zone)
		}
	}

	if len(availableZones) == 0 {
		fmt.Println("⚠️ No available availability zones found (excluding internal). Proceeding without a zone.")
		return ""
	}

	for i, zone := range availableZones {
		fmt.Printf("%d) %s\n", i+1, zone.ZoneName)
	}
	for retries := 0; retries < 3; retries++ {
		idx := toChoice(prompt("Choose availability zone (or enter 0 to skip): "), len(availableZones)+1)
		if idx == -1 {
			fmt.Printf("Invalid choice. %d retries left.\n", 2-retries)
			continue
		}
		if idx == 0 {
			return "" // Skip zone
		}
		fmt.Printf("You Chose: %s\n", availableZones[idx-1].ZoneName)
		return availableZones[idx-1].ZoneName
	}
	fmt.Println("❌ Too many invalid attempts. Exiting.")
	os.Exit(1)
	return ""
}

func selectImage(ctx context.Context, client *gophercloud.ServiceClient) string {
	pages, err := images.List(client, images.ListOpts{Status: "active"}).AllPages(ctx)
	if err != nil {
		checkErr("list images", err)
	}

	imgs, err := images.ExtractImages(pages)
	if err != nil {
		checkErr("extract images", err)
	}

	for i, img := range imgs {
		fmt.Printf("%d) %s\n", i+1, img.Name)
	}
	for retries := 0; retries < 3; retries++ {
		idx := toChoice(prompt("Choose image: "), len(imgs)+1)
		if idx >= 0 {
			fmt.Printf("You Chose: %s\n", imgs[idx-1].Name)
			return imgs[idx-1].ID
		}
		fmt.Printf("Invalid choice. %d retries left.\n", 2-retries)
	}
	fmt.Println("❌ Too many invalid attempts. Exiting.")
	os.Exit(1)
	return ""
}

func selectFlavor(ctx context.Context, client *gophercloud.ServiceClient) string {
	pages, err := flavors.ListDetail(client, nil).AllPages(ctx)
	if err != nil {
		checkErr("list flavors", err)
	}

	allFlavors, err := flavors.ExtractFlavors(pages)
	if err != nil {
		checkErr("extract flavors", err)
	}

	for i, fl := range allFlavors {
		fmt.Printf("%d) %s (%d vCPU, %dMB RAM)\n", i+1, fl.Name, fl.VCPUs, fl.RAM)
	}
	for retries := 0; retries < 3; retries++ {
		idx := toChoice(prompt("Choose flavor: "), len(allFlavors)+1)
		if idx >= 0 {
			fmt.Printf("You Chose: %s\n", allFlavors[idx-1].Name)
			return allFlavors[idx-1].ID
		}
		fmt.Printf("Invalid choice. %d retries left.\n", 2-retries)
	}
	fmt.Println("❌ Too many invalid attempts. Exiting.")
	os.Exit(1)
	return ""
}

func selectNetwork(ctx context.Context, client *gophercloud.ServiceClient) string {
	pages, err := networks.List(client, nil).AllPages(ctx)
	if err != nil {
		checkErr("list networks", err)
	}

	nets, err := networks.ExtractNetworks(pages)
	if err != nil {
		checkErr("extract networks", err)
	}

	for i, net := range nets {
		fmt.Printf("%d) %s (%s)\n", i+1, net.Name, net.ID)
	}
	for retries := 0; retries < 3; retries++ {
		idx := toChoice(prompt("Choose network: "), len(nets)+1)
		if idx >= 0 {
			fmt.Printf("You Chose: %s\n", nets[idx-1].Name)
			return nets[idx-1].ID
		}
		fmt.Printf("Invalid choice. %d retries left.\n", 2-retries)
	}
	fmt.Println("❌ Too many invalid attempts. Exiting.")
	os.Exit(1)
	return ""
}

func selectComputeHost(ctx context.Context, client *gophercloud.ServiceClient, zone string) string {
	pages, err := hypervisors.List(client, nil).AllPages(ctx)
	if err != nil {
		checkErr("list hypervisors", err)
	}

	hosts, err := hypervisors.ExtractHypervisors(pages)
	if err != nil {
		checkErr("extract hypervisors", err)
	}

	if len(hosts) == 0 {
		fmt.Println("⚠️ No hypervisors found. Proceeding without a host selection.")
		return ""
	}

	if zone != "" {
		fmt.Printf("Please choose a compute host from the availability zone '%s'. Use 'openstack hypervisor list --long' to verify hosts in this zone.\n", zone)
	} else {
		fmt.Println("No availability zone selected. Choose any compute host.")
	}

	zones, err := availabilityzones.ListDetail(client).AllPages(ctx)
	if err != nil {
		checkErr("list availability zones", err)
	}

	allZones, err := availabilityzones.ExtractAvailabilityZones(zones)
	if err != nil {
		checkErr("extract availability zones", err)
	}

	if len(allZones) == 0 {
		fmt.Println("⚠️ No availability zones found. Proceeding without a zone.")
		return ""
	}

	// Collect only available zones, excluding 'internal'
	var availableZones []availabilityzones.AvailabilityZone
	for _, zone1 := range allZones {
		if zone1.ZoneState.Available && zone1.ZoneName != "internal" {
			availableZones = append(availableZones, zone1)
		}
	}

	var zoneHosts []string
	for _, zone1 := range availableZones {
		if zone1.ZoneName == zone {
			for zoneHost := range zone1.Hosts {
				zoneHosts = append(zoneHosts, zoneHost)
			}
		}
	}

	for _, h := range hosts {
		for j, zh := range zoneHosts {
			if h.HypervisorHostname == zh {
				fmt.Printf("%d) %s\n", j+1, h.HypervisorHostname)
			}
		}
	}
	for retries := 0; retries < 3; retries++ {
		fmt.Printf("zoneHosts number: %d\n", len(zoneHosts))
		idx := toChoice(prompt("Choose compute host (or enter 0 to skip): "), len(zoneHosts)+1)
		if idx == -1 {
			fmt.Printf("Invalid choice. %d retries left.\n", 2-retries)
			continue
		}
		if idx == 0 {
			return "" // Skip host which will pick any host from the availability zone
		}
		fmt.Printf("You Chose: %s\n", hosts[idx-1].HypervisorHostname)
		return hosts[idx-1].HypervisorHostname
	}
	fmt.Println("❌ Too many invalid attempts. Exiting.")
	os.Exit(1)
	return ""
}

func selectKeyPair(ctx context.Context, client *gophercloud.ServiceClient) string {
	pages, err := keypairs.List(client, nil).AllPages(ctx)
	if err != nil {
		checkErr("list keypairs", err)
	}

	allKeypairs, err := keypairs.ExtractKeyPairs(pages)
	if err != nil {
		checkErr("extract keypairs", err)
	}

	if len(allKeypairs) == 0 {
		fmt.Println("⚠️ No key pairs found. Proceeding without a key pair.")
		return ""
	}

	for i, kp := range allKeypairs {
		fmt.Printf("%d) %s\n", i+1, kp.Name)
	}
	for retries := 0; retries < 3; retries++ {
		idx := toChoice(prompt("Choose key pair (or enter 0 to skip): "), len(allKeypairs)+1)
		if idx == -1 {
			fmt.Printf("Invalid choice. %d retries left.\n", 2-retries)
			continue
		}
		if idx == 0 {
			return ""
		}
		fmt.Printf("You chose: %s\n", allKeypairs[idx-1].Name)
		return allKeypairs[idx-1].Name
	}
	fmt.Println("❌ Too many invalid attempts. Exiting.")
	os.Exit(1)
	return ""
}
