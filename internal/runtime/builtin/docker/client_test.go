// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testAbsoluteVolumePath(unixPath string, windowsPath string) string {
	if runtime.GOOS == "windows" {
		return windowsPath
	}
	return unixPath
}

func invalidVolumeTooManyPartsSpec() string {
	if runtime.GOOS == "windows" {
		return "C:/host:/container:ro:extra"
	}
	return "/host:/container:ro:extra"
}

func invalidVolumeTooManyPartsError(spec string) string {
	if runtime.GOOS == "windows" {
		return "invalid mode ro:extra in " + spec
	}
	return "invalid volume format: " + spec
}

func mustPort(s string) network.Port {
	return network.MustParsePort(s)
}

func mustAddr(s string) netip.Addr {
	return netip.MustParseAddr(s)
}

// TestLoadConfigFromMap covers LoadConfigFromMap with 92.7% coverage.
// The uncovered lines (7.3%) are error handling for mapstructure.NewDecoder failures
// which cannot be triggered in practice because we always pass valid struct pointers.
// These error checks exist as defensive programming for potential future changes.
func TestLoadConfigFromMap(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]any
		expected    *Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "MinimalConfigWithImage",
			input: map[string]any{
				"image": "alpine:latest",
			},
			expected: &Config{
				Image:       "alpine:latest",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "MinimalConfigWithContainerName",
			input: map[string]any{
				"container_name": "my-container",
			},
			expected: &Config{
				ContainerName: "my-container",
				Pull:          core.PullPolicyMissing,
				Container:     &container.Config{},
				Host:          &container.HostConfig{},
				Network:       &network.NetworkingConfig{},
				ExecOptions:   &client.ExecCreateOptions{},
			},
		},
		{
			name: "ErrorWhenNeitherImageNorContainerNameProvided",
			input: map[string]any{
				"platform": "linux/amd64",
			},
			expectError: true,
			errorMsg:    "container_name or image must be specified",
		},
		{
			name: "FullConfigForNewContainerNoContainerName",
			input: map[string]any{
				"image":       "ubuntu:20.04",
				"platform":    "linux/arm64",
				"pull":        "always",
				"auto_remove": true,
				"container": map[string]any{
					"Env":        []string{"FOO=bar"},
					"WorkingDir": "/app",
					"User":       "1000",
				},
				"host": map[string]any{
					"AutoRemove": true,
					"Privileged": true,
				},
				"network": map[string]any{
					"EndpointsConfig": map[string]any{},
				},
			},
			expected: &Config{
				Image:      "ubuntu:20.04",
				Platform:   "linux/arm64",
				Pull:       core.PullPolicyAlways,
				AutoRemove: true,
				Container: &container.Config{
					Env:        []string{"FOO=bar"},
					WorkingDir: "/app",
					User:       "1000",
				},
				Host: &container.HostConfig{
					AutoRemove: false, // Should be false because auto_remove is handled separately
					Privileged: true,
				},
				Network: &network.NetworkingConfig{
					EndpointsConfig: map[string]*network.EndpointSettings{},
				},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "ExecModeWithContainerNameAndExecOptions",
			input: map[string]any{
				"container_name": "test-container",
				"exec": map[string]any{
					"User":       "root",
					"WorkingDir": "/tmp",
					"Env":        []string{"BAR=baz"},
				},
			},
			expected: &Config{
				ContainerName: "test-container",
				Pull:          core.PullPolicyMissing,
				Container:     &container.Config{},
				Host:          &container.HostConfig{},
				Network:       &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{
					User:       "root",
					WorkingDir: "/tmp",
					Env:        []string{"BAR=baz"},
				},
			},
		},
		{
			name: "AutoRemoveFromHostConfig",
			input: map[string]any{
				"image": "alpine",
				"host": map[string]any{
					"AutoRemove": true,
				},
			},
			expected: &Config{
				Image:      "alpine",
				Pull:       core.PullPolicyMissing,
				AutoRemove: true,
				Container:  &container.Config{},
				Host: &container.HostConfig{
					AutoRemove: false,
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "AutoRemoveExplicitTrueOverridesHostConfigFalse",
			input: map[string]any{
				"image":       "alpine",
				"auto_remove": true,
				"host": map[string]any{
					"AutoRemove": false,
				},
			},
			expected: &Config{
				Image:      "alpine",
				Pull:       core.PullPolicyMissing,
				AutoRemove: true,
				Container:  &container.Config{},
				Host: &container.HostConfig{
					AutoRemove: false,
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "AutoRemoveStringValueTrue",
			input: map[string]any{
				"image":       "alpine",
				"auto_remove": "true",
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				AutoRemove:  true,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "AutoRemoveStringValueFalse",
			input: map[string]any{
				"image":       "alpine",
				"auto_remove": "false",
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				AutoRemove:  false,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "AutoRemoveStringValue1",
			input: map[string]any{
				"image":       "alpine",
				"auto_remove": "1",
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				AutoRemove:  true,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "AutoRemoveStringValue0",
			input: map[string]any{
				"image":       "alpine",
				"auto_remove": "0",
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				AutoRemove:  false,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "AutoRemoveInvalidValue",
			input: map[string]any{
				"image":       "alpine",
				"auto_remove": "invalid",
			},
			expectError: true,
			errorMsg:    "failed to evaluate auto_remove value",
		},
		{
			name: "AutoRemoveUnsupportedType",
			input: map[string]any{
				"image":       "alpine",
				"auto_remove": 123,
			},
			expectError: true,
			errorMsg:    "failed to evaluate auto_remove value",
		},
		{
			name: "PullPolicyNever",
			input: map[string]any{
				"image": "alpine",
				"pull":  "never",
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyNever,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PullPolicyMissing",
			input: map[string]any{
				"image": "alpine",
				"pull":  "missing",
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PullPolicyAsBooleanTrue",
			input: map[string]any{
				"image": "alpine",
				"pull":  true,
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyAlways,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PullPolicyAsBooleanFalse",
			input: map[string]any{
				"image": "alpine",
				"pull":  false,
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyNever,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PullPolicyAsStringTrue",
			input: map[string]any{
				"image": "alpine",
				"pull":  "true",
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyAlways,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "InvalidPullPolicy",
			input: map[string]any{
				"image": "alpine",
				"pull":  "invalid",
			},
			expectError: true,
			errorMsg:    "failed to parse pull policy as boolean",
		},
		{
			name: "PullPolicyUnsupportedType",
			input: map[string]any{
				"image": "alpine",
				"pull":  123,
			},
			expectError: true,
			errorMsg:    "invalid pull policy type",
		},
		{
			name: "ContainerConfigWithWeaklyTypedInput",
			input: map[string]any{
				"image": "alpine",
				"container": map[string]any{
					"Env": "FOO=bar", // String instead of slice
				},
			},
			expected: &Config{
				Image: "alpine",
				Pull:  core.PullPolicyMissing,
				Container: &container.Config{
					Env: []string{"FOO=bar"},
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "InvalidContainerConfigDecoder",
			input: map[string]any{
				"image":     "alpine",
				"container": "invalid", // Not a map
			},
			expectError: true,
			errorMsg:    "failed to decode config",
		},
		{
			name: "InvalidHostConfigDecoder",
			input: map[string]any{
				"image": "alpine",
				"host":  "invalid", // Not a map
			},
			expectError: true,
			errorMsg:    "failed to decode config",
		},
		{
			name: "InvalidNetworkConfigDecoder",
			input: map[string]any{
				"image":   "alpine",
				"network": "invalid", // Not a map
			},
			expectError: true,
			errorMsg:    "failed to decode config",
		},
		{
			name: "InvalidExecConfigDecoder",
			input: map[string]any{
				"image": "alpine",
				"exec":  "invalid", // Not a map
			},
			expectError: true,
			errorMsg:    "failed to decode config",
		},
		{
			name: "EmptyConfigSections",
			input: map[string]any{
				"image":     "alpine",
				"container": map[string]any{},
				"host":      map[string]any{},
				"network":   map[string]any{},
				"exec":      map[string]any{},
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "BothImageAndContainerNameEmptyStrings",
			input: map[string]any{
				"image":          "",
				"container_name": "",
			},
			expectError: true,
			errorMsg:    "container_name or image must be specified",
		},
		{
			name: "ErrorWhenExecProvidedWithImageOnly",
			input: map[string]any{
				"image": "alpine",
				"exec": map[string]any{
					"User": "root",
				},
			},
			expectError: true,
			errorMsg:    "exec' options require 'container_name",
		},
		{
			name: "PlatformAsNonStringType",
			input: map[string]any{
				"image":    "alpine",
				"platform": 123, // Not a string
			},
			expected: &Config{
				Image:       "alpine",
				Platform:    "123",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "ContainerNameAsNonStringTypeExecMode",
			input: map[string]any{
				"container_name": 123, // Not a string
			},
			expected: &Config{
				ContainerName: "123",
				Pull:          core.PullPolicyMissing,
				Container:     &container.Config{},
				Host:          &container.HostConfig{},
				Network:       &network.NetworkingConfig{},
				ExecOptions:   &client.ExecCreateOptions{},
			},
		},
		{
			name: "ImageAsNonStringType",
			input: map[string]any{
				"image": 123, // Not a string
			},
			expected: &Config{
				Image:       "123",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "NilSectionsAreHandled",
			input: map[string]any{
				"image":     "alpine",
				"container": nil,
				"host":      nil,
				"network":   nil,
				"exec":      nil,
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PullPolicyNil",
			input: map[string]any{
				"image": "alpine",
				"pull":  nil,
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PullPolicyEmptyString",
			input: map[string]any{
				"image": "alpine",
				"pull":  "",
			},
			expected: &Config{
				Image:       "alpine",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "AutoRemoveNilValue",
			input: map[string]any{
				"image":       "alpine",
				"auto_remove": nil,
			},
			expected: &Config{
				Image:       "alpine",
				AutoRemove:  false,
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PlatformNilValue",
			input: map[string]any{
				"image":    "alpine",
				"platform": nil,
			},
			expected: &Config{
				Image:       "alpine",
				Platform:    "",
				Pull:        core.PullPolicyMissing,
				Container:   &container.Config{},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "ContainerNameNilValue",
			input: map[string]any{
				"image":          "alpine",
				"container_name": nil,
			},
			expected: &Config{
				Image:         "alpine",
				ContainerName: "",
				Pull:          core.PullPolicyMissing,
				Container:     &container.Config{},
				Host:          &container.HostConfig{},
				Network:       &network.NetworkingConfig{},
				ExecOptions:   &client.ExecCreateOptions{},
			},
		},
		{
			name: "ImageNilValue",
			input: map[string]any{
				"image":          nil,
				"container_name": "test",
			},
			expected: &Config{
				Image:         "",
				ContainerName: "test",
				Pull:          core.PullPolicyMissing,
				Container:     &container.Config{},
				Host:          &container.HostConfig{},
				Network:       &network.NetworkingConfig{},
				ExecOptions:   &client.ExecCreateOptions{},
			},
		},
		{
			name: "WorkingDirShortcut",
			input: map[string]any{
				"image":       "alpine",
				"working_dir": "/app",
			},
			expected: &Config{
				Image: "alpine",
				Pull:  core.PullPolicyMissing,
				Container: &container.Config{
					WorkingDir: "/app",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "VolumesShortcut",
			input: map[string]any{
				"image":   "alpine",
				"volumes": []string{"/host/path:/container/path", "/data:/data:ro"},
			},
			expected: &Config{
				Image:     "alpine",
				Pull:      core.PullPolicyMissing,
				Container: &container.Config{},
				Host: &container.HostConfig{
					Binds: []string{"/host/path:/container/path", "/data:/data:ro"},
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "WorkingDirAndVolumesShortcuts",
			input: map[string]any{
				"image":       "golang:1.22",
				"working_dir": "/work",
				"volumes":     []string{"$PWD:/work"},
			},
			expected: &Config{
				Image: "golang:1.22",
				Pull:  core.PullPolicyMissing,
				Container: &container.Config{
					WorkingDir: "/work",
				},
				Host: &container.HostConfig{
					Binds: []string{"$PWD:/work"},
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "WorkingDirShortcutDoesNotOverrideContainerWorkingDir",
			input: map[string]any{
				"image":       "alpine",
				"working_dir": "/shortcut",
				"container": map[string]any{
					"WorkingDir": "/explicit",
				},
			},
			expected: &Config{
				Image: "alpine",
				Pull:  core.PullPolicyMissing,
				Container: &container.Config{
					WorkingDir: "/explicit",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "VolumesShortcutAppendsToHostBinds",
			input: map[string]any{
				"image":   "alpine",
				"volumes": []string{"/new:/new"},
				"host": map[string]any{
					"Binds": []string{"/existing:/existing"},
				},
			},
			expected: &Config{
				Image:     "alpine",
				Pull:      core.PullPolicyMissing,
				Container: &container.Config{},
				Host: &container.HostConfig{
					Binds: []string{"/existing:/existing", "/new:/new"},
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := LoadConfigFromMap(tt.input, nil)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected.Image, result.Image)
			assert.Equal(t, tt.expected.ContainerName, result.ContainerName)
			assert.Equal(t, tt.expected.Platform, result.Platform)
			assert.Equal(t, tt.expected.Pull, result.Pull)
			assert.Equal(t, tt.expected.AutoRemove, result.AutoRemove)

			// Compare container config
			assert.Equal(t, tt.expected.Container.Env, result.Container.Env)
			assert.Equal(t, tt.expected.Container.WorkingDir, result.Container.WorkingDir)
			assert.Equal(t, tt.expected.Container.User, result.Container.User)

			// Compare host config
			assert.Equal(t, tt.expected.Host.AutoRemove, result.Host.AutoRemove)
			assert.Equal(t, tt.expected.Host.Privileged, result.Host.Privileged)
			assert.Equal(t, tt.expected.Host.Binds, result.Host.Binds)

			// Compare exec options
			assert.Equal(t, tt.expected.ExecOptions.User, result.ExecOptions.User)
			assert.Equal(t, tt.expected.ExecOptions.WorkingDir, result.ExecOptions.WorkingDir)
			assert.Equal(t, tt.expected.ExecOptions.Env, result.ExecOptions.Env)
		})
	}
}

func TestLoadConfig(t *testing.T) {
	hostDataPath := testAbsoluteVolumePath("/host/data", "C:/host/data")
	hostPath := testAbsoluteVolumePath("/host/path", "C:/host/path")
	invalidVolumeSpec := invalidVolumeTooManyPartsSpec()

	tests := []struct {
		name        string
		input       core.Container
		expected    *Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "MinimalContainerWithImageOnly",
			input: core.Container{
				Image: "alpine:latest",
			},
			expected: &Config{
				Image:      "alpine:latest",
				Pull:       core.PullPolicyAlways, // Zero value of PullPolicy
				AutoRemove: true,                  // Default when KeepContainer is false
				Container: &container.Config{
					Image: "alpine:latest",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "ErrorWhenImageIsEmpty",
			input: core.Container{
				Platform: "linux/amd64",
			},
			expectError: true,
			errorMsg:    "image is required",
		},
		{
			name: "FullContainerConfiguration",
			input: core.Container{
				Image:         "ubuntu:20.04",
				PullPolicy:    core.PullPolicyAlways,
				Env:           []string{"FOO=bar", "BAZ=qux"},
				Volumes:       []string{hostDataPath + ":/data:ro", "myvolume:/app"},
				User:          "1000:1000",
				WorkingDir:    "/workspace",
				Platform:      "linux/arm64",
				Ports:         []string{"8080:80", "9090"},
				Network:       "mynetwork",
				KeepContainer: true,
			},
			expected: &Config{
				Image:      "ubuntu:20.04",
				Platform:   "linux/arm64",
				Pull:       core.PullPolicyAlways,
				AutoRemove: false, // KeepContainer is true
				Container: &container.Config{
					Image:      "ubuntu:20.04",
					Env:        []string{"FOO=bar", "BAZ=qux"},
					User:       "1000:1000",
					WorkingDir: "/workspace",
					ExposedPorts: network.PortSet{
						mustPort("80/tcp"):   {},
						mustPort("9090/tcp"): {},
					},
				},
				Host: &container.HostConfig{
					Binds: []string{hostDataPath + ":/data:ro"},
					Mounts: []mount.Mount{
						{
							Type:     mount.TypeVolume,
							Source:   "myvolume",
							Target:   "/app",
							ReadOnly: false,
						},
					},
					PortBindings: network.PortMap{
						mustPort("80/tcp"): []network.PortBinding{
							{
								HostIP:   mustAddr("0.0.0.0"),
								HostPort: "8080",
							},
						},
					},
					NetworkMode: "mynetwork",
				},
				Network: &network.NetworkingConfig{
					EndpointsConfig: map[string]*network.EndpointSettings{
						"mynetwork": {},
					},
				},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "StandardNetworkModes",
			input: core.Container{
				Image:   "nginx",
				Network: "host",
			},
			expected: &Config{
				Image:      "nginx",
				AutoRemove: true,
				Container: &container.Config{
					Image: "nginx",
				},
				Host: &container.HostConfig{
					NetworkMode: "host",
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "ContainerNetworkReference",
			input: core.Container{
				Image:   "nginx",
				Network: "container:myapp",
			},
			expected: &Config{
				Image:      "nginx",
				AutoRemove: true,
				Container: &container.Config{
					Image: "nginx",
				},
				Host: &container.HostConfig{
					NetworkMode: "container:myapp",
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "BindMountWithDefaultRwMode",
			input: core.Container{
				Image:   "alpine",
				Volumes: []string{hostPath + ":/container/path"},
			},
			expected: &Config{
				Image:      "alpine",
				AutoRemove: true,
				Container: &container.Config{
					Image: "alpine",
				},
				Host: &container.HostConfig{
					Binds: []string{hostPath + ":/container/path:rw"},
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "RelativeBindMount",
			input: core.Container{
				Image:   "alpine",
				Volumes: []string{"./data:/data:ro"},
			},
			expected: func() *Config {
				// Relative paths are resolved to absolute paths
				cwd, _ := os.Getwd()
				resolvedPath := filepath.Join(cwd, "data")
				return &Config{
					Image:      "alpine",
					AutoRemove: true,
					Container: &container.Config{
						Image: "alpine",
					},
					Host: &container.HostConfig{
						Binds: []string{resolvedPath + ":/data:ro"},
					},
					Network:     &network.NetworkingConfig{},
					ExecOptions: &client.ExecCreateOptions{},
				}
			}(),
		},
		{
			name: "HomeDirectoryBindMount",
			input: core.Container{
				Image:   "alpine",
				Volumes: []string{"~/data:/data:rw"},
			},
			expected: func() *Config {
				// Home directory paths are resolved to absolute paths
				homeDir, _ := os.UserHomeDir()
				resolvedPath := filepath.Join(homeDir, "data")
				return &Config{
					Image:      "alpine",
					AutoRemove: true,
					Container: &container.Config{
						Image: "alpine",
					},
					Host: &container.HostConfig{
						Binds: []string{resolvedPath + ":/data:rw"},
					},
					Network:     &network.NetworkingConfig{},
					ExecOptions: &client.ExecCreateOptions{},
				}
			}(),
		},
		{
			name: "PortWithIPAddress",
			input: core.Container{
				Image: "nginx",
				Ports: []string{"127.0.0.1:8080:80/tcp"},
			},
			expected: &Config{
				Image:      "nginx",
				AutoRemove: true,
				Container: &container.Config{
					Image: "nginx",
					ExposedPorts: network.PortSet{
						mustPort("80/tcp"): {},
					},
				},
				Host: &container.HostConfig{
					PortBindings: network.PortMap{
						mustPort("80/tcp"): []network.PortBinding{
							{
								HostIP:   mustAddr("127.0.0.1"),
								HostPort: "8080",
							},
						},
					},
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "UdpPort",
			input: core.Container{
				Image: "dns-server",
				Ports: []string{"53:53/udp"},
			},
			expected: &Config{
				Image:      "dns-server",
				AutoRemove: true,
				Container: &container.Config{
					Image: "dns-server",
					ExposedPorts: network.PortSet{
						mustPort("53/udp"): {},
					},
				},
				Host: &container.HostConfig{
					PortBindings: network.PortMap{
						mustPort("53/udp"): []network.PortBinding{
							{
								HostIP:   mustAddr("0.0.0.0"),
								HostPort: "53",
							},
						},
					},
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "InvalidVolumeFormatTooFewParts",
			input: core.Container{
				Image:   "alpine",
				Volumes: []string{"/data"},
			},
			expectError: true,
			errorMsg:    "invalid volume format: /data",
		},
		{
			name: "InvalidVolumeFormatTooManyParts",
			input: core.Container{
				Image:   "alpine",
				Volumes: []string{invalidVolumeSpec},
			},
			expectError: true,
			errorMsg:    invalidVolumeTooManyPartsError(invalidVolumeSpec),
		},
		{
			name: "InvalidVolumeMode",
			input: core.Container{
				Image:   "alpine",
				Volumes: []string{"/data:/data:invalid"},
			},
			expectError: true,
			errorMsg:    "invalid volume format: invalid mode invalid in /data:/data:invalid",
		},
		{
			name: "InvalidPortFormatTooManyParts",
			input: core.Container{
				Image: "nginx",
				Ports: []string{"1.2.3.4:8080:80:extra"},
			},
			expectError: true,
			errorMsg:    "invalid port format: 1.2.3.4:8080:80:extra",
		},
		{
			name: "InvalidPortProtocolDelimiter",
			input: core.Container{
				Image: "nginx",
				Ports: []string{"80/tcp/extra"},
			},
			expectError: true,
			errorMsg:    "invalid port format: invalid protocol in 80/tcp/extra",
		},
		{
			name: "InvalidPortProtocol",
			input: core.Container{
				Image: "nginx",
				Ports: []string{"80/invalid"},
			},
			expectError: true,
			errorMsg:    "invalid port format: invalid protocol invalid in 80/invalid",
		},
		{
			name: "SctpPortProtocol",
			input: core.Container{
				Image: "sctp-server",
				Ports: []string{"132/sctp"},
			},
			expected: &Config{
				Image:      "sctp-server",
				AutoRemove: true,
				Container: &container.Config{
					Image: "sctp-server",
					ExposedPorts: network.PortSet{
						mustPort("132/sctp"): {},
					},
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "WhitespaceInPortSpecification",
			input: core.Container{
				Image: "nginx",
				Ports: []string{" 8080:80 "},
			},
			expected: &Config{
				Image:      "nginx",
				AutoRemove: true,
				Container: &container.Config{
					Image: "nginx",
					ExposedPorts: network.PortSet{
						mustPort("80/tcp"): {},
					},
				},
				Host: &container.HostConfig{
					PortBindings: network.PortMap{
						mustPort("80/tcp"): []network.PortBinding{
							{
								HostIP:   mustAddr("0.0.0.0"),
								HostPort: "8080",
							},
						},
					},
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "EmptyNetworkUsesDefault",
			input: core.Container{
				Image:   "nginx",
				Network: "",
			},
			expected: &Config{
				Image:      "nginx",
				AutoRemove: true,
				Container: &container.Config{
					Image: "nginx",
				},
				Host: &container.HostConfig{
					NetworkMode: "", // Empty string for default
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "BridgeNetworkMode",
			input: core.Container{
				Image:   "nginx",
				Network: "bridge",
			},
			expected: &Config{
				Image:      "nginx",
				AutoRemove: true,
				Container: &container.Config{
					Image: "nginx",
				},
				Host: &container.HostConfig{
					NetworkMode: "bridge",
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "NoneNetworkMode",
			input: core.Container{
				Image:   "nginx",
				Network: "none",
			},
			expected: &Config{
				Image:      "nginx",
				AutoRemove: true,
				Container: &container.Config{
					Image: "nginx",
				},
				Host: &container.HostConfig{
					NetworkMode: "none",
				},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "KeepContainerFalseSetsAutoRemoveTrue",
			input: core.Container{
				Image:         "alpine",
				KeepContainer: false,
			},
			expected: &Config{
				Image:      "alpine",
				AutoRemove: true,
				Container: &container.Config{
					Image: "alpine",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "KeepContainerTrueSetsAutoRemoveFalse",
			input: core.Container{
				Image:         "alpine",
				KeepContainer: true,
			},
			expected: &Config{
				Image:      "alpine",
				AutoRemove: false,
				Container: &container.Config{
					Image: "alpine",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PullPolicyPropagation",
			input: core.Container{
				Image:      "alpine",
				PullPolicy: core.PullPolicyNever,
			},
			expected: &Config{
				Image:      "alpine",
				Pull:       core.PullPolicyNever,
				AutoRemove: true,
				Container: &container.Config{
					Image: "alpine",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "PlatformPropagation",
			input: core.Container{
				Image:    "alpine",
				Platform: "linux/386",
			},
			expected: &Config{
				Image:      "alpine",
				Platform:   "linux/386",
				AutoRemove: true,
				Container: &container.Config{
					Image: "alpine",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "ContainerNamePropagation",
			input: core.Container{
				Name:  "my-dag-container",
				Image: "alpine",
			},
			expected: &Config{
				ContainerName: "my-dag-container",
				Image:         "alpine",
				AutoRemove:    true,
				Container: &container.Config{
					Image: "alpine",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
		{
			name: "ContainerNameEmptyWhenNotSpecified",
			input: core.Container{
				Image: "alpine",
			},
			expected: &Config{
				ContainerName: "",
				Image:         "alpine",
				AutoRemove:    true,
				Container: &container.Config{
					Image: "alpine",
				},
				Host:        &container.HostConfig{},
				Network:     &network.NetworkingConfig{},
				ExecOptions: &client.ExecCreateOptions{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := LoadConfig("", tt.input, nil)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected.ContainerName, result.ContainerName)
			assert.Equal(t, tt.expected.Image, result.Image)
			assert.Equal(t, tt.expected.Platform, result.Platform)
			assert.Equal(t, tt.expected.Pull, result.Pull)
			assert.Equal(t, tt.expected.AutoRemove, result.AutoRemove)

			// Compare container config
			assert.Equal(t, tt.expected.Container.Image, result.Container.Image)
			assert.Equal(t, tt.expected.Container.Env, result.Container.Env)
			assert.Equal(t, tt.expected.Container.User, result.Container.User)
			assert.Equal(t, tt.expected.Container.WorkingDir, result.Container.WorkingDir)

			// Compare exposed ports
			if tt.expected.Container.ExposedPorts != nil {
				assert.Equal(t, tt.expected.Container.ExposedPorts, result.Container.ExposedPorts)
			}

			// Compare host config
			assert.Equal(t, tt.expected.Host.Binds, result.Host.Binds)
			if tt.expected.Host.Mounts != nil {
				assert.Equal(t, tt.expected.Host.Mounts, result.Host.Mounts)
			}
			if tt.expected.Host.PortBindings != nil {
				assert.Equal(t, tt.expected.Host.PortBindings, result.Host.PortBindings)
			}
			assert.Equal(t, tt.expected.Host.NetworkMode, result.Host.NetworkMode)

			// Compare network config
			if tt.expected.Network.EndpointsConfig != nil {
				assert.Equal(t, tt.expected.Network.EndpointsConfig, result.Network.EndpointsConfig)
			}
		})
	}
}

func TestMergeEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		base     []string
		override []string
		expected []string
	}{
		{
			name:     "EmptyBase",
			base:     nil,
			override: []string{"FOO=bar"},
			expected: []string{"FOO=bar"},
		},
		{
			name:     "EmptyOverride",
			base:     []string{"FOO=bar"},
			override: nil,
			expected: []string{"FOO=bar"},
		},
		{
			name:     "BothEmpty",
			base:     nil,
			override: nil,
			expected: nil,
		},
		{
			name:     "NoOverlap",
			base:     []string{"A=1", "B=2"},
			override: []string{"C=3", "D=4"},
			expected: []string{"A=1", "B=2", "C=3", "D=4"},
		},
		{
			name:     "OverrideTakesPrecedence",
			base:     []string{"SHARED=base_value", "A=1"},
			override: []string{"SHARED=override_value", "B=2"},
			expected: []string{"A=1", "B=2", "SHARED=override_value"},
		},
		{
			name:     "CompleteOverride",
			base:     []string{"X=old"},
			override: []string{"X=new"},
			expected: []string{"X=new"},
		},
		{
			name:     "ValueWithEquals",
			base:     []string{"URL=http://example.com?a=1&b=2"},
			override: []string{"OTHER=value"},
			expected: []string{"OTHER=value", "URL=http://example.com?a=1&b=2"},
		},
		{
			name:     "EmptyValue",
			base:     []string{"EMPTY="},
			override: []string{"OTHER=val"},
			expected: []string{"EMPTY=", "OTHER=val"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeEnvVars(tt.base, tt.override)

			// Sort both for comparison since map iteration order is not deterministic
			if result != nil {
				sort.Strings(result)
			}
			if tt.expected != nil {
				sort.Strings(tt.expected)
			}

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWrapCommandWithShell(t *testing.T) {
	tests := []struct {
		name     string
		shell    []string
		cmd      []string
		expected []string
	}{
		{
			name:     "NoShell_ReturnsCommandAsIs",
			shell:    nil,
			cmd:      []string{"echo", "hello"},
			expected: []string{"echo", "hello"},
		},
		{
			name:     "EmptyShell_ReturnsCommandAsIs",
			shell:    []string{},
			cmd:      []string{"echo", "hello"},
			expected: []string{"echo", "hello"},
		},
		{
			name:     "EmptyCommand_ReturnsEmpty",
			shell:    []string{"/bin/bash", "-c"},
			cmd:      []string{},
			expected: []string{},
		},
		{
			name:     "SimpleCommand_WithFlagAlready",
			shell:    []string{"/bin/bash", "-c"},
			cmd:      []string{"echo", "hello"},
			expected: []string{"/bin/bash", "-c", "echo hello"},
		},
		{
			name:     "SimpleCommand_AutoAddsFlag",
			shell:    []string{"/bin/sh"},
			cmd:      []string{"echo", "hello"},
			expected: []string{"/bin/sh", "-c", "echo hello"},
		},
		{
			name:     "BashWithStrictFlags_AutoAddsFlag",
			shell:    []string{"/bin/bash", "-o", "errexit", "-o", "pipefail"},
			cmd:      []string{"echo", "test"},
			expected: []string{"/bin/bash", "-o", "errexit", "-o", "pipefail", "-c", "echo test"},
		},
		{
			name:     "BashWithStrictFlags_FlagAlreadyPresent",
			shell:    []string{"/bin/bash", "-o", "errexit", "-o", "pipefail", "-c"},
			cmd:      []string{"echo", "test"},
			expected: []string{"/bin/bash", "-o", "errexit", "-o", "pipefail", "-c", "echo test"},
		},
		{
			name:     "PowerShell_AutoAddsCommandFlag",
			shell:    []string{"powershell"},
			cmd:      []string{"Write-Host", "hello"},
			expected: []string{"powershell", "-Command", "Write-Host hello"},
		},
		{
			name:     "PowerShell_FlagAlreadyPresent",
			shell:    []string{"pwsh", "-NoProfile", "-Command"},
			cmd:      []string{"Write-Host", "hello"},
			expected: []string{"pwsh", "-NoProfile", "-Command", "Write-Host hello"},
		},
		{
			name:     "CmdExe_AutoAddsFlag",
			shell:    []string{"cmd.exe"},
			cmd:      []string{"echo", "hello"},
			expected: []string{"cmd.exe", "/c", "echo hello"},
		},
		{
			name:     "NixShell_AutoAddsFlag",
			shell:    []string{"nix-shell"},
			cmd:      []string{"echo", "hello"},
			expected: []string{"nix-shell", "--run", "echo hello"},
		},
		{
			name:     "CommandWithAndOperator_MultiElement",
			shell:    []string{"/bin/sh", "-c"},
			cmd:      []string{"echo", "line1", "&&", "echo", "line2"},
			expected: []string{"/bin/sh", "-c", "echo line1 && echo line2"},
		},
		{
			name:     "CommandWithAndOperator_SingleElement",
			shell:    []string{"/bin/sh", "-c"},
			cmd:      []string{"echo line1 && echo line2"},
			expected: []string{"/bin/sh", "-c", "echo line1 && echo line2"},
		},
		{
			name:     "CommandWithQuotesPreserved_SingleElement",
			shell:    []string{"/bin/sh", "-c"},
			cmd:      []string{`echo "hello world"`},
			expected: []string{"/bin/sh", "-c", `echo "hello world"`},
		},
		{
			name:     "CommandWithPipe_MultiElement",
			shell:    []string{"/bin/sh", "-c"},
			cmd:      []string{"echo", "hello", "|", "tr", "a-z", "A-Z"},
			expected: []string{"/bin/sh", "-c", "echo hello | tr a-z A-Z"},
		},
		{
			name:     "CommandWithPipe_SingleElement",
			shell:    []string{"/bin/sh", "-c"},
			cmd:      []string{"echo hello | tr a-z A-Z"},
			expected: []string{"/bin/sh", "-c", "echo hello | tr a-z A-Z"},
		},
		{
			name:     "MultipleArguments",
			shell:    []string{"/bin/bash", "-c"},
			cmd:      []string{"cat", "/etc/hosts", "/etc/resolv.conf"},
			expected: []string{"/bin/bash", "-c", "cat /etc/hosts /etc/resolv.conf"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapCommandWithShell(tt.shell, tt.cmd)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDockerClientRespectsDockerHostEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://test-host:2375")

	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()

	assert.Equal(t, "tcp://test-host:2375", cli.DaemonHost())
}

// TestDaemonClientOpts_SelectedHostIsolatedFromDockerTLSEnv guards the contract
// that a service-selected daemon host (podman's Docker-compatible socket) is
// isolated from Docker TLS environment. With a stale DOCKER_CERT_PATH, the Docker
// default path (empty host -> client.FromEnv) fails client construction loading
// certs, while the selected-host opts succeed because they do not apply
// WithTLSClientConfigFromEnv. The selected host is also driven over plain http,
// not https, and DOCKER_HOST does not override the explicit selection.
func TestDaemonClientOpts_SelectedHostIsolatedFromDockerTLSEnv(t *testing.T) {
	t.Setenv("DOCKER_CERT_PATH", filepath.Join(t.TempDir(), "missing-certs"))
	t.Setenv("DOCKER_TLS_VERIFY", "1")
	t.Setenv("DOCKER_HOST", "tcp://should-not-win:2375")

	// Docker default path inherits the (stale) TLS env and fails to construct.
	_, derr := client.New(daemonClientOpts("")...)
	require.Error(t, derr, "docker default path inherits stale DOCKER_CERT_PATH and fails")

	// Selected podman socket is isolated: construction succeeds, host is the
	// selected socket (DOCKER_HOST did not win), and the scheme is plain http.
	sockHost := "unix:///run/podman/podman.sock"
	cli, err := client.New(daemonClientOpts(sockHost)...)
	require.NoError(t, err, "selected host must not inherit Docker TLS env")
	defer cli.Close()
	assert.Equal(t, sockHost, cli.DaemonHost(), "explicit selection wins over DOCKER_HOST")
}

// TestClientStopAfterCloseIsNilSafe is a regression guard for the cancel/Stop
// race: a captured *Client can have Stop() called after a concurrent Close()
// has nilled the underlying SDK handle (e.g. a containerized harness.run step
// cancelled as runContainerOnce's deferred Close runs). Close and Stop serialize
// on c.mu, but serialization does not prevent the Close-then-Stop ordering, so
// Stop must tolerate a nil c.cli rather than dereferencing it.
func TestClientStopAfterCloseIsNilSafe(t *testing.T) {
	sdkCli, err := client.New(client.FromEnv)
	require.NoError(t, err)

	c := &Client{
		// AutoRemove false so Close() does not attempt a daemon ContainerRemove;
		// this test must stay daemon-free. The race we reproduce is purely the
		// nil-handle ordering, not container teardown.
		cfg:         &Config{AutoRemove: false},
		cli:         sdkCli,
		containerID: "deadbeef",
	}
	c.started.Store(true) // simulate a started container so Stop passes its guards

	// Close() nils c.cli, exactly as the runContainerOnce cleanup defer does.
	c.Close(context.Background())

	// A Stop() that captured this *Client before cleanup now runs after Close.
	// Without the nil guard this panics dereferencing c.cli; with it, it returns nil.
	require.NotPanics(t, func() {
		err := c.Stop(syscall.SIGTERM)
		assert.NoError(t, err)
	})
}
