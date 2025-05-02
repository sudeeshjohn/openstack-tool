package getvminfo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/users"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Config holds configuration parameters
type Config struct {
	Verbose      bool
	FilterStr    string
	OutputFormat string
	Timeout      time.Duration
	MaxRetries   int
	Region       string
}

// Pair and PairList for sorting VMs by user ID
type Pair struct {
	Key   string
	Value string
}

type PairList []Pair

func (p PairList) Len() int           { return len(p) }
func (p PairList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p PairList) Less(i, j int) bool { return p[i].Value < p[j].Value }

// Vmdetails defines the VM details structure
type Vmdetails struct {
	VmName       string
	Project      string
	CreationTime time.Time
	NumberOfDays int
	UserID       string
	UserEmail    string
	VmStatus     string
	VmVcpu       int
	VmMemory     int
	VmProcUnit   float64
	VmHost       string
	IPAddresses  string
}

// UserDetails defines the user details structure
type UserDetails struct {
	UserName    string
	UserProject string
	UserEmail   string
}

// ProjectDetails defines the project details structure
type ProjectDetails struct {
	ProjectName string
}

// FlavorDetails defines the flavor details structure
type FlavorDetails struct {
	Vcpus     int
	Memory    int
	ProcUnits float64
}

// flavorMap protects the flavor map with a read-write mutex
type flavorMap struct {
	sync.RWMutex
	data map[string]FlavorDetails
}

// filter represents a single filter condition
type filter struct {
	Host      string
	Email     string
	Status    string
	Project   string
	DaysOp    string // >, <, =, >=, <=
	DaysValue int
}

// Client holds OpenStack clients
type Client struct {
	Identity *gophercloud.ServiceClient
	Compute  *gophercloud.ServiceClient
}

// Logger for structured logging
var log = logrus.New()

// Run executes the VM info retrieval logic
func Run(verbose bool, filterStr, outputFormat, region string) error {
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if verbose {
		log.SetLevel(logrus.DebugLevel)
	}

	cfg := Config{
		Verbose:      verbose,
		FilterStr:    filterStr,
		OutputFormat: outputFormat,
		Timeout:      120 * time.Second,
		MaxRetries:   3,
		Region:       region,
	}

	if cfg.Region == "" {
		cfg.Region = os.Getenv("OS_REGION_NAME")
		if cfg.Region == "" {
			cfg.Region = "RegionOne"
		}
	}

	// Validate required environment variables
	requiredEnv := []string{"OS_AUTH_URL", "OS_USERNAME", "OS_PASSWORD", "OS_PROJECT_NAME", "OS_DOMAIN_NAME"}
	for _, env := range requiredEnv {
		if os.Getenv(env) == "" {
			return fmt.Errorf("missing required environment variable: %s", env)
		}
	}

	f, err := parseFilters(cfg.FilterStr)
	if err != nil {
		return errors.Wrap(err, "error parsing filter")
	}
	log.Debugf("Applied filters: host=%q, email=%q, status=%q, project=%q, days%s%d",
		f.Host, f.Email, f.Status, f.Project, f.DaysOp, f.DaysValue)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	client, err := initializeClients(ctx, cfg)
	if err != nil {
		return errors.Wrap(err, "failed to initialize clients")
	}

	var wg sync.WaitGroup
	var usersErr, projectsErr, flavorsErr error
	var allUsers []users.User
	var allProjects []projects.Project
	var allFlavors []flavors.Flavor

	wg.Add(3)
	go func() {
		defer wg.Done()
		allUsers, usersErr = fetchUsers(ctx, client)
	}()
	go func() {
		defer wg.Done()
		allProjects, projectsErr = fetchProjects(ctx, client)
	}()
	go func() {
		defer wg.Done()
		allFlavors, flavorsErr = fetchFlavors(ctx, client)
	}()
	wg.Wait()

	if usersErr != nil {
		return errors.Wrap(usersErr, "failed to fetch users")
	}
	if projectsErr != nil {
		return errors.Wrap(projectsErr, "failed to fetch projects")
	}
	if flavorsErr != nil {
		return errors.Wrap(flavorsErr, "failed to fetch flavors")
	}

	allServers, err := fetchServers(ctx, client, cfg)
	if err != nil {
		return errors.Wrap(err, "failed to fetch servers")
	}

	fm, err := processFlavors(ctx, client, allFlavors)
	if err != nil {
		return errors.Wrap(err, "failed to process flavors")
	}

	vmMap, userMap, projectMap := processData(allUsers, allProjects, allServers, fm)

	if err := printResults(vmMap, f, cfg.OutputFormat); err != nil {
		return errors.Wrap(err, "failed to print results")
	}

	_ = userMap
	_ = projectMap
	return nil
}

