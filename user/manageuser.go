package user

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/roles"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/users"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Config holds configuration parameters
type Config struct {
	Verbose      bool
	OutputFormat string
	Timeout      time.Duration
	Action       string // list, assign, remove, list-roles, list-users-by-role, list-user-roles-all-projects, list-users-in-project
	UserName     string
	ProjectName  string
	RoleName     string
}

// RoleDetails defines the role details structure
type RoleDetails struct {
	RoleName    string
	RoleID      string
	ProjectName string
}

// UserDetails defines the user details structure
type UserDetails struct {
	UserName string
	UserID   string
	Email    string
}

// Client holds OpenStack clients
type Client struct {
	Identity *gophercloud.ServiceClient
}

// Logger for structured logging
var log = logrus.New()

// Run executes the user role management logic
func Run(verbose bool, outputFormat, action, userName, projectName, roleName string) error {
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if verbose {
		log.SetLevel(logrus.DebugLevel)
	}

	cfg := Config{
		Verbose:      verbose,
		OutputFormat: outputFormat,
		Timeout:      120 * time.Second,
		Action:       strings.ToLower(action),
		UserName:     userName,
		ProjectName:  projectName,
		RoleName:     roleName,
	}

	// Validate action
	validActions := []string{"list", "assign", "remove", "list-roles", "list-users-by-role", "list-user-roles-all-projects", "list-users-in-project"}
	if !contains(validActions, cfg.Action) {
		return fmt.Errorf("invalid action: %s; valid options: %v", cfg.Action, validActions)
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

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	client, err := initializeClients(ctx, region)
	if err != nil {
		return errors.Wrap(err, "failed to initialize clients")
	}

	switch cfg.Action {
	case "list":
		if cfg.UserName == "" || cfg.ProjectName == "" {
			return fmt.Errorf("user-name and project-name are required for list action")
		}
		return listUserRoles(ctx, client, cfg)
	case "assign":
		if cfg.UserName == "" || cfg.ProjectName == "" || cfg.RoleName == "" {
			return fmt.Errorf("user-name, project-name, and role-name are required for assign action")
		}
		return assignUserRole(ctx, client, cfg)
	case "remove":
		if cfg.UserName == "" || cfg.ProjectName == "" || cfg.RoleName == "" {
			return fmt.Errorf("user-name, project-name, and role-name are required for remove action")
		}
		return removeUserRole(ctx, client, cfg)
	case "list-roles":
		return listAllRoles(ctx, client, cfg)
	case "list-users-by-role":
		if cfg.ProjectName == "" || cfg.RoleName == "" {
			return fmt.Errorf("project-name and role-name are required for list-users-by-role action")
		}
		return listUsersByRole(ctx, client, cfg)
	case "list-user-roles-all-projects":
		if cfg.UserName == "" {
			return fmt.Errorf("user-name is required for list-user-roles-all-projects action")
		}
		return listUserRolesAllProjects(ctx, client, cfg)
	case "list-users-in-project":
		if cfg.ProjectName == "" {
			return fmt.Errorf("project-name is required for list-users-in-project action")
		}
		return listUsersInProject(ctx, client, cfg)
	}

	return nil
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

// initializeClients sets up OpenStack clients
func initializeClients(ctx context.Context, region string) (*Client, error) {
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
	log.Debug("Identity client initialized")

	return &Client{Identity: identity}, nil
}

// getUserID retrieves the user ID by username
func getUserID(ctx context.Context, client *Client, username string) (string, error) {
	listOpts := users.ListOpts{Name: username}
	userPages, err := users.List(client.Identity, listOpts).AllPages(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list users")
	}
	userList, err := users.ExtractUsers(userPages)
	if err != nil {
		return "", errors.Wrap(err, "failed to extract users")
	}
	if len(userList) == 0 {
		return "", fmt.Errorf("user %s not found", username)
	}
	if len(userList) > 1 {
		return "", fmt.Errorf("multiple users found with name %s", username)
	}
	return userList[0].ID, nil
}

// getProjectID retrieves the project ID by project name
func getProjectID(ctx context.Context, client *Client, projectName string) (string, error) {
	listOpts := projects.ListOpts{Name: projectName}
	projectPages, err := projects.List(client.Identity, listOpts).AllPages(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list projects")
	}
	projectList, err := projects.ExtractProjects(projectPages)
	if err != nil {
		return "", errors.Wrap(err, "failed to extract projects")
	}
	if len(projectList) == 0 {
		return "", fmt.Errorf("project %s not found", projectName)
	}
	if len(projectList) > 1 {
		return "", fmt.Errorf("multiple projects found with name %s", projectName)
	}
	return projectList[0].ID, nil
}

// getProjectName retrieves the project name by project ID
func getProjectName(ctx context.Context, client *Client, projectID string) (string, error) {
	project, err := projects.Get(ctx, client.Identity, projectID).Extract()
	if err != nil {
		return "", errors.Wrap(err, "failed to get project")
	}
	return project.Name, nil
}

// getRoleID retrieves the role ID by role name
func getRoleID(ctx context.Context, client *Client, roleName string) (string, error) {
	listOpts := roles.ListOpts{Name: roleName}
	rolePages, err := roles.List(client.Identity, listOpts).AllPages(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list roles")
	}
	roleList, err := roles.ExtractRoles(rolePages)
	if err != nil {
		return "", errors.Wrap(err, "failed to extract roles")
	}
	if len(roleList) == 0 {
		return "", fmt.Errorf("role %s not found", roleName)
	}
	if len(roleList) > 1 {
		return "", fmt.Errorf("multiple roles found with name %s", roleName)
	}
	return roleList[0].ID, nil
}

// listUserRoles lists roles for a user in a project
func listUserRoles(ctx context.Context, client *Client, cfg Config) error {
	if cfg.UserName == "" || cfg.ProjectName == "" {
		return fmt.Errorf("user-name and project-name are required for list action")
	}

	userID, err := getUserID(ctx, client, cfg.UserName)
	if err != nil {
		return errors.Wrap(err, "failed to get user ID")
	}
	projectID, err := getProjectID(ctx, client, cfg.ProjectName)
	if err != nil {
		return errors.Wrap(err, "failed to get project ID")
	}

	var roleDetails []RoleDetails
	var mu sync.Mutex
	start := time.Now()

	roleAssignments, err := roles.ListAssignments(client.Identity, roles.ListAssignmentsOpts{
		UserID:         userID,
		ScopeProjectID: projectID,
	}).AllPages(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list role assignments")
	}

	roleList, err := roles.ExtractRoleAssignments(roleAssignments)
	if err != nil {
		return errors.Wrap(err, "failed to extract role assignments")
	}

	for _, assignment := range roleList {
		if assignment.Role.ID != "" {
			role, err := roles.Get(ctx, client.Identity, assignment.Role.ID).Extract()
			if err != nil {
				log.Warnf("Failed to get role %s: %v", assignment.Role.ID, err)
				continue
			}
			mu.Lock()
			roleDetails = append(roleDetails, RoleDetails{
				RoleName:    role.Name,
				RoleID:      role.ID,
				ProjectName: cfg.ProjectName,
			})
			mu.Unlock()
		}
	}

	log.Debugf("Fetched %d roles in %v", len(roleDetails), time.Since(start))
	return printRoles(roleDetails, cfg.OutputFormat)
}

// listAllRoles lists all available roles in OpenStack
func listAllRoles(ctx context.Context, client *Client, cfg Config) error {
	var roleDetails []RoleDetails
	var mu sync.Mutex
	start := time.Now()

	rolePages, err := roles.List(client.Identity, roles.ListOpts{}).AllPages(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list roles")
	}

	roleList, err := roles.ExtractRoles(rolePages)
	if err != nil {
		return errors.Wrap(err, "failed to extract roles")
	}

	for _, role := range roleList {
		mu.Lock()
		roleDetails = append(roleDetails, RoleDetails{
			RoleName: role.Name,
			RoleID:   role.ID,
		})
		mu.Unlock()
	}

	log.Debugf("Fetched %d roles in %v", len(roleDetails), time.Since(start))
	return printRoles(roleDetails, cfg.OutputFormat)
}

// listUsersByRole lists all users with a specific role in a project
func listUsersByRole(ctx context.Context, client *Client, cfg Config) error {
	projectID, err := getProjectID(ctx, client, cfg.ProjectName)
	if err != nil {
		return errors.Wrap(err, "failed to get project ID")
	}
	roleID, err := getRoleID(ctx, client, cfg.RoleName)
	if err != nil {
		return errors.Wrap(err, "failed to get role ID")
	}

	var userDetails []UserDetails
	var mu sync.Mutex
	start := time.Now()

	roleAssignments, err := roles.ListAssignments(client.Identity, roles.ListAssignmentsOpts{
		RoleID:         roleID,
		ScopeProjectID: projectID,
	}).AllPages(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list role assignments")
	}

	roleList, err := roles.ExtractRoleAssignments(roleAssignments)
	if err != nil {
		return errors.Wrap(err, "failed to extract role assignments")
	}

	for _, assignment := range roleList {
		if assignment.User.ID != "" {
			user, err := users.Get(ctx, client.Identity, assignment.User.ID).Extract()
			if err != nil {
				log.Warnf("Failed to get user %s: %v", assignment.User.ID, err)
				continue
			}
			email := ""
			if e, ok := user.Extra["email"].(string); ok {
				email = e
			}
			mu.Lock()
			userDetails = append(userDetails, UserDetails{
				UserName: user.Name,
				UserID:   user.ID,
				Email:    email,
			})
			mu.Unlock()
		}
	}

	log.Debugf("Fetched %d users in %v", len(userDetails), time.Since(start))
	return printUsers(userDetails, cfg.OutputFormat)
}

// listUserRolesAllProjects lists all roles for a user across all projects
func listUserRolesAllProjects(ctx context.Context, client *Client, cfg Config) error {
	userID, err := getUserID(ctx, client, cfg.UserName)
	if err != nil {
		return errors.Wrap(err, "failed to get user ID")
	}

	var roleDetails []RoleDetails
	var mu sync.Mutex
	start := time.Now()

	roleAssignments, err := roles.ListAssignments(client.Identity, roles.ListAssignmentsOpts{
		UserID: userID,
	}).AllPages(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list role assignments")
	}

	roleList, err := roles.ExtractRoleAssignments(roleAssignments)
	if err != nil {
		return errors.Wrap(err, "failed to extract role assignments")
	}

	for _, assignment := range roleList {
		if assignment.Role.ID != "" && assignment.Scope.Project.ID != "" {
			role, err := roles.Get(ctx, client.Identity, assignment.Role.ID).Extract()
			if err != nil {
				log.Warnf("Failed to get role %s: %v", assignment.Role.ID, err)
				continue
			}
			projectName, err := getProjectName(ctx, client, assignment.Scope.Project.ID)
			if err != nil {
				log.Warnf("Failed to get project %s: %v", assignment.Scope.Project.ID, err)
				continue
			}
			mu.Lock()
			roleDetails = append(roleDetails, RoleDetails{
				RoleName:    role.Name,
				RoleID:      role.ID,
				ProjectName: projectName,
			})
			mu.Unlock()
		}
	}

	log.Debugf("Fetched %d roles in %v", len(roleDetails), time.Since(start))
	return printRoles(roleDetails, cfg.OutputFormat)
}

// listUsersInProject lists all users with any role in a project
func listUsersInProject(ctx context.Context, client *Client, cfg Config) error {
	projectID, err := getProjectID(ctx, client, cfg.ProjectName)
	if err != nil {
		return errors.Wrap(err, "failed to get project ID")
	}

	var userDetails []UserDetails
	var mu sync.Mutex
	userMap := make(map[string]UserDetails) // Track unique users by UserID
	start := time.Now()

	roleAssignments, err := roles.ListAssignments(client.Identity, roles.ListAssignmentsOpts{
		ScopeProjectID: projectID,
	}).AllPages(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list role assignments")
	}

	roleList, err := roles.ExtractRoleAssignments(roleAssignments)
	if err != nil {
		return errors.Wrap(err, "failed to extract role assignments")
	}

	for _, assignment := range roleList {
		if assignment.User.ID != "" {
			if _, exists := userMap[assignment.User.ID]; !exists {
				user, err := users.Get(ctx, client.Identity, assignment.User.ID).Extract()
				if err != nil {
					log.Warnf("Failed to get user %s: %v", assignment.User.ID, err)
					continue
				}
				email := ""
				if e, ok := user.Extra["email"].(string); ok {
					email = e
				}
				mu.Lock()
				userMap[assignment.User.ID] = UserDetails{
					UserName: user.Name,
					UserID:   user.ID,
					Email:    email,
				}
				mu.Unlock()
			}
		}
	}

	// Convert map to slice for output
	for _, user := range userMap {
		userDetails = append(userDetails, user)
	}

	log.Debugf("Fetched %d users in %v", len(userDetails), time.Since(start))
	return printUsers(userDetails, cfg.OutputFormat)
}

// assignUserRole assigns a role to a user in a project
func assignUserRole(ctx context.Context, client *Client, cfg Config) error {
	userID, err := getUserID(ctx, client, cfg.UserName)
	if err != nil {
		return errors.Wrap(err, "failed to get user ID")
	}
	projectID, err := getProjectID(ctx, client, cfg.ProjectName)
	if err != nil {
		return errors.Wrap(err, "failed to get project ID")
	}
	roleID, err := getRoleID(ctx, client, cfg.RoleName)
	if err != nil {
		return errors.Wrap(err, "failed to get role ID")
	}

	err = roles.Assign(ctx, client.Identity, roleID, roles.AssignOpts{
		UserID:    userID,
		ProjectID: projectID,
	}).ExtractErr()
	if err != nil {
		return errors.Wrap(err, "failed to assign role")
	}

	log.Infof("Assigned role %s to user %s in project %s", cfg.RoleName, cfg.UserName, cfg.ProjectName)
	return nil
}

// removeUserRole removes a role from a user in a project
func removeUserRole(ctx context.Context, client *Client, cfg Config) error {
	userID, err := getUserID(ctx, client, cfg.UserName)
	if err != nil {
		return errors.Wrap(err, "failed to get user ID")
	}
	projectID, err := getProjectID(ctx, client, cfg.ProjectName)
	if err != nil {
		return errors.Wrap(err, "failed to get project ID")
	}
	roleID, err := getRoleID(ctx, client, cfg.RoleName)
	if err != nil {
		return errors.Wrap(err, "failed to get role ID")
	}

	err = roles.Unassign(ctx, client.Identity, roleID, roles.UnassignOpts{
		UserID:    userID,
		ProjectID: projectID,
	}).ExtractErr()
	if err != nil {
		return errors.Wrap(err, "failed to remove role")
	}

	log.Infof("Removed role %s from user %s in project %s", cfg.RoleName, cfg.UserName, cfg.ProjectName)
	return nil
}

// printRoles prints the role details in the specified format
func printRoles(roleDetails []RoleDetails, outputFormat string) error {
	if len(roleDetails) == 0 {
		fmt.Println("No roles found.")
		return nil
	}

	if strings.ToLower(outputFormat) == "table" {
		writer := tabwriter.NewWriter(os.Stdout, 0, 1, 2, ' ', tabwriter.TabIndent)
		fmt.Fprintln(writer, "ROLE_NAME\tROLE_ID\tPROJECT_NAME\t")
		fmt.Println("##############")
		for _, role := range roleDetails {
			fmt.Fprintf(writer, "%s\t%s\t%s\t\n", role.RoleName, role.RoleID, role.ProjectName)
		}
		writer.Flush()
		fmt.Println("##############")
		fmt.Printf("Number of roles: %d\n", len(roleDetails))
		fmt.Println("##############")
	} else {
		data, err := json.MarshalIndent(roleDetails, "", "  ")
		if err != nil {
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	}

	return nil
}

// printUsers prints the user details in the specified format
func printUsers(userDetails []UserDetails, outputFormat string) error {
	if len(userDetails) == 0 {
		fmt.Println("No users found in the project.")
		return nil
	}

	if strings.ToLower(outputFormat) == "table" {
		writer := tabwriter.NewWriter(os.Stdout, 0, 1, 2, ' ', tabwriter.TabIndent)
		fmt.Fprintln(writer, "USER_NAME\tUSER_ID\tEMAIL\t")
		fmt.Println("##############")
		for _, user := range userDetails {
			fmt.Fprintf(writer, "%s\t%s\t%s\t\n", user.UserName, user.UserID, user.Email)
		}
		writer.Flush()
		fmt.Println("##############")
		fmt.Printf("Number of users: %d\n", len(userDetails))
		fmt.Println("##############")
	} else {
		data, err := json.MarshalIndent(userDetails, "", "  ")
		if err != nil {
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	}

	return nil
}
