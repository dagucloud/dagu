// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

// configVersion versions the configuration a starting process hands the
// proxy, which may run an executable replaced in the meantime.
const configVersion = 2

const (
	// startAttempts bounds the starts of a proxy that took the browser's
	// debugging port.
	startAttempts = 2
	// readyTimeout bounds the wait for a starting proxy to accept
	// connections.
	readyTimeout = 10 * time.Second
)

// wireConfig is the configuration as the proxy process reads it.
type wireConfig struct {
	Version int `json:"version"`
	Config
}

// readyLine is the one line the proxy process writes: Ready, or why it did
// not start.
type readyLine struct {
	Ready
	Error string `json:"error,omitempty"`
}

// Handle locates a running proxy process.
type Handle struct {
	// URL is the base of the proxy's control paths.
	URL string `json:"url"`
	PID int    `json:"pid"`
	// StartedAt is the process's start time in Unix milliseconds, which
	// tells it apart from a later process reusing PID.
	StartedAt int64  `json:"started_at"`
	PACURL    string `json:"pac_url"`
	SPKI      string `json:"spki"`
	// Port is the proxy port the browser connects to.
	Port int `json:"port,omitempty"`
	// Manual reports that ChromeArgs uses a manual proxy, because a PAC
	// script cannot route a configured destination.
	Manual bool `json:"manual,omitempty"`
	// DebugPort is the browser's debugging port. A manual proxy leaves it
	// direct: <-loopback> would send the runtime's own WebSocket through
	// the proxy.
	DebugPort int `json:"-"`
}

// ChromeArgs returns the browser flags that send the certificate hosts to
// the proxy and accept the certificates it shows. A manual proxy, which
// carries every request, is what can include destinations Chrome skips in
// every PAC script.
func (h Handle) ChromeArgs() []string {
	route := "--proxy-pac-url=" + h.PACURL
	if h.Manual {
		route = fmt.Sprintf("--proxy-server=127.0.0.1:%d", h.Port)
	}
	args := []string{route, "--ignore-certificate-errors-spki-list=" + h.SPKI}
	if h.Manual {
		// An explicit port bypass stays after <-loopback> removes the
		// implicit loopback rules, so the debugging socket is not proxied.
		bypass := "<-loopback>"
		if h.DebugPort > 0 {
			bypass += fmt.Sprintf(";127.0.0.1:%d", h.DebugPort)
		}
		args = append(args, "--proxy-bypass-list="+bypass)
	}
	return args
}

// Kill ends the proxy process if it is still the one h records. A process
// already gone is not an error.
func (h Handle) Kill() error {
	matched, _, ok := procutil.MatchesStartTime(h.PID, h.StartedAt)
	if !ok || !matched {
		return nil
	}
	process, err := os.FindProcess(h.PID)
	if err != nil {
		return nil
	}
	defer func() { _ = process.Release() }()
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("end the certificate proxy: %w", err)
	}
	return nil
}

