// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
)

const (
	// Use alpine with openssh for minimal SSH server
	sshServerImage = "alpine:3"
	sshTestUser    = "testuser"
	sshTestPass    = "testpass123"
)

// sshServerContainer holds info about a running SSH server container
type sshServerContainer struct {
	containerID string
	hostPort    string
	keyPath     string
	pubKeyPath  string
	workDir     string // working directory for test files
}

// sshConfig returns the common SSH configuration block for DAG tests.
func (s *sshServerContainer) sshConfig(shell string) string {
	return fmt.Sprintf(`ssh:
  host: 127.0.0.1
  port: "%s"
  user: %s
  key: "%s"
  strict_host_key: false
  shell: %s
`, s.hostPort, sshTestUser, s.keyPath, shell)
}

// sshConfigNoShell returns SSH configuration without an explicit shell.
func (s *sshServerContainer) sshConfigNoShell() string {
	return fmt.Sprintf(`ssh:
  host: 127.0.0.1
  port: "%s"
  user: %s
  key: "%s"
  strict_host_key: false
`, s.hostPort, sshTestUser, s.keyPath)
}

// sshPasswordConfig returns SSH configuration using password authentication.
func (s *sshServerContainer) sshPasswordConfig(shell string) string {
	return fmt.Sprintf(`ssh:
  host: 127.0.0.1
  port: "%s"
  user: %s
  password: "%s"
  strict_host_key: false
  shell: %s
`, s.hostPort, sshTestUser, sshTestPass, shell)
}

