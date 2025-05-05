package volume

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/sudeeshjohn/openstack-tool/auth"
)

// Logger for structured logging
var log = logrus.New()

// Run executes the volume management logic
func Run(ctx context.Context, client *auth.Client, verbose bool, outputFormat, subcommand, volumeNames, projectName, status string, long, notAssociated bool) error {
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if verbose {
		log.SetLevel(logrus.DebugLevel)
	}

	// Initialize block storage client
	volumeClient, err := auth.NewBlockStorageV3Client(client)
	if err != nil {
		return errors.Wrap(err, "failed to initialize block storage client")
	}

	switch subcommand {
	case "list":
		// Use projectName from flag or OS_PROJECT_NAME
		if projectName == "" {
			projectName = os.Getenv("OS_PROJECT_NAME")
		}
		return listVolumes(ctx, client, volumeClient, projectName, outputFormat, long, notAssociated)
	case "list-all":
		return listAllVolumes(ctx, volumeClient, client, outputFormat, long, notAssociated)
	case "change-status":
		return changeVolumeStatus(ctx, client, volumeClient, volumeNames, projectName, status)
	case "delete":
		return deleteVolumes(ctx, client, volumeClient, volumeNames, projectName)
	default:
		return fmt.Errorf("unsupported subcommand: %s", subcommand)
	}
}

// VolumeDetails holds the output data for a volume
type VolumeDetails struct {
	Name        string
	Status      string
	Size        int
	VolumeType  string
	ProjectName string
	AttachedTo  string
	WWN         string
	ImageName   string
}

// processVolumes processes volumes concurrently and assigns image names
func processVolumes(ctx context.Context, authClient *auth.Client, volumeClient, imageClient *gophercloud.ServiceClient, volumeList []volumes.Volume, projectName string, projectNameCache map[string]string, serverNameCache *sync.Map) []VolumeDetails {
	var wg sync.WaitGroup
	volumeDetailsChan := make(chan VolumeDetails, len(volumeList))
	imageCache := sync.Map{} // Cache image data

	for _, vol := range volumeList {
		wg.Add(1)
		go func(vol volumes.Volume) {
			defer wg.Done()
			detail := VolumeDetails{
				Name:       vol.Name,
				Status:     vol.Status,
				Size:       vol.Size,
				VolumeType: vol.VolumeType,
				WWN:        vol.Metadata["volume_wwn"],
			}

			// Assign project name
			if projectName != "" {
				detail.ProjectName = projectName
			} else if projectNameCache != nil {
				if name, exists := projectNameCache[vol.TenantID]; exists {
					detail.ProjectName = name
				} else {
					detail.ProjectName = "Unknown"
				}
			} else {
				detail.ProjectName = "Unknown"
			}

			// Format Attached to
			var attachedTo []string
			for _, attachment := range vol.Attachments {
				serverName, err := getServerName(ctx, authClient, attachment.ServerID, serverNameCache)
				if err != nil || serverName == "" {
					continue
				}
				attachedTo = append(attachedTo, serverName)
			}
			detail.AttachedTo = strings.Join(attachedTo, ", ")

			// Get image name
			if imageClient != nil {
				imageName, err := getAssociatedImageName(ctx, imageClient, vol.ID, &imageCache)
				if err != nil {
					log.Warnf("Failed to get image for volume %s: %v", vol.ID, err)
				}
				detail.ImageName = imageName
			} else {
				detail.ImageName = "N/A"
			}

			volumeDetailsChan <- detail
		}(vol)
	}

	// Close channel when all goroutines are done
	go func() {
		wg.Wait()
		close(volumeDetailsChan)
	}()

	// Collect results
	var volumeDetails []VolumeDetails
	for detail := range volumeDetailsChan {
		volumeDetails = append(volumeDetails, detail)
	}
	return volumeDetails
}

// getServerName retrieves server name from cache or API
func getServerName(ctx context.Context, authClient *auth.Client, serverID string, serverNameCache *sync.Map) (string, error) {
	if serverID == "" {
		return "", nil
	}
	if cached, exists := serverNameCache.Load(serverID); exists {
		log.Debugf("Cache hit for server %s", serverID)
		return cached.(string), nil
	}
	computeClient, err := auth.NewComputeV2Client(authClient)
	if err != nil {
		log.Warnf("Failed to initialize compute client: %v", err)
		return serverID, nil // Fallback to server ID
	}
	server, err := servers.Get(ctx, computeClient, serverID).Extract()
	if err != nil {
		log.Warnf("Failed to get server name for ID %s: %v", serverID, err)
		return serverID, nil // Fallback to server ID
	}
	serverNameCache.Store(serverID, server.Name)
	log.Debugf("Cached server name %s for ID %s", server.Name, serverID)
	return server.Name, nil
}