// Start runs executable's browser cert-proxy command with cfg and returns
// once the proxy accepts connections. The proxy outlives the caller and
// exits once the browser at cfg.CDPURL is gone.
func Start(ctx context.Context, executable string, cfg Config) (*Handle, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cdpPort := urlPort(cfg.CDPURL)
	for range startAttempts {
		handle, ready, err := startProcess(ctx, executable, cfg)
		if err != nil {
			return nil, err
		}
		if !ready.listensOn(cdpPort) {
			return handle, nil
		}
		// The browser's debugging port was only free when chosen, and the
		// proxy, or its guard, took it before the browser started.
		if err := handle.Kill(); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("the certificate proxy kept taking the browser's debugging port %d", cdpPort)
}

// listensOn reports whether the proxy, or its guard, listens on port.
func (r Ready) listensOn(port int) bool {
	return r.Port == port || r.GuardPort != 0 && r.GuardPort == port
}

// startProcess starts one proxy process and returns it with what it said
// once ready.
func startProcess(ctx context.Context, executable string, cfg Config) (*Handle, Ready, error) {
	input, err := json.Marshal(wireConfig{Version: configVersion, Config: cfg})
	if err != nil {
		return nil, Ready{}, err
	}
	cmd := exec.Command(executable, "browser", "cert-proxy") //nolint:gosec // starting this executable's own proxy is the purpose
	// The proxy outlives the caller, so it does not keep the caller's
	// directory in use.
	cmd.Dir = filepath.Dir(executable)
	// On Windows the proxy stays in the caller's job, and ends with the
	// browser started there.
	cmd.SysProcAttr = cmdutil.DetachAttr(false)
	cmd.Stdin = bytes.NewReader(input)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, Ready{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, Ready{}, fmt.Errorf("start the certificate proxy: %w", err)
	}
	startedAt, _ := procutil.StartTime(cmd.Process.Pid)
	ready, err := readReady(ctx, stdout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, Ready{}, err
	}
	go func() { _ = cmd.Wait() }()
	return &Handle{
		URL:       controlURL(ready.Port, cfg.Token),
		PID:       cmd.Process.Pid,
		StartedAt: startedAt,
		PACURL:    ready.PACURL,
		SPKI:      ready.SPKI,
		Port:      ready.Port,
		Manual:    ready.Manual,
	}, ready, nil
}

// readReady reads the line the proxy writes once it accepts connections.
func readReady(ctx context.Context, stdout io.Reader) (Ready, error) {
	type result struct {
		line readyLine
		err  error
	}
	results := make(chan result, 1)
	go func() {
		var line readyLine
		data, err := bufio.NewReader(stdout).ReadBytes('\n')
		switch {
		case len(data) == 0 && err != nil:
			err = errors.New("the certificate proxy exited before it was ready")
		default:
			err = json.Unmarshal(data, &line)
		}
		results <- result{line: line, err: err}
	}()
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case r := <-results:
		switch {
		case r.err != nil:
			return Ready{}, r.err
		case r.line.Error != "":
			return Ready{}, fmt.Errorf("the certificate proxy did not start: %s", r.line.Error)
		}
		return r.line.Ready, nil
	case <-timer.C:
		return Ready{}, fmt.Errorf("the certificate proxy was not ready within %s", readyTimeout)
	case <-ctx.Done():
		return Ready{}, ctx.Err()
	}
}

// Events returns the events the proxy at handleURL recorded after seq. An
// error means the proxy no longer answers.
func Events(ctx context.Context, handleURL string, after int64) ([]Event, error) {
	query := url.Values{afterParam: {strconv.FormatInt(after, 10)}}
	var events []Event
	if err := getControl(ctx, handleURL+"/"+eventsPath+"?"+query.Encode(), &events); err != nil {
		return nil, fmt.Errorf("read certificate proxy events: %w", err)
	}
	return events, nil
}

// PendingConnections returns the connections the proxy at handleURL has not
// yet heard a site answer on, the longest waiting first. An error means the
// proxy no longer answers.
func PendingConnections(ctx context.Context, handleURL string) ([]Pending, error) {
	var pending []Pending
	if err := getControl(ctx, handleURL+"/"+pendingPath, &pending); err != nil {
		return nil, fmt.Errorf("read certificate proxy connections: %w", err)
	}
	return pending, nil
}

// Verified reports whether the proxy at handleURL verified the server
// certificate of host on a route it still carries. An error means the proxy
// no longer answers.
func Verified(ctx context.Context, handleURL, host string) (bool, error) {
	query := url.Values{hostParam: {host}}
	var verified bool
	if err := getControl(ctx, handleURL+"/"+verifiedPath+"?"+query.Encode(), &verified); err != nil {
		return false, fmt.Errorf("read certificate proxy verification: %w", err)
	}
	return verified, nil
}

// getControl reads the JSON a control path of the proxy answers into v.
func getControl(ctx context.Context, address string, v any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the certificate proxy answered %s", response.Status)
	}
	return json.NewDecoder(response.Body).Decode(v)
}

// controlURL returns the base of the control paths of the proxy on port.
func controlURL(port int, token string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/%s", port, token)
}

// urlPort returns the port of rawURL, or zero when it names none.
func urlPort(rawURL string) int {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}
