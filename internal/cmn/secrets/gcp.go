// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"regexp"
	"slices"
	"strings"
	"sync"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const gcpSecretManagerProvider = "gcp"

var gcpLocationPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

func init() {
	registerResolver(gcpSecretManagerProvider, func(_ []string) Resolver {
		return &gcpSecretManagerResolver{}
	})
}

type gcpSecretManagerResolver struct {
	mu            sync.Mutex
	clientFactory func(context.Context, string) (gcpSecretManagerClient, error)
	clients       map[string]gcpSecretManagerClient
}

type gcpSecretReference struct {
	resource string
	location string
}

func (r *gcpSecretManagerResolver) Name() string {
	return gcpSecretManagerProvider
}

func (r *gcpSecretManagerResolver) Validate(ref core.SecretRef) error {
	_, err := parseGCPSecretReference(ref, "", "", true)
	return err
}

func (r *gcpSecretManagerResolver) CheckCapability(core.SecretRef) CheckCapability {
	return CheckCapabilityRequiresValueRead
}

func (r *gcpSecretManagerResolver) Resolve(ctx context.Context, ref core.SecretRef) (string, error) {
	cfg := config.GetConfig(ctx).Secrets.GCP
	parsed, err := parseGCPSecretReference(ref, cfg.ProjectID, cfg.Location, false)
	if err != nil {
		return "", err
	}
	client, err := r.getClient(ctx, parsed.location)
	if err != nil {
		return "", err
	}
	response, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: parsed.resource})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", fmt.Errorf("GCP Secret Manager secret %q was not found: %w", parsed.resource, err)
		}
		return "", fmt.Errorf("failed to read GCP Secret Manager secret %q: %w", parsed.resource, err)
	}
	if response == nil || response.Payload == nil {
		return "", fmt.Errorf("GCP Secret Manager secret %q has no payload", parsed.resource)
	}
	if checksum := response.Payload.DataCrc32C; checksum != nil {
		actual := crc32.Checksum(response.Payload.Data, crc32.MakeTable(crc32.Castagnoli))
		if int64(actual) != *checksum {
			return "", fmt.Errorf("GCP Secret Manager secret %q failed CRC32C verification", parsed.resource)
		}
	}
	return selectJSONField(string(response.Payload.Data), ref.Options["field"])
}

func (r *gcpSecretManagerResolver) CheckAccessibility(ctx context.Context, ref core.SecretRef) error {
	_, err := r.Resolve(ctx, ref)
	return err
}

func (r *gcpSecretManagerResolver) getClient(ctx context.Context, location string) (gcpSecretManagerClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if client := r.clients[location]; client != nil {
		return client, nil
	}
	factory := r.clientFactory
	if factory == nil {
		factory = newGCPSecretManagerClient
	}
	client, err := factory(ctx, location)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP Secret Manager client: %w", err)
	}
	if r.clients == nil {
		r.clients = make(map[string]gcpSecretManagerClient)
	}
	r.clients[location] = client
	return client, nil
}

func (r *gcpSecretManagerResolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for _, client := range r.clients {
		errs = append(errs, client.Close())
	}
	r.clients = nil
	return errors.Join(errs...)
}

func parseGCPSecretReference(ref core.SecretRef, defaultProject, defaultLocation string, allowMissingProject bool) (gcpSecretReference, error) {
	key := strings.TrimSpace(ref.Key)
	if key == "" {
		return gcpSecretReference{}, fmt.Errorf("key (GCP Secret Manager secret ID or resource name) is required")
	}
	if strings.HasPrefix(key, "projects/") {
		return parseGCPResourceName(key, ref.Options)
	}
	if strings.Contains(key, "/") {
		return gcpSecretReference{}, fmt.Errorf("GCP Secret Manager secret ID must not contain slashes")
	}

	project := strings.TrimSpace(ref.Options["project_id"])
	if project == "" {
		project = strings.TrimSpace(defaultProject)
	}
	location := strings.TrimSpace(ref.Options["location"])
	if location == "" {
		location = strings.TrimSpace(defaultLocation)
	}
	version := strings.TrimSpace(ref.Options["version"])
	if version == "" {
		version = "latest"
	}
	if err := validateGCPResourceParts(project, location, key, version); err != nil {
		return gcpSecretReference{}, err
	}
	if project == "" {
		if allowMissingProject {
			return gcpSecretReference{location: location}, nil
		}
		return gcpSecretReference{}, fmt.Errorf("project ID is required for GCP Secret Manager secret %q", key)
	}
	if location != "" {
		return gcpSecretReference{
			resource: fmt.Sprintf("projects/%s/locations/%s/secrets/%s/versions/%s", project, location, key, version),
			location: location,
		}, nil
	}
	return gcpSecretReference{resource: fmt.Sprintf("projects/%s/secrets/%s/versions/%s", project, key, version)}, nil
}

