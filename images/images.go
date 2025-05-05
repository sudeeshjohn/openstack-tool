package images

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
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/sudeeshjohn/openstack-tool/auth"
)

// Logger for structured logging
var log = logrus.New()

// Config holds configuration parameters for the images module
type Config struct {
	Verbose      bool
	ProjectName  string
	OutputFormat string
	Action       string
	Timeout      time.Duration
	Limit        int  // Limit number of images to fetch
	Long         bool // Show WWN and Size in table output
}

// ImageDetails holds the details of an image for output
type ImageDetails struct {
	Name        string `json:"name"`
	VolumeName  string `json:"volume_name"`
	Size        int    `json:"size"`
	WWN         string `json:"wwn"`
	ProjectName string `json:"project_name"`
}

// Run executes the image management logic
func Run(ctx context.Context, client *auth.Client, cfg Config) error {
	log.Debugf("Starting image management with config: Verbose=%v, ProjectName=%s, OutputFormat=%s, Action=%s, Timeout=%v, Long=%v, Limit=%d",
		cfg.Verbose, cfg.ProjectName, cfg.OutputFormat, cfg.Action, cfg.Timeout, cfg.Long, cfg.Limit)
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if cfg.Verbose {
		log.SetLevel(logrus.DebugLevel)
	}

	// Apply timeout to context
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	// Initialize image service client
	log.Debug("Initializing image service client")
	imageClient, err := newImageClient(client.Provider)
	if err != nil {
		log.Debugf("Failed to initialize image client: %v", err)
		return errors.Wrap(err, "failed to initialize image service client")
	}

	// Validate action
	validActions := []string{"list", "list-all"}
	if !contains(validActions, cfg.Action) {
		log.Debugf("Invalid action detected: %s", cfg.Action)
		return fmt.Errorf("invalid action: %s; valid actions: %v", cfg.Action, validActions)
	}

	switch cfg.Action {
	case "list":
		if cfg.ProjectName == "" {
			cfg.ProjectName = os.Getenv("OS_PROJECT_NAME")
			if cfg.ProjectName == "" {
				log.Debug("Project name not provided and OS_PROJECT_NAME not set")
				return fmt.Errorf("project name must be provided via --project or OS_PROJECT_NAME")
			}
		}
		log.Debugf("Executing list action for project: %s", cfg.ProjectName)
		return listImages(ctx, client, imageClient, cfg.ProjectName, cfg.OutputFormat, cfg.Limit, cfg.Long)
	case "list-all":
		log.Debug("Executing list-all action")
		return listAllImages(ctx, client, imageClient, cfg.OutputFormat, cfg.Limit, cfg.Long)
	default:
		log.Debugf("Unsupported action encountered: %s", cfg.Action)
		return fmt.Errorf("unsupported action: %s", cfg.Action)
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

func newImageClient(provider *gophercloud.ProviderClient) (*gophercloud.ServiceClient, error) {
	log.Debug("Creating new Image V2 client")
	endpointOpts := gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	}
	imageClient, err := openstack.NewImageV2(provider, endpointOpts)
	if err != nil {
		log.Debugf("Failed to create image v2 client: %v", err)
		return nil, errors.Wrap(err, "failed to create image v2 client")
	}
	log.Debug("Image V2 client created successfully")
	return imageClient, nil
}

func getProjectID(ctx context.Context, client *auth.Client, projectName string) (string, error) {
	log.Debugf("Retrieving project ID for project name: %s", projectName)
	listOpts := projects.ListOpts{
		Name: projectName,
	}
	var allProjects []projects.Project
	err := projects.List(client.Identity, listOpts).EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing project list page")
		projectList, err := projects.ExtractProjects(page)
		if err != nil {
			log.Debugf("Failed to extract projects from page: %v", err)
			return false, err
		}
		allProjects = append(allProjects, projectList...)
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to list projects for name %s: %v", projectName, err)
		return "", errors.Wrapf(err, "failed to list projects for name %s", projectName)
	}

	if len(allProjects) == 0 {
		log.Debugf("No project found with name '%s'", projectName)
		return "", fmt.Errorf("no project found with name '%s'", projectName)
	}
	log.Debugf("Found project ID: %s for name %s", allProjects[0].ID, projectName)
	return allProjects[0].ID, nil
}

