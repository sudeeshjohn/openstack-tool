package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

// Logger for structured logging
var log = logrus.New()

// Config holds configuration parameters for the storage module
type Config struct {
	IP       string
	Username string
	Password string
	Long     bool
	Verbose  bool
	Timeout  int // Timeout in seconds
}

// Volume represents a volume on the FlashSystem
type Volume struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Capacity   string `json:"capacity"`
	PoolName   string `json:"pool_name"`
	Status     string `json:"status"`
	VolumeType string `json:"volume_type"`
	WWN        string `json:"wwn"`
	HostName   string `json:"host_name"`
}

// Run executes the storage volume listing logic (handles 'list' action)
func Run(ctx context.Context, cfg Config) error {
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)

	// Validate input arguments
	if cfg.IP == "" || cfg.Username == "" || cfg.Password == "" {
		return fmt.Errorf("all fields IP, Username, and Password are required")
	}

	// Apply timeout to context
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	// SSH configuration
	config := &ssh.ClientConfig{
		User: cfg.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(cfg.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Insecure; use known_hosts in production
	}

	// Connect to the FlashSystem
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", cfg.IP), config)
	if err != nil {
		return fmt.Errorf("failed to connect via SSH: %v", err)
	}
	defer client.Close()

	// Create a session for lsvdisk
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %v", err)
	}
	defer session.Close()

	// Run lsvdisk command with CSV delimiter
	var lsvdiskStdout, lsvdiskStderr bytes.Buffer
	session.Stdout = &lsvdiskStdout
	session.Stderr = &lsvdiskStderr
	log.Println("Executing command: lsvdisk -delim ,")
	err = session.Run("lsvdisk -delim ,")
	if err != nil {
		return fmt.Errorf("failed to run lsvdisk: %v, stderr: %s", err, lsvdiskStderr.String())
	}

	// If verbose, print raw lsvdisk output and exit
	if cfg.Verbose {
		fmt.Println("Raw lsvdisk output:")
		fmt.Println(lsvdiskStdout.String())
		return nil
	}

	// Run lshostvdiskmap to get all host-to-volume mappings
	hostMap, err := getHostMappings(client)
	if err != nil {
		return fmt.Errorf("failed to get host mappings: %v", err)
	}

	// Parse lsvdisk output
	volumes, err := parseLsvdiskOutput(lsvdiskStdout.String(), hostMap)
	if err != nil {
		return fmt.Errorf("failed to parse lsvdisk output: %v", err)
	}

	// Output results
	if len(volumes) == 0 {
		fmt.Println("No volumes found on Storage.")
		return nil
	}

	if cfg.Long {
		// Detailed format with all fields
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tName\tCapacity\tPool Name\tStatus\tVolume Type\tWWN\tHost Name")
		fmt.Fprintln(w, "--------------------------------------------------------------------------------")
		for _, vol := range volumes {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				vol.ID, vol.Name, vol.Capacity, vol.PoolName, vol.Status, vol.VolumeType, vol.WWN, vol.HostName)
		}
		w.Flush()
	} else {
		// Compact format with Name, PoolName, WWN, HostName
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Name\tPool Name\tWWN\tHost Name")
		fmt.Fprintln(w, "--------------------------------------------")
		for _, vol := range volumes {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				vol.Name, vol.PoolName, vol.WWN, vol.HostName)
		}
		w.Flush()
	}

	return nil
}

// getHostMappings runs lshostvdiskmap -delim , and returns a map of volume names to host names
func getHostMappings(client *ssh.Client) (map[string]string, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH session: %v", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	log.Println("Executing command: lshostvdiskmap -delim ,")
	err = session.Run("lshostvdiskmap -delim ,")
	if err != nil {
		if strings.Contains(stderr.String(), "No host mappings found") || stdout.String() == "" {
			return make(map[string]string), nil // No mappings exist
		}
		return nil, fmt.Errorf("failed to run lshostvdiskmap: %v, stderr: %s", err, stderr.String())
	}

	hostMap := make(map[string]string)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "id,") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 5 {
			log.Printf("Skipping malformed lshostvdiskmap line: %s", line)
			continue
		}
		volumeName := fields[4] // vdisk_name
		hostName := fields[1]   // name (host_name)
		// Use first host mapping
		if _, exists := hostMap[volumeName]; !exists {
			hostMap[volumeName] = hostName
		}
	}
	return hostMap, nil
}

// parseLsvdiskOutput parses the lsvdisk CSV output into a slice of Volume structs
func parseLsvdiskOutput(output string, hostMap map[string]string) ([]Volume, error) {
	var volumes []Volume
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "id,") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 14 {
			log.Printf("Skipping malformed line (insufficient fields): %s", line)
			continue
		}
		volumeName := fields[1]
		hostName, exists := hostMap[volumeName]
		if !exists {
			hostName = "None"
		}
		volume := Volume{
			ID:         fields[0],  // id
			Name:       fields[1],  // name
			Status:     fields[4],  // status
			Capacity:   fields[7],  // capacity
			PoolName:   fields[6],  // mdisk_grp_name
			VolumeType: fields[8],  // volume_type
			WWN:        fields[13], // vdisk_UID
			HostName:   hostName,
		}
		volumes = append(volumes, volume)
	}
	if len(volumes) == 0 {
		return nil, fmt.Errorf("no volumes found in lsvdisk output")
	}
	return volumes, nil
}