// TestSSHExecutorIntegration tests SSH executor with a real SSH server in Docker
func TestSSHExecutorIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	requireLinuxContainerRuntime(t)

	th := test.Setup(t)

	dockerClient := requireDockerClient(t)

	// Start SSH server container
	sshServer := startSSHServer(t, th, dockerClient)
	defer stopSSHServer(t, th, dockerClient, sshServer)

	// Wait for SSH server to be ready
	waitForSSHReady(t, sshServer)

	t.Run("BasicCommandExecution", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: basic-ssh
    action: ssh.run
    with:
      command: echo "hello from ssh"
    output: SSH_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"SSH_OUT": "hello from ssh",
		})
	})

	t.Run("CommandWithArguments", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: args-test
    action: ssh.run
    with:
      command: echo hello world
    output: SSH_ARGS_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"SSH_ARGS_OUT": "hello world",
		})
	})

	t.Run("WorkingDirectory", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: workdir-test
    action: ssh.run
    with:
      command: pwd
    working_dir: /tmp
    output: SSH_PWD_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"SSH_PWD_OUT": "/tmp",
		})
	})

	t.Run("ScriptExecution", func(t *testing.T) {
		th := test.Setup(t)

		// Test multi-line script execution
		// Note: Avoid shell variables with ${} as dagu expands them before sending to SSH
		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: script-test
    action: ssh.run
    with:
      command: |
        echo -n "hello "
        echo "world"
    output: SSH_SCRIPT_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"SSH_SCRIPT_OUT": "hello world",
		})
	})

	t.Run("ScriptWithWorkingDirectory", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: script-workdir-test
    action: ssh.run
    with:
      command: |
        echo "working in $(pwd)"
    working_dir: /tmp
    output: SSH_SCRIPT_WORKDIR_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"SSH_SCRIPT_WORKDIR_OUT": "working in /tmp",
		})
	})

	t.Run("ErrorHandling_CommandFailure", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: error-test
    action: ssh.run
    with:
      command: exit 1
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunError(t)
	})

	t.Run("ErrorHandling_InvalidWorkingDirectory", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: invalid-dir-test
    action: ssh.run
    working_dir: /nonexistent/directory/path
    with:
      command: echo "should not reach"
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunError(t)
	})

	t.Run("StepLevelSSHConfig", func(t *testing.T) {
		th := test.Setup(t)

		// Test step-level SSH configuration (no DAG-level ssh config)
		dagConfig := fmt.Sprintf(`
steps:
  - name: step-ssh-config
    action: ssh.run
    with:
      command: echo "step config works"
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      shell: /bin/sh
    output: STEP_SSH_OUT
`, sshServer.hostPort, sshTestUser, sshServer.keyPath)

		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"STEP_SSH_OUT": "step config works",
		})
	})

	t.Run("BashShell", func(t *testing.T) {
		th := test.Setup(t)

		// Test that bash shell configuration works
		// Verifies the shell config is being applied by running a simple script
		dagConfig := sshServer.sshConfig("/bin/bash") + `
steps:
  - name: bash-test
    action: ssh.run
    with:
      command: |
        echo "bash test"
    output: SSH_BASH_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"SSH_BASH_OUT": "bash test",
		})
	})

	t.Run("NoWorkingDir_UsesHomeDirectory", func(t *testing.T) {
		th := test.Setup(t)

		// Test that when no step.Dir is set, SSH runs in user's home directory
		// Note: DAG-level workingDir is NOT used for SSH (it's for local execution)
		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: home-dir-test
    action: ssh.run
    with:
      command: pwd
    output: SSH_HOME_DIR_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Should be the SSH user's home directory (e.g., /home/testuser)
		dag.AssertOutputs(t, map[string]any{
			"SSH_HOME_DIR_OUT": test.Contains("/home/"),
		})
	})

	t.Run("StepWorkingDirOverridesDAGWorkingDir", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := "working_dir: /var\n\n" + sshServer.sshConfig("/bin/sh") + `
steps:
  - name: step-override-test
    action: ssh.run
    with:
      command: pwd
    working_dir: /tmp
    output: SSH_OVERRIDE_WORKDIR_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"SSH_OVERRIDE_WORKDIR_OUT": "/tmp",
		})
	})

	t.Run("PipeInScript", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: pipe-test
    action: ssh.run
    with:
      command: |
        echo "hello" | tr 'h' 'H'
    output: SSH_PIPE_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"SSH_PIPE_OUT": "Hello",
		})
	})

	t.Run("CommandSubstitution", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: subst-test
    action: ssh.run
    with:
      command: echo "hostname is $(hostname)"
    output: SSH_SUBST_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Just verify it ran - hostname will vary
	})

	t.Run("SetEStopsOnFirstError", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: set-e-test
    action: ssh.run
    with:
      command: |
        false
        echo "should not reach"
    output: SSH_SETE_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunError(t)
	})

	t.Run("PasswordAuthentication", func(t *testing.T) {
		th := test.Setup(t)

		// Use password authentication instead of key-based auth
		dagConfig := sshServer.sshPasswordConfig("/bin/sh") + `
steps:
  - name: password-auth-test
    action: ssh.run
    with:
      command: echo "authenticated with password"
    output: PASSWORD_AUTH_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"PASSWORD_AUTH_OUT": "authenticated with password",
		})
	})

	t.Run("OSEnvVarNotExpandedLocally", func(t *testing.T) {
		th := test.Setup(t)

		// $HOME must NOT be expanded to the local machine's home directory.
		// The remote shell should resolve it to the SSH user's home directory.
		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: remote-home
    action: ssh.run
    with:
      command: echo $HOME
    output: REMOTE_HOME
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Remote $HOME should be /home/testuser, not the local $HOME
		dag.AssertOutputs(t, map[string]any{
			"REMOTE_HOME": fmt.Sprintf("/home/%s", sshTestUser),
		})
	})

	t.Run("DollarEscape_Command_WithShell", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: escape-command-shell
    action: ssh.run
    with:
      command: echo "\$HOME"
    output: ESCAPE_CMD_SHELL_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"ESCAPE_CMD_SHELL_OUT": "$HOME",
		})
	})

	t.Run("DollarEscape_Command_WithoutShell", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfigNoShell() + `
steps:
  - name: escape-command-noshell
    action: ssh.run
    with:
      command: echo "\$HOME"
    output: ESCAPE_CMD_NOSHELL_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"ESCAPE_CMD_NOSHELL_OUT": fmt.Sprintf("/home/%s", sshTestUser),
		})
	})

	t.Run("DollarEscape_Script_WithShell", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfig("/bin/sh") + `
steps:
  - name: escape-script-shell
    action: ssh.run
    with:
      command: |
        echo "\$HOME"
    output: ESCAPE_SCRIPT_SHELL_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"ESCAPE_SCRIPT_SHELL_OUT": "$HOME",
		})
	})

	t.Run("DollarEscape_Script_WithoutShell", func(t *testing.T) {
		th := test.Setup(t)

		dagConfig := sshServer.sshConfigNoShell() + `
steps:
  - name: escape-script-noshell
    action: ssh.run
    with:
      command: |
        echo "\$HOME"
    output: ESCAPE_SCRIPT_NOSHELL_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"ESCAPE_SCRIPT_NOSHELL_OUT": fmt.Sprintf("/home/%s", sshTestUser),
		})
	})

	t.Run("DAGScopedVarExpandedInSSH", func(t *testing.T) {
		th := test.Setup(t)

		// Variables defined in the DAG env scope SHOULD be expanded,
		// while OS-only variables like $USER should be left for the remote shell.
		dagConfig := sshServer.sshConfig("/bin/sh") + `
env:
  GREETING: hello
steps:
  - name: mixed-vars
    action: ssh.run
    with:
      command: echo "$GREETING from $USER"
    output: MIXED_OUT
`
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// GREETING is DAG-scoped → expanded by Dagu to "hello"
		// $USER is OS-only → left for the remote shell → resolves to sshTestUser
		dag.AssertOutputs(t, map[string]any{
			"MIXED_OUT": fmt.Sprintf("hello from %s", sshTestUser),
		})
	})

	t.Run("TimeoutConfiguration", func(t *testing.T) {
		th := test.Setup(t)

		// Test that custom timeout configuration works
		dagConfig := fmt.Sprintf(`ssh:
  host: 127.0.0.1
  port: "%s"
  user: %s
  key: "%s"
  strict_host_key: false
  shell: /bin/sh
  timeout: "10s"
steps:
  - name: timeout-config-test
    action: ssh.run
    with:
      command: echo "timeout configured"
    output: TIMEOUT_OUT
`, sshServer.hostPort, sshTestUser, sshServer.keyPath)
		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"TIMEOUT_OUT": "timeout configured",
		})
	})

}

