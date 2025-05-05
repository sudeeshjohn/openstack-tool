package user

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/roles"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/users"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/sudeeshjohn/openstack-tool/auth"
)

// Logger for structured logging
var log = logrus.New()

// Run executes the user role management logic
func Run(ctx context.Context, client *auth.Client, verbose bool, outputFormat, action, userName, projectName, roleName string) error {
	log.Debugf("Starting user role management with config: Verbose=%v, OutputFormat=%s, Action=%s, User=%s, Project=%s, Role=%s",
		verbose, outputFormat, action, userName, projectName, roleName)
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if verbose {
		log.SetLevel(logrus.DebugLevel)
	}

	// Action validation
	validActions := []string{"list", "assign", "remove", "list-roles", "list-users-by-role", "list-user-roles-all-projects", "list-users-in-project"}
	if !contains(validActions, action) {
		log.Debugf("Invalid action detected: %s", action)
		return fmt.Errorf("invalid action: %s; valid actions: %v", action, validActions)
	}

	switch action {
	case "list":
		log.Debug("Executing list action")
		return listUsers(ctx, client, outputFormat)
	case "assign":
		if userName == "" || projectName == "" || roleName == "" {
			log.Debug("Missing required flags for assign action")
			return fmt.Errorf("user, project, and role flags are required for assign action")
		}
		log.Debugf("Executing assign action for user %s, project %s, role %s", userName, projectName, roleName)
		return assignRole(ctx, client, userName, projectName, roleName)
	case "remove":
		if userName == "" || projectName == "" || roleName == "" {
			log.Debug("Missing required flags for remove action")
			return fmt.Errorf("user, project, and role flags are required for remove action")
		}
		log.Debugf("Executing remove action for user %s, project %s, role %s", userName, projectName, roleName)
		return removeRole(ctx, client, userName, projectName, roleName)
	case "list-roles":
		log.Debug("Executing list-roles action")
		return listRoles(ctx, client, outputFormat)
	case "list-users-by-role":
		if roleName == "" {
			log.Debug("Missing role flag for list-users-by-role action")
			return fmt.Errorf("role flag is required for list-users-by-role action")
		}
		log.Debugf("Executing list-users-by-role action for role %s", roleName)
		return listUsersByRole(ctx, client, roleName, outputFormat)
	case "list-user-roles-all-projects":
		if userName == "" {
			log.Debug("Missing user flag for list-user-roles-all-projects action")
			return fmt.Errorf("user flag is required for list-user-roles-all-projects action")
		}
		log.Debugf("Executing list-user-roles-all-projects action for user %s", userName)
		return listUserRolesAllProjects(ctx, client, userName, outputFormat)
	case "list-users-in-project":
		if projectName == "" {
			log.Debug("Missing project flag for list-users-in-project action")
			return fmt.Errorf("project flag is required for list-users-in-project action")
		}
		log.Debugf("Executing list-users-in-project action for project %s", projectName)
		return listUsersInProject(ctx, client, projectName, outputFormat)
	default:
		log.Debugf("Unsupported action encountered: %s", action)
		return fmt.Errorf("unsupported action: %s", action)
	}
}

func contains(slice []string, item string) bool {
	log.Debugf("Checking if %s is in slice", item)
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			log.Debugf("Found match for %s", item)
			return true
		}
	}
	log.Debugf("%s not found in slice", item)
	return false
}