// parseFilters parses the filter string into a filter struct
func parseFilters(filterStr string) (filter, error) {
	f := filter{}
	if filterStr == "" {
		return f, nil
	}
	conditions := strings.Split(filterStr, ",")
	for _, cond := range conditions {
		cond = strings.TrimSpace(cond)
		if cond == "" {
			continue
		}
		parts := strings.SplitN(cond, "=", 2)
		if len(parts) != 2 {
			if strings.HasPrefix(cond, "days") {
				op := ""
				val := ""
				switch {
				case strings.Contains(cond, ">="):
					op, val = ">=", strings.TrimPrefix(cond, "days>=")
				case strings.Contains(cond, "<="):
					op, val = "<=", strings.TrimPrefix(cond, "days<=")
				case strings.Contains(cond, ">"):
					op, val = ">", strings.TrimPrefix(cond, "days>")
				case strings.Contains(cond, "<"):
					op, val = "<", strings.TrimPrefix(cond, "days<")
				case strings.Contains(cond, "="):
					op, val = "=", strings.TrimPrefix(cond, "days=")
				}
				if op != "" {
					days, err := strconv.Atoi(strings.TrimSpace(val))
					if err != nil || days < 0 {
						return f, fmt.Errorf("invalid days filter: %s", cond)
					}
					f.DaysOp = op
					f.DaysValue = days
					continue
				}
			}
			return f, fmt.Errorf("invalid filter condition: %s", cond)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch strings.ToLower(key) {
		case "host":
			f.Host = value
		case "email":
			f.Email = value
		case "status":
			f.Status = strings.ToUpper(value)
			validStatuses := []string{"ACTIVE", "SHUTOFF", "PAUSED", "SUSPENDED", "ERROR", "BUILD", "REBOOT"}
			if !contains(validStatuses, f.Status) {
				return f, fmt.Errorf("invalid status: %s; valid options: %v", f.Status, validStatuses)
			}
		case "project":
			f.Project = value
		default:
			return f, fmt.Errorf("unknown filter key: %s", key)
		}
	}
	return f, nil
}

// contains checks if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// matchesFilter checks if a VM matches the filter conditions
func matchesFilter(vm Vmdetails, f filter) bool {
	if f.Host != "" && !strings.EqualFold(vm.VmHost, f.Host) {
		return false
	}
	if f.Email != "" && !strings.EqualFold(vm.UserEmail, f.Email) {
		return false
	}
	if f.Status != "" && vm.VmStatus != f.Status {
		return false
	}
	if f.Project != "" && !strings.EqualFold(vm.Project, f.Project) {
		return false
	}
	if f.DaysOp != "" {
		switch f.DaysOp {
		case ">":
			if vm.NumberOfDays <= f.DaysValue {
				return false
			}
		case "<":
			if vm.NumberOfDays >= f.DaysValue {
				return false
			}
		case "=":
			if vm.NumberOfDays != f.DaysValue {
				return false
			}
		case ">=":
			if vm.NumberOfDays < f.DaysValue {
				return false
			}
		case "<=":
			if vm.NumberOfDays > f.DaysValue {
				return false
			}
		}
	}
	return true
}

// initializeClients sets up OpenStack clients
func initializeClients(ctx context.Context, cfg Config) (*Client, error) {
	ao, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		return nil, errors.Wrap(err, "failed to load auth options from environment")
	}
	log.Debugf("Auth options loaded: IdentityEndpoint=%s", ao.IdentityEndpoint)

	provider, err := openstack.AuthenticatedClient(ctx, ao)
	if err != nil {
		return nil, errors.Wrap(err, "authentication failed")
	}
	log.Debug("Authenticated successfully")

	identity, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Identity V3 client")
	}
	compute, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{Region: cfg.Region})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Compute V2 client")
	}
	log.Debug("Clients initialized")

	return &Client{Identity: identity, Compute: compute}, nil
}

// fetchUsers retrieves all users
func fetchUsers(ctx context.Context, client *Client) ([]users.User, error) {
	start := time.Now()
	userPages, err := users.List(client.Identity, users.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list users")
	}
	allUsers, err := users.ExtractUsers(userPages)
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract users")
	}
	log.Debugf("Fetched %d users in %v", len(allUsers), time.Since(start))
	return allUsers, nil
}

