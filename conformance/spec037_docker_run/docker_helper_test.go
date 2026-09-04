// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec037_docker_run_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const testImage = "alpine:3"

// requireDockerDaemon skips the test unless a reachable Docker daemon is
// running Linux containers. testImage (alpine:3) is a Linux-only image, so
// a daemon in Windows-container mode -- reachable, but unable to run it --
// must skip too, rather than fail every test in this package with a
// platform mismatch.
func requireDockerDaemon(t *testing.T) *client.Client {
	t.Helper()

	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("Skipping Docker-backed conformance test: failed to create docker client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	info, err := dockerClient.Info(ctx, client.InfoOptions{})
	if err != nil {
		_ = dockerClient.Close()
		t.Skipf("Skipping Docker-backed conformance test: docker daemon unavailable: %v", err)
	}
	if info.Info.OSType != "linux" {
		_ = dockerClient.Close()
		t.Skipf("Skipping Docker-backed conformance test: requires a Linux container runtime, got %q", info.Info.OSType)
	}

	t.Cleanup(func() { _ = dockerClient.Close() })
	return dockerClient
}

// removeContainerIfExists force-removes a container by name or ID, ignoring
// a not-found error. Used as post-test cleanup.
func removeContainerIfExists(t *testing.T, dockerClient *client.Client, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = dockerClient.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
}

// uniqueContainerName returns a container name that cannot collide with
// another concurrently running invocation of this package's tests against
// the same Docker daemon, so force-removing it (defensively, or as
// cleanup) never touches a container that isn't this test's own.
func uniqueContainerName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a unique container name: %v", err)
	}
	return fmt.Sprintf("dagu-conformance-docker-run-%s", hex.EncodeToString(suffix))
}

// pullTestImage pulls testImage, so a test-created container (as opposed to
// one docker.run itself creates, which goes through its own pull-policy
// handling) does not depend on the image already being cached locally.
func pullTestImage(t *testing.T, ctx context.Context, dockerClient *client.Client) {
	t.Helper()

	reader, err := dockerClient.ImagePull(ctx, testImage, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("failed to pull image %s: %v", testImage, err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatalf("failed to read pull response for %s: %v", testImage, err)
	}
}

// startLongRunningContainer creates and starts a container under the given
// name so a fixture can exec into it, and waits until it reports Running.
// Cleanup is registered immediately once the container is created, so it
// runs even if ContainerStart or the readiness wait below fails -- neither
// of those failure paths should leak the container.
func startLongRunningContainer(t *testing.T, dockerClient *client.Client, name string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pullTestImage(t, ctx, dockerClient)

	created, err := dockerClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: testImage,
			Cmd:   []string{"sh", "-c", "while true; do sleep 3600; done"},
		},
		HostConfig: &container.HostConfig{AutoRemove: false},
		Name:       name,
	})
	if err != nil {
		t.Fatalf("failed to create container %s: %v", name, err)
	}
	t.Cleanup(func() { removeContainerIfExists(t, dockerClient, created.ID) })

	if _, err := dockerClient.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("failed to start container %s: %v", name, err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := dockerClient.ContainerInspect(ctx, created.ID, client.ContainerInspectOptions{})
		if err == nil && resp.Container.State != nil && resp.Container.State.Running {
			return created.ID
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("container %s did not become running in time", name)
	return ""
}

// containerExists reports whether a container with the given name is still
// known to the daemon (running or not). A genuine "not found" is the only
// error treated as "does not exist"; any other error (daemon unreachable,
// permission denied, timeout, and similar) fails the test instead of being
// silently read as "container is gone", which could otherwise turn an
// unrelated infrastructure problem into a misleading assertion failure.
func containerExists(t *testing.T, dockerClient *client.Client, name string) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := dockerClient.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err == nil {
		return true
	}
	if errdefs.IsNotFound(err) {
		return false
	}
	t.Fatalf("inspecting container %s: %v", name, err)
	return false
}