// getAssociatedImageName finds the image associated with a volume by checking image block_device_mapping
func getAssociatedImageName(ctx context.Context, imageClient *gophercloud.ServiceClient, volumeID string, imageCache *sync.Map) (string, error) {
	if cached, exists := imageCache.Load(volumeID); exists {
		log.Debugf("Cache hit for image associated with volume %s", volumeID)
		return cached.(string), nil
	}

	// List all images
	var imageList []images.Image
	err := images.List(imageClient, images.ListOpts{}).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		imgs, err := images.ExtractImages(page)
		if err != nil {
			return false, err
		}
		imageList = append(imageList, imgs...)
		return true, nil
	})
	if err != nil {
		return "N/A", errors.Wrap(err, "failed to list images")
	}

	// Check each image's block_device_mapping for the volume ID
	for _, img := range imageList {
		bdmStr, exists := img.Properties["block_device_mapping"]
		if !exists {
			continue
		}
		var bdm []map[string]interface{}
		if err := json.Unmarshal([]byte(bdmStr.(string)), &bdm); err != nil {
			log.Warnf("Failed to parse block_device_mapping for image %s: %v", img.ID, err)
			continue
		}
		for _, mapping := range bdm {
			if volID, ok := mapping["volume_id"].(string); ok && volID == volumeID {
				imageCache.Store(volumeID, img.Name)
				log.Debugf("Cached image name %s for volume %s", img.Name, volumeID)
				return img.Name, nil
			}
		}
	}

	imageCache.Store(volumeID, "N/A")
	return "N/A", nil
}

func listVolumes(ctx context.Context, authClient *auth.Client, volumeClient *gophercloud.ServiceClient, projectName, outputFormat string, long, notAssociated bool) error {
	if projectName == "" {
		return fmt.Errorf("project name must be provided via --project or OS_PROJECT_NAME")
	}

	// Get project ID
	projectID, err := getProjectID(ctx, authClient, projectName)
	if err != nil {
		return err
	}

	// Initialize image client (only needed if long=true, JSON output, or notAssociated=true)
	var imageClient *gophercloud.ServiceClient
	if long || strings.ToLower(outputFormat) == "json" || notAssociated {
		imageClient, err = auth.NewImageV2(authClient)
		if err != nil {
			log.Warnf("Failed to initialize image client: %v, proceeding without image names", err)
		}
	}

	// List volumes for the specific project
	listOpts := volumes.ListOpts{
		TenantID: projectID,
	}
	var projectVolumes []volumes.Volume
	err = volumes.List(volumeClient, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		volumeList, err := volumes.ExtractVolumes(page)
		if err != nil {
			return false, err
		}
		projectVolumes = append(projectVolumes, volumeList...)
		return true, nil
	})
	if err != nil {
		return errors.Wrapf(err, "failed to list volumes for project %s", projectName)
	}

	// Cache server names
	serverNameCache := sync.Map{}

	// Process volumes concurrently
	volumeDetails := processVolumes(ctx, authClient, volumeClient, imageClient, projectVolumes, projectName, nil, &serverNameCache)

	// Filter for unassociated volumes if requested
	if notAssociated {
		var filteredDetails []VolumeDetails
		for _, detail := range volumeDetails {
			if detail.ImageName == "N/A" && detail.AttachedTo == "" {
				filteredDetails = append(filteredDetails, detail)
			}
		}
		volumeDetails = filteredDetails
	}

	// Define output structs
	type volumeOutputStandard struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		Size        int    `json:"size"`
		VolumeType  string `json:"volume_type"`
		ProjectName string `json:"project_name"`
		ImageName   string `json:"image_name"`
	}
	type volumeOutputLong struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		Size        int    `json:"size"`
		VolumeType  string `json:"volume_type"`
		ProjectName string `json:"project_name"`
		AttachedTo  string `json:"attached_to"`
		WWN         string `json:"wwn"`
		ImageName   string `json:"image_name"`
	}

	var outputStandard []volumeOutputStandard
	var outputLong []volumeOutputLong

	for _, detail := range volumeDetails {
		if long {
			outputLong = append(outputLong, volumeOutputLong{
				Name:        detail.Name,
				Status:      detail.Status,
				Size:        detail.Size,
				VolumeType:  detail.VolumeType,
				ProjectName: detail.ProjectName,
				AttachedTo:  detail.AttachedTo,
				WWN:         detail.WWN,
				ImageName:   detail.ImageName,
			})
		} else {
			outputStandard = append(outputStandard, volumeOutputStandard{
				Name:        detail.Name,
				Status:      detail.Status,
				Size:        detail.Size,
				VolumeType:  detail.VolumeType,
				ProjectName: detail.ProjectName,
				ImageName:   detail.ImageName,
			})
		}
	}

	if strings.ToLower(outputFormat) == "json" {
		if long {
			data, err := json.MarshalIndent(outputLong, "", "  ")
			if err != nil {
				return errors.Wrap(err, "failed to marshal JSON")
			}
			fmt.Println(string(data))
		} else {
			data, err := json.MarshalIndent(outputStandard, "", "  ")
			if err != nil {
				return errors.Wrap(err, "failed to marshal JSON")
			}
			fmt.Println(string(data))
		}
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if long {
			fmt.Fprintln(w, "Name\tStatus\tSize\tVolume Type\tProject Name\tAttached to\tWWN\tImage Name")
			for _, v := range outputLong {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n", v.Name, v.Status, v.Size, v.VolumeType, v.ProjectName, v.AttachedTo, v.WWN, v.ImageName)
			}
		} else {
			fmt.Fprintln(w, "Name\tStatus\tSize\tVolume Type\tProject Name")
			for _, v := range outputStandard {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", v.Name, v.Status, v.Size, v.VolumeType, v.ProjectName)
			}
		}
		w.Flush()
	}
	return nil
}

