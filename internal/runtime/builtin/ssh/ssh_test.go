// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSSHExecutor(t *testing.T) {
	t.Parallel()

	step := core.Step{
		Name: "ssh-exec",
		ExecutorConfig: core.ExecutorConfig{
			Type: "ssh",
			Config: map[string]any{
				"User":     "testuser",
				"IP":       "testip",
				"Port":     25,
				"Password": "testpassword",
			},
		},
	}
	ctx := context.Background()
	_, err := NewSSHExecutor(ctx, step)
	require.NoError(t, err)
}

func TestNewSSHExecutor_WithShellConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		config        map[string]any
		expectedShell string
		expectedArgs  []string
	}{
		{
			name: "ShellFromConfig",
			config: map[string]any{
				"user":     "testuser",
				"ip":       "testip",
				"port":     22,
				"password": "testpassword",
				"shell":    "/bin/bash",
			},
			expectedShell: "/bin/bash",
		},
		{
			name: "ShellFromConfigWithArgs",
			config: map[string]any{
				"user":     "testuser",
				"ip":       "testip",
				"port":     22,
				"password": "testpassword",
				"shell":    "/bin/bash -e",
			},
			expectedShell: "/bin/bash",
			expectedArgs:  []string{"-e"},
		},
		{
			name: "NoShellInConfig",
			config: map[string]any{
				"user":     "testuser",
				"ip":       "testip",
				"port":     22,
				"password": "testpassword",
			},
			expectedShell: "/bin/sh", // Fallback to POSIX shell when no shell configured
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := core.Step{
				Name: "ssh-exec",
				ExecutorConfig: core.ExecutorConfig{
					Type:   "ssh",
					Config: tt.config,
				},
			}
			ctx := context.Background()
			exec, err := NewSSHExecutor(ctx, step)
			require.NoError(t, err)

			sshExec, ok := exec.(*sshExecutor)
			require.True(t, ok)
			assert.Equal(t, tt.expectedShell, sshExec.shell)
			assert.Equal(t, tt.expectedArgs, sshExec.shellArgs)
		})
	}
}

func TestSSHExecutor_ShellPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		client        *Client
		step          core.Step
		expectedShell string
		expectedArgs  []string
	}{
		{
			name: "DAGLevelShell",
			client: &Client{
				hostPort:  "localhost:22",
				Shell:     "/bin/bash",
				ShellArgs: []string{"-e"},
			},
			step: core.Step{
				Name:           "ssh-step",
				ExecutorConfig: core.ExecutorConfig{Type: "ssh", Config: nil},
			},
			expectedShell: "/bin/bash",
			expectedArgs:  []string{"-e"},
		},
		{
			name: "StepLevelShellOverridesDAGLevel",
			client: &Client{
				hostPort:  "localhost:22",
				Shell:     "/bin/sh",
				ShellArgs: []string{"-e"},
			},
			step: core.Step{
				Name: "ssh-step",
				ExecutorConfig: core.ExecutorConfig{
					Type: "ssh",
					Config: map[string]any{
						"user":     "testuser",
						"ip":       "testip",
						"port":     22,
						"password": "testpassword",
						"shell":    "/bin/zsh -o pipefail",
					},
				},
			},
			expectedShell: "/bin/zsh",
			expectedArgs:  []string{"-o", "pipefail"},
		},
		{
			name: "StepShellFallbackWhenNoSSHConfigShell",
			client: &Client{
				hostPort: "localhost:22",
				Shell:    "",
			},
			step: core.Step{
				Name:           "ssh-step",
				Shell:          "/bin/bash",
				ShellArgs:      []string{"-e"},
				ExecutorConfig: core.ExecutorConfig{Type: "ssh", Config: nil},
			},
			expectedShell: "/bin/bash",
			expectedArgs:  []string{"-e"},
		},
		{
			name: "SSHConfigShellTakesPriorityOverStepShell",
			client: &Client{
				hostPort:  "localhost:22",
				Shell:     "/bin/zsh",
				ShellArgs: []string{"-e"},
			},
			step: core.Step{
				Name:           "ssh-step",
				Shell:          "/bin/bash",
				ShellArgs:      []string{"-o", "pipefail"},
				ExecutorConfig: core.ExecutorConfig{Type: "ssh", Config: nil},
			},
			expectedShell: "/bin/zsh",
			expectedArgs:  []string{"-e"},
		},
		{
			name: "StepSSHConfigWithoutShellIgnoresDAGShell",
			client: &Client{
				hostPort: "localhost:22",
				Shell:    "/bin/zsh",
			},
			step: core.Step{
				Name: "ssh-step",
				ExecutorConfig: core.ExecutorConfig{
					Type: "ssh",
					Config: map[string]any{
						"user":     "stepuser",
						"ip":       "step-host",
						"port":     22,
						"password": "steppassword",
					},
				},
			},
			expectedShell: "/bin/sh",
			expectedArgs:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := WithSSHClient(context.Background(), tt.client)
			exec, err := NewSSHExecutor(ctx, tt.step)
			require.NoError(t, err)

			sshExec, ok := exec.(*sshExecutor)
			require.True(t, ok)
			assert.Equal(t, tt.expectedShell, sshExec.shell)
			assert.Equal(t, tt.expectedArgs, sshExec.shellArgs)
		})
	}
}

func TestSSHExecutorCommandResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		step            core.Step
		dagShell        string
		expectSkipShell bool
	}{
		{
			name: "StepShellSet",
			step: core.Step{
				Shell:          "/bin/bash",
				ExecutorConfig: core.ExecutorConfig{Type: "ssh"},
			},
			expectSkipShell: false,
		},
		{
			name: "StepConfigShellSet",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{
					Type:   "ssh",
					Config: map[string]any{"shell": "/bin/bash"},
				},
			},
			expectSkipShell: false,
		},
		{
			name: "StepConfigNoShell",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{
					Type:   "ssh",
					Config: map[string]any{"user": "test"},
				},
			},
			expectSkipShell: true,
		},
		{
			name: "DAGShellSet",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "ssh"},
			},
			dagShell:        "/bin/bash",
			expectSkipShell: false,
		},
		{
			name: "NoShellAnywhere",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "ssh"},
			},
			expectSkipShell: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.dagShell != "" {
				ctx = WithSSHClient(ctx, &Client{Shell: tt.dagShell})
			}

			command := tt.step.CommandResolution(ctx)
			require.Equal(t, cmnvalue.CommandTargetSSH, command.Target)
			require.Equal(t, !tt.expectSkipShell, command.ShellConfigured)
		})
	}
}

func TestSSHExecutor_BuildScript_WithWorkingDir(t *testing.T) {
	t.Parallel()

	exec := &sshExecutor{
		step: core.Step{
			Dir: "/app/src", // Working directory is taken from step.Dir
			Commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}},
			},
		},
		shell: "/bin/sh",
	}

	script := exec.buildScript()

	// Verify the script contains cd command (path may or may not be quoted)
	assert.Contains(t, script, "cd ")
	assert.Contains(t, script, "/app/src")
	assert.Contains(t, script, "|| return 1")
	assert.Contains(t, script, "set -e")
	assert.Contains(t, script, "echo hello")
	assert.Contains(t, script, "__dagu_exec(){")
	assert.Contains(t, script, "__dagu_exec")
}

func TestSSHExecutor_BuildScript_WithScript(t *testing.T) {
	t.Parallel()

	exec := &sshExecutor{
		step: core.Step{
			Script: "echo 'line1'\necho 'line2'",
		},
		shell: "/bin/bash",
	}

	script := exec.buildScript()

	// Verify script content is included
	assert.Contains(t, script, "echo 'line1'")
	assert.Contains(t, script, "echo 'line2'")
	assert.Contains(t, script, "set -e")
	assert.Contains(t, script, "__dagu_exec(){")
}