// fetchProjectNames pre-fetches all project names for a domain
func fetchProjectNames(ctx context.Context, identityClient *gophercloud.ServiceClient) (map[string]string, error) {
	log.Debug("Fetching all project names")
	listOpts := projects.ListOpts{
		DomainID: os.Getenv("OS_DOMAIN_NAME"),
	}
	projectMap := make(map[string]string)
	err := projects.List(identityClient, listOpts).EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing project names page")
		projectList, err := projects.ExtractProjects(page)
		if err != nil {
			log.Debugf("Failed to extract projects: %v", err)
			return false, err
		}
		for _, proj := range projectList {
			log.Debugf("Mapping project ID %s to name %s", proj.ID, proj.Name)
			projectMap[proj.ID] = proj.Name
		}
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to fetch project names: %v", err)
		return nil, errors.Wrap(err, "failed to fetch project names")
	}
	log.Debugf("Fetched %d project names", len(projectMap))
	return projectMap, nil
}

func listImages(ctx context.Context, authClient *auth.Client, imageClient *gophercloud.ServiceClient, projectName, outputFormat string, limit int, long bool) error {
	log.Debugf("Listing images for project: %s, OutputFormat: %s, Limit: %d, Long: %v", projectName, outputFormat, limit, long)
	// Get project ID
	projectID, err := getProjectID(ctx, authClient, projectName)
	if err != nil {
		log.Debugf("Failed to get project ID for %s: %v", projectName, err)
		return err
	}
	log.Debugf("Resolved project ID: %s", projectID)

	// Initialize volume client
	log.Debug("Initializing volume client")
	volumeClient, err := auth.NewBlockStorageV3Client(authClient)
	if err != nil {
		log.Warnf("Failed to initialize volume client: %v, proceeding without volume details", err)
	}

	// List images for the specific project
	log.Debugf("Listing images with opts: Owner=%s, Limit=%d", projectID, limit)
	listOpts := images.ListOpts{
		Owner: projectID,
		Limit: limit,
	}
	var projectImages []images.Image
	err = images.List(imageClient, listOpts).EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing image list page")
		imageList, err := images.ExtractImages(page)
		if err != nil {
			log.Debugf("Failed to extract images from page: %v", err)
			return false, err
		}
		log.Debugf("Extracted %d images from page", len(imageList))
		projectImages = append(projectImages, imageList...)
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to list images for project %s: %v", projectName, err)
		return errors.Wrapf(err, "failed to list images for project %s", projectName)
	}
	log.Debugf("Total images fetched: %d", len(projectImages))

	// Process images concurrently
	log.Debug("Processing images concurrently")
	imageDetails := processImages(ctx, volumeClient, projectImages, projectName, nil)

	// Output results
	if strings.ToLower(outputFormat) == "json" {
		log.Debug("Preparing JSON output")
		data, err := json.MarshalIndent(imageDetails, "", "  ")
		if err != nil {
			log.Debugf("Failed to marshal JSON: %v", err)
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		log.Debug("Preparing table output")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if long {
			fmt.Fprintln(w, "Name\tVolume Name\tSize\tWWN\tProject Name")
		} else {
			fmt.Fprintln(w, "Name\tVolume Name\tProject Name")
		}
		for _, img := range imageDetails {
			volumeName := img.VolumeName
			if volumeName == "" {
				volumeName = "N/A"
			}
			if long {
				wwn := img.WWN
				if wwn == "" {
					wwn = "N/A"
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
					img.Name, volumeName, img.Size, wwn, img.ProjectName)
			} else {
				fmt.Fprintf(w, "%s\t%s\t%s\n",
					img.Name, volumeName, img.ProjectName)
			}
		}
		w.Flush()
	}
	log.Debug("Image listing completed")
	return nil
}