func listAllVolumes(ctx context.Context, volumeClient *gophercloud.ServiceClient, authClient *auth.Client, outputFormat string, long, notAssociated bool) error {
	// Initialize image client (only needed if long=true, JSON output, or notAssociated=true)
	var imageClient *gophercloud.ServiceClient
	if long || strings.ToLower(outputFormat) == "json" || notAssociated {
		var err error
		imageClient, err = auth.NewImageV2(authClient)
		if err != nil {
			log.Warnf("Failed to initialize image client: %v, proceeding without image names", err)
		}
	}

	// List all volumes with all_tenants=1
	listOpts := volumes.ListOpts{
		AllTenants: true,
	}
	var allVolumes []volumes.Volume
	err := volumes.List(volumeClient, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		volumeList, err := volumes.ExtractVolumes(page)
		if err != nil {
			return false, err
		}
		allVolumes = append(allVolumes, volumeList...)
		return true, nil
	})
	if err != nil {
		return errors.Wrap(err, "failed to list volumes")
	}

	// Cache project names
	projectNameCache := make(map[string]string)
	getProjectName := func(projectID string) (string, error) {
		if name, exists := projectNameCache[projectID]; exists {
			return name, nil
		}
		project, err := projects.Get(ctx, authClient.Identity, projectID).Extract()
		if err != nil {
			return "", errors.Wrapf(err, "failed to get project %s", projectID)
		}
		projectNameCache[projectID] = project.Name
		return project.Name, nil
	}
	for _, vol := range allVolumes {
		if name, err := getProjectName(vol.TenantID); err == nil {
			projectNameCache[vol.TenantID] = name
		} else {
			log.Warnf("Failed to get project name for ID %s: %v", vol.TenantID, err)
		}
	}

	// Cache server names
	serverNameCache := sync.Map{}

	// Process volumes concurrently
	volumeDetails := processVolumes(ctx, authClient, volumeClient, imageClient, allVolumes, "", projectNameCache, &serverNameCache)

	// Filter for unassociated volumes if requested
	if notAssociated {
		var filteredDetails []VolumeDetails
		for _, detail := range volumeDetails {
			if detail.ImageName == "N/A" && detail.AttachedTo == "" {
				filteredDetails = append(filteredDetails, detail)
			}
		}
		volumeDetails = filteredDetails
	}

	// Define output structs
	type volumeOutputStandard struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		Size        int    `json:"size"`
		VolumeType  string `json:"volume_type"`
		ProjectName string `json:"project_name"`
		ImageName   string `json:"image_name"`
	}
	type volumeOutputLong struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		Size        int    `json:"size"`
		VolumeType  string `json:"volume_type"`
		ProjectName string `json:"project_name"`
		AttachedTo  string `json:"attached_to"`
		WWN         string `json:"wwn"`
		ImageName   string `json:"image_name"`
	}

	var outputStandard []volumeOutputStandard
	var outputLong []volumeOutputLong

	for _, detail := range volumeDetails {
		if long {
			outputLong = append(outputLong, volumeOutputLong{
				Name:        detail.Name,
				Status:      detail.Status,
				Size:        detail.Size,
				VolumeType:  detail.VolumeType,
				ProjectName: detail.ProjectName,
				AttachedTo:  detail.AttachedTo,
				WWN:         detail.WWN,
				ImageName:   detail.ImageName,
			})
		} else {
			outputStandard = append(outputStandard, volumeOutputStandard{
				Name:        detail.Name,
				Status:      detail.Status,
				Size:        detail.Size,
				VolumeType:  detail.VolumeType,
				ProjectName: detail.ProjectName,
				ImageName:   detail.ImageName,
			})
		}
	}

	if strings.ToLower(outputFormat) == "json" {
		if long {
			data, err := json.MarshalIndent(outputLong, "", "  ")
			if err != nil {
				return errors.Wrap(err, "failed to marshal JSON")
			}
			fmt.Println(string(data))
		} else {
			data, err := json.MarshalIndent(outputStandard, "", "  ")
			if err != nil {
				return errors.Wrap(err, "failed to marshal JSON")
			}
			fmt.Println(string(data))
		}
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if long {
			fmt.Fprintln(w, "Name\tStatus\tSize\tVolume Type\tProject Name\tAttached to\tWWN\tImage Name")
			for _, v := range outputLong {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n", v.Name, v.Status, v.Size, v.VolumeType, v.ProjectName, v.AttachedTo, v.WWN, v.ImageName)
			}
		} else {
			fmt.Fprintln(w, "Name\tStatus\tSize\tVolume Type\tProject Name")
			for _, v := range outputStandard {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", v.Name, v.Status, v.Size, v.VolumeType, v.ProjectName)
			}
		}
		w.Flush()
	}
	return nil
}