func TestSSHExecutor_BuildScript_WithCommands(t *testing.T) {
	t.Parallel()

	exec := &sshExecutor{
		step: core.Step{
			Commands: []core.CommandEntry{
				{Command: "git", Args: []string{"pull"}},
				{Command: "make", Args: []string{"build"}},
				{Command: "./deploy.sh"},
			},
		},
		shell: "/bin/sh",
	}

	script := exec.buildScript()

	// Verify all commands are included
	assert.Contains(t, script, "git pull")
	assert.Contains(t, script, "make build")
	assert.Contains(t, script, "./deploy.sh")
	assert.Contains(t, script, "set -e")
}

func TestSSHExecutor_BuildScript_FunctionWrapper(t *testing.T) {
	t.Parallel()

	exec := &sshExecutor{
		step: core.Step{
			Commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"test"}},
			},
		},
		shell: "/bin/sh",
	}

	script := exec.buildScript()

	// Verify function wrapper format
	assert.True(t, strings.HasPrefix(script, "__dagu_exec(){"))
	assert.True(t, strings.HasSuffix(script, "__dagu_exec\n"))
	assert.Contains(t, script, "}\n__dagu_exec")
}

func TestSSHExecutor_ResolveShell_Fallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		step          core.Step
		client        *Client
		expectedShell string
		expectedArgs  []string
	}{
		{
			name:          "FallbackToSh",
			step:          core.Step{},
			client:        &Client{},
			expectedShell: "/bin/sh",
			expectedArgs:  nil,
		},
		{
			name: "ClientShellTakesPriority",
			step: core.Step{Shell: "/bin/zsh"},
			client: &Client{
				Shell:     "/bin/bash",
				ShellArgs: []string{"-e"},
			},
			expectedShell: "/bin/bash",
			expectedArgs:  []string{"-e"},
		},
		{
			name:          "StepShellWhenNoClient",
			step:          core.Step{Shell: "/bin/zsh", ShellArgs: []string{"-x"}},
			client:        &Client{},
			expectedShell: "/bin/zsh",
			expectedArgs:  []string{"-x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shell, args := resolveShell(tt.step, tt.client)
			assert.Equal(t, tt.expectedShell, shell)
			assert.Equal(t, tt.expectedArgs, args)
		})
	}
}

