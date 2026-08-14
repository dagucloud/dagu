// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package opencodehost

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	envPrefix     = "_DAGU_INTERNAL_OPENCODE_"
	envURL        = envPrefix + "URL"
	envUsername   = envPrefix + "USERNAME"
	envPassword   = envPrefix + "PASSWORD" //nolint:gosec // This is an environment variable name, not a credential.
	envInstanceID = envPrefix + "INSTANCE_ID"
	envVersion    = envPrefix + "VERSION"
	envError      = envPrefix + "ERROR"

	managedUsername = "opencode"
	stderrTailLimit = 64 * 1024
)

var safeExactEnvironment = map[string]bool{
	"ALL_PROXY": true, "APPDATA": true, "COMSPEC": true, "HOME": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "LANG": true, "LANGUAGE": true,
	"LOCALAPPDATA": true, "LOGNAME": true, "NODE_EXTRA_CA_CERTS": true,
	"NO_PROXY": true, "PATH": true, "PATHEXT": true, "SHELL": true,
	"SSL_CERT_DIR": true, "SSL_CERT_FILE": true, "SYSTEMROOT": true,
	"TEMP": true, "TMP": true, "TMPDIR": true, "TZ": true,
	"USER": true, "USERPROFILE": true,
	"XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true,
}

// Config contains the private loopback connection used by managed harness steps.
type Config struct {
	URL        string
	Username   string
	Password   string
	InstanceID string
	Version    string
}

// Env returns the private transport entries needed by a child Dagu process.
func (c Config) Env() []string {
	if c.URL == "" {
		return nil
	}
	return []string{
		envURL + "=" + c.URL,
		envUsername + "=" + c.Username,
		envPassword + "=" + c.Password,
		envInstanceID + "=" + c.InstanceID,
		envVersion + "=" + c.Version,
	}
}

// UnavailableEnv returns a private transport error for a required managed step.
func UnavailableEnv(err error) []string {
	if err == nil {
		return nil
	}
	return []string{envError + "=" + sanitizeError(err)}
}

// Host lazily owns one OpenCode server process.
type Host struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	settings    config.OpenCodeConfig
	cmd         *exec.Cmd
	waitCh      chan error
	config      Config
	stderr      *tailBuffer
	healthCheck func(Config) (string, error)
}

// New creates an idle host whose lifecycle ends with parent.
func New(parent context.Context, settings config.OpenCodeConfig) *Host {
	if parent == nil {
		parent = context.Background()
	}
	if strings.TrimSpace(settings.Executable) == "" {
		settings.Executable = "opencode"
	}
	ctx, cancel := context.WithCancel(parent)
	host := &Host{ctx: ctx, cancel: cancel, settings: settings}
	host.healthCheck = host.probeHealth
	return host
}

// Ensure returns a healthy managed server, starting it when necessary.
func (h *Host) Ensure(_ context.Context) (Config, error) {
	if h == nil {
		return Config{}, errors.New("managed OpenCode host is not configured for this process")
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.config.URL != "" && h.runningLocked() {
		version, err := h.healthCheck(h.config)
		if err != nil {
			return Config{}, fmt.Errorf("managed OpenCode host is unhealthy: %w", err)
		}
		if version != "" {
			h.config.Version = version
		}
		return h.config, nil
	}
	h.clearLocked(false)
	return h.startLocked()
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

func (h *Host) startLocked() (Config, error) {
	binary, err := exec.LookPath(h.settings.Executable)
	if err != nil {
		return Config{}, fmt.Errorf("managed OpenCode requires executable %q: %w", h.settings.Executable, err)
	}
	password, err := randomToken()
	if err != nil {
		return Config{}, fmt.Errorf("create OpenCode server password: %w", err)
	}
	instanceID, err := randomToken()
	if err != nil {
		return Config{}, fmt.Errorf("create OpenCode host identity: %w", err)
	}
	env, err := managedEnvironment(os.Environ(), h.settings.EnvPassthrough)
	if err != nil {
		return Config{}, err
	}
	env, err = withManagedConfig(env)
	if err != nil {
		return Config{}, err
	}
	env = append(env,
		"OPENCODE_SERVER_USERNAME="+managedUsername,
		"OPENCODE_SERVER_PASSWORD="+password,
	)

	cmd := exec.CommandContext(h.ctx, binary, "serve", "--hostname", "127.0.0.1", "--port", "0") //nolint:gosec // The operator-selected executable is resolved from PATH.
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Config{}, fmt.Errorf("open OpenCode server stdout: %w", err)
	}
	stderr := &tailBuffer{limit: stderrTailLimit}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return Config{}, fmt.Errorf("start OpenCode server: %w", err)
	}

	ready := make(chan string, 1)
	go scanEndpoint(stdout, ready)
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	startup := time.NewTimer(10 * time.Second)
	defer startup.Stop()
	var endpoint string
	select {
	case endpoint = <-ready:
	case <-waitCh:
		return Config{}, errors.New("OpenCode server exited before startup")
	case <-startup.C:
		_ = cmd.Process.Kill()
		return Config{}, errors.New("timed out waiting for OpenCode server startup")
	case <-h.ctx.Done():
		_ = cmd.Process.Kill()
		return Config{}, h.ctx.Err()
	}

	hostConfig := Config{
		URL: strings.TrimRight(endpoint, "/"), Username: managedUsername,
		Password: password, InstanceID: instanceID,
	}
	if err := validate(hostConfig); err != nil {
		_ = cmd.Process.Kill()
		return Config{}, err
	}
	version, err := h.preflight(hostConfig)
	if err != nil {
		_ = cmd.Process.Kill()
		return Config{}, err
	}
	hostConfig.Version = version
	h.cmd = cmd
	h.waitCh = waitCh
	h.config = hostConfig
	h.stderr = stderr
	return hostConfig, nil
}

