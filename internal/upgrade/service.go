// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// Background service definitions written by scripts/installer.sh and
// scripts/installer.ps1.
const (
	launchdServiceLabel  = "local.dagu.server"
	systemdServiceUnit   = "dagu.service"
	systemdSystemUnitDir = "/etc/systemd/system"
	winswServiceConfig   = "dagu-service.xml"
	winswServiceName     = "Dagu"
)

// Commands that add a systemd drop-in limiting a stop of the service to the
// Dagu process itself, then reload systemd.
const (
	systemKeepRunsCommand = `sudo sh -c 'mkdir -p /etc/systemd/system/dagu.service.d && printf "[Service]\nKillMode=process\n" > /etc/systemd/system/dagu.service.d/keep-runs.conf && systemctl daemon-reload'`
	userKeepRunsCommand   = `mkdir -p "$HOME/.config/systemd/user/dagu.service.d" && printf '[Service]\nKillMode=process\n' > "$HOME/.config/systemd/user/dagu.service.d/keep-runs.conf" && systemctl --user daemon-reload`
)

// Service is the background service the Dagu installer set up on this machine.
type Service struct {
	// RestartCommand is the shell command that restarts the service.
	RestartCommand string
	// StopsRuns reports that restarting the service also ends runs in progress.
	StopsRuns bool
	// KeepRunsCommand is the shell command that reconfigures the service so a
	// restart leaves runs in progress running. It is empty when StopsRuns is
	// false or when the service manager offers no such setting.
	KeepRunsCommand string
}

// DetectService returns the background service the Dagu installer set up on
// this machine, or nil when none is found.
func DetectService() *Service {
	var execDir string
	if execPath, err := GetExecutablePath(); err == nil {
		execDir = filepath.Dir(execPath)
	}
	homeDir, _ := os.UserHomeDir()
	return detectService(runtime.GOOS, execDir, homeDir, systemdSystemUnitDir)
}

func detectService(goos, execDir, homeDir, systemUnitDir string) *Service {
	switch goos {
	case "darwin":
		// launchd ends only the job's process group, and each run starts its
		// own, so runs outlive a restart.
		if homeDir != "" && fileutil.IsFile(filepath.Join(homeDir, "Library", "LaunchAgents", launchdServiceLabel+".plist")) {
			return &Service{RestartCommand: "launchctl kickstart -k gui/$(id -u)/" + launchdServiceLabel}
		}
	case "linux":
		if unit := filepath.Join(systemUnitDir, systemdServiceUnit); fileutil.IsFile(unit) {
			return systemdService(unit, "sudo systemctl restart dagu", systemKeepRunsCommand)
		}
		if homeDir != "" {
			if unit := filepath.Join(homeDir, ".config", "systemd", "user", systemdServiceUnit); fileutil.IsFile(unit) {
				return systemdService(unit, "systemctl --user restart dagu", userKeepRunsCommand)
			}
		}
	case "windows":
		// The installer keeps the service wrapper next to the binary it runs.
		// WinSW stops the whole process tree of the service, runs included.
		if execDir != "" && fileutil.IsFile(filepath.Join(execDir, winswServiceConfig)) {
			return &Service{RestartCommand: "Restart-Service " + winswServiceName, StopsRuns: true}
		}
	}
	return nil
}

// systemdService describes the service of the systemd unit at unitPath.
func systemdService(unitPath, restartCommand, keepRunsCommand string) *Service {
	service := &Service{RestartCommand: restartCommand}
	if systemdStopsRuns(unitPath) {
		service.StopsRuns = true
		service.KeepRunsCommand = keepRunsCommand
	}
	return service
}

// systemdStopsRuns reports whether stopping the unit at unitPath also ends the
// runs it started. systemd applies the drop-ins next to the unit in file name
// order, and its default KillMode ends every process of the unit.
func systemdStopsRuns(unitPath string) bool {
	files := []string{unitPath}
	dropInDir := unitPath + ".d"
	if entries, err := os.ReadDir(dropInDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".conf") {
				files = append(files, filepath.Join(dropInDir, entry.Name()))
			}
		}
	}

	var killMode string
	for _, file := range files {
		if mode, ok := readKillMode(file); ok {
			killMode = mode
		}
	}
	return killMode != "process" && killMode != "none"
}

// readKillMode returns the last KillMode assignment in the [Service] section
// of a unit file. An empty value resets the setting to its default.
func readKillMode(path string) (string, bool) {
	f, err := os.Open(path) //nolint:gosec // path names a systemd unit file of the Dagu service
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	var (
		mode    string
		found   bool
		section string
	)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "["):
			section = line
		case section == "[Service]":
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "KillMode" {
				mode, found = strings.TrimSpace(value), true
			}
		}
	}
	return mode, found
}

// Restart restarts the service. The command shares the caller's terminal, so
// sudo can ask for a password.
func (s *Service) Restart(ctx context.Context) error {
	return runShell(ctx, s.RestartCommand)
}

// KeepRuns runs KeepRunsCommand. The command shares the caller's terminal, so
// sudo can ask for a password.
func (s *Service) KeepRuns(ctx context.Context) error {
	if s.KeepRunsCommand == "" {
		return fmt.Errorf("the service offers no setting that keeps runs across restarts")
	}
	return runShell(ctx, s.KeepRunsCommand)
}

func runShell(ctx context.Context, command string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command) //nolint:gosec // command is a fixed service command
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command) //nolint:gosec // command is a fixed service command
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}
