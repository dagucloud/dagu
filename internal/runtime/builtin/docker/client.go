// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/signal"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Errors for container
var (
	ErrImageOrContainerShouldNotBeEmpty = errors.New("container_name or image must be specified")
	ErrImageRequired                    = errors.New("image is required")
	ErrInvalidVolumeFormat              = errors.New("invalid volume format")
	ErrInvalidPortFormat                = errors.New("invalid port format")
	ErrContainerIsNotRunning            = errors.New("container is not running")
	// Validation errors for docker executor map config
	ErrExecOnlyWithContainerName       = errors.New("'exec' options require 'container_name' (exec-in-existing mode)")
	ErrInvalidOptionsWithContainerName = errors.New("'container', 'host', 'network', 'pull', 'platform', or 'auto_remove' not supported with 'container_name'")
)

// Constants for container operations
const (
	// errorExitCode is the exit code to return when an error occurs and we
	// cannot get a more specific code
	errorExitCode = 1

	// Default timeout values
	defaultReadinessTimeout = 120 * time.Second
	defaultPollInterval     = 500 * time.Millisecond

	// Container runtime detection files
	dockerEnvFile    = "/.dockerenv"
	podmanEnvFile    = "/run/.containerenv"
	proc1CgroupFile  = "/proc/1/cgroup"
	dockerSocketFile = "/var/run/docker.sock"

	// Keepalive settings
	keepAliveSleepCmd   = "while true; do sleep 86400; done"
	keepAliveTargetPath = "/__dagu_runner/keepalive"

	// Marks a cancelable exec so the daemon can signal it from inside the container.
	execCancelTokenEnv = "DAGU_EXEC_TOKEN"

	// Log scanning buffer sizes
	logScanInitialBuf = 64 * 1024
	logScanMaxBuf     = 1024 * 1024
)

var containerCgroupIDPattern = regexp.MustCompile(
	`(^|/)((docker|libpod|cri-containerd)-[0-9a-f]{12,64}\.scope|(docker|containerd|libpod)/[0-9a-f]{12,64})($|/)`,
)

type cancelHelperMode uint8

const (
	cancelHelperNone cancelHelperMode = iota
	cancelHelperBound
	cancelHelperCopied
)

type Client struct {
	cfg *Config

	platform       specs.Platform // resolved platform
	containerID    string         // ID of the running container (if any)
	started        atomic.Bool
	cleanupPending atomic.Bool

	mu  sync.Mutex
	cli *client.Client

	keepAliveTmp string
	cancelHelper cancelHelperMode

	// authManager handles registry authentication
	authManager *RegistryAuthManager

	cancelMu sync.Mutex
	cancel   func()
}

// ExecOptions specifies options to execute commands in the container.
type ExecOptions struct {
	// WorkingDir overrides the working directory for the exec command.
	WorkingDir string
	// Env adds or overrides environment variables for this exec command.
	Env []string
	// Direct executes cmd as argv without applying the configured shell wrapper.
	Direct bool
	// TerminateOnCancel attempts to terminate only the exec process when ctx is canceled.
	TerminateOnCancel bool
}

func inspectContainer(ctx context.Context, cli *client.Client, containerID string) (container.InspectResponse, error) {
	result, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, err
	}
	return result.Container, nil
}

// daemonClientOpts builds the Moby client options for the given daemon host.
//
// Empty host is the Docker default and is exactly client.FromEnv — byte-identical
// to upstream behavior (honoring DOCKER_HOST, DOCKER_CERT_PATH, DOCKER_API_VERSION).
//
// A non-empty host is the service-selected runtime (podman's Docker-compatible
// socket via DAGU_CONTAINER_RUNTIME). It deliberately does NOT use client.FromEnv,
// which is WithTLSClientConfigFromEnv + WithHostFromEnv + WithAPIVersionFromEnv:
//   - WithTLSClientConfigFromEnv would couple the selected plain socket to Docker
//     TLS env (DOCKER_CERT_PATH) — making client.New pick scheme=https, and failing
//     client construction outright if those cert files are stale/missing.
//   - WithHostFromEnv (DOCKER_HOST) must not override the explicit selection.
//
// So the selected-host client is built from only the intended pieces: the host, a
// pinned http scheme for the plain Docker-compatible socket, and DOCKER_API_VERSION
// negotiation.
func daemonClientOpts(daemonHost string) []client.Opt {
	if host := strings.TrimSpace(daemonHost); host != "" {
		return []client.Opt{
			client.WithHost(host),
			client.WithScheme("http"),
			client.WithAPIVersionFromEnv(),
		}
	}
	return []client.Opt{client.FromEnv}
}

// InitializeClient creates a new container client
func InitializeClient(ctx context.Context, cfg *Config) (*Client, error) {
	logger.Debug(ctx, "Docker: InitializeClient started",
		slog.String("image", cfg.Image),
		slog.String("containerName", cfg.ContainerName),
		slog.Bool("autoRemove", cfg.AutoRemove),
	)

	dockerCli, err := client.New(daemonClientOpts(cfg.DaemonHost)...)
	if err != nil {
		logger.Error(ctx, "Docker: failed to create docker client", tag.Error(err))
		return nil, err
	}
	logger.Debug(ctx, "Docker: docker client created successfully")

	platform, err := getPlatform(ctx, dockerCli, cfg)
	if err != nil {
		logger.Error(ctx, "Docker: failed to get platform", tag.Error(err))
		return nil, err
	}
	logger.Debug(ctx, "Docker: platform resolved",
		slog.String("os", platform.OS),
		slog.String("arch", platform.Architecture),
	)

	// Check if the container is running when containerName is specified
	var ctID string
	var name = strings.TrimSpace(cfg.ContainerName)
	if name != "" {
		if cfg.Image == "" {
			// Exec mode: wait for container to be running with timeout
			waitCtx, cancel := context.WithTimeout(ctx, defaultReadinessTimeout)
			defer cancel()

			if err := waitForContainerRunning(waitCtx, dockerCli, name); err != nil {
				return nil, fmt.Errorf("container %q: %w", name, err)
			}

			// Get container ID after it's running
			info, err := inspectContainer(ctx, dockerCli, name)
			if err != nil {
				return nil, fmt.Errorf("failed to inspect container %q: %w", name, err)
			}
			ctID = info.ID
		} else {
			// Image mode with name: check existing container
			info, inspectErr := inspectContainer(ctx, dockerCli, name)
			isRunning, err := isContainerRunning(info, inspectErr)
			if err != nil {
				return nil, fmt.Errorf("failed to check if container %q is running: %w", name, err)
			}
			if info.ID != "" {
				ctID = info.ID
			}
			if !isRunning {
				// Preserve the stopped container ID so Run can remove it before re-creating
				// the named container without hitting a Docker name conflict.
				ctID = info.ID
			}
		}
	}

	logger.Debug(ctx, "Docker: InitializeClient completed successfully",
		slog.String("containerID", ctID),
	)
	return &Client{
		cfg:         cfg,
		containerID: ctID,
		cli:         dockerCli,
		platform:    platform,
		authManager: cfg.AuthManager,
	}, nil
}

