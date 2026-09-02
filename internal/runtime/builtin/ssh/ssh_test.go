// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

func TestNewSSHExecutor(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Name: "ssh-exec",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "ssh",
			Config: map[string]any{
				"User":            "testuser",
				"IP":              "testip",
				"Port":            25,
				"Password":        "testpassword",
				"strict_host_key": false,
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
				"user":            "testuser",
				"ip":              "testip",
				"port":            22,
				"password":        "testpassword",
				"strict_host_key": false,
				"shell":           "/bin/bash",
			},
			expectedShell: "/bin/bash",
		},
		{
			name: "ShellFromConfigWithArgs",
			config: map[string]any{
				"user":            "testuser",
				"ip":              "testip",
				"port":            22,
				"password":        "testpassword",
				"strict_host_key": false,
				"shell":           "/bin/bash -e",
			},
			expectedShell: "/bin/bash",
			expectedArgs:  []string{"-e"},
		},
		{
			name: "NoShellInConfig",
			config: map[string]any{
				"user":            "testuser",
				"ip":              "testip",
				"port":            22,
				"password":        "testpassword",
				"strict_host_key": false,
			},
			expectedShell: "/bin/sh", // Fallback to POSIX shell when no shell configured
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := ir.Step{
				Name: "ssh-exec",
				ExecutorConfig: ir.ExecutorConfig{
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
		step          ir.Step
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
			step: ir.Step{
				Name:           "ssh-step",
				ExecutorConfig: ir.ExecutorConfig{Type: "ssh", Config: nil},
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
			step: ir.Step{
				Name: "ssh-step",
				ExecutorConfig: ir.ExecutorConfig{
					Type: "ssh",
					Config: map[string]any{
						"user":            "testuser",
						"ip":              "testip",
						"port":            22,
						"password":        "testpassword",
						"strict_host_key": false,
						"shell":           "/bin/zsh -o pipefail",
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
			step: ir.Step{
				Name:           "ssh-step",
				Shell:          "/bin/bash",
				ShellArgs:      []string{"-e"},
				ExecutorConfig: ir.ExecutorConfig{Type: "ssh", Config: nil},
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
			step: ir.Step{
				Name:           "ssh-step",
				Shell:          "/bin/bash",
				ShellArgs:      []string{"-o", "pipefail"},
				ExecutorConfig: ir.ExecutorConfig{Type: "ssh", Config: nil},
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
			step: ir.Step{
				Name: "ssh-step",
				ExecutorConfig: ir.ExecutorConfig{
					Type: "ssh",
					Config: map[string]any{
						"user":            "stepuser",
						"ip":              "step-host",
						"port":            22,
						"password":        "steppassword",
						"strict_host_key": false,
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
		step            ir.Step
		dagShell        string
		expectSkipShell bool
	}{
		{
			name: "StepShellSet",
			step: ir.Step{
				Shell:          "/bin/bash",
				ExecutorConfig: ir.ExecutorConfig{Type: "ssh"},
			},
			expectSkipShell: false,
		},
		{
			name: "StepConfigShellSet",
			step: ir.Step{
				ExecutorConfig: ir.ExecutorConfig{
					Type:   "ssh",
					Config: map[string]any{"shell": "/bin/bash"},
				},
			},
			expectSkipShell: false,
		},
		{
			name: "StepConfigNoShell",
			step: ir.Step{
				ExecutorConfig: ir.ExecutorConfig{
					Type:   "ssh",
					Config: map[string]any{"user": "test"},
				},
			},
			expectSkipShell: true,
		},
		{
			name: "DAGShellSet",
			step: ir.Step{
				ExecutorConfig: ir.ExecutorConfig{Type: "ssh"},
			},
			dagShell:        "/bin/bash",
			expectSkipShell: false,
		},
		{
			name: "NoShellAnywhere",
			step: ir.Step{
				ExecutorConfig: ir.ExecutorConfig{Type: "ssh"},
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

			command := registry.CommandResolution(ctx, tt.step)
			require.Equal(t, cmnvalue.CommandTargetSSH, command.Target)
			require.Equal(t, !tt.expectSkipShell, command.ShellConfigured)
		})
	}
}

func TestSSHExecutor_BuildScript_WithWorkingDir(t *testing.T) {
	t.Parallel()

	exec := &sshExecutor{
		step: ir.Step{
			Dir: "/app/src", // Working directory is taken from step.Dir
			Commands: []ir.CommandEntry{
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
		step: ir.Step{
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
		step: ir.Step{
			Commands: []ir.CommandEntry{
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
		step: ir.Step{
			Commands: []ir.CommandEntry{
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
		step          ir.Step
		client        *Client
		expectedShell string
		expectedArgs  []string
	}{
		{
			name:          "FallbackToSh",
			step:          ir.Step{},
			client:        &Client{},
			expectedShell: "/bin/sh",
			expectedArgs:  nil,
		},
		{
			name: "ClientShellTakesPriority",
			step: ir.Step{Shell: "/bin/zsh"},
			client: &Client{
				Shell:     "/bin/bash",
				ShellArgs: []string{"-e"},
			},
			expectedShell: "/bin/bash",
			expectedArgs:  []string{"-e"},
		},
		{
			name:          "StepShellWhenNoClient",
			step:          ir.Step{Shell: "/bin/zsh", ShellArgs: []string{"-x"}},
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
		cmd      ir.CommandEntry
		expected string
	}{
		{
			name:     "CommandOnly",
			cmd:      ir.CommandEntry{Command: "ls"},
			expected: "ls",
		},
		{
			name:     "CommandWithArgs",
			cmd:      ir.CommandEntry{Command: "ls", Args: []string{"-la", "/tmp"}},
			expected: "ls -la /tmp",
		},
		{
			name:     "CommandWithSpacesInArgs",
			cmd:      ir.CommandEntry{Command: "echo", Args: []string{"hello world"}},
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
				"user":            "testuser",
				"ip":              "testip",
				"password":        "testpassword",
				"strict_host_key": false,
			},
			expectedTimeout: 30 * time.Second, // Default timeout
		},
		{
			name: "CustomTimeout",
			config: map[string]any{
				"user":            "testuser",
				"ip":              "testip",
				"password":        "testpassword",
				"strict_host_key": false,
				"timeout":         "1m",
			},
			expectedTimeout: 1 * time.Minute,
		},
		{
			name: "ShortTimeout",
			config: map[string]any{
				"user":            "testuser",
				"ip":              "testip",
				"password":        "testpassword",
				"strict_host_key": false,
				"timeout":         "5s",
			},
			expectedTimeout: 5 * time.Second,
		},
		{
			name: "InvalidTimeout",
			config: map[string]any{
				"user":            "testuser",
				"ip":              "testip",
				"password":        "testpassword",
				"strict_host_key": false,
				"timeout":         "invalid",
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

func TestFromMapConfigKeys(t *testing.T) {
	t.Parallel()

	t.Run("HostKey", func(t *testing.T) {
		knownHostFile := filepath.Join(t.TempDir(), "known_hosts")
		_, err := FromMapConfig(context.Background(), map[string]any{
			"user":            "testuser",
			"host":            "testhost",
			"password":        "testpass",
			"strict_host_key": true,
			"known_host_file": knownHostFile,
		})
		require.ErrorContains(t, err, knownHostFile)
	})

	t.Run("DefaultHostKey", func(t *testing.T) {
		knownHostFile := filepath.Join(t.TempDir(), "known_hosts")
		_, err := FromMapConfig(context.Background(), map[string]any{
			"user":            "testuser",
			"host":            "testhost",
			"password":        "testpass",
			"known_host_file": knownHostFile,
		})
		require.ErrorContains(t, err, knownHostFile)
	})

	t.Run("ShellArgs", func(t *testing.T) {
		client, err := FromMapConfig(context.Background(), map[string]any{
			"user":            "testuser",
			"host":            "testhost",
			"password":        "testpass",
			"strict_host_key": false,
			"shell":           "/bin/bash",
			"shell_args":      []any{"-x"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"-x"}, client.ShellArgs)
	})
}

func TestSSHExecutor_Run_NoCommands(t *testing.T) {
	t.Parallel()

	// Create executor with no commands or script
	exec := &sshExecutor{
		step: ir.Step{
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

func TestNewSSHExecutor_NoConfig(t *testing.T) {
	t.Parallel()

	// Create step without SSH config and without DAG-level SSH client
	step := ir.Step{
		Name: "ssh-exec",
		ExecutorConfig: ir.ExecutorConfig{
			Type:   "ssh",
			Config: nil,
		},
	}

	ctx := context.Background()
	_, err := NewSSHExecutor(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh configuration is not found")
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
				"user":            "testuser",
				"host":            "target.example.com",
				"password":        "targetpass",
				"strict_host_key": false,
				"bastion":         tt.bastion,
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

	step := ir.Step{
		Name: "sftp-transfer",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "sftp",
			Config: map[string]any{
				"user":            "testuser",
				"host":            "testhost",
				"password":        "testpass",
				"strict_host_key": false,
				"direction":       "upload",
				"source":          "/local/path",
				"destination":     "/remote/path",
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
				"user":            "testuser",
				"host":            "testhost",
				"password":        "testpass",
				"strict_host_key": false,
				"direction":       "upload",
				"destination":     "/remote/path",
			},
			expectedErr: "source path is required",
		},
		{
			name: "MissingDestination",
			config: map[string]any{
				"user":            "testuser",
				"host":            "testhost",
				"password":        "testpass",
				"strict_host_key": false,
				"direction":       "download",
				"source":          "/remote/path",
			},
			expectedErr: "destination path is required",
		},
		{
			name: "InvalidDirection",
			config: map[string]any{
				"user":            "testuser",
				"host":            "testhost",
				"password":        "testpass",
				"strict_host_key": false,
				"direction":       "invalid",
				"source":          "/local/path",
				"destination":     "/remote/path",
			},
			expectedErr: "invalid direction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := ir.Step{
				Name:           "sftp-transfer",
				ExecutorConfig: ir.ExecutorConfig{Type: "sftp", Config: tt.config},
			}
			_, err := NewSFTPExecutor(context.Background(), step)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestNewSFTPExecutor_DefaultDirection(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Name: "sftp-transfer",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "sftp",
			Config: map[string]any{
				"user":            "testuser",
				"host":            "testhost",
				"password":        "testpass",
				"strict_host_key": false,
				"source":          "/local/path",
				"destination":     "/remote/path",
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

func TestExecutorCancellationInterruptsSSHHandshake(t *testing.T) {
	tests := []struct {
		name    string
		newExec func(*Client) runtimeexec.Executor
	}{
		{
			name: "SSH",
			newExec: func(client *Client) runtimeexec.Executor {
				return &sshExecutor{
					step:   ir.Step{Commands: []ir.CommandEntry{{Command: "true", CmdWithArgs: "true"}}},
					client: client,
					shell:  "/bin/sh",
					stdout: io.Discard,
					stderr: io.Discard,
				}
			},
		},
		{
			name: "SFTP",
			newExec: func(client *Client) runtimeexec.Executor {
				return &sftpExecutor{
					client:      client,
					direction:   "download",
					source:      "/remote/file",
					destination: "/local/file",
					stdout:      io.Discard,
					stderr:      io.Discard,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("ContextCancellation", func(t *testing.T) {
				addr, accepted := startBlackholeSSHServer(t)
				exec := tt.newExec(newBlackholeSSHClient(addr))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				errCh := make(chan error, 1)
				go func() { errCh <- exec.Run(ctx) }()
				conn := receiveAcceptedConnection(t, accepted)
				cancel()
				assertConnectionCloses(t, conn)

				select {
				case err := <-errCh:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(2 * time.Second):
					t.Fatal("executor did not return after context cancellation")
				}
			})

			t.Run("Kill", func(t *testing.T) {
				addr, accepted := startBlackholeSSHServer(t)
				exec := tt.newExec(newBlackholeSSHClient(addr))
				errCh := make(chan error, 1)
				go func() { errCh <- exec.Run(context.Background()) }()
				conn := receiveAcceptedConnection(t, accepted)

				require.NoError(t, exec.Kill(nil))
				require.NoError(t, exec.Kill(nil))
				assertConnectionCloses(t, conn)

				select {
				case err := <-errCh:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(2 * time.Second):
					t.Fatal("executor did not return after Kill")
				}
			})
		})
	}
}

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}

func TestExecutorLifecycleForcedShutdown(t *testing.T) {
	t.Parallel()

	var events []string
	unexpectedErr := errors.New("unexpected close error")
	lifecycle := executorLifecycle{
		cancel: func() { events = append(events, "cancel") },
		transport: closerFunc(func() error {
			events = append(events, "transport")
			return errors.Join(net.ErrClosed, unexpectedErr)
		}),
		resource: closerFunc(func() error {
			events = append(events, "resource")
			return io.EOF
		}),
	}

	err := lifecycle.shutdown(true)
	assert.Equal(t, []string{"cancel", "transport", "resource"}, events)
	require.ErrorIs(t, err, unexpectedErr)
	require.NotErrorIs(t, err, net.ErrClosed)
	require.NotErrorIs(t, err, io.EOF)
}

func startBlackholeSSHServer(t *testing.T) (string, <-chan net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	return listener.Addr().String(), accepted
}

func newBlackholeSSHClient(addr string) *Client {
	return &Client{
		hostPort: addr,
		cfg: &gossh.ClientConfig{
			User:            "test",
			Auth:            []gossh.AuthMethod{gossh.Password("test")},
			HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec
			Timeout:         time.Second,
		},
	}
}

func receiveAcceptedConnection(t *testing.T, accepted <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not connect to test server")
		return nil
	}
}

func assertConnectionCloses(t *testing.T, conn net.Conn) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not close the underlying connection")
	}
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
