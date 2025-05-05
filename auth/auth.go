package auth

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Client struct {
	Identity *gophercloud.ServiceClient
	Compute  *gophercloud.ServiceClient
	Provider *gophercloud.ProviderClient
	Image    *gophercloud.ServiceClient // Added for image client
}

type Config struct {
	Region  string
	Timeout time.Duration
	Verbose bool
}

const DefaultTimeout = 120 * time.Second

var log = logrus.New()

func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	log.SetOutput(os.Stdout)
	if cfg.Verbose {
		log.SetLevel(logrus.DebugLevel)
	} else {
		log.SetLevel(logrus.InfoLevel)
	}

	log.Debugf("Initializing new OpenStack client with config: Region=%s, Timeout=%v, Verbose=%v", cfg.Region, cfg.Timeout, cfg.Verbose)
	if cfg.Region == "" {
		cfg.Region = os.Getenv("OS_REGION_NAME")
		if cfg.Region == "" {
			cfg.Region = "RegionOne"
			log.Debug("OS_REGION_NAME not set, defaulting to RegionOne")
		}
	}

	if cfg.Timeout == 0 {
		if timeoutStr := os.Getenv("OS_TIMEOUT_SECONDS"); timeoutStr != "" {
			if timeout, err := strconv.Atoi(timeoutStr); err == nil && timeout > 0 {
				cfg.Timeout = time.Duration(timeout) * time.Second
			} else {
				log.Warnf("Invalid OS_TIMEOUT_SECONDS value: %s, using default timeout %v", timeoutStr, DefaultTimeout)
				cfg.Timeout = DefaultTimeout
			}
		} else {
			cfg.Timeout = DefaultTimeout
		}
	}

	requiredEnv := []string{"OS_AUTH_URL", "OS_USERNAME", "OS_PASSWORD", "OS_PROJECT_NAME", "OS_DOMAIN_NAME"}
	for _, env := range requiredEnv {
		if os.Getenv(env) == "" {
			log.Debugf("Checking environment variable: %s", env)
			return nil, fmt.Errorf("missing required environment variable: %s", env)
		}
	}

	log.Debug("Loading authentication options from environment")
	ao, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		log.Debugf("Failed to load auth options: %v", err)
		return nil, errors.Wrap(err, "failed to load auth options from environment")
	}
	log.Debugf("Auth options loaded: IdentityEndpoint=%s, DomainName=%s, DomainID=%s", ao.IdentityEndpoint, ao.DomainName, ao.DomainID)

	log.Debug("Attempting client authentication")
	provider, err := openstack.AuthenticatedClient(ctx, ao)
	if err != nil {
		log.Debugf("Authentication failed: %v", err)
		return nil, errors.Wrap(err, "authentication failed")
	}
	log.Debug("Authentication successful")

	log.Debug("Creating Identity V3 client")
	identity, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{Region: cfg.Region})
	if err != nil {
		log.Debugf("Failed to create Identity V3 client: %v", err)
		return nil, errors.Wrap(err, "failed to create Identity V3 client")
	}
	log.Debug("Creating Compute V2 client")
	compute, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{Region: cfg.Region})
	if err != nil {
		log.Debugf("Failed to create Compute V2 client: %v", err)
		return nil, errors.Wrap(err, "failed to create Compute V2 client")
	}
	log.Debug("OpenStack clients initialized successfully")

	return &Client{
		Identity: identity,
		Compute:  compute,
		Provider: provider,
	}, nil
}

func NewBlockStorageV3Client(client *Client) (*gophercloud.ServiceClient, error) {
	log.Debug("Initializing Block Storage V3 client")
	volumeClient, err := openstack.NewBlockStorageV3(client.Provider, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	if err != nil {
		log.Debugf("Failed to create block storage v3 client: %v", err)
		return nil, errors.Wrap(err, "failed to create block storage v3 client")
	}
	log.Debug("Block Storage V3 client initialized successfully")
	return volumeClient, nil
}

func NewComputeV2Client(client *Client) (*gophercloud.ServiceClient, error) {
	log.Debug("Checking or initializing Compute V2 client")
	if client.Compute != nil {
		log.Debug("Returning existing Compute V2 client")
		return client.Compute, nil
	}
	log.Debug("Creating new Compute V2 client")
	compute, err := openstack.NewComputeV2(client.Provider, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	if err != nil {
		log.Debugf("Failed to create compute v2 client: %v", err)
		return nil, errors.Wrap(err, "failed to create compute v2 client")
	}
	client.Compute = compute
	log.Debug("Compute V2 client initialized successfully")
	return compute, nil
}

func NewImageV2(client *Client) (*gophercloud.ServiceClient, error) {
	log.Debug("Checking or initializing Image V2 client")
	if client.Image != nil {
		log.Debug("Returning existing Image V2 client")
		return client.Image, nil
	}
	log.Debug("Creating new Image V2 client")
	image, err := openstack.NewImageV2(client.Provider, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	if err != nil {
		log.Debugf("Failed to create image v2 client: %v", err)
		return nil, errors.Wrap(err, "failed to create image v2 client")
	}
	client.Image = image
	log.Debug("Image V2 client initialized successfully")
	return image, nil
}