// waitForContainerRunning waits for a container to be in running state with timeout
func waitForContainerRunning(ctx context.Context, cli *client.Client, name string) error {
	// Check immediately before starting the polling loop
	info, err := inspectContainer(ctx, cli, name)
	if err == nil && info.State != nil && info.State.Running {
		return nil
	}
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	// Log that we're waiting for the container
	lastStatus := getContainerStatus(info, err)
	logger.Info(ctx, "Waiting for container to be running",
		slog.String("container", name),
		slog.String("currentStatus", lastStatus),
		slog.Duration("timeout", defaultReadinessTimeout),
	)

	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	// Log progress every 10 seconds
	logInterval := 10 * time.Second
	lastLogTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return newContainerWaitTimeoutError(cli, name)
		case <-ticker.C:
			info, err := inspectContainer(ctx, cli, name)
			if err != nil {
				if errdefs.IsNotFound(err) {
					// Log progress periodically
					if time.Since(lastLogTime) >= logInterval {
						logger.Info(ctx, "Container not found, still waiting...",
							slog.String("container", name),
						)
						lastLogTime = time.Now()
					}
					continue // Container doesn't exist yet, keep waiting
				}
				// If the context has already expired while inspecting, treat it as timeout
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return newContainerWaitTimeoutError(cli, name)
				}
				return fmt.Errorf("failed to inspect container: %w", err)
			}
			if info.State != nil && info.State.Running {
				logger.Info(ctx, "Container is now running",
					slog.String("container", name),
				)
				return nil
			}
			// Log progress periodically with current status
			if time.Since(lastLogTime) >= logInterval {
				status := "unknown"
				if info.State != nil {
					status = string(info.State.Status)
				}
				logger.Info(ctx, "Container not running yet, still waiting...",
					slog.String("container", name),
					slog.String("status", status),
				)
				lastLogTime = time.Now()
			}
		}
	}
}

func newContainerWaitTimeoutError(cli *client.Client, name string) error {
	// Get final state for better error message
	finalInfo, finalErr := inspectContainer(context.Background(), cli, name)
	finalStatus := getContainerStatus(finalInfo, finalErr)
	return fmt.Errorf("timed out waiting for container to be running (current status: %s)", finalStatus)
}

// getContainerStatus returns a human-readable status string for the container
func getContainerStatus(info container.InspectResponse, err error) string {
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "not found"
		}
		return fmt.Sprintf("error: %v", err)
	}

	state := info.State
	if state == nil {
		return "unknown"
	}
	if state.Running {
		return "running"
	}
	if state.Status == "" {
		return "not running"
	}
	if state.ExitCode != 0 {
		return fmt.Sprintf("%s (exit code: %d)", state.Status, state.ExitCode)
	}
	return string(state.Status)
}

// Close closes the container client and cleans up resources
func (c *Client) Close(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cli == nil {
		return
	}

	// Remove a container this client started under auto-remove, and any container
	// an earlier cleanup attempt failed to remove.
	shouldRemove := (c.cfg.AutoRemove && c.started.Load()) || c.cleanupPending.Load()
	if shouldRemove && c.containerID != "" {
		if removeContainerForCleanup(ctx, c.cli, c.containerID, client.ContainerRemoveOptions{Force: true}, defaultCancelStopWait) {
			c.clearContainerStateLocked(c.containerID)
		}
	}

	c.removeKeepAliveTmp(ctx)

	_ = c.cli.Close()
	c.cli = nil
}

// Exec executes the command in the running container
func (c *Client) Exec(ctx context.Context, cmd []string, stdout, stderr io.Writer, opts ExecOptions) (int, error) {
	c.mu.Lock()
	if c.containerID == "" {
		c.mu.Unlock()
		return 1, ErrContainerIsNotRunning
	}
	cli := c.cli
	c.mu.Unlock()

	return c.execInContainer(ctx, cli, cmd, stdout, stderr, opts)
}