func parseGCPResourceName(key string, options map[string]string) (gcpSecretReference, error) {
	parts := strings.Split(key, "/")
	location := ""
	versionResource := false
	switch {
	case len(parts) == 4 && parts[0] == "projects" && parts[2] == "secrets":
	case len(parts) == 6 && parts[0] == "projects" && parts[2] == "secrets" && parts[4] == "versions":
		versionResource = true
	case len(parts) == 6 && parts[0] == "projects" && parts[2] == "locations" && parts[4] == "secrets":
		location = parts[3]
	case len(parts) == 8 && parts[0] == "projects" && parts[2] == "locations" && parts[4] == "secrets" && parts[6] == "versions":
		location = parts[3]
		versionResource = true
	default:
		return gcpSecretReference{}, fmt.Errorf("invalid GCP Secret Manager resource name")
	}
	if slices.Contains(parts, "") {
		return gcpSecretReference{}, fmt.Errorf("invalid GCP Secret Manager resource name")
	}
	if location != "" && !gcpLocationPattern.MatchString(location) {
		return gcpSecretReference{}, fmt.Errorf("GCP Secret Manager location contains invalid characters")
	}
	projectOption := strings.TrimSpace(options["project_id"])
	locationOption := strings.TrimSpace(options["location"])
	if projectOption != "" || locationOption != "" {
		return gcpSecretReference{}, fmt.Errorf("project_id and location options cannot be used with a GCP Secret Manager resource name")
	}
	if versionResource {
		if strings.TrimSpace(options["version"]) != "" {
			return gcpSecretReference{}, fmt.Errorf("options.version conflicts with the GCP Secret Manager version resource name")
		}
		return gcpSecretReference{resource: key, location: location}, nil
	}
	version := strings.TrimSpace(options["version"])
	if version == "" {
		version = "latest"
	}
	if strings.Contains(version, "/") {
		return gcpSecretReference{}, fmt.Errorf("GCP Secret Manager version must not contain slashes")
	}
	return gcpSecretReference{resource: key + "/versions/" + version, location: location}, nil
}

func validateGCPResourceParts(project, location, secret, version string) error {
	parts := []struct {
		label string
		value string
	}{
		{label: "project ID", value: project},
		{label: "location", value: location},
		{label: "secret ID", value: secret},
		{label: "version", value: version},
	}
	for _, part := range parts {
		if strings.Contains(part.value, "/") {
			return fmt.Errorf("GCP Secret Manager %s must not contain slashes", part.label)
		}
	}
	if location != "" && !gcpLocationPattern.MatchString(location) {
		return fmt.Errorf("GCP Secret Manager location contains invalid characters")
	}
	return nil
}

type gcpSecretManagerClient interface {
	AccessSecretVersion(context.Context, *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error)
	Close() error
}

type gcpSDKSecretManagerClient struct {
	client *secretmanager.Client
}

func newGCPSecretManagerClient(ctx context.Context, location string) (gcpSecretManagerClient, error) {
	var options []option.ClientOption
	if location != "" {
		endpoint := fmt.Sprintf("secretmanager.%s.rep.googleapis.com:443", location)
		options = append(options, option.WithEndpoint(endpoint))
	}
	client, err := secretmanager.NewClient(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &gcpSDKSecretManagerClient{client: client}, nil
}

func (c *gcpSDKSecretManagerClient) AccessSecretVersion(ctx context.Context, request *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	return c.client.AccessSecretVersion(ctx, request)
}

func (c *gcpSDKSecretManagerClient) Close() error {
	return c.client.Close()
}