func listUsers(ctx context.Context, client *auth.Client, outputFormat string) error {
	log.Debugf("Listing all users with output format: %s", outputFormat)
	var allUsers []users.User
	err := users.List(client.Identity, users.ListOpts{}).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing user list page")
		usersList, err := users.ExtractUsers(page)
		if err != nil {
			log.Debugf("Failed to extract users from page: %v", err)
			return false, err
		}
		log.Debugf("Extracted %d users from page", len(usersList))
		allUsers = append(allUsers, usersList...)
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to list users: %v", err)
		return errors.Wrap(err, "failed to list users")
	}
	log.Debugf("Total users fetched: %d", len(allUsers))

	// Custom struct for output without ID
	type userOutput struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	var outputUsers []userOutput
	for _, user := range allUsers {
		log.Debugf("Processing user: %s, Email: %s", user.Name, user.Description)
		outputUsers = append(outputUsers, userOutput{
			Name:  user.Name,
			Email: user.Description,
		})
	}

	if strings.ToLower(outputFormat) == "json" {
		log.Debug("Preparing JSON output for users")
		data, err := json.MarshalIndent(outputUsers, "", "  ")
		if err != nil {
			log.Debugf("Failed to marshal JSON: %v", err)
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		log.Debug("Preparing table output for users")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Name\tEmail")
		for _, u := range outputUsers {
			fmt.Fprintf(w, "%s\t%s\n", u.Name, u.Email)
		}
		w.Flush()
	}
	log.Debug("User listing completed")
	return nil
}

func assignRole(ctx context.Context, client *auth.Client, userName, projectName, roleName string) error {
	log.Debugf("Assigning role %s to user %s in project %s", roleName, userName, projectName)
	userID, err := getUserID(ctx, client, userName)
	if err != nil {
		log.Debugf("Failed to get user ID for %s: %v", userName, err)
		return err
	}
	log.Debugf("Resolved user ID: %s", userID)

	projectID, err := getProjectID(ctx, client, projectName)
	if err != nil {
		log.Debugf("Failed to get project ID for %s: %v", projectName, err)
		return err
	}
	log.Debugf("Resolved project ID: %s", projectID)

	roleID, err := getRoleID(ctx, client, roleName)
	if err != nil {
		log.Debugf("Failed to get role ID for %s: %v", roleName, err)
		return err
	}
	log.Debugf("Resolved role ID: %s", roleID)

	log.Debugf("Assigning role %s (ID: %s) to user %s (ID: %s) in project %s (ID: %s)", roleName, roleID, userName, userID, projectName, projectID)
	err = roles.Assign(ctx, client.Identity, roleID, roles.AssignOpts{
		UserID:    userID,
		ProjectID: projectID,
	}).ExtractErr()
	if err != nil {
		log.Debugf("Failed to assign role: %v", err)
		return errors.Wrap(err, "failed to assign role")
	}
	log.Infof("Assigned role %s to user %s in project %s", roleName, userName, projectName)
	log.Debug("Role assignment completed")
	return nil
}

func removeRole(ctx context.Context, client *auth.Client, userName, projectName, roleName string) error {
	log.Debugf("Removing role %s from user %s in project %s", roleName, userName, projectName)
	userID, err := getUserID(ctx, client, userName)
	if err != nil {
		log.Debugf("Failed to get user ID for %s: %v", userName, err)
		return err
	}
	log.Debugf("Resolved user ID: %s", userID)

	projectID, err := getProjectID(ctx, client, projectName)
	if err != nil {
		log.Debugf("Failed to get project ID for %s: %v", projectName, err)
		return err
	}
	log.Debugf("Resolved project ID: %s", projectID)

	roleID, err := getRoleID(ctx, client, roleName)
	if err != nil {
		log.Debugf("Failed to get role ID for %s: %v", roleName, err)
		return err
	}
	log.Debugf("Resolved role ID: %s", roleID)

	log.Debugf("Removing role %s (ID: %s) from user %s (ID: %s) in project %s (ID: %s)", roleName, roleID, userName, userID, projectName, projectID)
	err = roles.Unassign(ctx, client.Identity, roleID, roles.UnassignOpts{
		UserID:    userID,
		ProjectID: projectID,
	}).ExtractErr()
	if err != nil {
		log.Debugf("Failed to remove role: %v", err)
		return errors.Wrap(err, "failed to remove role")
	}
	log.Infof("Removed role %s from user %s in project %s", roleName, userName, projectName)
	log.Debug("Role removal completed")
	return nil
}