// CreateContainerKeepAlive creates the container that lives while the DAG running
func (c *Client) CreateContainerKeepAlive(ctx context.Context) error {
	if c.containerID != "" {
		return fmt.Errorf("container already exists. id=%s", c.containerID)
	}

	// Check if a container with the specified name already exists
	if name := c.cfg.ContainerName; name != "" {
		info, err := inspectContainer(ctx, c.cli, name)
		if err == nil {
			// Container exists - fail regardless of state
			if info.State != nil && info.State.Running {
				return fmt.Errorf("container with name %q already exists and is running", name)
			}
			return fmt.Errorf("container with name %q already exists", name)
		}
		// If error is not "not found", it's an unexpected error
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to check existing container %q: %w", name, err)
		}
		// Container doesn't exist, proceed to create
	}

	// Choose startup mode and command
	var cmd []string
	mode := c.cfg.Startup
	if mode == "" {
		mode = "keepalive"
	}

	switch mode {
	case "keepalive":
		if len(c.cfg.Container.Cmd) == 0 {
			cmd = []string{keepAliveTargetPath}
		}
	case "entrypoint":
		// Respect image ENTRYPOINT/CMD: do not set cmd; run as-is
		cmd = nil
	case "command":
		if len(c.cfg.StartCmd) == 0 {
			return fmt.Errorf("startup 'command' requires non-empty command array")
		}
		cmd = append([]string{}, c.cfg.StartCmd...)
	default:
		return fmt.Errorf("invalid startup mode: %s", mode)
	}
	helpMode, installHelper, err := c.prepareCancelHelper()
	if err != nil {
		logger.Warn(ctx, "Docker exec cancellation helper unavailable; using container tools", tag.Error(err))
		helpMode = cancelHelperNone
		installHelper = nil
		cmd = c.keepAliveFallbackCmd(mode, cmd)
	}
	c.cancelHelper = helpMode

	// Set init true to prevent zombie subprocess issues
	c.cfg.Host.Init = new(true)

	ctx, cancel := context.WithCancel(ctx)
	c.cancelMu.Lock()
	c.cancel = cancel
	c.cancelMu.Unlock()

	ctID, err := c.startNewContainer(ctx, c.cfg.ContainerName, c.cli, cmd, true, installHelper)
	if err != nil && helpMode != cancelHelperNone {
		// Installing the helper can fail for reasons the run itself does not depend
		// on: a read-only rootfs rejects the copy, and a bind source resolved on this
		// host is absent from a daemon that turned out to be remote. Cancellation
		// then falls back to in-container tooling instead of failing the run.
		logger.Warn(ctx, "Docker exec cancellation helper install failed; using container tools", tag.Error(err))
		c.discardCancelHelper(ctx, ctID)
		helpMode = cancelHelperNone
		c.cancelHelper = helpMode
		cmd = c.keepAliveFallbackCmd(mode, cmd)
		ctID, err = c.startNewContainer(ctx, c.cfg.ContainerName, c.cli, cmd, true, nil)
	}
	if err != nil {
		c.cancelHelper = cancelHelperNone
		return fmt.Errorf("failed to start a new container: %w", err)
	}
	c.containerID = ctID

	// Readiness wait
	waitMode := c.cfg.WaitFor
	if waitMode == "" {
		waitMode = "running"
	}
	// Default timeout for readiness
	readyCtx, cancel := context.WithTimeout(ctx, defaultReadinessTimeout)
	defer cancel()

	switch waitMode {
	case "running":
		if err := c.waitRunning(readyCtx, c.cli, ctID); err != nil {
			return err
		}
	case "healthy":
		// If no healthcheck defined, warn and fallback to running
		hasHealth, err := c.hasHealthcheck(readyCtx, c.cli, ctID)
		if err != nil {
			return err
		}
		if !hasHealth {
			logger.Warn(ctx, "Selected waitFor=healthy but image has no healthcheck; falling back to running")
			if err := c.waitRunning(readyCtx, c.cli, ctID); err != nil {
				return err
			}
		} else {
			if err := c.waitHealthy(readyCtx, c.cli, ctID); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid waitFor mode: %s", waitMode)
	}

	// Optional log pattern wait after base readiness
	if strings.TrimSpace(c.cfg.LogPattern) != "" {
		if err := c.waitLogPattern(readyCtx, c.cli, ctID, c.cfg.LogPattern); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) prepareCancelHelper() (cancelHelperMode, func(context.Context, string) error, error) {
	if c.shouldCopyCancelHelper() {
		if _, _, err := getKeepaliveBinary(c.platform); err != nil {
			return cancelHelperNone, nil, err
		}
		return cancelHelperCopied, func(ctx context.Context, containerID string) error {
			return copyKeepaliveToContainer(ctx, c.cli, containerID, c.platform)
		}, nil
	}

	hostPath, err := GetKeepaliveFile(c.platform)
	if err != nil {
		return cancelHelperNone, nil, err
	}
	c.keepAliveTmp = hostPath
	c.cfg.Host.Binds = append(c.cfg.Host.Binds, hostPath+":"+keepAliveTargetPath+":ro")
	return cancelHelperBound, nil, nil
}

// keepAliveFallbackCmd returns the startup command for a container that will run
// without the cancellation helper. Keepalive mode has to supply its own idle
// process once the helper binary is out of the picture; other modes are unaffected.
func (c *Client) keepAliveFallbackCmd(mode string, cmd []string) []string {
	if mode == "keepalive" && len(c.cfg.Container.Cmd) == 0 {
		return []string{"sh", "-c", keepAliveSleepCmd}
	}
	return cmd
}

// discardCancelHelper undoes prepareCancelHelper and removes any container left
// behind by the failed attempt, so the run can be retried under the same name.
func (c *Client) discardCancelHelper(ctx context.Context, containerID string) {
	if containerID != "" {
		if removeContainerForCleanup(
			ctx, c.cli, containerID,
			client.ContainerRemoveOptions{Force: true},
			defaultCancelStopWait,
		) {
			c.clearContainerState(containerID)
		}
	}
	if c.keepAliveTmp == "" {
		return
	}
	bind := c.keepAliveTmp + ":" + keepAliveTargetPath + ":ro"
	c.cfg.Host.Binds = slices.DeleteFunc(c.cfg.Host.Binds, func(b string) bool { return b == bind })
	c.removeKeepAliveTmp(ctx)
}

// removeKeepAliveTmp deletes the host-side copy of the keepalive binary, if any.
func (c *Client) removeKeepAliveTmp(ctx context.Context) {
	if c.keepAliveTmp == "" {
		return
	}
	if err := fileutil.Remove(c.keepAliveTmp); err != nil && !os.IsNotExist(err) {
		logger.Error(ctx, "Docker executor: remove keep alive file", tag.Error(err))
	}
	c.keepAliveTmp = ""
}

func (c *Client) shouldCopyCancelHelper() bool {
	return daemonNeedsHelperCopy(c.cli.DaemonHost(), c.isDockerInDocker())
}

func daemonNeedsHelperCopy(daemonHost string, containerized bool) bool {
	if containerized {
		return true
	}
	host, err := url.Parse(daemonHost)
	if err != nil {
		return true
	}
	switch host.Scheme {
	case "", "unix", "npipe":
		return false
	default:
		return true
	}
}

// StopContainerKeepAlive stops the container running keep alive command
func (c *Client) StopContainerKeepAlive(ctx context.Context) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()

	if c.cancel == nil {
		return
	}

	c.cancel()
	c.cancel = nil

	if c.containerID != "" {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultCancelStopWait)
		defer cancel()
		if err := stopContainer(cleanupCtx, c.cli, c.containerID, c.cfg.StopSignal, c.cfg.StopGrace); err != nil {
			logger.Error(ctx, "Docker executor: stop container", tag.Error(err))
		}
	}

	c.removeKeepAliveTmp(ctx)
}

// Run executes the command in the container and returns exit code
func (c *Client) Run(ctx context.Context, cmd []string, stdout, stderr io.Writer) (int, error) {
	logger.Debug(ctx, "Docker: Run started",
		slog.Any("cmd", cmd),
		slog.String("containerID", c.containerID),
	)

	ctID := c.containerID

	// check if container with the same name already exists
	if ctID != "" {
		logger.Debug(ctx, "Docker: Run checking existing container", slog.String("containerID", ctID))
		// Check if the container is running
		info, err := inspectContainer(ctx, c.cli, ctID)
		if err != nil && !errdefs.IsNotFound(err) {
			return errorExitCode, fmt.Errorf("failed to inspect container %s: %w", ctID, err)
		}
		// Container exists and is running; exec in it
		if err == nil && info.State != nil && info.State.Running {
			return c.execInContainer(ctx, c.cli, cmd, stdout, stderr, nativeExecOptions())
		}
		// If shouldStart is false, return error
		if !c.cfg.ShouldStart {
			return errorExitCode, fmt.Errorf("container %s already exists and is not running", ctID)
		}
		if err == nil {
			if removeErr := removeStoppedContainer(ctx, c.cli, ctID); removeErr != nil {
				return errorExitCode, fmt.Errorf("failed to remove stopped container %s: %w", ctID, removeErr)
			}
			c.mu.Lock()
			c.containerID = ""
			c.mu.Unlock()
		}
	}

	// If container is not running, start a new one
	// The container should be stopped and removed after run with autoRemove
	// set to true.
	logger.Debug(ctx, "Docker: Run starting new container",
		slog.String("containerName", c.cfg.ContainerName),
		slog.Any("cmd", cmd),
	)
	ctID, err := c.startNewContainer(ctx, c.cfg.ContainerName, c.cli, cmd, false, nil)
	if err != nil {
		logger.Error(ctx, "Docker: Run failed to start new container", tag.Error(err))
		return errorExitCode, fmt.Errorf("failed to start a new container: %w", err)
	}
	logger.Debug(ctx, "Docker: Run new container started", slog.String("containerID", ctID))

	defer func() {
		if !c.cfg.AutoRemove {
			return
		}

		if removeContainerForCleanup(ctx, c.cli, ctID, client.ContainerRemoveOptions{Force: true}, defaultCancelStopWait) {
			c.clearContainerState(ctID)
		}
	}()

	logger.Debug(ctx, "Docker: Run calling attachAndWait", slog.String("containerID", ctID))
	exitCode, err := c.attachAndWait(ctx, c.cli, ctID, stdout, stderr)
	logger.Debug(ctx, "Docker: Run attachAndWait returned",
		slog.Int("exitCode", exitCode),
		slog.Bool("hasError", err != nil),
	)

	// Wait for container to be stopped before returning. Honor ctx so a
	// timeout_sec cancel stops the container instead of polling forever.
	logger.Debug(ctx, "Docker: Run waiting for container to stop")
	if waitErr := waitUntilContainerStopped(ctx,
		func(inspectCtx context.Context) (bool, bool, error) {
			info, inspectErr := inspectContainer(inspectCtx, c.cli, ctID)
			if inspectErr != nil {
				if errdefs.IsNotFound(inspectErr) {
					return false, true, nil
				}
				return false, false, fmt.Errorf("failed to inspect container %s: %w", ctID, inspectErr)
			}
			running := info.State != nil && info.State.Running
			if !running && info.State != nil {
				logger.Debug(ctx, "Docker: Run container stopped", slog.String("status", string(info.State.Status)))
			}
			return running, false, nil
		},
		func(cleanupCtx context.Context) error {
			logger.Debug(ctx, "Docker: Run stopping container after context cancel", slog.String("containerID", ctID))
			return stopContainer(cleanupCtx, c.cli, ctID, c.cfg.StopSignal, c.cfg.StopGrace)
		},
		defaultPollInterval,
		defaultCancelStopWait,
	); waitErr != nil {
		return exitCode, errors.Join(err, waitErr)
	}

	logger.Debug(ctx, "Docker: Run completed", slog.Int("exitCode", exitCode))
	return exitCode, err
}