// fetchProjects retrieves all projects
func fetchProjects(ctx context.Context, client *Client) ([]projects.Project, error) {
	start := time.Now()
	enabled := true
	projectPages, err := projects.List(client.Identity, projects.ListOpts{Enabled: &enabled}).AllPages(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list projects")
	}
	allProjects, err := projects.ExtractProjects(projectPages)
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract projects")
	}
	log.Debugf("Fetched %d projects in %v", len(allProjects), time.Since(start))
	return allProjects, nil
}

// fetchFlavors retrieves all flavors
func fetchFlavors(ctx context.Context, client *Client) ([]flavors.Flavor, error) {
	start := time.Now()
	flavorPages, err := flavors.ListDetail(client.Compute, flavors.ListOpts{AccessType: flavors.AllAccess}).AllPages(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list flavors")
	}
	allFlavors, err := flavors.ExtractFlavors(flavorPages)
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract flavors")
	}
	log.Debugf("Fetched %d flavors in %v", len(allFlavors), time.Since(start))
	return allFlavors, nil
}

// fetchServers retrieves all servers with retries
func fetchServers(ctx context.Context, client *Client, cfg Config) ([]servers.Server, error) {
	start := time.Now()
	var allServers []servers.Server
	var mu sync.Mutex   // Protect allServers
	maxConcurrency := 5 // Adjust based on API limits

	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		allServers = nil
		pager := servers.List(client.Compute, servers.ListOpts{AllTenants: true})

		// Collect all pages sequentially
		var pages []pagination.Page
		err := pager.EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
			pages = append(pages, page)
			return true, nil
		})
		if err != nil {
			log.Warnf("Attempt %d/%d: failed to list server pages: %v", attempt, cfg.MaxRetries, err)
			if attempt == cfg.MaxRetries {
				return nil, errors.Wrap(err, "failed to list server pages after retries")
			}
			continue
		}

		// Process pages concurrently
		pagesChan := make(chan pagination.Page, len(pages))
		errorsChan := make(chan error, len(pages))
		var wg sync.WaitGroup

		// Feed pages to channel
		for _, page := range pages {
			pagesChan <- page
		}
		close(pagesChan)

		// Worker pool to extract servers
		for i := 0; i < maxConcurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for page := range pagesChan {
					servers, err := servers.ExtractServers(page)
					if err != nil {
						errorsChan <- errors.Wrap(err, "failed to extract servers")
						return
					}
					mu.Lock()
					allServers = append(allServers, servers...)
					mu.Unlock()
				}
			}()
		}

		// Wait for workers and close channels
		go func() {
			wg.Wait()
			close(errorsChan)
		}()

		// Check for errors
		for err := range errorsChan {
			if err != nil {
				log.Warnf("Attempt %d/%d: error processing servers: %v", attempt, cfg.MaxRetries, err)
				if attempt == cfg.MaxRetries {
					return nil, err
				}
				continue
			}
		}

		break // Success
	}

	log.Debugf("Fetched %d servers in %v", len(allServers), time.Since(start))
	return allServers, nil
}

// processFlavors processes flavor extra specs concurrently
func processFlavors(ctx context.Context, client *Client, allFlavors []flavors.Flavor) (*flavorMap, error) {
	start := time.Now()
	fm := &flavorMap{data: make(map[string]FlavorDetails)}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Increased from 5 to 10

	for _, flavor := range allFlavors {
		wg.Add(1)
		go func(f flavors.Flavor) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			extraSpecs, err := flavors.ListExtraSpecs(ctx, client.Compute, f.ID).Extract()
			if err != nil {
				log.Warnf("Failed to fetch extra specs for flavor %s: %v", f.ID, err)
				return
			}
			var procUnits float64
			if procUnitStr, ok := extraSpecs["powervm:proc_units"]; ok {
				procUnits, _ = strconv.ParseFloat(procUnitStr, 64)
			}
			fm.Lock()
			fm.data[f.ID] = FlavorDetails{
				Vcpus:     f.VCPUs,
				Memory:    f.RAM,
				ProcUnits: procUnits,
			}
			fm.Unlock()
		}(flavor)
	}
	wg.Wait()
	close(sem)
	log.Debugf("Processed %d flavors in %v", len(allFlavors), time.Since(start))
	return fm, nil
}

