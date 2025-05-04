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
	Verbose        bool
	FilterStr      string
	OutputFormat   string
	Timeout        time.Duration
	MaxRetries     int
	MaxConcurrency int
	UseFlavorCache bool
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
func Run(verbose bool, filterStr, outputFormat string, useFlavorCache bool) error {
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if verbose {
		log.SetLevel(logrus.DebugLevel)
	}

	cfg := Config{
		Verbose:        verbose,
		FilterStr:      filterStr,
		OutputFormat:   outputFormat,
		Timeout:        120 * time.Second,
		MaxRetries:     3,
		MaxConcurrency: 10,
		UseFlavorCache: useFlavorCache,
	}

	// Determine region
	region := os.Getenv("OS_REGION_NAME")
	if region == "" {
		region = "RegionOne"
		log.Debug("OS_REGION_NAME not set, defaulting to RegionOne")
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

	client, err := initializeClients(ctx, cfg, region)
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

	// Map project names to IDs for API-level filtering
	projectIDMap := make(map[string]string)
	for _, p := range allProjects {
		projectIDMap[p.Name] = p.ID
	}
	if f.Project != "" {
		if id, ok := projectIDMap[f.Project]; ok {
			f.Project = id // Replace project name with ID
		} else {
			return fmt.Errorf("project %s not found", f.Project)
		}
	}

	fm, err := processFlavors(ctx, client, allFlavors, cfg.UseFlavorCache)
	if err != nil {
		return errors.Wrap(err, "failed to process flavors")
	}

	err = streamAndPrintServers(ctx, client, cfg, f, allUsers, allProjects, fm, outputFormat)
	if err != nil {
		return errors.Wrap(err, "failed to stream servers")
	}

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
func initializeClients(ctx context.Context, cfg Config, region string) (*Client, error) {
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
	compute, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{Region: region})
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

// fetchServers retrieves servers with API-level filtering
func fetchServers(ctx context.Context, client *Client, cfg Config, f filter) ([]servers.Server, error) {
	start := time.Now()
	var allServers []servers.Server

	listOpts := servers.ListOpts{AllTenants: true}
	if f.Status != "" {
		listOpts.Status = f.Status
	}
	if f.Project != "" {
		listOpts.TenantID = f.Project
	}

	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		allServers = nil
		pager := servers.List(client.Compute, listOpts)
		err := pager.EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
			servers, err := servers.ExtractServers(page)
			if err != nil {
				return false, errors.Wrap(err, "failed to extract servers")
			}
			allServers = append(allServers, servers...)
			return true, nil
		})
		if err != nil {
			log.Warnf("Attempt %d/%d: error fetching servers: %v", attempt, cfg.MaxRetries, err)
			if attempt == cfg.MaxRetries {
				return nil, errors.Wrap(err, "failed to fetch servers after retries")
			}
			continue
		}
		break // Success
	}

	log.Debugf("Fetched %d servers in %v", len(allServers), time.Since(start))
	return allServers, nil
}

// loadFlavorCache loads flavor details from a cache file
func loadFlavorCache(cacheFile string) (map[string]FlavorDetails, error) {
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}
	var cache map[string]FlavorDetails
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return cache, nil
}

// saveFlavorCache saves flavor details to a cache file
func saveFlavorCache(cacheFile string, data map[string]FlavorDetails) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(cacheFile, bytes, 0644)
}