func listRoles(ctx context.Context, client *auth.Client, outputFormat string) error {
	log.Debugf("Listing all roles with output format: %s", outputFormat)
	var allRoles []roles.Role
	err := roles.List(client.Identity, roles.ListOpts{}).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing role list page")
		rolesList, err := roles.ExtractRoles(page)
		if err != nil {
			log.Debugf("Failed to extract roles from page: %v", err)
			return false, err
		}
		log.Debugf("Extracted %d roles from page", len(rolesList))
		allRoles = append(allRoles, rolesList...)
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to list roles: %v", err)
		return errors.Wrap(err, "failed to list roles")
	}
	log.Debugf("Total roles fetched: %d", len(allRoles))

	if strings.ToLower(outputFormat) == "json" {
		log.Debug("Preparing JSON output for roles")
		data, err := json.MarshalIndent(allRoles, "", "  ")
		if err != nil {
			log.Debugf("Failed to marshal JSON: %v", err)
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		log.Debug("Preparing table output for roles")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tName")
		for _, r := range allRoles {
			fmt.Fprintf(w, "%s\t%s\n", r.ID, r.Name)
		}
		w.Flush()
	}
	log.Debug("Role listing completed")
	return nil
}

func listUsersByRole(ctx context.Context, client *auth.Client, roleName, outputFormat string) error {
	log.Debugf("Listing users by role %s with output format: %s", roleName, outputFormat)
	roleID, err := getRoleID(ctx, client, roleName)
	if err != nil {
		log.Debugf("Failed to get role ID for %s: %v", roleName, err)
		return err
	}
	log.Debugf("Resolved role ID: %s", roleID)

	var assignments []roles.RoleAssignment
	err = roles.ListAssignments(client.Identity, roles.ListAssignmentsOpts{
		RoleID: roleID,
	}).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing role assignment page")
		assignmentList, err := roles.ExtractRoleAssignments(page)
		if err != nil {
			log.Debugf("Failed to extract assignments from page: %v", err)
			return false, err
		}
		log.Debugf("Extracted %d assignments from page", len(assignmentList))
		assignments = append(assignments, assignmentList...)
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to list assignments for role: %v", err)
		return errors.Wrap(err, "failed to list assignments for role")
	}
	log.Debugf("Total assignments fetched: %d", len(assignments))

	// Map to collect unique users
	log.Debug("Collecting unique users from assignments")
	userMap := make(map[string]users.User)
	for _, assignment := range assignments {
		if assignment.User.ID != "" {
			log.Debugf("Processing assignment for user ID: %s", assignment.User.ID)
			user, err := getUserByID(ctx, client, assignment.User.ID)
			if err != nil {
				log.Warnf("Failed to fetch user %s: %v", assignment.User.ID, err)
				continue
			}
			userMap[assignment.User.ID] = user
		}
	}
	log.Debugf("Found %d unique users", len(userMap))

	var allUsers []struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	for _, user := range userMap {
		log.Debugf("Adding user to output: %s, Email: %s", user.Name, user.Description)
		allUsers = append(allUsers, struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}{
			Name:  user.Name,
			Email: user.Description,
		})
	}

	if strings.ToLower(outputFormat) == "json" {
		log.Debug("Preparing JSON output for users by role")
		data, err := json.MarshalIndent(allUsers, "", "  ")
		if err != nil {
			log.Debugf("Failed to marshal JSON: %v", err)
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		log.Debug("Preparing table output for users by role")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Name\tEmail")
		for _, u := range allUsers {
			fmt.Fprintf(w, "%s\t%s\n", u.Name, u.Email)
		}
		w.Flush()
	}
	log.Debug("Users by role listing completed")
	return nil
}