// processData processes fetched data into maps
func processData(allUsers []users.User, allProjects []projects.Project, allServers []servers.Server, fm *flavorMap) (map[string]Vmdetails, map[string]UserDetails, map[string]ProjectDetails) {
	vmMap := make(map[string]Vmdetails)
	userMap := make(map[string]UserDetails)
	projectMap := make(map[string]ProjectDetails)
	var mu sync.Mutex // Protect maps
	maxWorkers := 10  // Adjust based on CPU cores

	// Process users (sequential, typically small dataset)
	for _, user := range allUsers {
		var email string
		if e, ok := user.Extra["email"].(string); ok {
			email = e
		} else {
			log.Warnf("No email found for user %s", user.ID)
		}
		userMap[user.ID] = UserDetails{
			UserName:    user.Name,
			UserProject: "",
			UserEmail:   email,
		}
	}

	// Process projects (sequential, typically small dataset)
	for _, project := range allProjects {
		projectMap[project.ID] = ProjectDetails{
			ProjectName: project.Name,
		}
	}

	// Process servers concurrently
	serversChan := make(chan servers.Server, len(allServers))
	var wg sync.WaitGroup

	// Feed servers to channel
	for _, server := range allServers {
		serversChan <- server
	}
	close(serversChan)

	// Worker pool
	for range maxWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for server := range serversChan {
				diff := time.Now().Sub(server.Created) / (24 * time.Hour)
				flavorID, ok := server.Flavor["id"].(string)
				if !ok {
					log.Warnf("Flavor ID not found for server %s", server.ID)
					continue
				}
				host, ok := server.Metadata["original_host"]
				if !ok {
					host = "Unknown"
				}
				var ipAddresses []string
				for _, addresses := range server.Addresses {
					addrList, ok := addresses.([]interface{})
					if !ok {
						log.Warnf("Invalid address format for server %s", server.ID)
						continue
					}
					for _, addr := range addrList {
						addrMap, ok := addr.(map[string]interface{})
						if !ok {
							continue
						}
						ip, ok := addrMap["addr"].(string)
						if ok && ip != "" {
							ipAddresses = append(ipAddresses, ip)
						}
					}
				}
				ipStr := strings.Join(ipAddresses, ",")
				if ipStr == "" {
					ipStr = "None"
				}
				fm.RLock()
				flavor := fm.data[flavorID]
				fm.RUnlock()
				vmDetails := Vmdetails{
					VmName:       server.Name,
					Project:      projectMap[server.TenantID].ProjectName,
					CreationTime: server.Created,
					NumberOfDays: int(diff),
					UserID:       server.UserID,
					UserEmail:    userMap[server.UserID].UserEmail,
					VmStatus:     server.Status,
					VmMemory:     flavor.Memory,
					VmVcpu:       flavor.Vcpus,
					VmProcUnit:   flavor.ProcUnits,
					VmHost:       host,
					IPAddresses:  ipStr,
				}
				mu.Lock()
				vmMap[server.ID] = vmDetails
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	return vmMap, userMap, projectMap
}

// printResults outputs the results in the specified format
func printResults(vmMap map[string]Vmdetails, f filter, outputFormat string) error {
	start := time.Now()
	var filteredVMs []Vmdetails
	p := make(PairList, 0, len(vmMap))
	for k, v := range vmMap {
		if matchesFilter(v, f) {
			p = append(p, Pair{k, v.UserID})
			filteredVMs = append(filteredVMs, v)
		}
	}
	sort.Sort(p)
	log.Debugf("Sorted %d VMs in %v", len(p), time.Since(start))

	if len(filteredVMs) == 0 {
		fmt.Println("No VMs match the specified filters.")
		return nil
	}

	fmt.Println("##############")
	switch strings.ToLower(outputFormat) {
	case "json":
		data, err := json.MarshalIndent(filteredVMs, "", "  ")
		if err != nil {
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	case "table":
		writer := tabwriter.NewWriter(os.Stdout, 0, 1, 2, ' ', tabwriter.TabIndent)
		fmt.Fprintln(writer, "VM_NAME\tUSER_EMAIL\tUP_FOR_DAYS\tPROJECT\tSTATUS\tMEMORY\tVCPUs\tPROC_UNIT\tHOST\tIP_ADDRESSES\t")
		for _, pair := range p {
			vm := vmMap[pair.Key]
			fmt.Fprintf(writer, "%v\t%v\t%v\t%v\t%v\t%v\t%v\t%v\t%v\t%v\t\n",
				vm.VmName, vm.UserEmail, vm.NumberOfDays, vm.Project, vm.VmStatus,
				vm.VmMemory, vm.VmVcpu, vm.VmProcUnit, vm.VmHost, vm.IPAddresses)
		}
		writer.Flush()
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
	fmt.Println("##############")
	fmt.Printf("Number of VMs: %d\n", len(filteredVMs))
	fmt.Println("##############")
	return nil
}