func TestSSHExecutor_BuildCommandString(t *testing.T) {
	t.Parallel()

	exec := &sshExecutor{}

	tests := []struct {
		name     string
		cmd      core.CommandEntry
		expected string
	}{
		{
			name:     "CommandOnly",
			cmd:      core.CommandEntry{Command: "ls"},
			expected: "ls",
		},
		{
			name:     "CommandWithArgs",
			cmd:      core.CommandEntry{Command: "ls", Args: []string{"-la", "/tmp"}},
			expected: "ls -la /tmp",
		},
		{
			name:     "CommandWithSpacesInArgs",
			cmd:      core.CommandEntry{Command: "echo", Args: []string{"hello world"}},
			expected: "echo 'hello world'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exec.buildCommandString(tt.cmd)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSSHExecutor_BuildShellCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		shell     string
		shellArgs []string
		expected  string
	}{
		{
			name:     "ShellOnly",
			shell:    "/bin/sh",
			expected: "/bin/sh",
		},
		{
			name:      "ShellWithSingleArg",
			shell:     "/bin/bash",
			shellArgs: []string{"-e"},
			expected:  "/bin/bash -e",
		},
		{
			name:      "ShellWithMultipleArgs",
			shell:     "/bin/bash",
			shellArgs: []string{"-e", "-o", "pipefail"},
			expected:  "/bin/bash -e -o pipefail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &sshExecutor{
				shell:     tt.shell,
				shellArgs: tt.shellArgs,
			}
			result := exec.buildShellCommand()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSSHExecutor_TimeoutConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		config          map[string]any
		expectedTimeout time.Duration
		expectError     bool
	}{
		{
			name: "DefaultTimeout",
			config: map[string]any{
				"user":     "testuser",
				"ip":       "testip",
				"password": "testpassword",
			},
			expectedTimeout: 30 * time.Second, // Default timeout
		},
		{
			name: "CustomTimeout",
			config: map[string]any{
				"user":     "testuser",
				"ip":       "testip",
				"password": "testpassword",
				"timeout":  "1m",
			},
			expectedTimeout: 1 * time.Minute,
		},
		{
			name: "ShortTimeout",
			config: map[string]any{
				"user":     "testuser",
				"ip":       "testip",
				"password": "testpassword",
				"timeout":  "5s",
			},
			expectedTimeout: 5 * time.Second,
		},
		{
			name: "InvalidTimeout",
			config: map[string]any{
				"user":     "testuser",
				"ip":       "testip",
				"password": "testpassword",
				"timeout":  "invalid",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client, err := FromMapConfig(ctx, tt.config)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid timeout duration")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, client)
			assert.Equal(t, tt.expectedTimeout, client.cfg.Timeout)
		})
	}
}

func TestSSHExecutor_Run_NoCommands(t *testing.T) {
	t.Parallel()

	// Create executor with no commands or script
	exec := &sshExecutor{
		step: core.Step{
			Commands: nil,
			Script:   "",
		},
		shell: "/bin/sh",
	}

	// Run should return nil immediately when there are no commands
	err := exec.Run(context.Background())
	require.NoError(t, err)
}

func TestSSHExecutor_SetStdout_SetStderr(t *testing.T) {
	t.Parallel()

	exec := &sshExecutor{}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exec.SetStdout(stdout)
	exec.SetStderr(stderr)

	assert.Equal(t, stdout, exec.stdout)
	assert.Equal(t, stderr, exec.stderr)
}

func TestSSHExecutor_Kill_NoSession(t *testing.T) {
	t.Parallel()

	// Create executor without session
	exec := &sshExecutor{}

	// Kill should return nil when there's no session
	err := exec.Kill(nil)
	require.NoError(t, err)
}

func TestNewSSHExecutor_NoConfig(t *testing.T) {
	t.Parallel()

	// Create step without SSH config and without DAG-level SSH client
	step := core.Step{
		Name: "ssh-exec",
		ExecutorConfig: core.ExecutorConfig{
			Type:   "ssh",
			Config: nil,
		},
	}

	ctx := context.Background()
	_, err := NewSSHExecutor(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh configuration is not found")
}

func TestSSHExecutor_ClosedFlag(t *testing.T) {
	t.Parallel()

	// Verify that closed flag prevents double close issues
	exec := &sshExecutor{
		closed: false,
	}

	// First Kill should work (no session, so just returns nil)
	err := exec.Kill(nil)
	require.NoError(t, err)
	assert.True(t, exec.closed)

	// Second Kill should be no-op due to closed flag
	err = exec.Kill(nil)
	require.NoError(t, err)
}

func TestFromMapConfig_WithBastion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		bastion          map[string]any
		expectedHostPort string
		expectedUser     string
	}{
		{
			name: "ExplicitPort",
			bastion: map[string]any{
				"host":     "bastion.example.com",
				"port":     "2222",
				"user":     "bastionuser",
				"password": "bastionpass",
			},
			expectedHostPort: "bastion.example.com:2222",
			expectedUser:     "bastionuser",
		},
		{
			name: "NumericPort",
			bastion: map[string]any{
				"host":     "bastion.example.com",
				"port":     2222,
				"user":     "bastionuser",
				"password": "bastionpass",
			},
			expectedHostPort: "bastion.example.com:2222",
			expectedUser:     "bastionuser",
		},
		{
			name: "DefaultPort",
			bastion: map[string]any{
				"host":     "bastion.example.com",
				"user":     "bastionuser",
				"password": "bastionpass",
			},
			expectedHostPort: "bastion.example.com:22",
			expectedUser:     "bastionuser",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]any{
				"user":     "testuser",
				"host":     "target.example.com",
				"password": "targetpass",
				"bastion":  tt.bastion,
			}

			client, err := FromMapConfig(context.Background(), config)
			require.NoError(t, err)
			require.NotNil(t, client)
			require.NotNil(t, client.bastionCfg)
			assert.Equal(t, tt.expectedHostPort, client.bastionCfg.hostPort)
			assert.Equal(t, tt.expectedUser, client.bastionCfg.cfg.User)
		})
	}
}