func scanEndpoint(stdout io.Reader, ready chan<- string) {
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
}

func (h *Host) preflight(hostConfig Config) (string, error) {
	version, err := h.healthCheck(hostConfig)
	if err != nil {
		return "", fmt.Errorf("OpenCode health capability failed: %w", err)
	}
	for _, path := range []string{"/config", "/session/status", "/permission", "/question"} {
		var target any
		if err := hostJSON(hostConfig, path, &target); err != nil {
			return "", fmt.Errorf("OpenCode capability %s is unavailable: %w", path, err)
		}
		if path == "/config" {
			settings, ok := target.(map[string]any)
			if !ok || settings["share"] != "disabled" {
				return "", errors.New("OpenCode managed sharing could not be disabled")
			}
		}
	}
	return version, nil
}

func (h *Host) probeHealth(hostConfig Config) (string, error) {
	var health struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := hostJSON(hostConfig, "/global/health", &health); err != nil {
		return "", err
	}
	if !health.Healthy {
		return "", errors.New("OpenCode reported an unhealthy service")
	}
	return health.Version, nil
}

func hostJSON(hostConfig Config, path string, target any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hostConfig.URL+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(hostConfig.Username, hostConfig.Password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("OpenCode did not respond")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenCode returned %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target); err != nil {
		return errors.New("OpenCode returned an invalid response")
	}
	return nil
}

func (h *Host) clearLocked(kill bool) {
	if kill && h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	h.cmd = nil
	h.waitCh = nil
	h.config = Config{}
	h.stderr = nil
}

// Close stops the managed server.
func (h *Host) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.cancel()
	h.mu.Lock()
	waitCh := h.waitCh
	h.waitCh = nil
	h.cmd = nil
	h.config = Config{}
	h.mu.Unlock()
	if waitCh == nil {
		return nil
	}
	select {
	case err := <-waitCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != -1 {
				return errors.New("OpenCode server exited unexpectedly")
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randomToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func managedEnvironment(environ, extra []string) ([]string, error) {
	allowed := make(map[string]bool, len(safeExactEnvironment)+len(extra))
	for key := range safeExactEnvironment {
		allowed[key] = true
	}
	for _, key := range extra {
		key = strings.TrimSpace(key)
		normalized := strings.ToUpper(key)
		if normalized == "OPENCODE_SERVER_USERNAME" || normalized == "OPENCODE_SERVER_PASSWORD" || strings.HasPrefix(normalized, "_DAGU_INTERNAL_") {
			return nil, fmt.Errorf("opencode.env_passthrough contains reserved variable %q", key)
		}
		if key != "" {
			allowed[normalized] = true
		}
	}

	result := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		normalized := strings.ToUpper(key)
		if strings.HasPrefix(normalized, envPrefix) || normalized == "OPENCODE_SERVER_USERNAME" || normalized == "OPENCODE_SERVER_PASSWORD" {
			continue
		}
		if allowed[normalized] || strings.HasPrefix(normalized, "LC_") || strings.HasPrefix(normalized, "OPENCODE_") {
			result = append(result, entry)
		}
	}
	return result, nil
}

func withManagedConfig(environ []string) ([]string, error) {
	content := map[string]any{}
	for i := range environ {
		key, value, ok := strings.Cut(environ[i], "=")
		if !ok || strings.ToUpper(key) != "OPENCODE_CONFIG_CONTENT" {
			continue
		}
		if strings.TrimSpace(value) != "" {
			if err := json.Unmarshal([]byte(value), &content); err != nil {
				return nil, errors.New("OPENCODE_CONFIG_CONTENT must contain a JSON object")
			}
			if content == nil {
				return nil, errors.New("OPENCODE_CONFIG_CONTENT must contain a JSON object")
			}
		}
		environ = append(environ[:i], environ[i+1:]...)
		break
	}
	content["share"] = "disabled"
	data, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("build managed OpenCode configuration: %w", err)
	}
	return append(environ, "OPENCODE_CONFIG_CONTENT="+string(data)), nil
}