// StartBackground starts a container in the background without waiting for it to exit.
// This is useful for starting containers that should stay running while multiple commands
// are executed via Exec. The container uses the configured startup command (StartCmd) when
// startup mode is "command", or the default keepalive when in "keepalive" mode.
// This delegates to CreateContainerKeepAlive which handles all startup modes properly.
func (c *Client) StartBackground(ctx context.Context) error {
	logger.Debug(ctx, "Docker: StartBackground started",
		slog.String("startup", c.cfg.Startup),
		slog.Any("startCmd", c.cfg.StartCmd),
	)

	// Use CreateContainerKeepAlive which properly handles:
	// - keepalive mode (default): uses keepalive binary or sleep fallback
	// - command mode: uses user-provided StartCmd
	// - entrypoint mode: respects image ENTRYPOINT/CMD
	// - readiness waiting (running/healthy)
	// - log pattern waiting
	if err := c.CreateContainerKeepAlive(ctx); err != nil {
		logger.Error(ctx, "Docker: StartBackground failed to start container", tag.Error(err))
		return fmt.Errorf("failed to start container in background: %w", err)
	}

	logger.Debug(ctx, "Docker: StartBackground container started", slog.String("containerID", c.containerID))
	return nil
}

// Stop stops the running container
func (c *Client) Stop(sig os.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A closed client (Close set c.cli = nil) can no longer stop anything. This
	// guards the cancel/Stop-vs-cleanup race: a captured *Client can have Stop
	// called after a concurrent Close has nilled the underlying SDK handle (e.g.
	// a containerized harness.run step cancelled as runContainerOnce's deferred
	// Close runs). Without this guard the inspect below dereferences a nil client.
	if c.cli == nil {
		return nil
	}

	if c.containerID == "" {
		return nil
	}

	// Only stop containers owned by this client.
	if !c.started.Load() {
		return nil
	}

	var sigName string
	if sysSig, ok := sig.(syscall.Signal); ok {
		sigName = signal.GetSignalName(sysSig)
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultCancelStopWait)
	defer cancel()
	return stopContainer(cleanupCtx, c.cli, c.containerID, sigName, c.cfg.StopGrace)
}

func (c *Client) startNewContainer(
	ctx context.Context,
	name string,
	cli *client.Client,
	cmd []string,
	clearEntrypoint bool,
	beforeStart func(context.Context, string) error,
) (string, error) {
	logger.Debug(ctx, "Docker: startNewContainer started",
		slog.String("name", name),
		slog.Any("cmd", cmd),
		slog.Bool("clearEntrypoint", clearEntrypoint),
	)

	pull, err := c.shouldPullImage(ctx, cli, &c.platform)
	if err != nil {
		logger.Error(ctx, "Docker: startNewContainer shouldPullImage failed", tag.Error(err))
		return "", err
	}
	logger.Debug(ctx, "Docker: startNewContainer shouldPullImage result", slog.Bool("pull", pull))

	logger.Info(ctx, "Creating a new container",
		slog.Any("platform", c.platform),
		slog.String("image", c.cfg.Container.Image),
		slog.String("pull-policy", c.cfg.Pull.String()),
		slog.Bool("should-pull", pull),
	)

	if pull {
		logger.Infof(ctx, "Pulling the image %q", c.cfg.Image)
		logger.Debug(ctx, "Docker: startNewContainer beginning image pull")

		// Get pull options with authentication if configured
		var pullOpts client.ImagePullOptions
		if c.authManager != nil {
			var err error
			pullOpts, err = c.authManager.GetPullOptions(c.cfg.Image, c.platform)
			if err != nil {
				logger.Error(ctx, "Docker: startNewContainer failed to get pull options", tag.Error(err))
				return "", fmt.Errorf("failed to get pull options: %w", err)
			}
		} else {
			pullOpts = client.ImagePullOptions{Platforms: []specs.Platform{c.platform}}
		}

		logger.Debug(ctx, "Docker: startNewContainer calling ImagePull")
		reader, err := cli.ImagePull(ctx, c.cfg.Image, pullOpts)

		// Check for API-level error
		if err != nil {
			logger.Error(ctx, "Docker: startNewContainer ImagePull failed", tag.Error(err))
		} else {
			// Check for stream-level error (registry errors reported in JSON)
			logger.Debug(ctx, "Docker: startNewContainer ImagePull returned, checking stream for errors")
			err = checkImagePullStream(reader)
			_ = reader.Close()
			if err != nil {
				logger.Error(ctx, "Docker: startNewContainer image pull stream error", tag.Error(err))
			}
		}

		// Handle pull failure with unified fallback logic
		if err != nil {
			if c.cfg.Pull == ir.PullPolicyFallback {
				hasLocal, checkErr := c.hasLocalImage(ctx, cli, &c.platform)
				if checkErr != nil {
					return "", fmt.Errorf("image pull failed and local image check failed: %w (original pull error: %v)", checkErr, err)
				}
				if !hasLocal {
					return "", fmt.Errorf("image pull failed and no local image available: %w", err)
				}
				logger.Warnf(ctx, "Pull failed for %q, falling back to local image: %v", c.cfg.Image, err)
			} else {
				return "", err
			}
		} else {
			logger.Debug(ctx, "Docker: startNewContainer image pull completed")
			logger.Infof(ctx, "Successfully pulled the image %q", c.cfg.Image)
		}
	}

	ctCfg := *c.cfg.Container // Copy to avoid mutating original
	ctCfg.Image = c.cfg.Image

	if len(cmd) > 0 {
		// Use cmd as-is for container startup (not wrapped with shell)
		ctCfg.Cmd = cmd
		if clearEntrypoint {
			// Entrypoint should be empty slice to override image ENTRYPOINT
			ctCfg.Entrypoint = []string{}
		}
	} else if c.cfg.Startup == "command" && len(c.cfg.StartCmd) > 0 {
		// Use StartCmd for startup: command mode
		ctCfg.Cmd = c.cfg.StartCmd
	}

	logger.Debug(ctx, "Docker: startNewContainer calling ContainerCreate",
		slog.String("image", ctCfg.Image),
		slog.Any("cmd", ctCfg.Cmd),
		slog.Any("entrypoint", ctCfg.Entrypoint),
		slog.String("workingDir", ctCfg.WorkingDir),
	)
	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           &ctCfg,
		HostConfig:       c.cfg.Host,
		NetworkingConfig: c.cfg.Network,
		Platform:         &c.platform,
		Name:             name,
	})
	if err != nil {
		logger.Error(ctx, "Docker: startNewContainer ContainerCreate failed", tag.Error(err))
		return "", err
	}
	logger.Debug(ctx, "Docker: startNewContainer ContainerCreate succeeded",
		slog.String("containerID", resp.ID),
		slog.Any("warnings", resp.Warnings),
	)

	for _, warning := range resp.Warnings {
		logger.Warn(ctx, warning)
	}

	c.containerID = resp.ID
	c.started.Store(true)
	if beforeStart != nil {
		if err := beforeStart(ctx, resp.ID); err != nil {
			return "", c.cleanupFailedHelperInstall(ctx, cli, resp.ID, err)
		}
	}

	logger.Debug(ctx, "Docker: startNewContainer calling ContainerStart", slog.String("containerID", resp.ID))
	_, err = cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	if err != nil {
		logger.Error(ctx, "Docker: startNewContainer ContainerStart failed", tag.Error(err))
	} else {
		logger.Debug(ctx, "Docker: startNewContainer ContainerStart succeeded")
	}

	return resp.ID, err
}

