// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec043_sftp_transfer_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	sshTestUser            = "sshtest"
	sshTestPassword        = "sshtestpass"
	sshTestHomeDir         = "/home/sshtest"
	sshContainerNamePrefix = "dagu-conformance-sftpd"
)

// sshTestImage is content-addressed from sshdDockerfile's own text: a tag
// that only depends on a mutable global would let a stale image from a
// previous, different version of sshdDockerfile silently satisfy the
// "already built" check below. Editing the Dockerfile therefore always
// produces a different tag -- a genuine cache hit only happens when the
// content is actually unchanged.
var sshTestImage = "dagu-conformance-sftpd:" + dockerfileTag(sshdDockerfile)

func dockerfileTag(dockerfile string) string {
	sum := sha256.Sum256([]byte(dockerfile))
	return hex.EncodeToString(sum[:])[:12]
}

// sshdDockerfile builds a minimal Alpine image running an SSH server with a
// single password-authenticated user, entirely self-contained so this
// package does not depend on any pre-existing local or public image.
// Alpine's openssh package enables the sftp subsystem
// ("Subsystem sftp internal-sftp") by default, so no extra sshd_config
// change is needed to support SFTP on top of the same image spec042's
// ssh.run tests use.
const sshdDockerfile = `FROM alpine:3
RUN apk add --no-cache openssh \
    && ssh-keygen -A \
    && adduser -D -h ` + sshTestHomeDir + ` -s /bin/sh ` + sshTestUser + ` \
    && echo '` + sshTestUser + `:` + sshTestPassword + `' | chpasswd \
    && sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config \
    && sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
`

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

// buildSSHTestImage builds sshTestImage from sshdDockerfile if an image
// under that (content-addressed) tag is not already present, so repeat
// test runs against an unchanged Dockerfile do not rebuild it.
func buildSSHTestImage(t *testing.T, dockerClient *client.Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := dockerClient.ImageInspect(ctx, sshTestImage); err == nil {
		return
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	contents := []byte(sshdDockerfile)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(contents))}); err != nil {
		t.Fatalf("writing tar header: %v", err)
	}
	if _, err := tw.Write(contents); err != nil {
		t.Fatalf("writing tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar writer: %v", err)
	}

	buildCtx, buildCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer buildCancel()
	result, err := dockerClient.ImageBuild(buildCtx, &buf, client.ImageBuildOptions{
		Tags: []string{sshTestImage},
	})
	if err != nil {
		t.Fatalf("building %s: %v", sshTestImage, err)
	}
	defer func() { _ = result.Body.Close() }()
	if _, err := io.Copy(io.Discard, result.Body); err != nil {
		t.Fatalf("reading build response for %s: %v", sshTestImage, err)
	}
}

// startSSHDContainer builds the test image if needed, starts a fresh sshd
// container -- under a name unique to this call, so concurrent test runs
// against the same Docker daemon never create, remove, or inspect each
// other's container -- publishing 22/tcp to a Docker-assigned host port,
// waits for it to accept TCP connections, and registers cleanup by
// container ID. It returns the host port Docker actually bound, read back
// after the container starts -- reserving a port locally and releasing it
// before container creation would leave a window for another process to
// claim that port first -- together with the container's ID, so callers
// can seed remote fixture state via execInContainer/putRemoteFile before
// a download test runs.
func startSSHDContainer(t *testing.T, dockerClient *client.Client) (hostPort int, containerID string) {
	t.Helper()

	buildSSHTestImage(t, dockerClient)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	containerName := uniqueContainerName(t)
	sshPort := network.MustParsePort("22/tcp")
	created, err := dockerClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:        sshTestImage,
			ExposedPorts: network.PortSet{sshPort: {}},
		},
		HostConfig: &container.HostConfig{
			AutoRemove: true,
			PortBindings: network.PortMap{
				// Empty HostPort: let Docker assign a free host port itself.
				sshPort: {{HostIP: netip.MustParseAddr("127.0.0.1")}},
			},
		},
		Name: containerName,
	})
	if err != nil {
		t.Fatalf("creating %s: %v", containerName, err)
	}
	containerID = created.ID
	t.Cleanup(func() {
		removeCtx, removeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer removeCancel()
		_, _ = dockerClient.ContainerRemove(removeCtx, containerID, client.ContainerRemoveOptions{Force: true})
	})

	if _, err := dockerClient.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("starting %s: %v", containerName, err)
	}

	hostPort = resolvedHostPort(t, ctx, dockerClient, containerID, sshPort)
	waitForTCP(t, hostPort)
	return hostPort, containerID
}

// uniqueContainerName returns a container name that cannot collide with
// another concurrently running invocation of startSSHDContainer, in this
// test binary or another one against the same Docker daemon.
func uniqueContainerName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a unique container name: %v", err)
	}
	return fmt.Sprintf("%s-%s", sshContainerNamePrefix, hex.EncodeToString(suffix))
}

// resolvedHostPort inspects the running container and returns the host
// port Docker bound for containerPort.
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

func waitForTCP(t *testing.T, port int) {
	t.Helper()

	addr := net.JoinHostPort("127.0.0.1", portString(port))
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("sshd never became reachable on %s", addr)
}

func portString(port int) string {
	return strconv.Itoa(port)
}

// putRemoteFile creates a file owned by sshTestUser inside the container at
// remotePath with the given content and permission mode, via a throwaway
// docker exec, so download tests have a file the SFTP session (which
// authenticates as sshTestUser) is actually permitted to read.
func putRemoteFile(t *testing.T, dockerClient *client.Client, containerID, remotePath, content string, mode int) {
	t.Helper()

	script := fmt.Sprintf(
		"set -e; mkdir -p \"$(dirname %q)\"; printf %%s %q > %q; chown %s:%s %q; chmod %o %q",
		remotePath, content, remotePath, sshTestUser, sshTestUser, remotePath, mode, remotePath,
	)
	execInContainer(t, dockerClient, containerID, []string{"sh", "-c", script})
}

// execInContainer runs cmd inside the container and fails the test if it
// exits non-zero, so remote fixture setup that silently no-ops (rather than
// producing the state a test relies on) is caught immediately instead of
// surfacing as a confusing assertion failure later.
func execInContainer(t *testing.T, dockerClient *client.Client, containerID string, cmd []string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	created, err := dockerClient.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		t.Fatalf("creating exec for %v: %v", cmd, err)
	}

	attached, err := dockerClient.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		t.Fatalf("attaching exec for %v: %v", cmd, err)
	}
	defer attached.Close()

	var output bytes.Buffer
	if _, err := io.Copy(&output, attached.Reader); err != nil {
		t.Fatalf("reading exec output for %v: %v", cmd, err)
	}

	inspected, err := dockerClient.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		t.Fatalf("inspecting exec for %v: %v", cmd, err)
	}
	if inspected.ExitCode != 0 {
		t.Fatalf("exec %v exited %d:\n%s", cmd, inspected.ExitCode, output.String())
	}
}