func changeVolumeStatus(ctx context.Context, authClient *auth.Client, volumeClient *gophercloud.ServiceClient, volumeNames, projectName, status string) error {
	// Get project ID
	projectID, err := getProjectID(ctx, authClient, projectName)
	if err != nil {
		return err
	}

	// Split volume names
	volumeNameList := strings.Split(volumeNames, ",")
	for _, volumeName := range volumeNameList {
		volumeName = strings.TrimSpace(volumeName)
		if volumeName == "" {
			continue
		}

		// Find volume by name and project
		listOpts := volumes.ListOpts{
			Name:       volumeName,
			TenantID:   projectID,
			AllTenants: true,
		}
		var volumeList []volumes.Volume
		err = volumes.List(volumeClient, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
			vols, err := volumes.ExtractVolumes(page)
			if err != nil {
				return false, err
			}
			volumeList = append(volumeList, vols...)
			return true, nil
		})
		if err != nil {
			return errors.Wrapf(err, "failed to list volumes for name %s", volumeName)
		}
		if len(volumeList) == 0 {
			log.Warnf("Volume %s not found in project %s", volumeName, projectName)
			continue
		}
		volume := volumeList[0] // Assume first match

		// Reset volume status using os-reset_status action
		resetStatusPayload := map[string]map[string]string{
			"os-reset_status": {
				"status": status,
			},
		}
		payloadBytes, err := json.Marshal(resetStatusPayload)
		if err != nil {
			return errors.Wrapf(err, "failed to marshal os-reset_status payload for volume %s", volumeName)
		}

		// Send POST request to /v3/{project_id}/volumes/{volume_id}/action
		_, err = volumeClient.Post(
			ctx,
			fmt.Sprintf("%s/volumes/%s/action", volumeClient.ServiceURL(), volume.ID),
			bytes.NewReader(payloadBytes),
			nil,
			&gophercloud.RequestOpts{
				OkCodes: []int{202},
			},
		)
		if err != nil {
			log.Warnf("Failed to reset status of volume %s to %s: %v", volumeName, status, err)
			continue
		}
		log.Infof("Reset status of volume %s in project %s to %s", volumeName, projectName, status)
	}
	return nil
}