func (c *Client) cleanupFailedHelperInstall(
	ctx context.Context,
	cli *client.Client,
	containerID string,
	installErr error,
) error {
	removeErr := removeContainerForCleanupError(cli, containerID, client.ContainerRemoveOptions{Force: true}, defaultCancelStopWait)
	if removeErr != nil {
		c.cleanupPending.Store(true)
		logger.Error(ctx, "Docker executor: remove container after helper install failure", tag.Error(removeErr))
		return errors.Join(installErr, fmt.Errorf("remove container after helper install failure: %w", removeErr))
	}
	c.clearContainerState(containerID)
	return installErr
}

// ensureCommandFlag adds the appropriate command flag (-c, -Command, /c)
// if not already present in the shell array.
func ensureCommandFlag(shell []string) []string {
	if len(shell) == 0 {
		return shell
	}

	flag := cmdutil.ShellCommandFlag(shell[0])
	if slices.Contains(shell, flag) {
		return shell
	}

	return append(slices.Clone(shell), flag)
}

// wrapCommandWithShell wraps a command array with a shell if specified.
// If shell is not specified, returns the command as-is.
//
// The command flag (-c, -Command, /c) is automatically added if not present.
// The command array is treated as follows:
//   - Single element: Used as-is (preserves original YAML quoting from CmdWithArgs)
//   - Multiple elements: Joined with spaces to create shell command string
//
// Shell format: ["/bin/bash", "-o", "errexit"]
// Command: ["echo \"hello world\""]  (from CmdWithArgs)
// Result: ["/bin/bash", "-o", "errexit", "-c", "echo \"hello world\""]
//
// Command: ["echo", "line1", "&&", "echo", "line2"]  (reconstructed array)
// Result: ["/bin/bash", "-o", "errexit", "-c", "echo line1 && echo line2"]
func wrapCommandWithShell(shell, cmd []string) []string {
	if len(shell) == 0 || len(cmd) == 0 {
		return cmd
	}

	// Auto-add command flag if not already present
	shellWithFlag := ensureCommandFlag(shell)

	// If single element, use as-is (preserves original quoting from CmdWithArgs)
	// If multiple elements, join with spaces (array was reconstructed)
	var cmdString string
	if len(cmd) == 1 {
		cmdString = cmd[0]
	} else {
		cmdString = strings.Join(cmd, " ")
	}

	return append(shellWithFlag, cmdString)
}

