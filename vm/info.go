package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/users"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/sudeeshjohn/openstack-tool/auth"
)

// Vmdetails holds the details of a VM for output
type Vmdetails struct {
	Name            string
	FlavorID        string
	Hypervisor      string
	Email           string
	ProjectName     string
	Created         time.Time
	Age             string
	FixedIP         string
	Status          string
	FlavorVCPUs     int
	FlavorMemory    int
	FlavorProcUnits float64
}

// Run executes the VM info or manage logic based on the action
func Run(ctx context.Context, client *auth.Client, action string, cfg Config) error {
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if cfg.Verbose {
		log.SetLevel(logrus.DebugLevel)
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	switch action {
	case "info":
		return runInfo(ctx, client, cfg)
	default:
		return runManage(ctx, client, action, cfg)
	}
}

func runInfo(ctx context.Context, client *auth.Client, cfg Config) error {
	log.Debugf("Starting VM info with config: %+v", cfg)

	// Initialize flavor cache
	fm := &flavorMap{data: make(map[string]FlavorDetails)}
	if cfg.UseFlavorCache {
		var err error
		fm, err = loadFlavorCache("flavor_cache.json", 24*time.Hour)
		if err != nil {
			log.Warnf("Failed to load flavor cache: %v", err)
			fm = &flavorMap{data: make(map[string]FlavorDetails)}
		}
	}

	// Fetch users, projects, and flavors
	users, err := fetchAllUsers(ctx, client)
	if err != nil {
		return errors.Wrap(err, "failed to fetch users")
	}
	projects, err := fetchAllProjects(ctx, client)
	if err != nil {
		return errors.Wrap(err, "failed to fetch projects")
	}
	allFlavors, err := fetchFlavors(ctx, client)
	if err != nil {
		return errors.Wrap(err, "failed to fetch flavors")
	}
	fm, err = processFlavors(ctx, client, allFlavors, cfg.UseFlavorCache)
	if err != nil {
		return errors.Wrap(err, "failed to process flavors")
	}

	// Parse filter
	f, err := parseFilter(cfg.FilterStr)
	if err != nil {
		return errors.Wrap(err, "failed to parse filter")
	}

	// List VMs
	var results []Vmdetails
	var totalVMs uint32
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.MaxConcurrency)
	var mu sync.Mutex

	err = servers.List(client.Compute, servers.ListOpts{AllTenants: true}).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		serverList, err := servers.ExtractServers(page)
		if err != nil {
			return false, errors.Wrap(err, "failed to extract servers")
		}

		atomic.AddUint32(&totalVMs, uint32(len(serverList)))

		for _, server := range serverList {
			wg.Add(1)
			go func(s servers.Server) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				for i := 0; i < cfg.MaxRetries; i++ {
					pairs, err := processServer(ctx, s, users, projects, fm, f)
					if err != nil {
						log.Warnf("Error processing server %s: %v, attempt %d/%d", s.ID, err, i+1, cfg.MaxRetries)
						time.Sleep(time.Second * time.Duration(i+1))
						continue
					}
					if pairs != nil {
						vm := Vmdetails{
							Name:            s.Name,
							FlavorID:        s.Flavor["id"].(string),
							Hypervisor:      s.Host,
							Email:           pairs[6].Value,
							ProjectName:     pairs[7].Value,
							Created:         s.Created,
							Age:             pairs[9].Value,
							FixedIP:         pairs[10].Value,
							Status:          s.Status,
							FlavorVCPUs:     atoi(pairs[2].Value),
							FlavorMemory:    atoi(pairs[3].Value),
							FlavorProcUnits: atof(pairs[4].Value),
						}
						mu.Lock()
						results = append(results, vm)
						mu.Unlock()
					}
					break
				}
			}(server)
		}
		return true, nil
	})
	if err != nil {
		return errors.Wrap(err, "failed to list servers")
	}
	wg.Wait()

	if cfg.OutputFormat == "json" {
		output := struct {
			VMs      []Vmdetails `json:"vms"`
			TotalVMs uint32      `json:"total_vms"`
		}{
			VMs:      results,
			TotalVMs: atomic.LoadUint32(&totalVMs),
		}
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Name\tFlavor VCPUs\tFlavor Memory\tFlavor ProcUnits\tHypervisor\tEmail\tProject\tCreated\tAge\tFixed IP\tStatus")
		for _, vm := range results {
			fmt.Fprintf(w, "%s\t%d\t%d\t%.2f\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				vm.Name, vm.FlavorVCPUs, vm.FlavorMemory, vm.FlavorProcUnits,
				vm.Hypervisor, vm.Email, vm.ProjectName, vm.Created.Format(time.RFC3339),
				vm.Age, vm.FixedIP, vm.Status)
		}
		w.Flush()
		fmt.Printf("\nTotal VMs: %d\n", atomic.LoadUint32(&totalVMs))
	}

	return nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atof(s string) float64 {
	n, _ := strconv.ParseFloat(s, 64)
	return n
}