func deleteVolumes(ctx context.Context, authClient *auth.Client, volumeClient *gophercloud.ServiceClient, volumeNames, projectName string) error {
	// Get project ID
	projectID, err := getProjectID(ctx, authClient, projectName)
	if err != nil {
		return err
	}

	// Split volume names
	volumeNameList := strings.Split(volumeNames, ",")
	for _, volumeName := range volumeNameList {
		volumeName = strings.TrimSpace(volumeName)
		if volumeName == "" {
			continue
		}

		// Find volume by name and project
		listOpts := volumes.ListOpts{
			Name:       volumeName,
			TenantID:   projectID,
			AllTenants: true,
		}
		var volumeList []volumes.Volume
		err = volumes.List(volumeClient, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
			vols, err := volumes.ExtractVolumes(page)
			if err != nil {
				return false, err
			}
			volumeList = append(volumeList, vols...)
			return true, nil
		})
		if err != nil {
			return errors.Wrapf(err, "failed to list volumes for name %s", volumeName)
		}
		if len(volumeList) == 0 {
			log.Warnf("Volume %s not found in project %s", volumeName, projectName)
			continue
		}
		volume := volumeList[0] // Assume first match

		// Delete volume
		err = volumes.Delete(ctx, volumeClient, volume.ID, volumes.DeleteOpts{}).ExtractErr()
		if err != nil {
			log.Warnf("Failed to delete volume %s: %v", volumeName, err)
			continue
		}
		log.Infof("Deleted volume %s in project %s", volumeName, projectName)
	}
	return nil
}

func getProjectID(ctx context.Context, authClient *auth.Client, projectName string) (string, error) {
	log.Debugf("Looking up project ID for name: %s", projectName)

	// Try authenticated project's ID if it matches projectName
	if strings.EqualFold(projectName, os.Getenv("OS_PROJECT_NAME")) {
		ao, err := openstack.AuthOptionsFromEnv()
		if err == nil && ao.TenantName == projectName {
			log.Debugf("Using authenticated project ID for %s: %s", projectName, ao.TenantID)
			return ao.TenantID, nil
		}
	}

	// Initial query with project name filter using Identity client
	listOpts := projects.ListOpts{
		Name: projectName,
	}
	var projectList []projects.Project
	err := projects.List(authClient.Identity, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		projects, err := projects.ExtractProjects(page)
		if err != nil {
			return false, err
		}
		projectList = append(projectList, projects...)
		return true, nil
	})
	if err == nil && len(projectList) > 0 {
		log.Debugf("Found project %s with ID %s in initial query", projectName, projectList[0].ID)
		return projectList[0].ID, nil
	}

	// Log initial query failure
	if err != nil {
		log.Warnf("Initial project query failed for name %s: %v", projectName, err)
	} else {
		log.Warnf("No project found with name %s in initial query", projectName)
	}

	// Fallback: List all projects
	log.Debug("Attempting fallback: listing all projects")
	listOpts = projects.ListOpts{}
	projectList = nil
	err = projects.List(authClient.Identity, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		projects, err := projects.ExtractProjects(page)
		if err != nil {
			return false, err
		}
		projectList = append(projectList, projects...)
		return true, nil
	})
	if err != nil {
		return "", errors.Wrapf(err, "failed to list all projects for name %s", projectName)
	}

	for _, project := range projectList {
		if strings.EqualFold(project.Name, projectName) {
			log.Debugf("Found project %s with ID %s in fallback query", projectName, project.ID)
			return project.ID, nil
		}
	}

	// Fallback: Try default domain explicitly
	log.Debug("Attempting fallback: querying projects in default domain")
	domainClient, err := openstack.NewIdentityV3(authClient.Provider, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	if err == nil {
		listOpts = projects.ListOpts{
			Name:     projectName,
			DomainID: "default",
		}
		projectList = nil
		err = projects.List(domainClient, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
			projects, err := projects.ExtractProjects(page)
			if err != nil {
				return false, err
			}
			projectList = append(projectList, projects...)
			return true, nil
		})
		if err == nil && len(projectList) > 0 {
			log.Debugf("Found project %s with ID %s in default domain", projectName, projectList[0].ID)
			return projectList[0].ID, nil
		}
		if err != nil {
			log.Warnf("Default domain query failed for name %s: %v", projectName, err)
		}
	}

	return "", fmt.Errorf("no project found with name '%s'; verify project exists, name is case-sensitive, and user has permission to list projects in the correct domain", projectName)
}