func (c *Client) execInContainer(ctx context.Context, cli *client.Client, cmd []string, stdout, stderr io.Writer, opts ExecOptions) (int, error) {
	// Get container ID from context
	c.mu.Lock()
	containerID := c.containerID
	c.mu.Unlock()

	// Check if info exists and is running
	info, err := inspectContainer(ctx, cli, containerID)
	if err != nil {
		return 1, fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	if info.State == nil || !info.State.Running {
		return 1, fmt.Errorf("container %s is not running", containerID)
	}

	cmd = execCommand(c.cfg.Shell, cmd, opts)

	var cfgExec client.ExecCreateOptions
	if c.cfg.ExecOptions != nil {
		cfgExec = *c.cfg.ExecOptions
	}

	// Merge container env vars with exec env vars.
	// ExecCreateOptions.Env replaces the container's environment, so we must
	// merge the container's Config.Env (from container.env:) with any exec-level env.
	var containerEnv []string
	if info.Config != nil {
		containerEnv = append(containerEnv, info.Config.Env...)
	}
	var configuredEnv []string
	if c.cfg.Container != nil {
		configuredEnv = append(configuredEnv, c.cfg.Container.Env...)
	}
	execEnv := mergeEnvByKey(containerEnv, configuredEnv, cfgExec.Env, opts.Env)
	execCancelToken := ""
	if opts.TerminateOnCancel {
		execCancelToken = newExecCancelToken()
		execEnv = append(execEnv, execCancelTokenEnv+"="+execCancelToken)
	}

	// Create exec configuration
	execOpts := client.ExecCreateOptions{
		User:         cfgExec.User,
		Privileged:   cfgExec.Privileged,
		TTY:          cfgExec.TTY,
		AttachStdin:  false,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
		Env:          execEnv,
		WorkingDir:   cfgExec.WorkingDir,
	}

	// Override the working dir if specified
	if opts.WorkingDir != "" {
		execOpts.WorkingDir = opts.WorkingDir
	}
	signalOpts := execSignalOptions{user: cfgExec.User}
	if c.started.Load() {
		signalOpts.helper = c.cancelHelper
	}

	// Create exec instance
	execCreateResp, err := cli.ExecCreate(ctx, containerID, execOpts)
	if err != nil {
		return 1, fmt.Errorf("failed to create exec: %w", err)
	}

	// Start exec instance
	resp, err := cli.ExecAttach(ctx, execCreateResp.ID, client.ExecAttachOptions{TTY: cfgExec.TTY})
	if err != nil {
		if ctx.Err() != nil && opts.TerminateOnCancel {
			return 1, canceledExecError(ctx, cli, execCreateResp.ID, execCancelToken, signalOpts)
		}
		return 1, fmt.Errorf("failed to start exec: %w", err)
	}

	// Copy output
	var wg sync.WaitGroup
	wg.Add(1)
	copyDone := make(chan struct{})
	defer func() {
		resp.Close()
		wg.Wait()
	}()

	go func() {
		defer wg.Done()
		defer close(copyDone)
		var copyErr error
		if cfgExec.TTY {
			_, copyErr = io.Copy(stdout, resp.Reader)
		} else {
			_, copyErr = stdcopy.StdCopy(stdout, stderr, resp.Reader)
		}
		if copyErr != nil {
			logger.Error(ctx, "Docker executor: exec output copy", tag.Error(copyErr))
		}
	}()

	// The daemon closes the attached stream once the exec's process exits, and it
	// marks the exec running only after that stream is established. Inspecting
	// before the stream ends can therefore observe a not-yet-running exec and
	// read its zero-valued exit code as success.
	select {
	case <-copyDone:
	case <-ctx.Done():
		resp.Close()
		if opts.TerminateOnCancel {
			return 1, canceledExecError(ctx, cli, execCreateResp.ID, execCancelToken, signalOpts)
		}
		return 1, ctx.Err()
	}

	// Wait for exec to complete
	for {
		inspectResp, err := cli.ExecInspect(ctx, execCreateResp.ID, client.ExecInspectOptions{})
		if err != nil {
			if ctx.Err() != nil {
				if opts.TerminateOnCancel {
					resp.Close()
					return 1, canceledExecError(ctx, cli, execCreateResp.ID, execCancelToken, signalOpts)
				}
				resp.Close()
				return 1, ctx.Err()
			}
			return 1, fmt.Errorf("failed to inspect exec: %w", err)
		}

		if !inspectResp.Running {
			if inspectResp.ExitCode != 0 {
				return inspectResp.ExitCode, fmt.Errorf("exec failed with exit code: %d", inspectResp.ExitCode)
			}
			return inspectResp.ExitCode, nil
		}

		if err := waitForContainerPoll(ctx, defaultPollInterval); err != nil {
			if opts.TerminateOnCancel {
				resp.Close()
				return 1, canceledExecError(ctx, cli, execCreateResp.ID, execCancelToken, signalOpts)
			}
			resp.Close()
			return 1, ctx.Err()
		}
	}
}

func mergeEnvByKey(layers ...[]string) []string {
	var merged []string
	indexByKey := make(map[string]int)
	for _, layer := range layers {
		for _, entry := range layer {
			key, _, ok := strings.Cut(entry, "=")
			if !ok || key == "" {
				continue
			}
			if idx, exists := indexByKey[key]; exists {
				merged[idx] = entry
				continue
			}
			indexByKey[key] = len(merged)
			merged = append(merged, entry)
		}
	}
	return merged
}

func execCommand(shell, cmd []string, opts ExecOptions) []string {
	if opts.Direct {
		return append([]string(nil), cmd...)
	}
	return wrapCommandWithShell(shell, cmd)
}

func newExecCancelToken() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type execSignalOptions struct {
	helper cancelHelperMode
	user   string
}

func canceledExecError(
	ctx context.Context,
	cli *client.Client,
	execID string,
	token string,
	signalOpts execSignalOptions,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultCancelStopWait)
	defer cancel()
	cleanupErr := terminateExecProcess(cleanupCtx, cli, execID, token, signalOpts)
	return errors.Join(ctx.Err(), cleanupErr)
}

func terminateExecProcess(
	ctx context.Context,
	cli *client.Client,
	execID string,
	token string,
	signalOpts execSignalOptions,
) error {
	inspectResp, err := cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect exec %s: %w", execID, err)
	}

	if token == "" {
		if !inspectResp.Running {
			return nil
		}
		return fmt.Errorf("exec %s has no cancel token for daemon-side cancellation", execID)
	}
	stopped := !inspectResp.Running
	var cleanupErr error
	if !stopped {
		if err := signalContainerTokenProcess(ctx, cli, inspectResp.ContainerID, token, "TERM", signalOpts); err != nil {
			cleanupErr = err
		} else {
			stopped, err = execStoppedWithin(ctx, cli, execID, 2*time.Second)
			if err != nil {
				cleanupErr = err
			}
		}
	}
	if err := signalContainerTokenProcess(ctx, cli, inspectResp.ContainerID, token, "KILL", signalOpts); err != nil {
		return errors.Join(cleanupErr, err)
	}
	if stopped {
		return cleanupErr
	}
	stopped, err = execStoppedWithin(ctx, cli, execID, 0)
	if err != nil {
		return errors.Join(cleanupErr, fmt.Errorf("wait for exec %s after KILL: %w", execID, err))
	}
	if stopped {
		return cleanupErr
	}
	return errors.Join(cleanupErr, fmt.Errorf("exec %s is still running after KILL", execID))
}

func signalContainerTokenProcess(
	ctx context.Context,
	cli *client.Client,
	containerID string,
	token string,
	signalName string,
	signalOpts execSignalOptions,
) error {
	if signalOpts.helper != cancelHelperNone {
		execOpts := helperSignalExecOptions(signalOpts.user, signalName, token)
		return runContainerSignalExec(ctx, cli, containerID, signalName, "cancel token", execOpts)
	}

	script := `sig="$1"
token="$2"
kill_tree() {
  for child in $(ps -eo pid,ppid 2>/dev/null | awk -v parent="$1" 'NR > 1 && $2 == parent { print $1 }'); do
    kill_tree "$child"
  done
  kill "-$sig" "$1" 2>/dev/null || true
}
for environ in /proc/[0-9]*/environ; do
  pid="${environ#/proc/}"
  pid="${pid%/environ}"
  if tr '\0' '\n' < "$environ" 2>/dev/null | grep -Fxq "` + execCancelTokenEnv + `=$token"; then
    kill "-$sig" -- "-$pid" 2>/dev/null || true
    kill_tree "$pid"
  fi
done`
	return runContainerSignalExec(ctx, cli, containerID, signalName, "cancel token", client.ExecCreateOptions{
		User: signalOpts.user,
		Cmd:  []string{"sh", "-c", script, "dagu-kill-exec", signalName, token},
	})
}

func helperSignalExecOptions(user string, signalName string, token string) client.ExecCreateOptions {
	return client.ExecCreateOptions{
		User: user,
		Cmd:  []string{keepAliveTargetPath, "signal-token", signalName, token},
	}
}

func runContainerSignalExec(
	ctx context.Context,
	cli *client.Client,
	containerID string,
	signalName string,
	target string,
	execOpts client.ExecCreateOptions,
) error {
	resp, err := cli.ExecCreate(ctx, containerID, execOpts)
	if err != nil {
		return fmt.Errorf("create %s signal exec for %s: %w", signalName, target, err)
	}
	if _, err := cli.ExecStart(ctx, resp.ID, client.ExecStartOptions{Detach: true}); err != nil {
		return fmt.Errorf("start %s signal exec for %s: %w", signalName, target, err)
	}

	for {
		inspectResp, err := cli.ExecInspect(ctx, resp.ID, client.ExecInspectOptions{})
		if err != nil {
			return fmt.Errorf("inspect %s signal exec for %s: %w", signalName, target, err)
		}
		if !inspectResp.Running {
			if inspectResp.ExitCode != 0 {
				return fmt.Errorf("%s signal exec for %s exited with code %d", signalName, target, inspectResp.ExitCode)
			}
			return nil
		}
		if err := waitForContainerPoll(ctx, defaultPollInterval); err != nil {
			return fmt.Errorf("wait for %s signal exec for %s: %w", signalName, target, err)
		}
	}
}