func TestNewClient_WithBastionConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		User:     "testuser",
		Host:     "target.example.com",
		Port:     "22",
		Password: "targetpass",
		Bastion: &BastionConfig{
			Host:     "bastion.example.com",
			Port:     "2222",
			User:     "bastionuser",
			Password: "bastionpass",
		},
	}

	client, err := NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Verify bastion config is properly set
	assert.NotNil(t, client.bastionCfg)
	assert.Equal(t, "bastion.example.com:2222", client.bastionCfg.hostPort)
}

func TestNewSFTPExecutor(t *testing.T) {
	t.Parallel()

	step := core.Step{
		Name: "sftp-transfer",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sftp",
			Config: map[string]any{
				"user":        "testuser",
				"host":        "testhost",
				"password":    "testpass",
				"direction":   "upload",
				"source":      "/local/path",
				"destination": "/remote/path",
			},
		},
	}

	ctx := context.Background()
	exec, err := NewSFTPExecutor(ctx, step)
	require.NoError(t, err)
	require.NotNil(t, exec)

	sftpExec, ok := exec.(*sftpExecutor)
	require.True(t, ok)
	assert.Equal(t, "upload", sftpExec.direction)
	assert.Equal(t, "/local/path", sftpExec.source)
	assert.Equal(t, "/remote/path", sftpExec.destination)
}

func TestNewSFTPExecutor_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      map[string]any
		expectedErr string
	}{
		{
			name: "MissingSource",
			config: map[string]any{
				"user":        "testuser",
				"host":        "testhost",
				"password":    "testpass",
				"direction":   "upload",
				"destination": "/remote/path",
			},
			expectedErr: "source path is required",
		},
		{
			name: "MissingDestination",
			config: map[string]any{
				"user":      "testuser",
				"host":      "testhost",
				"password":  "testpass",
				"direction": "download",
				"source":    "/remote/path",
			},
			expectedErr: "destination path is required",
		},
		{
			name: "InvalidDirection",
			config: map[string]any{
				"user":        "testuser",
				"host":        "testhost",
				"password":    "testpass",
				"direction":   "invalid",
				"source":      "/local/path",
				"destination": "/remote/path",
			},
			expectedErr: "invalid direction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := core.Step{
				Name:           "sftp-transfer",
				ExecutorConfig: core.ExecutorConfig{Type: "sftp", Config: tt.config},
			}
			_, err := NewSFTPExecutor(context.Background(), step)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestNewSFTPExecutor_DefaultDirection(t *testing.T) {
	t.Parallel()

	step := core.Step{
		Name: "sftp-transfer",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sftp",
			Config: map[string]any{
				"user":        "testuser",
				"host":        "testhost",
				"password":    "testpass",
				"source":      "/local/path",
				"destination": "/remote/path",
				// direction not specified - should default to upload
			},
		},
	}

	ctx := context.Background()
	exec, err := NewSFTPExecutor(ctx, step)
	require.NoError(t, err)

	sftpExec, ok := exec.(*sftpExecutor)
	require.True(t, ok)
	assert.Equal(t, "upload", sftpExec.direction) // Default to upload
}

func TestSFTPExecutor_SetStdout_SetStderr(t *testing.T) {
	t.Parallel()

	exec := &sftpExecutor{}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exec.SetStdout(stdout)
	exec.SetStderr(stderr)

	assert.Equal(t, stdout, exec.stdout)
	assert.Equal(t, stderr, exec.stderr)
}

func TestSFTPExecutor_Kill(t *testing.T) {
	t.Parallel()

	exec := &sftpExecutor{}

	// Kill always returns nil for SFTP executor
	err := exec.Kill(nil)
	require.NoError(t, err)
}

func TestGetStringConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     map[string]any
		key        string
		defaultVal string
		expected   string
	}{
		{
			name:       "KeyExists",
			config:     map[string]any{"key1": "value1"},
			key:        "key1",
			defaultVal: "default",
			expected:   "value1",
		},
		{
			name:       "KeyNotExists",
			config:     map[string]any{"other": "value"},
			key:        "key1",
			defaultVal: "default",
			expected:   "default",
		},
		{
			name:       "EmptyValue",
			config:     map[string]any{"key1": ""},
			key:        "key1",
			defaultVal: "default",
			expected:   "default",
		},
		{
			name:       "NilConfig",
			config:     nil,
			key:        "key1",
			defaultVal: "default",
			expected:   "default",
		},
		{
			name:       "WrongType",
			config:     map[string]any{"key1": 123},
			key:        "key1",
			defaultVal: "default",
			expected:   "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStringConfig(tt.config, tt.key, tt.defaultVal)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultIfZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		val        time.Duration
		defaultVal time.Duration
		expected   time.Duration
	}{
		{
			name:       "ZeroValue",
			val:        0,
			defaultVal: 30 * time.Second,
			expected:   30 * time.Second,
		},
		{
			name:       "NonZeroValue",
			val:        1 * time.Minute,
			defaultVal: 30 * time.Second,
			expected:   1 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultIfZero(tt.val, tt.defaultVal)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultIfEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		val        string
		defaultVal string
		expected   string
	}{
		{
			name:       "EmptyValue",
			val:        "",
			defaultVal: "22",
			expected:   "22",
		},
		{
			name:       "ZeroString",
			val:        "0",
			defaultVal: "22",
			expected:   "22",
		},
		{
			name:       "NonEmptyValue",
			val:        "2222",
			defaultVal: "22",
			expected:   "2222",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultIfEmpty(tt.val, tt.defaultVal)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDefaultSSHKeys(t *testing.T) {
	t.Parallel()

	keys := getDefaultSSHKeys()

	// Should return 4 default key paths
	assert.Len(t, keys, 4)

	// All paths should contain .ssh directory
	for _, key := range keys {
		assert.Contains(t, key, ".ssh")
	}

	// Should contain the standard key names
	keyNames := strings.Join(keys, ",")
	assert.Contains(t, keyNames, "id_rsa")
	assert.Contains(t, keyNames, "id_ecdsa")
	assert.Contains(t, keyNames, "id_ed25519")
	assert.Contains(t, keyNames, "id_dsa")
}

func TestGetHostKeyCallback_InsecureMode(t *testing.T) {
	t.Parallel()

	// When strict_host_key is false, should return InsecureIgnoreHostKey
	callback, err := getHostKeyCallback(false, "")
	require.NoError(t, err)
	require.NotNil(t, callback)

	// The callback should accept any host key (insecure mode)
	// We can't easily test the callback itself, but we verify it's not nil
}

func TestSelectSSHAuthMethod_Password(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Password: "testpassword",
	}

	authMethod, err := selectSSHAuthMethod(cfg)
	require.NoError(t, err)
	require.NotNil(t, authMethod)
}

func TestSelectSSHAuthMethod_NoAuth(t *testing.T) {
	t.Parallel()

	// Skip if default SSH keys exist on the system
	if findDefaultSSHKey() != "" {
		t.Skip("Skipping: default SSH keys found on system")
	}

	// No key, no password, and no default keys exist
	cfg := &Config{
		// Empty - no auth method specified
	}

	_, err := selectSSHAuthMethod(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SSH key specified")
}

func TestSelectBastionAuthMethod_Password(t *testing.T) {
	t.Parallel()

	bastion := &BastionConfig{
		Host:     "bastion.example.com",
		User:     "bastionuser",
		Password: "bastionpass",
	}

	authMethod, err := selectBastionAuthMethod(bastion)
	require.NoError(t, err)
	require.NotNil(t, authMethod)
}

func TestSelectBastionAuthMethod_NoAuth(t *testing.T) {
	t.Parallel()

	// Skip if default SSH keys exist on the system
	if findDefaultSSHKey() != "" {
		t.Skip("Skipping: default SSH keys found on system")
	}

	bastion := &BastionConfig{
		Host: "bastion.example.com",
		User: "bastionuser",
		// No key, no password
	}

	_, err := selectBastionAuthMethod(bastion)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authentication method available for bastion")
}