// processFlavors processes flavor extra specs with optional caching
func processFlavors(ctx context.Context, client *Client, allFlavors []flavors.Flavor, useFlavorCache bool) (*flavorMap, error) {
	start := time.Now()
	fm := &flavorMap{data: make(map[string]FlavorDetails)}
	cacheFile := "flavor_cache.json"

	if useFlavorCache {
		if cached, err := loadFlavorCache(cacheFile); err == nil {
			fm.data = cached
			log.Debugf("Loaded %d flavors from cache", len(cached))
			return fm, nil
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
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
				var err error
				procUnits, err = strconv.ParseFloat(procUnitStr, 64)
				if err != nil {
					log.Warnf("Invalid proc_units for flavor %s: %v", f.ID, err)
				}
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

	if useFlavorCache {
		if err := saveFlavorCache(cacheFile, fm.data); err != nil {
			log.Warnf("Failed to save flavor cache: %v", err)
		}
	}

	log.Debugf("Processed %d flavors in %v", len(allFlavors), time.Since(start))
	return fm, nil
}

// processData processes fetched data into maps
func processData(allUsers []users.User, allProjects []projects.Project, allServers []servers.Server, fm *flavorMap) (map[string]Vmdetails, map[string]UserDetails, map[string]ProjectDetails) {
	vmMap := make(map[string]Vmdetails)
	userMap := make(map[string]UserDetails)
	projectMap := make(map[string]ProjectDetails)

	// Process users sequentially
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

	// Process projects sequentially
	for _, project := range allProjects {
		projectMap[project.ID] = ProjectDetails{
			ProjectName: project.Name,
		}
	}

	// Process servers concurrently
	maxWorkers := 10
	serversChan := make(chan servers.Server, len(allServers))
	var serverWg sync.WaitGroup
	var mu sync.Mutex
	for _, server := range allServers {
		serversChan <- server
	}
	close(serversChan)
	for i := 0; i < maxWorkers; i++ {
		serverWg.Add(1)
		go func() {
			defer serverWg.Done()
			for server := range serversChan {
				vm := processServer(server, fm, userMap, projectMap)
				mu.Lock()
				vmMap[server.ID] = vm
				mu.Unlock()
			}
		}()
	}
	serverWg.Wait()

	return vmMap, userMap, projectMap
}

// processServer processes a single server into Vmdetails
func processServer(server servers.Server, fm *flavorMap, userMap map[string]UserDetails, projectMap map[string]ProjectDetails) Vmdetails {
	diff := time.Now().Sub(server.Created) / (24 * time.Hour)
	flavorID, ok := server.Flavor["id"].(string)
	if !ok {
		log.Warnf("Flavor ID not found for server %s", server.ID)
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
	return Vmdetails{
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
}

// streamAndPrintServers processes servers and prints results
func streamAndPrintServers(ctx context.Context, client *Client, cfg Config, f filter, allUsers []users.User, allProjects []projects.Project, fm *flavorMap, outputFormat string) error {
	start := time.Now()
	userMap := make(map[string]UserDetails)
	projectMap := make(map[string]ProjectDetails)
	for _, user := range allUsers {
		var email string
		if e, ok := user.Extra["email"].(string); ok {
			email = e
		}
		userMap[user.ID] = UserDetails{UserName: user.Name, UserEmail: email}
	}
	for _, project := range allProjects {
		projectMap[project.ID] = ProjectDetails{ProjectName: project.Name}
	}

	allServers, err := fetchServers(ctx, client, cfg, f)
	if err != nil {
		return errors.Wrap(err, "failed to fetch servers")
	}

	var vmCount int
	writer := tabwriter.NewWriter(os.Stdout, 0, 1, 2, ' ', tabwriter.TabIndent)
	var jsonVMs []Vmdetails
	var pairs PairList

	fmt.Println("##############")
	if strings.ToLower(outputFormat) == "table" {
		fmt.Fprintln(writer, "VM_NAME\tUSER_EMAIL\tUP_FOR_DAYS\tPROJECT\tSTATUS\tMEMORY\tVCPUs\tPROC_UNIT\tHOST\tIP_ADDRESSES\t")
	}

	for i, server := range allServers {
		vm := processServer(server, fm, userMap, projectMap)
		if matchesFilter(vm, f) {
			vmCount++
			if strings.ToLower(outputFormat) == "table" {
				fmt.Fprintf(writer, "%v\t%v\t%v\t%v\t%v\t%v\t%v\t%v\t%v\t%v\t\n",
					vm.VmName, vm.UserEmail, vm.NumberOfDays, vm.Project, vm.VmStatus,
					vm.VmMemory, vm.VmVcpu, vm.VmProcUnit, vm.VmHost, vm.IPAddresses)
			} else {
				jsonVMs = append(jsonVMs, vm)
				pairs = append(pairs, Pair{Key: strconv.Itoa(i), Value: vm.UserID})
			}
		}
	}

	if strings.ToLower(outputFormat) == "table" {
		writer.Flush()
	} else if len(jsonVMs) > 0 {
		sort.Sort(pairs)
		sortedVMs := make([]Vmdetails, len(jsonVMs))
		for i, pair := range pairs {
			index, err := strconv.Atoi(pair.Key)
			if err != nil {
				log.Warnf("Failed to parse index %s: %v", pair.Key, err)
				continue
			}
			sortedVMs[i] = jsonVMs[index]
		}
		data, err := json.MarshalIndent(sortedVMs, "", "  ")
		if err != nil {
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		fmt.Println("No VMs match the specified filters.")
	}

	fmt.Println("##############")
	fmt.Printf("Number of VMs: %d\n", vmCount)
	fmt.Println("##############")
	log.Debugf("Processed and printed %d VMs in %v", vmCount, time.Since(start))
	return nil
}
