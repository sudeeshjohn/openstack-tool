package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sudeeshjohn/openstack-tool/cleanupvms"
	"github.com/sudeeshjohn/openstack-tool/getvminfo"
	"github.com/sudeeshjohn/openstack-tool/user"
)

func main() {
	// Define subcommands
	getVMInfoCmd := flag.NewFlagSet("get-vminfo", flag.ExitOnError)
	verbose := getVMInfoCmd.Bool("v", false, "Enable verbose logging")
	filter := getVMInfoCmd.String("filter", "", "Filter VMs (e.g., host=host1,email=user@example.com)")
	output := getVMInfoCmd.String("output", "table", "Output format (table or json)")
	useFlavorCache := getVMInfoCmd.Bool("use-flavor-cache", false, "Use flavor cache")

	cleanupCmd := flag.NewFlagSet("cleanup-vms", flag.ExitOnError)
	userFlag := cleanupCmd.String("u", "", "SSH username")
	passFlag := cleanupCmd.String("p", "", "SSH password")
	ipFlag := cleanupCmd.String("i", "", "Hypervisor IP address")
	dryRun := cleanupCmd.Bool("dry-run", false, "Perform a dry run without deleting VMs")

	userRolesCmd := flag.NewFlagSet("user-roles", flag.ExitOnError)
	userVerbose := userRolesCmd.Bool("v", false, "Enable verbose logging")
	userOutput := userRolesCmd.String("output", "table", "Output format (table or json)")
	userAction := userRolesCmd.String("action", "list", "Action to perform (list, assign, remove, list-roles, list-users-by-role, list-user-roles-all-projects, list-users-in-project)")
	userName := userRolesCmd.String("user-name", "", "User name")
	projectName := userRolesCmd.String("project-name", "", "Project name")
	roleName := userRolesCmd.String("role-name", "", "Role name")

	// Check if a subcommand is provided
	if len(os.Args) < 2 {
		fmt.Println("Expected 'get-vminfo', 'cleanup-vms', or 'user-roles' subcommands")
		fmt.Println("Usage:")
		fmt.Println("  get-vminfo [-v] [-filter=<filter>] [-output=<table|json>] [-use-flavor-cache]")
		fmt.Println("  cleanup-vms -u=<username> -p=<password> -i=<ip> [-dry-run]")
		fmt.Println("  user-roles [-v] [-output=<table|json>] [-action=<list|assign|remove|list-roles|list-users-by-role|list-user-roles-all-projects|list-users-in-project>] [-user-name=<name>] [-project-name=<name>] [-role-name=<name>]")
		os.Exit(1)
	}

	// Parse the subcommand
	switch os.Args[1] {
	case "get-vminfo":
		getVMInfoCmd.Parse(os.Args[2:])
		if err := getvminfo.Run(*verbose, *filter, *output, *useFlavorCache); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "cleanup-vms":
		cleanupCmd.Parse(os.Args[2:])
		if *userFlag == "" || *passFlag == "" || *ipFlag == "" {
			fmt.Println("Error: -u, -p, and -i flags are required for cleanup-vms")
			cleanupCmd.Usage()
			os.Exit(1)
		}
		if err := cleanupvms.Run(*userFlag, *passFlag, *ipFlag, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "user-roles":
		userRolesCmd.Parse(os.Args[2:])
		if err := user.Run(*userVerbose, *userOutput, *userAction, *userName, *projectName, *roleName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Println("Expected 'get-vminfo', 'cleanup-vms', or 'user-roles' subcommands")
		fmt.Println("Usage:")
		fmt.Println("  get-vminfo [-v] [-filter=<filter>] [-output=<table|json>] [-use-flavor-cache]")
		fmt.Println("  cleanup-vms -u=<username> -p=<password> -i=<ip> [-dry-run]")
		fmt.Println("  user-roles [-v] [-output=<table|json>] [-action=<list|assign|remove|list-roles|list-users-by-role|list-user-roles-all-projects|list-users-in-project>] [-user-name=<name>] [-project-name=<name>] [-role-name=<name>]")
		os.Exit(1)
	}
}