// startSSHServer creates and starts an SSH server container
func startSSHServer(t *testing.T, th test.Helper, dockerClient *client.Client) *sshServerContainer {
	t.Helper()

	ctx := th.Context

	// Get Docker info for platform
	platform, err := currentDockerPlatform(ctx, dockerClient)
	require.NoError(t, err, "failed to get docker info")

	// Pull the image
	pullOpts := client.ImagePullOptions{Platforms: []specs.Platform{platform}}
	reader, err := dockerClient.ImagePull(ctx, sshServerImage, pullOpts)
	require.NoError(t, err, "failed to pull ssh server image")
	_, _ = io.Copy(io.Discard, reader)
	_ = reader.Close()

	// Create temp directory for SSH keys
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	pubKeyPath := keyPath + ".pub"

	// Generate SSH key pair using Go crypto
	generateSSHKey(t, keyPath, pubKeyPath)

	// Read the public key
	pubKey, err := os.ReadFile(pubKeyPath)
	require.NoError(t, err, "failed to read public key")

	// Create container config
	containerName := fmt.Sprintf("dagu-ssh-test-%d", time.Now().UnixNano())

	// Setup script to configure SSH server
	// Uses shell variables to reduce repetition
	setupScript := fmt.Sprintf(`
set -e
USER="%s"
PASS="%s"
PUBKEY='%s'

apk add --no-cache openssh bash
ssh-keygen -A

adduser -D -s /bin/bash "$USER"
echo "$USER:$PASS" | chpasswd

mkdir -p "/home/$USER/.ssh"
echo "$PUBKEY" > "/home/$USER/.ssh/authorized_keys"
chmod 700 "/home/$USER/.ssh"
chmod 600 "/home/$USER/.ssh/authorized_keys"
chown -R "$USER:$USER" "/home/$USER/.ssh"

sed -i 's/#PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config
sed -i 's/#PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config

exec /usr/sbin/sshd -D -e
`, sshTestUser, sshTestPass, string(pubKey))

	// Create container
	created, err := dockerClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: sshServerImage,
			Cmd:   []string{"/bin/sh", "-c", setupScript},
			ExposedPorts: network.PortSet{
				network.MustParsePort("22/tcp"): struct{}{},
			},
		},
		HostConfig: &container.HostConfig{
			AutoRemove: true,
			PortBindings: network.PortMap{
				network.MustParsePort("22/tcp"): []network.PortBinding{
					{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"}, // Random port
				},
			},
		},
		NetworkingConfig: &network.NetworkingConfig{},
		Name:             containerName,
	})
	require.NoError(t, err, "failed to create SSH server container")

	// Start container
	_, err = dockerClient.ContainerStart(ctx, created.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "failed to start SSH server container")

	// Get the assigned port
	inspect, err := inspectContainer(ctx, dockerClient, created.ID)
	require.NoError(t, err, "failed to inspect SSH server container")

	hostPort := inspect.NetworkSettings.Ports[network.MustParsePort("22/tcp")][0].HostPort

	return &sshServerContainer{
		containerID: created.ID,
		hostPort:    hostPort,
		keyPath:     keyPath,
		pubKeyPath:  pubKeyPath,
		workDir:     keyDir,
	}
}

