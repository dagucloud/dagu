// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

const minioImage = "minio/minio:RELEASE.2024-10-02T17-50-41Z"

// TestMinIOContainer_WithMCCommands tests S3-like operations using MinIO's mc client
// inside a container. This validates the container-based workflow pattern for object storage.
func TestMinIOContainer_WithMCCommands(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()
	requireLinuxContainerRuntime(t)

	tempDir, err := os.MkdirTemp("", "dagu-s3-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	testContent := "Hello from S3 integration test!"
	downloadDest := fmt.Sprintf("%s/downloaded.txt", tempDir)

	th := test.Setup(t)

	// Use startup: command to run MinIO server, then execute mc commands inside the container
	dagConfig := fmt.Sprintf(`
type: graph
container:
  image: %s
  startup: command
  command: ["minio", "server", "/data", "--console-address", ":9001"]
  wait_for: running
  volumes:
    - %s:/host-data:rw
  env:
    - MINIO_ROOT_USER=minioadmin
    - MINIO_ROOT_PASSWORD=minioadmin

steps:
  # Wait for MinIO to be ready with retry loop, then create bucket
  - name: create-bucket
    run: sh -c "for i in 1 2 3 4 5; do mc alias set local http://127.0.0.1:9000 minioadmin minioadmin && break || sleep 1; done && mc mb local/test-bucket --ignore-existing"

  # Upload a file (use echo -n to avoid trailing newline)
  - name: upload-file
    run: sh -c "echo -n '%s' > /tmp/test.txt && mc cp /tmp/test.txt local/test-bucket/uploaded.txt"
    depends:
      - create-bucket

  # List objects
  - name: list-objects
    run: mc ls local/test-bucket/
    output: LIST_RESULT
    depends:
      - upload-file

  # Download file to host-mounted volume
  - name: download-file
    run: mc cp local/test-bucket/uploaded.txt /host-data/downloaded.txt
    depends:
      - list-objects

  # Verify content
  - name: verify-content
    run: cat /host-data/downloaded.txt
    output: DOWNLOADED_CONTENT
    depends:
      - download-file

  # Delete object
  - name: delete-object
    run: mc rm local/test-bucket/uploaded.txt
    depends:
      - verify-content

  # Verify deletion
  - name: verify-deletion
    run: sh -c "mc ls local/test-bucket/ | wc -l | tr -d ' '"
    output: OBJECT_COUNT
    depends:
      - delete-object
`, minioImage, tempDir, testContent)

	dag := th.DAG(t, dagConfig)
	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"DOWNLOADED_CONTENT": testContent,
		"OBJECT_COUNT":       "0",
	})

	// Verify the file was downloaded to the host
	content, err := os.ReadFile(downloadDest)
	require.NoError(t, err)
	require.Equal(t, testContent, string(content))
}
