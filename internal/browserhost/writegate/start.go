// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

// configVersion versions the configuration a starting process hands the
// gate, which may run an executable replaced in the meantime.
const configVersion = 1

// readyTimeout bounds the wait for a starting gate to arm.
const readyTimeout = 30 * time.Second

type wireConfig struct {
	Version int `json:"version"`
	Config
}

// readyLine is the one line the gate process writes: Ready, or why it did
// not start.
type readyLine struct {
	Ready
	Error string `json:"error,omitempty"`
}

// Handle locates a running gate process.
type Handle struct {
	// URL is the base of the gate's control paths.
	URL string `json:"url"`
	PID int    `json:"pid"`
	// StartedAt is the process's start time in Unix milliseconds, which
	// tells it apart from a later process reusing PID.
	StartedAt int64 `json:"started_at"`
}

// Alive reports whether the gate process still runs.
func (h Handle) Alive() bool {
	if !procutil.IsAlive(h.PID) {
		return false
	}
	matched, _, ok := procutil.MatchesStartTime(h.PID, h.StartedAt)
	return !ok || matched
}

// Kill ends the gate process if it is still the one h records. End it only
// after its browser: a request the gate paused would be sent the moment
// the gate is gone.
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
		return fmt.Errorf("end the write gate: %w", err)
	}
	return nil
}

// Start runs executable's browser write-gate command with cfg and returns
// once the gate decides every request of the browser at cfg.CDPURL. Start
// it before the browser loads any page. The gate outlives the caller and
// exits once the browser is gone.
func Start(ctx context.Context, executable string, cfg Config) (*Handle, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(wireConfig{Version: configVersion, Config: cfg})
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, "browser", "write-gate") //nolint:gosec // starting this executable's own gate is the purpose
	// The gate outlives the caller, so it does not keep the caller's
	// directory in use.
	cmd.Dir = filepath.Dir(executable)
	// On Windows the gate stays in the caller's job, and ends with the
	// browser started there.
	cmd.SysProcAttr = cmdutil.DetachAttr(false)
	cmd.Stdin = bytes.NewReader(input)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the write gate: %w", err)
	}
	startedAt, _ := procutil.StartTime(cmd.Process.Pid)
	ready, err := readReady(ctx, stdout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	return &Handle{URL: fmt.Sprintf("http://127.0.0.1:%d/%s", ready.Port, cfg.Token), PID: cmd.Process.Pid, StartedAt: startedAt}, nil
}

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
			err = errors.New("the write gate exited before it was ready")
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
			return Ready{}, fmt.Errorf("the write gate did not start: %s", r.line.Error)
		}
		return r.line.Ready, nil
	case <-timer.C:
		return Ready{}, fmt.Errorf("the write gate was not ready within %s", readyTimeout)
	case <-ctx.Done():
		return Ready{}, ctx.Err()
	}
}

// Main is the write-gate command. It reads a Config as JSON from stdin,
// writes one line to stdout once the gate decides the browser's requests,
// closes stdout, and serves until the browser is gone or the process is
// told to stop.
func Main(ctx context.Context, stdin io.Reader, stdout io.WriteCloser) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	written := false
	write := func(line readyLine) {
		if written {
			return
		}
		written = true
		data, _ := json.Marshal(line)
		_, _ = stdout.Write(append(data, '\n'))
		_ = stdout.Close()
	}
	var wire wireConfig
	if err := json.NewDecoder(stdin).Decode(&wire); err != nil {
		err = fmt.Errorf("read the write gate config: %w", err)
		write(readyLine{Error: err.Error()})
		return err
	}
	if wire.Version != configVersion {
		err := fmt.Errorf("write gate config version %d is not %d; the executable was likely replaced by another version", wire.Version, configVersion)
		write(readyLine{Error: err.Error()})
		return err
	}
	err := Serve(ctx, wire.Config, func(ready Ready) { write(readyLine{Ready: ready}) })
	if err != nil {
		write(readyLine{Error: err.Error()})
	}
	return err
}

// Events returns the events the gate at handleURL recorded after seq. An
// error means the gate no longer answers.
func Events(ctx context.Context, handleURL string, after int64) ([]Event, error) {
	query := url.Values{afterParam: {strconv.FormatInt(after, 10)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, handleURL+"/"+eventsPath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var events []Event
	if err := do(request, &events); err != nil {
		return nil, fmt.Errorf("read the write gate's events: %w", err)
	}
	return events, nil
}

// SetPerson tells the gate at handleURL whether a person holds the browser,
// whose writes it then sends.
func SetPerson(ctx context.Context, handleURL string, on bool) error {
	query := url.Values{personParam: {strconv.FormatBool(on)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, handleURL+"/"+personPath+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	if err := do(request, nil); err != nil {
		return fmt.Errorf("tell the write gate who holds the browser: %w", err)
	}
	return nil
}

// Begin tells the gate at handleURL which writes the operation starting
// may send, for at most lease. In record mode every other write is
// blocked, and so is every write once End is called or the lease ends.
func Begin(ctx context.Context, handleURL string, signatures []string, lease time.Duration) error {
	data, err := json.Marshal(allowance{Signatures: signatures, Seconds: max(int(lease.Seconds()), 1)})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, handleURL+"/"+beginPath, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := do(request, nil); err != nil {
		return fmt.Errorf("tell the write gate what the operation may send: %w", err)
	}
	return nil
}

// End tells the gate at handleURL that the operation that began ended.
func End(ctx context.Context, handleURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, handleURL+"/"+endPath, nil)
	if err != nil {
		return err
	}
	if err := do(request, nil); err != nil {
		return fmt.Errorf("tell the write gate the operation ended: %w", err)
	}
	return nil
}

func do(request *http.Request, v any) error {
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("the write gate answered %s", response.Status)
	}
	if v == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(v)
}
