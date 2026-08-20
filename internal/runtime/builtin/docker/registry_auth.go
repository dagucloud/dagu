// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// RegistryAuthManager handles authentication for container registries
type RegistryAuthManager struct {
	auths map[string]*ir.AuthConfig
}

// NewRegistryAuthManager creates a new registry authentication manager
func NewRegistryAuthManager(auths map[string]*ir.AuthConfig) *RegistryAuthManager {
	return &RegistryAuthManager{
		auths: auths,
	}
}

// GetPullOptions returns ImagePullOptions with authentication for the given image.
func (r *RegistryAuthManager) GetPullOptions(imageName string, platform specs.Platform) (client.ImagePullOptions, error) {
	opts := client.ImagePullOptions{}
	if platform.OS != "" {
		opts.Platforms = []specs.Platform{platform}
	}

	authHeader, err := r.GetAuthHeader(imageName)
	if err != nil {
		return opts, fmt.Errorf("failed to get auth header: %w", err)
	}

	if authHeader != "" {
		opts.RegistryAuth = authHeader
	}

	return opts, nil
}

// GetAuthHeader returns the X-Registry-Auth header value for the given image
func (r *RegistryAuthManager) GetAuthHeader(imageName string) (string, error) {
	// First check if we have DAG-level auth
	authConfig, err := r.getAuthConfig(imageName)
	if err != nil {
		return "", err
	}

	if authConfig != nil {
		return authconfig.Encode(*authConfig)
	}

	// Fall back to DOCKER_AUTH_CONFIG environment variable
	dockerAuthConfig := os.Getenv("DOCKER_AUTH_CONFIG")
	if dockerAuthConfig != "" {
		authConfig, err := getAuthFromDockerConfig(dockerAuthConfig, imageName)
		if err != nil {
			return "", fmt.Errorf("failed to parse DOCKER_AUTH_CONFIG: %w", err)
		}
		if authConfig != nil {
			return authconfig.Encode(*authConfig)
		}
	}

	// No authentication configured
	return "", nil
}

// getAuthConfig returns the authentication config for the given image
func (r *RegistryAuthManager) getAuthConfig(imageName string) (*registry.AuthConfig, error) {
	if len(r.auths) == 0 {
		return nil, nil
	}

	// Check if we have a special _json entry (entire DOCKER_AUTH_CONFIG as JSON)
	if jsonAuth, ok := r.auths["_json"]; ok && jsonAuth.Auth != "" {
		return getAuthFromDockerConfig(jsonAuth.Auth, imageName)
	}

	// Extract registry from image name
	registryHost := extractRegistry(imageName)

	// Look for exact match
	if authCfg, ok := r.auths[registryHost]; ok {
		return convertToDockerAuth(authCfg, registryHost)
	}

	// No match found
	return nil, nil
}

// convertToDockerAuth converts our AuthConfig to Docker's registry.AuthConfig
func convertToDockerAuth(auth *ir.AuthConfig, serverAddress string) (*registry.AuthConfig, error) {
	if auth == nil {
		return nil, nil
	}

	// Check if this is a JSON string
	if auth.Auth != "" && strings.HasPrefix(auth.Auth, "{") {
		var dockerAuth registry.AuthConfig
		if err := json.Unmarshal([]byte(auth.Auth), &dockerAuth); err != nil {
			// Not JSON, treat as base64 encoded username:password
			dockerAuth.Auth = auth.Auth
			dockerAuth.ServerAddress = serverAddress
			return &dockerAuth, nil
		}
		dockerAuth.ServerAddress = serverAddress
		return &dockerAuth, nil
	}

	dockerAuth := &registry.AuthConfig{
		ServerAddress: serverAddress,
	}

	// If we have username/password, use them
	if auth.Username != "" && auth.Password != "" {
		dockerAuth.Username = auth.Username
		dockerAuth.Password = auth.Password
	} else if auth.Auth != "" {
		// Use pre-encoded auth string
		dockerAuth.Auth = auth.Auth
	}

	return dockerAuth, nil
}

// getAuthFromDockerConfig parses DOCKER_AUTH_CONFIG format and returns auth for the image
func getAuthFromDockerConfig(configJSON string, imageName string) (*registry.AuthConfig, error) {
	var config struct {
		Auths map[string]registry.AuthConfig `json:"auths"`
	}

	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, fmt.Errorf("invalid DOCKER_AUTH_CONFIG format: %w", err)
	}

	registryHost := extractRegistry(imageName)

	// Look for exact match
	if auth, ok := config.Auths[registryHost]; ok {
		auth.ServerAddress = registryHost
		return &auth, nil
	}

	// Try with https:// prefix
	if auth, ok := config.Auths["https://"+registryHost]; ok {
		auth.ServerAddress = registryHost
		return &auth, nil
	}

	// Try without port if present
	if strings.Contains(registryHost, ":") {
		hostWithoutPort, _, _ := strings.Cut(registryHost, ":")
		if auth, ok := config.Auths[hostWithoutPort]; ok {
			auth.ServerAddress = registryHost
			return &auth, nil
		}
	}

	return nil, nil
}

// extractRegistry extracts the registry hostname from an image name
func extractRegistry(imageName string) string {
	// Remove digest if present
	if idx := strings.Index(imageName, "@"); idx != -1 {
		imageName = imageName[:idx]
	}

	// Split by slash to get potential registry
	parts := strings.Split(imageName, "/")

	if len(parts) == 1 {
		// No slash means Docker Hub official image (e.g., "ubuntu")
		return "docker.io"
	}

	// Check if first part looks like a registry
	firstPart := parts[0]

	// Registry if: contains dot (domain), contains colon (port), or is localhost
	if strings.Contains(firstPart, ".") || strings.Contains(firstPart, ":") || firstPart == "localhost" {
		return firstPart
	}

	// Otherwise it's Docker Hub (e.g., "user/image")
	return "docker.io"
}

// EncodeBasicAuth encodes username and password as base64(username:password)
func EncodeBasicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