func execStoppedWithin(ctx context.Context, cli *client.Client, execID string, timeout time.Duration) (bool, error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		inspectResp, err := cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if err != nil {
			return false, fmt.Errorf("inspect exec %s while waiting for stop: %w", execID, err)
		}
		if !inspectResp.Running {
			return true, nil
		}
		poll := defaultPollInterval
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return false, nil
			}
			if remaining < poll {
				poll = remaining
			}
		}
		if err := waitForContainerPoll(ctx, poll); err != nil {
			return false, err
		}
	}
}

func removeStoppedContainer(ctx context.Context, cli *client.Client, containerID string) error {
	if _, err := cli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{}); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// removeContainerForCleanup reports whether the container no longer needs removal.
func removeContainerForCleanup(
	ctx context.Context,
	cli *client.Client,
	containerID string,
	opts client.ContainerRemoveOptions,
	timeout time.Duration,
) bool {
	err := removeContainerForCleanupError(cli, containerID, opts, timeout)
	if err != nil {
		logger.Error(ctx, "Docker executor: remove container", tag.Error(err))
		return false
	}
	return true
}

// removeContainerForCleanupError removes a container on its own timeout so
// cleanup completes after the caller's context has been canceled.
func removeContainerForCleanupError(
	cli *client.Client,
	containerID string,
	opts client.ContainerRemoveOptions,
	timeout time.Duration,
) error {
	if timeout <= 0 {
		timeout = defaultCancelStopWait
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := cli.ContainerRemove(cleanupCtx, containerID, opts); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// clearContainerState forgets ownership of a container after cleanup.
func (c *Client) clearContainerState(containerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearContainerStateLocked(containerID)
}

// clearContainerStateLocked clears the tracked container only when it still matches.
func (c *Client) clearContainerStateLocked(containerID string) {
	if c.containerID != containerID {
		return
	}
	c.containerID = ""
	c.started.Store(false)
	c.cleanupPending.Store(false)
}

func (c *Client) attachAndWait(ctx context.Context, cli *client.Client, containerID string, stdout, stderr io.Writer) (int, error) {
	logger.Debug(ctx, "Docker: attachAndWait started", slog.String("containerID", containerID))

	logger.Debug(ctx, "Docker: attachAndWait calling ContainerLogs")
	out, err := cli.ContainerLogs(
		ctx, containerID, client.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
		},
	)
	if err != nil {
		logger.Error(ctx, "Docker: attachAndWait ContainerLogs failed", tag.Error(err))
		return 1, err
	}
	logger.Debug(ctx, "Docker: attachAndWait ContainerLogs succeeded")

	var wg sync.WaitGroup
	wg.Add(1)
	copyDone := make(chan struct{})
	defer func() {
		// A followed log stream does not end while the container still runs, so
		// draining it is only bounded by ctx. Close the reader once either the
		// copy finishes or ctx ends, then join the copy goroutine.
		select {
		case <-copyDone:
		case <-ctx.Done():
		}
		logger.Debug(ctx, "Docker: attachAndWait closing log reader")
		_ = out.Close()
		wg.Wait()
	}()

	go func() {
		defer wg.Done()
		defer close(copyDone)
		logger.Debug(ctx, "Docker: attachAndWait stdcopy goroutine started")
		if _, err := stdcopy.StdCopy(stdout, stderr, out); err != nil {
			logger.Error(ctx, "Docker executor: stdcopy", tag.Error(err))
		}
		logger.Debug(ctx, "Docker: attachAndWait stdcopy goroutine finished")
	}()

	logger.Debug(ctx, "Docker: attachAndWait calling ContainerWait")
	waitResult := cli.ContainerWait(ctx, containerID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	logger.Debug(ctx, "Docker: attachAndWait waiting for container to finish")
	select {
	case err := <-waitResult.Error:
		logger.Debug(ctx, "Docker: attachAndWait received error from errCh", slog.Bool("hasError", err != nil))
		if err != nil {
			return 1, err
		}

	case status := <-waitResult.Result:
		logger.Debug(ctx, "Docker: attachAndWait received status",
			slog.Int64("statusCode", status.StatusCode),
		)
		if status.StatusCode != 0 {
			return int(status.StatusCode), fmt.Errorf("exit status %v", status.StatusCode)
		}
		return int(status.StatusCode), nil
	}

	return 0, nil
}

// isDockerInDocker detects if we're running inside a Docker container
func (c *Client) isDockerInDocker() bool {
	// Check for container runtime environment files
	if c.fileExists(dockerEnvFile) || c.fileExists(podmanEnvFile) {
		return true
	}

	// Check if we're in a container by examining cgroup
	if c.isInContainerByCgroup() {
		return true
	}

	// Check for Kubernetes environment
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// fileExists checks if a file exists
func (c *Client) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isInContainerByCgroup checks if we're running in a container by examining cgroup
func (c *Client) isInContainerByCgroup() bool {
	data, err := os.ReadFile(proc1CgroupFile)
	if err != nil {
		return false
	}
	return cgroupIndicatesContainer(string(data))
}

// cgroupIndicatesContainer reports whether a /proc/1/cgroup body describes a
// containerized init. Host init systems also run under named slices, so only
// runtime-specific paths count; a missed runtime is recovered from at container
// start rather than guessed at here.
func cgroupIndicatesContainer(content string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		_, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		_, cgroupPath, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		if containerCgroupIDPattern.MatchString(cgroupPath) {
			return true
		}
		for segment := range strings.SplitSeq(strings.Trim(cgroupPath, "/"), "/") {
			if isContainerCgroupSegment(segment) {
				return true
			}
		}
	}
	return false
}

// isContainerCgroupSegment reports whether one cgroup path segment names a
// container runtime's own hierarchy rather than a host slice.
func isContainerCgroupSegment(segment string) bool {
	switch segment {
	case "kubepods", "kubepods.slice", "lxc", "ecs", "docker", "actions_job":
		return true
	}
	return strings.HasPrefix(segment, "kubepods-") ||
		strings.HasPrefix(segment, "lxc.payload") ||
		strings.HasPrefix(segment, "machine-") ||
		strings.HasPrefix(segment, "nspawn-")
}

func getPlatform(ctx context.Context, cli *client.Client, cfg *Config) (specs.Platform, error) {
	// Extract platform from the current input and fallback to the current docker host platform.
	var platform specs.Platform
	if cfg.Platform != "" {
		var err error
		platform, err = platforms.Parse(cfg.Platform)
		if err != nil {
			return platform, fmt.Errorf("failed to parse platform %s: %w", cfg.Platform, err)
		}
	} else {
		info, err := cli.Info(ctx, client.InfoOptions{})
		if err != nil {
			return platform, fmt.Errorf("failed to get current docker host info: %w", err)
		}
		platform.Architecture = info.Info.Architecture
		platform.OS = info.Info.OSType
		platform = platforms.Normalize(platform)
	}
	return platform, nil
}

// checkImagePullStream decodes the Docker image pull JSON stream and checks for errors.
// Docker's ImagePull can return a successful io.ReadCloser even when the registry reports
// errors in the JSON stream itself (e.g., authentication failures, not found, etc.).
func checkImagePullStream(reader io.Reader) error {
	decoder := json.NewDecoder(reader)
	type pullMessage struct {
		Status string `json:"status,omitempty"`
		Error  string `json:"error,omitempty"`
	}

	for {
		var msg pullMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode stream message: %w", err)
		}

		if msg.Error != "" {
			return fmt.Errorf("image pull error: %s", msg.Error)
		}
	}

	return nil
}

func (c *Client) shouldPullImage(ctx context.Context, cli *client.Client, platform *specs.Platform) (bool, error) {
	if c.cfg.Pull == ir.PullPolicyAlways {
		return true, nil
	}
	if c.cfg.Pull == ir.PullPolicyNever {
		return false, nil
	}
	if c.cfg.Pull == ir.PullPolicyFallback {
		// Always attempt pull; fallback to local is handled by the caller.
		return true, nil
	}

	return c.needsPull(ctx, cli, platform)
}

// hasLocalImage checks whether a local image matching the given platform exists.
func (c *Client) hasLocalImage(ctx context.Context, cli *client.Client, platform *specs.Platform) (bool, error) {
	filters := make(client.Filters).Add("reference", c.cfg.Image)

	images, err := cli.ImageList(ctx, client.ImageListOptions{Filters: filters})
	if err != nil {
		return false, fmt.Errorf("failed to list local images %s: %w", c.cfg.Image, err)
	}

	for _, summary := range images.Items {
		inspect, err := cli.ImageInspect(ctx, summary.ID)
		if err != nil {
			return false, fmt.Errorf("failed to inspect image %s: %w", summary.ID, err)
		}

		localPlatform := specs.Platform{
			OS:           inspect.Os,
			Architecture: inspect.Architecture,
			Variant:      inspect.Variant,
		}

		if platforms.OnlyStrict(*platform).Match(localPlatform) {
			return true, nil
		}
	}

	return false, nil
}

// needsPull checks if the image needs to be pulled for PullPolicyMissing.
func (c *Client) needsPull(ctx context.Context, cli *client.Client, platform *specs.Platform) (bool, error) {
	hasLocal, err := c.hasLocalImage(ctx, cli, platform)
	if err != nil {
		return false, err
	}
	if hasLocal {
		return false, nil
	}
	return true, nil
}

// parseRestartPolicy parses a docker restart policy string into container.RestartPolicy.
// Supported forms: "no", "always", "unless-stopped" (on-failure not supported).
func parseRestartPolicy(s string) (container.RestartPolicy, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return container.RestartPolicy{}, nil
	}
	switch s { // use tagged switch to satisfy linter
	case "no":
		return container.RestartPolicy{Name: "no"}, nil
	case "always":
		return container.RestartPolicy{Name: "always"}, nil
	case "unless-stopped":
		return container.RestartPolicy{Name: "unless-stopped"}, nil
	default:
		return container.RestartPolicy{}, fmt.Errorf("invalid restart_policy: %s (supported: no, always, unless-stopped)", s)
	}
}

