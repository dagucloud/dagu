// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec046_postgres_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver, already a dagu dependency
)

const (
	postgresImage         = "postgres:16-alpine"
	postgresUser          = "dagutest"
	postgresPassword      = "dagutestpass"
	postgresDB            = "dagutest"
	containerNamePrefix   = "dagu-conformance-postgres"
	postgresReadyDeadline = 60 * time.Second
)

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

// pullPostgresImageIfMissing pulls the official postgres image only when it
// is not already present locally, so repeat test runs do not re-pull it.
func pullPostgresImageIfMissing(t *testing.T, dockerClient *client.Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := dockerClient.ImageInspect(ctx, postgresImage); err == nil {
		return
	}

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer pullCancel()
	rc, err := dockerClient.ImagePull(pullCtx, postgresImage, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("pulling %s: %v", postgresImage, err)
	}
	defer func() { _ = rc.Close() }()
	// Drain the pull progress stream; its content isn't needed.
	buf := make([]byte, 32*1024)
	for {
		if _, err := rc.Read(buf); err != nil {
			break
		}
	}
}

// startPostgresContainer starts a fresh, uniquely named postgres container,
// publishing 5432/tcp to a Docker-assigned host port, waits for it to
// actually accept a real SQL connection (not merely a TCP connection --
// postgres restarts itself partway through first-time initialization), and
// registers cleanup by container ID. It returns a ready-to-use DSN.
func startPostgresContainer(t *testing.T, dockerClient *client.Client) string {
	t.Helper()

	pullPostgresImageIfMissing(t, dockerClient)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	containerName := uniqueContainerName(t)
	pgPort := network.MustParsePort("5432/tcp")
	created, err := dockerClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: postgresImage,
			Env: []string{
				"POSTGRES_USER=" + postgresUser,
				"POSTGRES_PASSWORD=" + postgresPassword,
				"POSTGRES_DB=" + postgresDB,
			},
			ExposedPorts: network.PortSet{pgPort: {}},
		},
		HostConfig: &container.HostConfig{
			AutoRemove: true,
			PortBindings: network.PortMap{
				pgPort: {{HostIP: netip.MustParseAddr("127.0.0.1")}},
			},
		},
		Name: containerName,
	})
	if err != nil {
		t.Fatalf("creating %s: %v", containerName, err)
	}
	containerID := created.ID
	t.Cleanup(func() {
		removeCtx, removeCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer removeCancel()
		_, _ = dockerClient.ContainerRemove(removeCtx, containerID, client.ContainerRemoveOptions{Force: true})
	})

	if _, err := dockerClient.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("starting %s: %v", containerName, err)
	}

	hostPort := resolvedHostPort(t, ctx, dockerClient, containerID, pgPort)
	dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", postgresUser, postgresPassword, hostPort, postgresDB)
	waitForPostgresReady(t, dsn)
	return dsn
}

func uniqueContainerName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a unique container name: %v", err)
	}
	return fmt.Sprintf("%s-%s", containerNamePrefix, hex.EncodeToString(suffix))
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

// waitForPostgresReady polls with a real SQL connection and ping, not just a
// TCP dial, since the official postgres image accepts TCP connections
// briefly, restarts itself partway through first-time initialization, then
// becomes durably ready -- a bare TCP check would race that restart.
func waitForPostgresReady(t *testing.T, dsn string) {
	t.Helper()

	deadline := time.Now().Add(postgresReadyDeadline)
	var lastErr error
	for time.Now().Before(deadline) {
		db, err := sql.Open("pgx", dsn)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			lastErr = db.PingContext(pingCtx)
			cancel()
			_ = db.Close()
			if lastErr == nil {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("postgres never became ready within %s: %v", postgresReadyDeadline, lastErr)
}

// execSQL runs a setup statement directly against dsn, failing the test on
// error. Used to seed or verify database state that a fixture's own
// postgres.query/postgres.import step didn't create, the same way other
// conformance packages use a direct docker exec for fixture setup.
func execSQL(t *testing.T, dsn, query string, args ...any) {
	t.Helper()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening setup connection: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("executing setup query %q: %v", query, err)
	}
}

// queryInt runs a single-row, single-column integer query directly against
// dsn, failing the test on error. Used to verify database state after a
// fixture ran, the same way other conformance packages read back local
// files or remote command output to verify behavior.
func queryInt(t *testing.T, dsn, query string, args ...any) int {
	t.Helper()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening verification connection: %v", err)
	}
	defer func() { _ = db.Close() }()

	var result int
	if err := db.QueryRow(query, args...).Scan(&result); err != nil {
		t.Fatalf("executing verification query %q: %v", query, err)
	}
	return result
}
