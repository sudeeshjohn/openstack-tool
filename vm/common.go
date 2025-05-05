package vm

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Logger for structured logging
var log = logrus.New()

// Config holds configuration parameters for VM operations
type Config struct {
	Verbose        bool
	FilterStr      string // For info subcommand
	OutputFormat   string
	UseFlavorCache bool // For info subcommand
	MaxRetries     int  // For info subcommand
	MaxConcurrency int  // For info subcommand
	Timeout        time.Duration
	VM             string // For manage subcommand
	Project        string // For manage subcommand
	DryRun         bool   // For manage subcommand
	State          string // For set-state action in manage subcommand
}

// filter holds filtering criteria for VMs
type filter struct {
	Host      string
	Email     string
	Status    string
	Project   string
	DaysOp    string
	DaysValue int
}

// FlavorDetails holds flavor information
type FlavorDetails struct {
	Vcpus     int
	Memory    int
	ProcUnits float64
}

// flavorMap holds a thread-safe map of flavor details
type flavorMap struct {
	sync.Mutex
	data map[string]FlavorDetails
}

// UserDetails holds user information
type UserDetails struct {
	ID    string
	Name  string
	Email string
}

// ProjectDetails holds project information
type ProjectDetails struct {
	ID   string
	Name string
}

// Pair represents a key-value pair for output
type Pair struct {
	Key   string
	Value string
}