func parseFilter(filterStr string) (*filter, error) {
	f := &filter{}
	if filterStr == "" {
		return f, nil
	}
	pairs := strings.Split(filterStr, ",")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid filter format: %s", pair)
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		switch key {
		case "host":
			f.Host = value
		case "email":
			f.Email = value
		case "status":
			f.Status = value
		case "project":
			f.Project = value
		case "days":
			if strings.HasPrefix(value, ">") {
				f.DaysOp = ">"
				value = strings.TrimPrefix(value, ">")
			} else if strings.HasPrefix(value, "<") {
				f.DaysOp = "<"
				value = strings.TrimPrefix(value, "<")
			} else {
				return nil, fmt.Errorf("invalid days filter operator: %s", value)
			}
			days, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid days value: %s", value)
			}
			f.DaysValue = days
		default:
			return nil, fmt.Errorf("unknown filter key: %s", key)
		}
	}
	return f, nil
}

func matchesFilter(vm Vmdetails, f *filter) bool {
	if f.Host != "" && !strings.EqualFold(vm.Hypervisor, f.Host) {
		return false
	}
	if f.Email != "" && !strings.Contains(strings.ToLower(vm.Email), strings.ToLower(f.Email)) {
		return false
	}
	if f.Status != "" && !strings.EqualFold(vm.Status, f.Status) {
		return false
	}
	if f.Project != "" && !strings.EqualFold(vm.ProjectName, f.Project) {
		return false
	}
	if f.DaysOp != "" {
		daysSince := int(time.Since(vm.Created).Hours() / 24)
		if f.DaysOp == ">" && daysSince <= f.DaysValue {
			return false
		}
		if f.DaysOp == "<" && daysSince >= f.DaysValue {
			return false
		}
	}
	return true
}

func fetchAllUsers(ctx context.Context, client *auth.Client) ([]users.User, error) {
	listOpts := users.ListOpts{}
	var allUsers []users.User
	err := users.List(client.Identity, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		usersList, err := users.ExtractUsers(page)
		if err != nil {
			return false, err
		}
		allUsers = append(allUsers, usersList...)
		return true, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch users")
	}
	return allUsers, nil
}

func fetchAllProjects(ctx context.Context, client *auth.Client) ([]projects.Project, error) {
	listOpts := projects.ListOpts{}
	var allProjects []projects.Project
	err := projects.List(client.Identity, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		projectsList, err := projects.ExtractProjects(page)
		if err != nil {
			return false, err
		}
		allProjects = append(allProjects, projectsList...)
		return true, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch projects")
	}
	return allProjects, nil
}

func fetchFlavors(ctx context.Context, client *auth.Client) ([]flavors.Flavor, error) {
	listOpts := flavors.ListOpts{}
	var allFlavors []flavors.Flavor
	err := flavors.ListDetail(client.Compute, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		flavorsList, err := flavors.ExtractFlavors(page)
		if err != nil {
			return false, err
		}
		allFlavors = append(allFlavors, flavorsList...)
		return true, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch flavors")
	}
	return allFlavors, nil
}

func loadFlavorCache(cacheFile string, maxAge time.Duration) (*flavorMap, error) {
	fm := &flavorMap{data: make(map[string]FlavorDetails)}
	info, err := os.Stat(cacheFile)
	if os.IsNotExist(err) {
		return fm, fmt.Errorf("cache file does not exist")
	}
	if err != nil {
		return fm, err
	}
	if time.Since(info.ModTime()) > maxAge {
		return fm, fmt.Errorf("cache expired")
	}
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return fm, err
	}
	if err := json.Unmarshal(data, &fm.data); err != nil {
		return fm, err
	}
	log.Debugf("Loaded %d flavors from cache", len(fm.data))
	return fm, nil
}

func saveFlavorCache(cacheFile string, data map[string]FlavorDetails) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(cacheFile, bytes, 0644)
}

