// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
)

// TestSFTPExecutorIntegration tests SFTP executor with a real SSH server in Docker
func TestSFTPExecutorIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	requireLinuxContainerRuntime(t)

	th := test.Setup(t)

	dockerClient := requireDockerClient(t)

	// Start SSH server container (reuses helpers from ssh_test.go)
	sshServer := startSSHServer(t, th, dockerClient)
	defer stopSSHServer(t, th, dockerClient, sshServer)

	// Wait for SSH server to be ready
	waitForSSHReady(t, sshServer)

	t.Run("UploadFile", func(t *testing.T) {
		th := test.Setup(t)

		// Create local file to upload
		localDir := t.TempDir()
		localFile := filepath.Join(localDir, "upload_test.txt")
		err := os.WriteFile(localFile, []byte("sftp upload test content"), 0644)
		require.NoError(t, err, "failed to create local test file")

		// Upload file to remote
		dagConfig := fmt.Sprintf(`
type: graph
steps:
  - name: upload-file
    action: sftp.upload
    with:
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      source: "%s"
      destination: /tmp/uploaded_file.txt
  - name: verify-upload
    action: ssh.run
    with:
      command: cat /tmp/uploaded_file.txt
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      shell: /bin/sh
    output: UPLOAD_VERIFY
    depends:
      - upload-file
`, sshServer.hostPort, sshTestUser, sshServer.keyPath, localFile,
			sshServer.hostPort, sshTestUser, sshServer.keyPath)

		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"UPLOAD_VERIFY": "sftp upload test content",
		})
	})

	t.Run("DownloadFile", func(t *testing.T) {
		th := test.Setup(t)

		// Create file on remote first, then download
		downloadDir := t.TempDir()
		downloadPath := filepath.Join(downloadDir, "downloaded.txt")

		dagConfig := fmt.Sprintf(`
type: graph
steps:
  - name: create-remote-file
    action: ssh.run
    with:
      command: |
        echo "sftp download test content" > /tmp/download_test.txt
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      shell: /bin/sh
  - name: download-file
    action: sftp.download
    with:
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      source: /tmp/download_test.txt
      destination: "%s"
    depends:
      - create-remote-file
`, sshServer.hostPort, sshTestUser, sshServer.keyPath,
			sshServer.hostPort, sshTestUser, sshServer.keyPath, downloadPath)

		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)

		// Verify downloaded file contents
		content, err := os.ReadFile(downloadPath)
		require.NoError(t, err, "failed to read downloaded file")
		require.Equal(t, "sftp download test content\n", string(content))
	})

	t.Run("UploadDirectory", func(t *testing.T) {
		th := test.Setup(t)

		// Create local directory with files to upload
		localDir := t.TempDir()
		subDir := filepath.Join(localDir, "subdir")
		require.NoError(t, os.MkdirAll(subDir, 0755))

		// Create files in directory (with trailing newlines for realistic file content)
		require.NoError(t, os.WriteFile(filepath.Join(localDir, "file1.txt"), []byte("content1\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(localDir, "file2.txt"), []byte("content2\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested content\n"), 0644))

		// Upload directory to remote
		dagConfig := fmt.Sprintf(`
type: graph
steps:
  - name: upload-dir
    action: sftp.upload
    with:
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      source: "%s"
      destination: /tmp/uploaded_dir
  - name: verify-upload
    action: ssh.run
    with:
      command: |
        cat /tmp/uploaded_dir/file1.txt
        cat /tmp/uploaded_dir/subdir/nested.txt
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      shell: /bin/sh
    output: DIR_UPLOAD_VERIFY
    depends:
      - upload-dir
`, sshServer.hostPort, sshTestUser, sshServer.keyPath, localDir,
			sshServer.hostPort, sshTestUser, sshServer.keyPath)

		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"DIR_UPLOAD_VERIFY": "content1\nnested content",
		})
	})

	t.Run("DownloadDirectory", func(t *testing.T) {
		th := test.Setup(t)

		// Download directory from remote
		downloadDir := t.TempDir()
		downloadPath := filepath.Join(downloadDir, "downloaded_dir")

		dagConfig := fmt.Sprintf(`
type: graph
steps:
  - name: create-remote-dir
    action: ssh.run
    with:
      command: |
        mkdir -p /tmp/remote_dir/subdir
        echo "remote file1" > /tmp/remote_dir/file1.txt
        echo "remote nested" > /tmp/remote_dir/subdir/nested.txt
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      shell: /bin/sh
  - name: download-dir
    action: sftp.download
    with:
      host: 127.0.0.1
      port: "%s"
      user: %s
      key: "%s"
      strict_host_key: false
      source: /tmp/remote_dir
      destination: "%s"
    depends:
      - create-remote-dir
`, sshServer.hostPort, sshTestUser, sshServer.keyPath,
			sshServer.hostPort, sshTestUser, sshServer.keyPath, downloadPath)

		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)

		// Verify downloaded directory contents
		content1, err := os.ReadFile(filepath.Join(downloadPath, "file1.txt"))
		require.NoError(t, err, "failed to read downloaded file1.txt")
		require.Equal(t, "remote file1\n", string(content1))

		nested, err := os.ReadFile(filepath.Join(downloadPath, "subdir", "nested.txt"))
		require.NoError(t, err, "failed to read downloaded nested.txt")
		require.Equal(t, "remote nested\n", string(nested))
	})
}