// terminalContainerStatuses are container statuses that indicate the container has stopped
// and will not become running.
var terminalContainerStatuses = []string{"exited", "dead", "removing"}

// waitRunning waits until the container is in running state or context times out.
func (c *Client) waitRunning(ctx context.Context, cli *client.Client, id string) error {
	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness timeout waiting for running; last state=%s: %w", last, ctx.Err())
		case <-ticker.C:
			info, err := inspectContainer(ctx, cli, id)
			if err != nil {
				return fmt.Errorf("failed to inspect container %s: %w", id, err)
			}
			if info.State == nil {
				continue
			}
			if info.State.Running {
				logger.Info(ctx, "Container ready (running)", slog.String("id", id))
				return nil
			}
			// If the container has already exited or is dead, fail fast
			status := strings.ToLower(string(info.State.Status))
			if slices.Contains(terminalContainerStatuses, status) {
				return fmt.Errorf("container %s not running; status=%s, exitCode=%d", id, status, info.State.ExitCode)
			}
			last = fmt.Sprintf("running=%v,status=%s", info.State.Running, info.State.Status)
		}
	}
}

// hasHealthcheck checks if the container has a healthcheck configured.
func (c *Client) hasHealthcheck(ctx context.Context, cli *client.Client, id string) (bool, error) {
	info, err := inspectContainer(ctx, cli, id)
	if err != nil {
		return false, fmt.Errorf("failed to inspect container %s: %w", id, err)
	}
	return info.State != nil && info.State.Health != nil, nil
}

// waitHealthy waits until the container health status is healthy.
func (c *Client) waitHealthy(ctx context.Context, cli *client.Client, id string) error {
	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness timeout waiting for healthy; last health=%s: %w", last, ctx.Err())
		case <-ticker.C:
			info, err := inspectContainer(ctx, cli, id)
			if err != nil {
				return fmt.Errorf("failed to inspect container %s: %w", id, err)
			}
			if info.State != nil && info.State.Health != nil {
				status := string(info.State.Health.Status)
				last = status
				if strings.ToLower(status) == "healthy" {
					logger.Info(ctx, "Container ready (healthy)",
						slog.String("id", id),
					)
					return nil
				}
			}
		}
	}
}

// waitLogPattern follows container logs until the given regex pattern appears.
func (c *Client) waitLogPattern(ctx context.Context, cli *client.Client, id string, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid logPattern regex: %w", err)
	}
	reader, err := cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Tail: "all"})
	if err != nil {
		return fmt.Errorf("failed to read container logs: %w", err)
	}
	defer func() {
		if cerr := reader.Close(); cerr != nil {
			logger.Error(ctx, "Docker executor: close logs reader", tag.Error(cerr))
		}
	}()

	pr, pw := io.Pipe()
	// Demultiplex logs into a single stream
	go func() {
		defer func() {
			if cerr := pw.Close(); cerr != nil {
				logger.Error(ctx, "Docker executor: close pipe writer", tag.Error(cerr))
			}
		}()
		_, _ = stdcopy.StdCopy(pw, pw, reader)
	}()

	scanner := bufio.NewScanner(pr)
	// Allow long lines for log parsing
	buf := make([]byte, 0, logScanInitialBuf)
	scanner.Buffer(buf, logScanMaxBuf)
	for scanner.Scan() {
		line := scanner.Text()
		if re.MatchString(line) {
			logger.Info(ctx, "Container ready (log pattern matched)",
				slog.String("id", id),
				slog.String("pattern", pattern),
			)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness timeout waiting for logPattern; pattern=%q: %w", pattern, ctx.Err())
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading logs: %w", err)
	}
	return fmt.Errorf("log stream ended before pattern matched: %q", pattern)
}

func isContainerRunning(info container.InspectResponse, err error) (bool, error) {
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return (info.State != nil && info.State.Running), nil
}