func listAllImages(ctx context.Context, authClient *auth.Client, imageClient *gophercloud.ServiceClient, outputFormat string, limit int, long bool) error {
	log.Debugf("Listing all images with OutputFormat: %s, Limit: %d, Long: %v", outputFormat, limit, long)
	// Initialize volume client
	log.Debug("Initializing volume client for all images")
	volumeClient, err := auth.NewBlockStorageV3Client(authClient)
	if err != nil {
		log.Warnf("Failed to initialize volume client: %v, proceeding without volume details", err)
	}

	// Pre-fetch project names
	log.Debug("Fetching project names")
	projectNames, err := fetchProjectNames(ctx, authClient.Identity)
	if err != nil {
		log.Warnf("Failed to fetch project names: %v, using 'Unknown' as fallback", err)
	}

	// List all images
	log.Debugf("Listing all images with limit: %d", limit)
	listOpts := images.ListOpts{
		Limit: limit,
	}
	var allImages []images.Image
	err = images.List(imageClient, listOpts).EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		log.Debug("Processing all images page")
		imageList, err := images.ExtractImages(page)
		if err != nil {
			log.Debugf("Failed to extract images from page: %v", err)
			return false, err
		}
		log.Debugf("Extracted %d images from page", len(imageList))
		allImages = append(allImages, imageList...)
		return true, nil
	})
	if err != nil {
		log.Debugf("Failed to list all images: %v", err)
		return errors.Wrap(err, "failed to list all images")
	}
	log.Debugf("Total images fetched: %d", len(allImages))

	// Process images concurrently
	log.Debug("Processing all images concurrently")
	imageDetails := processImages(ctx, volumeClient, allImages, "", projectNames)

	// Output results
	if strings.ToLower(outputFormat) == "json" {
		log.Debug("Preparing JSON output for all images")
		data, err := json.MarshalIndent(imageDetails, "", "  ")
		if err != nil {
			log.Debugf("Failed to marshal JSON: %v", err)
			return errors.Wrap(err, "failed to marshal JSON")
		}
		fmt.Println(string(data))
	} else {
		log.Debug("Preparing table output for all images")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if long {
			fmt.Fprintln(w, "Name\tVolume Name\tSize\tWWN\tProject Name")
		} else {
			fmt.Fprintln(w, "Name\tVolume Name\tProject Name")
		}
		for _, img := range imageDetails {
			volumeName := img.VolumeName
			if volumeName == "" {
				volumeName = "N/A"
			}
			if long {
				wwn := img.WWN
				if wwn == "" {
					wwn = "N/A"
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
					img.Name, volumeName, img.Size, wwn, img.ProjectName)
			} else {
				fmt.Fprintf(w, "%s\t%s\t%s\n",
					img.Name, volumeName, img.ProjectName)
			}
		}
		w.Flush()
	}
	log.Debug("All images listing completed")
	return nil
}

