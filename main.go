package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sudeeshjohn/openstack-tool/cleanupvms"
	"github.com/sudeeshjohn/openstack-tool/getvminfo"
)

func main() {
	mode := flag.String("mode", "", "Operation mode: get-vminfo or cleanup-vms (required)")
	verbose := flag.Bool("v", false, "Enable verbose debug output (get-vminfo)")
	filterStr := flag.String("filter", "", "Filter VMs by host, email, status, project, or days (get-vminfo, e.g., 'host=host1,email=user@example.com,status=ACTIVE,project=proj1,days>30')")
	outputFormat := flag.String("output", "table", "Output format: table, json (get-vminfo)")
	region := flag.String("region", "", "OpenStack region (get-vminfo, defaults to OS_REGION_NAME or RegionOne)")
	user := flag.String("u", "root", "Username for SSH (cleanup-vms, default: root)")
	password := flag.String("p", "", "Password for SSH (cleanup-vms)")
	ip := flag.String("i", "", "IP of the NovaLink host (cleanup-vms)")
	dryRun := flag.Bool("dry-run", false, "Only show VMs that would be deleted, without deleting (cleanup-vms)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "A tool to manage OpenStack VMs. Select a mode with -mode.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nModes:\n")
		fmt.Fprintf(os.Stderr, "  get-vminfo: Fetch and display VM information.\n")
		fmt.Fprintf(os.Stderr, "    Required env vars: OS_AUTH_URL, OS_USERNAME, OS_PASSWORD, OS_PROJECT_NAME, OS_DOMAIN_NAME\n")
		fmt.Fprintf(os.Stderr, "    Example: %s -mode=get-vminfo -v -filter=\"host=host1,status=ACTIVE,days>7\" -output=table\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  cleanup-vms: Clean up abandoned VMs on a NovaLink host.\n")
		fmt.Fprintf(os.Stderr, "    Required env vars: OS_AUTH_URL, OS_USERNAME, OS_PASSWORD, OS_PROJECT_NAME, OS_DOMAIN_NAME, OS_REGION_NAME\n")
		fmt.Fprintf(os.Stderr, "    Example: %s -mode=cleanup-vms -u=root -p=secret -i=192.168.1.100 -dry-run\n", os.Args[0])
	}
	flag.Parse()

	if *mode == "" {
		fmt.Fprintln(os.Stderr, "Error: -mode is required")
		flag.Usage()
		os.Exit(1)
	}

	switch *mode {
	case "get-vminfo":
		if err := getvminfo.Run(*verbose, *filterStr, *outputFormat, *region); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "cleanup-vms":
		if *ip == "" {
			fmt.Fprintln(os.Stderr, "Error: -i (NovaLink host IP) is required for cleanup-vms")
			flag.Usage()
			os.Exit(1)
		}
		if err := cleanupvms.Run(*user, *password, *ip, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Error: invalid -mode %q; must be 'get-vminfo' or 'cleanup-vms'\n", *mode)
		flag.Usage()
		os.Exit(1)
	}
}
