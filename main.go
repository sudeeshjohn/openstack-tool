package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sudeeshjohn/openstack-tool/cleanupvms"
	"github.com/sudeeshjohn/openstack-tool/getvminfo"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Error: a subcommand is required (get-vminfo or cleanup-vms)")
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	args := os.Args[2:]

	switch subcommand {
	case "get-vminfo":
		handleGetVMInfo(args)
	case "cleanup-vms":
		handleCleanupVMs(args)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown subcommand %q; must be 'get-vminfo' or 'cleanup-vms'\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <subcommand> [flags]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "A tool to manage OpenStack VMs. Select a subcommand.\n\n")
	fmt.Fprintf(os.Stderr, "Subcommands:\n")
	fmt.Fprintf(os.Stderr, "  get-vminfo     Fetch and display VM information\n")
	fmt.Fprintf(os.Stderr, "  cleanup-vms    Clean up abandoned VMs on a NovaLink host\n")
	fmt.Fprintf(os.Stderr, "\nRun '%s <subcommand> -h' for subcommand-specific help.\n", os.Args[0])
}

func handleGetVMInfo(args []string) {
	fs := flag.NewFlagSet("get-vminfo", flag.ExitOnError)
	verbose := fs.Bool("v", false, "Enable verbose debug output")
	filterStr := fs.String("filter", "", "Filter VMs by host, email, status, project, or days (e.g., 'host=host1,email=user@example.com,status=ACTIVE,project=proj1,days>30')")
	outputFormat := fs.String("output", "table", "Output format: table, json")
	useFlavorCache := fs.Bool("use-flavor-cache", true, "Use flavor caching")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s get-vminfo [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Fetch and display VM information.\n\n")
		fmt.Fprintf(os.Stderr, "Required environment variables: OS_AUTH_URL, OS_USERNAME, OS_PASSWORD, OS_PROJECT_NAME, OS_DOMAIN_NAME\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample: %s get-vminfo -v -filter=\"host=host1,status=ACTIVE,days>7\" -output=table -use-flavor-cache=false\n", os.Args[0])
	}

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if err := getvminfo.Run(*verbose, *filterStr, *outputFormat, *useFlavorCache); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleCleanupVMs(args []string) {
	fs := flag.NewFlagSet("cleanup-vms", flag.ExitOnError)
	user := fs.String("u", "root", "Username for SSH (default: root)")
	password := fs.String("p", "", "Password for SSH")
	ip := fs.String("i", "", "IP of the NovaLink host")
	dryRun := fs.Bool("dry-run", false, "Only show VMs that would be deleted, without deleting")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s cleanup-vms [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Clean up abandoned VMs on a NovaLink host.\n\n")
		fmt.Fprintf(os.Stderr, "Required environment variables: OS_AUTH_URL, OS_USERNAME, OS_PASSWORD, OS_PROJECT_NAME, OS_DOMAIN_NAME, OS_REGION_NAME\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample: %s cleanup-vms -u=root -p=secret -i=192.168.1.100 -dry-run\n", os.Args[0])
	}

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if *ip == "" {
		fmt.Fprintln(os.Stderr, "Error: -i (NovaLink host IP) is required")
		fs.Usage()
		os.Exit(1)
	}

	if err := cleanupvms.Run(*user, *password, *ip, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
