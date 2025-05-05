package vm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/roles"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/pkg/errors"
	"github.com/sudeeshjohn/openstack-tool/auth"
)

// Result holds the result of a VM operation
type Result struct {
	VMName  string `json:"vm_name"`
	VMID    string `json:"vm_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ActionFunc defines the signature for action handler functions
type ActionFunc func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error

// actionHandlers maps subcommands to their handler functions
var actionHandlers = map[string]ActionFunc{
	"delete": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering delete handler for VM: %s (ID: %s)", vmName, vm.ID)
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping delete for VM: %s", vmName)
			return nil
		}
		fmt.Printf("Type 'confirm' to delete VM '%s' (ID: %s): ", vmName, vm.ID)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		response := strings.TrimSpace(scanner.Text())
		log.Debugf("User response for delete confirmation: %s", response)
		if strings.ToLower(response) != "confirm" {
			log.Debugf("Delete aborted by user for VM: %s (ID: %s)", vmName, vm.ID)
			return fmt.Errorf("delete aborted by user for VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Initiating delete API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.Delete(ctx, client.Compute, vm.ID).ExtractErr()
		if err != nil {
			log.Debugf("Delete failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to delete VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Delete successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"force-delete": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering force-delete handler for VM: %s (ID: %s)", vmName, vm.ID)
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping force-delete for VM: %s", vmName)
			return nil
		}
		fmt.Printf("Type 'confirm' to force delete VM '%s' (ID: %s): ", vmName, vm.ID)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		response := strings.TrimSpace(scanner.Text())
		log.Debugf("User response for force-delete confirmation: %s", response)
		if strings.ToLower(response) != "confirm" {
			log.Debugf("Force-delete aborted by user for VM: %s (ID: %s)", vmName, vm.ID)
			return fmt.Errorf("force delete aborted by user for VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Initiating force-delete API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.ForceDelete(ctx, client.Compute, vm.ID).ExtractErr()
		if err != nil {
			log.Debugf("Force-delete failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to force delete VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Force-delete successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"start": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering start handler for VM: %s (ID: %s)", vmName, vm.ID)
		if strings.ToUpper(vm.Status) == "ACTIVE" {
			log.Debugf("VM %s (ID: %s) already active, skipping start", vmName, vm.ID)
			return fmt.Errorf("VM '%s' (ID: %s) is already active", vmName, vm.ID)
		}
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping start for VM: %s", vmName)
			return nil
		}
		log.Debugf("Initiating start API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.Start(ctx, client.Compute, vm.ID).ExtractErr()
		if err != nil {
			log.Debugf("Start failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to start VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Start successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"stop": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering stop handler for VM: %s (ID: %s)", vmName, vm.ID)
		if strings.ToUpper(vm.Status) == "SHUTOFF" {
			log.Debugf("VM %s (ID: %s) already stopped, skipping stop", vmName, vm.ID)
			return fmt.Errorf("VM '%s' (ID: %s) is already stopped", vmName, vm.ID)
		}
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping stop for VM: %s", vmName)
			return nil
		}
		log.Debugf("Initiating stop API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.Stop(ctx, client.Compute, vm.ID).ExtractErr()
		if err != nil {
			log.Debugf("Stop failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to stop VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Stop successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"pause": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering pause handler for VM: %s (ID: %s)", vmName, vm.ID)
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping pause for VM: %s", vmName)
			return nil
		}
		log.Debugf("Initiating pause API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.Pause(ctx, client.Compute, vm.ID).ExtractErr()
		if err != nil {
			log.Debugf("Pause failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to pause VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Pause successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"unpause": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering unpause handler for VM: %s (ID: %s)", vmName, vm.ID)
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping unpause for VM: %s", vmName)
			return nil
		}
		log.Debugf("Initiating unpause API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.Unpause(ctx, client.Compute, vm.ID).ExtractErr()
		if err != nil {
			log.Debugf("Unpause failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to unpause VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Unpause successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"suspend": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering suspend handler for VM: %s (ID: %s)", vmName, vm.ID)
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping suspend for VM: %s", vmName)
			return nil
		}
		log.Debugf("Initiating suspend API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.Suspend(ctx, client.Compute, vm.ID).ExtractErr()
		if err != nil {
			log.Debugf("Suspend failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to suspend VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Suspend successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"resume": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering resume handler for VM: %s (ID: %s)", vmName, vm.ID)
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping resume for VM: %s", vmName)
			return nil
		}
		log.Debugf("Initiating resume API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.Resume(ctx, client.Compute, vm.ID).ExtractErr()
		if err != nil {
			log.Debugf("Resume failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to resume VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Resume successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"reboot": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering reboot handler for VM: %s (ID: %s)", vmName, vm.ID)
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping reboot for VM: %s", vmName)
			return nil
		}
		log.Debugf("Initiating reboot API call for VM: %s (ID: %s)", vmName, vm.ID)
		err := servers.Reboot(ctx, client.Compute, vm.ID, servers.RebootOpts{Type: servers.SoftReboot}).ExtractErr()
		if err != nil {
			log.Debugf("Reboot failed for VM: %s (ID: %s), error: %v", vmName, vm.ID, err)
			return errors.Wrapf(err, "failed to reboot VM '%s' (ID: %s)", vmName, vm.ID)
		}
		log.Debugf("Reboot successful for VM: %s (ID: %s)", vmName, vm.ID)
		return nil
	},
	"set-state": func(ctx context.Context, client *auth.Client, cfg Config, vm *servers.Server, vmName string) error {
		log.Debugf("Entering set-state handler for VM: %s (ID: %s)", vmName, vm.ID)
		if cfg.DryRun {
			log.Debugf("Dry-run enabled, skipping set-state for VM: %s to %s", vmName, cfg.State)
			return nil
		}
		desiredState := strings.ToUpper(cfg.State)
		if desiredState != "ACTIVE" && desiredState != "ERROR" {
			return fmt.Errorf("invalid state '%s'; supported states are 'ACTIVE' or 'ERROR'", cfg.State)
		}

		currentState := strings.ToUpper(vm.Status)
		if currentState == desiredState {
			log.Debugf("VM %s (ID: %s) already in state %s, skipping set-state", vmName, vm.ID, desiredState)
			return fmt.Errorf("VM '%s' (ID: %s) is already in state %s", vmName, vm.ID, desiredState)
		}

		fmt.Printf("Type 'confirm' to set state of VM '%s' (ID: %s) to %s: ", vmName, vm.ID, desiredState)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		response := strings.TrimSpace(scanner.Text())
		log.Debugf("User response for set-state confirmation: %s", response)
		if strings.ToLower(response) != "confirm" {
			log.Debugf("Set-state aborted by user for VM: %s (ID: %s) to %s", vmName, vm.ID, desiredState)
			return fmt.Errorf("set-state aborted by user for VM '%s' (ID: %s) to %s", vmName, vm.ID, desiredState)
		}

		var err error
		switch desiredState {
		case "ACTIVE":
			if currentState == "SHUTOFF" {
				log.Debugf("Initiating start API call for VM: %s (ID: %s)", vmName, vm.ID)
				err = servers.Start(ctx, client.Compute, vm.ID).ExtractErr()
			} else if currentState == "PAUSED" {
				log.Debugf("Initiating unpause API call for VM: %s (ID: %s)", vmName, vm.ID)
				err = servers.Unpause(ctx, client.Compute, vm.ID).ExtractErr()
			} else if currentState == "SUSPENDED" {
				log.Debugf("Initiating resume API call for VM: %s (ID: %s)", vmName, vm.ID)
				err = servers.Resume(ctx, client.Compute, vm.ID).ExtractErr()
			}
		case "ERROR":
			log.Debugf("Initiating set to ERROR state for VM: %s (ID: %s)", vmName, vm.ID)
			err = fmt.Errorf("setting ERROR state is not directly supported; consider using a custom OpenStack extension")
		}

		if err != nil {
			log.Debugf("Set-state failed for VM: %s (ID: %s) to %s, error: %v", vmName, vm.ID, desiredState, err)
			return errors.Wrapf(err, "failed to set state of VM '%s' (ID: %s) to %s", vmName, vm.ID, desiredState)
		}
		log.Debugf("Set-state successful for VM: %s (ID: %s) to %s", vmName, vm.ID, desiredState)
		return nil
	},
}

func runManage(ctx context.Context, client *auth.Client, action string, cfg Config) error {
	if cfg.VM == "" {
		log.Debugf("Validation failed: VM flag is empty")
		return fmt.Errorf("vm flag is required")
	}
	if cfg.Project == "" {
		log.Debugf("Validation failed: Project flag is empty")
		return fmt.Errorf("project flag is required")
	}
	log.Debugf("Validated inputs: VM=%s, Project=%s", cfg.VM, cfg.Project)

	action = strings.ToLower(action)
	handler, ok := actionHandlers[action]
	if !ok {
		log.Debugf("Invalid action: %s, available actions: %v", action, listActions())
		return fmt.Errorf("invalid subcommand: %s; valid subcommands: %v", action, listActions())
	}
	log.Debugf("Selected action handler: %s", action)

	projectID, err := getProjectID(ctx, client, cfg.Project)
	if err != nil {
		log.Debugf("Failed to get project ID for %s: %v", cfg.Project, err)
		return errors.Wrap(err, "failed to get project ID")
	}
	log.Debugf("Resolved project %s to ID: %s", cfg.Project, projectID)

	log.Debugf("Fetching user roles for project: %s", cfg.Project)
	rolePages, err := roles.ListAssignments(client.Identity, roles.ListAssignmentsOpts{
		ScopeProjectID: projectID,
	}).AllPages(ctx)
	if err != nil {
		log.Debugf("Failed to list roles for project %s: %v", cfg.Project, err)
	} else {
		roleAssignments, err := roles.ExtractRoleAssignments(rolePages)
		if err != nil {
			log.Debugf("Failed to extract roles: %v", err)
		} else {
			log.Debugf("Roles for project %s:", cfg.Project)
			for _, role := range roleAssignments {
				log.Debugf("Role: %s, UserID: %s, GroupID: %s", role.Role.Name, role.User.ID, role.Group.ID)
			}
		}
	}

	vmNamesOrIDs := strings.Split(cfg.VM, ",")
	log.Debugf("Parsed VM list: %v", vmNamesOrIDs)
	var results []Result
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	var mu sync.Mutex
	totalCount := 0
	successCount := 0

	for _, vmNameOrID := range vmNamesOrIDs {
		vmNameOrID = strings.TrimSpace(vmNameOrID)
		if vmNameOrID == "" {
			log.Debugf("Skipping empty VM name/ID")
			continue
		}
		totalCount++
		log.Debugf("Processing VM %d/%d: %s", totalCount, len(vmNamesOrIDs), vmNameOrID)

		isID := uuidRegex.MatchString(vmNameOrID)
		log.Debugf("VM: %s identified as %s", vmNameOrID, map[bool]string{true: "ID", false: "Name"}[isID])

		wg.Add(1)
		go func(vmNameOrID string, isID bool) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			log.Debugf("Acquired semaphore for VM: %s", vmNameOrID)

			if isID {
				log.Debugf("Validating VM ID: %s", vmNameOrID)
				if len(vmNameOrID) != 36 {
					mu.Lock()
					results = append(results, Result{
						VMName:  vmNameOrID,
						VMID:    "",
						Status:  "error",
						Message: fmt.Sprintf("Invalid VM ID format: %s", vmNameOrID),
					})
					mu.Unlock()
					log.Debugf("Invalid VM ID format for: %s", vmNameOrID)
					return
				}
			} else {
				log.Debugf("Validating VM Name: %s", vmNameOrID)
				if strings.Contains(vmNameOrID, "-") && uuidRegex.MatchString(vmNameOrID) {
					log.Warnf("VM name %s resembles an ID; proceeding as name but verify input", vmNameOrID)
				}
			}

			log.Debugf("Initiating findVM for: %s in project %s", vmNameOrID, cfg.Project)
			vm, err := findVM(ctx, client, vmNameOrID, projectID, isID)
			if err != nil {
				mu.Lock()
				results = append(results, Result{
					VMName:  vmNameOrID,
					VMID:    "",
					Status:  "error",
					Message: fmt.Errorf("failed to find VM: %v", err).Error(),
				})
				mu.Unlock()
				log.Errorf("Error finding VM %s: %v", vmNameOrID, err)
				return
			}

			err = handler(ctx, client, cfg, vm, vmNameOrID)
			if err != nil {
				mu.Lock()
				results = append(results, Result{
					VMName:  vmNameOrID,
					VMID:    vm.ID,
					Status:  "error",
					Message: err.Error(),
				})
				mu.Unlock()
				log.Errorf("Error executing action %s on VM %s: %v", action, vmNameOrID, err)
				return
			}

			mu.Lock()
			results = append(results, Result{
				VMName:  vmNameOrID,
				VMID:    vm.ID,
				Status:  "success",
				Message: fmt.Sprintf("Action %s completed", action),
			})
			successCount++
			mu.Unlock()
			log.Debugf("Action %s successful for VM: %s (ID: %s)", action, vmNameOrID, vm.ID)
		}(vmNameOrID, isID)
	}
	wg.Wait()

	if cfg.OutputFormat == "json" {
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		fmt.Printf("Total VMs processed: %d, Successful: %d\n", totalCount, successCount)
		for _, result := range results {
			fmt.Printf("VM: %s (ID: %s) - Status: %s, Message: %s\n", result.VMName, result.VMID, result.Status, result.Message)
		}
	}

	return nil
}

func listActions() []string {
	actions := make([]string, 0, len(actionHandlers))
	for k := range actionHandlers {
		actions = append(actions, k)
	}
	return actions
}

func findVM(ctx context.Context, client *auth.Client, vmNameOrID, projectID string, isID bool) (*servers.Server, error) {
	if isID {
		// Use servers.Get for ID-based lookup
		server, err := servers.Get(ctx, client.Compute, vmNameOrID).Extract()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get server with ID %s", vmNameOrID)
		}
		// Verify the server belongs to the specified project
		if server.TenantID != projectID {
			return nil, fmt.Errorf("server with ID %s does not belong to project %s", vmNameOrID, projectID)
		}
		return server, nil
	}

	// Use servers.List for name-based lookup
	listOpts := servers.ListOpts{
		Name:     vmNameOrID,
		TenantID: projectID,
	}

	var server *servers.Server
	err := servers.List(client.Compute, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		serverList, err := servers.ExtractServers(page)
		if err != nil {
			return false, err
		}
		for _, s := range serverList {
			if s.Name == vmNameOrID {
				server = &s
				return false, nil // Stop paging once we find a match
			}
		}
		return true, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list servers")
	}
	if server == nil {
		return nil, fmt.Errorf("VM %s not found in project %s", vmNameOrID, projectID)
	}
	return server, nil
}

func getProjectID(ctx context.Context, client *auth.Client, projectName string) (string, error) {
	listOpts := projects.ListOpts{
		Name: projectName,
	}
	var projectID string
	err := projects.List(client.Identity, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		projectList, err := projects.ExtractProjects(page)
		if err != nil {
			return false, err
		}
		for _, p := range projectList {
			if p.Name == projectName {
				projectID = p.ID
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to list projects")
	}
	if projectID == "" {
		return "", fmt.Errorf("project %s not found", projectName)
	}
	return projectID, nil
}

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
