// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractRegistry(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		expected string
	}{
		{
			name:     "DockerHubShortName",
			image:    "alpine:latest",
			expected: "docker.io",
		},
		{
			name:     "DockerHubWithUser",
			image:    "myuser/myimage:v1.0",
			expected: "docker.io",
		},
		{
			name:     "ExplicitDockerHub",
			image:    "docker.io/library/alpine:latest",
			expected: "docker.io",
		},
		{
			name:     "GitHubContainerRegistry",
			image:    "ghcr.io/owner/repo:latest",
			expected: "ghcr.io",
		},
		{
			name:     "GoogleContainerRegistry",
			image:    "gcr.io/project/image:v1",
			expected: "gcr.io",
		},
		{
			name:     "AWSECR",
			image:    "123456789012.dkr.ecr.us-east-1.amazonaws.com/myimage:latest",
			expected: "123456789012.dkr.ecr.us-east-1.amazonaws.com",
		},
		{
			name:     "PrivateRegistryWithPort",
			image:    "myregistry.com:5000/myimage:latest",
			expected: "myregistry.com:5000",
		},
		{
			name:     "LocalhostRegistry",
			image:    "localhost:5000/myimage",
			expected: "localhost:5000",
		},
		{
			name:     "ImageWithDigest",
			image:    "myregistry.com/myimage@sha256:abc123",
			expected: "myregistry.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRegistry(tt.image)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertToDockerAuth(t *testing.T) {
	tests := []struct {
		name          string
		auth          *core.AuthConfig
		serverAddress string
		expected      *registry.AuthConfig
		expectError   bool
	}{
		{
			name: "UsernameAndPassword",
			auth: &core.AuthConfig{
				Username: "user",
				Password: "pass",
			},
			serverAddress: "docker.io",
			expected: &registry.AuthConfig{
				Username:      "user",
				Password:      "pass",
				ServerAddress: "docker.io",
			},
		},
		{
			name: "PreEncodedAuth",
			auth: &core.AuthConfig{
				Auth: base64.StdEncoding.EncodeToString([]byte("user:pass")),
			},
			serverAddress: "ghcr.io",
			expected: &registry.AuthConfig{
				Auth:          base64.StdEncoding.EncodeToString([]byte("user:pass")),
				ServerAddress: "ghcr.io",
			},
		},
		{
			name: "JSONStringAuth",
			auth: &core.AuthConfig{
				Auth: `{"username":"user","password":"pass"}`,
			},
			serverAddress: "gcr.io",
			expected: &registry.AuthConfig{
				Username:      "user",
				Password:      "pass",
				ServerAddress: "gcr.io",
			},
		},
		{
			name:          "NilAuth",
			auth:          nil,
			serverAddress: "docker.io",
			expected:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertToDockerAuth(tt.auth, tt.serverAddress)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetAuthFromDockerConfig(t *testing.T) {
	dockerConfig := `{
		"auths": {
			"docker.io": {
				"auth": "dXNlcjpwYXNz"
			},
			"ghcr.io": {
				"username": "github_user",
				"password": "github_token"
			},
			"myregistry.com:5000": {
				"auth": "bXl1c2VyOm15cGFzcw=="
			}
		}
	}`

	tests := []struct {
		name      string
		imageName string
		hasAuth   bool
		checkAuth func(*testing.T, *registry.AuthConfig)
	}{
		{
			name:      "DockerHubImage",
			imageName: "alpine:latest",
			hasAuth:   true,
			checkAuth: func(t *testing.T, auth *registry.AuthConfig) {
				assert.Equal(t, "dXNlcjpwYXNz", auth.Auth)
				assert.Equal(t, "docker.io", auth.ServerAddress)
			},
		},
		{
			name:      "GitHubContainerRegistry",
			imageName: "ghcr.io/owner/repo:latest",
			hasAuth:   true,
			checkAuth: func(t *testing.T, auth *registry.AuthConfig) {
				assert.Equal(t, "github_user", auth.Username)
				assert.Equal(t, "github_token", auth.Password)
				assert.Equal(t, "ghcr.io", auth.ServerAddress)
			},
		},
		{
			name:      "PrivateRegistryWithPort",
			imageName: "myregistry.com:5000/myimage:latest",
			hasAuth:   true,
			checkAuth: func(t *testing.T, auth *registry.AuthConfig) {
				assert.Equal(t, "bXl1c2VyOm15cGFzcw==", auth.Auth)
				assert.Equal(t, "myregistry.com:5000", auth.ServerAddress)
			},
		},
		{
			name:      "UnknownRegistry",
			imageName: "unknown.registry.com/image:latest",
			hasAuth:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth, err := getAuthFromDockerConfig(dockerConfig, tt.imageName)
			require.NoError(t, err)

			if tt.hasAuth {
				require.NotNil(t, auth)
				tt.checkAuth(t, auth)
			} else {
				assert.Nil(t, auth)
			}
		})
	}
}

func TestRegistryAuthManager_GetAuthHeader(t *testing.T) {
	// Test with DAG-level auth
	t.Run("DAGLevelAuth", func(t *testing.T) {
		manager := NewRegistryAuthManager(map[string]*core.AuthConfig{
			"docker.io": {
				Username: "user",
				Password: "pass",
			},
		})

		header, err := manager.GetAuthHeader("alpine:latest")
		require.NoError(t, err)
		assert.NotEmpty(t, header)

		// Decode and verify
		decoded, err := authconfig.Decode(header)
		require.NoError(t, err)
		assert.Equal(t, "user", decoded.Username)
		assert.Equal(t, "pass", decoded.Password)
		assert.Equal(t, "docker.io", decoded.ServerAddress)
	})

	// Test with DOCKER_AUTH_CONFIG env var
	t.Run("DOCKERAUTHCONFIGFallback", func(t *testing.T) {
		oldEnv := os.Getenv("DOCKER_AUTH_CONFIG")
		defer func() {
			_ = os.Setenv("DOCKER_AUTH_CONFIG", oldEnv)
		}()

		dockerConfig := `{"auths":{"gcr.io":{"username":"_json_key","password":"key"}}}`
		require.NoError(t, os.Setenv("DOCKER_AUTH_CONFIG", dockerConfig))

		manager := NewRegistryAuthManager(nil)

		header, err := manager.GetAuthHeader("gcr.io/project/image:latest")
		require.NoError(t, err)
		assert.NotEmpty(t, header)

		// Decode and verify
		decoded, err := authconfig.Decode(header)
		require.NoError(t, err)
		assert.Equal(t, "_json_key", decoded.Username)
		assert.Equal(t, "key", decoded.Password)
		assert.Equal(t, "gcr.io", decoded.ServerAddress)
	})

	// Test with _json special entry
	t.Run("JSONConfigInDAG", func(t *testing.T) {
		dockerConfig := `{"auths":{"ghcr.io":{"auth":"dGVzdDp0ZXN0"}}}`

		manager := NewRegistryAuthManager(map[string]*core.AuthConfig{
			"_json": {
				Auth: dockerConfig,
			},
		})

		header, err := manager.GetAuthHeader("ghcr.io/owner/repo:latest")
		require.NoError(t, err)
		assert.NotEmpty(t, header)

		// Decode and verify
		decoded, err := authconfig.Decode(header)
		require.NoError(t, err)
		assert.Equal(t, "dGVzdDp0ZXN0", decoded.Auth)
		assert.Equal(t, "ghcr.io", decoded.ServerAddress)
	})

	// Test with no auth configured
	t.Run("NoAuth", func(t *testing.T) {
		oldEnv := os.Getenv("DOCKER_AUTH_CONFIG")
		defer func() {
			_ = os.Setenv("DOCKER_AUTH_CONFIG", oldEnv)
		}()
		require.NoError(t, os.Unsetenv("DOCKER_AUTH_CONFIG"))

		manager := NewRegistryAuthManager(nil)

		header, err := manager.GetAuthHeader("alpine:latest")
		require.NoError(t, err)
		assert.Empty(t, header)
	})
}

func TestEncodeBasicAuth(t *testing.T) {
	result := EncodeBasicAuth("user", "pass")
	expected := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	assert.Equal(t, expected, result)

	// Verify it can be decoded
	decoded, err := base64.StdEncoding.DecodeString(result)
	require.NoError(t, err)
	assert.Equal(t, "user:pass", string(decoded))
}

func TestRegistryAuthManager_GetPullOptions(t *testing.T) {
	manager := NewRegistryAuthManager(map[string]*core.AuthConfig{
		"docker.io": {
			Username: "user",
			Password: "pass",
		},
	})

	opts, err := manager.GetPullOptions("alpine:latest", specs.Platform{OS: "linux", Architecture: "amd64"})
	require.NoError(t, err)
	assert.Equal(t, client.ImagePullOptions{
		Platforms:    []specs.Platform{{OS: "linux", Architecture: "amd64"}},
		RegistryAuth: opts.RegistryAuth,
	}, opts)
	assert.NotEmpty(t, opts.RegistryAuth)

	// Verify the auth header is valid
	decoded, err := authconfig.Decode(opts.RegistryAuth)
	require.NoError(t, err)
	assert.Equal(t, "user", decoded.Username)
	assert.Equal(t, "pass", decoded.Password)
}

func TestInvalidDockerAuthConfig(t *testing.T) {
	_, err := getAuthFromDockerConfig("invalid json", "alpine:latest")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid DOCKER_AUTH_CONFIG format")
}

func TestRegistryAuthManager_ComplexJSONAuth(t *testing.T) {
	// Test with a complex auth configuration as JSON string
	authJSON := map[string]any{
		"username": "myuser",
		"password": "mypass",
		"email":    "user@example.com", // Should be ignored
	}
	authBytes, err := json.Marshal(authJSON)
	require.NoError(t, err)

	manager := NewRegistryAuthManager(map[string]*core.AuthConfig{
		"myregistry.com": {
			Auth: string(authBytes),
		},
	})

	header, err := manager.GetAuthHeader("myregistry.com/myimage:latest")
	require.NoError(t, err)
	assert.NotEmpty(t, header)

	// Decode and verify
	decoded, err := authconfig.Decode(header)
	require.NoError(t, err)
	assert.Equal(t, "myuser", decoded.Username)
	assert.Equal(t, "mypass", decoded.Password)
	assert.Equal(t, "myregistry.com", decoded.ServerAddress)
}
