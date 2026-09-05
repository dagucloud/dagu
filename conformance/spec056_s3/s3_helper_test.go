// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec056_s3_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

const (
	minioRootUser        = "minioadmin"
	minioRootPassword    = "minioadminpass"
	minioContainerPrefix = "dagu-conformance-minio"
	// minioImage is pinned by digest (a multi-arch manifest list) for a
	// reproducible test environment across runs and platforms.
	minioImage = "minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// lastStepStdout reads the exact bytes the last step in the run wrote to
// stdout, by locating that step's captured-output log file from dagu
// start's own tree render (the last "stdout:" line in a multi-step run)
// and reading it directly, since the tree render re-wraps long lines with
// its own indentation, which would corrupt a strict content match.
func lastStepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	matches := stdoutLogPattern.FindAllStringSubmatch(daguStartOutput, -1)
	require.NotEmptyf(t, matches, "expected a stdout log path in output:\n%s", daguStartOutput)
	path := strings.TrimSpace(matches[len(matches)-1][1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

func requireDockerDaemon(t *testing.T) *client.Client {
	t.Helper()

	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("Skipping Docker-backed conformance test: failed to create docker client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := dockerClient.Info(ctx, client.InfoOptions{}); err != nil {
		_ = dockerClient.Close()
		t.Skipf("Skipping Docker-backed conformance test: docker daemon unavailable: %v", err)
	}

	t.Cleanup(func() { _ = dockerClient.Close() })
	return dockerClient
}

// startMinIOContainer starts a fresh MinIO container -- under a name unique
// to this call, so concurrent test runs against the same Docker daemon
// never create, remove, or inspect each other's container -- publishing
// 9000/tcp to a Docker-assigned host port, waits for its health endpoint to
// answer, and registers cleanup by container ID. It returns the host port
// Docker actually bound, read back after the container starts.
func startMinIOContainer(t *testing.T, dockerClient *client.Client) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	containerName := uniqueContainerName(t)
	apiPort := network.MustParsePort("9000/tcp")
	created, err := dockerClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: minioImage,
			Env: []string{
				"MINIO_ROOT_USER=" + minioRootUser,
				"MINIO_ROOT_PASSWORD=" + minioRootPassword,
			},
			Cmd:          []string{"server", "/data"},
			ExposedPorts: network.PortSet{apiPort: {}},
		},
		HostConfig: &container.HostConfig{
			AutoRemove: true,
			PortBindings: network.PortMap{
				// Empty HostPort: let Docker assign a free host port itself.
				apiPort: {{HostIP: netip.MustParseAddr("127.0.0.1")}},
			},
		},
		Name: containerName,
	})
	if err != nil {
		t.Fatalf("creating %s: %v", containerName, err)
	}
	containerID := created.ID
	t.Cleanup(func() {
		removeCtx, removeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer removeCancel()
		_, _ = dockerClient.ContainerRemove(removeCtx, containerID, client.ContainerRemoveOptions{Force: true})
	})

	if _, err := dockerClient.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("starting %s: %v", containerName, err)
	}

	hostPort := resolvedHostPort(t, ctx, dockerClient, containerID, apiPort)
	waitForMinIO(t, hostPort)
	return hostPort
}

func uniqueContainerName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a unique container name: %v", err)
	}
	return fmt.Sprintf("%s-%s", minioContainerPrefix, hex.EncodeToString(suffix))
}

func resolvedHostPort(t *testing.T, ctx context.Context, dockerClient *client.Client, containerID string, containerPort network.Port) int {
	t.Helper()

	inspected, err := dockerClient.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("inspecting %s: %v", containerID, err)
	}
	if inspected.Container.NetworkSettings == nil {
		t.Fatalf("%s has no network settings", containerID)
	}
	bindings := inspected.Container.NetworkSettings.Ports[containerPort]
	if len(bindings) == 0 {
		t.Fatalf("%s has no host binding for %s", containerID, containerPort)
	}
	hostPort, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		t.Fatalf("parsing bound host port %q: %v", bindings[0].HostPort, err)
	}
	return hostPort
}

func waitForMinIO(t *testing.T, port int) {
	t.Helper()

	url := fmt.Sprintf("http://127.0.0.1:%d/minio/health/live", port)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:gosec,noctx // fixed local test URL.
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("MinIO never became healthy on %s", url)
}

// s3Endpoint returns the http://127.0.0.1:<port> endpoint URL for a MinIO
// container started by startMinIOContainer.
func s3Endpoint(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// createTestBucket creates a fresh, uniquely named bucket in the MinIO
// instance listening on port, so parallel subtests never see each other's
// objects, and returns its name.
func createTestBucket(t *testing.T, port int) string {
	t.Helper()

	bucket := "conformance-" + uniqueSuffix(t)
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(minioRootUser, minioRootPassword, "")),
	}
	s3Client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(s3Endpoint(port))
		o.UsePathStyle = true
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s3Client.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoErrorf(t, err, "creating bucket %s", bucket)
	return bucket
}

func uniqueSuffix(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a unique suffix: %v", err)
	}
	return hex.EncodeToString(suffix)
}
