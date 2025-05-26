package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/pflag"
	"github.com/sudeeshjohn/openstack-tool/auth"
	"github.com/sudeeshjohn/openstack-tool/cleannovastalevms"
	"github.com/sudeeshjohn/openstack-tool/images"
	"github.com/sudeeshjohn/openstack-tool/storage"
	"github.com/sudeeshjohn/openstack-tool/user"
	"github.com/sudeeshjohn/openstack-tool/vm"
	"github.com/sudeeshjohn/openstack-tool/volume"
)

func main() {
	// Define subcommands
	vmInfoCmd := pflag.NewFlagSet("vm info", pflag.ExitOnError)
	verbose := vmInfoCmd.Bool("verbose", false, "Enable verbose logging")
	filter := vmInfoCmd.String("filter", "", "Filter VMs (e.g., host=host1,email=user@example.com)")
	output := vmInfoCmd.String("output", "table", "Output format (table or json)")
	useFlavorCache := vmInfoCmd.Bool("use-flavor-cache", false, "Use flavor cache")
	timeout := vmInfoCmd.Int("timeout", 300, "Timeout in seconds for API operations")

	vmManageCmd := pflag.NewFlagSet("vm manage", pflag.ExitOnError)
	manageVerbose := vmManageCmd.Bool("verbose", false, "Enable verbose logging")
	manageVM := vmManageCmd.String("vm", "", "VM name(s) or ID(s), comma-separated (e.g., vm1,vm2)")
	manageProject := vmManageCmd.String("project", "", "Project name")
	manageDryRun := vmManageCmd.Bool("dry-run", false, "Perform a dry run without making changes")
	manageOutput := vmManageCmd.String("output", "table", "Output format (table or json)")
	manageTimeout := vmManageCmd.Int("timeout", 300, "Timeout in seconds for API operations")
	manageState := vmManageCmd.String("state", "", "Desired state for set-state action (ACTIVE or ERROR)")

	cleanNovaStaleVmsCmd := pflag.NewFlagSet("clean-nova-stale-vms", pflag.ExitOnError)
	cleanVerbose := cleanNovaStaleVmsCmd.Bool("verbose", false, "Enable verbose logging")
	userFlag := cleanNovaStaleVmsCmd.String("user", "", "SSH username")
	passFlag := cleanNovaStaleVmsCmd.String("password", "", "SSH password")
	ipFlag := cleanNovaStaleVmsCmd.String("ip", "", "Hypervisor IP address")
	dryRunClean := cleanNovaStaleVmsCmd.Bool("dry-run", false, "Perform a dry run without deleting VMs")
	outputClean := cleanNovaStaleVmsCmd.String("output", "table", "Output format (table or json)")
	timeoutClean := cleanNovaStaleVmsCmd.Int("timeout", 300, "Timeout in seconds for API operations")

	userRolesCmd := pflag.NewFlagSet("user-roles", pflag.ExitOnError)
	userVerbose := userRolesCmd.Bool("verbose", false, "Enable verbose logging")
	userOutput := userRolesCmd.String("output", "table", "Output format (table or json)")
	userAction := userRolesCmd.String("action", "list", "Action to perform (list, assign, remove, list-roles, list-users-by-role, list-user-roles-all-projects, list-users-in-project)")
	userName := userRolesCmd.String("user", "", "User name")
	userProjectName := userRolesCmd.String("project", "", "Project name")
	roleName := userRolesCmd.String("role", "", "Role name")
	userTimeout := userRolesCmd.Int("timeout", 300, "Timeout in seconds for API operations")

	vmCreateCmd := pflag.NewFlagSet("vm create", pflag.ExitOnError)
	createVerbose := vmCreateCmd.Bool("verbose", false, "Enable verbose logging")
	createTimeout := vmCreateCmd.Int("timeout", 300, "Timeout in seconds for API operations")

	createCmd := pflag.NewFlagSet("create", pflag.ExitOnError)
	createCmdVerbose := createCmd.Bool("verbose", false, "Enable verbose logging")
	createCmdTimeout := createCmd.Int("timeout", 300, "Timeout in seconds for API operations")

	volumeCmd := pflag.NewFlagSet("volume", pflag.ExitOnError)
	volumeCmd.Usage = func() {
		fmt.Println("Usage: openstack-tool volume <subcommand> [flags]")
		fmt.Println("Subcommands:")
		fmt.Println("  list")
		fmt.Println("    List volumes in a specific project")
		fmt.Println("  list-all")
		fmt.Println("    List all volumes across all projects")
		fmt.Println("  change-status")
		fmt.Println("    Change the status of specified volumes")
		fmt.Println("  delete")
		fmt.Println("    Delete specified volumes")
		fmt.Println("Flags:")
		fmt.Println("  --verbose          Enable verbose logging")
		fmt.Println("  --output           Output format (table or json, default: table)")
		fmt.Println("  --volume           Comma-separated volume names (required for change-status, delete)")
		fmt.Println("  --project          Project name (required for list, change-status, delete; overrides OS_PROJECT_NAME)")
		fmt.Println("  --status           Target status for volume (required for change-status, e.g., available, in-use)")
		fmt.Println("  --long             Show extended volume details (attached-to, wwn) for list and list-all")
		fmt.Println("  --not-associated   Show only volumes not associated with images or VMs (for list and list-all)")
		fmt.Println("  --timeout          Timeout in seconds for API operations (default: 300)")
		fmt.Println("Examples:")
		fmt.Println("  openstack-tool volume list --project=proj1 --not-associated --output=table")
		fmt.Println("  openstack-tool volume list-all --long --not-associated --output=json")
		fmt.Println("  openstack-tool volume change-status --volume=vol1,vol2 --project=proj1 --status=available")
		fmt.Println("  openstack-tool volume delete --volume=vol1 --project=proj1")
	}
	volumeVerbose := volumeCmd.Bool("verbose", false, "Enable verbose logging")
	volumeOutput := volumeCmd.String("output", "table", "Output format (table or json)")
	volumeNames := volumeCmd.String("volume", "", "Comma-separated volume names (required for change-status, delete)")
	volumeProject := volumeCmd.String("project", "", "Project name (required for list, change-status, delete; overrides OS_PROJECT_NAME)")
	volumeStatus := volumeCmd.String("status", "", "Target status for volume (e.g., available, in-use)")
	volumeLong := volumeCmd.Bool("long", false, "Show extended volume details (attached-to, wwn) for list and list-all")
	volumeNotAssociated := volumeCmd.Bool("not-associated", false, "Show only volumes not associated with images or VMs (for list and list-all)")
	volumeTimeout := volumeCmd.Int("timeout", 300, "Timeout in seconds for API operations")

	imagesCmd := pflag.NewFlagSet("images", pflag.ExitOnError)
	imagesVerbose := imagesCmd.Bool("verbose", false, "Enable verbose logging")
	imagesProject := imagesCmd.String("project", "", "Project name (overrides OS_PROJECT_NAME)")
	imagesOutput := imagesCmd.String("output", "table", "Output format (table or json, default: table)")
	imagesAction := imagesCmd.String("action", "list", "Action to perform (list, list-all)")
	imagesTimeout := imagesCmd.Int("timeout", 300, "Timeout in seconds for API operations")
	imagesLong := imagesCmd.Bool("long", false, "Show WWN and Size in table output")
	imagesLimit := imagesCmd.Int("limit", 0, "Limit number of images to fetch (0 for no limit)")

	// Define vol subcommand
	volCmd := pflag.NewFlagSet("vol", pflag.ExitOnError)
	volCmd.Usage = func() {
		fmt.Println("Usage: openstack-tool storage vol <action> [flags]")
		fmt.Println("Actions:")
		fmt.Println("  list")
		fmt.Println("    List storage volumes")
		fmt.Println("Flags:")
		fmt.Println("  --ip               IP address or hostname of the Storage (required)")
		fmt.Println("  --username         Username for SSH authentication (required)")
		fmt.Println("  --password         Password for SSH authentication (required)")
		fmt.Println("  --long             Include ID, Capacity, Status, and Volume Type in detailed format")
		fmt.Println("  --verbose          Display raw lsvdisk output only")
		fmt.Println("  --timeout          Timeout in seconds for API operations (default: 300)")
		fmt.Println("Examples:")
		fmt.Println("  openstack-tool storage vol list --ip=192.168.1.100 --username=admin --password=secret --long --timeout=300")
	}
	storageIP := volCmd.String("ip", "", "IP address or hostname of the Storage (required)")
	storageUsername := volCmd.String("username", "", "Username for SSH authentication (required)")
	storagePassword := volCmd.String("password", "", "Password for SSH authentication (required)")
	storageLong := volCmd.Bool("long", false, "Include ID, Capacity, Status, and Volume Type in detailed format")
	storageVerbose := volCmd.Bool("verbose", false, "Display raw lsvdisk output only")
	storageTimeout := volCmd.Int("timeout", 300, "Timeout in seconds for API operations (default: 300)")

	// Check if a subcommand is provided
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Parse the subcommand
	var authVerbose bool
	var authClient *auth.Client
	var err error

	switch os.Args[1] {
	case "vm":
		if len(os.Args) < 3 {
			fmt.Println("Error: 'vm' subcommand requires 'info', 'manage', or 'create' action")
			printUsage()
			os.Exit(1)
		}
		switch os.Args[2] {
		case "info":
			vmInfoCmd.Parse(os.Args[3:])
			authVerbose = *verbose
			timeoutDuration := time.Duration(*timeout) * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
			defer cancel()
			authClient, err = auth.NewClient(ctx, auth.Config{
				Verbose: authVerbose,
				Timeout: timeoutDuration,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
				os.Exit(1)
			}
			if err := vm.Run(ctx, authClient, "info", vm.Config{
				Verbose:        *verbose,
				FilterStr:      *filter,
				OutputFormat:   *output,
				UseFlavorCache: *useFlavorCache,
				MaxRetries:     3,
				MaxConcurrency: 10,
				Timeout:        timeoutDuration,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		case "manage":
			vmManageCmd.Parse(os.Args[3:])
			authVerbose = *manageVerbose
			timeoutDuration := time.Duration(*manageTimeout) * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
			defer cancel()
			authClient, err = auth.NewClient(ctx, auth.Config{
				Verbose: authVerbose,
				Timeout: timeoutDuration,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
				os.Exit(1)
			}
			if *manageVM == "" || *manageProject == "" {
				fmt.Println("Error: --vm and --project flags are required for manage")
				printManageVmsUsage()
				os.Exit(1)
			}
			if len(os.Args) < 4 {
				fmt.Println("Error: 'vm manage' requires a subcommand (e.g., delete, start)")
				printManageVmsUsage()
				os.Exit(1)
			}
			if os.Args[3] == "set-state" && *manageState == "" {
				fmt.Println("Error: --state flag is required for set-state subcommand")
				printManageVmsUsage()
				os.Exit(1)
			}
			if err := vm.Run(ctx, authClient, os.Args[3], vm.Config{
				Verbose:      *manageVerbose,
				VM:           *manageVM,
				Project:      *manageProject,
				DryRun:       *manageDryRun,
				OutputFormat: *manageOutput,
				Timeout:      timeoutDuration,
				State:        *manageState,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		case "create":
			vmCreateCmd.Parse(os.Args[3:])
			authVerbose = *createVerbose
			timeoutDuration := time.Duration(*createTimeout) * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
			defer cancel()
			authClient, err = auth.NewClient(ctx, auth.Config{
				Verbose: authVerbose,
				Timeout: timeoutDuration,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
				os.Exit(1)
			}
			if err := vm.CreateVM(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Printf("Error: invalid subcommand '%s' for 'vm'; expected 'info', 'manage', or 'create'\n", os.Args[2])
			printUsage()
			os.Exit(1)
		}
	case "clean-nova-stale-vms":
		cleanNovaStaleVmsCmd.Parse(os.Args[2:])
		authVerbose = *cleanVerbose
		timeoutDuration := time.Duration(*timeoutClean) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
		authClient, err = auth.NewClient(ctx, auth.Config{
			Verbose: authVerbose,
			Timeout: timeoutDuration,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
			os.Exit(1)
		}
		if *userFlag == "" || *passFlag == "" || *ipFlag == "" {
			fmt.Println("Error: --user, --password, and --ip flags are required for clean-nova-stale-vms")
			cleanNovaStaleVmsCmd.Usage()
			os.Exit(1)
		}
		if err := cleannovastalevms.Run(ctx, authClient, *cleanVerbose, *userFlag, *passFlag, *ipFlag, *outputClean, *dryRunClean); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "user-roles":
		userRolesCmd.Parse(os.Args[2:])
		authVerbose = *userVerbose
		timeoutDuration := time.Duration(*userTimeout) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
		authClient, err = auth.NewClient(ctx, auth.Config{
			Verbose: authVerbose,
			Timeout: timeoutDuration,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
			os.Exit(1)
		}
		if err := user.Run(ctx, authClient, *userVerbose, *userOutput, *userAction, *userName, *userProjectName, *roleName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "volume":
		if len(os.Args) < 3 {
			fmt.Println("Error: 'volume' subcommand requires 'list', 'list-all', 'change-status', or 'delete'")
			volumeCmd.Usage()
			os.Exit(1)
		}
		validVolumeSubcommands := map[string]bool{
			"list":          true,
			"list-all":      true,
			"change-status": true,
			"delete":        true,
		}
		subcommand := os.Args[2]
		if !validVolumeSubcommands[subcommand] {
			fmt.Printf("Error: invalid subcommand '%s' for 'volume'; expected 'list', 'list-all', 'change-status', or 'delete'\n", subcommand)
			volumeCmd.Usage()
			os.Exit(1)
		}
		volumeCmd.Parse(os.Args[2:])
		if volumeCmd.Parsed() && volumeCmd.Lookup("help") != nil && volumeCmd.Lookup("help").Value.String() == "true" {
			volumeCmd.Usage()
			os.Exit(0)
		}
		authVerbose = *volumeVerbose
		timeoutDuration := time.Duration(*volumeTimeout) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
		authClient, err = auth.NewClient(ctx, auth.Config{
			Verbose: authVerbose,
			Timeout: timeoutDuration,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
			os.Exit(1)
		}
		if (subcommand == "list" || subcommand == "change-status" || subcommand == "delete") && (*volumeProject == "" && os.Getenv("OS_PROJECT_NAME") == "") {
			fmt.Println("Error: --project flag or OS_PROJECT_NAME environment variable is required for list, change-status, and delete subcommands")
			volumeCmd.Usage()
			os.Exit(1)
		}
		if subcommand == "change-status" && *volumeStatus == "" {
			fmt.Println("Error: --status flag is required for change-status subcommand")
			volumeCmd.Usage()
			os.Exit(1)
		}
		if (subcommand == "change-status" || subcommand == "delete") && *volumeNames == "" {
			fmt.Println("Error: --volume flag is required for change-status and delete subcommands")
			volumeCmd.Usage()
			os.Exit(1)
		}
		if err := volume.Run(ctx, authClient, *volumeVerbose, *volumeOutput, subcommand, *volumeNames, *volumeProject, *volumeStatus, *volumeLong, *volumeNotAssociated); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "images":
		imagesCmd.Parse(os.Args[2:])
		authVerbose = *imagesVerbose
		timeoutDuration := time.Duration(*imagesTimeout) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
		authClient, err = auth.NewClient(ctx, auth.Config{
			Verbose: authVerbose,
			Timeout: timeoutDuration,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
			os.Exit(1)
		}
		if *imagesAction == "list" && *imagesProject == "" && os.Getenv("OS_PROJECT_NAME") == "" {
			fmt.Println("Error: --project flag or OS_PROJECT_NAME environment variable is required for list action")
			imagesCmd.Usage()
			os.Exit(1)
		}
		if err := images.Run(ctx, authClient, images.Config{
			Verbose:      *imagesVerbose,
			ProjectName:  *imagesProject,
			OutputFormat: *imagesOutput,
			Action:       *imagesAction,
			Timeout:      timeoutDuration,
			Long:         *imagesLong,
			Limit:        *imagesLimit,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "storage":
		if len(os.Args) < 3 {
			fmt.Println("Error: 'storage' subcommand requires 'vol'")
			printStorageUsage()
			os.Exit(1)
		}
		if os.Args[2] != "vol" {
			fmt.Printf("Error: invalid subcommand '%s' for 'storage'; expected 'vol'\n", os.Args[2])
			printStorageUsage()
			os.Exit(1)
		}
		if len(os.Args) < 4 {
			fmt.Println("Error: 'vol' subcommand requires an action (e.g., 'list')")
			volCmd.Usage()
			os.Exit(1)
		}
		if os.Args[3] != "list" {
			fmt.Printf("Error: invalid action '%s' for 'vol'; expected 'list'\n", os.Args[3])
			volCmd.Usage()
			os.Exit(1)
		}
		volCmd.Parse(os.Args[2:]) // Parse vol subcommand and flags starting from 'vol'
		if volCmd.Parsed() && volCmd.Lookup("help") != nil && volCmd.Lookup("help").Value.String() == "true" {
			volCmd.Usage()
			os.Exit(0)
		}
		authVerbose = *storageVerbose
		timeoutDuration := time.Duration(*storageTimeout) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
		if *storageIP == "" || *storageUsername == "" || *storagePassword == "" {
			fmt.Println("Error: --ip, --username, and --password flags are required for storage vol")
			volCmd.Usage()
			os.Exit(1)
		}
		// Initialize authentication client (optional for storage, but kept for consistency)
		authClient, err = auth.NewClient(ctx, auth.Config{
			Verbose: authVerbose,
			Timeout: timeoutDuration,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
			os.Exit(1)
		}
		if err := storage.Run(ctx, storage.Config{
			IP:       *storageIP,
			Username: *storageUsername,
			Password: *storagePassword,
			Long:     *storageLong,
			Verbose:  *storageVerbose,
			Timeout:  *storageTimeout,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "create":
		createCmd.Parse(os.Args[2:])
		authVerbose = *createCmdVerbose
		timeoutDuration := time.Duration(*createCmdTimeout) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
		authClient, err = auth.NewClient(ctx, auth.Config{
			Verbose: authVerbose,
			Timeout: timeoutDuration,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
			os.Exit(1)
		}
		if err := vm.CreateVM(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Printf("Error: unknown subcommand '%s'\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("OpenStack Tool: Manage VMs, users, volumes, images, and storage in an OpenStack cloud.")
	fmt.Println("Usage: openstack-tool <subcommand> [flags]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  vm")
	fmt.Println("    Subcommands: info, manage, create")
	fmt.Println("    Example: openstack-tool vm info --verbose --filter=\"host=host1,status=ACTIVE,days>7\" --output=json --timeout=300")
	fmt.Println("    Example: openstack-tool vm manage delete --vm=test-vm1,test-vm2 --project=admin --dry-run --output=table --timeout=300")
	fmt.Println("    Example: openstack-tool vm create --verbose --timeout=300")
	fmt.Println("  clean-nova-stale-vms")
	fmt.Println("    Clean stale VMs on a hypervisor")
	fmt.Println("    Example: openstack-tool clean-nova-stale-vms --verbose --user=root --password=secret --ip=192.168.1.100 --dry-run --output=table --timeout=300")
	fmt.Println("  user-roles")
	fmt.Println("    Manage user roles in OpenStack")
	fmt.Println("    Example: openstack-tool user-roles --action=list-users-in-project --project=admin --output=table --timeout=300")
	fmt.Println("  volume")
	fmt.Println("    Manage volumes in OpenStack")
	fmt.Println("    Example: openstack-tool volume list --project=proj1 --not-associated --output=table")
	fmt.Println("    Example: openstack-tool volume list-all --long --not-associated --output=json")
	fmt.Println("  images")
	fmt.Println("    Manage OpenStack images")
	fmt.Println("    Example: openstack-tool images --action=list --project=proj1 --output=table --timeout=300")
	fmt.Println("  storage")
	fmt.Println("    Manage storage volumes on Storage")
	fmt.Println("    Subcommands: vol")
	fmt.Println("    Example: openstack-tool storage vol list --ip=192.168.1.100 --username=admin --password=secret --long --timeout=300")
	fmt.Println("  create")
	fmt.Println("    Interactively create a new VM")
	fmt.Println("    Example: openstack-tool create --verbose --timeout=300")
	fmt.Println("\nEnvironment Variables:")
	fmt.Println("  OS_AUTH_URL, OS_USERNAME, OS_PASSWORD, OS_PROJECT_NAME, OS_DOMAIN_NAME, OS_REGION_NAME")
}

func printManageVmsUsage() {
	fmt.Println("Usage: openstack-tool vm manage <subcommand> [flags]")
	fmt.Println("Subcommands: delete, force-delete, start, stop, pause, unpause, suspend, resume, reboot, set-state")
	fmt.Println("Flags:")
	fmt.Println("  --verbose           Enable verbose logging")
	fmt.Println("  --vm                VM name(s) or ID(s), comma-separated (e.g., vm1,vm2) (required)")
	fmt.Println("  --project           Project name (required)")
	fmt.Println("  --dry-run           Perform a dry run without making changes")
	fmt.Println("  --output            Output format (table or json, default: table)")
	fmt.Println("  --timeout           Timeout in seconds for API operations (default: 300)")
	fmt.Println("  --state             Desired state for set-state action (ACTIVE or ERROR)")
	fmt.Println("Examples:")
	fmt.Println("  openstack-tool vm manage delete --vm=test-vm1,test-vm2 --project=admin --dry-run --output=table --timeout=300")
	fmt.Println("  openstack-tool vm manage set-state --vm=test-vm1 --project=admin --state=ACTIVE --dry-run --output=json --timeout=300")
}

func printStorageUsage() {
	fmt.Println("Usage: openstack-tool storage <subcommand> [flags]")
	fmt.Println("Subcommands:")
	fmt.Println("  vol")
	fmt.Println("    Manage storage volumes on Storage")
	fmt.Println("    Example: openstack-tool storage vol list --ip=192.168.1.100 --username=admin --password=secret")
	fmt.Println("    Actions: list")
}