// processImages processes images concurrently and assigns project names
func processImages(ctx context.Context, volumeClient *gophercloud.ServiceClient, imageList []images.Image, defaultProjectName string, projectNames map[string]string) []ImageDetails {
	log.Debugf("Processing %d images concurrently", len(imageList))
	var wg sync.WaitGroup
	imageDetailsChan := make(chan ImageDetails, len(imageList))
	volumeCache := sync.Map{} // Cache volume data

	for _, img := range imageList {
		wg.Add(1)
		go func(img images.Image) {
			defer wg.Done()
			log.Debugf("Processing image: %s (ID: %s)", img.Name, img.ID)
			detail := ImageDetails{
				Name: img.Name,
			}

			// Assign project name
			if defaultProjectName != "" {
				log.Debugf("Assigning default project name: %s", defaultProjectName)
				detail.ProjectName = defaultProjectName
			} else if projectNames != nil {
				if name, exists := projectNames[img.Owner]; exists {
					log.Debugf("Mapped owner %s to project name %s", img.Owner, name)
					detail.ProjectName = name
				} else {
					log.Debugf("Owner %s not found in project names, using 'Unknown'", img.Owner)
					detail.ProjectName = "Unknown"
				}
			} else {
				log.Debug("No project names map provided, using 'Unknown'")
				detail.ProjectName = "Unknown"
			}

			// Get volume details
			if volumeClient != nil {
				log.Debugf("Fetching volume details for image %s", img.Name)
				volumeName, volumeWwn, volSize, err := getAssociatedVolumeName(ctx, volumeClient, img, &volumeCache)
				if err != nil {
					log.Warnf("Failed to get volume for image %s: %v", img.Name, err)
				} else if volumeName != "" {
					log.Debugf("Found volume details: Name=%s, WWN=%s, Size=%d", volumeName, volumeWwn, volSize)
					detail.VolumeName = volumeName
					detail.WWN = volumeWwn
					detail.Size = volSize
				}
			}

			log.Debugf("Completed processing image %s", img.Name)
			imageDetailsChan <- detail
		}(img)
	}

	// Close channel when all goroutines are done
	go func() {
		log.Debug("Waiting for all image processing goroutines to complete")
		wg.Wait()
		close(imageDetailsChan)
	}()

	// Collect results
	log.Debug("Collecting processed image details")
	var imageDetails []ImageDetails
	for detail := range imageDetailsChan {
		imageDetails = append(imageDetails, detail)
	}
	log.Debugf("Collected %d image details", len(imageDetails))
	return imageDetails
}

func getAssociatedVolumeName(ctx context.Context, volumeClient *gophercloud.ServiceClient, img images.Image, volumeCache *sync.Map) (string, string, int, error) {
	log.Debugf("Looking for volume associated with image %s (ID: %s)", img.Name, img.ID)
	// Check if block_device_mapping exists
	blockMappingRaw, exists := img.Properties["block_device_mapping"]
	if !exists {
		log.Debugf("No block_device_mapping found for image %s", img.Name)
		return "", "", 0, nil
	}
	blockMappingStr, ok := blockMappingRaw.(string)
	if !ok {
		log.Warnf("block_device_mapping for image %s is not a string: %v", img.Name, blockMappingRaw)
		return "", "", 0, nil
	}

	var volID string
	var blockMappings []map[string]interface{}
	if err := json.Unmarshal([]byte(blockMappingStr), &blockMappings); err != nil {
		log.Warnf("Failed to unmarshal block_device_mapping for image %s: %v", img.Name, err)
		return "", "", 0, nil
	}
	if len(blockMappings) > 0 {
		if id, ok := blockMappings[0]["volume_id"].(string); ok {
			log.Debugf("Found volume ID %s in block_device_mapping", id)
			volID = id
		}
	}

	if volID == "" {
		log.Debugf("No volume_id found in block_device_mapping for image %s", img.Name)
		return "", "", 0, nil
	}

	// Check cache first
	if cached, exists := volumeCache.Load(volID); exists {
		log.Debugf("Cache hit for volume %s for image %s", volID, img.Name)
		if vol, ok := cached.(*volumes.Volume); ok {
			wwn, _ := vol.Metadata["volume_wwn"]
			log.Debugf("Returning cached volume: Name=%s, WWN=%s, Size=%d", vol.Name, wwn, vol.Size)
			return vol.Name, wwn, vol.Size, nil
		}
	}

	// Query the volume by ID
	log.Debugf("Querying volume with ID: %s", volID)
	vol, err := volumes.Get(ctx, volumeClient, volID).Extract()
	if err != nil {
		log.Warnf("Failed to get volume %s for image %s: %v", volID, img.Name, err)
		return "", "", 0, nil
	}

	// Cache the volume
	log.Debugf("Caching volume %s for ID %s", vol.Name, volID)
	volumeCache.Store(volID, vol)
	wwn, ok := vol.Metadata["volume_wwn"]
	if !ok {
		log.Warnf("No volume_wwn found in metadata for volume %s", vol.Name)
		wwn = ""
	}
	log.Debugf("Matched volume %s to image %s via volume_id", vol.Name, img.Name)
	return vol.Name, wwn, vol.Size, nil
}