func listUserRolesAllProjects(ctx context.Context, client *auth.Client, userName, outputFormat string) error {
	log.Debugf("Listing user %s roles across all projects with output format: %s", userName, outputFormat)
	userID, err := getUserID(ctx, client, userName)
	if err != nil {
		log.Debugf("Failed to get user ID for %s: %v", userName, err)
		return err
	}
	log.Debugf("Resolved user ID: %s", userID)

	var assignments []roles.RoleAssignment
	err = roles.ListAssignments(client.Identity, roles.ListAssignmentsOpts{
		UserID: userID,
	}).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing user role assignment page")
		assignmentList, err := roles.ExtractRoleAssignments(page)
		if err != nil {
			log.Debugf("Failed to extract assignments from page: %v", err)
			return false, err
		}
		log.Debugf("Extracted %d assignments from page", len(assignmentList))
		assignments = append(assignments, assignmentList...)
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to list assignments for user: %v", err)
		return errors.Wrap(err, "failed to list assignments for user")
	}
	log.Debugf("Total assignments fetched: %d", len(assignments))

	// Map to collect unique role names
	log.Debug("Collecting unique role names from assignments")
	roleMap := make(map[string]string)
	for _, assignment := range assignments {
		if assignment.Scope.Project.ID != "" {
			log.Debugf("Processing assignment for project ID: %s", assignment.Scope.Project.ID)
			role, err := getRoleByID(ctx, client, assignment.Role.ID)
			if err != nil {
				log.Warnf("Failed to fetch role %s: %v", assignment.Role.ID, err)
				continue
			}
			log.Debugf("Adding role %s to map", role.Name)
			roleMap[role.Name] = role.Name
		}
	}
	log.Debugf("Found %d unique roles", len(roleMap))

	var roleAssignments []struct {
		RoleName string `json:"role_name"`
	}
	for _, roleName := range roleMap {
		log.Debugf("Adding role to output: %s", roleName)
		roleAssignments = append(roleAssignments, struct {
			RoleName string `json:"role_name"`
		}{
			RoleName: roleName,
		})
	}

	if strings.ToLower(outputFormat) == "json" {
		log.Debug("Preparing JSON output for user roles")
		data, err := json.MarshalIndent(roleAssignments, "", "  ")
		if err != nil {
			log.Debugf("Failed to marshal JSON: %v", err)
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		log.Debug("Preparing table output for user roles")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Role Name")
		for _, ra := range roleAssignments {
			fmt.Fprintf(w, "%s\n", ra.RoleName)
		}
		w.Flush()
	}
	log.Debug("User roles listing completed")
	return nil
}

func listUsersInProject(ctx context.Context, client *auth.Client, projectName, outputFormat string) error {
	log.Debugf("Listing users in project %s with output format: %s", projectName, outputFormat)
	log.Warnf("list-users-in-project is a placeholder for project '%s'; full implementation requires roles.ListAssignments", projectName)

	var allUsers []users.User
	err := users.List(client.Identity, users.ListOpts{}).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing user list page")
		usersList, err := users.ExtractUsers(page)
		if err != nil {
			log.Debugf("Failed to extract users from page: %v", err)
			return false, err
		}
		log.Debugf("Extracted %d users from page", len(usersList))
		allUsers = append(allUsers, usersList...)
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to list users in project: %v", err)
		return errors.Wrap(err, "failed to list users in project")
	}
	log.Debugf("Total users fetched: %d", len(allUsers))

	// Custom struct for output without ID
	type userOutput struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	var outputUsers []userOutput
	for _, user := range allUsers {
		log.Debugf("Processing user: %s, Email: %s", user.Name, user.Description)
		outputUsers = append(outputUsers, userOutput{
			Name:  user.Name,
			Email: user.Description,
		})
	}

	if strings.ToLower(outputFormat) == "json" {
		log.Debug("Preparing JSON output for users in project")
		data, err := json.MarshalIndent(outputUsers, "", "  ")
		if err != nil {
			log.Debugf("Failed to marshal JSON: %v", err)
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		log.Debug("Preparing table output for users in project")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Name\tEmail")
		for _, u := range outputUsers {
			fmt.Fprintf(w, "%s\t%s\n", u.Name, u.Email)
		}
		w.Flush()
	}
	log.Debug("Users in project listing completed")
	return nil
}