// generateSSHKey generates an ED25519 SSH key pair using Go crypto library
func generateSSHKey(t *testing.T, keyPath, pubKeyPath string) {
	t.Helper()

	// Generate ED25519 key pair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err, "failed to generate ed25519 key")

	// Marshal private key to OpenSSH format
	privKeyBytes, err := ssh.MarshalPrivateKey(privKey, "")
	require.NoError(t, err, "failed to marshal private key")

	// Write private key
	err = os.WriteFile(keyPath, pem.EncodeToMemory(privKeyBytes), 0600)
	require.NoError(t, err, "failed to write private key")

	// Generate SSH public key
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	require.NoError(t, err, "failed to create SSH public key")

	// Write public key in authorized_keys format
	pubKeyData := ssh.MarshalAuthorizedKey(sshPubKey)
	err = os.WriteFile(pubKeyPath, pubKeyData, 0644)
	require.NoError(t, err, "failed to write public key")
}

// stopSSHServer stops and removes the SSH server container
func stopSSHServer(t *testing.T, th test.Helper, dockerClient *client.Client, server *sshServerContainer) {
	t.Helper()

	timeout := 5
	_, _ = dockerClient.ContainerStop(th.Context, server.containerID, client.ContainerStopOptions{Timeout: &timeout})
	_, _ = dockerClient.ContainerRemove(th.Context, server.containerID, client.ContainerRemoveOptions{Force: true})
}

// waitForSSHReady waits for the SSH server to be ready to accept connections
// and verifies that commands can be executed via shell stdin.
func waitForSSHReady(t *testing.T, server *sshServerContainer) {
	t.Helper()

	config := buildSSHClientConfig(t, server)
	addr := net.JoinHostPort("127.0.0.1", server.hostPort)

	require.Eventually(t, func() bool {
		return trySSHConnection(t, addr, config)
	}, 30*time.Second, time.Second, "SSH server failed to become ready on port %s", server.hostPort)

	t.Logf("SSH server ready on port %s", server.hostPort)
}

// buildSSHClientConfig creates an SSH client config for testing.
func buildSSHClientConfig(t *testing.T, server *sshServerContainer) *ssh.ClientConfig {
	t.Helper()

	keyBytes, err := os.ReadFile(server.keyPath)
	require.NoError(t, err, "failed to read private key for connection test")

	signer, err := ssh.ParsePrivateKey(keyBytes)
	require.NoError(t, err, "failed to parse private key for connection test")

	return &ssh.ClientConfig{
		User:            sshTestUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
}

// trySSHConnection attempts to connect and run a test command.
func trySSHConnection(t *testing.T, addr string, config *ssh.ClientConfig) bool {
	t.Helper()

	conn, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		t.Logf("Waiting for SSH server: %v", err)
		return false
	}
	defer func() { _ = conn.Close() }()

	session, err := conn.NewSession()
	if err != nil {
		t.Logf("SSH session creation failed: %v", err)
		return false
	}
	defer func() { _ = session.Close() }()

	session.Stdin = strings.NewReader("__dagu_exec(){\nset -e\necho test\n}\n__dagu_exec\n")

	var stdout, stderr strings.Builder
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err = session.Run("/bin/sh"); err != nil || strings.TrimSpace(stdout.String()) != "test" {
		t.Logf("SSH shell stdin test failed: stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), err)
		return false
	}
	return true
}