func processFlavors(ctx context.Context, client *auth.Client, allFlavors []flavors.Flavor, useFlavorCache bool) (*flavorMap, error) {
	start := time.Now()
	fm := &flavorMap{data: make(map[string]FlavorDetails)}
	cacheFile := "flavor_cache.json"
	cacheMaxAge := 24 * time.Hour

	if useFlavorCache {
		if cached, err := loadFlavorCache(cacheFile, cacheMaxAge); err == nil {
			fm = cached
			log.Debugf("Loaded %d flavors from cache", len(cached.data))
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

	if useFlavorCache {
		if err := saveFlavorCache(cacheFile, fm.data); err != nil {
			log.Warnf("Failed to save flavor cache: %v", err)
		} else {
			log.Debugf("Saved %d flavors to cache", len(fm.data))
		}
	}

	log.Debugf("Processed %d flavors in %v", len(allFlavors), time.Since(start))
	return fm, nil
}

func extractEmailFromDescription(desc string) string {
	if desc == "" {
		return ""
	}
	re := regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	if email := re.FindString(desc); email != "" {
		return email
	}
	return ""
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(d.Hours())
	if hours >= 1 {
		return fmt.Sprintf("%dh", hours)
	}
	minutes := int(d.Minutes())
	return fmt.Sprintf("%dm", minutes)
}

func processData(server servers.Server, users []users.User, projects []projects.Project, flavors *flavorMap) (Vmdetails, UserDetails, ProjectDetails, error) {
	var vm Vmdetails
	var user UserDetails
	var project ProjectDetails

	vm.Name = server.Name
	vm.FlavorID = server.Flavor["id"].(string)
	vm.Hypervisor = server.Host
	vm.Created = server.Created
	vm.Age = formatDuration(time.Now().Sub(server.Created))
	vm.Status = server.Status

	flavors.Lock()
	flavor, ok := flavors.data[vm.FlavorID]
	flavors.Unlock()
	if ok {
		vm.FlavorVCPUs = flavor.Vcpus
		vm.FlavorMemory = flavor.Memory
		vm.FlavorProcUnits = flavor.ProcUnits
	} else {
		log.Warnf("Flavor %s not found for server %s", vm.FlavorID, server.ID)
	}

	for _, network := range server.Addresses {
		for _, addr := range network.([]interface{}) {
			addrMap := addr.(map[string]interface{})
			if addrMap["OS-EXT-IPS:type"] == "fixed" {
				vm.FixedIP = addrMap["addr"].(string)
			}
		}
	}

	for _, u := range users {
		if u.ID == server.UserID {
			user = UserDetails{
				ID:   u.ID,
				Name: u.Name,
			}
			if email, ok := u.Extra["email"].(string); ok && email != "" {
				user.Email = email
			} else {
				user.Email = extractEmailFromDescription(u.Description)
				if user.Email == "" {
					log.Warnf("No email found for user %s (ID: %s); Extra: %v, Description: %q; using empty string",
						u.Name, u.ID, u.Extra, u.Description)
				}
			}
			vm.Email = user.Email
			break
		}
	}

	for _, p := range projects {
		if p.ID == server.TenantID {
			project = ProjectDetails{
				ID:   p.ID,
				Name: p.Name,
			}
			vm.ProjectName = p.Name
			break
		}
	}

	return vm, user, project, nil
}

func processServer(ctx context.Context, server servers.Server, users []users.User, projects []projects.Project, flavors *flavorMap, f *filter) ([]Pair, error) {
	vm, user, project, err := processData(server, users, projects, flavors)
	if err != nil {
		return nil, err
	}

	if !matchesFilter(vm, f) {
		return nil, nil
	}

	pairs := []Pair{
		{Key: "VM Name", Value: vm.Name},
		{Key: "Flavor ID", Value: vm.FlavorID},
		{Key: "Flavor VCPUs", Value: strconv.Itoa(vm.FlavorVCPUs)},
		{Key: "Flavor Memory", Value: strconv.Itoa(vm.FlavorMemory)},
		{Key: "Flavor ProcUnits", Value: fmt.Sprintf("%.2f", vm.FlavorProcUnits)},
		{Key: "Hypervisor", Value: vm.Hypervisor},
		{Key: "User Email", Value: user.Email},
		{Key: "Project Name", Value: project.Name},
		{Key: "Created", Value: vm.Created.Format(time.RFC3339)},
		{Key: "Age", Value: vm.Age},
		{Key: "Fixed IP", Value: vm.FixedIP},
		{Key: "Status", Value: vm.Status},
	}
	return pairs, nil
}