func getUserID(ctx context.Context, client *auth.Client, userName string) (string, error) {
	log.Debugf("Retrieving user ID for user name: %s", userName)
	listOpts := users.ListOpts{
		Name: userName,
	}
	allPages, err := users.List(client.Identity, listOpts).AllPages(ctx)
	if err != nil {
		log.Debugf("Failed to list users: %v", err)
		return "", errors.Wrap(err, "failed to list users")
	}
	userList, err := users.ExtractUsers(allPages)
	if err != nil {
		log.Debugf("Failed to extract users: %v", err)
		return "", errors.Wrap(err, "failed to extract users")
	}
	if len(userList) == 0 {
		log.Debugf("User '%s' not found", userName)
		return "", fmt.Errorf("user '%s' not found", userName)
	}
	log.Debugf("Found user ID: %s for name %s", userList[0].ID, userName)
	return userList[0].ID, nil
}

func getProjectID(ctx context.Context, client *auth.Client, projectName string) (string, error) {
	log.Debugf("Retrieving project ID for project name: %s", projectName)
	listOpts := projects.ListOpts{
		Name: projectName,
	}
	allPages, err := projects.List(client.Identity, listOpts).AllPages(ctx)
	if err != nil {
		log.Debugf("Failed to list projects: %v", err)
		return "", errors.Wrap(err, "failed to list projects")
	}
	projectList, err := projects.ExtractProjects(allPages)
	if err != nil {
		log.Debugf("Failed to extract projects: %v", err)
		return "", errors.Wrap(err, "failed to extract projects")
	}
	if len(projectList) == 0 {
		log.Debugf("Project '%s' not found", projectName)
		return "", fmt.Errorf("project '%s' not found", projectName)
	}
	log.Debugf("Found project ID: %s for name %s", projectList[0].ID, projectName)
	return projectList[0].ID, nil
}

func getRoleID(ctx context.Context, client *auth.Client, roleName string) (string, error) {
	log.Debugf("Retrieving role ID for role name: %s", roleName)
	listOpts := roles.ListOpts{
		Name: roleName,
	}
	allPages, err := roles.List(client.Identity, listOpts).AllPages(ctx)
	if err != nil {
		log.Debugf("Failed to list roles: %v", err)
		return "", errors.Wrap(err, "failed to list roles")
	}
	roleList, err := roles.ExtractRoles(allPages)
	if err != nil {
		log.Debugf("Failed to extract roles: %v", err)
		return "", errors.Wrap(err, "failed to extract roles")
	}
	if len(roleList) == 0 {
		log.Debugf("Role '%s' not found", roleName)
		return "", fmt.Errorf("role '%s' not found", roleName)
	}
	log.Debugf("Found role ID: %s for name %s", roleList[0].ID, roleName)
	return roleList[0].ID, nil
}

// Helper function to get user details by ID
func getUserByID(ctx context.Context, client *auth.Client, userID string) (users.User, error) {
	log.Debugf("Retrieving user details for ID: %s", userID)
	user, err := users.Get(ctx, client.Identity, userID).Extract()
	if err != nil {
		log.Debugf("Failed to get user with ID %s: %v", userID, err)
		return users.User{}, errors.Wrapf(err, "failed to get user with ID %s", userID)
	}
	log.Debugf("Successfully retrieved user: %s", user.Name)
	return *user, nil
}

// Helper function to get role details by ID
func getRoleByID(ctx context.Context, client *auth.Client, roleID string) (roles.Role, error) {
	log.Debugf("Retrieving role details for ID: %s", roleID)
	role, err := roles.Get(ctx, client.Identity, roleID).Extract()
	if err != nil {
		log.Debugf("Failed to get role with ID %s: %v", roleID, err)
		return roles.Role{}, errors.Wrapf(err, "failed to get role with ID %s", roleID)
	}
	log.Debugf("Successfully retrieved role: %s", role.Name)
	return *role, nil
}
