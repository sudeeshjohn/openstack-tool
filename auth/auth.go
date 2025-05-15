package auth

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Client holds OpenStack service clients
type Client struct {
	Identity *gophercloud.ServiceClient
	Compute  *gophercloud.ServiceClient
}

// Config holds authentication configuration
type Config struct {
	Region  string
	Timeout time.Duration
	Verbose bool
}

// DefaultTimeout is the default context timeout
const DefaultTimeout = 120 * time.Second

// Logger for structured logging
var log = logrus.New()

// NewClient initializes OpenStack clients with authentication
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Region == "" {
		cfg.Region = os.Getenv("OS_REGION_NAME")
		if cfg.Region == "" {
			cfg.Region = "RegionOne"
			log.Debug("OS_REGION_NAME not set, defaulting to RegionOne")
		}
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

	log.SetOutput(os.Stdout)
	if cfg.Verbose {
		log.SetLevel(logrus.DebugLevel)
	} else {
		log.SetLevel(logrus.InfoLevel)
	}

	// Validate required environment variables
	requiredEnv := []string{"OS_AUTH_URL", "OS_USERNAME", "OS_PASSWORD", "OS_PROJECT_NAME", "OS_DOMAIN_NAME"}
	for _, env := range requiredEnv {
		if os.Getenv(env) == "" {
			return nil, fmt.Errorf("missing required environment variable: %s", env)
		}
	}

	// Load auth options
	ao, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		return nil, errors.Wrap(err, "failed to load auth options from environment")
	}
	log.Debugf("Auth options loaded: IdentityEndpoint=%s", ao.IdentityEndpoint)

	// Authenticate
	provider, err := openstack.AuthenticatedClient(ctx, ao)
	if err != nil {
		return nil, errors.Wrap(err, "authentication failed")
	}
	log.Debug("Authenticated successfully")

	// Initialize service clients
	identity, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{Region: cfg.Region})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Identity V3 client")
	}
	compute, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{Region: cfg.Region})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Compute V2 client")
	}
	log.Debug("OpenStack clients initialized")

	return &Client{
		Identity: identity,
		Compute:  compute,
	}, nil
}
