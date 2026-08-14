// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package opencodehost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	envURL      = "DAGU_INTERNAL_OPENCODE_URL"
	envUsername = "DAGU_INTERNAL_OPENCODE_USERNAME"
	envPassword = "DAGU_INTERNAL_OPENCODE_PASSWORD" //nolint:gosec // This is an environment variable name, not a credential.
)

// Config contains the private loopback connection used by managed harness steps.
type Config struct {
	URL      string
	Username string
	Password string
}

// Env returns the environment entries needed by a scheduler child process.
func (c Config) Env() []string {
	if c.URL == "" {
		return nil
	}
	return []string{
		envURL + "=" + c.URL,
		envUsername + "=" + c.Username,
		envPassword + "=" + c.Password,
	}
}

// Host lazily owns one OpenCode server process.
type Host struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	waitCh chan error
	config Config
}

// New creates an idle host whose lifecycle ends with parent.
func New(parent context.Context) *Host {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Host{ctx: ctx, cancel: cancel}
}

// Ensure returns a healthy managed server, starting it when necessary.
func (h *Host) Ensure(ctx context.Context) (Config, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.config.URL != "" && h.runningLocked() && healthy(ctx, h.config) {
		return h.config, nil
	}
	h.clearLocked()
	return h.startLocked(ctx)
}

func (h *Host) runningLocked() bool {
	if h.cmd == nil || h.waitCh == nil {
		return false
	}
	select {
	case <-h.waitCh:
		return false
	default:
		return true
	}
}

func (h *Host) startLocked(ctx context.Context) (Config, error) {
	binary, err := exec.LookPath("opencode")
	if err != nil {
		return Config{}, fmt.Errorf("managed OpenCode requires the opencode executable: %w", err)
	}
	password, err := randomPassword()
	if err != nil {
		return Config{}, fmt.Errorf("create OpenCode server password: %w", err)
	}

	cmd := exec.CommandContext(h.ctx, binary, "serve", "--hostname", "127.0.0.1", "--port", "0") //nolint:gosec // binary is resolved from PATH by design.
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_USERNAME=opencode", "OPENCODE_SERVER_PASSWORD="+password)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Config{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Config{}, fmt.Errorf("start OpenCode server: %w", err)
	}

	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if endpoint, ok := strings.CutPrefix(line, "opencode server listening on "); ok {
				select {
				case ready <- strings.TrimSpace(endpoint):
				default:
				}
			}
		}
	}()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	var endpoint string
	select {
	case endpoint = <-ready:
	case err := <-waitCh:
		return Config{}, fmt.Errorf("OpenCode server exited before startup: %w: %s", err, strings.TrimSpace(stderr.String()))
	case <-timer.C:
		_ = cmd.Process.Kill()
		return Config{}, errors.New("timed out waiting for OpenCode server startup")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return Config{}, ctx.Err()
	}

	config := Config{URL: strings.TrimRight(endpoint, "/"), Username: "opencode", Password: password}
	if err := validate(config); err != nil {
		_ = cmd.Process.Kill()
		return Config{}, err
	}
	if !healthy(ctx, config) {
		_ = cmd.Process.Kill()
		return Config{}, errors.New("OpenCode server health check failed")
	}

	h.cmd = cmd
	h.waitCh = waitCh
	h.config = config
	return config, nil
}

func (h *Host) clearLocked() {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	h.cmd = nil
	h.waitCh = nil
	h.config = Config{}
}

// Close stops the managed server.
func (h *Host) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.cancel()
	h.mu.Lock()
	waitCh := h.waitCh
	h.mu.Unlock()
	if waitCh == nil {
		return nil
	}
	select {
	case err := <-waitCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != -1 {
				return err
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randomPassword() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func healthy(ctx context.Context, config Config) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.URL+"/global/health", nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth(config.Username, config.Password)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func validate(config Config) error {
	parsed, err := url.Parse(config.URL)
	if err != nil {
		return fmt.Errorf("invalid OpenCode server URL: %w", err)
	}
	host := parsed.Hostname()
	if parsed.Scheme != "http" || (host != "localhost" && net.ParseIP(host) == nil) {
		return errors.New("OpenCode server must use a loopback HTTP address")
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		return errors.New("OpenCode server must use a loopback HTTP address")
	}
	if config.Password == "" {
		return errors.New("OpenCode server password is required")
	}
	return nil
}

type hostContextKey struct{}

// WithHost makes a managed OpenCode host available to runtime executors.
func WithHost(ctx context.Context, host *Host) context.Context {
	return context.WithValue(ctx, hostContextKey{}, host)
}

// ConfigFromContext resolves a host-owned or scheduler-injected connection.
func ConfigFromContext(ctx context.Context) (Config, bool, error) {
	if host, ok := ctx.Value(hostContextKey{}).(*Host); ok && host != nil {
		config, err := host.Ensure(ctx)
		return config, err == nil, err
	}
	config := Config{
		URL:      os.Getenv(envURL),
		Username: os.Getenv(envUsername),
		Password: os.Getenv(envPassword),
	}
	if config.URL == "" {
		return Config{}, false, nil
	}
	if err := validate(config); err != nil {
		return Config{}, false, err
	}
	return config, true, nil
}