func validate(hostConfig Config) error {
	parsed, err := url.Parse(hostConfig.URL)
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
	if hostConfig.Password == "" || hostConfig.InstanceID == "" {
		return errors.New("OpenCode server credentials and identity are required")
	}
	return nil
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ReplaceAll(strings.TrimSpace(err.Error()), "\n", " ")
	if len(message) > 1024 {
		message = message[:1024]
	}
	return message
}

type tailBuffer struct {
	limit int
	data  []byte
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	size := len(data)
	b.data = append(b.data, data...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return size, nil
}

type hostContextKey struct{}

// WithHost makes a managed OpenCode host available to runtime executors.
func WithHost(ctx context.Context, host *Host) context.Context {
	return context.WithValue(ctx, hostContextKey{}, host)
}

// ConfigFromContext resolves a host-owned or process-injected connection.
func ConfigFromContext(ctx context.Context) (Config, bool, error) {
	if host, ok := ctx.Value(hostContextKey{}).(*Host); ok && host != nil {
		hostConfig, err := host.Ensure(ctx)
		return hostConfig, err == nil, err
	}
	if message := os.Getenv(envError); message != "" {
		return Config{}, false, errors.New(message)
	}
	hostConfig := Config{
		URL: os.Getenv(envURL), Username: os.Getenv(envUsername), Password: os.Getenv(envPassword),
		InstanceID: os.Getenv(envInstanceID), Version: os.Getenv(envVersion),
	}
	if hostConfig.URL == "" {
		return Config{}, false, nil
	}
	if err := validate(hostConfig); err != nil {
		return Config{}, false, err
	}
	return hostConfig, true, nil
}

// DeleteSession removes a host-owned OpenCode session after Dagu has persisted its terminal state.
func DeleteSession(ctx context.Context, hostConfig Config, directory, sessionID string) error {
	if err := validate(hostConfig); err != nil {
		return err
	}
	deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	query := url.Values{}
	query.Set("directory", directory)
	req, err := http.NewRequestWithContext(deleteCtx, http.MethodDelete, hostConfig.URL+"/session/"+url.PathEscape(sessionID)+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(hostConfig.Username, hostConfig.Password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("OpenCode session cleanup could not reach the managed host")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenCode session cleanup returned %s", resp.Status)
	}
	return nil
}

// ModeResult describes how an OpenCode harness config should run.
type ModeResult struct {
	Managed  bool
	Required bool
	Reason   string
}

// Mode reports whether an OpenCode harness config can use the managed service.
func Mode(values map[string]any) (ModeResult, error) {
	result := ModeResult{}
	if value, ok := values["managed"]; ok {
		flag, valid := value.(bool)
		if !valid {
			return result, errors.New("harness: config.managed must be a boolean")
		}
		if !flag {
			result.Reason = "managed is false"
			return result, nil
		}
		result.Required = true
	}
	allowed := map[string]bool{
		"provider": true, "managed": true, "fallback": true,
		"agent": true, "model": true, "variant": true, "session": true,
		"fork": true, "title": true, "file": true, "command": true, "format": true,
	}
	for key := range values {
		if allowed[key] {
			continue
		}
		result.Reason = fmt.Sprintf("option %q requires the CLI integration", key)
		if result.Required {
			return result, fmt.Errorf("harness: managed OpenCode does not support option %q; set managed: false", key)
		}
		return result, nil
	}
	if format, _ := values["format"].(string); format != "" && format != "default" {
		result.Reason = fmt.Sprintf("format %q requires the CLI integration", format)
		if result.Required {
			return result, errors.New("harness: managed OpenCode supports only format: default; set managed: false")
		}
		return result, nil
	}
	result.Managed = true
	return result, nil
}

// DAGUsesManaged reports whether a DAG has a host-compatible managed OpenCode step.
func DAGUsesManaged(dag *ir.DAG) bool {
	if dag == nil {
		return false
	}
	for _, step := range dag.Steps {
		if step.ExecutorConfig.Type != "harness" || step.Container != nil || step.ExecutorConfig.Config["provider"] != "opencode" {
			continue
		}
		mode, err := Mode(step.ExecutorConfig.Config)
		if err == nil && mode.Managed {
			return true
		}
	}
	return false
}
